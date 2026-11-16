// Package sqlitebackup creates and restores consistent SQLite snapshots.
//
// The backup uses SQLite's VACUUM INTO on a separate connection, so the source
// database stays untouched. The destination must be a new file; restore never
// overwrites an existing database.
package sqlitebackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Backup writes a consistent snapshot of sourcePath to destinationPath. The
// destination must not exist and its parent directory must already exist.
func Backup(ctx context.Context, sourcePath, destinationPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := regularFile(sourcePath); err != nil {
		return fmt.Errorf("backup source: %w", err)
	}
	if !filepath.IsAbs(destinationPath) {
		return errors.New("backup destination must be an absolute path")
	}
	if samePath(sourcePath, destinationPath) {
		return errors.New("backup destination must differ from the source")
	}
	if _, err := os.Lstat(destinationPath); !os.IsNotExist(err) {
		return errors.New("backup destination already exists")
	}
	if info, err := os.Stat(filepath.Dir(destinationPath)); err != nil || !info.IsDir() {
		return errors.New("backup destination directory does not exist")
	}
	db, err := sql.Open("sqlite", dsn(sourcePath))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		return fmt.Errorf("backup source: %w", err)
	}
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", destinationPath); err != nil {
		os.Remove(destinationPath)
		return fmt.Errorf("create backup: %w", err)
	}
	if err = os.Chmod(destinationPath, 0600); err != nil {
		os.Remove(destinationPath)
		return err
	}
	if err = Verify(ctx, destinationPath); err != nil {
		os.Remove(destinationPath)
		return fmt.Errorf("verify backup: %w", err)
	}
	return nil
}

// Restore copies a verified snapshot to a new destination database. The
// destination must not exist. The copy is written to a temporary file, synced,
// verified and linked into place atomically.
func Restore(ctx context.Context, sourcePath, destinationPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := Verify(ctx, sourcePath); err != nil {
		return fmt.Errorf("restore source: %w", err)
	}
	if !filepath.IsAbs(destinationPath) {
		return errors.New("restore destination must be an absolute path")
	}
	if samePath(sourcePath, destinationPath) {
		return errors.New("restore destination must differ from the source")
	}
	if _, err := os.Lstat(destinationPath); !os.IsNotExist(err) {
		return errors.New("restore destination already exists")
	}
	directory := filepath.Dir(destinationPath)
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return errors.New("restore destination directory does not exist")
	}
	temp, err := os.CreateTemp(directory, ".custodia-restore-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tempName)
		}
	}()
	if err = temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		temp.Close()
		return err
	}
	_, copyErr := io.Copy(temp, source)
	source.Close()
	if copyErr != nil {
		temp.Close()
		return copyErr
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = Verify(ctx, tempName); err != nil {
		return fmt.Errorf("verify restored copy: %w", err)
	}
	// Link is an atomic create-only rename on the same filesystem, so an
	// existing destination is never overwritten.
	if err = os.Link(tempName, destinationPath); err != nil {
		return err
	}
	cleanup = false
	if err = os.Remove(tempName); err != nil {
		os.Remove(destinationPath)
		return err
	}
	if dir, err := os.Open(directory); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return Verify(ctx, destinationPath)
}

// Verify opens a database read-only and requires a clean integrity check and
// no foreign-key violations.
func Verify(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := regularFile(path); err != nil {
		return err
	}
	location := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{}
	query.Set("mode", "ro")
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var result string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if strings.TrimSpace(result) != "ok" {
		return errors.New("sqlite integrity check failed")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("sqlite foreign key check failed")
	}
	return rows.Err()
}

func dsn(path string) string {
	location := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	location.RawQuery = query.Encode()
	return location.String()
}

func regularFile(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("path must be a regular file")
	}
	return nil
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
