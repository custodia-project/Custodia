package lab

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"custodia/server/internal/inventory"
)

func newSyncStore(t *testing.T, now time.Time) (*Store, Principal) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	p, err := s.principal(context.Background(), "synthetic-lab")
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func addSyncSource(t *testing.T, s *Store, p Principal, id, collection, scope string, now time.Time) {
	t.Helper()
	source := SyncSource{
		ConnectionID: id, Connector: syncConnector, BaseURL: "http://127.0.0.1:18080",
		Collection: collection, Scope: scope, Generation: "test-v1",
	}
	if err := s.AddHTTPSource(context.Background(), "host", source, []string{p.Subject}, now); err != nil {
		t.Fatal(err)
	}
}

func runSync(t *testing.T, s *Store, p Principal, connection string, now time.Time, result syncResult) SyncRun {
	t.Helper()
	runID, err := s.EnqueueSyncRun(context.Background(), p, connection, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.claimSyncRun(context.Background(), runID, "worker-test", now); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.SyncRun(context.Background(), p, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishSyncRun(context.Background(), runID, "worker-test", claimed.AttemptCount, result, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	run, err := s.SyncRun(context.Background(), p, runID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func object(external string, attributes map[string]string) inventory.Object {
	return inventory.Object{ExternalID: external, Attributes: attributes}
}

func TestSyncMigrationIsAdditiveAndIdempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.DB.QueryRow("SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(err, version)
	}
	for _, column := range []string{"status", "first_run_id", "last_run_id", "scope", "generation", "updated_at", "retired_at", "retired_by_run_id"} {
		var count int
		if err := s.DB.QueryRow("SELECT count(*) FROM pragma_table_info('lab_observations') WHERE name=?", column).Scan(&count); err != nil || count != 1 {
			t.Fatal("missing additive column", column, err, count)
		}
	}
	var runColumns int
	if err := s.DB.QueryRow("SELECT count(*) FROM pragma_table_info('lab_sync_runs') WHERE name='source_url'").Scan(&runColumns); err != nil || runColumns != 1 {
		t.Fatal("missing run source_url", err, runColumns)
	}
	// The seeded capture and its normalized observations survive the upgrade.
	var observations int
	if err := s.DB.QueryRow("SELECT count(*) FROM lab_observations WHERE connection_id=? AND status='active'", NetskopeConnection).Scan(&observations); err != nil || observations == 0 {
		t.Fatal(err, observations)
	}
	if p.Subject != "synthetic-lab" {
		t.Fatal(p)
	}
}

func TestSyncMigrationUpgradesExistingObservations(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, _ := newSyncStore(t, now)
	var before []byte
	if err := s.DB.QueryRow("SELECT document FROM lab_observations WHERE connection_id=? AND collection='users' ORDER BY row_id LIMIT 1", NetskopeConnection).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP TABLE lab_sync_reviews", "DROP TABLE lab_sync_baselines", "DROP TABLE lab_sync_runs",
		"DROP TABLE lab_sync_grants", "DROP TABLE lab_sync_sources",
		"ALTER TABLE lab_observations DROP COLUMN retired_by_run_id",
		"ALTER TABLE lab_observations DROP COLUMN retired_at",
		"ALTER TABLE lab_observations DROP COLUMN updated_at",
		"ALTER TABLE lab_observations DROP COLUMN generation",
		"ALTER TABLE lab_observations DROP COLUMN scope",
		"ALTER TABLE lab_observations DROP COLUMN last_run_id",
		"ALTER TABLE lab_observations DROP COLUMN first_run_id",
		"ALTER TABLE lab_observations DROP COLUMN status",
		"DELETE FROM lab_schema_migrations WHERE version=5",
	} {
		if _, err := s.DB.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	if _, err := s.Load(ctx); err == nil {
		t.Fatal("version-4 database served without migration")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var after []byte
	var status, first, last string
	if err := s.DB.QueryRow("SELECT document,status,first_run_id,last_run_id FROM lab_observations WHERE connection_id=? AND collection='users' ORDER BY row_id LIMIT 1", NetskopeConnection).Scan(&after, &status, &first, &last); err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || status != "active" || first != "" || last != "" {
		t.Fatal("migration changed an existing observation", status, first, last)
	}
	var version int
	if err := s.DB.QueryRow("SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(err, version)
	}
}

func TestSyncConfigurationRejectsNonLoopbackAndDuplicate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	base := SyncSource{ConnectionID: "lab-http-a", Connector: syncConnector, BaseURL: "http://192.0.2.1:18080", Collection: "users", Scope: "scope-a", Generation: "test-v1"}
	if err := s.AddHTTPSource(ctx, "host", base, nil, now); err == nil {
		t.Fatal("non-loopback source accepted")
	}
	base.Connector = "other"
	base.BaseURL = "http://127.0.0.1:18080"
	if err := s.AddHTTPSource(ctx, "host", base, nil, now); err == nil {
		t.Fatal("unknown connector accepted")
	}
	base.Connector = syncConnector
	base.Collection = "secrets"
	if err := s.AddHTTPSource(ctx, "host", base, nil, now); err == nil {
		t.Fatal("unsupported collection accepted")
	}
	base.Collection = "users"
	base.ConnectionID = "Lab HTTP"
	if err := s.AddHTTPSource(ctx, "host", base, nil, now); err == nil {
		t.Fatal("invalid connection ID accepted")
	}
	base.ConnectionID = "lab-http-a"
	if err := s.AddHTTPSource(ctx, "host", base, []string{"missing-subject"}, now); err == nil {
		t.Fatal("unknown grant subject accepted")
	}
	if err := s.AddHTTPSource(ctx, "host", base, []string{p.Subject}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHTTPSource(ctx, "host", base, nil, now); err == nil {
		t.Fatal("duplicate connection accepted")
	}
	var audit int
	if err := s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='sync_configure'").Scan(&audit); err != nil || audit != 1 {
		t.Fatal(err, audit)
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='sync_grant'").Scan(&audit); err != nil || audit < 1 {
		t.Fatal("configuration grant was not audited", err, audit)
	}
}

func TestSyncCompletePublishesAndRetiresOnlyComparableAbsences(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	first := runSync(t, s, p, "lab-http-a", now, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 2,
		Objects: []inventory.Object{
			object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"}),
			object("u2", map[string]string{"email": "bob@acme.example", "name": "Bob"}),
		},
	})
	if first.Status != inventory.StatusCompleted || first.Added != 0 || first.Retired != 0 || first.Received != 2 {
		t.Fatal(first)
	}
	second := runSync(t, s, p, "lab-http-a", now.Add(time.Minute), syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"})},
	})
	if second.Retired != 1 || second.Retained != 0 || second.Added != 0 || second.Changed != 0 {
		t.Fatal(second)
	}
	records, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10)
	if err != nil || len(records) != 1 || records[0].ExternalID != "u1" || records[0].LastRunID != second.RunID {
		t.Fatal(err, records)
	}
	retired, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "retired", 10)
	if err != nil || len(retired) != 1 || retired[0].ExternalID != "u2" || retired[0].RetiredByRunID != second.RunID {
		t.Fatal(err, retired)
	}
	// A complete run with a new scope is a new baseline: it does not retire
	// records observed under the previous scope.
	if _, err := s.DB.Exec("UPDATE lab_sync_sources SET scope='scope-b' WHERE connection_id='lab-http-a'"); err != nil {
		t.Fatal(err)
	}
	// Enqueue copies the current source scope into the run.
	third := runSync(t, s, p, "lab-http-a", now.Add(2*time.Minute), syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u3", map[string]string{"email": "carol@acme.example", "name": "Carol"})},
	})
	if third.Retired != 0 {
		t.Fatal("new scope retired old-scope records", third)
	}
	records, err = s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10)
	if err != nil || len(records) != 2 {
		t.Fatal(err, records)
	}
}

func TestSyncPartialPublishesButNeverRetiresOrReplacesBaseline(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runSync(t, s, p, "lab-http-a", now, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 2,
		Objects: []inventory.Object{
			object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"}),
			object("u2", map[string]string{"email": "bob@acme.example", "name": "Bob"}),
		},
	})
	partial := runSync(t, s, p, "lab-http-a", now.Add(time.Minute), syncResult{
		Status: inventory.StatusPartial, Pages: 1, Received: 1, ErrorCode: "http_status_500", ErrorDetail: "page 2 failed",
		Warnings: []string{"page_failed"},
		Objects:  []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice Updated"})},
	})
	if partial.Status != inventory.StatusPartial || partial.Retired != 0 || partial.Retained != 1 {
		t.Fatal(partial)
	}
	records, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10)
	if err != nil || len(records) != 2 {
		t.Fatal(err, records)
	}
	// The partial changed the observed attribute but did not touch the
	// complete baseline; the next complete run still compares with it.
	third := runSync(t, s, p, "lab-http-a", now.Add(2*time.Minute), syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice Updated"})},
	})
	if third.Retired != 1 || third.Changed != 0 {
		t.Fatal(third)
	}
}

func TestSyncFailureLeavesObservationsAndAuditAtomic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runSync(t, s, p, "lab-http-a", now, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"})},
	})
	before, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "all", 10)
	if err != nil {
		t.Fatal(err)
	}
	failed := runSync(t, s, p, "lab-http-a", now.Add(time.Minute), syncResult{
		Status: inventory.StatusFailed, ErrorCode: "timeout", ErrorDetail: "source timed out",
	})
	if failed.Status != inventory.StatusFailed || failed.Received != 0 {
		t.Fatal(failed)
	}
	after, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "all", 10)
	if err != nil || len(after) != len(before) || after[0].UpdatedAt != before[0].UpdatedAt || after[0].LastRunID != before[0].LastRunID {
		t.Fatal("failed run changed observations", err, after)
	}
	if _, err = s.DB.Exec(`CREATE TRIGGER fail_sync_audit BEFORE INSERT ON lab_audit WHEN NEW.action='sync_complete' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	runID, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.claimSyncRun(ctx, runID, "worker-test", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	} else if err = s.finishSyncRun(ctx, runID, "worker-test", claimed.AttemptCount, syncResult{
		Status: inventory.StatusFailed, ErrorCode: "timeout",
	}, now.Add(3*time.Minute)); err == nil {
		t.Fatal("audit failure committed a terminal run")
	}
	run, err := s.SyncRun(ctx, p, runID)
	if err != nil || run.Status != "running" {
		t.Fatal("run was not left fenced for retry", err, run.Status)
	}
}

func TestSyncRunsAreIdempotentAndOneActivePerConnection(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runID, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "cart-1", now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "cart-1", now)
	if err != nil || again != runID {
		t.Fatal("idempotency key created another run", err, again)
	}
	if _, err = s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now); !errors.Is(err, ErrSyncActive) {
		t.Fatal("second active run accepted", err)
	}
	claimed, err := s.claimSyncRun(ctx, runID, "worker-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishSyncRun(ctx, runID, "worker-test", claimed.AttemptCount, syncResult{Status: inventory.StatusCompleted, Pages: 1}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now.Add(time.Minute)); err != nil {
		t.Fatal("completed run blocked the next one", err)
	}
	var audits int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='sync_queued'").Scan(&audits); err != nil || audits != 2 {
		t.Fatal(err, audits)
	}
}

func TestSyncProcessTerminalizesAfterHeartbeatFailure(t *testing.T) {
	s, p, _, _ := newSyncHTTPLab(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_progress BEFORE UPDATE ON lab_sync_runs WHEN NEW.status_message LIKE 'Página %' BEGIN SELECT RAISE(ABORT,'test heartbeat failure'); END`); err != nil {
		t.Fatal(err)
	}
	runID, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessSyncRun(ctx, runID, "worker-progress", func() time.Time { return now }); err == nil {
		t.Fatal("heartbeat failure was not reported")
	}
	run, err := s.SyncRun(ctx, p, runID)
	if err != nil || run.Status == "running" || run.Status == "queued" {
		t.Fatal("heartbeat failure left a stuck run", err, run)
	}
}

func TestSyncLeaseRequeueIsAuditedAndRecoverable(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runID, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.claimSyncRun(ctx, runID, "worker-test", now); err != nil {
		t.Fatal(err)
	} else if claimed.AttemptCount != 1 {
		t.Fatal("first attempt", claimed)
	}
	if _, err = s.DB.Exec("UPDATE lab_sync_runs SET lease_expires_at=? WHERE run_id=?", now.Add(-time.Minute).UTC().Format(time.RFC3339), runID); err != nil {
		t.Fatal(err)
	}
	count, err := s.RequeueExpiredSyncRuns(ctx, now)
	if err != nil || count != 1 {
		t.Fatal(err, count)
	}
	run, err := s.SyncRun(ctx, p, runID)
	if err != nil || run.Status != "queued" || run.WorkerID != "" {
		t.Fatal(err, run)
	}
	// The old worker cannot heartbeat or publish after its lease was requeued.
	if err = s.progressSyncRun(ctx, runID, "worker-test", 1, "stale heartbeat", now.Add(time.Second)); !errors.Is(err, ErrSyncConflict) {
		t.Fatal("expired worker renewed its lease", err)
	}
	if err = s.finishSyncRun(ctx, runID, "worker-test", 1, syncResult{
		Status: inventory.StatusCompleted, Pages: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"})},
	}, now.Add(time.Second)); !errors.Is(err, ErrSyncConflict) {
		t.Fatal("expired worker published after requeue", err)
	}
	if records, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10); err != nil || len(records) != 0 {
		t.Fatal("expired worker changed observations", err, records)
	}
	// Simulate the same worker name being reused by the next attempt. The
	// attempt counter, not the name, is the fencing token.
	reclaimed, err := s.claimSyncRun(ctx, runID, "worker-test", now.Add(time.Second))
	if err != nil || reclaimed.AttemptCount != 2 {
		t.Fatal(err, reclaimed)
	}
	if err = s.finishSyncRun(ctx, runID, "worker-test", 1, syncResult{Status: inventory.StatusCompleted, Pages: 1}, now.Add(2*time.Second)); !errors.Is(err, ErrSyncConflict) {
		t.Fatal("stale attempt published after a new claim", err)
	}
	if err = s.finishSyncRun(ctx, runID, "worker-test", 2, syncResult{Status: inventory.StatusCompleted, Pages: 1}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='sync_requeued'").Scan(&audits); err != nil || audits != 1 {
		t.Fatal(err, audits)
	}
}

func TestSyncAuthorizationAndTwoConnectionIsolation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	addSyncSource(t, s, p, "lab-http-b", "users", "scope-b", now)
	runSync(t, s, p, "lab-http-a", now, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("same-id", map[string]string{"email": "a@acme.example", "name": "A"})},
	})
	runSync(t, s, p, "lab-http-b", now, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("same-id", map[string]string{"email": "b@acme.example", "name": "B"})},
	})
	a, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10)
	if err != nil || len(a) != 1 || a[0].Attributes["name"] != "A" {
		t.Fatal(err, a)
	}
	b, err := s.SyncRecords(ctx, p, "lab-http-b", "users", "active", 10)
	if err != nil || len(b) != 1 || b[0].Attributes["name"] != "B" {
		t.Fatal(err, b)
	}
	if err = s.IssuePrincipal(ctx, "read-only", "read-key", []string{"lab-http-a"}); err != nil {
		t.Fatal(err)
	}
	readOnly, err := s.credential(ctx, "read-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.GrantSync(ctx, "host", "custodia-lab", "read-only", "lab-http-a", false, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueSyncRun(ctx, readOnly, "lab-http-a", "", now); !errors.Is(err, ErrDenied) {
		t.Fatal("read-only principal enqueued a run", err)
	}
	runs, err := s.SyncRuns(ctx, readOnly, 10)
	if err != nil || len(runs) != 1 || runs[0].ConnectionID != "lab-http-a" {
		t.Fatal(err, runs)
	}
	if _, err = s.SyncRun(ctx, readOnly, runs[0].RunID); err != nil {
		t.Fatal("read-only principal cannot read its run", err)
	}
	if _, err = s.SyncRecords(ctx, readOnly, "lab-http-b", "users", "active", 10); !errors.Is(err, ErrDenied) {
		t.Fatal("hidden connection records leaked", err)
	}
	// Revocation removes both the visible history and the ability to enqueue.
	if err = s.RevokePrincipal(ctx, "read-only"); err != nil {
		t.Fatal(err)
	}
	if runs, err = s.SyncRuns(ctx, readOnly, 10); err != nil || len(runs) != 0 {
		t.Fatal("revoked principal retained runs", err, runs)
	}
	if _, err = s.EnqueueSyncRun(ctx, readOnly, "lab-http-a", "", now); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked principal enqueued a run", err)
	}
	if _, err = s.SyncRecords(ctx, readOnly, "lab-http-a", "users", "active", 10); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked principal read observations", err)
	}
}

func TestSyncReviewSurvivesObservationRefresh(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runSync(t, s, p, "lab-http-a", now, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"})},
	})
	if err := s.SetSyncReview(ctx, p, "lab-http-a", "users", "u1", "owner-a", "reviewed", now); err != nil {
		t.Fatal(err)
	}
	runSync(t, s, p, "lab-http-a", now.Add(time.Minute), syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice Renamed"})},
	})
	review, err := s.SyncReview(ctx, p, "lab-http-a", "users", "u1")
	if err != nil || review.Owner != "owner-a" || review.Note != "reviewed" {
		t.Fatal(err, review)
	}
	var reviewAudits int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='sync_review'").Scan(&reviewAudits); err != nil || reviewAudits != 1 {
		t.Fatal("review was not audited", err, reviewAudits)
	}
	records, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10)
	if err != nil || records[0].Attributes["name"] != "Alice Renamed" {
		t.Fatal(err, records)
	}
}

func TestSyncZeroConfirmedAndRepeatedSnapshotDoNotDuplicate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	objects := []inventory.Object{
		object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"}),
		object("u2", map[string]string{"email": "bob@acme.example", "name": "Bob"}),
	}
	first := runSync(t, s, p, "lab-http-a", now, syncResult{Status: inventory.StatusCompleted, Pages: 1, Received: 2, Objects: objects})
	if first.Received != 2 {
		t.Fatal(first)
	}
	// A complete, comparable empty collection is a confirmed zero: it retires
	// the active records rather than being treated as a failed read.
	zero := runSync(t, s, p, "lab-http-a", now.Add(time.Minute), syncResult{Status: inventory.StatusCompleted, Pages: 1, Received: 0})
	if zero.Retired != 2 || zero.Retained != 0 {
		t.Fatal("confirmed zero did not retire", zero)
	}
	active, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10)
	if err != nil || len(active) != 0 {
		t.Fatal(err, active)
	}
	// Repetition reactivates the same identities and never duplicates rows.
	again := runSync(t, s, p, "lab-http-a", now.Add(2*time.Minute), syncResult{Status: inventory.StatusCompleted, Pages: 1, Received: 2, Objects: objects})
	if again.Added != 2 || again.Retired != 0 {
		t.Fatal(again)
	}
	repeat := runSync(t, s, p, "lab-http-a", now.Add(3*time.Minute), syncResult{Status: inventory.StatusCompleted, Pages: 1, Received: 2, Objects: objects})
	if repeat.Added != 0 || repeat.Changed != 0 || repeat.Retired != 0 || repeat.Received != 2 {
		t.Fatal(repeat)
	}
	var rows int
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_observations WHERE connection_id='lab-http-a' AND collection='users'").Scan(&rows); err != nil || rows != 2 {
		t.Fatal("repeated snapshots duplicated observations", err, rows)
	}
	for _, action := range []string{"sync_queued", "sync_start", "sync_complete"} {
		var count int
		if err = s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action=?", action).Scan(&count); err != nil || count != 4 {
			t.Fatal("missing audit", action, err, count)
		}
	}
}

func TestSyncConflictingDuplicateFailsWithoutPublication(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runSync(t, s, p, "lab-http-a", now, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"})},
	})
	runID, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.claimSyncRun(ctx, runID, "worker-test", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishSyncRun(ctx, runID, "worker-test", claimed.AttemptCount, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 2,
		Objects: []inventory.Object{
			object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"}),
			object("u1", map[string]string{"email": "alice@acme.example", "name": "Conflicting"}),
		},
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	run, err := s.SyncRun(ctx, p, runID)
	if err != nil || run.Status != inventory.StatusFailed || run.ErrorCode != "duplicate_conflict" {
		t.Fatal(err, run)
	}
	records, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10)
	if err != nil || len(records) != 1 || records[0].Attributes["name"] != "Alice" {
		t.Fatal("conflicting duplicate changed observations", err, records)
	}
}

func TestSyncQueueAndStartAuditsAreAtomic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_sync_queued BEFORE INSERT ON lab_audit WHEN NEW.action='sync_queued' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now); err == nil {
		t.Fatal("queued run committed without its audit")
	}
	var runs int
	if err := s.DB.QueryRow("SELECT count(*) FROM lab_sync_runs").Scan(&runs); err != nil || runs != 0 {
		t.Fatal("failed enqueue left a run", err, runs)
	}
	if _, err := s.DB.Exec("DROP TRIGGER fail_sync_queued"); err != nil {
		t.Fatal(err)
	}
	runID, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TRIGGER fail_sync_start BEFORE INSERT ON lab_audit WHEN NEW.action='sync_start' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.claimSyncRun(ctx, runID, "worker-test", now); err == nil {
		t.Fatal("running claim committed without its audit")
	}
	run, err := s.SyncRun(ctx, p, runID)
	if err != nil || run.Status != "queued" || run.AttemptCount != 0 {
		t.Fatal("failed claim changed the queued run", err, run)
	}
}

func TestSyncRequeueIsConnectionScoped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	addSyncSource(t, s, p, "lab-http-b", "users", "scope-b", now)
	runA, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now)
	if err != nil {
		t.Fatal(err)
	}
	runB, err := s.EnqueueSyncRun(ctx, p, "lab-http-b", "", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{runA, runB} {
		if _, err = s.claimSyncRun(ctx, runID, "worker-test", now); err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec("UPDATE lab_sync_runs SET lease_expires_at=? WHERE run_id=?", now.Add(-time.Minute).Format(time.RFC3339), runID); err != nil {
			t.Fatal(err)
		}
	}
	count, err := s.requeueExpiredSyncRuns(ctx, "lab-http-a", p.Actor(), now)
	if err != nil || count != 1 {
		t.Fatal(err, count)
	}
	a, err := s.SyncRun(ctx, p, runA)
	if err != nil || a.Status != "queued" {
		t.Fatal(err, a)
	}
	b, err := s.SyncRun(ctx, p, runB)
	if err != nil || b.Status != "running" {
		t.Fatal("scoped requeue changed another connection", err, b)
	}
	var actor string
	if err = s.DB.QueryRow("SELECT actor FROM lab_audit WHERE action='sync_requeued'").Scan(&actor); err != nil || actor != p.Actor() {
		t.Fatal("requeue audit actor is not the caller", err, actor)
	}
}

func TestSyncIdempotencyIsScopedByConnection(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	addSyncSource(t, s, p, "lab-http-b", "users", "scope-b", now)
	a, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "shared-key", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnqueueSyncRun(ctx, p, "lab-http-b", "shared-key", now)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("same idempotency key crossed connections")
	}
	if again, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "shared-key", now); err != nil || again != a {
		t.Fatal("same connection key did not replay", err, again)
	}
}

func TestSyncFinishRechecksSourceAndRequester(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runID, err := s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.claimSyncRun(ctx, runID, "worker-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE lab_sync_sources SET scope='scope-changed' WHERE connection_id='lab-http-a'"); err != nil {
		t.Fatal(err)
	}
	if err = s.finishSyncRun(ctx, runID, "worker-test", claimed.AttemptCount, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"})},
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	run, err := s.SyncRun(ctx, p, runID)
	if err != nil || run.Status != inventory.StatusFailed || run.ErrorCode != "configuration_changed" {
		t.Fatal(err, run)
	}
	if records, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10); err != nil || len(records) != 0 {
		t.Fatal("changed source published observations", err, records)
	}
	if _, err = s.DB.Exec("UPDATE lab_sync_sources SET scope='scope-a' WHERE connection_id='lab-http-a'"); err != nil {
		t.Fatal(err)
	}
	runID, err = s.EnqueueSyncRun(ctx, p, "lab-http-a", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = s.claimSyncRun(ctx, runID, "worker-test", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.GrantSync(ctx, "host", "custodia-lab", p.Subject, "lab-http-a", false, now); err != nil {
		t.Fatal(err)
	}
	if err = s.finishSyncRun(ctx, runID, "worker-test", claimed.AttemptCount, syncResult{
		Status: inventory.StatusCompleted, Pages: 1, Received: 1,
		Objects: []inventory.Object{object("u1", map[string]string{"email": "alice@acme.example", "name": "Alice"})},
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	run, err = s.SyncRun(ctx, p, runID)
	if err != nil || run.Status != inventory.StatusFailed || run.ErrorCode != "authorization_revoked" {
		t.Fatal(err, run)
	}
	if records, err := s.SyncRecords(ctx, p, "lab-http-a", "users", "active", 10); err != nil || len(records) != 0 {
		t.Fatal("revoked requester published observations", err, records)
	}
}

func TestSyncSchedulesKeepLastSuccessAndCountFailures(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	addSyncSource(t, s, p, "lab-http-a", "users", "scope-a", now)
	runSync(t, s, p, "lab-http-a", now, syncResult{Status: inventory.StatusCompleted, Pages: 1})
	runSync(t, s, p, "lab-http-a", now.Add(time.Minute), syncResult{Status: inventory.StatusFailed, ErrorCode: "timeout"})
	rows, err := s.SyncSchedules(ctx, p)
	if err != nil || len(rows) != 1 {
		t.Fatal(err, rows)
	}
	row := rows[0]
	if row.LastSuccessAt == "" || row.LastStatus != inventory.StatusFailed || row.ConsecutiveFailure != 1 || row.Active {
		t.Fatal(row)
	}
	b, _ := json.Marshal(row)
	if len(b) == 0 {
		t.Fatal("schedule not serializable")
	}
}

func TestSyncSchemaRejectsFutureVersion(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, _ := newSyncStore(t, now)
	if _, err := s.DB.Exec("INSERT INTO lab_schema_migrations VALUES(?)", SchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx); err == nil {
		t.Fatal("future schema served")
	}
}
