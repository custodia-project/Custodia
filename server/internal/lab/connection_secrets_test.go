package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"custodia/server/internal/connectors"
	"custodia/server/internal/secrets"
)

func secretStore(t *testing.T) (*Store, Principal, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateLocalAdministrator(ctx, "ada.admin", "Ada Admin", "hash-placeholder", nil, false, now); err != nil {
		t.Fatal(err)
	}
	admin, err := s.ResolvePrincipal(ctx, LocalIssuer, "ada.admin")
	if err != nil {
		t.Fatal(err)
	}
	key, err := secrets.Generate(secrets.PathFor(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	s.SetMasterKey(key)
	return s, admin, now
}

func registerSecretConnection(t *testing.T, s *Store, admin Principal, now time.Time, secret string) ConnectionRegistration {
	t.Helper()
	registration, err := s.RegisterConnection(context.Background(), admin.Actor(), admin, SyncSource{
		Connector: connectors.CyberArkPAM, BaseURL: "https://pam.acme.example",
		AuthMode: "bearer", Secret: secret, Collection: "safes", Scope: "prod",
	}, []string{"safes"}, now)
	if err != nil {
		t.Fatal(err)
	}
	return registration
}

func TestConnectionSecretIsEncryptedAndNeverListed(t *testing.T) {
	ctx := context.Background()
	s, admin, now := secretStore(t)
	registration := registerSecretConnection(t, s, admin, now, "a bearer secret value")
	group := registration.GroupID
	plaintext, err := s.ResolveReference(ctx, group, "vault:"+group+":auth")
	if err != nil || plaintext != "a bearer secret value" {
		t.Fatal(plaintext, err)
	}
	var nonce, ciphertext []byte
	if err = s.DB.QueryRow("SELECT nonce,ciphertext FROM lab_connection_secrets WHERE group_id=?", group).Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("a bearer secret value")) || len(nonce) == 0 {
		t.Fatal("secret was stored in clear text")
	}
	groups, err := s.ListConnectionGroups(ctx, admin)
	if err != nil || len(groups) != 1 || !groups[0].SecretConfigured || groups[0].SecretVersion != 1 {
		t.Fatal(groups, err)
	}
	// The administrative view and audit never carry the value or the reference.
	for _, group := range groups {
		if strings.Contains(group.GroupID, "bearer secret") || strings.Contains(group.BaseURL, "bearer secret") {
			t.Fatal("secret leaked into the group view")
		}
	}
	var audited string
	if err = s.DB.QueryRow("SELECT group_concat(after_json) FROM lab_audit WHERE action='connection_secret_set'").Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audited, "a bearer secret value") || strings.Contains(audited, "vault:") {
		t.Fatal("audit exposed the secret or its internal reference", audited)
	}
	if version, err := s.PutConnectionSecret(ctx, admin, group, "rotated bearer secret", now.Add(time.Minute)); err != nil || version != 2 {
		t.Fatal(version, err)
	}
	if value, err := s.ResolveReference(ctx, group, "vault:"+group+":auth"); err != nil || value != "rotated bearer secret" {
		t.Fatal(value, err)
	}
}

func TestConnectionSecretRejectsMissingKeyAndBadValue(t *testing.T) {
	s, admin, now := secretStore(t)
	registration := registerSecretConnection(t, s, admin, now, "initial secret")
	s.SetMasterKey(nil)
	if _, err := s.PutConnectionSecret(context.Background(), admin, registration.GroupID, "another secret", now); !errors.Is(err, ErrNoMasterKey) {
		t.Fatal(err)
	}
	if _, err := s.ResolveReference(context.Background(), registration.GroupID, "vault:"+registration.GroupID+":auth"); !errors.Is(err, ErrNoMasterKey) {
		t.Fatal(err)
	}
	key, _ := secrets.Generate(secrets.PathFor(t.TempDir()))
	s.SetMasterKey(key)
	if _, err := s.PutConnectionSecret(context.Background(), admin, registration.GroupID, "line\nbreak", now); !errors.Is(err, ErrDenied) {
		t.Fatal("control character accepted", err)
	}
	if _, err := s.PutConnectionSecret(context.Background(), admin, "missing", "value", now); !errors.Is(err, ErrSyncNotFound) {
		t.Fatal(err)
	}
	if _, err := s.ResolveReference(context.Background(), "missing", "vault:missing:auth"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal(err)
	}
}

func TestConnectionManagementAndScheduleGate(t *testing.T) {
	ctx := context.Background()
	s, admin, now := secretStore(t)
	registration := registerSecretConnection(t, s, admin, now, "initial secret")
	group := registration.GroupID
	child := registration.Connections[0].ConnectionID
	if err := s.UpdateConnectionGroup(ctx, admin, group, "https://pam.acme.example", "bearer", registration.Connections[0].CredentialRef, "", "", 30, 100, 100, 10000, now); !errors.Is(err, ErrProbeRequired) {
		t.Fatal("schedule accepted without a successful probe", err)
	}
	if err := s.RecordConnectionProbe(ctx, group, true, "ok", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateConnectionGroup(ctx, admin, group, "https://pam.acme.example", "bearer", registration.Connections[0].CredentialRef, "", "", 30, 100, 100, 10000, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSyncSchedule(ctx, admin, child, 15, now); err != nil {
		t.Fatal(err)
	}
	// Changing the secret invalidates the probe and the schedule.
	if _, err := s.PutConnectionSecret(ctx, admin, group, "rotated secret", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.ConnectionProbe(ctx, group); err != nil || found {
		t.Fatal("secret rotation kept the old probe", found, err)
	}
	if err := s.SetSyncSchedule(ctx, admin, child, 15, now.Add(2*time.Minute)); !errors.Is(err, ErrProbeRequired) {
		t.Fatal("schedule survived rotation", err)
	}
	if err := s.SetConnectionEnabled(ctx, admin, group, false, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConnectionEnabled(ctx, admin, group, true, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireConnection(ctx, admin, group, now); err != nil {
		t.Fatal(err)
	}
	groups, err := s.ListConnectionGroups(ctx, admin)
	if err != nil || len(groups) != 1 || groups[0].RetiredAt == "" || groups[0].Enabled {
		t.Fatal(groups, err)
	}
	if err := s.SetConnectionEnabled(ctx, admin, group, true, now); !errors.Is(err, ErrSyncNotFound) {
		t.Fatal("retired connection resumed", err)
	}
	if listed, err := s.Connections(ctx, admin); err != nil || len(listed) != 0 {
		t.Fatal("retired connection still listed", listed, err)
	}
}

func TestConnectionSecretHTTPContract(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	key, err := secrets.Generate(secrets.PathFor(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	s.SetMasterKey(key)
	if _, err = s.DB.Exec("INSERT INTO gateway_administrators(principal_id) VALUES(?)", p.ID); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("lab-key", func() time.Time { return now })

	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections", `{
		"connector":"cyberark-pam-safes-v1","base_url":"https://pam.acme.example",
		"collections":["safes"],"scope":"prod","auth_mode":"bearer","secret":"http secret value"}`)
	if code != 201 || !strings.Contains(body, `"secret_configured":true`) || strings.Contains(body, "http secret value") {
		t.Fatal(code, body)
	}
	var created struct {
		GroupID string `json:"group_id"`
	}
	if err = json.Unmarshal([]byte(body), &created); err != nil || created.GroupID == "" {
		t.Fatal(err, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections", "")
	if code != 200 || !strings.Contains(body, `"secret_configured":true`) || strings.Contains(body, "http secret value") || strings.Contains(body, "vault:") {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections/"+created.GroupID+"/secret", `{"secret":"rotated http secret"}`)
	if code != 200 || !strings.Contains(body, `"version":2`) || strings.Contains(body, "rotated http secret") {
		t.Fatal(code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "PUT", "/api/ingestion/connections/"+created.GroupID, `{
		"base_url":"https://pam.acme.example","auth_mode":"bearer","credential_ref":"vault:`+created.GroupID+`:auth",
		"ca_file":"","interval_minutes":15,"page_size":100,"max_pages":100,"max_records":10000}`)
	if code != 409 || !strings.Contains(body, "Probá la conexión") {
		t.Fatal("schedule without probe", code, body)
	}
	if err = s.RecordConnectionProbe(ctx, created.GroupID, true, "ok", now); err != nil {
		t.Fatal(err)
	}
	code, body = syncRequest(t, h, "lab-key", "PUT", "/api/ingestion/connections/"+created.GroupID, `{
		"base_url":"https://pam.acme.example","auth_mode":"bearer","credential_ref":"vault:`+created.GroupID+`:auth",
		"ca_file":"","interval_minutes":15,"page_size":100,"max_pages":100,"max_records":10000}`)
	if code != 200 {
		t.Fatal(code, body)
	}
	for _, action := range []string{"pause", "resume", "retire"} {
		code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections/"+created.GroupID+"/"+action, "{}")
		if code != 200 {
			t.Fatal(action, code, body)
		}
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections", "")
	if code != 200 || !strings.Contains(body, `"retired_at":"2026-11-16`) {
		t.Fatal(code, body)
	}
}

// A connection never sends another connection's stored secret: clients cannot
// name a foreign vault: reference, and the resolver refuses one that reached
// the database anyway.
func TestConnectionSecretCannotBeBorrowedByAnotherConnection(t *testing.T) {
	ctx := context.Background()
	s, admin, now := secretStore(t)
	victim := registerSecretConnection(t, s, admin, now, "victim secret")
	attacker := registerSecretConnection(t, s, admin, now, "attacker secret")
	foreign := "vault:" + victim.GroupID + ":auth"

	if _, err := s.RegisterConnection(ctx, admin.Actor(), admin, SyncSource{
		Connector: connectors.CyberArkPAM, BaseURL: "https://collector.globex.example",
		AuthMode: "bearer", CredentialRef: foreign, Collection: "safes", Scope: "prod",
	}, []string{"safes"}, now); !errors.Is(err, ErrForeignSecret) {
		t.Fatal("registration accepted a foreign vault reference", err)
	}
	if err := s.UpdateConnectionGroup(ctx, admin, attacker.GroupID, "", "", foreign, "", "", 0, 0, 0, 0, now); !errors.Is(err, ErrForeignSecret) {
		t.Fatal("update accepted a foreign vault reference", err)
	}
	// Defense in depth: even a foreign reference written directly to the
	// database never resolves for the wrong owner.
	if _, err := s.ResolveReference(ctx, attacker.GroupID, foreign); !errors.Is(err, ErrForeignSecret) {
		t.Fatal("resolver returned another connection's secret", err)
	}
	if _, err := s.ResolveReference(ctx, "", foreign); !errors.Is(err, ErrForeignSecret) {
		t.Fatal("resolver returned a secret without an owner", err)
	}
}

// Moving a connection to another host requires the secret again, and a
// rejected edit never leaves a rotated secret behind.
func TestConnectionSecretOriginChangeAndAtomicRotation(t *testing.T) {
	ctx := context.Background()
	s, admin, now := secretStore(t)
	registration := registerSecretConnection(t, s, admin, now, "original secret")
	group := registration.GroupID

	if err := s.UpdateConnectionGroup(ctx, admin, group, "https://other.globex.example", "", "", "", "", 0, 0, 0, 0, now); !errors.Is(err, ErrSecretReentry) {
		t.Fatal("stored secret redirected to a new origin", err)
	}
	// Same origin with another path keeps the stored secret.
	if err := s.UpdateConnectionGroup(ctx, admin, group, "https://pam.acme.example/PasswordVault", "", "", "", "", 0, 0, 0, 0, now); err != nil {
		t.Fatal(err)
	}
	// A new origin with the secret again is accepted and rotates it.
	if err := s.UpdateConnectionGroup(ctx, admin, group, "https://pam2.acme.example", "", "", "", "new host secret", 0, 0, 0, 0, now); err != nil {
		t.Fatal(err)
	}
	if value, err := s.ResolveReference(ctx, group, "vault:"+group+":auth"); err != nil || value != "new host secret" {
		t.Fatal(value, err)
	}
	// An invalid edit carrying a secret rolls back the rotation too.
	if err := s.UpdateConnectionGroup(ctx, admin, group, "http://not-https.acme.example", "", "", "", "must not persist", 0, 0, 0, 0, now); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if value, err := s.ResolveReference(ctx, group, "vault:"+group+":auth"); err != nil || value != "new host secret" {
		t.Fatal("rejected edit rotated the secret", value, err)
	}
	if _, version, err := s.SecretConfigured(ctx, group); err != nil || version != 2 {
		t.Fatal("secret version changed on a rejected edit", version, err)
	}
	// The ciphertext is bound to its version: an older row cannot be replayed.
	var nonce, ciphertext []byte
	if err := s.DB.QueryRow("SELECT nonce,ciphertext FROM lab_connection_secrets WHERE group_id=?", group).Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE lab_connection_secrets SET version=1 WHERE group_id=?", group); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveReference(ctx, group, "vault:"+group+":auth"); err == nil {
		t.Fatal("ciphertext decrypted under another version")
	}
}

// Whitespace around a vault: reference must not let it skip the origin check.
func TestConnectionSecretReferenceIsCanonicalized(t *testing.T) {
	ctx := context.Background()
	s, admin, now := secretStore(t)
	registration := registerSecretConnection(t, s, admin, now, "original secret")
	group := registration.GroupID
	padded := "  vault:" + group + ":auth \t"
	if err := s.UpdateConnectionGroup(ctx, admin, group, "https://other.globex.example", "", padded, "", "", 0, 0, 0, 0, now); !errors.Is(err, ErrSecretReentry) {
		t.Fatal("padded vault reference skipped the origin check", err)
	}
	if err := s.UpdateConnectionGroup(ctx, admin, group, "", "", " vault:other-group:auth", "", "", 0, 0, 0, 0, now); !errors.Is(err, ErrForeignSecret) {
		t.Fatal("padded foreign vault reference accepted", err)
	}
	if err := s.UpdateConnectionGroup(ctx, admin, group, "", "", padded, "", "", 0, 0, 0, 0, now); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.DB.QueryRow("SELECT credential_ref FROM lab_sync_sources WHERE group_id=?", group).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "vault:"+group+":auth" {
		t.Fatalf("stored reference was not canonical: %q", stored)
	}
}
