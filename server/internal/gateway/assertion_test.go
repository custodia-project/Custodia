package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

func testKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}
func testClaims(now time.Time) Claims {
	return Claims{Issuer: "https://login.acme.example", Audience: "custodia", Subject: "opaque-admin", Email: "admin@acme.example", Name: "Acme Admin", IssuedAt: now.Unix(), Expiry: now.Add(60 * time.Second).Unix(), ID: "random-request-id"}
}
func signPayload(t *testing.T, key ed25519.PrivateKey, payload []byte, extra map[jose.HeaderKey]any) string {
	t.Helper()
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", KeyID(key.Public().(ed25519.PublicKey)))
	for k, v := range extra {
		opts.WithHeader(k, v)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, opts)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func signClaims(t *testing.T, key ed25519.PrivateKey, claims Claims) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return signPayload(t, key, b, nil)
}

func TestPinnedAssertionPolicy(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	pub, key := testKeys(t)
	keys := map[string]ed25519.PublicKey{KeyID(pub): pub}
	base := testClaims(now)
	if got, err := Verify(signClaims(t, key, base), keys, base.Issuer, base.Audience, now); err != nil || got.Subject != base.Subject {
		t.Fatal("valid assertion rejected", err)
	}
	for _, change := range []func(*Claims){func(c *Claims) { c.Issuer = "https://other.example" }, func(c *Claims) { c.Audience = "other-app" }, func(c *Claims) { c.Subject = " " }, func(c *Claims) { c.ID = "" }, func(c *Claims) { c.Expiry = now.Add(-6 * time.Second).Unix(); c.IssuedAt = c.Expiry - 60 }, func(c *Claims) { c.IssuedAt = now.Add(6 * time.Second).Unix(); c.Expiry = c.IssuedAt + 60 }, func(c *Claims) { c.Expiry = c.IssuedAt + 61 }, func(c *Claims) { c.IssuedAt = 0 }, func(c *Claims) { c.Expiry = c.IssuedAt }} {
		c := base
		change(&c)
		if _, err := Verify(signClaims(t, key, c), keys, base.Issuer, base.Audience, now); err == nil {
			t.Fatal("invalid claims accepted")
		}
	}
	_, wrong := testKeys(t)
	if _, err := Verify(signClaims(t, wrong, base), keys, base.Issuer, base.Audience, now); err == nil {
		t.Fatal("untrusted key accepted")
	}
	payload, _ := json.Marshal(base)
	token := signPayload(t, wrong, payload, map[jose.HeaderKey]any{"kid": KeyID(pub)})
	if _, err := Verify(token, keys, base.Issuer, base.Audience, now); err == nil {
		t.Fatal("bad signature accepted")
	}
	for _, extra := range []map[jose.HeaderKey]any{{"jku": "https://keys.invalid"}, {"x5u": "https://keys.invalid"}, {"jwk": jose.JSONWebKey{Key: pub}}} {
		if _, err := Verify(signPayload(t, key, payload, extra), keys, base.Issuer, base.Audience, now); err == nil {
			t.Fatal("token-selected key material accepted")
		}
	}
	duplicate := append(payload[:len(payload)-1], []byte(`,"sub":"another-subject"}`)...)
	if _, err := Verify(signPayload(t, key, duplicate, nil), keys, base.Issuer, base.Audience, now); err == nil {
		t.Fatal("duplicate claim accepted")
	}
	// Clock tolerance is bounded, including its exact expiry boundary.
	c := base
	c.Expiry = now.Add(-4 * time.Second).Unix()
	c.IssuedAt = c.Expiry - 60
	if _, err := Verify(signClaims(t, key, c), keys, base.Issuer, base.Audience, now); err != nil {
		t.Fatal("configured clock tolerance rejected")
	}
}
