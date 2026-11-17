package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateLoadAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	generated, err := Generate(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Generate(path); err == nil {
		t.Fatal("existing key was overwritten")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated[:], loaded[:]) {
		t.Fatal("generated and loaded keys differ")
	}
	aad := "v1|acme-pam|auth"
	nonce, ciphertext, err := loaded.Encrypt([]byte("a connection secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("a connection secret")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	plaintext, err := loaded.Decrypt(nonce, ciphertext, aad)
	if err != nil || string(plaintext) != "a connection secret" {
		t.Fatal(string(plaintext), err)
	}
	if _, err = loaded.Decrypt(nonce, ciphertext, "v1|other|auth"); err == nil {
		t.Fatal("AAD mismatch decrypted")
	}
	tampered := append([]byte{}, ciphertext...)
	tampered[0] ^= 0xff
	if _, err = loaded.Decrypt(nonce, tampered, aad); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
	other := Key{}
	other[0] = 1
	if _, err = other.Decrypt(nonce, ciphertext, aad); err == nil {
		t.Fatal("another key decrypted")
	}
}

func TestLoadRejectsUnsafeFiles(t *testing.T) {
	directory := t.TempDir()
	if _, err := Load(filepath.Join(directory, "missing")); !errors.Is(err, ErrKeyMissing) {
		t.Fatal(err)
	}
	short := filepath.Join(directory, "short")
	if err := os.WriteFile(short, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(short); !errors.Is(err, ErrKeyInvalid) {
		t.Fatal(err)
	}
	open := filepath.Join(directory, "open")
	if err := os.WriteFile(open, make([]byte, KeySize), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(open); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatal(err)
	}
}

func TestValidSecretValue(t *testing.T) {
	for _, value := range []string{"", "line\nbreak", "carriage\rreturn", "null\x00byte", strings.Repeat("x", 4097)} {
		if ValidSecretValue(value) {
			t.Fatalf("unsafe secret accepted: %q", value)
		}
	}
	if !ValidSecretValue("a single line secret") {
		t.Fatal("valid secret rejected")
	}
}

func TestPathFor(t *testing.T) {
	if got := PathFor("/var/lib/custodia"); got != "/var/lib/custodia/master.key" {
		t.Fatal(got)
	}
}
