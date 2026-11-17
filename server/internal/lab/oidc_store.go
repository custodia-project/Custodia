package lab

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
)

// OIDC is an explicit authentication mode: a relying-party client configured
// from the administration console. The issuer is stored in SQLite, never
// chosen by the browser, and every identity is (issuer, subject). Pending
// identities are approved by a local administrator before they receive any
// principal or connection grant.
const (
	OIDCStateLifetime  = 10 * time.Minute
	OIDCPendingLimit   = 1024
	OIDCTestValidity   = 10 * time.Minute
	OIDCStateLimit     = 4096
	OIDCSecretRefLimit = 512
)

var (
	ErrOIDCNotConfigured = errors.New("OIDC is not configured")
	ErrOIDCDisabled      = errors.New("OIDC is not enabled")
	ErrOIDCState         = errors.New("OIDC state is missing or expired")
	ErrOIDCPending       = errors.New("OIDC identity is pending approval")
	ErrOIDCUnknown       = errors.New("OIDC identity is not known")
	ErrOIDCTestStale     = errors.New("OIDC configuration was not tested after its last change")
	ErrOIDCPendingFull   = errors.New("OIDC pending capacity reached")
)

// migrateOIDC is migration 10: the relying-party configuration, one-time
// login state, approved accounts and the pending-approval queue. It is
// additive and idempotent.
func migrateOIDC(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS oidc_config(
 id INTEGER PRIMARY KEY CHECK(id=1),
 issuer TEXT NOT NULL,
 client_id TEXT NOT NULL,
 client_secret_ref TEXT NOT NULL DEFAULT '',
 redirect_uri TEXT NOT NULL,
 scopes TEXT NOT NULL,
 subject_claim TEXT NOT NULL,
 email_claim TEXT NOT NULL,
 name_claim TEXT NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 tested_at TEXT,
 test_ok INTEGER NOT NULL DEFAULT 0,
 test_detail TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL,
 updated_by TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS oidc_states(
 state_hash TEXT PRIMARY KEY,
 nonce TEXT NOT NULL,
 verifier TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS oidc_states_expiry ON oidc_states(expires_at);
CREATE TABLE IF NOT EXISTS oidc_accounts(
 principal_id INTEGER PRIMARY KEY REFERENCES lab_principals(id),
 issuer TEXT NOT NULL,
 subject TEXT NOT NULL,
 display_name TEXT NOT NULL,
 email TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 last_login_at TEXT,
 UNIQUE(issuer,subject)
);
CREATE TABLE IF NOT EXISTS oidc_pending(
 issuer TEXT NOT NULL,
 subject TEXT NOT NULL,
 email TEXT NOT NULL DEFAULT '',
 display_name TEXT NOT NULL,
 first_seen TEXT NOT NULL,
 last_seen TEXT NOT NULL,
 PRIMARY KEY(issuer,subject)
);
INSERT OR IGNORE INTO lab_schema_migrations VALUES(10);`)
	return err
}

// OIDCConfig never carries a client secret, only a reference to it.
type OIDCConfig struct {
	Issuer          string  `json:"issuer"`
	ClientID        string  `json:"client_id"`
	ClientSecretRef string  `json:"client_secret_ref"`
	RedirectURI     string  `json:"redirect_uri"`
	Scopes          string  `json:"scopes"`
	SubjectClaim    string  `json:"subject_claim"`
	EmailClaim      string  `json:"email_claim"`
	NameClaim       string  `json:"name_claim"`
	Enabled         bool    `json:"enabled"`
	TestedAt        *string `json:"tested_at"`
	TestOK          bool    `json:"test_ok"`
	TestDetail      string  `json:"test_detail"`
	UpdatedAt       string  `json:"updated_at"`
	UpdatedBy       string  `json:"updated_by"`
}

func (s *Store) GetOIDCConfig(ctx context.Context) (OIDCConfig, error) {
	var c OIDCConfig
	var tested sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT issuer,client_id,client_secret_ref,redirect_uri,scopes,subject_claim,email_claim,name_claim,enabled,tested_at,test_ok,test_detail,updated_at,updated_by FROM oidc_config WHERE id=1`).
		Scan(&c.Issuer, &c.ClientID, &c.ClientSecretRef, &c.RedirectURI, &c.Scopes, &c.SubjectClaim, &c.EmailClaim, &c.NameClaim, &c.Enabled, &tested, &c.TestOK, &c.TestDetail, &c.UpdatedAt, &c.UpdatedBy)
	if err == sql.ErrNoRows {
		return c, ErrOIDCNotConfigured
	}
	if err != nil {
		return c, err
	}
	if tested.Valid {
		value := tested.String
		c.TestedAt = &value
	}
	return c, nil
}

// SaveOIDCConfig stores a draft. Any change disables the provider and clears
// the previous test, so a validated configuration cannot be altered silently.
func (s *Store) SaveOIDCConfig(ctx context.Context, actor Principal, c OIDCConfig, now time.Time) error {
	if len(c.Issuer) > 2048 || len(c.ClientID) > 512 || len(c.ClientSecretRef) > OIDCSecretRefLimit || len(c.RedirectURI) > 2048 || len(c.Scopes) > 512 || len(c.SubjectClaim) > 128 || len(c.EmailClaim) > 128 || len(c.NameClaim) > 128 {
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
	stamp := now.UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `INSERT INTO oidc_config(id,issuer,client_id,client_secret_ref,redirect_uri,scopes,subject_claim,email_claim,name_claim,enabled,tested_at,test_ok,test_detail,updated_at,updated_by)
VALUES(1,?,?,?,?,?,?,?,?,0,NULL,0,'',?,?)
ON CONFLICT(id) DO UPDATE SET issuer=excluded.issuer,client_id=excluded.client_id,client_secret_ref=excluded.client_secret_ref,redirect_uri=excluded.redirect_uri,scopes=excluded.scopes,subject_claim=excluded.subject_claim,email_claim=excluded.email_claim,name_claim=excluded.name_claim,enabled=0,tested_at=NULL,test_ok=0,test_detail='',updated_at=excluded.updated_at,updated_by=excluded.updated_by`,
		c.Issuer, c.ClientID, c.ClientSecretRef, c.RedirectURI, c.Scopes, c.SubjectClaim, c.EmailClaim, c.NameClaim, stamp, actor.Actor())
	if err != nil {
		return err
	}
	// Any change invalidates live OIDC sessions: the issuer or client the
	// session was created under is no longer the configured one. Pending
	// identities from a previous issuer can no longer authenticate and are
	// removed instead of accumulating until the queue fills.
	revoked, err := tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE principal_id IN (SELECT principal_id FROM oidc_accounts)")
	if err != nil {
		return err
	}
	sessions, _ := revoked.RowsAffected()
	cleared, err := tx.ExecContext(ctx, "DELETE FROM oidc_pending WHERE issuer<>?", c.Issuer)
	if err != nil {
		return err
	}
	pending, _ := cleared.RowsAffected()
	// The secret reference is a pointer, never a value; it is safe to audit
	// but it adds no operational value, so it is omitted.
	if err = audit(ctx, tx, actor.Actor(), "save_oidc_config", map[string]any{
		"issuer": c.Issuer, "client_id": c.ClientID, "redirect_uri": c.RedirectURI,
		"scopes": c.Scopes, "subject_claim": c.SubjectClaim, "email_claim": c.EmailClaim,
		"name_claim": c.NameClaim, "enabled": false, "revoked_sessions": sessions, "cleared_pending": pending,
	}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordOIDCTest stores the result of discovery and key validation. Only a
// successful test of the current configuration authorizes activation.
func (s *Store) RecordOIDCTest(ctx context.Context, actor Principal, ok bool, detail string, now time.Time) error {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	stamp := now.UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `UPDATE oidc_config SET tested_at=?,test_ok=?,test_detail=? WHERE id=1`, stamp, boolInt(ok), detail)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrOIDCNotConfigured
	}
	if err = audit(ctx, tx, actor.Actor(), "test_oidc_config", map[string]any{"ok": ok, "detail": detail}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// EnableOIDC requires a successful test of the current configuration, made in
// the last ten minutes. It never tests implicitly.
func (s *Store) EnableOIDC(ctx context.Context, actor Principal, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	var tested sql.NullString
	var ok int
	var updated string
	err = tx.QueryRowContext(ctx, `SELECT tested_at,test_ok,updated_at FROM oidc_config WHERE id=1`).Scan(&tested, &ok, &updated)
	if err == sql.ErrNoRows {
		return ErrOIDCNotConfigured
	}
	if err != nil {
		return err
	}
	if ok != 1 || !tested.Valid || tested.String < updated {
		return ErrOIDCTestStale
	}
	testedAt, err := time.Parse(time.RFC3339, tested.String)
	if err != nil || now.Sub(testedAt) > OIDCTestValidity || now.Before(testedAt) {
		return ErrOIDCTestStale
	}
	if _, err = tx.ExecContext(ctx, "UPDATE oidc_config SET enabled=1 WHERE id=1"); err != nil {
		return err
	}
	// Switching to OIDC ends every local reader session. The break-glass
	// administrator keeps its local credentials; readers do not.
	revoked, err := tx.ExecContext(ctx, `DELETE FROM local_sessions WHERE principal_id IN (
SELECT p.id FROM lab_principals p JOIN local_accounts a ON a.principal_id=p.id
WHERE p.issuer=? AND NOT EXISTS(SELECT 1 FROM gateway_administrators g WHERE g.principal_id=p.id))`, LocalIssuer)
	if err != nil {
		return err
	}
	sessions, _ := revoked.RowsAffected()
	if err = audit(ctx, tx, actor.Actor(), "enable_oidc", map[string]any{"enabled": true, "revoked_local_readers": sessions}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DisableOIDC(ctx context.Context, actor Principal, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE oidc_config SET enabled=0 WHERE id=1"); err != nil {
		return err
	}
	revoked, err := tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE principal_id IN (SELECT principal_id FROM oidc_accounts)")
	if err != nil {
		return err
	}
	sessions, _ := revoked.RowsAffected()
	if err = audit(ctx, tx, actor.Actor(), "disable_oidc", map[string]any{"enabled": false, "revoked_sessions": sessions}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateOIDCState persists one login attempt. Only the hash of the browser
// state is stored; the nonce and PKCE verifier live for ten minutes and are
// consumed once.
func (s *Store) CreateOIDCState(ctx context.Context, stateHash, nonce, verifier string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM oidc_states WHERE expires_at<=?", now.Unix()); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM oidc_states").Scan(&count); err != nil {
		return err
	}
	if count >= OIDCStateLimit {
		return ErrOIDCState
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO oidc_states VALUES(?,?,?,?,?)", stateHash, nonce, verifier, now.Unix(), now.Add(OIDCStateLifetime).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeOIDCState removes the attempt before returning it, so a code or
// state cannot be replayed.
func (s *Store) ConsumeOIDCState(ctx context.Context, stateHash string, now time.Time) (nonce, verifier string, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var created, expires int64
	err = tx.QueryRowContext(ctx, "SELECT nonce,verifier,created_at,expires_at FROM oidc_states WHERE state_hash=?", stateHash).Scan(&nonce, &verifier, &created, &expires)
	if err == sql.ErrNoRows {
		return "", "", ErrOIDCState
	}
	if err != nil {
		return "", "", err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM oidc_states WHERE state_hash=?", stateHash); err != nil {
		return "", "", err
	}
	if now.Unix() >= expires || created > now.Unix()+60 {
		return "", "", ErrOIDCState
	}
	return nonce, verifier, tx.Commit()
}

// OIDCAccount is an approved external identity.
type OIDCAccount struct {
	Issuer        string   `json:"issuer"`
	Subject       string   `json:"subject"`
	DisplayName   string   `json:"display_name"`
	Email         string   `json:"email"`
	Enabled       bool     `json:"enabled"`
	LastLoginAt   *string  `json:"last_login_at"`
	ActiveSession int      `json:"active_sessions"`
	Connections   []string `json:"connections"`
}

// OIDCIdentity resolves an approved account. Unknown or disabled identities
// never authenticate.
func (s *Store) OIDCIdentity(ctx context.Context, issuer, subject string) (Principal, OIDCAccount, error) {
	var p Principal
	var a OIDCAccount
	var last sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT p.id,p.issuer,p.subject,a.subject,a.display_name,a.email,p.enabled,a.last_login_at
FROM lab_principals p JOIN oidc_accounts a ON a.principal_id=p.id WHERE p.issuer=? AND p.subject=?`, issuer, subject).
		Scan(&p.ID, &p.Issuer, &p.Subject, &a.Subject, &a.DisplayName, &a.Email, &a.Enabled, &last)
	if last.Valid {
		value := last.String
		a.LastLoginAt = &value
	}
	if err == sql.ErrNoRows {
		return p, a, ErrOIDCUnknown
	}
	return p, a, err
}

// RecordOIDCPending queues an identity that passed token validation but has no
// approval yet. It never creates a principal or a grant.
func cleanOIDCClaim(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	return value
}

func (s *Store) RecordOIDCPending(ctx context.Context, issuer, subject, email, name string, now time.Time) error {
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > 512 || strings.IndexFunc(subject, unicode.IsControl) >= 0 {
		return ErrDenied
	}
	email = cleanOIDCClaim(email, 512)
	name = cleanOIDCClaim(name, 200)
	if name == "" {
		name = subject
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM oidc_pending WHERE issuer=? AND subject=?", issuer, subject).Scan(&existing); err != nil {
		return err
	}
	if existing == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM oidc_pending").Scan(&count); err != nil {
			return err
		}
		if count >= OIDCPendingLimit {
			return ErrOIDCPendingFull
		}
	}
	stamp := now.UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `INSERT INTO oidc_pending(issuer,subject,email,display_name,first_seen,last_seen) VALUES(?,?,?,?,?,?)
ON CONFLICT(issuer,subject) DO UPDATE SET email=excluded.email,display_name=excluded.display_name,last_seen=excluded.last_seen`,
		issuer, subject, email, name, stamp, stamp)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type OIDCPending struct {
	Issuer      string `json:"issuer"`
	Subject     string `json:"subject"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	FirstSeen   string `json:"first_seen"`
	LastSeen    string `json:"last_seen"`
}

func (s *Store) ListOIDCPending(ctx context.Context, actor Principal) ([]OIDCPending, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT issuer,subject,email,display_name,first_seen,last_seen FROM oidc_pending ORDER BY last_seen DESC,issuer,subject")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := []OIDCPending{}
	for rows.Next() {
		var p OIDCPending
		if err = rows.Scan(&p.Issuer, &p.Subject, &p.Email, &p.DisplayName, &p.FirstSeen, &p.LastSeen); err != nil {
			return nil, err
		}
		pending = append(pending, p)
	}
	return pending, rows.Err()
}

// ApproveOIDCPending creates the principal only after an administrator grants
// a subset of its own connections. Approval is atomic and audited.
func (s *Store) ApproveOIDCPending(ctx context.Context, actor Principal, issuer, subject string, connections []string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	var email, display string
	if err = tx.QueryRowContext(ctx, "SELECT email,display_name FROM oidc_pending WHERE issuer=? AND subject=?", issuer, subject).Scan(&email, &display); err != nil {
		return ErrOIDCPending
	}
	seen := map[string]bool{}
	for _, connection := range connections {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_grants WHERE principal_id=? AND connection_id=?", actor.ID, connection).Scan(&count); err != nil {
			return err
		}
		if count != 1 || seen[connection] {
			return ErrDenied
		}
		seen[connection] = true
	}
	var existing int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_principals WHERE issuer=? AND subject=?", issuer, subject).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return ErrLocalExists
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO lab_principals(issuer,subject,enabled) VALUES(?,?,1)", issuer, subject)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	stamp := now.UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, "INSERT INTO oidc_accounts(principal_id,issuer,subject,display_name,email,created_at) VALUES(?,?,?,?,?,?)", id, issuer, subject, display, email, stamp); err != nil {
		return err
	}
	for _, connection := range connections {
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_grants VALUES(?,?)", id, connection); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM oidc_pending WHERE issuer=? AND subject=?", issuer, subject); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor.Actor(), "approve_oidc_user", map[string]any{"subject": subject, "issuer": issuer, "connections": connections}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DenyOIDCPending(ctx context.Context, actor Principal, issuer, subject string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM oidc_pending WHERE issuer=? AND subject=?", issuer, subject)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrOIDCPending
	}
	if err = audit(ctx, tx, actor.Actor(), "deny_oidc_user", map[string]any{"subject": subject}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ListOIDCUsers returns approved external identities, never token material.
func (s *Store) ListOIDCUsers(ctx context.Context, actor Principal, now time.Time) ([]OIDCAccount, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id,a.issuer,a.subject,a.display_name,a.email,p.enabled,a.last_login_at,(SELECT count(*) FROM local_sessions s WHERE s.principal_id=p.id AND s.expires_at>?)
FROM lab_principals p JOIN oidc_accounts a ON a.principal_id=p.id ORDER BY a.issuer,a.subject`, now.Unix())
	if err != nil {
		return nil, err
	}
	users := []OIDCAccount{}
	ids := []int{}
	for rows.Next() {
		var u OIDCAccount
		var id int
		var last sql.NullString
		if err = rows.Scan(&id, &u.Issuer, &u.Subject, &u.DisplayName, &u.Email, &u.Enabled, &last, &u.ActiveSession); err != nil {
			rows.Close()
			return nil, err
		}
		if last.Valid {
			value := last.String
			u.LastLoginAt = &value
		}
		u.Connections = []string{}
		users = append(users, u)
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, id := range ids {
		grants, err := tx.QueryContext(ctx, "SELECT connection_id FROM lab_grants WHERE principal_id=? ORDER BY connection_id", id)
		if err != nil {
			return nil, err
		}
		for grants.Next() {
			var connection string
			if err = grants.Scan(&connection); err != nil {
				grants.Close()
				return nil, err
			}
			users[i].Connections = append(users[i].Connections, connection)
		}
		grants.Close()
	}
	return users, tx.Commit()
}

// StartOIDCSession stores only the token digest and audits the sign-in. The
// principal must belong to the configured issuer and be enabled.
func (s *Store) StartOIDCSession(ctx context.Context, p Principal, issuer, tokenHash string, now, expires time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE expires_at<=?", now.Unix()); err != nil {
		return err
	}
	var enabled int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_principals p JOIN oidc_accounts a ON a.principal_id=p.id WHERE p.id=? AND p.issuer=? AND p.enabled=1", p.ID, issuer).Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return ErrDenied
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_sessions VALUES(?,?,?,?,?)", tokenHash, p.ID, now.Unix(), now.Unix(), expires.Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE oidc_accounts SET last_login_at=? WHERE principal_id=?", now.UTC().Format(time.RFC3339), p.ID); err != nil {
		return err
	}
	if err = audit(ctx, tx, p.Actor(), "oidc_login", map[string]any{"issuer": issuer, "subject": p.Subject}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveOIDCSession enforces absolute and idle expiry on the server.
func (s *Store) ResolveOIDCSession(ctx context.Context, tokenHash string, idle time.Duration, now time.Time, issuer string) (LocalSession, error) {
	var session LocalSession
	var lastSeen, expires int64
	err := s.DB.QueryRowContext(ctx, `SELECT p.id,p.issuer,p.subject,a.display_name,s.last_seen_at,s.expires_at
FROM local_sessions s JOIN lab_principals p ON p.id=s.principal_id AND p.enabled=1 JOIN oidc_accounts a ON a.principal_id=p.id
WHERE s.token_hash=? AND p.issuer=?`, tokenHash, issuer).Scan(&session.ID, &session.Issuer, &session.Subject, &session.DisplayName, &lastSeen, &expires)
	if err == sql.ErrNoRows {
		return session, ErrNoSession
	}
	if err != nil {
		return session, err
	}
	current := now.Unix()
	if current >= expires || current-lastSeen >= int64(idle/time.Second) {
		if _, err = s.DB.ExecContext(ctx, "DELETE FROM local_sessions WHERE token_hash=?", tokenHash); err != nil {
			return session, err
		}
		return session, ErrNoSession
	}
	if current-lastSeen >= 60 {
		if _, err = s.DB.ExecContext(ctx, "UPDATE local_sessions SET last_seen_at=? WHERE token_hash=?", current, tokenHash); err != nil {
			return session, err
		}
	}
	return session, nil
}

func (s *Store) EndOIDCSession(ctx context.Context, p Principal, tokenHash string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE token_hash=? AND principal_id=?", tokenHash, p.ID); err != nil {
		return err
	}
	if err = audit(ctx, tx, p.Actor(), "oidc_logout", map[string]any{"username": p.Subject}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// SetOIDCUserEnabled disables an approved reader (revoking sessions) or
// re-enables it. Administrators are never remotes created by approval.
func (s *Store) SetOIDCUserEnabled(ctx context.Context, actor Principal, issuer, subject string, enabled bool, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	var target int
	if err = tx.QueryRowContext(ctx, "SELECT principal_id FROM oidc_accounts WHERE issuer=? AND subject=?", issuer, subject).Scan(&target); err != nil {
		return ErrDenied
	}
	if target == actor.ID {
		return ErrDenied
	}
	var admins int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_administrators WHERE principal_id=?", target).Scan(&admins); err != nil {
		return err
	}
	if admins != 0 {
		return ErrDenied
	}
	var outside int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_grants g WHERE g.principal_id=? AND NOT EXISTS(SELECT 1 FROM lab_grants own WHERE own.principal_id=? AND own.connection_id=g.connection_id)", target, actor.ID).Scan(&outside); err != nil {
		return err
	}
	if outside != 0 {
		return ErrDenied
	}
	flag := 0
	action := "disable_oidc_user"
	if enabled {
		flag = 1
		action = "enable_oidc_user"
	}
	if _, err = tx.ExecContext(ctx, "UPDATE lab_principals SET enabled=? WHERE id=?", flag, target); err != nil {
		return err
	}
	var sessions int64
	if !enabled {
		revoked, err := tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE principal_id=?", target)
		if err != nil {
			return err
		}
		sessions, _ = revoked.RowsAffected()
	}
	if err = audit(ctx, tx, actor.Actor(), action, map[string]any{"issuer": issuer, "subject": subject, "enabled": enabled, "revoked_sessions": sessions}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RevokeOIDCSessions(ctx context.Context, actor Principal, issuer, subject string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	var target int
	if err = tx.QueryRowContext(ctx, "SELECT principal_id FROM oidc_accounts WHERE issuer=? AND subject=?", issuer, subject).Scan(&target); err != nil {
		return ErrDenied
	}
	revoked, err := tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE principal_id=?", target)
	if err != nil {
		return err
	}
	sessions, _ := revoked.RowsAffected()
	if err = audit(ctx, tx, actor.Actor(), "revoke_oidc_sessions", map[string]any{"issuer": issuer, "subject": subject, "revoked_sessions": sessions}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// OIDCConfigured reports the bare minimum for the login screen.
func (s *Store) OIDCConfigured(ctx context.Context) (bool, bool, error) {
	var enabled int
	err := s.DB.QueryRowContext(ctx, "SELECT enabled FROM oidc_config WHERE id=1").Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, enabled == 1, nil
}

// OIDCHealthy is used by the admin summary; it never returns the secret.
func (s *Store) OIDCHealthy(ctx context.Context, now time.Time) (bool, error) {
	var tested sql.NullString
	var ok int
	err := s.DB.QueryRowContext(ctx, "SELECT tested_at,test_ok FROM oidc_config WHERE id=1").Scan(&tested, &ok)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ok != 1 || !tested.Valid {
		return false, nil
	}
	at, err := time.Parse(time.RFC3339, tested.String)
	return err == nil && now.Sub(at) <= OIDCTestValidity && !now.Before(at), nil
}
