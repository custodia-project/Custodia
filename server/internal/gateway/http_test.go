package gateway

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"custodia/server/internal/lab"
	"custodia/server/internal/localauth"
)

func TestGatewayAuthorizationLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	pub, key := testKeys(t)
	s, err := lab.Open(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, now); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Issuer: "https://login.acme.example", Audience: "custodia", PublicOrigin: "https://custodia.acme.example", Keys: map[string]ed25519.PublicKey{KeyID(pub): pub}}
	if err = s.BootstrapAdministrator(ctx, cfg.Issuer, "opaque-admin", []string{lab.IdentityConnection, lab.NetskopeConnection}); err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapAdministrator(ctx, cfg.Issuer, "another-admin", nil); err == nil {
		t.Fatal("first administrator replaced")
	}
	h, err := NewHandler(s, cfg, func() time.Time { return now }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("compiled frontend")) }))
	if err != nil {
		t.Fatal(err)
	}
	sequence := 0
	request := func(subject, method, path, body, origin string) *httptest.ResponseRecorder {
		t.Helper()
		sequence++
		claims := testClaims(now)
		claims.Subject = subject
		claims.ID = fmt.Sprintf("request-%d", sequence)
		claims.Email = "admin@acme.example"
		claims.Groups = []string{"admin"}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Ozal-Assertion", signClaims(t, key, claims))
		r.Header.Set("X-Auth-Request-User", "opaque-admin")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/", "/api/auth/status", "/api/chains/person/search"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer ignored-lab-key")
		r.Header.Set("X-Auth-Request-User", "opaque-admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unsigned direct request accepted", path, w.Code)
		}
	}
	if w := request("unknown-subject", "GET", "/api/auth/status", "", ""); w.Code != 403 {
		t.Fatal("email/groups provisioned identity", w.Code)
	}
	status := request("opaque-admin", "GET", "/api/auth/status", "", "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), "installation:manage") || status.Header().Get("Set-Cookie") != "" {
		t.Fatal("administrator/session contract", status.Code, status.Body.String())
	}
	body := `{"subject":"opaque-reader","connections":["acme-identity"]}`
	for _, origin := range []string{"", "https://attacker.example"} {
		if w := request("opaque-admin", "POST", "/api/access/readers", body, origin); w.Code != 403 {
			t.Fatal("CSRF accepted", w.Code)
		}
	}
	if w := request("opaque-admin", "POST", "/api/access/readers", body, cfg.PublicOrigin); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	readerStatus := request("opaque-reader", "GET", "/api/auth/status", "", "")
	if readerStatus.Code != 200 || strings.Contains(readerStatus.Body.String(), "installation:manage") || strings.Contains(readerStatus.Body.String(), "netskope:read") {
		t.Fatal("reader inherited administrator scopes", readerStatus.Code, readerStatus.Body.String())
	}
	if w := request("opaque-reader", "GET", "/api/chains/person/coverage", "", ""); w.Code != 403 {
		t.Fatal("cross-source read accepted", w.Code)
	}
	if w := request("opaque-reader", "POST", "/api/access/readers", `{"subject":"escalated"}`, cfg.PublicOrigin); w.Code != 403 {
		t.Fatal("reader administered identities")
	}
	if w := request("opaque-admin", "POST", "/api/access/readers", `{"subject":"over-scope","connections":["hidden-connection"]}`, cfg.PublicOrigin); w.Code != 403 {
		t.Fatal("delegation ceiling bypassed")
	}
	if w := request("opaque-admin", "DELETE", "/api/access/readers", `{"subject":"opaque-admin"}`, cfg.PublicOrigin); w.Code != 403 {
		t.Fatal("administrator remotely locked out")
	}
	if w := request("opaque-reader", "POST", "/api/auth/login", `{}`, cfg.PublicOrigin); w.Code != 405 {
		t.Fatal("local login available")
	}
	if w := request("opaque-reader", "POST", "/api/auth/logout", `{}`, cfg.PublicOrigin); w.Code != 200 || !strings.Contains(w.Body.String(), "gateway_logout") {
		t.Fatal("gateway logout contract", w.Code)
	}
	if w := request("opaque-admin", "DELETE", "/api/access/readers", `{"subject":"opaque-reader"}`, cfg.PublicOrigin); w.Code != 204 {
		t.Fatal("disable reader failed", w.Code, w.Body.String())
	}
	if w := request("opaque-reader", "GET", "/api/auth/status", "", ""); w.Code != 403 {
		t.Fatal("disabled identity accepted")
	}
	claims := testClaims(now)
	claims.ID = "replayed-request"
	token := signClaims(t, key, claims)
	for index, want := range []int{200, 401} {
		r := httptest.NewRequest("GET", "/api/auth/status", nil)
		r.Header.Set("X-Ozal-Assertion", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal("replay contract", index, w.Code)
		}
	}
	for _, headers := range []http.Header{{"X-Ozal-Assertion": {token, token}}, {"X_Ozal_Assertion": {token}}, {"X-Ozal-Assertion": {token}, "X_Ozal_Assertion": {token}}} {
		r := httptest.NewRequest("GET", "/api/auth/status", nil)
		r.Header = headers
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("ambiguous assertion header accepted")
		}
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_principals WHERE issuer=?", cfg.Issuer).Scan(&count); err != nil || count != 2 {
		t.Fatal("unexpected account provisioning", count, err)
	}
	var audit []byte
	if err = s.DB.QueryRow("SELECT after_json FROM lab_audit WHERE action='authorize_reader'").Scan(&audit); err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if json.Unmarshal(audit, &entry) != nil || entry["subject"] != "opaque-reader" {
		t.Fatal("missing administrative audit")
	}
}

// Ozal mode never accepts a local session cookie, even a live one, and local
// accounts never resolve as Ozal principals.
func TestOzalModeRejectsLocalSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	pub, key := testKeys(t)
	s, err := lab.Open(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateLocalAdministrator(ctx, "opaque-admin", "Local Admin", "$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", []string{lab.IdentityConnection}, now); err != nil {
		t.Fatal(err)
	}
	local, err := s.LocalCredential(ctx, "opaque-admin")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("A", 43)
	if err = s.StartLocalSession(ctx, local.Principal, localauth.TokenHash(token), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Issuer: "https://login.acme.example", Audience: "custodia", PublicOrigin: "https://custodia.acme.example", Keys: map[string]ed25519.PublicKey{KeyID(pub): pub}}
	h, err := NewHandler(s, cfg, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"custodia_session", "__Host-custodia_session"} {
		r := httptest.NewRequest("GET", "/api/auth/status", nil)
		r.AddCookie(&http.Cookie{Name: name, Value: token})
		r.Header.Set(localauth.CSRFHeader, localauth.CSRFToken(token))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("Ozal mode accepted a local session", name, w.Code)
		}
	}
	// A signed assertion for the same subject is not the local account.
	claims := testClaims(now)
	claims.Subject = "opaque-admin"
	r := httptest.NewRequest("GET", "/api/auth/status", nil)
	r.Header.Set("X-Ozal-Assertion", signClaims(t, key, claims))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("local account resolved as Ozal principal", w.Code)
	}
}
