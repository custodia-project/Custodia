// Package updater applies a signed release with an explicit database backup
// and a verified rollback. The release key (allowed_signers) is a trust anchor
// pinned by the privileged caller: it never comes from the release, the
// console request or any file the service user can write. A console request
// may name HTTPS locations, which are downloaded into a private directory.
package updater

import (
	"archive/tar"
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

	"custodia/server/internal/sqlitebackup"
)

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
	Service        string
	NoSystemd      bool
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
	staging, err := os.MkdirTemp("", "custodia-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err = extract(options.Package, staging); err != nil {
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
	backup := filepath.Join(options.DataDir, "state.db.update-backup")
	previousBinary := options.Binary + ".previous"
	previousWeb := options.WebDir + ".previous"
	_ = os.RemoveAll(previousWeb)
	_ = os.Remove(backup)
	if err = sqlitebackup.Backup(ctx, filepath.Join(options.DataDir, "state.db"), backup); err != nil {
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
		if err := copyFile(backup, filepath.Join(options.DataDir, "state.db")); err != nil {
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
	if err = runMigrations(ctx, options.Binary, options.DataDir); err != nil {
		return fail(fmt.Errorf("migration: %w", err))
	}
	if options.Service != "" && !options.NoSystemd {
		if err = systemctl(ctx, "start", options.Service); err != nil {
			return fail(err)
		}
	}
	if options.HealthURL != "" {
		if err = waitHealth(ctx, options.HealthURL, options.HealthCA); err != nil {
			return fail(err)
		}
	}
	_ = os.Remove(backup)
	_ = os.Remove(previousBinary)
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

func extract(archive, destination string) error {
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
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
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
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err = io.Copy(out, io.LimitReader(reader, 1<<30)); err != nil {
				out.Close()
				return err
			}
			out.Close()
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

func verifyManifest(packageDir string, options Options) error {
	manifest, err := os.ReadFile(filepath.Join(packageDir, "manifest.json"))
	if err != nil {
		return err
	}
	command := exec.Command("ssh-keygen", "-Y", "verify", "-f", options.AllowedSigners, "-I", "custodia-release", "-n", "custodia-release", "-s", options.Signature)
	command.Stdin = strings.NewReader(string(manifest))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("release signature rejected: %s", strings.TrimSpace(string(output)))
	}
	var decoded struct {
		Version string `json:"version"`
		Files   []struct {
			Path   string `json:"path"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifest, &decoded); err != nil {
		return err
	}
	if decoded.Version != options.Version {
		return errors.New("release manifest version mismatch")
	}
	// The signature covers manifest.json only, so the signed manifest must be
	// authoritative for every extracted file: each listed file must exist with
	// its size and digest, and nothing unlisted may be installed. The files
	// were extracted into a private root-owned directory, so what is checked
	// here is exactly what will be copied.
	if len(decoded.Files) == 0 {
		return errors.New("release manifest lists no files")
	}
	listed := map[string]bool{}
	for _, file := range decoded.Files {
		name := filepath.Clean(filepath.FromSlash(file.Path))
		if file.Path == "" || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || listed[name] {
			return errors.New("release manifest lists an unsafe or duplicated path")
		}
		listed[name] = true
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

func runMigrations(ctx context.Context, binary, dataDir string) error {
	command := exec.CommandContext(ctx, binary, "migrate", "--dir", dataDir)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func systemctl(ctx context.Context, action, service string) error {
	command := exec.CommandContext(ctx, "systemctl", action, service)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func waitHealth(ctx context.Context, target, caFile string) error {
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
		client.Transport = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}
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
func ApplyRequest(ctx context.Context, requestPath string, options Options) error {
	if strings.TrimSpace(options.AllowedSigners) == "" {
		return errors.New("console updates require a pinned --allowed-signers")
	}
	body, err := os.ReadFile(requestPath)
	if err != nil {
		return err
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
			// A local path chosen by the service user must be a regular file,
			// so it cannot point the root helper at a device or a link.
			info, err := os.Lstat(location)
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				return "", errors.New("update request names a non-regular local file")
			}
			return location, nil
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
	if err := Apply(ctx, options); err != nil {
		return err
	}
	return os.Remove(requestPath)
}

func download(ctx context.Context, location, target string) error {
	request, err := http.NewRequestWithContext(ctx, "GET", location, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
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
