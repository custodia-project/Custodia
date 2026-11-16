// Package lab is an explicitly synthetic, single-instance migration laboratory.
package lab

import (
	"context"
	"custodia/server/internal/coverage"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"path/filepath"
	"strconv"
	"time"
)

//go:embed company.json
var fixture []byte

type Person struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	IdentityIDs []int  `json:"identityIds"`
	NetskopeID  int    `json:"netskopeId"`
	PamID       *int   `json:"pamId"`
}
type Device struct {
	ExternalID string `json:"deviceId,omitempty"`
	PersonID   int    `json:"personId"`
	Last       string `json:"lastSignalAt"`
}
type Company struct {
	normalized bool
	runs       map[string]sourceRun
	sourceIDs  map[int]string
	userCounts map[int]int
	Company    struct {
		AsOf     string `json:"asOf"`
		Captured string `json:"capturedAt"`
	} `json:"company"`
	People  []Person `json:"people"`
	Devices []Device `json:"devices"`
}

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database file required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	location := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)"} {
		query.Add("_pragma", pragma)
	}
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db}, nil
}

// Migrate is explicit; serving never creates or migrates a database.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='lab_schema_migrations'").Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		var version int
		if err = tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
			return err
		}
		if version > SchemaVersion {
			return fmt.Errorf("unsupported schema")
		}
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY CHECK(version=1));
 CREATE TABLE IF NOT EXISTS lab_snapshot(id INTEGER PRIMARY KEY CHECK(id=1), captured_at TEXT NOT NULL, document BLOB NOT NULL);
 INSERT OR IGNORE INTO schema_version VALUES(1);`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS lab_schema_migrations(version INTEGER PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS lab_preferences(actor TEXT PRIMARY KEY, document BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS lab_audit(id INTEGER PRIMARY KEY, actor TEXT NOT NULL, action TEXT NOT NULL, before_json BLOB NOT NULL, after_json BLOB NOT NULL, at TEXT NOT NULL);
 INSERT OR IGNORE INTO lab_schema_migrations VALUES(1);
 INSERT OR IGNORE INTO lab_schema_migrations VALUES(2);`)
	if err != nil {
		return err
	}
	if err = migrateObservations(ctx, tx); err != nil {
		return err
	}
	if err = migrateGateway(ctx, tx); err != nil {
		return err
	}
	if err = migrateSync(ctx, tx); err != nil {
		return err
	}
	if err = migrateScheduling(ctx, tx); err != nil {
		return err
	}
	if err = migrateConnections(ctx, tx); err != nil {
		return err
	}
	if err = migrateConnectionGroups(ctx, tx); err != nil {
		return err
	}
	if err = migrateLocalAuth(ctx, tx); err != nil {
		return err
	}
	if err = migrateOIDC(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Seed(ctx context.Context, now time.Time) error {
	var c Company
	if err := json.Unmarshal(fixture, &c); err != nil {
		return err
	}
	// Shift once at explicit seed time, never on reads or server restarts.
	asof, _ := time.Parse(time.RFC3339, c.Company.AsOf)
	offset := now.Sub(asof)
	captured, _ := time.Parse(time.RFC3339, c.Company.Captured)
	c.Company.AsOf = now.UTC().Format(time.RFC3339)
	c.Company.Captured = captured.Add(offset).UTC().Format(time.RFC3339)
	for i := range c.Devices {
		t, err := time.Parse(time.RFC3339, c.Devices[i].Last)
		if err != nil {
			return err
		}
		c.Devices[i].Last = t.Add(offset).UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_snapshot VALUES(1,?,?)", c.Company.Captured, b); err != nil {
		return err
	}
	if err = normalize(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Load(ctx context.Context) (Company, error) {
	var c Company
	var b []byte
	var migrated int
	if err := s.DB.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&migrated); err != nil || migrated != SchemaVersion {
		return c, fmt.Errorf("run migrate-lab with a compatible binary")
	}
	var v int
	if err := s.DB.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&v); err != nil {
		return c, fmt.Errorf("run migrate first")
	}
	if v != 1 {
		return c, fmt.Errorf("unsupported schema")
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT document FROM lab_snapshot WHERE id=1").Scan(&b); err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func (c Company) Fresh(now time.Time) bool {
	if c.normalized {
		if len(c.runs) != 2 {
			return false
		}
		for _, run := range c.runs {
			completed, _ := time.Parse(time.RFC3339, run.Completed)
			if !coverage.Fresh(run.Status, completed, now) {
				return false
			}
		}
		return true
	}
	t, _ := time.Parse(time.RFC3339, c.Company.Captured)
	return coverage.Fresh("completed", t, now)
}
func (c Company) Evidence(p Person, now time.Time) coverage.Evidence {
	e := coverage.Evidence{Identity: len(p.IdentityIDs)}
	if c.normalized {
		e.Users = c.userCounts[p.ID]
	} else if p.NetskopeID != 0 {
		e.Users = 1
	}
	if e.Identity != 1 || e.Users != 1 {
		return e
	}
	e.Recent, e.Old = c.DeviceCounts(p, now)
	return e
}

// DeviceCounts is a tool observation; it does not assert a cross-source identity.
func (c Company) DeviceCounts(p Person, now time.Time) (recent, old int) {
	for _, d := range c.Devices {
		if d.PersonID != p.ID {
			continue
		}
		t, err := time.Parse(time.RFC3339, d.Last)
		age := now.Sub(t)
		if err == nil && age >= 0 && age <= 7*24*time.Hour {
			recent++
		} else {
			old++
		}
	}
	return
}

func (c Company) Sources(now time.Time) map[string]any {
	if c.normalized {
		result := map[string]any{}
		for key, run := range c.runs {
			system := run.Kind
			if key == "identity" {
				system = "identity_administration"
			}
			freshness := "missing"
			var completed any
			var age any
			if run.Completed != "" {
				completed = run.Completed
				t, err := time.Parse(time.RFC3339, run.Completed)
				if err == nil {
					age = round2(now.Sub(t).Hours())
				}
				freshness = "incomplete"
				if run.Status == "completed" {
					freshness = "stale"
					if coverage.Fresh(run.Status, t, now) {
						freshness = "fresh"
					}
				}
			}
			var status any = run.Status
			var runID any = "acme-lab-" + run.Connection
			if run.Status == "missing" {
				status = nil
				runID = nil
			}
			result[key] = map[string]any{"source_system": system, "source_file": "fictional-" + system, "run_id": runID, "connection_id": run.Connection, "status": status, "completed_at": completed, "age_hours": age, "freshness": freshness}
		}
		return result
	}
	freshness := "stale"
	if c.Fresh(now) {
		freshness = "fresh"
	}
	result := map[string]any{}
	for key, system := range map[string]string{"identity": "identity_administration", "netskope": "netskope"} {
		result[key] = map[string]any{"source_system": system, "source_file": "fictional-" + system, "run_id": "acme-lab-" + system, "status": "completed", "started_at": c.Company.Captured, "completed_at": c.Company.Captured, "freshness": freshness}
	}
	return result
}
func (c Company) Detail(email string, now time.Time) map[string]any {
	e := coverage.Evidence{}
	status := "unknown"
	if c.Fresh(now) {
		status = "not_applicable"
	}
	for _, p := range c.People {
		if p.Email == email {
			e = c.Evidence(p, now)
			status = coverage.Classify(e, c.Fresh(now))
			break
		}
	}
	explanation := map[string]string{"covered": "Todos los dispositivos activos vinculados son recientes (últimos 7 días).", "partial": "Hay dispositivos activos recientes y otros no recientes o sin fecha; esto no confirma su antigüedad.", "gap": "El vínculo es único, pero no hay dispositivos activos recientes.", "unknown": "El vínculo o la evidencia de fuente es ambiguo, incompleto o no vigente.", "not_applicable": "La dirección no pertenece a la población elegible de Identity Administration."}[status]
	return map[string]any{"metric_code": "CONN-001", "metric_version": "1", "email": email, "status": status, "identity_records": e.Identity, "netskope_users": e.Users, "devices_recent": e.Recent, "devices_not_recent": e.Old, "devices_total": e.Recent + e.Old, "devices_ambiguous": e.Ambiguous, "sources": c.Sources(now), "explanation": explanation}
}

// Summary uses the Python denominator: devices from uniquely linked people only.
func (c Company) Summary(now time.Time) map[string]any {
	counts := map[string]any{"denominator": len(c.People), "covered": 0, "partial": 0, "gap": 0, "unknown": 0, "coverage_percent": nil, "devices_recent": 0, "devices_total": 0, "device_coverage_percent": nil}
	recent, total := 0, 0
	denominator := 0
	for _, p := range c.People {
		weight := len(p.IdentityIDs)
		if weight == 0 {
			continue
		}
		denominator += weight
		e := c.Evidence(p, now)
		status := coverage.Classify(e, c.Fresh(now))
		counts[status] = counts[status].(int) + weight
		if e.Identity == 1 && e.Users == 1 {
			recent += e.Recent
			total += e.Recent + e.Old
		}
	}
	counts["denominator"] = denominator
	counts["devices_recent"] = recent
	counts["devices_total"] = total
	if c.Fresh(now) {
		if denominator > 0 {
			counts["coverage_percent"] = round2(float64(counts["covered"].(int)) * 100 / float64(denominator))
		}
		if total > 0 {
			counts["device_coverage_percent"] = round2(float64(recent) * 100 / float64(total))
		}
	}
	return map[string]any{"metric_code": "CONN-001", "metric_version": "1", "label": "Cliente Netskope activo y observado recientemente", "population": "Usuarios humanos activos de Identity Administration", "person_freshness_hours": 24, "device_freshness_days": 7, "counts": counts, "sources": c.Sources(now)}
}

// Decimal formatting rounds the exact binary64 value, matching Python round(v, 2).
// Multiplying by 100 first loses the distinction at values such as 2.675.
func round2(value float64) float64 {
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'f', 2, 64), 64)
	return rounded
}
