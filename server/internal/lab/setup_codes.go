package lab

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrSetupCode is the single answer for an unknown, expired or used setup
// code, so a guesser learns nothing about which one it was.
var ErrSetupCode = errors.New("setup code is not valid")

// migrateSetupCodes is migration 13: one-use codes that let the first local
// administrator be created from the browser. Only the SHA-256 of a code is
// stored. Additive and idempotent.
func migrateSetupCodes(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS local_setup_codes(
 code_hash TEXT PRIMARY KEY,
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 used_at INTEGER
);
INSERT OR IGNORE INTO lab_schema_migrations VALUES(13);`)
	return err
}

func bootstrapDoneTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM local_bootstrap").Scan(&count); err != nil {
		return false, err
	}
	return count != 0, nil
}

// IssueSetupCode stores a new code (by its hash) and invalidates every
// earlier unused one, so at most one code is ever valid. It is refused once
// the first administrator exists. The audit never carries the code.
func (s *Store) IssueSetupCode(ctx context.Context, codeHash string, now, expires time.Time) error {
	if strings.TrimSpace(codeHash) == "" || !expires.After(now) {
		return ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	done, err := bootstrapDoneTx(ctx, tx)
	if err != nil {
		return err
	}
	if done {
		return ErrLocalBootstrap
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_setup_codes WHERE used_at IS NULL"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_setup_codes(code_hash,created_at,expires_at) VALUES(?,?,?)", codeHash, now.Unix(), expires.Unix()); err != nil {
		return err
	}
	// The host is the owner: a new code clears the setup guess counters, so
	// someone guessing from outside cannot keep the owner from setting up.
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_login_attempts WHERE attempt_key='setup:all' OR attempt_key LIKE 'setup-ip:%'"); err != nil {
		return err
	}
	if err = audit(ctx, tx, "host", "setup_code_issued", map[string]any{"expires_at": expires.UTC().Format(time.RFC3339)}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// SetupAvailable reports whether the browser setup can be offered: no first
// administrator yet and an unused, unexpired code.
func (s *Store) SetupAvailable(ctx context.Context, now time.Time) (bool, error) {
	var available int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM local_bootstrap)=0
 AND EXISTS(SELECT 1 FROM local_setup_codes WHERE used_at IS NULL AND expires_at>?)`, now.Unix()).Scan(&available)
	return available == 1, err
}

// ClaimSetupCode creates the first local administrator with a password the
// person chose, in one transaction with the code check: no first
// administrator yet, the code unused and unexpired, then the account, its
// administrator role, the bootstrap marker, the code marked used and the
// audit. A wrong code changes nothing.
func (s *Store) ClaimSetupCode(ctx context.Context, codeHash, username, display, passwordHash string, now time.Time) (Principal, error) {
	username, ok := NormalizeLocalUsername(username)
	if !ok || !validDisplayName(display) || passwordHash == "" || codeHash == "" {
		return Principal{}, ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Principal{}, err
	}
	defer tx.Rollback()
	done, err := bootstrapDoneTx(ctx, tx)
	if err != nil {
		return Principal{}, err
	}
	if done {
		return Principal{}, ErrSetupCode
	}
	result, err := tx.ExecContext(ctx, "UPDATE local_setup_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?", now.Unix(), codeHash, now.Unix())
	if err != nil {
		return Principal{}, err
	}
	if claimed, _ := result.RowsAffected(); claimed != 1 {
		return Principal{}, ErrSetupCode
	}
	id, err := insertLocalAccount(ctx, tx, username, display, passwordHash, false, nil, now)
	if err != nil {
		return Principal{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_administrators VALUES(?)", id); err != nil {
		return Principal{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO local_bootstrap VALUES(1,?)", id); err != nil {
		return Principal{}, err
	}
	// Any other pending code is useless now; drop it.
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_setup_codes WHERE used_at IS NULL"); err != nil {
		return Principal{}, err
	}
	if err = audit(ctx, tx, "browser", "setup_admin_created", map[string]any{"issuer": LocalIssuer, "username": username}, now); err != nil {
		return Principal{}, err
	}
	if err = tx.Commit(); err != nil {
		return Principal{}, err
	}
	return Principal{ID: int(id), Issuer: LocalIssuer, Subject: username}, nil
}

// SetupCodeMatches is a cheap pre-check before the expensive password hash:
// whether the hash names an unused, unexpired code while no administrator
// exists. ClaimSetupCode re-checks everything in its transaction.
func (s *Store) SetupCodeMatches(ctx context.Context, codeHash string, now time.Time) (bool, error) {
	var matches int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM local_bootstrap)=0
 AND EXISTS(SELECT 1 FROM local_setup_codes WHERE code_hash=? AND used_at IS NULL AND expires_at>?)`, codeHash, now.Unix()).Scan(&matches)
	return matches == 1, err
}
