package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"custodia/server/internal/lab"
	"custodia/server/internal/localauth"
)

// TestLocalModeEndToEnd runs the real composition: init and create-admin over
// the CLI (password piped on stdin), then HTTP login, CSRF, temporary password,
// revocation, password change and logout against the handler in local mode.
func TestLocalModeEndToEnd(t *testing.T) {
	oldArgs, oldStdin := os.Args, os.Stdin
	defer func() { os.Args, os.Stdin = oldArgs, oldStdin }()
	base := t.TempDir()
	dir := filepath.Join(base, "installation")
	command := func(args ...string) error { os.Args = append([]string{"custodia"}, args...); return run() }
	if err := command("init", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seed, err := lab.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = seed.Seed(ctx, time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	seed.DB.Close()

	const adminPassword = "ada admin host passphrase"
	pipe, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = pipe
	if _, err = writer.WriteString(adminPassword + "\n"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	if err = command("create-admin", "--dir", dir, "--username", "ada.admin", "--display-name", "Ada Admin", "--connections", lab.IdentityConnection+","+lab.NetskopeConnection); err != nil {
		t.Fatal("create-admin with stdin password:", err)
	}
	os.Stdin = oldStdin

	store, err := lab.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	handler, err := localauth.NewHandler(store, localauth.Config{PublicOrigin: origin}, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	defer server.Close()
	originURL, _ := url.Parse(origin)

	newClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Timeout: 10 * time.Second}
	}
	adminClient, readerClient := newClient(), newClient()
	bodies := []string{}
	do := func(client *http.Client, method, path string, payload any, csrf string, setOrigin bool) (*http.Response, []byte) {
		t.Helper()
		var body io.Reader
		if payload != nil {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(encoded)
		}
		request, err := http.NewRequest(method, origin+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if setOrigin {
			request.Header.Set("Origin", origin)
		}
		if csrf != "" {
			request.Header.Set(localauth.CSRFHeader, csrf)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(data))
		return response, data
	}
	csrfFrom := func(data []byte) string {
		t.Helper()
		var out struct {
			CSRF string `json:"csrf_token"`
		}
		if err := json.Unmarshal(data, &out); err != nil || out.CSRF == "" {
			t.Fatal("missing CSRF token", string(data), err)
		}
		return out.CSRF
	}

	if response, data := do(adminClient, "GET", "/api/auth/status", nil, "", false); response.StatusCode != 200 || !strings.Contains(string(data), `"authenticated":false`) {
		t.Fatal("anonymous status", response.StatusCode, string(data))
	}
	wrong, wrongBody := do(adminClient, "POST", "/api/auth/login", map[string]string{"username": "ada.admin", "password": "not the password"}, "", true)
	unknown, unknownBody := do(adminClient, "POST", "/api/auth/login", map[string]string{"username": "nobody", "password": "not the password"}, "", true)
	if wrong.StatusCode != 401 || unknown.StatusCode != 401 || string(wrongBody) != string(unknownBody) {
		t.Fatal("login failure reveals the account", wrong.StatusCode, string(wrongBody), unknown.StatusCode, string(unknownBody))
	}
	if response, _ := do(adminClient, "POST", "/api/auth/login", map[string]string{"username": "ada.admin", "password": adminPassword}, "", false); response.StatusCode != 403 {
		t.Fatal("login without Origin accepted", response.StatusCode)
	}
	login, loginBody := do(adminClient, "POST", "/api/auth/login", map[string]string{"username": "ada.admin", "password": adminPassword}, "", true)
	if login.StatusCode != 200 {
		t.Fatal("admin login", login.StatusCode, string(loginBody))
	}
	cookie := login.Header.Get("Set-Cookie")
	for _, attribute := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(cookie, attribute) {
			t.Fatal("session cookie lacks", attribute, cookie)
		}
	}
	adminCSRF := csrfFrom(loginBody)
	if response, data := do(adminClient, "GET", "/api/auth/status", nil, "", false); response.StatusCode != 200 || !strings.Contains(string(data), `"access:local_users_manage"`) || !strings.Contains(string(data), `"csrf_token":"`+adminCSRF+`"`) {
		t.Fatal("authenticated status", response.StatusCode, string(data))
	}
	preferences := map[string]any{"visible_workspaces": []string{"person"}, "hidden_workspaces": []string{}, "recent_workspaces": []string{}}
	if response, _ := do(adminClient, "PUT", "/api/workspaces/preferences", preferences, "", true); response.StatusCode != 403 {
		t.Fatal("mutation without CSRF accepted", response.StatusCode)
	}
	if response, _ := do(adminClient, "PUT", "/api/workspaces/preferences", preferences, "forged", true); response.StatusCode != 403 {
		t.Fatal("forged CSRF accepted", response.StatusCode)
	}
	if response, data := do(adminClient, "PUT", "/api/workspaces/preferences", preferences, adminCSRF, true); response.StatusCode != 200 {
		t.Fatal("valid mutation rejected", response.StatusCode, string(data))
	}

	const readerPassword = "temporary reader passphrase"
	create := map[string]any{"username": "bruno", "display_name": "Bruno Reader", "password": readerPassword, "connections": []string{lab.IdentityConnection}}
	if response, data := do(adminClient, "POST", "/api/access/users", create, adminCSRF, true); response.StatusCode != 201 || strings.Contains(string(data), readerPassword) {
		t.Fatal("create reader", response.StatusCode, string(data))
	}
	if response, data := do(adminClient, "GET", "/api/access/users", nil, "", false); response.StatusCode != 200 || !strings.Contains(string(data), `"username":"bruno"`) || strings.Contains(string(data), "argon2") {
		t.Fatal("list users", response.StatusCode, string(data))
	}

	// The reader starts with a temporary password and cannot use the API yet.
	readerLogin, readerBody := do(readerClient, "POST", "/api/auth/login", map[string]string{"username": "bruno", "password": readerPassword}, "", true)
	if readerLogin.StatusCode != 200 || !strings.Contains(string(readerBody), `"must_change_password":true`) {
		t.Fatal("reader login", readerLogin.StatusCode, string(readerBody))
	}
	readerCSRF := csrfFrom(readerBody)
	if cookies := readerClient.Jar.Cookies(originURL); len(cookies) != 1 {
		t.Fatal("reader cookie not stored", len(cookies))
	}
	if response, _ := do(readerClient, "GET", "/api/connections", nil, "", false); response.StatusCode != 403 {
		t.Fatal("temporary password reached the API", response.StatusCode)
	}

	// Disabling revokes the session and a correct password still gets 403.
	if response, _ := do(adminClient, "POST", "/api/access/users/disable", map[string]string{"username": "bruno"}, adminCSRF, true); response.StatusCode != 204 {
		t.Fatal("disable reader", response.StatusCode)
	}
	if response, _ := do(readerClient, "GET", "/api/connections", nil, "", false); response.StatusCode != 401 {
		t.Fatal("disabled reader session survived", response.StatusCode)
	}
	if response, _ := do(readerClient, "POST", "/api/auth/login", map[string]string{"username": "bruno", "password": readerPassword}, "", true); response.StatusCode != 403 {
		t.Fatal("disabled reader signed in", response.StatusCode)
	}
	if response, _ := do(adminClient, "POST", "/api/access/users/enable", map[string]string{"username": "bruno"}, adminCSRF, true); response.StatusCode != 204 {
		t.Fatal("enable reader", response.StatusCode)
	}

	// Password change: CSRF, session rotation and revocation of the others.
	readerLogin, readerBody = do(readerClient, "POST", "/api/auth/login", map[string]string{"username": "bruno", "password": readerPassword}, "", true)
	if readerLogin.StatusCode != 200 {
		t.Fatal("re-enabled reader login", readerLogin.StatusCode, string(readerBody))
	}
	readerCSRF = csrfFrom(readerBody)
	oldCookies := readerClient.Jar.Cookies(originURL)
	if len(oldCookies) != 1 {
		t.Fatal("reader session cookie missing before the password change", len(oldCookies))
	}
	const newPassword = "a brand new reader passphrase"
	if response, _ := do(readerClient, "POST", "/api/auth/password", map[string]string{"current_password": readerPassword, "new_password": newPassword}, "", true); response.StatusCode != 403 {
		t.Fatal("password change without CSRF accepted", response.StatusCode)
	}
	changed, changedBody := do(readerClient, "POST", "/api/auth/password", map[string]string{"current_password": readerPassword, "new_password": newPassword}, readerCSRF, true)
	if changed.StatusCode != 200 {
		t.Fatal("password change", changed.StatusCode, string(changedBody))
	}
	if strings.Contains(string(changedBody), newPassword) || strings.Contains(string(changedBody), readerPassword) {
		t.Fatal("password echoed in the response", string(changedBody))
	}
	// A client holding the cookie from before the change is rejected.
	oldJar, _ := cookiejar.New(nil)
	oldJar.SetCookies(originURL, oldCookies)
	oldResponse, err := (&http.Client{Jar: oldJar, Timeout: 10 * time.Second}).Get(origin + "/api/connections")
	if err != nil {
		t.Fatal(err)
	}
	oldResponse.Body.Close()
	if oldResponse.StatusCode != 401 {
		t.Fatal("pre-change session survived", oldResponse.StatusCode)
	}
	if response, data := do(readerClient, "GET", "/api/connections", nil, "", false); response.StatusCode != 200 || strings.Contains(string(data), lab.NetskopeConnection) {
		t.Fatal("rotated session or reader scope", response.StatusCode, string(data))
	}
	// The old password no longer authenticates; the new one does.
	if response, _ := do(readerClient, "POST", "/api/auth/login", map[string]string{"username": "bruno", "password": readerPassword}, "", true); response.StatusCode != 401 {
		t.Fatal("old password still authenticates", response.StatusCode)
	}
	if response, _ := do(readerClient, "POST", "/api/auth/login", map[string]string{"username": "bruno", "password": newPassword}, "", true); response.StatusCode != 200 {
		t.Fatal("new password rejected", response.StatusCode)
	}

	// Administrative revocation ends the reader's current session.
	if response, _ := do(adminClient, "POST", "/api/access/users/revoke-sessions", map[string]string{"username": "bruno"}, adminCSRF, true); response.StatusCode != 204 {
		t.Fatal("revoke reader sessions", response.StatusCode)
	}
	if response, _ := do(readerClient, "GET", "/api/connections", nil, "", false); response.StatusCode != 401 {
		t.Fatal("revoked reader session accepted", response.StatusCode)
	}

	// Logout deletes the admin session and expires the cookie.
	logout, logoutBody := do(adminClient, "POST", "/api/auth/logout", nil, adminCSRF, true)
	if logout.StatusCode != 200 || !strings.Contains(string(logoutBody), `"logged_out":true`) || strings.Contains(string(logoutBody), "gateway_logout") {
		t.Fatal("logout", logout.StatusCode, string(logoutBody))
	}
	if response, data := do(adminClient, "GET", "/api/auth/status", nil, "", false); response.StatusCode != 200 || !strings.Contains(string(data), `"authenticated":false`) {
		t.Fatal("session survived logout", response.StatusCode, string(data))
	}
	for _, body := range bodies {
		for _, secret := range []string{adminPassword, readerPassword, newPassword} {
			if strings.Contains(body, secret) {
				t.Fatal("password reached an HTTP response", body)
			}
		}
	}
}
