package lab

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectionIsolationAndScopedHTTP(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "scoped.db"))
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
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Same provider, collection and external ID in a hidden connection remain separate.
	mustExec("INSERT INTO lab_connections VALUES('other-netskope','netskope')")
	mustExec("INSERT INTO lab_runs VALUES('other-netskope','partial',?)", now.Format(time.RFC3339))
	secret, _ := json.Marshal(record{Name: "Hidden Sentinel", Email: "hidden@initech.example"})
	mustExec("INSERT INTO lab_observations(connection_id,collection,external_id,document) VALUES('other-netskope','users','203',?)", secret)
	// A collection may reuse the same external ID, while duplicate composite keys fail.
	mustExec("INSERT INTO lab_observations(connection_id,collection,external_id,document) VALUES('other-netskope','devices','203',?)", []byte(`{"user_id":"203","last_signal_at":"2026-11-16T11:00:00Z"}`))
	if _, err = s.DB.Exec("INSERT INTO lab_observations(connection_id,collection,external_id,document) VALUES('other-netskope','users','203',?)", secret); err == nil {
		t.Fatal("duplicate composite key accepted")
	}
	for _, entry := range []struct {
		subject, token string
		scope          []string
	}{{"identity-reader", "identity-key", []string{IdentityConnection}}, {"netskope-reader", "netskope-key", []string{NetskopeConnection}}, {"pair-reader", "pair-key", []string{IdentityConnection, NetskopeConnection}}, {"unrelated-reader", "other-key", []string{IdentityConnection, "other-netskope"}}, {"empty-reader", "empty-key", nil}} {
		if err = s.IssuePrincipal(ctx, entry.subject, entry.token, entry.scope); err != nil {
			t.Fatal(err)
		}
	}
	h := s.Handler("bootstrap-key", func() time.Time { return now })
	request := func(token, method, path, body string) (int, string) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Auth-Request-User", "synthetic-lab")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	for _, token := range []string{"identity-key", "netskope-key"} {
		for _, path := range []string{"/api/chains/person/coverage", "/api/chains/person/coverage/detail?email=alice.morgan@acme.example", "/api/chains/person/coverage/detail?email=absent@acme.example", "/api/chains/person/search?netskope_connection=other-netskope", "/api/chains/person/search?netskope_connection=does-not-exist"} {
			if code, _ := request(token, "GET", path, ""); code != 403 {
				t.Fatalf("%s %s returned %d", token, path, code)
			}
		}
	}
	for _, tc := range []struct{ token, visible, hidden string }{{"identity-key", "identity", "netskope"}, {"netskope-key", "netskope", "identity"}} {
		code, body := request(tc.token, "GET", "/api/chains/person/search?q=alice", "")
		if code != 200 || strings.Contains(body, tc.hidden) || !strings.Contains(body, tc.visible) {
			t.Fatal(code, body)
		}
		code, body = request(tc.token, "GET", "/api/chains/person?email=alice.morgan@acme.example", "")
		if code != 200 || strings.Contains(body, `"`+tc.hidden+`"`) {
			t.Fatal(code, body)
		}
		code, body = request(tc.token, "GET", "/api/connections", "")
		if code != 200 || strings.Contains(body, tc.hidden) || strings.Contains(body, "other-") {
			t.Fatal(code, body)
		}
		code, body = request(tc.token, "GET", "/api/auth/status", "")
		if code != 200 || strings.Contains(body, "coverage:read") {
			t.Fatal(code, body)
		}
	}
	if code, _ := request("other-key", "GET", "/api/chains/person/coverage?netskope_connection=other-netskope", ""); code != 403 {
		t.Fatal("unapproved correlation rule accepted")
	}
	if code, _ := request("empty-key", "GET", "/api/chains/person/search", ""); code != 403 {
		t.Fatal("ungranted principal obtained population")
	}
	code, body := request("pair-key", "GET", "/api/chains/person/coverage", "")
	if code != 200 || strings.Contains(body, "Hidden Sentinel") || strings.Contains(body, "other-netskope") {
		t.Fatal(code, body)
	}
	var summary struct {
		Counts struct {
			Denominator, Unknown int
			Recent               int `json:"devices_recent"`
			Total                int `json:"devices_total"`
		}
	}
	if err = json.Unmarshal([]byte(body), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Counts.Denominator != 7 || summary.Counts.Unknown != 2 || summary.Counts.Recent != 4 || summary.Counts.Total != 5 {
		t.Fatal(body)
	}
	// The archive is not an API read model after normalization.
	mustExec("UPDATE lab_snapshot SET document='{}'")
	if code, body = request("pair-key", "GET", "/api/chains/person/search?q=alice", ""); code != 200 || !strings.Contains(body, "Alice Morgan") {
		t.Fatal(code, body)
	}
	// Source status is evaluated per selected connection, not globally.
	mustExec("UPDATE lab_runs SET status='partial' WHERE connection_id=?", NetskopeConnection)
	code, body = request("pair-key", "GET", "/api/chains/person/coverage", "")
	if code != 200 || !strings.Contains(body, `"unknown":7`) || !strings.Contains(body, `"coverage_percent":null`) {
		t.Fatal(code, body)
	}
	if code, body = request("identity-key", "GET", "/api/chains/person/search?q=alice", ""); code != 200 || strings.Contains(body, "partial") {
		t.Fatal(code, body)
	}
	if code, body = request("identity-key", "PUT", "/api/workspaces/preferences", `{"recent_workspaces":["person"]}`); code != 200 {
		t.Fatal(code, body)
	}
	if code, body = request("pair-key", "GET", "/api/workspaces/preferences", ""); code != 200 || strings.Contains(body, `"recent_workspaces":["person"]`) {
		t.Fatal("preferences crossed principal", body)
	}
	if err = s.RevokePrincipal(ctx, "pair-reader"); err != nil {
		t.Fatal(err)
	}
	if code, _ = request("pair-key", "GET", "/api/chains/person/search", ""); code != 401 {
		t.Fatal("revocation not applied")
	}
	if err = s.RevokePrincipal(ctx, "synthetic-lab"); err != nil {
		t.Fatal(err)
	}
	if code, _ = request("bootstrap-key", "GET", "/api/chains/person/search", ""); code != 401 {
		t.Fatal("bootstrap credential bypassed disabled principal")
	}
}

func TestGrantOperationsAreAtomicAndDoNotStoreTokens(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "grants.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.IssuePrincipal(ctx, "reader", "private-test-key", []string{"missing-connection"}); err == nil {
		t.Fatal("invalid grant accepted")
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM lab_principals WHERE subject='reader'").Scan(&count)
	if count != 0 {
		t.Fatal("partially issued principal")
	}
	if err = s.IssuePrincipal(ctx, "reader", "private-test-key", []string{IdentityConnection}); err != nil {
		t.Fatal(err)
	}
	if err = s.IssuePrincipal(ctx, "reader", "replacement-key", []string{NetskopeConnection}); err == nil {
		t.Fatal("reissue escalated scope")
	}
	var stored string
	if err = s.DB.QueryRow("SELECT digest FROM lab_credentials").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "private-test-key" || len(stored) != 64 {
		t.Fatal("raw credential stored")
	}
	_, err = s.DB.Exec(`CREATE TRIGGER reject_revocation_audit BEFORE INSERT ON lab_audit BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokePrincipal(ctx, "reader"); err == nil {
		t.Fatal("unaudited revocation committed")
	}
	if _, err = s.credential(ctx, "private-test-key"); err != nil {
		t.Fatal("audit failure changed principal")
	}
}

func TestPragmasSurviveReplacementConnections(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a?b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	// Force every operation to obtain a new driver connection.
	s.DB.SetMaxIdleConns(0)
	for range 2 {
		var foreign, timeout int
		var mode string
		if err = s.DB.QueryRow("PRAGMA foreign_keys").Scan(&foreign); err != nil {
			t.Fatal(err)
		}
		if err = s.DB.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if err = s.DB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if foreign != 1 || timeout != 5000 || mode != "wal" {
			t.Fatal("connection protections lost", foreign, timeout, mode)
		}
	}
}

func TestNormalizedCoverageMissingAndAmbiguousSources(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "coverage.db"))
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
	p, err := s.principal(ctx, "synthetic-lab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO lab_observations(connection_id,collection,external_id,document) SELECT connection_id,collection,'opaque/duplicate',document FROM lab_observations WHERE connection_id=? AND collection='users' AND external_id='203'", NetskopeConnection); err != nil {
		t.Fatal(err)
	}
	view, err := s.View(ctx, p, IdentityConnection, NetskopeConnection, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if detail := view.Detail("alice.morgan@acme.example", now); detail["status"] != "unknown" || detail["netskope_users"] != 2 {
		t.Fatal(detail)
	}
	if _, err = s.DB.Exec("DELETE FROM lab_runs WHERE connection_id=?", NetskopeConnection); err != nil {
		t.Fatal(err)
	}
	view, err = s.View(ctx, p, IdentityConnection, NetskopeConnection, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	summary := view.Summary(now)
	counts := summary["counts"].(map[string]any)
	if counts["unknown"] != 7 || counts["coverage_percent"] != nil {
		t.Fatal(counts)
	}
	source := view.Sources(now)["netskope"].(map[string]any)
	if source["status"] != nil || source["run_id"] != nil || source["freshness"] != "missing" {
		t.Fatal(source)
	}
}

func TestIdentityPopulationPreservesPythonDenominator(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "population.db"))
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
	p, err := s.principal(ctx, "synthetic-lab")
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.View(ctx, p, IdentityConnection, NetskopeConnection, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	counts := view.Summary(now)["counts"].(map[string]any)
	if counts["denominator"] != 7 || counts["unknown"] != 2 || counts["coverage_percent"] != 42.86 {
		t.Fatal(counts)
	}
	detail := view.Detail("eve.taylor@acme.example", now)
	if detail["status"] != "unknown" || detail["devices_total"] != 0 {
		t.Fatal("ambiguous identity attributed a device", detail)
	}
	found := false
	for _, person := range view.People {
		if person.Email == "eve.taylor@acme.example" {
			recent, old := view.DeviceCounts(person, now)
			if recent != 1 || old != 0 {
				t.Fatal("tool evidence was lost")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing synthetic person")
	}
	// Exact names never establish a cross-source identity for another email.
	b, _ := json.Marshal(record{Name: "Alice Morgan", Email: "another.alice@acme.example"})
	if _, err = s.DB.Exec("INSERT INTO lab_observations(connection_id,collection,external_id,document) VALUES(?,'users','opaque/new',?)", IdentityConnection, b); err != nil {
		t.Fatal(err)
	}
	view, err = s.View(ctx, p, IdentityConnection, NetskopeConnection, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	detail = view.Detail("another.alice@acme.example", now)
	if detail["netskope_users"] != 0 || detail["status"] != "unknown" {
		t.Fatal("name joined two identities", detail)
	}
}

func TestPythonRoundingContract(t *testing.T) {
	for _, tc := range []struct{ value, want float64 }{{3.125, 3.12}, {2.675, 2.67}, {66.666666666, 66.67}, {42.857142857, 42.86}} {
		if got := round2(tc.value); got != tc.want {
			t.Errorf("%v got %v want %v", tc.value, got, tc.want)
		}
	}
}
