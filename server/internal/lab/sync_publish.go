package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"custodia/server/internal/inventory"
)

// syncResult is the storage boundary for one finished extraction. The HTTP
// adapter and runner own pagination; this file owns publication, retirement and
// audit.
type syncResult struct {
	Status      string
	Objects     []inventory.Object
	Pages       int
	Received    int
	Duplicates  int
	ErrorCode   string
	ErrorDetail string
	Warnings    []string
}

type storedObservation struct {
	Attributes map[string]string
	Status     string
	Scope      string
	Generation string
}

// progressSyncRun refreshes a visible heartbeat without creating an audit event
// per page. It is fenced by worker_id and attempt_count so an expired or stale
// attempt cannot keep writing or renew the lease of a newer attempt.
func (s *Store) progressSyncRun(ctx context.Context, runID, workerID string, attempt int, message string, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339)
	lease := now.Add(syncLeaseDuration).UTC().Format(time.RFC3339)
	result, err := s.DB.ExecContext(ctx, `UPDATE lab_sync_runs SET status_message=?,heartbeat_at=?,lease_expires_at=?
		WHERE run_id=? AND status='running' AND worker_id=? AND attempt_count=?`, message, stamp, lease, runID, workerID, attempt)
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

// finishSyncRun publishes the result and its audit atomically. A partial run
// can update observations but never retires records or replaces a baseline.
// A failed run never publishes observations or replaces the baseline, but it
// still records its terminal status and sync_complete audit. The publication
// rechecks the source snapshot and the requester's grants in the same
// transaction; a revoked principal or changed source fails without writing.
func (s *Store) finishSyncRun(ctx context.Context, runID, workerID string, attempt int, result syncResult, now time.Time) error {
	if result.Status != inventory.StatusCompleted && result.Status != inventory.StatusPartial && result.Status != inventory.StatusFailed {
		result.Status = inventory.StatusFailed
		result.ErrorCode = "invalid_status"
		result.ErrorDetail = "unsupported run result"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	run, err := scanSyncRun(tx.QueryRowContext(ctx, syncRunSelect+" WHERE r.run_id=?", runID))
	if err != nil {
		return err
	}
	if run.Status != "running" || run.WorkerID != workerID || run.AttemptCount != attempt {
		return ErrSyncConflict
	}
	stamp := now.UTC().Format(time.RFC3339)
	publish := result.Status == inventory.StatusCompleted || result.Status == inventory.StatusPartial
	if publish {
		source, sourceErr := loadSyncSourceTx(ctx, tx, run.ConnectionID)
		if sourceErr != nil || !source.Enabled || source.Collection != run.Collection ||
			source.Scope != run.Scope || source.Generation != run.Generation || source.BaseURL != run.SourceURL {
			result.Status = inventory.StatusFailed
			result.ErrorCode = "configuration_changed"
			result.ErrorDetail = "sync source configuration changed during the run"
			publish = false
		} else if principalID, found := actorPrincipalID(run.RequestedBy); found {
			allowed, authErr := syncRunAuthorizedTx(ctx, tx, principalID, run.ConnectionID)
			if authErr != nil {
				return authErr
			}
			if !allowed {
				result.Status = inventory.StatusFailed
				result.ErrorCode = "authorization_revoked"
				result.ErrorDetail = "the run requester is no longer authorized for this connection"
				publish = false
			}
		}
	}
	received, added, changed, retired, retained, duplicates := 0, 0, 0, 0, 0, result.Duplicates
	if publish {
		unique, deduped, err := inventory.Deduplicate(run.Collection, result.Objects)
		if err != nil {
			// A contradictory observation set is not a partial page: publish
			// nothing and keep the previous baseline.
			result.Status = inventory.StatusFailed
			result.ErrorCode = errorCodeOf(err)
			result.ErrorDetail = safeDetail(err)
			result.Warnings = append(result.Warnings, "invalid_snapshot")
			publish = false
		} else {
			duplicates += deduped
			received = len(unique)
			added, changed, retired, retained, err = publishSyncObjects(ctx, tx, run, unique, result.Status, stamp)
			if err != nil {
				return err
			}
		}
	}
	warnings, err := json.Marshal(append([]string{}, result.Warnings...))
	if err != nil {
		return err
	}
	message := map[string]string{
		inventory.StatusCompleted: "Sincronización completada",
		inventory.StatusPartial:   "Sincronización completada con avisos",
		inventory.StatusFailed:    "Sincronización fallida",
	}[result.Status]
	if message == "" {
		message = "Sincronización fallida"
	}
	update, err := tx.ExecContext(ctx, `UPDATE lab_sync_runs SET status=?,completed_at=?,status_message=?,pages=?,received=?,added=?,changed=?,retired=?,retained=?,duplicate_rows=?,warnings_json=?,error_code=?,error_detail=?
		WHERE run_id=? AND status='running' AND worker_id=? AND attempt_count=?`, result.Status, stamp, message, result.Pages, received, added, changed, retired, retained, duplicates, warnings, result.ErrorCode, truncate(result.ErrorDetail, 300), runID, workerID, attempt)
	if err != nil {
		return err
	}
	affected, err := update.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrSyncConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO lab_runs(connection_id,status,completed_at) VALUES(?,?,?)
		ON CONFLICT(connection_id) DO UPDATE SET status=excluded.status,completed_at=excluded.completed_at`, run.ConnectionID, result.Status, stamp); err != nil {
		return err
	}
	after, _ := json.Marshal(map[string]any{
		"connection_id": run.ConnectionID, "collection": run.Collection, "status": result.Status,
		"received_objects": received, "added": added, "changed": changed, "retired": retired,
		"retained": retained, "error_code": result.ErrorCode,
	})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_complete','{}',?,?)", run.RequestedBy, after, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

// publishSyncObjects upserts the validated snapshot and reconciles absences
// only for a complete run comparable to the stored baseline. It returns the
// change counters used by the run and API.
func publishSyncObjects(ctx context.Context, tx *sql.Tx, run SyncRun, objects []inventory.Object, status, now string) (added, changed, retired, retained int, err error) {
	existing := map[string]storedObservation{}
	rows, err := tx.QueryContext(ctx, `SELECT external_id,document,status,scope,generation FROM lab_observations WHERE connection_id=? AND collection=?`, run.ConnectionID, run.Collection)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	for rows.Next() {
		var external string
		var document []byte
		var observation storedObservation
		if err = rows.Scan(&external, &document, &observation.Status, &observation.Scope, &observation.Generation); err != nil {
			rows.Close()
			return 0, 0, 0, 0, err
		}
		observation.Attributes = map[string]string{}
		if err = json.Unmarshal(document, &observation.Attributes); err != nil {
			rows.Close()
			return 0, 0, 0, 0, err
		}
		existing[external] = observation
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	var baselineScope, baselineGeneration string
	err = tx.QueryRowContext(ctx, "SELECT scope,generation FROM lab_sync_baselines WHERE connection_id=? AND collection=?", run.ConnectionID, run.Collection).Scan(&baselineScope, &baselineGeneration)
	if err != nil && err != sql.ErrNoRows {
		return 0, 0, 0, 0, err
	}
	comparable := err == nil && baselineScope == run.Scope && baselineGeneration == run.Generation
	seen := make(map[string]bool, len(objects))
	for _, object := range objects {
		seen[object.ExternalID] = true
		document, err := json.Marshal(object.Attributes)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		previous, found := existing[object.ExternalID]
		// added/changed count changes against a comparable baseline, not the
		// total number of observations in the snapshot.
		if !found {
			if comparable {
				added++
			}
		} else {
			if comparable && previous.Status == "retired" {
				added++
			}
			if comparable && previous.Status == "active" && !sameAttributes(previous.Attributes, object.Attributes) {
				changed++
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO lab_observations(connection_id,collection,external_id,document,status,first_run_id,last_run_id,scope,generation,updated_at,retired_at,retired_by_run_id)
			VALUES(?,?,?,?,'active',?,?,?,?,?,'','')
			ON CONFLICT(connection_id,collection,external_id) DO UPDATE SET
			 document=excluded.document,status='active',
			 first_run_id=CASE WHEN lab_observations.first_run_id='' THEN excluded.first_run_id ELSE lab_observations.first_run_id END,
			 last_run_id=excluded.last_run_id,scope=excluded.scope,generation=excluded.generation,
			 updated_at=excluded.updated_at,retired_at='',retired_by_run_id=''`,
			run.ConnectionID, run.Collection, object.ExternalID, document, run.RunID, run.RunID, run.Scope, run.Generation, now); err != nil {
			return 0, 0, 0, 0, err
		}
	}
	if status == inventory.StatusCompleted && comparable {
		for external, observation := range existing {
			if seen[external] || observation.Status != "active" || observation.Scope != run.Scope || observation.Generation != run.Generation {
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE lab_observations SET status='retired',retired_at=?,retired_by_run_id=?,updated_at=?
				WHERE connection_id=? AND collection=? AND external_id=? AND status='active'`, now, run.RunID, now, run.ConnectionID, run.Collection, external); err != nil {
				return 0, 0, 0, 0, err
			}
			retired++
		}
	}
	for external, observation := range existing {
		if seen[external] || observation.Status != "active" {
			continue
		}
		if status == inventory.StatusCompleted && comparable && observation.Scope == run.Scope && observation.Generation == run.Generation {
			continue
		}
		retained++
	}
	if status == inventory.StatusCompleted {
		if _, err = tx.ExecContext(ctx, `INSERT INTO lab_sync_baselines(connection_id,collection,run_id,scope,generation,completed_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(connection_id,collection) DO UPDATE SET run_id=excluded.run_id,scope=excluded.scope,generation=excluded.generation,completed_at=excluded.completed_at`,
			run.ConnectionID, run.Collection, run.RunID, run.Scope, run.Generation, now); err != nil {
			return 0, 0, 0, 0, err
		}
	}
	return added, changed, retired, retained, nil
}

func sameAttributes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func errorCodeOf(err error) string {
	var inventoryError *inventory.Error
	if errors.As(err, &inventoryError) {
		return inventoryError.Code
	}
	return "internal_error"
}

func safeDetail(err error) string {
	if err == nil {
		return ""
	}
	return truncate(err.Error(), 200)
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (s *Store) loadSyncSource(ctx context.Context, connectionID string) (SyncSource, error) {
	return scanSyncSource(s.DB.QueryRowContext(ctx, syncSourceSelect, connectionID))
}

const syncSourceSelect = `SELECT connection_id,connector,base_url,collection,scope,generation,enabled,page_size,max_pages,max_records,created_by,created_at
	FROM lab_sync_sources WHERE connection_id=?`

func loadSyncSourceTx(ctx context.Context, tx *sql.Tx, connectionID string) (SyncSource, error) {
	return scanSyncSource(tx.QueryRowContext(ctx, syncSourceSelect, connectionID))
}

func scanSyncSource(row rowScanner) (SyncSource, error) {
	var source SyncSource
	var enabled int
	err := row.Scan(&source.ConnectionID, &source.Connector, &source.BaseURL, &source.Collection, &source.Scope, &source.Generation,
		&enabled, &source.PageSize, &source.MaxPages, &source.MaxRecords, &source.CreatedBy, &source.CreatedAt)
	source.Enabled = enabled == 1
	return source, err
}

// actorPrincipalID parses the laboratory actor format. Host and system actors
// have no principal row and are not subject to the requester recheck.
func actorPrincipalID(actor string) (int, bool) {
	raw, found := strings.CutPrefix(actor, "principal:")
	if !found {
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func syncRunAuthorizedTx(ctx context.Context, tx *sql.Tx, principalID int, connectionID string) (bool, error) {
	var enabled, read, run int
	err := tx.QueryRowContext(ctx, `SELECT p.enabled,
		(SELECT count(*) FROM lab_grants g WHERE g.principal_id=p.id AND g.connection_id=?),
		(SELECT count(*) FROM lab_sync_grants r WHERE r.principal_id=p.id AND r.connection_id=? AND r.can_run=1)
		FROM lab_principals p WHERE p.id=?`, connectionID, connectionID, principalID).Scan(&enabled, &read, &run)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1 && read == 1 && run == 1, nil
}
