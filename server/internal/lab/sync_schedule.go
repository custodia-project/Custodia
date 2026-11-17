package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Migration 6 adds the per-connection schedule without touching migrations 1-5.
func migrateScheduling(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version >= 6 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE lab_sync_sources ADD COLUMN interval_minutes INTEGER NOT NULL DEFAULT 0 CHECK(interval_minutes BETWEEN 0 AND 525600);
INSERT INTO lab_schema_migrations VALUES(6);`)
	return err
}

// SyncCanRun reports the explicit run capability for one enabled connection.
func (s *Store) SyncCanRun(ctx context.Context, p Principal, connectionID string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	allowed, err := s.canRunSync(ctx, tx, p.ID, connectionID)
	if err != nil {
		return false, err
	}
	return allowed, tx.Commit()
}

// SyncCanRunAny drives the UI capability. It does not grant access; it only
// says at least one readable connection has an explicit run grant.
func (s *Store) SyncCanRunAny(ctx context.Context, p Principal) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*)
		FROM lab_sync_sources s
		JOIN lab_grants g ON g.connection_id=s.connection_id
		JOIN lab_principals p ON p.id=g.principal_id AND p.enabled=1
		JOIN lab_sync_grants r ON r.connection_id=s.connection_id AND r.principal_id=p.id AND r.can_run=1
		WHERE p.id=? AND s.enabled=1`, p.ID).Scan(&count)
	return count > 0, err
}

// SetSyncSchedule persists an administrative interval and audits it in the
// same transaction. Zero disables automatic runs; a positive value is minutes.
func (s *Store) SetSyncSchedule(ctx context.Context, p Principal, connectionID string, interval int, now time.Time) error {
	if interval != 0 && (interval < 5 || interval > 525600) {
		return fmt.Errorf("interval must be 0 or between 5 and 525600 minutes")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	allowed, err := s.canRunSync(ctx, tx, p.ID, connectionID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrDenied
	}
	source, err := s.readableSyncSource(ctx, tx, p.ID, connectionID)
	if err != nil {
		return ErrDenied
	}
	if interval > 0 && !(source.Connector == syncConnector && normalizedAuthMode(source.AuthMode) == "none") {
		var ok int
		if err = tx.QueryRowContext(ctx, "SELECT ok FROM lab_connection_probes WHERE group_id=?", source.GroupID).Scan(&ok); err != nil || ok != 1 {
			return ErrProbeRequired
		}
	}
	before, _ := json.Marshal(map[string]any{"connection_id": connectionID, "interval_minutes": source.Interval})
	if _, err = tx.ExecContext(ctx, "UPDATE lab_sync_sources SET interval_minutes=? WHERE connection_id=?", interval, connectionID); err != nil {
		return err
	}
	after, _ := json.Marshal(map[string]any{"connection_id": connectionID, "interval_minutes": interval})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_schedule',?,?,?)", p.Actor(), before, after, now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// ScheduleDueSyncRuns enqueues one run for each enabled source whose interval
// elapsed. The unique active index and the active check keep the scheduler
// idempotent; a failed run is retried after the same interval.
func (s *Store) ScheduleDueSyncRuns(ctx context.Context, now time.Time, actor string) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT connection_id,interval_minutes FROM lab_sync_sources WHERE enabled=1 AND interval_minutes>0 ORDER BY connection_id`)
	if err != nil {
		return 0, err
	}
	type due struct {
		connection string
		interval   int
	}
	list := []due{}
	for rows.Next() {
		var item due
		if err = rows.Scan(&item.connection, &item.interval); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	enqueued := 0
	for _, item := range list {
		var active int
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM lab_sync_runs WHERE connection_id=? AND status IN ('queued','running')", item.connection).Scan(&active); err != nil {
			return enqueued, err
		}
		if active != 0 {
			continue
		}
		var last sql.NullString
		if err = s.DB.QueryRowContext(ctx, "SELECT max(coalesce(completed_at,started_at,enqueued_at)) FROM lab_sync_runs WHERE connection_id=?", item.connection).Scan(&last); err != nil {
			return enqueued, err
		}
		if last.Valid && last.String != "" {
			instant, parseErr := time.Parse(time.RFC3339, last.String)
			if parseErr == nil && now.Before(instant.Add(time.Duration(item.interval)*time.Minute)) {
				continue
			}
		}
		if _, err = s.enqueueScheduledSyncRun(ctx, item.connection, actor, now); err != nil {
			if err == ErrSyncActive {
				continue
			}
			return enqueued, err
		}
		enqueued++
	}
	return enqueued, nil
}

func (s *Store) enqueueScheduledSyncRun(ctx context.Context, connectionID, actor string, now time.Time) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	source, err := scanSyncSource(tx.QueryRowContext(ctx, syncSourceSelect, connectionID))
	if err != nil || !source.Enabled || source.Interval <= 0 {
		return "", ErrSyncNotFound
	}
	var active int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_sync_runs WHERE connection_id=? AND status IN ('queued','running')", connectionID).Scan(&active); err != nil {
		return "", err
	}
	if active != 0 {
		return "", ErrSyncActive
	}
	runID, err := newRunID()
	if err != nil {
		return "", err
	}
	stamp := now.UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, `INSERT INTO lab_sync_runs(run_id,connection_id,collection,scope,generation,source_url,status,requested_by,enqueued_at,status_message)
		VALUES(?,?,?,?,?,?,'queued',?,?,'Corrida programada')`, runID, connectionID, source.Collection, source.Scope, source.Generation, source.BaseURL, actor, stamp); err != nil {
		return "", err
	}
	after, _ := json.Marshal(map[string]any{"connection_id": connectionID, "collection": source.Collection, "scope": source.Scope, "generation": source.Generation, "status": "queued", "trigger": "scheduler"})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_queued','{}',?,?)", actor, after, stamp); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return runID, nil
}
