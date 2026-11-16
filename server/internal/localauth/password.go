// Package localauth is the local username/password mode of Custodia: argon2id
// password hashes, server-side revocable sessions, CSRF and login throttling.
// It never accepts Ozal assertions, laboratory bearers or identity headers.
package localauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are argon2id cost parameters. Memory is in KiB.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultParams is the fixed production cost documented in docs/ACCESS.md.
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

// hashParams is only lowered by tests of this module.
var hashParams = DefaultParams

const (
	MinPasswordLength = 15
	MaxPasswordLength = 256
	// concurrentHashes bounds memory: each hash holds Params.Memory KiB.
	concurrentHashes = 4
)

var (
	ErrPasswordPolicy = errors.New("password does not satisfy the policy")
	errMalformedHash  = errors.New("malformed password hash")
	slots             = make(chan struct{}, concurrentHashes)
	dummyOnce         sync.Once
	dummyHash         string
)

// CheckPolicy applies length bounds and rejects the username as password.
func CheckPolicy(username, password string) error {
	count := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || count < MinPasswordLength || count > MaxPasswordLength || strings.ContainsRune(password, 0) {
		return ErrPasswordPolicy
	}
	if strings.EqualFold(strings.TrimSpace(password), strings.TrimSpace(username)) {
		return ErrPasswordPolicy
	}
	return nil
}

func acquire(ctx context.Context) error {
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func release() { <-slots }

// HashPassword returns a PHC-formatted argon2id hash with a random salt.
func HashPassword(ctx context.Context, password string) (string, error) {
	p := hashParams
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := acquire(ctx); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	release()
	encode := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads, encode(salt), encode(key)), nil
}

func parseHash(encoded string) (Params, []byte, []byte, error) {
	var p Params
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return p, nil, nil, errMalformedHash
	}
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &threads); err != nil {
		return p, nil, nil, errMalformedHash
	}
	if fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, threads) != parts[3] {
		return p, nil, nil, errMalformedHash
	}
	// Bounds stop a tampered row from turning a login into a memory/CPU bomb.
	if threads < 1 || threads > 16 || p.Time < 1 || p.Time > 10 || p.Memory < 8*threads || p.Memory > 256*1024 {
		return p, nil, nil, errMalformedHash
	}
	p.Threads = uint8(threads)
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return p, nil, nil, errMalformedHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, errMalformedHash
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key))
	return p, salt, key, nil
}

// VerifyPassword compares in constant time. A malformed hash never matches.
func VerifyPassword(ctx context.Context, encoded, password string) (bool, error) {
	p, salt, key, err := parseHash(encoded)
	if err != nil {
		return false, nil
	}
	if err = acquire(ctx); err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	release()
	return subtle.ConstantTimeCompare(candidate, key) == 1, nil
}

// burnDummy spends the same work as a real verification for unknown users.
func burnDummy(ctx context.Context, password string) error {
	dummyOnce.Do(func() {
		dummyHash, _ = HashPassword(context.Background(), "custodia-unknown-user-placeholder")
	})
	_, err := VerifyPassword(ctx, dummyHash, password)
	return err
}
