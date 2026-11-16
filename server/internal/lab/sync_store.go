package lab

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"custodia/server/internal/connectors"
)

var (
	ErrSyncActive   = errors.New("sync run already queued or running")
	ErrSyncNotFound = errors.New("sync run not found")
	ErrSyncConflict = errors.New("sync run state conflict")
)

var connectionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

const (
	syncConnector                 = "http-inventory-v1"
	syncConnectorCyberArkPAMSafes = "cyberark-pam-safes-v1"
	syncLeaseDuration             = 15 * time.Minute
	syncDefaultPages              = 100
	syncDefaultRecords            = 10000
	syncDefaultPage               = 100
)

// SyncSource is one read-only connection. The URL and credential references
// are configuration, never secret values. The synthetic connector still
// requires a numeric loopback HTTP origin; a vendor connector registered by an
// administrator uses HTTPS and an auth plan validated by the probe package.
type SyncSource struct {
	ConnectionID    string   `json:"connection_id"`
	GroupID         string   `json:"group_id"`
	Connector       string   `json:"connector"`
	BaseURL         string   `json:"base_url"`
	Collection      string   `json:"collection"`
	Scope           string   `json:"scope"`
	Generation      string   `json:"generation"`
	Enabled         bool     `json:"enabled"`
	PageSize        int      `json:"page_size"`
	MaxPages        int      `json:"max_pages"`
	MaxRecords      int      `json:"max_records"`
	Interval        int      `json:"interval_minutes"`
	AuthMode        string   `json:"auth_mode"`
	CredentialRef   string   `json:"-"`
	TokenURL        string   `json:"-"`
	ClientIDRef     string   `json:"-"`
	ClientSecretRef string   `json:"-"`
	Scopes          []string `json:"-"`
	CAFile          string   `json:"-"`
	ClientCertFile  string   `json:"-"`
	ClientKeyFile   string   `json:"-"`
	CreatedBy       string   `json:"created_by"`
	CreatedAt       string   `json:"created_at"`
}

// SyncRun is the durable history row. Empty optional timestamps are omitted by
// the HTTP mapper, never confused with a completed run.
type SyncRun struct {
	RunID         string   `json:"run_id"`
	ConnectionID  string   `json:"connection_id"`
	Collection    string   `json:"collection"`
	Scope         string   `json:"scope"`
	Generation    string   `json:"generation"`
	SourceURL     string   `json:"source_url"`
	Connector     string   `json:"connector"`
	Status        string   `json:"status"`
	RequestedBy   string   `json:"requested_by"`
	EnqueuedAt    string   `json:"enqueued_at"`
	StartedAt     string   `json:"started_at,omitempty"`
	CompletedAt   string   `json:"completed_at,omitempty"`
	StatusMessage string   `json:"status_message,omitempty"`
	AttemptCount  int      `json:"attempt_count"`
	WorkerID      string   `json:"worker_id,omitempty"`
	HeartbeatAt   string   `json:"heartbeat_at,omitempty"`
	LeaseExpires  string   `json:"lease_expires_at,omitempty"`
	Pages         int      `json:"pages"`
	Received      int      `json:"received"`
	Added         int      `json:"added"`
	Changed       int      `json:"changed"`
	Retired       int      `json:"retired"`
	Retained      int      `json:"retained"`
	DuplicateRows int      `json:"duplicate_rows"`
	Warnings      []string `json:"warnings,omitempty"`
	ErrorCode     string   `json:"error_code,omitempty"`
	ErrorDetail   string   `json:"error_detail,omitempty"`
}

// SyncSchedule is the read model for one configured sync connection.
type SyncSchedule struct {
	ConnectionID        string   `json:"connection_id"`
	Connector           string   `json:"connector"`
	SourceSystem        string   `json:"source_system"`
	Collection          string   `json:"collection"`
	Scope               string   `json:"scope"`
	Enabled             bool     `json:"enabled"`
	Active              bool     `json:"active"`
	IntervalMinutes     int      `json:"interval_minutes"`
	Freshness           string   `json:"freshness"`
	IntervalsSince      *float64 `json:"intervals_since_success,omitempty"`
	StaleAfterIntervals int      `json:"stale_after_intervals"`
	NextScheduledAt     string   `json:"next_scheduled_at,omitempty"`
	LastSuccessAt       string   `json:"last_success_at,omitempty"`
	LastAttemptAt       string   `json:"last_attempt_at,omitempty"`
	LastStatus          string   `json:"last_status,omitempty"`
	ConsecutiveFailure  int      `json:"consecutive_failures"`
}

// SyncRecord is one active or retired observation with its provenance.
type SyncRecord struct {
	ExternalID     string            `json:"external_id"`
	Attributes     map[string]string `json:"attributes"`
	Status         string            `json:"status"`
	FirstRunID     string            `json:"first_run_id"`
	LastRunID      string            `json:"last_run_id"`
	Scope          string            `json:"scope"`
	Generation     string            `json:"generation"`
	UpdatedAt      string            `json:"updated_at"`
	RetiredAt      string            `json:"retired_at,omitempty"`
	RetiredByRunID string            `json:"retired_by_run_id,omitempty"`
}

// SyncReview is a human decision kept apart from observations. Synchronization
// never writes this table.
type SyncReview struct {
	ConnectionID string `json:"connection_id"`
	Collection   string `json:"collection"`
	ExternalID   string `json:"external_id"`
	Owner        string `json:"owner"`
	Note         string `json:"note"`
	UpdatedBy    string `json:"updated_by"`
	UpdatedAt    string `json:"updated_at"`
}

// Migration 5 is additive. Applied migration statements 1-4 stay unchanged.
func migrateSync(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version >= 5 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS lab_sync_sources(
  connection_id TEXT PRIMARY KEY REFERENCES lab_connections(id),
  connector TEXT NOT NULL,
  base_url TEXT NOT NULL,
  collection TEXT NOT NULL,
  scope TEXT NOT NULL,
  generation TEXT NOT NULL,
  enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
  page_size INTEGER NOT NULL CHECK(page_size BETWEEN 1 AND 500),
  max_pages INTEGER NOT NULL CHECK(max_pages BETWEEN 1 AND 1000),
  max_records INTEGER NOT NULL CHECK(max_records BETWEEN 1 AND 100000),
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS lab_sync_grants(
  principal_id INTEGER NOT NULL REFERENCES lab_principals(id),
  connection_id TEXT NOT NULL REFERENCES lab_sync_sources(connection_id),
  can_run INTEGER NOT NULL CHECK(can_run IN (0,1)),
  PRIMARY KEY(principal_id,connection_id)
);
CREATE TABLE IF NOT EXISTS lab_sync_runs(
  run_id TEXT PRIMARY KEY,
  connection_id TEXT NOT NULL REFERENCES lab_connections(id),
  collection TEXT NOT NULL,
  scope TEXT NOT NULL,
  generation TEXT NOT NULL,
  source_url TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK(status IN ('queued','running','completed','partial','failed')),
  requested_by TEXT NOT NULL,
  idempotency_key TEXT,
  enqueued_at TEXT NOT NULL,
  started_at TEXT,
  completed_at TEXT,
  status_message TEXT NOT NULL DEFAULT '',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  worker_id TEXT,
  heartbeat_at TEXT,
  lease_expires_at TEXT,
  pages INTEGER NOT NULL DEFAULT 0,
  received INTEGER NOT NULL DEFAULT 0,
  added INTEGER NOT NULL DEFAULT 0,
  changed INTEGER NOT NULL DEFAULT 0,
  retired INTEGER NOT NULL DEFAULT 0,
  retained INTEGER NOT NULL DEFAULT 0,
  duplicate_rows INTEGER NOT NULL DEFAULT 0,
  warnings_json BLOB NOT NULL DEFAULT '[]',
  error_code TEXT NOT NULL DEFAULT '',
  error_detail TEXT NOT NULL DEFAULT '',
  UNIQUE(requested_by,connection_id,idempotency_key)
);
CREATE UNIQUE INDEX IF NOT EXISTS lab_sync_runs_active ON lab_sync_runs(connection_id) WHERE status IN ('queued','running');
CREATE INDEX IF NOT EXISTS lab_sync_runs_connection ON lab_sync_runs(connection_id,enqueued_at DESC);
CREATE TABLE IF NOT EXISTS lab_sync_baselines(
  connection_id TEXT NOT NULL,
  collection TEXT NOT NULL,
  run_id TEXT NOT NULL REFERENCES lab_sync_runs(run_id),
  scope TEXT NOT NULL,
  generation TEXT NOT NULL,
  completed_at TEXT NOT NULL,
  PRIMARY KEY(connection_id,collection)
);
CREATE TABLE IF NOT EXISTS lab_sync_reviews(
  connection_id TEXT NOT NULL REFERENCES lab_connections(id),
  collection TEXT NOT NULL,
  external_id TEXT NOT NULL,
  owner TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  updated_by TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(connection_id,collection,external_id)
);
ALTER TABLE lab_observations ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','retired'));
ALTER TABLE lab_observations ADD COLUMN first_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_observations ADD COLUMN last_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_observations ADD COLUMN scope TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_observations ADD COLUMN generation TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_observations ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_observations ADD COLUMN retired_at TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_observations ADD COLUMN retired_by_run_id TEXT NOT NULL DEFAULT '';
INSERT INTO lab_schema_migrations VALUES(5);`)
	return err
}

// validLabBaseURL is deliberately narrow: the laboratory adapter never talks
// to a real provider, a hostname or a non-loopback address.
func validLabBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid source URL")
	}
	if parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("laboratory source URL must be http:// with no user, query or fragment")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("laboratory source URL must use a numeric loopback address")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("laboratory source URL port is invalid")
		}
	}
	return nil
}

func (s *Store) readableSyncSource(ctx context.Context, tx *sql.Tx, principalID int, connectionID string) (SyncSource, error) {
	var source SyncSource
	var enabled int
	err := tx.QueryRowContext(ctx, `SELECT s.connection_id,s.connector,s.base_url,s.collection,s.scope,s.generation,s.enabled,s.page_size,s.max_pages,s.max_records,s.interval_minutes,s.created_by,s.created_at
		FROM lab_sync_sources s
		JOIN lab_grants g ON g.connection_id=s.connection_id
		JOIN lab_principals p ON p.id=g.principal_id AND p.enabled=1
		WHERE p.id=? AND s.connection_id=?`, principalID, connectionID).Scan(
		&source.ConnectionID, &source.Connector, &source.BaseURL, &source.Collection, &source.Scope, &source.Generation,
		&enabled, &source.PageSize, &source.MaxPages, &source.MaxRecords, &source.Interval, &source.CreatedBy, &source.CreatedAt,
	)
	source.Enabled = enabled == 1
	return source, err
}

func (s *Store) canRunSync(ctx context.Context, tx *sql.Tx, principalID int, connectionID string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*)
		FROM lab_sync_sources s
		JOIN lab_grants g ON g.connection_id=s.connection_id
		JOIN lab_principals p ON p.id=g.principal_id AND p.enabled=1
		JOIN lab_sync_grants r ON r.connection_id=s.connection_id AND r.principal_id=p.id AND r.can_run=1
		WHERE p.id=? AND s.connection_id=? AND s.enabled=1`, principalID, connectionID).Scan(&count)
	return count == 1, err
}

// SyncSourcesFor returns only enabled connections the principal may read.
func (s *Store) SyncSourcesFor(ctx context.Context, p Principal) ([]SyncSource, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.connection_id,s.connector,s.base_url,s.collection,s.scope,s.generation,s.enabled,s.page_size,s.max_pages,s.max_records,s.interval_minutes,s.created_by,s.created_at
		FROM lab_sync_sources s
		JOIN lab_grants g ON g.connection_id=s.connection_id
		JOIN lab_principals p ON p.id=g.principal_id AND p.enabled=1
		WHERE p.id=? AND s.enabled=1 ORDER BY s.connection_id`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SyncSource{}
	for rows.Next() {
		var source SyncSource
		var enabled int
		if err = rows.Scan(&source.ConnectionID, &source.Connector, &source.BaseURL, &source.Collection, &source.Scope, &source.Generation,
			&enabled, &source.PageSize, &source.MaxPages, &source.MaxRecords, &source.Interval, &source.CreatedBy, &source.CreatedAt); err != nil {
			return nil, err
		}
		source.Enabled = enabled == 1
		result = append(result, source)
	}
	return result, rows.Err()
}

func validateSyncConfiguration(source SyncSource, grants []string) error {
	descriptor, found := connectors.Get(source.Connector)
	if !found || !connectors.Connectable(descriptor) {
		return fmt.Errorf("unsupported sync connector")
	}
	if err := validateCommonConfiguration(source, grants); err != nil {
		return err
	}
	if err := validLabBaseURL(source.BaseURL); err != nil {
		return err
	}
	collection, found := collectionForSync(source.Collection)
	if !found || !connectorSupportsCollection(source.Connector, collection) {
		return fmt.Errorf("unsupported inventory collection")
	}
	return nil
}

// validateCommonConfiguration holds the checks shared by the host laboratory
// path and the administrative onboarding path.
func validateCommonConfiguration(source SyncSource, grants []string) error {
	if !connectionPattern.MatchString(source.ConnectionID) {
		return fmt.Errorf("invalid connection ID")
	}
	if strings.TrimSpace(source.Scope) == "" || len(source.Scope) > 128 || strings.ContainsAny(source.Scope, "\r\n") {
		return fmt.Errorf("invalid sync scope")
	}
	if strings.TrimSpace(source.Generation) == "" || len(source.Generation) > 64 || strings.ContainsAny(source.Generation, "\r\n") {
		return fmt.Errorf("invalid sync generation")
	}
	if source.PageSize == 0 {
		source.PageSize = syncDefaultPage
	}
	if source.MaxPages == 0 {
		source.MaxPages = syncDefaultPages
	}
	if source.MaxRecords == 0 {
		source.MaxRecords = syncDefaultRecords
	}
	if source.PageSize < 1 || source.PageSize > 500 || source.MaxPages < 1 || source.MaxPages > 1000 || source.MaxRecords < source.PageSize || source.MaxRecords > 100000 {
		return fmt.Errorf("invalid sync limits")
	}
	if source.Interval != 0 && (source.Interval < 5 || source.Interval > 525600) {
		return fmt.Errorf("invalid sync interval")
	}
	if len(grants) > 128 {
		return fmt.Errorf("too many grants")
	}
	return nil
}

// collectionForSync exists to keep the lab configuration independent from a
// future connector registry while still rejecting unknown collections.
func collectionForSync(name string) (string, bool) {
	switch name {
	case "users", "devices", "safes", "safe_members", "accounts", "platforms", "psm_servers", "psm_connectors", "platform_psm_links":
		return name, true
	default:
		return "", false
	}
}

// connectorSupportsCollection keeps connector-specific contracts explicit: the
// synthetic HTTP inventory is users/devices, while the PAM adapter only emits
// Safes and cannot be pointed at the normalized user fixture.
func connectorSupportsCollection(connector, collection string) bool {
	switch connector {
	case syncConnector:
		return collection == "users" || collection == "devices"
	case syncConnectorCyberArkPAMSafes:
		return collection == "safes" || collection == "safe_members" || collection == "accounts" ||
			collection == "platforms" || collection == "psm_servers" || collection == "psm_connectors" ||
			collection == "platform_psm_links"
	default:
		return false
	}
}

// AddHTTPSource is an explicit laboratory host operation. It creates the
// connection, its read/run grants and the audit events atomically.
func (s *Store) AddHTTPSource(ctx context.Context, actor string, source SyncSource, grants []string, now time.Time) error {
	if err := validateSyncConfiguration(source, grants); err != nil {
		return err
	}
	if source.PageSize == 0 {
		source.PageSize = syncDefaultPage
	}
	if source.MaxPages == 0 {
		source.MaxPages = syncDefaultPages
	}
	if source.MaxRecords == 0 {
		source.MaxRecords = syncDefaultRecords
	}
	source.CreatedBy = actor
	source.CreatedAt = now.UTC().Format(time.RFC3339)
	source.Enabled = true
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_connections(id,kind) VALUES(?,?)", source.ConnectionID, "netskope"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO lab_sync_sources(connection_id,connector,base_url,collection,scope,generation,enabled,page_size,max_pages,max_records,interval_minutes,created_by,created_at)
		VALUES(?,?,?,?,?,?,1,?,?,?,?,?,?)`, source.ConnectionID, source.Connector, source.BaseURL, source.Collection, source.Scope, source.Generation, source.PageSize, source.MaxPages, source.MaxRecords, source.Interval, source.CreatedBy, source.CreatedAt); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, subject := range grants {
		subject = strings.TrimSpace(subject)
		if subject == "" || seen[subject] {
			continue
		}
		seen[subject] = true
		var principalID int
		if err = tx.QueryRowContext(ctx, "SELECT id FROM lab_principals WHERE issuer='custodia-lab' AND subject=? AND enabled=1", subject).Scan(&principalID); err != nil {
			return fmt.Errorf("sync grant subject is not an enabled laboratory principal")
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO lab_grants(principal_id,connection_id) VALUES(?,?)", principalID, source.ConnectionID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_sync_grants(principal_id,connection_id,can_run) VALUES(?,?,1) ON CONFLICT(principal_id,connection_id) DO UPDATE SET can_run=1", principalID, source.ConnectionID); err != nil {
			return err
		}
		after, _ := json.Marshal(map[string]any{"connection_id": source.ConnectionID, "subject": subject, "can_run": true})
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_grant','{}',?,?)", actor, after, now.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	after, _ := json.Marshal(map[string]any{
		"connection_id": source.ConnectionID, "connector": source.Connector, "base_url": source.BaseURL,
		"collection": source.Collection, "scope": source.Scope, "generation": source.Generation,
		"page_size": source.PageSize, "max_pages": source.MaxPages, "max_records": source.MaxRecords, "interval_minutes": source.Interval,
	})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_configure','{}',?,?)", actor, after, now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// GrantSync changes only the explicit read/run grants. The caller is a host
// operation today; an administrative API is not exposed yet.
func (s *Store) GrantSync(ctx context.Context, actor, issuer, subject, connectionID string, canRun bool, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var principalID int
	if err = tx.QueryRowContext(ctx, "SELECT id FROM lab_principals WHERE issuer=? AND subject=? AND enabled=1", issuer, subject).Scan(&principalID); err != nil {
		return ErrDenied
	}
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_sync_sources WHERE connection_id=?", connectionID).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return ErrSyncNotFound
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO lab_grants(principal_id,connection_id) VALUES(?,?)", principalID, connectionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_sync_grants(principal_id,connection_id,can_run) VALUES(?,?,?) ON CONFLICT(principal_id,connection_id) DO UPDATE SET can_run=excluded.can_run", principalID, connectionID, boolInt(canRun)); err != nil {
		return err
	}
	after, _ := json.Marshal(map[string]any{"connection_id": connectionID, "issuer": issuer, "subject": subject, "can_run": canRun})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_grant','{}',?,?)", actor, after, now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// newRunID is a local UUIDv4-shaped opaque identifier; it is not derived from
// provider data and never reveals a sequence.
func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32], nil
}

// EnqueueSyncRun persists the job and its audit event before any external call.
// Authorization is rechecked inside the transaction so a revoked grant cannot
// enqueue a run.
func (s *Store) EnqueueSyncRun(ctx context.Context, p Principal, connectionID, idempotencyKey string, now time.Time) (string, error) {
	if len(idempotencyKey) > 128 || strings.ContainsAny(idempotencyKey, "\r\n") {
		return "", fmt.Errorf("invalid idempotency key")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	allowed, err := s.canRunSync(ctx, tx, p.ID, connectionID)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrDenied
	}
	source, err := s.readableSyncSource(ctx, tx, p.ID, connectionID)
	if err != nil {
		return "", ErrDenied
	}
	var existing string
	if idempotencyKey != "" {
		err = tx.QueryRowContext(ctx, "SELECT run_id FROM lab_sync_runs WHERE requested_by=? AND connection_id=? AND idempotency_key=?", p.Actor(), connectionID, idempotencyKey).Scan(&existing)
		if err == nil {
			return existing, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
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
	if _, err = tx.ExecContext(ctx, `INSERT INTO lab_sync_runs(run_id,connection_id,collection,scope,generation,source_url,status,requested_by,idempotency_key,enqueued_at,status_message)
		VALUES(?,?,?,?,?,?,'queued',?,?,?,'Corrida encolada')`, runID, connectionID, source.Collection, source.Scope, source.Generation, source.BaseURL, p.Actor(), nullIfEmpty(idempotencyKey), stamp); err != nil {
		return "", err
	}
	after, _ := json.Marshal(map[string]any{"connection_id": connectionID, "collection": source.Collection, "scope": source.Scope, "generation": source.Generation, "status": "queued"})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_queued','{}',?,?)", p.Actor(), after, stamp); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return runID, nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// claimSyncRun atomically transitions one queued run and records the start.
func (s *Store) claimSyncRun(ctx context.Context, runID, workerID string, now time.Time) (SyncRun, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SyncRun{}, err
	}
	defer tx.Rollback()
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

const syncRunSelect = `SELECT r.run_id,r.connection_id,r.collection,r.scope,r.generation,r.source_url,
 coalesce((SELECT s.connector FROM lab_sync_sources s WHERE s.connection_id=r.connection_id),''),
 r.status,r.requested_by,r.enqueued_at,
 coalesce(r.started_at,''),coalesce(r.completed_at,''),r.status_message,r.attempt_count,coalesce(r.worker_id,''),
 coalesce(r.heartbeat_at,''),coalesce(r.lease_expires_at,''),r.pages,r.received,r.added,r.changed,r.retired,r.retained,
 r.duplicate_rows,r.warnings_json,r.error_code,r.error_detail FROM lab_sync_runs r`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSyncRun(row rowScanner) (SyncRun, error) {
	var run SyncRun
	var warnings []byte
	if err := row.Scan(&run.RunID, &run.ConnectionID, &run.Collection, &run.Scope, &run.Generation, &run.SourceURL, &run.Connector, &run.Status, &run.RequestedBy, &run.EnqueuedAt,
		&run.StartedAt, &run.CompletedAt, &run.StatusMessage, &run.AttemptCount, &run.WorkerID, &run.HeartbeatAt, &run.LeaseExpires,
		&run.Pages, &run.Received, &run.Added, &run.Changed, &run.Retired, &run.Retained, &run.DuplicateRows, &warnings, &run.ErrorCode, &run.ErrorDetail); err != nil {
		return run, err
	}
	if len(warnings) != 0 {
		_ = json.Unmarshal(warnings, &run.Warnings)
	}
	return run, nil
}

// RequeueExpiredSyncRuns returns abandoned leases to the durable queue for a
// worker or host maintenance. The HTTP path uses the connection-scoped form so
// a principal cannot mutate another connection's run state.
func (s *Store) RequeueExpiredSyncRuns(ctx context.Context, now time.Time) (int, error) {
	return s.requeueExpiredSyncRuns(ctx, "", "system:sync", now)
}

func (s *Store) requeueExpiredSyncRuns(ctx context.Context, connectionID, actor string, now time.Time) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339)
	query := "SELECT run_id FROM lab_sync_runs WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=?)"
	args := []any{stamp}
	if connectionID != "" {
		query += " AND connection_id=?"
		args = append(args, connectionID)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	list := []string{}
	for rows.Next() {
		var runID string
		if err = rows.Scan(&runID); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, runID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	requeued := 0
	for _, runID := range list {
		result, err := tx.ExecContext(ctx, "UPDATE lab_sync_runs SET status='queued',worker_id=NULL,heartbeat_at=NULL,lease_expires_at=NULL,status_message='La concesión del worker venció; corrida reencolada' WHERE run_id=? AND status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=?)", runID, stamp)
		if err != nil {
			return 0, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if affected != 1 {
			// A progress update renewed the lease between the select and this
			// update; do not count or audit a run that was not requeued.
			continue
		}
		after, _ := json.Marshal(map[string]any{"status": "queued", "reason": "worker_lease_expired"})
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_requeued','{}',?,?)", actor, after, stamp); err != nil {
			return 0, err
		}
		requeued++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return requeued, nil
}

// SyncRuns returns only runs of connections the principal can read. The list
// never reveals a hidden connection through a total or a run ID.
func (s *Store) SyncRuns(ctx context.Context, p Principal, limit int) ([]SyncRun, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, syncRunSelect+` JOIN lab_grants g ON g.connection_id=r.connection_id JOIN lab_principals p ON p.id=g.principal_id AND p.enabled=1
		WHERE p.id=? ORDER BY r.enqueued_at DESC,r.run_id DESC LIMIT ?`, p.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SyncRun{}
	for rows.Next() {
		run, err := scanSyncRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

// SyncRun returns one visible run or ErrSyncNotFound; hidden runs are not
// distinguished from absent ones.
func (s *Store) SyncRun(ctx context.Context, p Principal, runID string) (SyncRun, error) {
	run, err := scanSyncRun(s.DB.QueryRowContext(ctx, syncRunSelect+` JOIN lab_grants g ON g.connection_id=r.connection_id JOIN lab_principals p ON p.id=g.principal_id AND p.enabled=1
		WHERE p.id=? AND r.run_id=?`, p.ID, runID))
	if err == sql.ErrNoRows {
		return SyncRun{}, ErrSyncNotFound
	}
	return run, err
}

// activeSyncRun is an internal worker lookup. Callers must already have
// verified the principal's run grant for the connection; it is never exposed
// as a list crossing connections.
func (s *Store) activeSyncRun(ctx context.Context, connectionID string) (SyncRun, error) {
	run, err := scanSyncRun(s.DB.QueryRowContext(ctx, syncRunSelect+` WHERE r.connection_id=? AND r.status IN ('queued','running') ORDER BY r.enqueued_at,r.run_id LIMIT 1`, connectionID))
	if err == sql.ErrNoRows {
		return SyncRun{}, ErrSyncNotFound
	}
	return run, err
}

// SyncSchedules returns one read model per readable sync connection. A failed
// last attempt never erases the last successful timestamp; partial counts as a
// refresh, matching the Python source_run_history contract.
func (s *Store) SyncSchedules(ctx context.Context, p Principal, now time.Time) ([]SyncSchedule, error) {
	sources, err := s.SyncSourcesFor(ctx, p)
	if err != nil {
		return nil, err
	}
	result := []SyncSchedule{}
	for _, source := range sources {
		schedule := SyncSchedule{
			ConnectionID: source.ConnectionID, Connector: source.Connector, Collection: source.Collection, Scope: source.Scope,
			Enabled: source.Enabled, SourceSystem: syncSourceSystem(source), IntervalMinutes: source.Interval,
			StaleAfterIntervals: 3, Freshness: "unknown",
		}
		var lastSuccess, lastAttempt sql.NullString
		var active int
		var lastStatus sql.NullString
		err = s.DB.QueryRowContext(ctx, `SELECT
			max(completed_at) FILTER (WHERE status IN ('completed','partial')) AS last_success,
			max(coalesce(completed_at,started_at,enqueued_at)) AS last_attempt,
			coalesce(max(CASE WHEN status IN ('queued','running') THEN 1 ELSE 0 END),0) AS active
			FROM lab_sync_runs WHERE connection_id=?`, source.ConnectionID).Scan(&lastSuccess, &lastAttempt, &active)
		if err != nil {
			return nil, err
		}
		err = s.DB.QueryRowContext(ctx, `SELECT status FROM lab_sync_runs WHERE connection_id=? ORDER BY coalesce(completed_at,started_at,enqueued_at) DESC,run_id DESC LIMIT 1`, source.ConnectionID).Scan(&lastStatus)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		var failures int
		if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM lab_sync_runs WHERE connection_id=? AND status='failed' AND (? IS NULL OR completed_at>?)`, source.ConnectionID, lastSuccess, lastSuccess).Scan(&failures); err != nil {
			return nil, err
		}
		schedule.Active = active == 1
		schedule.LastSuccessAt = lastSuccess.String
		schedule.LastAttemptAt = lastAttempt.String
		schedule.LastStatus = lastStatus.String
		schedule.ConsecutiveFailure = failures
		if source.Interval > 0 && lastSuccess.Valid && lastSuccess.String != "" {
			if success, parseErr := time.Parse(time.RFC3339, lastSuccess.String); parseErr == nil {
				intervals := now.Sub(success).Hours() / float64(source.Interval)
				if intervals < 0 {
					intervals = 0
				}
				schedule.IntervalsSince = &intervals
				schedule.Freshness = "fresh"
				if intervals > float64(schedule.StaleAfterIntervals) {
					schedule.Freshness = "stale"
				}
			}
		}
		if source.Interval > 0 && !schedule.Active {
			base := now
			if lastAttempt.Valid && lastAttempt.String != "" {
				if parsed, parseErr := time.Parse(time.RFC3339, lastAttempt.String); parseErr == nil {
					base = parsed
				}
			}
			schedule.NextScheduledAt = base.Add(time.Duration(source.Interval) * time.Minute).UTC().Format(time.RFC3339)
		}
		result = append(result, schedule)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ConnectionID < result[j].ConnectionID })
	return result, nil
}

func syncSourceSystem(source SyncSource) string {
	return syncSourceSystemForConnector(source.Connector)
}

func syncSourceSystemForConnector(connector string) string {
	if connector == syncConnectorCyberArkPAMSafes {
		return "cyberark"
	}
	// The typed UI contract groups the normalized user/device inventory under
	// the netskope label; this does not assert a provider adapter.
	return "netskope"
}

func syncSourceFile(connector, connectionID string) string {
	if connector == syncConnectorCyberArkPAMSafes {
		return "cyberark-pam-safes:" + connectionID
	}
	return "http-inventory:" + connectionID
}

// SyncRecords returns active or retired observations with provenance. Hidden
// connections return ErrDenied rather than an empty page.
func (s *Store) SyncRecords(ctx context.Context, p Principal, connectionID, collection, status string, limit int) ([]SyncRecord, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "retired" && status != "all" {
		return nil, fmt.Errorf("invalid record status")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	source, err := s.readableSyncSource(ctx, tx, p.ID, connectionID)
	if err != nil {
		return nil, ErrDenied
	}
	if collection == "" {
		collection = source.Collection
	}
	if collection != source.Collection {
		return nil, fmt.Errorf("collection does not belong to this connection")
	}
	query := `SELECT external_id,document,status,first_run_id,last_run_id,scope,generation,updated_at,retired_at,retired_by_run_id FROM lab_observations WHERE connection_id=? AND collection=?`
	args := []any{connectionID, collection}
	if status != "all" {
		query += " AND status=?"
		args = append(args, status)
	}
	query += " ORDER BY row_id LIMIT ?"
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SyncRecord{}
	for rows.Next() {
		var record SyncRecord
		var document []byte
		if err = rows.Scan(&record.ExternalID, &document, &record.Status, &record.FirstRunID, &record.LastRunID, &record.Scope, &record.Generation, &record.UpdatedAt, &record.RetiredAt, &record.RetiredByRunID); err != nil {
			return nil, err
		}
		record.Attributes = map[string]string{}
		if err = json.Unmarshal(document, &record.Attributes); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return result, tx.Commit()
}

// SetSyncReview keeps the human decision in a separate table. Synchronization
// never updates this row. It is a store operation today; an HTTP route must
// require a dedicated review capability before exposing it.
func (s *Store) SetSyncReview(ctx context.Context, p Principal, connectionID, collection, externalID, owner, note string, now time.Time) error {
	owner = strings.TrimSpace(owner)
	note = strings.TrimSpace(note)
	if len(owner) > 200 || len(note) > 2000 || strings.ContainsAny(owner+note, "\x00") {
		return fmt.Errorf("invalid review")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	source, err := s.readableSyncSource(ctx, tx, p.ID, connectionID)
	if err != nil {
		return ErrDenied
	}
	if collection != source.Collection {
		return fmt.Errorf("collection does not belong to this connection")
	}
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_observations WHERE connection_id=? AND collection=? AND external_id=?", connectionID, collection, externalID).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return ErrSyncNotFound
	}
	before := SyncReview{}
	_ = tx.QueryRowContext(ctx, "SELECT owner,note,updated_by,updated_at FROM lab_sync_reviews WHERE connection_id=? AND collection=? AND external_id=?", connectionID, collection, externalID).Scan(&before.Owner, &before.Note, &before.UpdatedBy, &before.UpdatedAt)
	stamp := now.UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, `INSERT INTO lab_sync_reviews(connection_id,collection,external_id,owner,note,updated_by,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(connection_id,collection,external_id) DO UPDATE SET owner=excluded.owner,note=excluded.note,updated_by=excluded.updated_by,updated_at=excluded.updated_at`, connectionID, collection, externalID, owner, note, p.Actor(), stamp); err != nil {
		return err
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(SyncReview{ConnectionID: connectionID, Collection: collection, ExternalID: externalID, Owner: owner, Note: note, UpdatedBy: p.Actor(), UpdatedAt: stamp})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_review',?,?,?)", p.Actor(), beforeJSON, afterJSON, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncReview returns a stored human decision for tests and future read APIs.
func (s *Store) SyncReview(ctx context.Context, p Principal, connectionID, collection, externalID string) (SyncReview, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SyncReview{}, err
	}
	defer tx.Rollback()
	source, err := s.readableSyncSource(ctx, tx, p.ID, connectionID)
	if err != nil {
		return SyncReview{}, ErrDenied
	}
	if collection != source.Collection {
		return SyncReview{}, ErrSyncNotFound
	}
	review := SyncReview{ConnectionID: connectionID, Collection: collection, ExternalID: externalID}
	err = tx.QueryRowContext(ctx, "SELECT owner,note,updated_by,updated_at FROM lab_sync_reviews WHERE connection_id=? AND collection=? AND external_id=?", connectionID, collection, externalID).Scan(&review.Owner, &review.Note, &review.UpdatedBy, &review.UpdatedAt)
	if err == sql.ErrNoRows {
		return review, ErrSyncNotFound
	}
	if err != nil {
		return SyncReview{}, err
	}
	return review, tx.Commit()
}
