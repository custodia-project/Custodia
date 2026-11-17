package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type testFile struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// signedPackage writes a package whose manifest lists every file with its
// digest and signs the manifest with a throwaway release key.
func signedPackage(t *testing.T, files map[string]string) (string, Options) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is required to sign the test release")
	}
	root := t.TempDir()
	packageDir := filepath.Join(root, "custodia-0.2.0-linux-amd64")
	listed := []testFile{}
	for name, content := range files {
		path := filepath.Join(packageDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		listed = append(listed, testFile{Path: name, Bytes: len(content), SHA256: hex.EncodeToString(sum[:])})
	}
	manifest, err := json.Marshal(map[string]any{"version": "0.2.0", "files": listed})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(packageDir, "manifest.json")
	if err = os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "release-key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	if output, err := exec.Command("ssh-keygen", "-Y", "sign", "-f", key, "-n", "custodia-release", manifestPath).CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(public))
	signers := filepath.Join(root, "allowed_signers")
	if err = os.WriteFile(signers, []byte("custodia-release "+fields[0]+" "+fields[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return packageDir, Options{Version: "0.2.0", Signature: manifestPath + ".sig", AllowedSigners: signers}
}

var releaseFiles = map[string]string{
	"custodia":       "signed binary",
	"VERSION":        "0.2.0\nlinux/amd64\n",
	"web/index.html": "<!doctype html>",
}

func TestVerifyManifestAcceptsTheSignedFiles(t *testing.T) {
	packageDir, options := signedPackage(t, releaseFiles)
	if err := verifyManifest(packageDir, options); err != nil {
		t.Fatal(err)
	}
}

// The signature covers only manifest.json: a repacked archive that keeps a
// genuine signed manifest but swaps the binary must be refused.
func TestVerifyManifestRejectsASwappedBinary(t *testing.T) {
	packageDir, options := signedPackage(t, releaseFiles)
	if err := os.WriteFile(filepath.Join(packageDir, "custodia"), []byte("malicious binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(packageDir, options); err == nil || !strings.Contains(err.Error(), "does not match the signed manifest") {
		t.Fatal("swapped binary accepted", err)
	}
}

func TestVerifyManifestRejectsUnlistedAndMissingFiles(t *testing.T) {
	packageDir, options := signedPackage(t, releaseFiles)
	if err := os.WriteFile(filepath.Join(packageDir, "web", "extra.js"), []byte("injected"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(packageDir, options); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatal("unlisted file accepted", err)
	}
	packageDir, options = signedPackage(t, releaseFiles)
	if err := os.Remove(filepath.Join(packageDir, "web", "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(packageDir, options); err == nil {
		t.Fatal("missing listed file accepted")
	}
}

func TestNewerVersionRefusesReplaysAndDowngrades(t *testing.T) {
	for _, case_ := range []struct {
		candidate, current string
		newer              bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.10.0", "0.9.9", true},
		{"1.0", "0.9.9", true},
		{"v0.3.0", "0.2.9", true},
		{"0.2.0", "0.2.0", false},
		{"0.1.0", "0.2.0", false},
		{"0.2.0-rc1", "0.2.0", false},
		{"0.2.0", "dev", true},
		{"dev", "dev", false},
	} {
		if got := newerVersion(case_.candidate, case_.current); got != case_.newer {
			t.Fatalf("newerVersion(%q, %q) = %v, want %v", case_.candidate, case_.current, got, case_.newer)
		}
	}
}

func TestCheckTrustAnchorRefusesWritableOrLinkedKeys(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, "allowed_signers")
	if err := os.WriteFile(key, []byte("custodia-release ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := checkTrustAnchor(key); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(key, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := checkTrustAnchor(key); err == nil {
		t.Fatal("world-writable release key accepted")
	}
	link := filepath.Join(directory, "linked")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	if err := checkTrustAnchor(link); err == nil {
		t.Fatal("symlinked release key accepted")
	}
}

type archiveEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func writeTestArchive(t *testing.T, entries ...archiveEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		flag := entry.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.body)), Typeflag: flag, Linkname: entry.linkname}
		if err = tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if flag == tar.TypeReg {
			if _, err = tarWriter.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractRefusesPastTheAggregateBudget(t *testing.T) {
	archive := writeTestArchive(t, archiveEntry{name: "package/payload", body: bytes.Repeat([]byte("a"), 2<<20)})
	destination := t.TempDir()
	if err := extract(archive, destination, 1<<20); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("oversized extraction error = %v", err)
	}
	var total int64
	if err := filepath.WalkDir(destination, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if total > 1<<20 {
		t.Fatalf("extracted %d bytes past the budget", total)
	}
}

// If extraction ran before the signature check, the symlink would fail first.
// The signature error proves the manifest is authenticated before any write.
func TestApplyVerifiesTheManifestBeforeExtracting(t *testing.T) {
	manifest := []byte(`{"version":"0.2.0","files":[]}`)
	archive := writeTestArchive(t,
		archiveEntry{name: "custodia-0.2.0-linux-amd64/manifest.json", body: manifest},
		archiveEntry{name: "custodia-0.2.0-linux-amd64/link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	)
	content, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	root := t.TempDir()
	signers := filepath.Join(root, "allowed_signers")
	if err := os.WriteFile(signers, []byte("custodia-release ssh-ed25519 AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	signature := filepath.Join(root, "manifest.json.sig")
	if err := os.WriteFile(signature, []byte("not a signature"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Apply(context.Background(), Options{
		DataDir: filepath.Join(root, "data"), Binary: filepath.Join(root, "bin", "custodia"), WebDir: filepath.Join(root, "web"),
		Package: archive, SHA256: hex.EncodeToString(digest[:]), Signature: signature, AllowedSigners: signers,
		Version: "0.2.0", CurrentVersion: "0.1.0",
	})
	if err == nil || !strings.Contains(err.Error(), "release signature rejected") {
		t.Fatalf("unsigned archive was not rejected at the signature: %v", err)
	}
}

func TestReadUpdateRequestRefusesLinksFifosAndOversize(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte(`{"manifest":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdateRequest(link); err == nil {
		t.Fatal("symlinked request accepted")
	}
	fifo := filepath.Join(root, "request.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdateRequest(fifo); err == nil {
		t.Fatal("FIFO request accepted")
	}
	large := filepath.Join(root, "large.json")
	if err := os.WriteFile(large, bytes.Repeat([]byte("x"), maxUpdateRequestBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdateRequest(large); err == nil {
		t.Fatal("oversized request accepted")
	}
}

func TestApplyRequestConsumesAnInvalidRequest(t *testing.T) {
	root := t.TempDir()
	request := filepath.Join(root, "request.json")
	if err := os.Mkdir(request, 0o700); err != nil {
		t.Fatal(err)
	}
	err := ApplyRequest(context.Background(), request, Options{AllowedSigners: filepath.Join(root, "signers")})
	if err == nil {
		t.Fatal("directory request accepted")
	}
	if _, statErr := os.Lstat(request); !os.IsNotExist(statErr) {
		t.Fatalf("invalid request was not consumed: %v", statErr)
	}
}
