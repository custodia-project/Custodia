package localauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"custodia/server/internal/lab"
)

const setupPassword = "the first admin chose this passphrase"

// newEmptyHarness is a fresh installation: migrated, no local account yet.
func newEmptyHarness(t *testing.T) *harness {
	t.Helper()
	s, err := lab.Open(filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	x := &harness{t: t, store: s, now: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), ip: "198.51.100.7:40000"}
	x.h, err = NewHandler(s, Config{PublicOrigin: origin}, func() time.Time { return x.now }, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func (x *harness) issue() string {
	x.t.Helper()
	code, err := NewSetupCode()
	if err != nil {
		x.t.Fatal(err)
	}
	normalized, ok := NormalizeSetupCode(code)
	if !ok {
		x.t.Fatal("generated code has the wrong shape", code)
	}
	if err = x.store.IssueSetupCode(context.Background(), SetupCodeHash(normalized), x.now, x.now.Add(SetupCodeLifetime)); err != nil {
		x.t.Fatal(err)
	}
	return code
}

func (x *harness) setupAvailable() bool {
	x.t.Helper()
	w := x.do("GET", "/api/auth/status", "", nil, nil)
	var status struct {
		SetupAvailable bool `json:"setup_available"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		x.t.Fatal(err)
	}
	return status.SetupAvailable
}

func setupBody(code, username, password string) string {
	encoded, _ := json.Marshal(map[string]string{"code": code, "username": username, "display_name": "First Admin", "password": password})
	return string(encoded)
}

func TestSetupCodeShape(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		code, err := NewSetupCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 9 || code[4] != '-' || strings.ContainsAny(code, "IO01") {
			t.Fatal("unexpected code", code)
		}
		seen[code] = true
	}
	if len(seen) < 199 {
		t.Fatal("codes repeat", len(seen))
	}
	for input, want := range map[string]string{"abcd-efgh": "ABCDEFGH", " ABCD EFGH ": "ABCDEFGH", "ABCDEFGH": "ABCDEFGH"} {
		if got, ok := NormalizeSetupCode(input); !ok || got != want {
			t.Fatal(input, got, ok)
		}
	}
	for _, input := range []string{"ABCD-EFG", "ABCD-EFGHJ", "ABCD-EFG0", "ABCD-EFGI", ""} {
		if _, ok := NormalizeSetupCode(input); ok {
			t.Fatal("accepted malformed code", input)
		}
	}
}

func TestSetupCreatesTheFirstAdministratorOnce(t *testing.T) {
	x := newEmptyHarness(t)
	if x.setupAvailable() {
		t.Fatal("setup offered without a code")
	}
	code := x.issue()
	if !x.setupAvailable() {
		t.Fatal("setup not offered with a valid code")
	}
	// Without Origin the claim is refused like any mutation.
	r := x.do("POST", "/api/auth/setup", setupBody(code, "first.admin", setupPassword), nil, map[string]string{"Origin": "https://evil.example"})
	if r.Code != 403 {
		t.Fatal("foreign origin accepted", r.Code)
	}
	// A weak password is refused before the code is consumed.
	if w := x.do("POST", "/api/auth/setup", setupBody(code, "first.admin", "short"), nil, nil); w.Code != 400 || !x.setupAvailable() {
		t.Fatal("weak password consumed the code", w.Code, w.Body.String())
	}
	// Lower case and no dash are accepted.
	lowered := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	w := x.do("POST", "/api/auth/setup", setupBody(lowered, "first.admin", setupPassword), nil, nil)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"must_change_password":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var cookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == "custodia_session" {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("setup did not start a session")
	}
	status := x.do("GET", "/api/auth/status", "", &session{cookie: cookie}, nil)
	if !strings.Contains(status.Body.String(), `"authenticated":true`) || !strings.Contains(status.Body.String(), "access:local_users_manage") {
		t.Fatal("new account is not an authenticated administrator", status.Body.String())
	}
	// The chosen password signs in normally.
	if login, _ := x.login("first.admin", setupPassword); login.Code != 200 {
		t.Fatal("chosen password does not sign in", login.Code)
	}
	// One use, and the setup path is closed afterwards.
	if again := x.do("POST", "/api/auth/setup", setupBody(code, "second.admin", setupPassword), nil, nil); again.Code != 400 {
		t.Fatal("code reused", again.Code)
	}
	if x.setupAvailable() {
		t.Fatal("setup still offered after the first administrator")
	}
	normalized, _ := NormalizeSetupCode(x.issueRaw())
	if err := x.store.IssueSetupCode(context.Background(), SetupCodeHash(normalized), x.now, x.now.Add(time.Minute)); !errors.Is(err, lab.ErrLocalBootstrap) {
		t.Fatal("a new code was issued after the first administrator", err)
	}
	// The code never reaches the audit.
	var audited string
	if err := x.store.DB.QueryRow("SELECT group_concat(action || ' ' || after_json, ' | ') FROM lab_audit").Scan(&audited); err != nil {
		t.Fatal(err)
	}
	plain, _ := NormalizeSetupCode(code)
	if strings.Contains(audited, plain) || strings.Contains(audited, code) || !strings.Contains(audited, "setup_code_issued") || !strings.Contains(audited, "setup_admin_created") {
		t.Fatal("audit leaked the code or missed the creation", audited)
	}
}

func (x *harness) issueRaw() string {
	code, err := NewSetupCode()
	if err != nil {
		x.t.Fatal(err)
	}
	return code
}

func TestSetupCodeExpiresAndIsReplaced(t *testing.T) {
	x := newEmptyHarness(t)
	first := x.issue()
	second := x.issue()
	if w := x.do("POST", "/api/auth/setup", setupBody(first, "first.admin", setupPassword), nil, nil); w.Code != 400 {
		t.Fatal("a replaced code was accepted", w.Code)
	}
	x.now = x.now.Add(SetupCodeLifetime + time.Second)
	if x.setupAvailable() {
		t.Fatal("expired code still offers setup")
	}
	if w := x.do("POST", "/api/auth/setup", setupBody(second, "first.admin", setupPassword), nil, nil); w.Code != 400 {
		t.Fatal("an expired code was accepted", w.Code)
	}
	// Unknown, expired and replaced codes answer alike.
	third := x.issue()
	unknown := x.do("POST", "/api/auth/setup", setupBody("ZZZZ-ZZZZ", "first.admin", setupPassword), nil, nil)
	x.ip = "198.51.100.8:40000"
	expired := x.do("POST", "/api/auth/setup", setupBody(second, "first.admin", setupPassword), nil, nil)
	if unknown.Body.String() != expired.Body.String() {
		t.Fatal("answers differ", unknown.Body.String(), expired.Body.String())
	}
	if w := x.do("POST", "/api/auth/setup", setupBody(third, "first.admin", setupPassword), nil, nil); w.Code != 201 {
		t.Fatal("the current code failed", w.Code, w.Body.String())
	}
}

func TestSetupCodeGuessingIsBoundedWithoutDestroyingTheCode(t *testing.T) {
	x := newEmptyHarness(t)
	code := x.issue()
	for range SetupIPAttempts.Limit {
		if w := x.do("POST", "/api/auth/setup", setupBody("ZZZZ-ZZZZ", "first.admin", setupPassword), nil, nil); w.Code != 400 {
			t.Fatal("wrong code not refused", w.Code)
		}
	}
	// The guessing address is locked out, even with the right code.
	if w := x.do("POST", "/api/auth/setup", setupBody(code, "first.admin", setupPassword), nil, nil); w.Code != 429 {
		t.Fatal("locked address was not refused", w.Code)
	}
	// Guesses spread over many addresses pause every claim.
	for i := 0; i < SetupAllAttempts.Limit; i++ {
		x.ip = fmt.Sprintf("203.0.113.%d:40000", i+1)
		x.do("POST", "/api/auth/setup", setupBody("ZZZZ-ZZZZ", "first.admin", setupPassword), nil, nil)
	}
	x.ip = "192.0.2.50:40000"
	if w := x.do("POST", "/api/auth/setup", setupBody(code, "first.admin", setupPassword), nil, nil); w.Code != 429 {
		t.Fatal("global guess limit did not pause claims", w.Code)
	}
	// The global pause is shorter than the code's life: once it passes, the
	// owner's code still works from an address that never guessed.
	issued := x.now
	x.now = x.now.Add(SetupAllAttempts.Lock + time.Second)
	if !x.now.Before(issued.Add(SetupCodeLifetime)) {
		t.Fatal("the global pause outlives the code")
	}
	if w := x.do("POST", "/api/auth/setup", setupBody(code, "first.admin", setupPassword), nil, nil); w.Code != 201 {
		t.Fatal("the valid code did not survive guessing", w.Code, w.Body.String())
	}
}

// A new code from the host clears the guess counters, so outside guessing
// cannot keep the owner from setting up.
func TestNewSetupCodeClearsGuessLimits(t *testing.T) {
	x := newEmptyHarness(t)
	x.issue()
	for i := 0; i < SetupAllAttempts.Limit; i++ {
		x.ip = fmt.Sprintf("203.0.113.%d:40000", i+1)
		x.do("POST", "/api/auth/setup", setupBody("ZZZZ-ZZZZ", "first.admin", setupPassword), nil, nil)
	}
	fresh := x.issue()
	if w := x.do("POST", "/api/auth/setup", setupBody(fresh, "first.admin", setupPassword), nil, nil); w.Code != 201 {
		t.Fatal("a new code did not lift the pause for its owner", w.Code, w.Body.String())
	}
}
