package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = 10
const ReplayCapacity = 65536

var ErrReplay = errors.New("assertion already used")
var ErrReplayCapacity = errors.New("assertion replay capacity reached")

func migrateGateway(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS gateway_administrators(principal_id INTEGER PRIMARY KEY REFERENCES lab_principals(id));
 CREATE TABLE IF NOT EXISTS gateway_bootstrap(id INTEGER PRIMARY KEY CHECK(id=1),principal_id INTEGER NOT NULL REFERENCES lab_principals(id));
 CREATE TABLE IF NOT EXISTS gateway_replay(issuer TEXT NOT NULL,jti TEXT NOT NULL,expires_at INTEGER NOT NULL,PRIMARY KEY(issuer,jti));
 CREATE INDEX IF NOT EXISTS gateway_replay_expiry ON gateway_replay(expires_at);
 INSERT OR IGNORE INTO lab_schema_migrations VALUES(4);`)
	return err
}
func (s *Store) CheckSchema(ctx context.Context) error {
	var version int
	if err := s.DB.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil || version != SchemaVersion {
		return fmt.Errorf("run migrate with a compatible binary")
	}
	return nil
}
func (s *Store) ResolvePrincipal(ctx context.Context, issuer, subject string) (Principal, error) {
	var p Principal
	err := s.DB.QueryRowContext(ctx, "SELECT id,issuer,subject FROM lab_principals WHERE issuer=? AND subject=? AND enabled=1", issuer, subject).Scan(&p.ID, &p.Issuer, &p.Subject)
	return p, err
}
func (s *Store) IsAdministrator(ctx context.Context, p Principal) bool {
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM gateway_administrators a JOIN lab_principals p ON p.id=a.principal_id WHERE p.id=? AND p.enabled=1", p.ID).Scan(&count)
	return err == nil && count == 1
}

// ConsumeAssertion is persistent and atomic across processes. No JWT is stored.
func (s *Store) ConsumeAssertion(ctx context.Context, issuer, id string, expires, now int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM gateway_replay WHERE expires_at<=?", now); err != nil {
		return err
	}
	var used int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_replay WHERE issuer=? AND jti=?", issuer, id).Scan(&used); err != nil {
		return err
	}
	if used != 0 {
		return ErrReplay
	}
	var total int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_replay").Scan(&total); err != nil {
		return err
	}
	if total >= ReplayCapacity {
		return ErrReplayCapacity
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_replay VALUES(?,?,?)", issuer, id, expires); err != nil {
		return err
	}
	return tx.Commit()
}

func validExternalIdentity(issuer, subject string) bool {
	return issuer != "" && issuer != "custodia-lab" && strings.TrimSpace(subject) != "" && len(subject) <= 1024
}

// BootstrapAdministrator is an explicit host operation, never a sign-in side effect.
func (s *Store) BootstrapAdministrator(ctx context.Context, issuer, subject string, connections []string) error {
	if !validExternalIdentity(issuer, subject) {
		return ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_bootstrap").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("first administrator already configured")
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO lab_principals(issuer,subject,enabled) VALUES(?,?,1)", issuer, subject)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_grants VALUES(?,?)", id, connection); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_administrators VALUES(?)", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_bootstrap VALUES(1,?)", id); err != nil {
		return err
	}
	after, _ := json.Marshal(map[string]any{"issuer": issuer, "subject": subject, "connections": connections})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES('host','bootstrap_administrator','{}',?,?)", after, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func administratorTx(ctx context.Context, tx *sql.Tx, p Principal) error {
	var count int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_administrators a JOIN lab_principals p ON p.id=a.principal_id WHERE p.id=? AND p.issuer=? AND p.enabled=1", p.ID, p.Issuer).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrDenied
	}
	return nil
}

// CreateReader cannot grant more source access than its administrator holds.
// JWT email/groups never enter this operation.
func (s *Store) CreateReader(ctx context.Context, actor Principal, subject string, connections []string) error {
	if !validExternalIdentity(actor.Issuer, subject) {
		return ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	for _, connection := range connections {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_grants WHERE principal_id=? AND connection_id=?", actor.ID, connection).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return ErrDenied
		}
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO lab_principals(issuer,subject,enabled) VALUES(?,?,1)", actor.Issuer, subject)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_grants VALUES(?,?)", id, connection); err != nil {
			return err
		}
	}
	after, _ := json.Marshal(map[string]any{"issuer": actor.Issuer, "subject": subject, "connections": connections})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'authorize_reader','{}',?,?)", actor.Actor(), after, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// DisableReader protects administrators from remote lockout; host recovery is explicit.
func (s *Store) DisableReader(ctx context.Context, actor Principal, subject string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	var target int
	if err = tx.QueryRowContext(ctx, "SELECT id FROM lab_principals WHERE issuer=? AND subject=? AND enabled=1", actor.Issuer, subject).Scan(&target); err != nil {
		return ErrDenied
	}
	var admins int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_administrators WHERE principal_id=?", target).Scan(&admins); err != nil {
		return err
	}
	if admins != 0 {
		return ErrDenied
	}
	// An administrator may only disable a reader whose complete scope it may manage.
	var outside int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_grants g WHERE g.principal_id=? AND NOT EXISTS(SELECT 1 FROM lab_grants own WHERE own.principal_id=? AND own.connection_id=g.connection_id)", target, actor.ID).Scan(&outside); err != nil {
		return err
	}
	if outside != 0 {
		return ErrDenied
	}
	if _, err = tx.ExecContext(ctx, "UPDATE lab_principals SET enabled=0 WHERE id=?", target); err != nil {
		return err
	}
	after, _ := json.Marshal(map[string]any{"issuer": actor.Issuer, "subject": subject, "enabled": false})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'disable_reader','{}',?,?)", actor.Actor(), after, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}
