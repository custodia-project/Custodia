package gateway

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Issuer         string                       `json:"issuer"`
	Audience       string                       `json:"audience"`
	PublicOrigin   string                       `json:"public_origin"`
	PublicKeyFiles []string                     `json:"public_key_files"`
	Keys           map[string]ed25519.PublicKey `json:"-"`
}

func origin(value string, loopbackHTTP bool) bool {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.ForceQuery || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return loopbackHTTP && u.Scheme == "http" && ip != nil && ip.IsLoopback()
}
func (c Config) Validate() error {
	if !origin(c.Issuer, false) || !origin(c.PublicOrigin, true) {
		return fmt.Errorf("issuer must be an HTTPS origin; public_origin requires HTTPS or numeric HTTP loopback")
	}
	if strings.TrimSpace(c.Audience) != c.Audience || len(c.Audience) == 0 || len(c.Audience) > 256 || len(c.Keys) == 0 || len(c.Keys) > 8 {
		return fmt.Errorf("audience and one to eight pinned public keys are required")
	}
	for id, key := range c.Keys {
		if len(key) != ed25519.PublicKeySize || KeyID(key) != id {
			return fmt.Errorf("invalid pinned Ed25519 key")
		}
	}
	return nil
}
func LoadConfig(path string) (Config, error) {
	var c Config
	file, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("cannot open gateway configuration")
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(b) > 65536 {
		return c, fmt.Errorf("invalid gateway configuration size")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid gateway configuration")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("invalid gateway configuration")
	}
	if len(c.PublicKeyFiles) == 0 || len(c.PublicKeyFiles) > 8 {
		return c, fmt.Errorf("one to eight public_key_files are required")
	}
	c.Keys = map[string]ed25519.PublicKey{}
	for _, name := range c.PublicKeyFiles {
		if !filepath.IsAbs(name) {
			name = filepath.Join(filepath.Dir(path), name)
		}
		file, err := os.Open(name)
		if err != nil {
			return c, fmt.Errorf("cannot open pinned public key")
		}
		b, err := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		if err != nil || len(b) > 4096 {
			return c, fmt.Errorf("invalid public key size")
		}
		block, rest := pem.Decode(b)
		if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
			return c, fmt.Errorf("expected one PKIX PUBLIC KEY")
		}
		value, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return c, fmt.Errorf("invalid pinned public key")
		}
		key, ok := value.(ed25519.PublicKey)
		if !ok {
			return c, fmt.Errorf("pinned key must be Ed25519")
		}
		c.Keys[KeyID(key)] = key
	}
	return c, c.Validate()
}
