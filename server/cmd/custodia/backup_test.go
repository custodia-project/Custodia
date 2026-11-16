package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"custodia/server/internal/lab"
)

func runCommand(t *testing.T, args ...string) error {
	t.Helper()
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = append([]string{"custodia"}, args...)
	return run()
}

func TestBackupRestoreCLIOnDisposableInstance(t *testing.T) {
	base := t.TempDir()
	sourceDir := filepath.Join(base, "source")
	restoredDir := filepath.Join(base, "restored")
	backup := filepath.Join(base, "state.backup")
	if err := runCommand(t, "init-lab", "--dir", sourceDir); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(t, "backup", "--dir", sourceDir, "--out", backup); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(backup); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatal("backup file missing", err)
	}
	if err := os.Mkdir(restoredDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(t, "restore", "--dir", restoredDir, "--from", backup); err != nil {
		t.Fatal(err)
	}
	store, err := lab.Open(filepath.Join(restoredDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if err = store.CheckSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	var snapshots, connections int
	if err = store.DB.QueryRow("SELECT count(*) FROM lab_snapshot").Scan(&snapshots); err != nil || snapshots == 0 {
		t.Fatal("restored snapshot missing", err, snapshots)
	}
	if err = store.DB.QueryRow("SELECT count(*) FROM lab_connections").Scan(&connections); err != nil || connections == 0 {
		t.Fatal("restored connections missing", err, connections)
	}
	if err = runCommand(t, "backup", "--dir", sourceDir, "--out", backup); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	if err = runCommand(t, "restore", "--dir", restoredDir, "--from", backup); err == nil {
		t.Fatal("restore overwrote an existing database")
	}
}

func TestBackupRestoreRefuseMissingInputs(t *testing.T) {
	base := t.TempDir()
	if err := runCommand(t, "backup", "--dir", base, "--out", filepath.Join(base, "out.db")); err == nil {
		t.Fatal("backup accepted a directory without state.db")
	}
	if err := runCommand(t, "restore", "--dir", base, "--from", filepath.Join(base, "missing.db")); err == nil {
		t.Fatal("restore accepted a missing backup")
	}
}
