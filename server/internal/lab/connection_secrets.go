package lab

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"custodia/server/internal/probe"
	"custodia/server/internal/secrets"
)

var (
	ErrNoMasterKey      = errors.New("master key is not loaded")
	ErrSecretNotFound   = errors.New("connection secret not found")
	ErrProbeRequired    = errors.New("a successful probe is required")
	ErrConnectionClosed = errors.New("connection is retired")
	// ErrForeignSecret rejects a vault: reference that names another
	// connection: a stored secret is only ever sent by its own connection.
	ErrForeignSecret = errors.New("vault reference belongs to another connection")
	// ErrSecretReentry requires the secret again when the tenant origin
	// changes, so a stored secret is never redirected to a new host.
	ErrSecretReentry = errors.New("changing the tenant origin requires the secret again")
)

// migrateConnectionSecrets is migration 11: encrypted connection secrets,
// probe evidence used to gate the schedule, and retirement. Additive and
// idempotent.
func migrateConnectionSecrets(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version >= 11 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE lab_sync_sources ADD COLUMN retired_at TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS lab_connection_secrets(
 group_id TEXT PRIMARY KEY,
 version INTEGER NOT NULL,
 nonce BLOB NOT NULL,
 ciphertext BLOB NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 updated_by TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS lab_connection_probes(
 group_id TEXT PRIMARY KEY,
 at TEXT NOT NULL,
 ok INTEGER NOT NULL CHECK(ok IN (0,1)),
 detail TEXT NOT NULL DEFAULT ''
);
INSERT INTO lab_schema_migrations VALUES(11);`)
	return err
}

// SetMasterKey enables encrypted connection secrets for this store. The key
// is never persisted by the store.
func (s *Store) SetMasterKey(key *secrets.Key) { s.masterKey = key }

// secretAAD binds a ciphertext to its connection, field and secret version,
// so a row cannot be moved to another connection or rolled back to an older
// version of the same one.
func secretAAD(groupID string, version int) string {
	return "v1|" + groupID + "|auth|" + strconv.Itoa(version)
}

// vaultReference is the only vault: reference a connection may carry.
func vaultReference(groupID string) string {
	return "vault:" + groupID + ":auth"
}

// checkOwnVaultReference accepts non-vault references and only the vault:
// reference of the same connection. Clients never choose a vault target.
func checkOwnVaultReference(groupID string, references ...string) error {
	for _, reference := range references {
		reference = strings.TrimSpace(reference)
		if strings.HasPrefix(reference, "vault:") && (groupID == "" || reference != vaultReference(groupID)) {
			return ErrForeignSecret
		}
	}
	return nil
}

// sameOrigin compares scheme and host (with port) of two base URLs.
func sameOrigin(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (s *Store) PutConnectionSecret(ctx context.Context, actor Principal, groupID, value string, now time.Time) (int, error) {
	if s.masterKey == nil {
		return 0, ErrNoMasterKey
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return 0, err
	}
	version, err := putConnectionSecretTx(ctx, tx, s.masterKey, actor, groupID, value, now)
	if err != nil {
		return 0, err
	}
	return version, tx.Commit()
}

func putConnectionSecretTx(ctx context.Context, tx *sql.Tx, key *secrets.Key, actor Principal, groupID, value string, now time.Time) (int, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" || !secrets.ValidSecretValue(value) || key == nil {
		return 0, ErrDenied
	}
	var rows int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_sync_sources WHERE group_id=?", groupID).Scan(&rows); err != nil {
		return 0, err
	}
	if rows == 0 {
		return 0, ErrSyncNotFound
	}
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(version),0)+1 FROM lab_connection_secrets WHERE group_id=?", groupID).Scan(&version); err != nil {
		return 0, err
	}
	nonce, ciphertext, err := key.Encrypt([]byte(value), secretAAD(groupID, version))
	if err != nil {
		return 0, err
	}
	stamp := now.UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, `INSERT INTO lab_connection_secrets(group_id,version,nonce,ciphertext,created_at,updated_at,updated_by)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(group_id) DO UPDATE SET version=excluded.version,nonce=excluded.nonce,ciphertext=excluded.ciphertext,updated_at=excluded.updated_at,updated_by=excluded.updated_by`,
		groupID, version, nonce, ciphertext, stamp, stamp, actor.Actor()); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE lab_sync_sources SET credential_ref=? WHERE group_id=?", vaultReference(groupID), groupID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM lab_connection_probes WHERE group_id=?", groupID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE lab_sync_sources SET interval_minutes=0 WHERE group_id=?", groupID); err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, actor.Actor(), "connection_secret_set", map[string]any{"group_id": groupID, "version": version, "secret_configured": true}, now); err != nil {
		return 0, err
	}
	return version, nil
}

func (s *Store) connectionSecret(ctx context.Context, groupID string) (string, error) {
	if s.masterKey == nil {
		return "", ErrNoMasterKey
	}
	var nonce, ciphertext []byte
	var version int
	err := s.DB.QueryRowContext(ctx, "SELECT version,nonce,ciphertext FROM lab_connection_secrets WHERE group_id=?", groupID).Scan(&version, &nonce, &ciphertext)
	if err == sql.ErrNoRows {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", err
	}
	plaintext, err := s.masterKey.Decrypt(nonce, ciphertext, secretAAD(groupID, version))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// ResolveReference resolves a credential reference for the probe or worker
// of the connection group owner. vault: only resolves the owner's own secret,
// even if a foreign reference reached the database; env:, file: and secret:
// keep their existing behaviour.
func (s *Store) ResolveReference(ctx context.Context, owner, reference string) (string, error) {
	if !strings.HasPrefix(reference, "vault:") {
		return probe.ResolveCredential(reference)
	}
	parts := strings.Split(reference, ":")
	if len(parts) != 3 || parts[1] == "" || parts[2] != "auth" {
		return "", errors.New("invalid vault reference")
	}
	if err := checkOwnVaultReference(owner, reference); err != nil {
		return "", err
	}
	return s.connectionSecret(ctx, parts[1])
}

func (s *Store) SecretConfigured(ctx context.Context, groupID string) (bool, int, error) {
	var version int
	err := s.DB.QueryRowContext(ctx, "SELECT version FROM lab_connection_secrets WHERE group_id=?", groupID).Scan(&version)
	if err == sql.ErrNoRows {
		return false, 0, nil
	}
	return err == nil, version, err
}

type ConnectionProbe struct {
	At     string `json:"at"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (s *Store) RecordConnectionProbe(ctx context.Context, groupID string, ok bool, detail string, now time.Time) error {
	if len(detail) > 500 {
		detail = detail[:500]
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO lab_connection_probes(group_id,at,ok,detail) VALUES(?,?,?,?)
ON CONFLICT(group_id) DO UPDATE SET at=excluded.at,ok=excluded.ok,detail=excluded.detail`,
		groupID, now.UTC().Format(time.RFC3339), boolInt(ok), detail)
	return err
}

func (s *Store) ConnectionProbe(ctx context.Context, groupID string) (ConnectionProbe, bool, error) {
	var p ConnectionProbe
	err := s.DB.QueryRowContext(ctx, "SELECT at,ok,detail FROM lab_connection_probes WHERE group_id=?", groupID).Scan(&p.At, &p.OK, &p.Detail)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	return p, err == nil, err
}

// ConnectionGroup is the operator view of one tenant connection. It never
// contains the secret, its ciphertext or the internal reference.
type ConnectionGroup struct {
	GroupID              string           `json:"group_id"`
	Connector            string           `json:"connector"`
	BaseURL              string           `json:"base_url"`
	Collections          []string         `json:"collections"`
	AuthMode             string           `json:"auth_mode"`
	Enabled              bool             `json:"enabled"`
	RetiredAt            string           `json:"retired_at"`
	IntervalMinutes      int              `json:"interval_minutes"`
	CredentialConfigured bool             `json:"credential_configured"`
	SecretConfigured     bool             `json:"secret_configured"`
	SecretVersion        int              `json:"secret_version"`
	LastProbe            *ConnectionProbe `json:"last_probe"`
}

func (s *Store) ListConnectionGroups(ctx context.Context, actor Principal) ([]ConnectionGroup, error) {
	if !s.IsAdministrator(ctx, actor) {
		return nil, ErrDenied
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT group_id,min(connector),min(base_url),max(enabled),max(retired_at),max(interval_minutes),min(auth_mode),min(case when credential_ref<>'' then 1 else 0 end)
FROM lab_sync_sources GROUP BY group_id ORDER BY group_id`)
	if err != nil {
		return nil, err
	}
	groups := []ConnectionGroup{}
	for rows.Next() {
		var g ConnectionGroup
		if err = rows.Scan(&g.GroupID, &g.Connector, &g.BaseURL, &g.Enabled, &g.RetiredAt, &g.IntervalMinutes, &g.AuthMode, &g.CredentialConfigured); err != nil {
			rows.Close()
			return nil, err
		}
		g.Collections = []string{}
		groups = append(groups, g)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range groups {
		collections, err := s.DB.QueryContext(ctx, "SELECT collection FROM lab_sync_sources WHERE group_id=? ORDER BY collection", groups[i].GroupID)
		if err != nil {
			return nil, err
		}
		for collections.Next() {
			var collection string
			if err = collections.Scan(&collection); err != nil {
				collections.Close()
				return nil, err
			}
			groups[i].Collections = append(groups[i].Collections, collection)
		}
		collections.Close()
		groups[i].SecretConfigured, groups[i].SecretVersion, _ = s.SecretConfigured(ctx, groups[i].GroupID)
		if probe, found, err := s.ConnectionProbe(ctx, groups[i].GroupID); err != nil {
			return nil, err
		} else if found {
			groups[i].LastProbe = &probe
		}
	}
	return groups, nil
}

func (s *Store) SetConnectionEnabled(ctx context.Context, actor Principal, groupID string, enabled bool, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM lab_sync_sources WHERE group_id=? AND retired_at=''", groupID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return ErrSyncNotFound
	}
	flag := 0
	action := "connection_pause"
	if enabled {
		flag = 1
		action = "connection_resume"
	}
	if _, err = tx.ExecContext(ctx, "UPDATE lab_sync_sources SET enabled=? WHERE group_id=?", flag, groupID); err != nil {
		return err
	}
	if !enabled {
		if _, err = tx.ExecContext(ctx, "UPDATE lab_sync_sources SET interval_minutes=0 WHERE group_id=?", groupID); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, actor.Actor(), action, map[string]any{"group_id": groupID, "enabled": enabled}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RetireConnection(ctx context.Context, actor Principal, groupID string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE lab_sync_sources SET enabled=0,interval_minutes=0,retired_at=? WHERE group_id=? AND retired_at=''", now.UTC().Format(time.RFC3339), groupID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrSyncNotFound
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM lab_connection_probes WHERE group_id=?", groupID); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor.Actor(), "connection_retire", map[string]any{"group_id": groupID, "retired": true}, now); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateConnectionGroup edits a connection group and, when secret is not
// empty, rotates its encrypted secret in the same transaction: a rejected edit
// never leaves a rotated credential behind. Any change to the URL, auth, CA,
// references or secret invalidates the probe and turns the schedule off.
func (s *Store) UpdateConnectionGroup(ctx context.Context, actor Principal, groupID, baseURL, authMode, credentialRef, caFile, secret string, interval, pageSize, maxPages, maxRecords int, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = administratorTx(ctx, tx, actor); err != nil {
		return err
	}
	var currentBase, currentAuth, currentCred, currentCA string
	if err = tx.QueryRowContext(ctx, "SELECT min(base_url),min(auth_mode),min(credential_ref),min(ca_file) FROM lab_sync_sources WHERE group_id=? AND retired_at=''", groupID).Scan(&currentBase, &currentAuth, &currentCred, &currentCA); err != nil {
		return err
	}
	if currentBase == "" {
		return ErrSyncNotFound
	}
	if secret != "" && credentialRef != "" {
		return errors.New("send either a secret or a credential reference, not both")
	}
	// A client may keep its own vault: reference but never name another one.
	if err = checkOwnVaultReference(groupID, credentialRef); err != nil {
		return err
	}
	// Empty values keep the current setting.
	if baseURL == "" {
		baseURL = currentBase
	}
	if authMode == "" {
		authMode = currentAuth
	}
	if credentialRef == "" {
		credentialRef = currentCred
	}
	if caFile == "" {
		caFile = currentCA
	}
	if secret != "" {
		if normalizedAuthMode(authMode) != "bearer" && normalizedAuthMode(authMode) != "basic-exchange" {
			return errors.New("encrypted secrets are supported for bearer and basic-exchange authentication")
		}
		if s.masterKey == nil {
			return ErrNoMasterKey
		}
		if _, err = putConnectionSecretTx(ctx, tx, s.masterKey, actor, groupID, secret, now); err != nil {
			return err
		}
		credentialRef = vaultReference(groupID)
	} else if credentialRef == vaultReference(groupID) && !sameOrigin(currentBase, baseURL) {
		// The stored secret was entered for the current host. Sending it to a
		// new origin needs the operator to provide it again.
		return ErrSecretReentry
	}
	changed := secret != "" || currentBase != baseURL || normalizedAuthMode(currentAuth) != normalizedAuthMode(authMode) || currentCred != credentialRef || currentCA != caFile
	if changed {
		interval = 0
	}
	if interval > 0 {
		var ok int
		if err = tx.QueryRowContext(ctx, "SELECT ok FROM lab_connection_probes WHERE group_id=?", groupID).Scan(&ok); err != nil || ok != 1 {
			return ErrProbeRequired
		}
	}
	var currentConnectionID, connector, collection, scope, generation, currentAuthUser string
	var currentPage, currentMaxPages, currentMaxRecords int
	if err = tx.QueryRowContext(ctx, "SELECT connection_id,connector,collection,scope,generation,coalesce(auth_user,''),page_size,max_pages,max_records FROM lab_sync_sources WHERE group_id=? AND retired_at='' ORDER BY connection_id LIMIT 1", groupID).Scan(&currentConnectionID, &connector, &collection, &scope, &generation, &currentAuthUser, &currentPage, &currentMaxPages, &currentMaxRecords); err != nil {
		return ErrSyncNotFound
	}
	if pageSize == 0 {
		pageSize = currentPage
	}
	if maxPages == 0 {
		maxPages = currentMaxPages
	}
	if maxRecords == 0 {
		maxRecords = currentMaxRecords
	}
	candidate := SyncSource{
		ConnectionID: currentConnectionID, GroupID: groupID, Connector: connector,
		BaseURL: baseURL, Collection: collection, Scope: scope, Generation: generation,
		AuthMode: authMode, AuthUser: currentAuthUser, CredentialRef: credentialRef, CAFile: caFile,
		PageSize: pageSize, MaxPages: maxPages, MaxRecords: maxRecords,
	}
	if err = validateConnectionConfiguration(candidate, nil); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE lab_sync_sources SET base_url=?,auth_mode=?,credential_ref=?,ca_file=?,interval_minutes=?,page_size=?,max_pages=?,max_records=? WHERE group_id=?`,
		baseURL, normalizedAuthMode(authMode), credentialRef, caFile, interval, pageSize, maxPages, maxRecords, groupID); err != nil {
		return err
	}
	if changed {
		if _, err = tx.ExecContext(ctx, "DELETE FROM lab_connection_probes WHERE group_id=?", groupID); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, actor.Actor(), "connection_update", map[string]any{"group_id": groupID, "base_url": baseURL, "auth_mode": normalizedAuthMode(authMode), "secret_configured": strings.HasPrefix(credentialRef, "vault:"), "ca_configured": caFile != "", "interval_minutes": interval}, now); err != nil {
		return err
	}
	return tx.Commit()
}
