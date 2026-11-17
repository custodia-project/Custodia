package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
