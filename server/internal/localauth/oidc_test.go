package localauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"custodia/server/internal/lab"
)

type fakeCode struct {
	nonce       string
	challenge   string
	redirectURI string
}

type fakeIDP struct {
	server           *httptest.Server
	key              *rsa.PrivateKey
	kid              string
	mu               sync.Mutex
	codes            map[string]fakeCode
	nonceOverride    string
	audienceOverride string
	subject          string
	email            string
	name             string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIDP{key: key, kid: "test-key-1", codes: map[string]fakeCode{}, subject: "external-subject-1", email: "ada@acme.example", name: "Ada Externa"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                   idp.server.URL,
			"authorization_endpoint":   idp.server.URL + "/authorize",
			"token_endpoint":           idp.server.URL + "/token",
			"jwks_uri":                 idp.server.URL + "/keys",
			"response_types_supported": []string{"code"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		public := key.Public().(*rsa.PublicKey)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": idp.kid, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("response_type") != "code" || query.Get("client_id") != "custodia-test" {
			http.Error(w, "bad authorize", 400)
			return
		}
		if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
			http.Error(w, "PKCE required", 400)
			return
		}
		code := fmt.Sprintf("code-%d", time.Now().UnixNano())
		idp.mu.Lock()
		idp.codes[code] = fakeCode{nonce: query.Get("nonce"), challenge: query.Get("code_challenge"), redirectURI: query.Get("redirect_uri")}
		idp.mu.Unlock()
		target, _ := url.Parse(query.Get("redirect_uri"))
		values := target.Query()
		values.Set("code", code)
		values.Set("state", query.Get("state"))
		target.RawQuery = values.Encode()
		http.Redirect(w, r, target.String(), http.StatusSeeOther)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		code := r.Form.Get("code")
		clientID := r.Form.Get("client_id")
		if basicID, _, hasBasic := r.BasicAuth(); hasBasic && basicID != "" {
			clientID = basicID
		}
		idp.mu.Lock()
		entry, ok := idp.codes[code]
		idp.mu.Unlock()
		if !ok || r.Form.Get("grant_type") != "authorization_code" || clientID != "custodia-test" {
			http.Error(w, "bad code", 400)
			return
		}
		challenge := pkceChallenge(r.Form.Get("code_verifier"))
		if challenge != entry.challenge {
			http.Error(w, "bad verifier", 400)
			return
		}
		idp.mu.Lock()
		delete(idp.codes, code)
		idp.mu.Unlock()
		nonce := entry.nonce
		if idp.nonceOverride != "" {
			nonce = idp.nonceOverride
		}
		audience := "custodia-test"
		if idp.audienceOverride != "" {
			audience = idp.audienceOverride
		}
		raw, err := idp.sign(audience, nonce)
		if err != nil {
			http.Error(w, "sign", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "opaque", "token_type": "Bearer", "id_token": raw, "expires_in": 300})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (idp *fakeIDP) sign(audience, nonce string) (string, error) {
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": idp.kid})
	claims, _ := json.Marshal(map[string]any{
		"iss": idp.server.URL, "aud": []string{audience}, "sub": idp.subject,
		"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Add(-time.Second).Unix(),
		"nonce": nonce, "email": idp.email, "name": idp.name,
	})
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type oidcHarness struct {
	t      *testing.T
	store  *lab.Store
	h      http.Handler
	now    time.Time
	ip     string
	idp    *fakeIDP
	origin string
}

func newOIDCHarness(t *testing.T) *oidcHarness {
	t.Helper()
	ctx := context.Background()
	s, err := lab.Open(filepath.Join(t.TempDir(), "oidc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, now); err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword(ctx, adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateLocalAdministrator(ctx, "ada.admin", "Ada Admin", hash, []string{lab.IdentityConnection, lab.NetskopeConnection}, false, now); err != nil {
		t.Fatal(err)
	}
	x := &oidcHarness{t: t, store: s, now: now, ip: "127.0.0.1:50000", idp: newFakeIDP(t), origin: origin}
	x.h, err = NewOIDCHandler(s, Config{PublicOrigin: origin, Mode: "oidc"}, func() time.Time { return x.now }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("compiled frontend")) }))
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func (x *oidcHarness) do(method, path, body string, s *session, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
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
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	x.h.ServeHTTP(w, r)
	return w
}

func (x *oidcHarness) loginAdmin() *session {
	x.t.Helper()
	body := fmt.Sprintf(`{"username":"ada.admin","password":%q}`, adminPassword)
	w := x.do("POST", "/api/auth/login", body, nil, nil)
	if w.Code != 200 {
		x.t.Fatal(w.Code, w.Body.String())
	}
	return x.sessionFrom(w)
}

func (x *oidcHarness) sessionFrom(w *httptest.ResponseRecorder) *session {
	x.t.Helper()
	value := ""
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "custodia_session" {
			value = cookie.Value
		}
	}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if value == "" {
		x.t.Fatal("session cookie missing")
	}
	if out.CSRF == "" {
		out.CSRF = CSRFToken(value)
	}
	return &session{cookie: value, csrf: out.CSRF}
}

func (x *oidcHarness) adminPrincipal() lab.Principal {
	x.t.Helper()
	p, err := x.store.ResolvePrincipal(context.Background(), lab.LocalIssuer, "ada.admin")
	if err != nil {
		x.t.Fatal(err)
	}
	return p
}

func (x *oidcHarness) saveConfig(admin *session) {
	x.t.Helper()
	body := fmt.Sprintf(`{"issuer":%q,"client_id":"custodia-test","client_secret_ref":"","redirect_uri":%q,"scopes":"openid profile email","subject_claim":"sub","email_claim":"email","name_claim":"name"}`,
		x.idp.server.URL, origin+"/api/auth/oidc/callback")
	w := x.do("PUT", "/api/access/oidc", body, admin, nil)
	if w.Code != 200 {
		x.t.Fatal(w.Code, w.Body.String())
	}
}

func (x *oidcHarness) enable(admin *session) {
	x.t.Helper()
	if w := x.do("POST", "/api/access/oidc/test", "", admin, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		x.t.Fatal("test failed", w.Code, w.Body.String())
	}
	if w := x.do("POST", "/api/access/oidc/enable", "", admin, nil); w.Code != 200 {
		x.t.Fatal("enable failed", w.Code, w.Body.String())
	}
}

func (x *oidcHarness) start() (callback string, state *http.Cookie) {
	x.t.Helper()
	w := x.do("GET", "/api/auth/oidc/start", "", nil, nil)
	if w.Code != http.StatusFound {
		x.t.Fatal(w.Code, w.Body.String())
	}
	authURL, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		x.t.Fatal(err)
	}
	query := authURL.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("state") == "" || query.Get("nonce") == "" {
		x.t.Fatal("authorization URL lacks PKCE, state or nonce", authURL)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "custodia_oidc_state" {
			state = cookie
		}
	}
	if state == nil {
		x.t.Fatal("state cookie missing")
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(authURL.String())
	if err != nil {
		x.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		x.t.Fatal("authorize did not redirect", response.StatusCode)
	}
	return response.Header.Get("Location"), state
}

func callbackPath(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.RequestURI()
}

func (x *oidcHarness) callback(callback string, state *http.Cookie) *httptest.ResponseRecorder {
	x.t.Helper()
	return x.do("GET", callbackPath(x.t, callback), "", nil, nil, state)
}

func TestOIDCAdminConfigAndActivation(t *testing.T) {
	x := newOIDCHarness(t)
	admin := x.loginAdmin()

	w := x.do("GET", "/api/access/oidc", "", admin, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"config":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// A redirect URI outside the configured origin is rejected before saving.
	bad := fmt.Sprintf(`{"issuer":%q,"client_id":"custodia-test","redirect_uri":"http://127.0.0.1:8443/evil","scopes":"openid","subject_claim":"sub","email_claim":"email","name_claim":"name"}`, x.idp.server.URL)
	if w = x.do("PUT", "/api/access/oidc", bad, admin, nil); w.Code != 400 {
		t.Fatal("unexpected redirect URI accepted", w.Code, w.Body.String())
	}
	x.saveConfig(admin)
	// Activation never tests implicitly.
	if w = x.do("POST", "/api/access/oidc/enable", "", admin, nil); w.Code != 409 {
		t.Fatal("activation without test accepted", w.Code, w.Body.String())
	}
	w = x.do("POST", "/api/access/oidc/test", "", admin, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatal("test failed", w.Code, w.Body.String())
	}
	if w = x.do("POST", "/api/access/oidc/enable", "", admin, nil); w.Code != 200 {
		t.Fatal("enable failed", w.Code, w.Body.String())
	}
	// Saving again deactivates the provider until the new configuration is tested.
	x.saveConfig(admin)
	config, err := x.store.GetOIDCConfig(context.Background())
	if err != nil || config.Enabled || config.TestOK {
		t.Fatal("saved draft stayed active", config, err)
	}
	// A non-administrator local account cannot use the password form in OIDC mode.
	hash, _ := HashPassword(context.Background(), readerPassword)
	if err = x.store.CreateLocalUser(context.Background(), x.adminPrincipal(), "reader", "Reader", hash, nil, x.now); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"username":"reader","password":%q}`, readerPassword)
	if w = x.do("POST", "/api/auth/login", body, nil, nil); w.Code != 403 {
		t.Fatal("reader used the password path in OIDC mode", w.Code, w.Body.String())
	}
}

func TestOIDCRejectsPersistedLocalReaderSession(t *testing.T) {
	x := newOIDCHarness(t)
	admin := x.loginAdmin()
	ctx := context.Background()
	hash, err := HashPassword(ctx, readerPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err = x.store.CreateLocalUser(ctx, x.adminPrincipal(), "reader", "Reader", hash, nil, x.now); err != nil {
		t.Fatal(err)
	}
	reader, err := x.store.LocalCredential(ctx, "reader")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("R", 43)
	if err = x.store.StartLocalSession(ctx, reader.Principal, reader.PasswordHash, TokenHash(token), x.now, x.now.Add(AbsoluteLifetime)); err != nil {
		t.Fatal(err)
	}
	readerSession := &session{cookie: token}
	if w := x.do("GET", "/api/connections", "", readerSession, nil); w.Code != 401 {
		t.Fatalf("local reader cookie accepted in OIDC mode: %d", w.Code)
	}
	if w := x.do("GET", "/api/auth/status", "", readerSession, nil); !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatalf("local reader cookie reported authenticated in OIDC mode: %s", w.Body.String())
	}
	// The host-created local administrator remains the explicit break-glass
	// path while OIDC is active.
	if w := x.do("GET", "/api/connections", "", admin, nil); w.Code != 200 {
		t.Fatalf("break-glass administrator session rejected: %d", w.Code)
	}
	// Enabling the provider also removes the reader's stored session, so it
	// cannot come back if the mode is switched later.
	x.saveConfig(admin)
	x.enable(admin)
	var sessions int
	if err = x.store.DB.QueryRow("SELECT count(*) FROM local_sessions WHERE principal_id=?", reader.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("reader session survived OIDC activation", sessions, err)
	}
	if w := x.do("GET", "/api/connections", "", admin, nil); w.Code != 200 {
		t.Fatalf("enabling OIDC revoked the administrator session: %d", w.Code)
	}
}

func TestOIDCLoginLifecycle(t *testing.T) {
	x := newOIDCHarness(t)
	admin := x.loginAdmin()
	x.saveConfig(admin)
	x.enable(admin)

	// The unauthenticated status advertises the explicit provider link.
	w := x.do("GET", "/api/auth/status", "", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"oidc"`) || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatal(w.Code, w.Body.String())
	}

	// First valid identity is queued, never provisioned or granted.
	callback, state := x.start()
	w = x.callback(callback, state)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_pending") {
		t.Fatal("unknown identity was not queued", w.Code, w.Header().Get("Location"))
	}
	pending, err := x.store.ListOIDCPending(context.Background(), x.adminPrincipal())
	if err != nil || len(pending) != 1 || pending[0].Subject != x.idp.subject {
		t.Fatal("pending identity not listed", pending, err)
	}

	// Approval grants only a subset of the administrator's connections.
	approve := fmt.Sprintf(`{"issuer":%q,"subject":%q,"connections":[%q]}`, x.idp.server.URL, x.idp.subject, lab.IdentityConnection)
	if w = x.do("POST", "/api/access/oidc/pending/approve", approve, admin, nil); w.Code != 204 {
		t.Fatal("approval failed", w.Code, w.Body.String())
	}
	if w = x.do("POST", "/api/access/oidc/pending/approve", approve, admin, nil); w.Code == 204 {
		t.Fatal("approval was not idempotent-safe: missing pending accepted")
	}

	// Second pass creates a server-side session and redirects to the app.
	callback, state = x.start()
	w = x.callback(callback, state)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatal("callback did not sign in", w.Code, w.Header().Get("Location"))
	}
	user := x.sessionFrom(w)
	status := x.do("GET", "/api/auth/status", "", user, nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"authenticated":true`) || !strings.Contains(status.Body.String(), `"auth_source":"oidc"`) || !strings.Contains(status.Body.String(), `"mode":"oidc"`) {
		t.Fatal(status.Code, status.Body.String())
	}
	// Replaying the consumed state fails even with the old cookie.
	if w = x.callback(callback, state); !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_state") {
		t.Fatal("state replay accepted", w.Header().Get("Location"))
	}

	// Logout deletes the OIDC session.
	if w = x.do("POST", "/api/auth/logout", "", user, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"logged_out":true`) {
		t.Fatal("logout failed", w.Code, w.Body.String())
	}
	if status = x.do("GET", "/api/auth/status", "", user, nil); status.Code != 200 || !strings.Contains(status.Body.String(), `"authenticated":false`) {
		t.Fatal("session survived logout", status.Code, status.Body.String())
	}
}

func TestOIDCTokenAndStateRejections(t *testing.T) {
	x := newOIDCHarness(t)
	admin := x.loginAdmin()
	x.saveConfig(admin)
	x.enable(admin)

	// Wrong nonce never creates a session.
	x.idp.nonceOverride = "different-nonce"
	callback, state := x.start()
	w := x.callback(callback, state)
	if !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_token") {
		t.Fatal("wrong nonce accepted", w.Header().Get("Location"))
	}
	x.idp.nonceOverride = ""

	// Wrong audience is rejected by the library verifier.
	x.idp.audienceOverride = "another-client"
	callback, state = x.start()
	w = x.callback(callback, state)
	if !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_token") {
		t.Fatal("wrong audience accepted", w.Header().Get("Location"))
	}
	x.idp.audienceOverride = ""

	// A missing state cookie is rejected before the code is exchanged.
	callback, _ = x.start()
	w = x.callback(callback, &http.Cookie{Name: "custodia_oidc_state", Value: "not-the-state"})
	if !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_state") {
		t.Fatal("wrong state cookie accepted", w.Header().Get("Location"))
	}

	// Expired state is rejected.
	x.now = x.now.Add(11 * time.Minute)
	callback, state = x.start()
	x.now = x.now.Add(11 * time.Minute)
	w = x.callback(callback, state)
	if !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_state") {
		t.Fatal("expired state accepted", w.Header().Get("Location"))
	}
}

func TestOIDCDisabledAccountAndProvider(t *testing.T) {
	x := newOIDCHarness(t)
	admin := x.loginAdmin()
	x.saveConfig(admin)
	x.enable(admin)
	callback, state := x.start()
	x.callback(callback, state)
	approve := fmt.Sprintf(`{"issuer":%q,"subject":%q,"connections":[]}`, x.idp.server.URL, x.idp.subject)
	if w := x.do("POST", "/api/access/oidc/pending/approve", approve, admin, nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	callback, state = x.start()
	w := x.callback(callback, state)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatal("approved identity did not sign in", w.Code, w.Header().Get("Location"))
	}
	user := x.sessionFrom(w)
	body := fmt.Sprintf(`{"issuer":%q,"subject":%q}`, x.idp.server.URL, x.idp.subject)
	if w = x.do("POST", "/api/access/oidc/users/disable", body, admin, nil); w.Code != 204 {
		t.Fatal("disable failed", w.Code, w.Body.String())
	}
	if status := x.do("GET", "/api/auth/status", "", user, nil); !strings.Contains(status.Body.String(), `"authenticated":false`) {
		t.Fatal("disabled account kept its session", status.Body.String())
	}
	callback, state = x.start()
	w = x.callback(callback, state)
	if !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_disabled") {
		t.Fatal("disabled account signed in", w.Header().Get("Location"))
	}
	// Disabling the provider stops new starts without touching local admin login.
	if w = x.do("POST", "/api/access/oidc/disable", "", admin, nil); w.Code != 200 {
		t.Fatal("provider disable failed", w.Code, w.Body.String())
	}
	if w = x.do("GET", "/api/auth/oidc/start", "", nil, nil); !strings.Contains(w.Header().Get("Location"), "auth_error=oidc_unavailable") {
		t.Fatal("disabled provider started a login", w.Header().Get("Location"))
	}
	if w = x.do("POST", "/api/auth/login", fmt.Sprintf(`{"username":"ada.admin","password":%q}`, adminPassword), nil, nil); w.Code != 200 {
		t.Fatal("local administrator lost break-glass access", w.Code, w.Body.String())
	}
}

func TestOIDCSecretReferenceIsNotExposed(t *testing.T) {
	x := newOIDCHarness(t)
	admin := x.loginAdmin()
	t.Setenv("CUSTODIA_OIDC_TEST_SECRET", "super-secret-client-value")
	body := fmt.Sprintf(`{"issuer":%q,"client_id":"custodia-test","client_secret_ref":"env:CUSTODIA_OIDC_TEST_SECRET","redirect_uri":%q,"scopes":"openid","subject_claim":"sub","email_claim":"email","name_claim":"name"}`,
		x.idp.server.URL, origin+"/api/auth/oidc/callback")
	if w := x.do("PUT", "/api/access/oidc", body, admin, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := x.do("GET", "/api/access/oidc", "", admin, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "super-secret-client-value") || !strings.Contains(w.Body.String(), "env:CUSTODIA_OIDC_TEST_SECRET") {
		t.Fatal(w.Code, w.Body.String())
	}
	var audited string
	if err := x.store.DB.QueryRow("SELECT group_concat(after_json) FROM lab_audit WHERE action='save_oidc_config'").Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audited, "super-secret-client-value") || strings.Contains(audited, "CUSTODIA_OIDC_TEST_SECRET") {
		t.Fatal("audit exposed the secret value or its reference", audited)
	}
}

func TestOIDCRejectsUnsafeConfiguration(t *testing.T) {
	base, _ := url.Parse("https://idp.acme.example")
	config := lab.OIDCConfig{
		Issuer: base.String(), ClientID: "custodia", RedirectURI: origin + "/api/auth/oidc/callback",
		Scopes: "openid profile email", SubjectClaim: "sub", EmailClaim: "email", NameClaim: "name",
	}
	if err := validateOIDCConfig(config, origin); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*lab.OIDCConfig){
		func(c *lab.OIDCConfig) { c.Issuer = "http://idp.acme.example" },
		func(c *lab.OIDCConfig) { c.Issuer = "https://idp.acme.example/?x=1" },
		func(c *lab.OIDCConfig) { c.RedirectURI = "https://evil.example/callback" },
		func(c *lab.OIDCConfig) { c.Scopes = "profile email" },
		func(c *lab.OIDCConfig) { c.SubjectClaim = "" },
		func(c *lab.OIDCConfig) { c.ClientSecretRef = "literal-secret" },
	} {
		broken := config
		mutate(&broken)
		if err := validateOIDCConfig(broken, origin); err == nil {
			t.Fatalf("unsafe configuration accepted: %+v", broken)
		}
	}
	// Numeric loopback HTTP is allowed for the harness.
	loopback := config
	loopback.Issuer = "http://127.0.0.1:9000"
	if err := validateOIDCConfig(loopback, origin); err != nil {
		t.Fatal(err)
	}
}

func TestOIDCSecretReferences(t *testing.T) {
	t.Setenv("CUSTODIA_OIDC_TEST_SECRET", "client-secret-value\n")
	if value, err := resolveOIDCSecret("env:CUSTODIA_OIDC_TEST_SECRET"); err != nil || value != "client-secret-value" {
		t.Fatal(value, err)
	}
	t.Setenv("CUSTODIA_OIDC_EMPTY_SECRET", "")
	if _, err := resolveOIDCSecret("env:CUSTODIA_OIDC_EMPTY_SECRET"); err == nil {
		t.Fatal("empty environment secret accepted")
	}
	directory := t.TempDir()
	t.Setenv("CUSTODIA_OIDC_SECRET_DIR", directory)
	file := filepath.Join(directory, "secret")
	if err := os.WriteFile(file, []byte("file-secret\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := resolveOIDCSecret("file:" + file); err != nil || value != "file-secret" {
		t.Fatal(value, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "client"), []byte("provider-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := resolveOIDCSecret("secret:client"); err != nil || value != "provider-secret" {
		t.Fatal(value, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"literal-value", "env:", "file:relative", "file:" + outside, "secret:../escape"} {
		if _, err := resolveOIDCSecret(reference); err == nil {
			t.Fatalf("unsafe reference accepted: %q", reference)
		}
	}
}
