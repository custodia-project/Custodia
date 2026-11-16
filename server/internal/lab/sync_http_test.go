package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHTTPSource struct {
	mu    sync.Mutex
	pages map[string][]struct {
		objects []map[string]any
		next    string
	}
	status map[string]int
}

func newFakeHTTPSource() *fakeHTTPSource {
	return &fakeHTTPSource{pages: map[string][]struct {
		objects []map[string]any
		next    string
	}{}, status: map[string]int{}}
}

func (f *fakeHTTPSource) set(prefix string, pages ...struct {
	objects []map[string]any
	next    string
}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[prefix] = pages
}

func (f *fakeHTTPSource) setStatus(prefix string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[prefix] = status
}

func (f *fakeHTTPSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := "/a"
	if strings.HasPrefix(r.URL.Path, "/b/") {
		prefix = "/b"
	}
	if status := f.status[prefix]; status != 0 {
		w.WriteHeader(status)
		return
	}
	pages := f.pages[prefix]
	cursor := r.URL.Query().Get("cursor")
	index := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "c%d", &index); err != nil || index < 1 || index > len(pages) {
			w.WriteHeader(400)
			return
		}
	}
	if index >= len(pages) {
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"objects": pages[index].objects, "next_cursor": pages[index].next})
}

func newSyncHTTPLab(t *testing.T) (*Store, Principal, *httptest.Server, *fakeHTTPSource) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	s, p := newSyncStore(t, now)
	fake := newFakeHTTPSource()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	for _, item := range []struct {
		id, prefix, scope string
	}{
		{"lab-http-a", "/a", "scope-a"},
		{"lab-http-b", "/b", "scope-b"},
	} {
		if err := s.AddHTTPSource(ctx, "host", SyncSource{
			ConnectionID: item.id, Connector: syncConnector, BaseURL: server.URL + item.prefix,
			Collection: "users", Scope: item.scope, Generation: "test-v1",
		}, []string{p.Subject}, now); err != nil {
			t.Fatal(err)
		}
	}
	fake.set("/a",
		struct {
			objects []map[string]any
			next    string
		}{objects: []map[string]any{
			{"external_id": "same-id", "attributes": map[string]string{"email": "a@acme.example", "name": "A"}},
			{"external_id": "a-2", "attributes": map[string]string{"email": "a2@acme.example", "name": "A2"}},
		}, next: "c1"},
		struct {
			objects []map[string]any
			next    string
		}{objects: []map[string]any{
			{"external_id": "a-3", "attributes": map[string]string{"email": "a3@acme.example", "name": "A3"}},
		}},
	)
	fake.set("/b",
		struct {
			objects []map[string]any
			next    string
		}{objects: []map[string]any{
			{"external_id": "same-id", "attributes": map[string]string{"email": "b@acme.example", "name": "B"}},
		}},
	)
	return s, p, server, fake
}

func syncRequest(t *testing.T, h http.Handler, token, method, path, body string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func TestSyncHTTPRunPaginationResultsAndIsolation(t *testing.T) {
	s, _, server, fake := newSyncHTTPLab(t)
	_ = server
	now := time.Now().UTC()
	h := s.Handler("lab-key", func() time.Time { return now })

	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	if code != 202 {
		t.Fatal(code, body)
	}
	var started struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" || started.Status != "completed" {
		t.Fatal(err, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs?limit=10", "")
	if code != 200 || !strings.Contains(body, started.RunID) || !strings.Contains(body, `"received_object_count":3`) || !strings.Contains(body, `"pages":2`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+started.RunID, "")
	if code != 200 || !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"connection_id":"lab-http-a"`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+started.RunID+"/stages", "")
	if code != 200 || !strings.Contains(body, `"stage":"extract"`) || !strings.Contains(body, `"item_count":3`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records", "")
	if code != 200 || !strings.Contains(body, `"external_id":"same-id"`) || strings.Count(body, `"external_id"`) != 3 || !strings.Contains(body, `"last_run_id"`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-b/runs", "{}")
	if code != 202 || !strings.Contains(body, `"status":"completed"`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-b/records", "")
	if code != 200 || !strings.Contains(body, `"name":"B"`) || strings.Contains(body, `"name":"A"`) || strings.Count(body, `"external_id"`) != 1 {
		t.Fatal("connection B records leaked or missing", code, body)
	}
	// A source failure on the first page leaves a failed run with no
	// publication (the page-2 partial case is covered below).
	fake.setStatus("/a", 500)
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	if code != 202 || !strings.Contains(body, `"status":"failed"`) {
		// A first-page failure is a failed run with no publication.
		t.Fatal(code, body)
	}
	fake.setStatus("/a", 0)
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("failed run changed records", code, body)
	}
}

func TestSyncHTTPPartialPageKeepsBaseline(t *testing.T) {
	s, _, _, fake := newSyncHTTPLab(t)
	now := time.Now().UTC()
	h := s.Handler("lab-key", func() time.Time { return now })
	if code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}"); code != 202 {
		t.Fatal(code, body)
	}
	// First page succeeds; the fake has no page for cursor c1, so it returns
	// 500 and the run becomes partial with the two observations.
	fake.set("/a",
		struct {
			objects []map[string]any
			next    string
		}{objects: []map[string]any{
			{"external_id": "same-id", "attributes": map[string]string{"email": "a@acme.example", "name": "A"}},
			{"external_id": "a-2", "attributes": map[string]string{"email": "a2@acme.example", "name": "A2"}},
		}, next: "c1"},
	)
	// The fake serves the cursor c1 as page index 1, which is absent -> 500.
	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	if code != 202 || !strings.Contains(body, `"status":"partial"`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 || !strings.Contains(body, `"a-3"`) {
		t.Fatal("partial did not retain the unseen record", code, body)
	}
	// The page that was read updated same-id and a-2, while a-3 keeps the
	// previous run and remains active because a partial never retires.
	if strings.Count(body, `"last_run_id"`) != 3 {
		t.Fatal("partial lost provenance", code, body)
	}
	// A complete run with the same scope and only one of the two records can
	// now retire the missing one because the partial never replaced the
	// complete baseline.
	fake.set("/a",
		struct {
			objects []map[string]any
			next    string
		}{objects: []map[string]any{
			{"external_id": "same-id", "attributes": map[string]string{"email": "a@acme.example", "name": "A"}},
		}},
	)
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	if code != 202 || !strings.Contains(body, `"status":"completed"`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"a-2"`) || !strings.Contains(body, `"retired_by_run_id"`) {
		t.Fatal("complete comparable run did not retire absence", code, body)
	}
}

func TestSyncHTTPResumesQueuedAndExpiredRuns(t *testing.T) {
	s, p, _, _ := newSyncHTTPLab(t)
	ctx := context.Background()
	now := time.Now().UTC()
	h := s.Handler("lab-key", func() time.Time { return now })
	queued, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now)
	if err != nil {
		t.Fatal(err)
	}
	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	var resumed struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	json.Unmarshal([]byte(body), &resumed)
	if code != 202 || resumed.RunID != queued || resumed.Status != "completed" {
		t.Fatal("queued run was not resumed", code, body)
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_sync_runs WHERE connection_id='lab-http-a'").Scan(&count); err != nil || count != 1 {
		t.Fatal("resume created a duplicate run", err, count)
	}
	// A running run whose lease expired is requeued and resumed by the same
	// explicit request; the fencing lets attempt_count advance to two.
	expired, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.claimSyncRun(ctx, expired, "worker-old", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE lab_sync_runs SET lease_expires_at=? WHERE run_id=?", now.Add(-time.Minute).Format(time.RFC3339), expired); err != nil {
		t.Fatal(err)
	}
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	json.Unmarshal([]byte(body), &resumed)
	if code != 202 || resumed.RunID != expired || resumed.Status != "completed" {
		t.Fatal("expired run was not resumed", code, body)
	}
	run, err := s.SyncRun(ctx, p, expired)
	if err != nil || run.AttemptCount != 2 {
		t.Fatal(err, run)
	}
	var requeues int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='sync_requeued'").Scan(&requeues); err != nil || requeues != 1 {
		t.Fatal("expired lease was not audited", err, requeues)
	}
}

func TestSyncHTTPPermissionsAndIdempotency(t *testing.T) {
	s, p, _, _ := newSyncHTTPLab(t)
	now := time.Now().UTC()
	h := s.Handler("lab-key", func() time.Time { return now })
	if err := s.IssuePrincipal(context.Background(), "sync-reader", "reader-key", []string{"lab-http-a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(context.Background(), "host", "custodia-lab", "sync-reader", "lab-http-a", false, now); err != nil {
		t.Fatal(err)
	}
	if err := s.IssuePrincipal(context.Background(), "sync-none", "none-key", nil); err != nil {
		t.Fatal(err)
	}
	_, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	var runA struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal([]byte(body), &runA)
	_, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-b/runs", "{}")
	var runB struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal([]byte(body), &runB)
	if runA.RunID == "" || runB.RunID == "" {
		t.Fatal("setup runs missing", body)
	}
	if code, body := syncRequest(t, h, "reader-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}"); code != 403 {
		t.Fatal("read-only principal started a run", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs/"+runA.RunID, ""); code != 200 || !strings.Contains(body, `"connection_id":"lab-http-a"`) {
		t.Fatal("read-only principal cannot read its run", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs/"+runB.RunID, ""); code != 404 {
		t.Fatal("hidden run detail leaked", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs/"+runB.RunID+"/stages", ""); code != 404 {
		t.Fatal("hidden run stages leaked", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs", ""); code != 200 || strings.Contains(body, "lab-http-b") {
		t.Fatal("run list leaked a hidden connection", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/schedules", ""); code != 200 || strings.Contains(body, "lab-http-b") || !strings.Contains(body, "lab-http-a") {
		t.Fatal("schedule list leaked a hidden connection", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/connections/lab-http-b/records", ""); code != 403 {
		t.Fatal("hidden connection records returned data", code, body)
	}
	if code, body := syncRequest(t, h, "none-key", "GET", "/api/ingestion/runs", ""); code != 200 || !strings.Contains(body, `"rows":[]`) {
		t.Fatal("ungranted principal saw runs", code, body)
	}
	if code, body := syncRequest(t, h, "none-key", "GET", "/api/ingestion/runs/"+runA.RunID, ""); code != 404 {
		t.Fatal("ungranted principal read a run detail", code, body)
	}
	if code, body := syncRequest(t, h, "none-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}"); code != 404 {
		t.Fatal("ungranted principal discovered a connection", code, body)
	}
	// The initial synthetic principal can run and idempotency reuses the run.
	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}")
	if code != 202 {
		t.Fatal(code, body)
	}
	var first struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal([]byte(body), &first)
	r := httptest.NewRequest("POST", "/api/ingestion/schedules/lab-http-a/runs", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer lab-key")
	r.Header.Set("Idempotency-Key", "cart-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var second struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &second)
	r = httptest.NewRequest("POST", "/api/ingestion/schedules/lab-http-a/runs", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer lab-key")
	r.Header.Set("Idempotency-Key", "cart-1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var third struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &third)
	if second.RunID == "" || third.RunID != second.RunID || first.RunID == second.RunID {
		t.Fatal("idempotency did not reuse the run", first, second, third)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM lab_sync_runs WHERE connection_id='lab-http-a'").Scan(&count); err != nil || count != 3 {
		t.Fatal("idempotent request duplicated a run", err, count)
	}
	if code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", `{"unexpected":true}`); code != 400 {
		t.Fatal("unknown sync option accepted", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/auth/status", ""); code != 200 || strings.Contains(body, "netskope:read") || strings.Contains(body, "ingestion:run") {
		t.Fatal("sync connection received provider-specific permissions", code, body)
	}
	_ = p
}
