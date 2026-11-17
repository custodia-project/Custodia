// Package secrets stores the master key and the AEAD used for connection
// secrets. Ciphertext never leaves SQLite; the key lives outside the database.
package secrets

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	KeySize   = chacha20poly1305.KeySize
	NonceSize = chacha20poly1305.NonceSizeX
	maxFile   = 64
)

var (
	ErrKeyMissing = errors.New("master key is missing")
	ErrKeyInvalid = errors.New("master key is invalid")
)

// Key is a 32-byte XChaCha20-Poly1305 key.
type Key [KeySize]byte

// Generate writes a new key with O_EXCL and 0600. It never overwrites.
func Generate(path string) (*Key, error) {
	if path == "" {
		return nil, ErrKeyInvalid
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("master key file: %w", err)
	}
	if _, err = file.Write(key); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if err = file.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	var result Key
	copy(result[:], key)
	return &result, nil
}

// Load reads exactly 32 bytes from a regular file that is not readable by
// group or others. A key with any other shape fails closed.
func Load(path string) (*Key, error) {
	if path == "" {
		return nil, ErrKeyMissing
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrKeyMissing
		}
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != KeySize {
		return nil, ErrKeyInvalid
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: permissions must be 0600", ErrKeyInvalid)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var key Key
	copy(key[:], content)
	return &key, nil
}

// Encrypt returns a fresh nonce and ciphertext authenticated by aad.
func (k Key) Encrypt(plaintext []byte, aad string) (nonce, ciphertext []byte, err error) {
	aead, err := chacha20poly1305.NewX(k[:])
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, NonceSize)
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = aead.Seal(nil, nonce, plaintext, []byte(aad))
	return nonce, ciphertext, nil
}

// Decrypt authenticates the ciphertext and aad before returning the plaintext.
func (k Key) Decrypt(nonce, ciphertext []byte, aad string) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, ErrKeyInvalid
	}
	aead, err := chacha20poly1305.NewX(k[:])
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, errors.New("secret authentication failed")
	}
	return plaintext, nil
}

// PathFor returns the conventional key path for a data directory.
func PathFor(dataDir string) string {
	return filepath.Join(dataDir, "master.key")
}

// ValidSecretValue bounds a connection secret and rejects control characters,
// so a value cannot break logs or line-oriented imports.
func ValidSecretValue(value string) bool {
	if value == "" || len(value) > 4096 {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n")
}
