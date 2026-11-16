package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebRoutesNeverHideAPIError(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "assets"), 0700)
	os.WriteFile(filepath.Join(dir, "private.db"), []byte("private sentinel"), 0600)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>real frontend</html>"), 0600)
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "API not implemented", 404) })
	h, err := WithWeb(api, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{{"/chains/person", 200, "real frontend"}, {"/api/unknown", 404, "API not implemented"}, {"/missing.js", 404, "404 page not found"}, {"/private.db", 404, "404 page not found"}, {"/.env", 404, "404 page not found"}, {"/assets/", 404, "404 page not found"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if _, err = WithWeb(api, t.TempDir()); err == nil {
		t.Fatal("unbuilt frontend accepted")
	}
}
func TestPreferencesAtomicAuditAndAuthorization(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("lab-key", time.Now)
	request := func(token, body string) int {
		r := httptest.NewRequest("PUT", "/api/workspaces/preferences", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if request("bad", `{"recent_workspaces":["person"]}`) != 401 {
		t.Fatal("unauthorized mutation")
	}
	if request("lab-key", `{"recent_workspaces":["safes"]}`) != 403 {
		t.Fatal("unsupported workspace accepted")
	}
	if request("lab-key", `{"recent_workspaces":["person"]}`) != 200 {
		t.Fatal("save failed")
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_audit").Scan(&count); err != nil || count != 1 {
		t.Fatal(err, count)
	}
	_, err = s.DB.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON lab_audit BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if request("lab-key", `{"recent_workspaces":[]}`) != 503 {
		t.Fatal("missing audit committed")
	}
	var b []byte
	if err = s.DB.QueryRow("SELECT document FROM lab_preferences").Scan(&b); err != nil {
		t.Fatal(err)
	}
	var p Preferences
	json.Unmarshal(b, &p)
	if len(p.Recent) != 1 {
		t.Fatal("audit failure did not roll back preferences")
	}
}

func TestExplicitMigrationPreservesOldLab(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	// The original migration remains intact; subsequent migration adds separate tables.
	_, err = s.DB.Exec(`CREATE TABLE schema_version(version INTEGER PRIMARY KEY CHECK(version=1)); INSERT INTO schema_version VALUES(1); CREATE TABLE lab_snapshot(id INTEGER PRIMARY KEY CHECK(id=1),captured_at TEXT NOT NULL,document BLOB NOT NULL); `)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO lab_snapshot VALUES(1,?,?)", "2026-11-16T12:00:00Z", fixture); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(context.Background()); err == nil {
		t.Fatal("old lab served without migration")
	}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var captured string
	if err = s.DB.QueryRow("SELECT captured_at FROM lab_snapshot").Scan(&captured); err != nil || captured != "2026-11-16T12:00:00Z" {
		t.Fatal("migration changed evidence", err)
	}
	if _, err = s.DB.Exec("INSERT INTO lab_schema_migrations VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(context.Background()); err == nil {
		t.Fatal("future schema accepted")
	}
	if _, err = s.Load(context.Background()); err == nil {
		t.Fatal("future schema served")
	}
}

func TestLaboratoryRejectsAmbiguousAuthorization(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	h := s.Handler("lab-key", time.Now)
	for _, values := range [][]string{{"lab-key"}, {"Basic lab-key"}, {"Bearer lab-key", "Bearer lab-key"}} {
		r := httptest.NewRequest("GET", "/api/auth/status", nil)
		for _, value := range values {
			r.Header.Add("Authorization", value)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("invalid Authorization accepted")
		}
	}
}
