package sqlitebackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"custodia/server/internal/lab"
)

func seededStore(t *testing.T, path string) *lab.Store {
	t.Helper()
	store, err := lab.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(context.Background()); err != nil {
		store.DB.Close()
		t.Fatal(err)
	}
	if err = store.Seed(context.Background(), time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		store.DB.Close()
		t.Fatal(err)
	}
	return store
}

func TestBackupRestoreSeededDisposableStore(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	store := seededStore(t, source)
	var sourceRuns, sourceObservations int
	if err := store.DB.QueryRow("SELECT count(*) FROM lab_runs").Scan(&sourceRuns); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow("SELECT count(*) FROM lab_observations").Scan(&sourceObservations); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(directory, "backup.db")
	if err := Backup(context.Background(), source, backup); err != nil {
		store.DB.Close()
		t.Fatal(err)
	}
	// The source remains open and usable after the online backup.
	if _, err := store.DB.Exec("INSERT INTO lab_preferences(actor,document) VALUES('backup-check','{}') ON CONFLICT(actor) DO UPDATE SET document=excluded.document"); err != nil {
		store.DB.Close()
		t.Fatal(err)
	}
	store.DB.Close()

	restoredPath := filepath.Join(directory, "restored.db")
	if err := Restore(context.Background(), backup, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := lab.Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	if err = restored.CheckSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	var restoredRuns, restoredObservations int
	if err = restored.DB.QueryRow("SELECT count(*) FROM lab_runs").Scan(&restoredRuns); err != nil {
		t.Fatal(err)
	}
	if err = restored.DB.QueryRow("SELECT count(*) FROM lab_observations").Scan(&restoredObservations); err != nil {
		t.Fatal(err)
	}
	if restoredRuns != sourceRuns || restoredObservations != sourceObservations {
		t.Fatal("restored counts differ", sourceRuns, sourceObservations, restoredRuns, restoredObservations)
	}
	var copyCount int
	if err = restored.DB.QueryRow("SELECT count(*) FROM lab_preferences WHERE actor='backup-check'").Scan(&copyCount); err != nil || copyCount != 0 {
		t.Fatal("backup did not preserve the snapshot boundary", err, copyCount)
	}
}

func TestBackupAndRestoreRefuseExistingOrUnsafePaths(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	store := seededStore(t, source)
	defer store.DB.Close()
	backup := filepath.Join(directory, "backup.db")
	if err := Backup(context.Background(), source, backup); err != nil {
		t.Fatal(err)
	}
	if err := Backup(context.Background(), source, backup); err == nil {
		t.Fatal("backup overwrote an existing destination")
	}
	if err := Backup(context.Background(), source, source); err == nil {
		t.Fatal("backup accepted the source as destination")
	}
	restoreDestination := filepath.Join(directory, "restore.db")
	if err := Restore(context.Background(), backup, restoreDestination); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), backup, restoreDestination); err == nil {
		t.Fatal("restore overwrote an existing destination")
	}
	if err := Restore(context.Background(), backup, backup); err == nil {
		t.Fatal("restore accepted the source as destination")
	}
}

func TestVerifyRejectsCorruptAndRelativePaths(t *testing.T) {
	directory := t.TempDir()
	corrupt := filepath.Join(directory, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), corrupt); err == nil {
		t.Fatal("corrupt database passed verify")
	}
	if err := Verify(context.Background(), "relative.db"); err == nil {
		t.Fatal("relative path passed verify")
	}
	if err := Restore(context.Background(), corrupt, filepath.Join(directory, "restored.db")); err == nil {
		t.Fatal("corrupt source passed restore")
	}
}
