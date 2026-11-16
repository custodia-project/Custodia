package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"
)

// claimNextSyncRun atomically claims the oldest queued run for the worker.
func (s *Store) claimNextSyncRun(ctx context.Context, workerID string, now time.Time) (SyncRun, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SyncRun{}, err
	}
	defer tx.Rollback()
	var runID string
	err = tx.QueryRowContext(ctx, "SELECT run_id FROM lab_sync_runs WHERE status='queued' ORDER BY enqueued_at,rowid LIMIT 1").Scan(&runID)
	if err == sql.ErrNoRows {
		return SyncRun{}, ErrSyncNotFound
	}
	if err != nil {
		return SyncRun{}, err
	}
	stamp := now.UTC().Format(time.RFC3339)
	lease := now.Add(syncLeaseDuration).UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `UPDATE lab_sync_runs SET status='running',started_at=?,attempt_count=attempt_count+1,worker_id=?,heartbeat_at=?,lease_expires_at=?,status_message='Sincronización iniciada'
		WHERE run_id=? AND status='queued'`, stamp, workerID, stamp, lease, runID)
	if err != nil {
		return SyncRun{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SyncRun{}, err
	}
	if affected != 1 {
		return SyncRun{}, ErrSyncConflict
	}
	run, err := scanSyncRun(tx.QueryRowContext(ctx, syncRunSelect+" WHERE r.run_id=?", runID))
	if err != nil {
		return SyncRun{}, err
	}
	after, _ := json.Marshal(map[string]any{"connection_id": run.ConnectionID, "collection": run.Collection, "status": "running", "attempt": run.AttemptCount})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_start','{}',?,?)", run.RequestedBy, after, stamp); err != nil {
		return SyncRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return SyncRun{}, err
	}
	return run, nil
}

// ProcessNextSyncRun claims and executes one queued run. The worker uses it in
// a loop; ErrSyncNotFound means there is no work.
func (s *Store) ProcessNextSyncRun(ctx context.Context, workerID string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	run, err := s.claimNextSyncRun(ctx, workerID, now())
	if err != nil {
		return err
	}
	return s.processClaimedSyncRun(ctx, run, workerID, now)
}

// RunSyncWorker is the laboratory background loop: reclaim expired leases,
// enqueue due schedules and process queued runs one at a time. A single worker
// keeps SQLite writes short and preserves source isolation.
func (s *Store) RunSyncWorker(ctx context.Context, poll time.Duration) {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	workerID := newSyncWorkerID()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.RequeueExpiredSyncRuns(ctx, time.Now()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("custodia sync: requeue expired: %v", err)
		}
		if _, err := s.ScheduleDueSyncRuns(ctx, time.Now(), "system:scheduler"); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("custodia sync: schedule: %v", err)
		}
		for {
			if ctx.Err() != nil {
				return
			}
			err := s.ProcessNextSyncRun(ctx, workerID, time.Now)
			if errors.Is(err, ErrSyncNotFound) {
				break
			}
			if errors.Is(err, ErrSyncConflict) {
				continue
			}
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					log.Printf("custodia sync: run: %v", err)
				}
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// heartbeatLoop renews the lease while a run is active. The attempt counter is
// part of the guard, so a stale worker cannot renew a newer attempt.
func (s *Store) heartbeatLoop(ctx context.Context, runID, workerID string, attempt int, now func() time.Time) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.renewSyncLease(ctx, runID, workerID, attempt, now()); err != nil {
				return
			}
		}
	}
}

func (s *Store) renewSyncLease(ctx context.Context, runID, workerID string, attempt int, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339)
	lease := now.Add(syncLeaseDuration).UTC().Format(time.RFC3339)
	result, err := s.DB.ExecContext(ctx, `UPDATE lab_sync_runs SET heartbeat_at=?,lease_expires_at=?
		WHERE run_id=? AND status='running' AND worker_id=? AND attempt_count=?`, stamp, lease, runID, workerID, attempt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrSyncConflict
	}
	return nil
}
