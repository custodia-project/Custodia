// Package updater applies a signed release with an explicit database backup
// and a verified rollback. The release key (allowed_signers) is a trust anchor
// pinned by the privileged caller: it never comes from the release, the
// console request or any file the service user can write. A console request
// may name HTTPS locations, which are downloaded into a private directory.
package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// maxManifestBytes bounds manifest.json itself. The real one is a few
	// kilobytes; the allowance also covers the detached signature file.
	maxManifestBytes = 1 << 20
	// maxUpdateRequestBytes bounds the service-owned request document.
	maxUpdateRequestBytes = 64 << 10
	// maxExtractBytes is the absolute ceiling for one release, independent of
	// what the signed manifest claims.
	maxExtractBytes = 1 << 30
	// maxExtractEntries stops an archive full of empty entries from consuming
	// inodes without consuming the byte budget.
	maxExtractEntries = 10000
)

type releaseManifest struct {
	Version string `json:"version"`
	Files   []struct {
		Path   string `json:"path"`
		Bytes  int64  `json:"bytes"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

type Options struct {
	DataDir        string
	Binary         string
	WebDir         string
	Package        string
	SHA256         string
	Signature      string
	AllowedSigners string
	Version        string
	// CurrentVersion is the installed version; a release that is not newer
	// is refused, so an old signed release cannot be replayed as a downgrade.
	CurrentVersion string
	HealthURL      string
	HealthCA       string
	// HealthServerName is the TLS name checked on the health certificate. The
	// updater connects to loopback, while the certificate names the public
	// host, so the name must be given explicitly.
	HealthServerName string
	Service          string
	NoSystemd        bool
}

// Apply verifies, backs up, swaps, migrates and health-checks one release.
// A failed health check restores the previous binary, web and database.
func Apply(ctx context.Context, options Options) error {
	for _, required := range []string{options.DataDir, options.Binary, options.WebDir, options.Package, options.SHA256, options.Signature, options.AllowedSigners, options.Version} {
		if strings.TrimSpace(required) == "" {
			return errors.New("update is missing a required path or version")
		}
	}
	if err := checkTrustAnchor(options.AllowedSigners); err != nil {
		return err
	}
	if err := verifyArchive(options.Package, options.SHA256); err != nil {
		return err
	}
	// The signed manifest is the only authority for what may be extracted.
	// Read and verify it before writing any archive entry, so an unsigned or
	// forged package cannot consume disk.
	manifest, err := readArchiveManifest(options.Package)
	if err != nil {
		return err
	}
	if err = verifyManifestSignature(manifest, options); err != nil {
		return err
	}
	_, _, total, err := decodeReleaseManifest(manifest, options.Version)
	if err != nil {
		return err
	}
	budget := total + 2*maxManifestBytes
	if budget < total || budget > maxExtractBytes {
		budget = maxExtractBytes
	}
	staging, err := os.MkdirTemp("", "custodia-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err = extract(options.Package, staging, budget); err != nil {
		return err
	}
	packageDir, err := findPackage(staging)
	if err != nil {
		return err
	}
	if err = verifyManifest(packageDir, options); err != nil {
		return err
	}
	if err = verifyVersion(packageDir, options.Version); err != nil {
		return err
	}
	if !newerVersion(options.Version, options.CurrentVersion) {
		return fmt.Errorf("release %s is not newer than the installed %s", options.Version, options.CurrentVersion)
	}
	// The data directory belongs to the service user. Root never opens a
	// path inside it: backup, migration and rollback of the database run as
	// the directory owner, so a link planted there cannot redirect a
	// privileged write.
	owner, err := dataOwner(options.DataDir)
	if err != nil {
		return err
	}
	database := filepath.Join(options.DataDir, "state.db")
	backup := filepath.Join(options.DataDir, "state.db.update-backup")
	previousBinary := options.Binary + ".previous"
	previousWeb := options.WebDir + ".previous"
	_ = os.RemoveAll(previousWeb)
	if err = runAs(ctx, owner, "rm", "-f", "--", backup); err != nil {
		return fmt.Errorf("database backup: %w", err)
	}
	if err = runAs(ctx, owner, options.Binary, "backup", "--dir", options.DataDir, "--out", backup); err != nil {
		return fmt.Errorf("database backup: %w", err)
	}
	if err = copyFile(options.Binary, previousBinary); err != nil {
		return fmt.Errorf("binary backup: %w", err)
	}
	if err = os.Rename(options.WebDir, previousWeb); err != nil {
		return fmt.Errorf("web backup: %w", err)
	}
	restore := func() error {
		var failures []string
		restoreBinary := options.Binary + ".restore"
		if err := copyFile(previousBinary, restoreBinary); err != nil {
			failures = append(failures, err.Error())
		} else if err := os.Chmod(restoreBinary, 0o755); err != nil {
			failures = append(failures, err.Error())
		} else if err := os.Rename(restoreBinary, options.Binary); err != nil {
			failures = append(failures, err.Error())
		}
		_ = os.RemoveAll(options.WebDir)
		if err := os.Rename(previousWeb, options.WebDir); err != nil {
			failures = append(failures, err.Error())
		}
		if err := runAs(ctx, owner, "rm", "-f", "--", database+"-wal", database+"-shm"); err != nil {
			failures = append(failures, err.Error())
		} else if err := runAs(ctx, owner, "cp", "--remove-destination", "--", backup, database); err != nil {
			failures = append(failures, err.Error())
		}
		if len(failures) > 0 {
			return errors.New("rollback incomplete: " + strings.Join(failures, "; "))
		}
		return nil
	}
	fail := func(cause error) error {
		restoreErr := restore()
		if options.Service != "" && !options.NoSystemd {
			_ = systemctl(ctx, "restart", options.Service)
		}
		if restoreErr != nil {
			return fmt.Errorf("%w; %v", cause, restoreErr)
		}
		return cause
	}
	if options.Service != "" && !options.NoSystemd {
		if err = systemctl(ctx, "stop", options.Service); err != nil {
			return fail(err)
		}
	}
	newBinary := options.Binary + ".new"
	if err = copyFile(filepath.Join(packageDir, "custodia"), newBinary); err != nil {
		return fail(err)
	}
	if err = os.Chmod(newBinary, 0o755); err != nil {
		return fail(err)
	}
	if err = os.Rename(newBinary, options.Binary); err != nil {
		return fail(err)
	}
	if err = os.Rename(filepath.Join(packageDir, "web"), options.WebDir); err != nil {
		return fail(err)
	}
	if err = runAs(ctx, owner, options.Binary, "migrate", "--dir", options.DataDir); err != nil {
		return fail(fmt.Errorf("migration: %w", err))
	}
	if options.Service != "" && !options.NoSystemd {
		if err = systemctl(ctx, "start", options.Service); err != nil {
			return fail(err)
		}
	}
	if options.HealthURL != "" {
		if err = waitHealth(ctx, options.HealthURL, options.HealthCA, options.HealthServerName); err != nil {
			return fail(err)
		}
	}
	_ = runAs(ctx, owner, "rm", "-f", "--", backup)
	_ = os.Remove(previousBinary)
	return nil
}

// dataOwner returns the credential that data directory operations must use.
// The directory itself must be a real directory, not a link. When the
// updater runs as root and the directory belongs to another user, every
// operation inside it drops to that user; otherwise it runs unchanged.
func dataOwner(dataDir string) (*syscall.Credential, error) {
	info, err := os.Lstat(dataDir)
	if err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("data directory must be a directory, not a link")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("data directory ownership is unknown")
	}
	if os.Geteuid() != 0 || stat.Uid == 0 {
		return nil, nil
	}
	return &syscall.Credential{Uid: stat.Uid, Gid: stat.Gid, Groups: []uint32{stat.Gid}}, nil
}

// readAs copies a file named by an untrusted request into target, opening it
// as owner when set. The copy is bounded in size and time.
func readAs(ctx context.Context, owner *syscall.Credential, location, target string) error {
	const limit = 1 << 30
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "cat", "--", location)
	if owner != nil {
		command.SysProcAttr = &syscall.SysProcAttr{Credential: owner}
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	if err = command.Start(); err != nil {
		return err
	}
	copied, copyErr := io.Copy(output, io.LimitReader(stdout, limit+1))
	if copied > limit {
		_ = command.Process.Kill()
		_ = command.Wait()
		return errors.New("file is larger than the update limit")
	}
	if err = command.Wait(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return copyErr
}

// runAs runs a command, as owner when it is set, and reports its output on
// failure.
func runAs(ctx context.Context, owner *syscall.Credential, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	if owner != nil {
		command.SysProcAttr = &syscall.SysProcAttr{Credential: owner}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func verifyArchive(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), strings.TrimSpace(expected)) {
		return errors.New("release archive checksum mismatch")
	}
	return nil
}

// readArchiveManifest scans the compressed archive for the top-level
// manifest.json without writing anything. The scan is bounded so a gzip bomb
// cannot turn this pre-check into its own denial of service.
func readArchiveManifest(archive string) ([]byte, error) {
	file, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	bounded := &io.LimitedReader{R: gzipReader, N: maxExtractBytes + 1}
	reader := tar.NewReader(bounded)
	entries := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if bounded.N <= 0 {
				return nil, errors.New("release archive exceeds the extraction limit")
			}
			return nil, err
		}
		entries++
		if entries > maxExtractEntries {
			return nil, errors.New("release archive contains too many entries")
		}
		name := filepath.Clean(header.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return nil, errors.New("release archive contains an unsafe path")
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		parts := strings.Split(filepath.ToSlash(name), "/")
		if len(parts) != 2 || parts[1] != "manifest.json" {
			continue
		}
		if header.Size < 0 || header.Size > maxManifestBytes {
			return nil, errors.New("release manifest is too large")
		}
		manifest := make([]byte, header.Size)
		if _, err = io.ReadFull(reader, manifest); err != nil {
			return nil, err
		}
		return manifest, nil
	}
	if bounded.N <= 0 {
		return nil, errors.New("release archive exceeds the extraction limit")
	}
	return nil, errors.New("release archive has no package manifest")
}

func extract(archive, destination string, budget int64) error {
	if budget <= 0 || budget > maxExtractBytes {
		budget = maxExtractBytes
	}
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	remaining := budget
	entries := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxExtractEntries {
			return errors.New("release archive contains too many entries")
		}
		name := filepath.Clean(header.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("release archive contains an unsafe path")
		}
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > remaining {
				return errors.New("release archive exceeds the extraction budget")
			}
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			copied, copyErr := io.CopyN(out, reader, header.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if copied != header.Size {
				return errors.New("release archive entry is truncated")
			}
			remaining -= copied
		default:
			return errors.New("release archive contains a non-regular entry")
		}
	}
	return nil
}

func findPackage(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			if _, err := os.Stat(filepath.Join(path, "manifest.json")); err == nil {
				return path, nil
			}
		}
	}
	return "", errors.New("release archive has no package manifest")
}

// checkTrustAnchor refuses a release key that a less privileged user could
// replace: it must be a regular file (not a symlink), not writable by group or
// others and, when the updater runs as root, owned by root.
func checkTrustAnchor(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("release key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("release key must be a regular file, not a link")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("release key must not be writable by group or others")
	}
	if os.Geteuid() == 0 {
		if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != 0 {
			return errors.New("release key must be owned by root")
		}
	}
	return nil
}

func verifyManifestSignature(manifest []byte, options Options) error {
	command := exec.Command("ssh-keygen", "-Y", "verify", "-f", options.AllowedSigners, "-I", "custodia-release", "-n", "custodia-release", "-s", options.Signature)
	command.Stdin = bytes.NewReader(manifest)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("release signature rejected: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// decodeReleaseManifest validates the signed manifest's file list and returns
// its total size. It runs before extraction so the signature bounds what may
// be written, not just what may be installed.
func decodeReleaseManifest(manifest []byte, version string) (releaseManifest, map[string]bool, int64, error) {
	var decoded releaseManifest
	if err := json.Unmarshal(manifest, &decoded); err != nil {
		return decoded, nil, 0, err
	}
	if decoded.Version != version {
		return decoded, nil, 0, errors.New("release manifest version mismatch")
	}
	if len(decoded.Files) == 0 {
		return decoded, nil, 0, errors.New("release manifest lists no files")
	}
	listed := map[string]bool{}
	var total int64
	for _, file := range decoded.Files {
		name := filepath.Clean(filepath.FromSlash(file.Path))
		if file.Path == "" || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || listed[name] || name == "manifest.json" || name == "manifest.json.sig" || file.Bytes < 0 {
			return decoded, nil, 0, errors.New("release manifest lists an unsafe, reserved or duplicated path")
		}
		if file.Bytes > maxExtractBytes-total {
			return decoded, nil, 0, errors.New("release manifest exceeds the extraction limit")
		}
		listed[name] = true
		total += file.Bytes
	}
	return decoded, listed, total, nil
}

func verifyManifest(packageDir string, options Options) error {
	manifest, err := os.ReadFile(filepath.Join(packageDir, "manifest.json"))
	if err != nil {
		return err
	}
	if err = verifyManifestSignature(manifest, options); err != nil {
		return err
	}
	decoded, listed, _, err := decodeReleaseManifest(manifest, options.Version)
	if err != nil {
		return err
	}
	// The signature covers manifest.json only, so the signed manifest must be
	// authoritative for every extracted file: each listed file must exist with
	// its size and digest, and nothing unlisted may be installed. The files
	// were extracted into a private root-owned directory, so what is checked
	// here is exactly what will be copied.
	for _, file := range decoded.Files {
		name := filepath.Clean(filepath.FromSlash(file.Path))
		digest, size, err := fileDigest(filepath.Join(packageDir, name))
		if err != nil {
			return fmt.Errorf("release file %s: %w", file.Path, err)
		}
		if size != file.Bytes || !strings.EqualFold(digest, file.SHA256) {
			return fmt.Errorf("release file %s does not match the signed manifest", file.Path)
		}
	}
	return filepath.WalkDir(packageDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(packageDir, path)
		if err != nil {
			return err
		}
		if relative == "manifest.json" || relative == "manifest.json.sig" {
			return nil
		}
		if !listed[relative] {
			return fmt.Errorf("release file %s is not listed in the signed manifest", filepath.ToSlash(relative))
		}
		return nil
	})
}

func fileDigest(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

// newerVersion reports whether candidate is strictly newer than current for
// dotted numeric versions (an optional leading "v" and a "-suffix" are
// ignored for ordering). An unknown current version only refuses the same
// version string.
func newerVersion(candidate, current string) bool {
	parse := func(value string) ([]int, bool) {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		if index := strings.IndexAny(value, "-+"); index >= 0 {
			value = value[:index]
		}
		parts := strings.Split(value, ".")
		numbers := make([]int, len(parts))
		for i, part := range parts {
			number := 0
			if part == "" {
				return nil, false
			}
			for _, r := range part {
				if r < '0' || r > '9' {
					return nil, false
				}
				number = number*10 + int(r-'0')
			}
			numbers[i] = number
		}
		return numbers, true
	}
	next, okNext := parse(candidate)
	installed, okInstalled := parse(current)
	if !okNext || !okInstalled {
		return strings.TrimSpace(candidate) != strings.TrimSpace(current)
	}
	for i := 0; i < len(next) || i < len(installed); i++ {
		a, b := 0, 0
		if i < len(next) {
			a = next[i]
		}
		if i < len(installed) {
			b = installed[i]
		}
		if a != b {
			return a > b
		}
	}
	return false
}

func verifyVersion(packageDir, expected string) error {
	content, err := os.ReadFile(filepath.Join(packageDir, "VERSION"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(strings.Split(string(content), "\n")[0]) != expected {
		return errors.New("release VERSION does not match")
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, input)
	return err
}

func systemctl(ctx context.Context, action, service string) error {
	command := exec.CommandContext(ctx, "systemctl", action, service)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func waitHealth(ctx context.Context, target, caFile, serverName string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("invalid health CA")
		}
		client.Transport = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: serverName}}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, "GET", target, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errors.New("updated service did not become healthy")
}

// ApplyRequest reads the console request written by the service user. The
// request embeds the release manifest; package and signature may be HTTPS URLs
// downloaded into a private temporary directory, or regular local files. The
// request is untrusted: an allowed_signers field in it is ignored, and the
// release is verified only against options.AllowedSigners, pinned by the
// privileged caller.
// readUpdateRequest opens the service-owned request without following links
// and without blocking on a FIFO. The path is fixed inside the service data
// directory, which the service account controls; root must treat every byte
// and every file type there as hostile.
func readUpdateRequest(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxUpdateRequestBytes {
		return nil, errors.New("update request must be a small regular file")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxUpdateRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxUpdateRequestBytes {
		return nil, errors.New("update request exceeds its size limit")
	}
	return body, nil
}

func ApplyRequest(ctx context.Context, requestPath string, options Options) error {
	if strings.TrimSpace(options.AllowedSigners) == "" {
		return errors.New("console updates require a pinned --allowed-signers")
	}
	body, readErr := readUpdateRequest(requestPath)
	// Consume the path whatever it was (regular file, link, FIFO or
	// directory). RemoveAll unlinks a link instead of following it.
	removeErr := os.RemoveAll(requestPath)
	if readErr != nil {
		return readErr
	}
	if removeErr != nil {
		return fmt.Errorf("consume update request: %w", removeErr)
	}
	var request struct {
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return err
	}
	var manifest struct {
		Version   string `json:"version"`
		Package   string `json:"package"`
		SHA256    string `json:"sha256"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(request.Manifest, &manifest); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "custodia-update-request-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	materialize := func(location, name string) (string, error) {
		if !strings.HasPrefix(location, "https://") {
			// A local path is chosen by the service user, so root never opens
			// it: a child process with the data directory owner's credentials
			// reads it (following only what that user could follow) and root
			// receives the bytes into its private directory. A device or a
			// FIFO cannot exceed the size limit or the deadline. Every later
			// step uses only the copy, verified against the signed manifest.
			owner, err := dataOwner(options.DataDir)
			if err != nil {
				return "", err
			}
			target := filepath.Join(directory, name)
			if err := readAs(ctx, owner, location, target); err != nil {
				return "", fmt.Errorf("update request file: %w", err)
			}
			return target, nil
		}
		target := filepath.Join(directory, name)
		if err := download(ctx, location, target); err != nil {
			return "", err
		}
		return target, nil
	}
	options.Version = manifest.Version
	options.SHA256 = manifest.SHA256
	if options.Package, err = materialize(manifest.Package, "package.tar.gz"); err != nil {
		return err
	}
	if options.Signature, err = materialize(manifest.Signature, "manifest.json.sig"); err != nil {
		return err
	}
	return Apply(ctx, options)
}

func download(ctx context.Context, location, target string) error {
	request, err := http.NewRequestWithContext(ctx, "GET", location, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("release download has too many redirects")
		}
		if next.URL.Scheme != "https" {
			return errors.New("release download redirect must stay on HTTPS")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("release download returned %d", response.StatusCode)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, io.LimitReader(response.Body, 1<<30))
	return err
}
