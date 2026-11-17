package lab

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalAuthMigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("second migrate failed", err)
	}
	hash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"
	if err = s.CreateLocalAdministrator(ctx, "ada.admin", "Ada Admin", hash, nil, false, now); err != nil {
		t.Fatal(err)
	}
	// A database that lost only the version row re-applies migration 9 without
	// duplicating tables or losing the account.
	if _, err = s.DB.Exec("DELETE FROM lab_schema_migrations WHERE version=9"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("re-applying migration 9 failed", err)
	}
	if _, err = s.LocalCredential(ctx, "ada.admin"); err != nil {
		t.Fatal("local account lost", err)
	}
	var version int
	if err = s.DB.QueryRow("SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal("schema version", version, err)
	}
}

func TestLocalAuthMigrationUpgradesVersionEight(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate a database created by the previous binary: version 8 and none
	// of the local-auth tables. Existing data must survive the upgrade.
	for _, statement := range []string{
		"DROP TABLE local_login_attempts", "DROP TABLE local_sessions",
		"DROP TABLE local_bootstrap", "DROP TABLE local_accounts",
		"DELETE FROM lab_schema_migrations WHERE version=9",
	} {
		if _, err = s.DB.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	if err = s.Seed(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version, observations int
	if err = s.DB.QueryRow("SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal("schema version", version, err)
	}
	if err = s.DB.QueryRow("SELECT count(*) FROM lab_observations WHERE status='active'").Scan(&observations); err != nil || observations == 0 {
		t.Fatal("existing observations lost", observations, err)
	}
	hash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"
	if err = s.CreateLocalAdministrator(ctx, "ada.admin", "Ada Admin", hash, []string{IdentityConnection}, false, now); err != nil {
		t.Fatal("upgraded database cannot bootstrap the first local administrator", err)
	}
}
