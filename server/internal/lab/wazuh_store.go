package lab

import (
	"context"
	"database/sql"
)

// migrateWazuhAuth is migration 12: the non-secret API user for Wazuh's
// basic-exchange login. The password stays encrypted and is referenced as
// vault:<group>:auth.
func migrateWazuhAuth(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version >= 12 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE lab_sync_sources ADD COLUMN auth_user TEXT NOT NULL DEFAULT '';
INSERT INTO lab_schema_migrations VALUES(12);`)
	return err
}
