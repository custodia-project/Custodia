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
	return newSyncHTTPLabMode(t, true)
}

func newSyncHTTPLabWithoutWorker(t *testing.T) (*Store, Principal, *httptest.Server, *fakeHTTPSource) {
	return newSyncHTTPLabMode(t, false)
}

func newSyncHTTPLabMode(t *testing.T, worker bool) (*Store, Principal, *httptest.Server, *fakeHTTPSource) {
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
	if worker {
		workerCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.RunSyncWorker(workerCtx, 10*time.Millisecond)
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
	}
	return s, p, server, fake
}

func waitForTerminal(t *testing.T, s *Store, p Principal, runID string) SyncRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := s.SyncRun(context.Background(), p, runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "queued" && run.Status != "running" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", runID)
	return SyncRun{}
}

func startRun(t *testing.T, h http.Handler, token, connection string) string {
	t.Helper()
	code, body := syncRequest(t, h, token, "POST", "/api/ingestion/schedules/"+connection+"/runs", "{}")
	if code != 202 {
		t.Fatalf("start %s: %d %s", connection, code, body)
	}
	var started struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" || started.Status != "queued" {
		t.Fatalf("start %s: %v %s", connection, err, body)
	}
	return started.RunID
}

func startRunWithKey(t *testing.T, h http.Handler, token, connection, key string) string {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/ingestion/schedules/"+connection+"/runs", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("start %s: %d %s", connection, w.Code, w.Body.String())
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil || started.RunID == "" {
		t.Fatalf("start %s: %v %s", connection, err, w.Body.String())
	}
	return started.RunID
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
	s, p, _, fake := newSyncHTTPLab(t)
	now := time.Now().UTC()
	h := s.Handler("lab-key", func() time.Time { return now })

	runA := startRun(t, h, "lab-key", "lab-http-a")
	if completed := waitForTerminal(t, s, p, runA); completed.Status != "completed" || completed.Pages != 2 || completed.Received != 3 {
		t.Fatal(completed)
	}
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs?limit=10", "")
	if code != 200 || !strings.Contains(body, runA) || !strings.Contains(body, `"received_object_count":3`) || !strings.Contains(body, `"pages":2`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+runA, "")
	if code != 200 || !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"connection_id":"lab-http-a"`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+runA+"/stages", "")
	if code != 200 || !strings.Contains(body, `"stage":"extract"`) || !strings.Contains(body, `"item_count":3`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records", "")
	if code != 200 || !strings.Contains(body, `"external_id":"same-id"`) || strings.Count(body, `"external_id"`) != 3 || !strings.Contains(body, `"last_run_id"`) {
		t.Fatal(code, body)
	}
	runB := startRun(t, h, "lab-key", "lab-http-b")
	if completed := waitForTerminal(t, s, p, runB); completed.Status != "completed" {
		t.Fatal(completed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-b/records", "")
	if code != 200 || !strings.Contains(body, `"name":"B"`) || strings.Contains(body, `"name":"A"`) || strings.Count(body, `"external_id"`) != 1 {
		t.Fatal("connection B records leaked or missing", code, body)
	}
	// A source failure on the first page leaves a failed run with no
	// publication (the page-2 partial case is covered below).
	fake.setStatus("/a", 500)
	runFailed := startRun(t, h, "lab-key", "lab-http-a")
	if failed := waitForTerminal(t, s, p, runFailed); failed.Status != "failed" {
		t.Fatal(failed)
	}
	fake.setStatus("/a", 0)
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("failed run changed records", code, body)
	}
}

func TestSyncHTTPPartialPageKeepsBaseline(t *testing.T) {
	s, p, _, fake := newSyncHTTPLab(t)
	now := time.Now().UTC()
	h := s.Handler("lab-key", func() time.Time { return now })
	first := startRun(t, h, "lab-key", "lab-http-a")
	if completed := waitForTerminal(t, s, p, first); completed.Status != "completed" {
		t.Fatal(completed)
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
	partial := startRun(t, h, "lab-key", "lab-http-a")
	if run := waitForTerminal(t, s, p, partial); run.Status != "partial" {
		t.Fatal(run)
	}
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records", "")
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
	final := startRun(t, h, "lab-key", "lab-http-a")
	if run := waitForTerminal(t, s, p, final); run.Status != "completed" {
		t.Fatal(run)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/lab-http-a/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"a-2"`) || !strings.Contains(body, `"retired_by_run_id"`) {
		t.Fatal("complete comparable run did not retire absence", code, body)
	}
}

func TestSyncHTTPResumesQueuedAndExpiredRuns(t *testing.T) {
	s, p, _, _ := newSyncHTTPLabWithoutWorker(t)
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
	if code != 202 || resumed.RunID != queued || resumed.Status != "queued" {
		t.Fatal("queued run was not resumed", code, body)
	}
	if err = s.ProcessNextSyncRun(ctx, "worker-resume", func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	if run := waitForTerminal(t, s, p, queued); run.AttemptCount != 1 || run.Status != "completed" {
		t.Fatal(run)
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
	if code != 202 || resumed.RunID != expired || resumed.Status != "queued" {
		t.Fatal("expired run was not requeued", code, body)
	}
	if err = s.ProcessNextSyncRun(ctx, "worker-resume-2", func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	run, err := s.SyncRun(ctx, p, expired)
	if err != nil || run.AttemptCount != 2 || run.Status != "completed" {
		t.Fatal(err, run)
	}
	var requeues int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='sync_requeued'").Scan(&requeues); err != nil || requeues != 1 {
		t.Fatal("expired lease was not audited", err, requeues)
	}
}

func TestSyncHTTPAllRunsScheduleAndStageHistory(t *testing.T) {
	s, p, _, _ := newSyncHTTPLab(t)
	ctx := context.Background()
	now := time.Now().UTC()
	h := s.Handler("lab-key", func() time.Time { return now })
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/auth/status", "")
	if code != 200 || !strings.Contains(body, `"ingestion:run"`) {
		t.Fatal("run capability not advertised", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/all/runs?scope=all", `{"max_safes":0,"page_size":100,"member_delay_seconds":0.3,"reconcile_complete":true}`)
	if code != 202 {
		t.Fatal(code, body)
	}
	var batch struct {
		Runs []struct {
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(body), &batch); err != nil || len(batch.Runs) != 2 {
		t.Fatal(err, body)
	}
	for _, run := range batch.Runs {
		if run.Status != "queued" {
			t.Fatal(run)
		}
		if completed := waitForTerminal(t, s, p, run.RunID); completed.Status != "completed" {
			t.Fatal(completed)
		}
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/sources/netskope/stages/history?runs=5", "")
	if code != 200 || !strings.Contains(body, `"stage":"extract"`) || !strings.Contains(body, `"completed_at"`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/sources/netskope/stages/latest", "")
	if code != 200 || !strings.Contains(body, `"last_status":"completed"`) || !strings.Contains(body, `"last_attempt_at"`) {
		t.Fatal(code, body)
	}
	if err := s.RecordConnectionProbe(context.Background(), "lab-http-a", true, "test probe", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	code, body = syncRequest(t, h, "lab-key", "PUT", "/api/ingestion/schedules/lab-http-a", `{"interval_minutes":5}`)
	if code != 200 || !strings.Contains(body, `"interval_minutes":5`) {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/schedules", "")
	if code != 200 || !strings.Contains(body, `"interval_minutes":5`) || !strings.Contains(body, `"interval_source":"administrative"`) || !strings.Contains(body, `"freshness":"fresh"`) {
		t.Fatal(code, body)
	}
	if code, body = syncRequest(t, h, "lab-key", "PUT", "/api/ingestion/schedules/lab-http-a", `{"interval_minutes":3}`); code != 400 {
		t.Fatal("invalid interval accepted", code, body)
	}
	if code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/all/runs?scope=cyberark", `{}`); code != 202 || !strings.Contains(body, `"runs":[]`) {
		t.Fatal("empty CyberArk scope failed", code, body)
	}
	if err := s.IssuePrincipal(ctx, "sync-reader", "reader-key", []string{"lab-http-a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(ctx, "host", "custodia-lab", "sync-reader", "lab-http-a", false, now); err != nil {
		t.Fatal(err)
	}
	if code, body = syncRequest(t, h, "reader-key", "POST", "/api/ingestion/all/runs", "{}"); code != 403 {
		t.Fatal("read-only principal started all runs", code, body)
	}
	if code, body = syncRequest(t, h, "reader-key", "GET", "/api/auth/status", ""); code != 200 || strings.Contains(body, `"ingestion:run"`) {
		t.Fatal("read-only principal received run capability", code, body)
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
	runA := startRun(t, h, "lab-key", "lab-http-a")
	if completed := waitForTerminal(t, s, p, runA); completed.Status != "completed" {
		t.Fatal(completed)
	}
	runB := startRun(t, h, "lab-key", "lab-http-b")
	if completed := waitForTerminal(t, s, p, runB); completed.Status != "completed" {
		t.Fatal(completed)
	}
	if code, body := syncRequest(t, h, "reader-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}"); code != 403 {
		t.Fatal("read-only principal started a run", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs/"+runA, ""); code != 200 || !strings.Contains(body, `"connection_id":"lab-http-a"`) {
		t.Fatal("read-only principal cannot read its run", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs/"+runB, ""); code != 404 {
		t.Fatal("hidden run detail leaked", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs/"+runB+"/stages", ""); code != 404 {
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
	if code, body := syncRequest(t, h, "none-key", "GET", "/api/ingestion/runs/"+runA, ""); code != 404 {
		t.Fatal("ungranted principal read a run detail", code, body)
	}
	if code, body := syncRequest(t, h, "none-key", "POST", "/api/ingestion/schedules/lab-http-a/runs", "{}"); code != 404 {
		t.Fatal("ungranted principal discovered a connection", code, body)
	}
	// The initial synthetic principal can run and idempotency reuses the run.
	first := startRun(t, h, "lab-key", "lab-http-a")
	if completed := waitForTerminal(t, s, p, first); completed.Status != "completed" {
		t.Fatal(completed)
	}
	second := startRunWithKey(t, h, "lab-key", "lab-http-a", "cart-1")
	if completed := waitForTerminal(t, s, p, second); completed.Status != "completed" {
		t.Fatal(completed)
	}
	third := startRunWithKey(t, h, "lab-key", "lab-http-a", "cart-1")
	if second == "" || third != second || first == second {
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
