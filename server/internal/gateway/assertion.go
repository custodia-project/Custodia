// Package gateway verifies the per-request Ozal identity contract using pinned keys.
package gateway

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

var ErrInvalid = errors.New("invalid identity assertion")

const Skew = 5 * time.Second

type Claims struct {
	Issuer   string   `json:"iss"`
	Audience string   `json:"aud"`
	Subject  string   `json:"sub"`
	Email    string   `json:"email,omitempty"`
	Name     string   `json:"name,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	IssuedAt int64    `json:"iat"`
	Expiry   int64    `json:"exp"`
	ID       string   `json:"jti"`
}

func KeyID(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:16])
}

// Validate is the pure claim policy. Email and groups grant no authority.
func Validate(c Claims, issuer, audience string, now time.Time) error {
	if c.Issuer != issuer || c.Audience != audience || strings.TrimSpace(c.Subject) == "" || len(c.Subject) > 1024 || strings.TrimSpace(c.ID) == "" || len(c.ID) > 256 {
		return ErrInvalid
	}
	if c.IssuedAt <= 0 || c.Expiry <= c.IssuedAt || c.Expiry-c.IssuedAt > 60 {
		return ErrInvalid
	}
	if c.IssuedAt > now.Add(Skew).Unix() || c.Expiry <= now.Add(-Skew).Unix() {
		return ErrInvalid
	}
	return nil
}

// DecodeObject rejects duplicate claims rather than letting the last value win.
func decodeClaims(payload []byte) (Claims, error) {
	var c Claims
	d := json.NewDecoder(bytes.NewReader(payload))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return c, ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return c, ErrInvalid
		}
		name, ok := key.(string)
		if !ok {
			return c, ErrInvalid
		}
		if _, exists := fields[name]; exists {
			return c, ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return c, ErrInvalid
		}
		fields[name] = value
	}
	if _, err = d.Token(); err != nil {
		return c, ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, ErrInvalid
	}
	clean, err := json.Marshal(fields)
	if err != nil {
		return c, ErrInvalid
	}
	if json.Unmarshal(clean, &c) != nil {
		return c, ErrInvalid
	}
	return c, nil
}

func Verify(token string, keys map[string]ed25519.PublicKey, issuer, audience string, now time.Time) (Claims, error) {
	var empty Claims
	if len(token) == 0 || len(token) > 8192 || strings.TrimSpace(token) != token {
		return empty, ErrInvalid
	}
	signed, err := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil || len(signed.Signatures) != 1 {
		return empty, ErrInvalid
	}
	header := signed.Signatures[0].Protected
	if header.JSONWebKey != nil {
		return empty, ErrInvalid
	}
	for _, name := range []jose.HeaderKey{"jku", "x5u", "crit", "b64"} {
		if _, found := header.ExtraHeaders[name]; found {
			return empty, ErrInvalid
		}
	}
	key, found := keys[header.KeyID]
	if !found || len(key) != ed25519.PublicKeySize {
		return empty, ErrInvalid
	}
	payload, err := signed.Verify(key)
	if err != nil {
		return empty, ErrInvalid
	}
	claims, err := decodeClaims(payload)
	if err != nil {
		return empty, err
	}
	if err = Validate(claims, issuer, audience, now); err != nil {
		return empty, err
	}
	return claims, nil
}
