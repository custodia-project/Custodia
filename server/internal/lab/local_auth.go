package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// LocalIssuer separates local accounts from every external issuer (Ozal,
// laboratory). An account of one issuer never authenticates in another mode.
const LocalIssuer = "custodia-local"

// LoginAttemptCapacity bounds the persisted throttling table. When it is full
// the login fails closed instead of evicting live lockouts.
const LoginAttemptCapacity = 65536

var (
	ErrNoSession       = errors.New("no valid local session")
	ErrLocalBootstrap  = errors.New("first local administrator already configured")
	ErrLocalExists     = errors.New("local account already exists")
	ErrLocalConflict   = errors.New("local account changed concurrently")
	ErrAttemptCapacity = errors.New("login attempt capacity reached")
)

var localUsername = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{0,127}$`)

// NormalizeLocalUsername returns the stored subject or false when invalid.
func NormalizeLocalUsername(value string) (string, bool) {
	username := strings.ToLower(strings.TrimSpace(value))
	return username, localUsername.MatchString(username)
}

func validDisplayName(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(value) <= 200 && !strings.ContainsAny(value, "\x00\r\n")
}

// migrateLocalAuth is migration 9: local accounts, server-side sessions and
// persisted login throttling. It is additive; principals, grants and
// administrators are reused. Every statement is idempotent, so re-running the
// migration neither duplicates tables nor loses accounts or sessions.
func migrateLocalAuth(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS local_accounts(principal_id INTEGER PRIMARY KEY REFERENCES lab_principals(id),display_name TEXT NOT NULL,password_hash TEXT NOT NULL,must_change_password INTEGER NOT NULL CHECK(must_change_password IN (0,1)),password_changed_at TEXT NOT NULL,created_at TEXT NOT NULL,last_login_at TEXT);
CREATE TABLE IF NOT EXISTS local_bootstrap(id INTEGER PRIMARY KEY CHECK(id=1),principal_id INTEGER NOT NULL REFERENCES lab_principals(id));
CREATE TABLE IF NOT EXISTS local_sessions(token_hash TEXT PRIMARY KEY,principal_id INTEGER NOT NULL REFERENCES lab_principals(id),created_at INTEGER NOT NULL,last_seen_at INTEGER NOT NULL,expires_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS local_sessions_principal ON local_sessions(principal_id);
CREATE TABLE IF NOT EXISTS local_login_attempts(attempt_key TEXT PRIMARY KEY,failures INTEGER NOT NULL,window_start INTEGER NOT NULL,locked_until INTEGER NOT NULL);
INSERT OR IGNORE INTO lab_schema_migrations VALUES(9);`)
	return err
}

func audit(ctx context.Context, tx *sql.Tx, actor, action string, after map[string]any, now time.Time) error {
	encoded, err := json.Marshal(after)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,?,'{}',?,?)", actor, action, encoded, now.UTC().Format(time.RFC3339))
	return err
}

func insertLocalAccount(ctx context.Context, tx *sql.Tx, username, display, hash string, mustChange bool, connections []string, now time.Time) (int64, error) {
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_principals WHERE issuer=? AND subject=?", LocalIssuer, username).Scan(&exists); err != nil {
		return 0, err
	}
	if exists != 0 {
		return 0, ErrLocalExists
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO lab_principals(issuer,subject,enabled) VALUES(?,?,1)", LocalIssuer, username)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, connection := range connections {
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_grants VALUES(?,?)", id, connection); err != nil {
			return 0, err
		}
	}
	flag := 0
	if mustChange {
		flag = 1
	}
	stamp := now.UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_accounts(principal_id,display_name,password_hash,must_change_password,password_changed_at,created_at) VALUES(?,?,?,?,?,?)", id, strings.TrimSpace(display), hash, flag, stamp, stamp); err != nil {
		return 0, err
	}
	return id, nil
}

// CreateLocalAdministrator is the explicit host bootstrap of local mode. It
// succeeds once; recovery afterwards is ResetLocalPassword.
func (s *Store) CreateLocalAdministrator(ctx context.Context, username, display, hash string, connections []string, now time.Time) error {
	username, ok := NormalizeLocalUsername(username)
	if !ok || !validDisplayName(display) || hash == "" {
		return ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM local_bootstrap").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrLocalBootstrap
	}
	id, err := insertLocalAccount(ctx, tx, username, display, hash, false, connections, now)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_administrators VALUES(?)", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_bootstrap VALUES(1,?)", id); err != nil {
		return err
	}
	if err = audit(ctx, tx, "host", "create_local_administrator", map[string]any{"issuer": LocalIssuer, "username": username, "connections": connections}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetLocalPassword is host recovery: temporary password, every session
// revoked and the username lockout cleared. Enablement is unchanged.
func (s *Store) ResetLocalPassword(ctx context.Context, username, hash string, now time.Time) error {
	username, ok := NormalizeLocalUsername(username)
	if !ok || hash == "" {
		return ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int
	if err = tx.QueryRowContext(ctx, "SELECT p.id FROM lab_principals p JOIN local_accounts a ON a.principal_id=p.id WHERE p.issuer=? AND p.subject=?", LocalIssuer, username).Scan(&id); err != nil {
		return ErrDenied
	}
	if _, err = tx.ExecContext(ctx, "UPDATE local_accounts SET password_hash=?,must_change_password=1,password_changed_at=? WHERE principal_id=?", hash, now.UTC().Format(time.RFC3339), id); err != nil {
		return err
	}
	revoked, err := tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE principal_id=?", id)
	if err != nil {
		return err
	}
	sessions, _ := revoked.RowsAffected()
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_login_attempts WHERE attempt_key=?", "user:"+username); err != nil {
		return err
	}
	if err = audit(ctx, tx, "host", "reset_local_password", map[string]any{"username": username, "revoked_sessions": sessions, "must_change_password": true}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// LocalCredential is what the login handler needs to verify a password.
type LocalCredential struct {
	Principal
	DisplayName  string
	PasswordHash string
	Enabled      bool
	MustChange   bool
}

func (s *Store) LocalCredential(ctx context.Context, username string) (LocalCredential, error) {
	var c LocalCredential
	err := s.DB.QueryRowContext(ctx, "SELECT p.id,p.issuer,p.subject,p.enabled,a.display_name,a.password_hash,a.must_change_password FROM lab_principals p JOIN local_accounts a ON a.principal_id=p.id WHERE p.issuer=? AND p.subject=?", LocalIssuer, username).Scan(&c.ID, &c.Issuer, &c.Subject, &c.Enabled, &c.DisplayName, &c.PasswordHash, &c.MustChange)
	return c, err
}

// AttemptPolicy bounds failures for one throttling key.
type AttemptPolicy struct {
	Limit        int
	Window, Lock time.Duration
}

// LoginLocked reports whether any key is locked. Expired rows are cleaned and
// a full table fails closed.
func (s *Store) LoginLocked(ctx context.Context, keys []string, window time.Duration, now time.Time) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	current := now.Unix()
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_login_attempts WHERE locked_until<=? AND window_start<=?", current, current-int64(window/time.Second)); err != nil {
		return true, err
	}
	for _, key := range keys {
		var until int64
		err = tx.QueryRowContext(ctx, "SELECT locked_until FROM local_login_attempts WHERE attempt_key=?", key).Scan(&until)
		if err == nil && until > current {
			return true, tx.Commit()
		}
		if err != nil && err != sql.ErrNoRows {
			return true, err
		}
	}
	var total int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM local_login_attempts").Scan(&total); err != nil {
		return true, err
	}
	if err = tx.Commit(); err != nil {
		return true, err
	}
	if total >= LoginAttemptCapacity {
		return true, ErrAttemptCapacity
	}
	return false, nil
}

// RecordLoginFailure counts one failure and reports whether the key locked now.
func (s *Store) RecordLoginFailure(ctx context.Context, key string, policy AttemptPolicy, actor string, now time.Time) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	current := now.Unix()
	failures, start := 0, current
	err = tx.QueryRowContext(ctx, "SELECT failures,window_start FROM local_login_attempts WHERE attempt_key=?", key).Scan(&failures, &start)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == sql.ErrNoRows || start+int64(policy.Window/time.Second) <= current {
		failures, start = 0, current
	}
	failures++
	var until int64
	locked := failures >= policy.Limit
	if locked {
		until = current + int64(policy.Lock/time.Second)
		failures, start = 0, current
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_login_attempts VALUES(?,?,?,?) ON CONFLICT(attempt_key) DO UPDATE SET failures=excluded.failures,window_start=excluded.window_start,locked_until=max(locked_until,excluded.locked_until)", key, failures, start, until); err != nil {
		return false, err
	}
	if locked {
		// The key is "user:<name>" or "ip:<address>", never a password.
		if err = audit(ctx, tx, actor, "local_login_locked", map[string]any{"key": key, "locked_until": time.Unix(until, 0).UTC().Format(time.RFC3339)}, now); err != nil {
			return false, err
		}
	}
	return locked, tx.Commit()
}

func (s *Store) ClearLoginFailures(ctx context.Context, key string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM local_login_attempts WHERE attempt_key=?", key)
	return err
}

// StartLocalSession stores only the token digest and audits the sign-in.
func (s *Store) StartLocalSession(ctx context.Context, p Principal, tokenHash string, now, expires time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE expires_at<=?", now.Unix()); err != nil {
		return err
	}
	var enabled int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_principals WHERE id=? AND issuer=? AND enabled=1", p.ID, LocalIssuer).Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return ErrDenied
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_sessions VALUES(?,?,?,?,?)", tokenHash, p.ID, now.Unix(), now.Unix(), expires.Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE local_accounts SET last_login_at=? WHERE principal_id=?", now.UTC().Format(time.RFC3339), p.ID); err != nil {
		return err
	}
	if err = audit(ctx, tx, p.Actor(), "local_login", map[string]any{"username": p.Subject}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// LocalSession is a resolved, enabled and unexpired session.
type LocalSession struct {
	Principal
	DisplayName string
	MustChange  bool
}

// ResolveLocalSession enforces absolute and idle expiry on the server.
func (s *Store) ResolveLocalSession(ctx context.Context, tokenHash string, idle time.Duration, now time.Time) (LocalSession, error) {
	var session LocalSession
	var lastSeen, expires int64
	err := s.DB.QueryRowContext(ctx, "SELECT p.id,p.issuer,p.subject,a.display_name,a.must_change_password,s.last_seen_at,s.expires_at FROM local_sessions s JOIN lab_principals p ON p.id=s.principal_id AND p.issuer=? AND p.enabled=1 JOIN local_accounts a ON a.principal_id=p.id WHERE s.token_hash=?", LocalIssuer, tokenHash).Scan(&session.ID, &session.Issuer, &session.Subject, &session.DisplayName, &session.MustChange, &lastSeen, &expires)
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

func (s *Store) EndLocalSession(ctx context.Context, p Principal, tokenHash string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE token_hash=? AND principal_id=?", tokenHash, p.ID); err != nil {
		return err
	}
	if err = audit(ctx, tx, p.Actor(), "local_logout", map[string]any{"username": p.Subject}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ChangeLocalPassword replaces the hash only if it is still the verified one,
// revokes every session of the account and starts the caller's new session.
func (s *Store) ChangeLocalPassword(ctx context.Context, p Principal, verifiedHash, newHash, newTokenHash string, now, expires time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE local_accounts SET password_hash=?,must_change_password=0,password_changed_at=? WHERE principal_id=? AND password_hash=? AND EXISTS(SELECT 1 FROM lab_principals WHERE id=? AND issuer=? AND enabled=1)", newHash, now.UTC().Format(time.RFC3339), p.ID, verifiedHash, p.ID, LocalIssuer)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrLocalConflict
	}
	revoked, err := tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE principal_id=?", p.ID)
	if err != nil {
		return err
	}
	sessions, _ := revoked.RowsAffected()
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_sessions VALUES(?,?,?,?,?)", newTokenHash, p.ID, now.Unix(), now.Unix(), expires.Unix()); err != nil {
		return err
	}
	if err = audit(ctx, tx, p.Actor(), "change_local_password", map[string]any{"username": p.Subject, "revoked_sessions": sessions}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// LocalUser is the administration view of one local account. It never
// carries a password or its hash.
type LocalUser struct {
	Username           string   `json:"username"`
	DisplayName        string   `json:"display_name"`
	Enabled            bool     `json:"enabled"`
	Administrator      bool     `json:"administrator"`
	MustChangePassword bool     `json:"must_change_password"`
	LastLoginAt        *string  `json:"last_login_at"`
	ActiveSessions     int      `json:"active_sessions"`
	Connections        []string `json:"connections"`
}

func (s *Store) ListLocalUsers(ctx context.Context, actor Principal, now time.Time) ([]LocalUser, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = localAdministratorTx(ctx, tx, actor); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.subject,a.display_name,p.enabled,EXISTS(SELECT 1 FROM gateway_administrators g WHERE g.principal_id=p.id),a.must_change_password,a.last_login_at,(SELECT count(*) FROM local_sessions s WHERE s.principal_id=p.id AND s.expires_at>?)
FROM lab_principals p JOIN local_accounts a ON a.principal_id=p.id WHERE p.issuer=? ORDER BY p.subject`, now.Unix(), LocalIssuer)
	if err != nil {
		return nil, err
	}
	users := []LocalUser{}
	ids := []int{}
	for rows.Next() {
		var u LocalUser
		var id int
		var last sql.NullString
		if err = rows.Scan(&id, &u.Username, &u.DisplayName, &u.Enabled, &u.Administrator, &u.MustChangePassword, &last, &u.ActiveSessions); err != nil {
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

func localAdministratorTx(ctx context.Context, tx *sql.Tx, actor Principal) error {
	if actor.Issuer != LocalIssuer {
		return ErrDenied
	}
	return administratorTx(ctx, tx, actor)
}

// CreateLocalUser adds a reader with a temporary password chosen by the
// administrator. It cannot grant more source access than the administrator holds.
func (s *Store) CreateLocalUser(ctx context.Context, actor Principal, username, display, hash string, connections []string, now time.Time) error {
	username, ok := NormalizeLocalUsername(username)
	if !ok || !validDisplayName(display) || hash == "" {
		return ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = localAdministratorTx(ctx, tx, actor); err != nil {
		return err
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
	if _, err = insertLocalAccount(ctx, tx, username, display, hash, true, connections, now); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor.Actor(), "create_local_user", map[string]any{"username": username, "connections": connections, "must_change_password": true}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// manageableLocalReader returns a local reader whose complete scope the
// administrator holds. Administrators are never remote targets.
func manageableLocalReader(ctx context.Context, tx *sql.Tx, actor Principal, username string) (int, error) {
	username, ok := NormalizeLocalUsername(username)
	if !ok {
		return 0, ErrDenied
	}
	var target int
	if err := tx.QueryRowContext(ctx, "SELECT p.id FROM lab_principals p JOIN local_accounts a ON a.principal_id=p.id WHERE p.issuer=? AND p.subject=?", LocalIssuer, username).Scan(&target); err != nil {
		return 0, ErrDenied
	}
	if target == actor.ID {
		return 0, ErrDenied
	}
	var admins, outside int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_administrators WHERE principal_id=?", target).Scan(&admins); err != nil {
		return 0, err
	}
	if admins != 0 {
		return 0, ErrDenied
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_grants g WHERE g.principal_id=? AND NOT EXISTS(SELECT 1 FROM lab_grants own WHERE own.principal_id=? AND own.connection_id=g.connection_id)", target, actor.ID).Scan(&outside); err != nil {
		return 0, err
	}
	if outside != 0 {
		return 0, ErrDenied
	}
	return target, nil
}

// SetLocalUserEnabled disables (revoking sessions) or re-enables a reader.
func (s *Store) SetLocalUserEnabled(ctx context.Context, actor Principal, username string, enabled bool, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = localAdministratorTx(ctx, tx, actor); err != nil {
		return err
	}
	target, err := manageableLocalReader(ctx, tx, actor, username)
	if err != nil {
		return err
	}
	flag := 0
	action := "disable_local_user"
	if enabled {
		flag = 1
		action = "enable_local_user"
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
	normalized, _ := NormalizeLocalUsername(username)
	if err = audit(ctx, tx, actor.Actor(), action, map[string]any{"username": normalized, "enabled": enabled, "revoked_sessions": sessions}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeLocalSessions ends every session of a manageable reader or of the
// administrator itself.
func (s *Store) RevokeLocalSessions(ctx context.Context, actor Principal, username string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = localAdministratorTx(ctx, tx, actor); err != nil {
		return err
	}
	normalized, ok := NormalizeLocalUsername(username)
	if !ok {
		return ErrDenied
	}
	target := actor.ID
	if normalized != actor.Subject {
		if target, err = manageableLocalReader(ctx, tx, actor, normalized); err != nil {
			return err
		}
	}
	revoked, err := tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE principal_id=?", target)
	if err != nil {
		return err
	}
	sessions, _ := revoked.RowsAffected()
	if err = audit(ctx, tx, actor.Actor(), "revoke_local_sessions", map[string]any{"username": normalized, "revoked_sessions": sessions}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// LocalAccountsExist lets serve warn when local mode has no account yet.
func (s *Store) LocalAccountsExist(ctx context.Context) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM local_accounts").Scan(&count)
	if err != nil {
		return false, fmt.Errorf("local accounts: %w", err)
	}
	return count > 0, nil
}
