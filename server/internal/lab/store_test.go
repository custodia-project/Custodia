package lab

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPersistentLabAndHTTP(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(ctx); err == nil {
		t.Fatal("unmigrated database accepted")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, now); err == nil {
		t.Fatal("seed overwrote existing snapshot")
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]string{"bob.chen@acme.example": "partial", "alice.morgan@acme.example": "gap", "eve.taylor@acme.example": "unknown", "carol.diaz@acme.example": "covered", "absent@acme.example": "not_applicable"} {
		if got := c.Detail(email, now)["status"]; got != want {
			t.Errorf("%s got %v want %v", email, got, want)
		}
	}
	if got := c.Detail("alice.morgan@acme.example", now.Add(25*time.Hour))["status"]; got != "unknown" {
		t.Fatal("restart/read refreshed stale evidence")
	}
	h := s.Handler("private-lab-token", func() time.Time { return now })
	for _, tc := range []struct {
		method, path, token string
		code                int
	}{{"GET", "/api/chains/person/search?q=Alice", "", 401}, {"GET", "/api/chains/person/search?q=Alice", "bad", 401}, {"GET", "/api/chains/person/search?q=Alice", "private-lab-token", 200}, {"GET", "/api/chains/person?email=eve.taylor@acme.example", "private-lab-token", 200}, {"GET", "/api/chains/person/coverage/detail?email=alice.morgan@acme.example", "private-lab-token", 200}, {"POST", "/api/chains/person", "private-lab-token", 405}, {"GET", "/api/not-ported", "private-lab-token", 404}} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("X-Auth-Request-User", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Errorf("%s got %d want %d", tc.path, w.Code, tc.code)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodGet, "/api/chains/person/search", nil)
			r.Header.Set("Authorization", "Bearer private-lab-token")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Errorf("concurrent read %d", w.Code)
			}
		}()
	}
	wg.Wait()
}

func TestFixtureAndSummaryContract(t *testing.T) {
	original, err := os.ReadFile("../../../web/src/demo/model/company.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, fixture) {
		t.Fatal("embedded company diverged from web scenario")
	}
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, now); err != nil {
		t.Fatal(err)
	}
	c, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := c.Summary(now)["counts"].(map[string]any)
	for key, want := range map[string]int{"denominator": 7, "covered": 3, "partial": 1, "gap": 1, "unknown": 2, "devices_recent": 4, "devices_total": 5} {
		if counts[key] != want {
			t.Errorf("%s got %v want %d", key, counts[key], want)
		}
	}
	if counts["coverage_percent"] != float64(42.86) || counts["device_coverage_percent"] != float64(80) {
		t.Fatal(counts)
	}
	stale := c.Summary(now.Add(25 * time.Hour))["counts"].(map[string]any)
	if stale["unknown"] != 7 || stale["coverage_percent"] != nil || stale["device_coverage_percent"] != nil {
		t.Fatal(stale)
	}
}
