package localauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"custodia/server/internal/lab"
)

const origin = "http://127.0.0.1:8443"
const adminPassword = "correct horse battery staple"
const readerPassword = "temporary reader passphrase"

func init() {
	// Production cost is covered by TestDefaultParamsRoundTrip; the HTTP
	// lifecycle uses a cheap but valid argon2id cost to stay fast under -race.
	hashParams = Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}
}

type harness struct {
	t     *testing.T
	store *lab.Store
	h     http.Handler
	now   time.Time
	ip    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	s, err := lab.Open(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, start); err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword(ctx, adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateLocalAdministrator(ctx, "Ada.Admin", "Ada Admin", hash, []string{lab.IdentityConnection, lab.NetskopeConnection}, false, start); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateLocalAdministrator(ctx, "second.admin", "Second", hash, nil, false, start); err == nil {
		t.Fatal("first local administrator replaced")
	}
	x := &harness{t: t, store: s, now: start, ip: "127.0.0.1:50000"}
	x.h, err = NewHandler(s, Config{PublicOrigin: origin}, func() time.Time { return x.now }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("compiled frontend")) }))
	if err != nil {
		t.Fatal(err)
	}
	return x
}

type session struct{ cookie, csrf string }

func (x *harness) do(method, path, body string, s *session, headers map[string]string) *httptest.ResponseRecorder {
	x.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = x.ip
	if method != "GET" {
		r.Header.Set("Origin", origin)
	}
	if s != nil {
		r.AddCookie(&http.Cookie{Name: "custodia_session", Value: s.cookie})
		if s.csrf != "" && method != "GET" {
			r.Header.Set(CSRFHeader, s.csrf)
		}
	}
	for name, value := range headers {
		if value == "" {
			r.Header.Del(name)
		} else {
			r.Header.Set(name, value)
		}
	}
	w := httptest.NewRecorder()
	x.h.ServeHTTP(w, r)
	return w
}

func (x *harness) login(username, password string) (*httptest.ResponseRecorder, *session) {
	x.t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	w := x.do("POST", "/api/auth/login", string(body), nil, nil)
	if w.Code != 200 {
		return w, nil
	}
	var cookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == "custodia_session" {
			cookie = c.Value
		}
	}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if cookie == "" || out.CSRF == "" {
		x.t.Fatal("login without cookie or CSRF token")
	}
	return w, &session{cookie: cookie, csrf: out.CSRF}
}

func TestLocalLoginLifecycle(t *testing.T) {
	x := newHarness(t)
	status := x.do("GET", "/api/auth/status", "", nil, nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"authenticated":false`) || !strings.Contains(status.Body.String(), `"mode":"local"`) {
		t.Fatal("anonymous status", status.Code, status.Body.String())
	}
	if w := x.do("GET", "/api/connections", "", nil, nil); w.Code != 401 {
		t.Fatal("API served without session", w.Code)
	}
	if w := x.do("GET", "/", "", nil, nil); w.Code != 200 || w.Body.String() != "compiled frontend" {
		t.Fatal("interface not served for the login", w.Code)
	}
	wrong, _ := x.login("ada.admin", "not the right password")
	unknown, _ := x.login("nobody.here", "not the right password")
	if wrong.Code != 401 || unknown.Code != 401 || wrong.Body.String() != unknown.Body.String() {
		t.Fatal("login failure reveals account existence", wrong.Code, wrong.Body.String(), unknown.Code, unknown.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"username": "ada.admin", "password": adminPassword})
	if w := x.do("POST", "/api/auth/login", string(body), nil, map[string]string{"Origin": ""}); w.Code != 403 {
		t.Fatal("login without Origin accepted", w.Code)
	}
	if w := x.do("POST", "/api/auth/login", string(body), nil, map[string]string{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Fatal("cross-origin login accepted", w.Code)
	}
	if w := x.do("POST", "/api/auth/login", `{"username":"ada.admin","password":"x","email":"a"}`, nil, nil); w.Code != 400 {
		t.Fatal("unknown login field accepted", w.Code)
	}
	ok, admin := x.login("ADA.admin", adminPassword)
	if admin == nil {
		t.Fatal("valid login rejected", ok.Code, ok.Body.String())
	}
	cookie := ok.Header().Get("Set-Cookie")
	for _, attribute := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(cookie, attribute) {
			t.Fatal("session cookie lacks", attribute, cookie)
		}
	}
	if strings.Contains(cookie, "Max-Age") || strings.Contains(cookie, "Domain") || strings.Contains(ok.Body.String(), adminPassword) {
		t.Fatal("unexpected cookie or response content", cookie)
	}
	status = x.do("GET", "/api/auth/status", "", admin, nil)
	for _, expected := range []string{`"authenticated":true`, `"mode":"local"`, "access:local_users_manage", "installation:manage", `"csrf_token":"` + admin.csrf + `"`, `"must_change_password":false`} {
		if !strings.Contains(status.Body.String(), expected) {
			t.Fatal("status lacks", expected, status.Body.String())
		}
	}
	// CSRF: same-origin header and session-bound token are both required.
	prefs := `{"visible_workspaces":["person"],"hidden_workspaces":[],"recent_workspaces":[]}`
	if w := x.do("PUT", "/api/workspaces/preferences", prefs, &session{cookie: admin.cookie}, nil); w.Code != 403 {
		t.Fatal("mutation without CSRF token accepted", w.Code)
	}
	if w := x.do("PUT", "/api/workspaces/preferences", prefs, &session{cookie: admin.cookie, csrf: "forged"}, nil); w.Code != 403 {
		t.Fatal("forged CSRF token accepted", w.Code)
	}
	if w := x.do("PUT", "/api/workspaces/preferences", prefs, admin, map[string]string{"Origin": ""}); w.Code != 403 {
		t.Fatal("mutation without Origin accepted", w.Code)
	}
	if w := x.do("PUT", "/api/workspaces/preferences", prefs, admin, nil); w.Code != 200 {
		t.Fatal("valid mutation rejected", w.Code, w.Body.String())
	}
	if w := x.do("POST", "/api/auth/logout", "", &session{cookie: admin.cookie}, nil); w.Code != 403 {
		t.Fatal("logout without CSRF accepted", w.Code)
	}
	logout := x.do("POST", "/api/auth/logout", "", admin, nil)
	if logout.Code != 200 || strings.Contains(logout.Body.String(), "gateway_logout") || !strings.Contains(logout.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatal("logout contract", logout.Code, logout.Body.String(), logout.Header().Get("Set-Cookie"))
	}
	if w := x.do("GET", "/api/connections", "", admin, nil); w.Code != 401 {
		t.Fatal("logged out session still valid", w.Code)
	}
}

func TestLocalModeIgnoresOtherCredentials(t *testing.T) {
	x := newHarness(t)
	for _, headers := range []map[string]string{
		{"X-Ozal-Assertion": "eyJhbGciOiJFZERTQSJ9.e30.c2ln"},
		{"Authorization": "Bearer lab-token"},
		{"X-Auth-Request-User": "ada.admin", "X-Forwarded-User": "ada.admin", "X-Forwarded-Email": "ada@acme.example"},
	} {
		if w := x.do("GET", "/api/connections", "", nil, headers); w.Code != 401 {
			t.Fatal("local mode accepted a foreign credential", headers, w.Code)
		}
		if w := x.do("GET", "/api/auth/status", "", nil, headers); !strings.Contains(w.Body.String(), `"authenticated":false`) {
			t.Fatal("status authenticated a foreign credential", headers)
		}
	}
	// A cookie with the right name but an unknown token is not a session.
	if w := x.do("GET", "/api/connections", "", &session{cookie: strings.Repeat("A", 43)}, nil); w.Code != 401 {
		t.Fatal("unknown session token accepted", w.Code)
	}
	// Ozal principals live under their own issuer and cannot sign in locally.
	if err := x.store.BootstrapAdministrator(context.Background(), "https://login.acme.example", "ada.admin", nil); err != nil {
		t.Fatal(err)
	}
	if w, _ := x.login("ada.admin", adminPassword); w.Code != 200 {
		t.Fatal("Ozal principal interfered with the local account", w.Code)
	}
}

func TestSessionExpiry(t *testing.T) {
	x := newHarness(t)
	start := x.now
	_, idle := x.login("ada.admin", adminPassword)
	x.now = start.Add(IdleTimeout - time.Minute)
	if w := x.do("GET", "/api/connections", "", idle, nil); w.Code != 200 {
		t.Fatal("active session expired early", w.Code)
	}
	x.now = x.now.Add(IdleTimeout)
	if w := x.do("GET", "/api/connections", "", idle, nil); w.Code != 401 {
		t.Fatal("idle session accepted", w.Code)
	}
	_, absolute := x.login("ada.admin", adminPassword)
	loginAt := x.now
	for x.now.Before(loginAt.Add(AbsoluteLifetime - 20*time.Minute)) {
		x.now = x.now.Add(20 * time.Minute)
		if w := x.do("GET", "/api/connections", "", absolute, nil); w.Code != 200 {
			t.Fatal("kept-alive session expired before absolute lifetime", x.now.Sub(loginAt), w.Code)
		}
	}
	x.now = loginAt.Add(AbsoluteLifetime)
	if w := x.do("GET", "/api/connections", "", absolute, nil); w.Code != 401 {
		t.Fatal("session outlived its absolute lifetime", w.Code)
	}
}

func TestLoginThrottling(t *testing.T) {
	x := newHarness(t)
	for i := 0; i < UserAttempts.Limit; i++ {
		if w, _ := x.login("ada.admin", "wrong password number"); w.Code != 401 {
			t.Fatal("unexpected failure code", i, w.Code)
		}
	}
	if w, _ := x.login("ada.admin", adminPassword); w.Code != 429 {
		t.Fatal("locked user verified a password", w.Code)
	}
	for i := 0; i < UserAttempts.Limit; i++ {
		x.login("ghost.user", "wrong password number")
	}
	locked, _ := x.login("ghost.user", "wrong password number")
	if locked.Code != 429 {
		t.Fatal("unknown user lockout differs and reveals existence", locked.Code)
	}
	x.now = x.now.Add(UserAttempts.Lock)
	if w, s := x.login("ada.admin", adminPassword); w.Code != 200 || s == nil {
		t.Fatal("lock did not expire", w.Code)
	}
	var audited int
	if err := x.store.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='local_login_locked'").Scan(&audited); err != nil || audited < 2 {
		t.Fatal("lockouts not audited", audited, err)
	}
	// Per-IP limit across many usernames from one address.
	x.ip = "192.0.2.10:4000"
	for i := 0; i < IPAttempts.Limit; i++ {
		x.login(fmt.Sprintf("spray%02d", i), "wrong password number")
	}
	if w, _ := x.login("ada.admin", adminPassword); w.Code != 429 {
		t.Fatal("IP lockout not enforced", w.Code)
	}
	x.ip = "192.0.2.11:4000"
	if w, _ := x.login("ada.admin", adminPassword); w.Code != 200 {
		t.Fatal("other address affected by IP lock", w.Code)
	}
}

func TestLocalUserAdministration(t *testing.T) {
	x := newHarness(t)
	_, admin := x.login("ada.admin", adminPassword)
	create := func(body string) *httptest.ResponseRecorder {
		return x.do("POST", "/api/access/users", body, admin, nil)
	}
	if w := create(`{"username":"bruno","display_name":"Bruno","password":"short","connections":[]}`); w.Code != 400 {
		t.Fatal("weak temporary password accepted", w.Code)
	}
	if w := create(`{"username":"bruno","display_name":"Bruno","password":"` + readerPassword + `","connections":["missing"]}`); w.Code != 403 {
		t.Fatal("grant outside administrator scope accepted", w.Code)
	}
	w := create(`{"username":"Bruno","display_name":"Bruno Reader","password":"` + readerPassword + `","connections":["acme-identity"]}`)
	if w.Code != 201 || strings.Contains(w.Body.String(), readerPassword) {
		t.Fatal("reader not created", w.Code, w.Body.String())
	}
	if w := create(`{"username":"bruno","display_name":"Bruno","password":"` + readerPassword + `","connections":[]}`); w.Code != 409 {
		t.Fatal("duplicate user accepted", w.Code)
	}
	list := x.do("GET", "/api/access/users", "", admin, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"username":"bruno"`) || strings.Contains(list.Body.String(), "argon2") || strings.Contains(list.Body.String(), readerPassword) {
		t.Fatal("user list contract", list.Code, list.Body.String())
	}
	// Temporary password: only status, password change and logout.
	_, reader := x.login("bruno", readerPassword)
	if w := x.do("GET", "/api/connections", "", reader, nil); w.Code != 403 {
		t.Fatal("temporary password reached the API", w.Code)
	}
	if s := x.do("GET", "/api/auth/status", "", reader, nil); !strings.Contains(s.Body.String(), `"must_change_password":true`) || strings.Contains(s.Body.String(), "access:local_users_manage") {
		t.Fatal("reader status", s.Body.String())
	}
	if w := x.do("GET", "/api/access/users", "", reader, nil); w.Code != 403 {
		t.Fatal("reader reached administration", w.Code)
	}
	if w := x.do("POST", "/api/auth/password", `{"current_password":"wrong wrong wrong","new_password":"a brand new reader passphrase"}`, reader, nil); w.Code != 400 {
		t.Fatal("wrong current password accepted", w.Code)
	}
	if w := x.do("POST", "/api/auth/password", `{"current_password":"`+readerPassword+`","new_password":"`+readerPassword+`"}`, reader, nil); w.Code != 400 {
		t.Fatal("password reuse accepted", w.Code)
	}
	_, second := x.login("bruno", readerPassword)
	changed := x.do("POST", "/api/auth/password", `{"current_password":"`+readerPassword+`","new_password":"a brand new reader passphrase"}`, reader, nil)
	if changed.Code != 200 || strings.Contains(changed.Body.String(), "passphrase") {
		t.Fatal("password change", changed.Code, changed.Body.String())
	}
	if w := x.do("GET", "/api/auth/status", "", second, nil); !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatal("other session survived password change")
	}
	if w := x.do("GET", "/api/connections", "", reader, nil); w.Code != 401 {
		t.Fatal("pre-change session survived", w.Code)
	}
	var rotated session
	for _, c := range changed.Result().Cookies() {
		rotated.cookie = c.Value
	}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(changed.Body.Bytes(), &out)
	rotated.csrf = out.CSRF
	if w := x.do("GET", "/api/connections", "", &rotated, nil); w.Code != 200 || strings.Contains(w.Body.String(), "acme-netskope") {
		t.Fatal("rotated session or grant scope", w.Code, w.Body.String())
	}
	// Disable revokes sessions; a disabled account with the right password gets 403.
	if w := x.do("POST", "/api/access/users/disable", `{"username":"bruno"}`, &session{cookie: admin.cookie}, nil); w.Code != 403 {
		t.Fatal("admin mutation without CSRF accepted", w.Code)
	}
	if w := x.do("POST", "/api/access/users/disable", `{"username":"bruno"}`, admin, nil); w.Code != 204 {
		t.Fatal("disable", w.Code, w.Body.String())
	}
	if w := x.do("GET", "/api/connections", "", &rotated, nil); w.Code != 401 {
		t.Fatal("disabled user's session survived", w.Code)
	}
	if w, _ := x.login("bruno", "a brand new reader passphrase"); w.Code != 403 {
		t.Fatal("disabled user signed in", w.Code)
	}
	if w := x.do("POST", "/api/access/users/enable", `{"username":"bruno"}`, admin, nil); w.Code != 204 {
		t.Fatal("enable", w.Code)
	}
	_, again := x.login("bruno", "a brand new reader passphrase")
	if again == nil {
		t.Fatal("re-enabled user rejected")
	}
	if w := x.do("POST", "/api/access/users/revoke-sessions", `{"username":"bruno"}`, admin, nil); w.Code != 204 {
		t.Fatal("revoke sessions", w.Code)
	}
	if w := x.do("GET", "/api/connections", "", again, nil); w.Code != 401 {
		t.Fatal("revoked session accepted", w.Code)
	}
	for _, target := range []string{"ada.admin", "nobody"} {
		if w := x.do("POST", "/api/access/users/disable", `{"username":"`+target+`"}`, admin, nil); w.Code != 403 {
			t.Fatal("disabled an administrator or unknown account", target, w.Code)
		}
	}
	// Secrets never reach the audit log; hashes are argon2id.
	rows, err := x.store.DB.Query("SELECT action,after_json FROM lab_audit")
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for rows.Next() {
		var action, after string
		rows.Scan(&action, &after)
		actions[action] = true
		for _, secret := range []string{adminPassword, readerPassword, "a brand new reader passphrase", "argon2", "short"} {
			if strings.Contains(after, secret) {
				t.Fatal("audit contains a secret", action, after)
			}
		}
	}
	rows.Close()
	for _, action := range []string{"create_local_administrator", "local_login", "create_local_user", "change_local_password", "disable_local_user", "enable_local_user", "revoke_local_sessions"} {
		if !actions[action] {
			t.Fatal("missing audit action", action)
		}
	}
	var hash string
	if err = x.store.DB.QueryRow("SELECT password_hash FROM local_accounts a JOIN lab_principals p ON p.id=a.principal_id WHERE p.subject='bruno'").Scan(&hash); err != nil || !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatal("stored hash format", hash, err)
	}
}

func TestHostPasswordReset(t *testing.T) {
	x := newHarness(t)
	_, admin := x.login("ada.admin", adminPassword)
	for i := 0; i < UserAttempts.Limit; i++ {
		x.login("ada.admin", "wrong password number")
	}
	hash, _ := HashPassword(context.Background(), "recovered temporary passphrase")
	if err := x.store.ResetLocalPassword(context.Background(), "ada.admin", hash, x.now); err != nil {
		t.Fatal(err)
	}
	if err := x.store.ResetLocalPassword(context.Background(), "nobody", hash, x.now); err == nil {
		t.Fatal("reset invented an account")
	}
	if w := x.do("GET", "/api/connections", "", admin, nil); w.Code != 401 {
		t.Fatal("reset kept old session", w.Code)
	}
	w, recovered := x.login("ada.admin", "recovered temporary passphrase")
	if recovered == nil || !strings.Contains(w.Body.String(), `"must_change_password":true`) {
		t.Fatal("reset did not clear lockout or mark temporary", w.Code, w.Body.String())
	}
}

func TestChangedPasswordCannotCreateSessionFromInFlightLogin(t *testing.T) {
	x := newHarness(t)
	ctx := context.Background()
	credential, err := x.store.LocalCredential(ctx, "ada.admin")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := HashPassword(ctx, "replacement password for the login race")
	if err != nil {
		t.Fatal(err)
	}
	oldToken := TokenHash("in-flight-old-password")
	if err = x.store.ChangeLocalPassword(ctx, credential.Principal, credential.PasswordHash, replacement, TokenHash("current-password-session"), x.now, x.now.Add(AbsoluteLifetime)); err != nil {
		t.Fatal(err)
	}
	if err = x.store.StartLocalSession(ctx, credential.Principal, credential.PasswordHash, oldToken, x.now, x.now.Add(AbsoluteLifetime)); !errors.Is(err, lab.ErrLocalConflict) {
		t.Fatalf("old credential created a session: %v", err)
	}
	var sessions int
	if err = x.store.DB.QueryRow("SELECT count(*) FROM local_sessions WHERE token_hash=?", oldToken).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("old session row exists", sessions, err)
	}
	if w, _ := x.login("ada.admin", adminPassword); w.Code != 401 {
		t.Fatal("old password logged in after the change", w.Code)
	}
}

func TestLoginReservationsBlockBeforePasswordHashing(t *testing.T) {
	x := newHarness(t)
	ctx := context.Background()
	keys := []lab.LoginAttemptKey{
		{Key: "ip:127.0.0.1", Policy: IPAttempts},
		{Key: "user:ada.admin", Policy: UserAttempts},
	}
	reservations := []string{}
	for i := 0; i < UserAttempts.Limit; i++ {
		reservation, locked, err := x.store.ReserveLoginAttempt(ctx, keys, x.now)
		if err != nil || locked {
			t.Fatal("could not reserve attempt", i, locked, err)
		}
		reservations = append(reservations, reservation)
	}
	if w, _ := x.login("ada.admin", adminPassword); w.Code != 429 {
		t.Fatal("login verified a password past the reservation limit", w.Code)
	}
	for _, reservation := range reservations {
		if err := x.store.ReleaseLoginAttempt(ctx, reservation); err != nil {
			t.Fatal(err)
		}
	}
	if w, session := x.login("ada.admin", adminPassword); w.Code != 200 || session == nil {
		t.Fatal("released reservations did not admit the login", w.Code, w.Body.String())
	}
}

func TestConsoleAndBackupDownloadSecurityHeaders(t *testing.T) {
	x := newHarness(t)
	_, admin := x.login("ada.admin", adminPassword)
	data := t.TempDir()
	backups := filepath.Join(data, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte("synthetic backup")
	if err := os.WriteFile(filepath.Join(backups, "manual.db"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	x.h.(*handler).config.DataDir = data
	for _, path := range []string{"/", "/api/system/backups/manual.db"} {
		w := x.do("GET", path, "", admin, nil)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		for _, header := range []string{"Content-Security-Policy", "X-Frame-Options", "Referrer-Policy"} {
			if w.Header().Get(header) == "" {
				t.Fatal(path, "missing", header)
			}
		}
		if path != "/" && w.Body.String() != string(body) {
			t.Fatal("backup body", w.Body.String())
		}
	}
	var audited int
	if err := x.store.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='system_backup_downloaded' AND CAST(after_json AS TEXT) LIKE '%manual.db%'").Scan(&audited); err != nil || audited != 1 {
		t.Fatal("backup download not audited", audited, err)
	}
}

func TestPasswordHashing(t *testing.T) {
	ctx := context.Background()
	saved := hashParams
	defer func() { hashParams = saved }()
	hashParams = DefaultParams
	hash, err := HashPassword(ctx, adminPassword)
	if err != nil || !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatal("production parameters", hash, err)
	}
	if ok, _ := VerifyPassword(ctx, hash, adminPassword); !ok {
		t.Fatal("valid password rejected")
	}
	if ok, _ := VerifyPassword(ctx, hash, adminPassword+"x"); ok {
		t.Fatal("wrong password accepted")
	}
	for _, tampered := range []string{"", "plain", strings.Replace(hash, "m=65536", "m=4194304", 1), strings.Replace(hash, "t=3", "t=99", 1), strings.Replace(hash, "argon2id", "argon2i", 1)} {
		if ok, _ := VerifyPassword(ctx, tampered, adminPassword); ok {
			t.Fatal("tampered hash accepted", tampered)
		}
	}
	for _, bad := range []string{"short", strings.Repeat("x", MaxPasswordLength+1), "ada.admin.ada.admin"} {
		username := "someone"
		if strings.HasPrefix(bad, "ada") {
			username = bad
		}
		if CheckPolicy(username, bad) == nil {
			t.Fatal("policy accepted", bad)
		}
	}
	if (Config{PublicOrigin: "https://custodia.acme.example"}).CookieName() != "__Host-custodia_session" {
		t.Fatal("HTTPS cookie prefix")
	}
	for _, invalid := range []string{"http://custodia.acme.example", "https://custodia.acme.example/app", "http://localhost:8000", ""} {
		if (Config{PublicOrigin: invalid}).Validate() == nil {
			t.Fatal("invalid origin accepted", invalid)
		}
	}
}
