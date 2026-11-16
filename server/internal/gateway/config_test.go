package gateway

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublicKeyConfigAndRotation(t *testing.T) {
	pub, key := testKeys(t)
	directory := t.TempDir()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(directory, "ozal-public.pem"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600)
	path := filepath.Join(directory, "gateway.json")
	body := []byte(`{"issuer":"https://login.acme.example","audience":"custodia","public_origin":"https://custodia.acme.example","public_key_files":["ozal-public.pem"]}`)
	os.WriteFile(path, body, 0600)
	cfg, err := LoadConfig(path)
	if err != nil || len(cfg.Keys) != 1 {
		t.Fatal(err)
	}
	for _, value := range []string{"http://public.acme.example", "https://custodia.acme.example/path", "https://custodia.acme.example?", "https://user@custodia.acme.example", "https://:443"} {
		bad := cfg
		bad.PublicOrigin = value
		if bad.Validate() == nil {
			t.Fatal("invalid public origin accepted", value)
		}
	}
	// Only explicit numeric loopback supports a local integration profile.
	local := cfg
	local.PublicOrigin = "http://127.0.0.1:9999"
	if err = local.Validate(); err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(directory, "ozal-public.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600)
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("private key accepted as pin")
	}
	// Rotation is an explicit verifier key set, not token-provided material.
	pub2, key2 := testKeys(t)
	claims := testClaims(testTime())
	rotated := map[string]ed25519.PublicKey{KeyID(pub): pub, KeyID(pub2): pub2}
	for _, signing := range []ed25519.PrivateKey{key, key2} {
		if _, err = Verify(signClaims(t, signing, claims), rotated, claims.Issuer, claims.Audience, testTime()); err != nil {
			t.Fatal("rotation overlap rejected", err)
		}
	}
	delete(rotated, KeyID(pub))
	if _, err = Verify(signClaims(t, key, claims), rotated, claims.Issuer, claims.Audience, testTime()); err == nil {
		t.Fatal("retired key accepted")
	}
	var parsed map[string]any
	json.Unmarshal(body, &parsed)
	parsed["keys_url"] = "https://attacker.example/keys"
	b, _ := json.Marshal(parsed)
	os.WriteFile(path, b, 0600)
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("unknown remote key option accepted")
	}
}

func testTime() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }
