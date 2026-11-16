package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitLabInitialization(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	dir := filepath.Join(t.TempDir(), "lab")
	os.Args = []string{"custodia", "init-lab", "--dir", dir}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(dir, "lab.token"))
	if err != nil || len(key) != 64 {
		t.Fatal("token not generated")
	}
	for _, name := range []string{"lab.token", "state.db"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal("file is not private")
		}
	}
	if err := run(); err == nil {
		t.Fatal("existing lab overwritten")
	}
	for _, addr := range []string{"0.0.0.0:8000", "[::]:8000", "localhost:8000", "192.0.2.1:8000"} {
		os.Args = []string{"custodia", "serve-lab", "--dir", dir, "--listen", addr}
		if err := run(); err == nil {
			t.Fatal("non-loopback listener accepted")
		}
	}
}

func TestScopedCredentialAdministration(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	base := t.TempDir()
	dir := filepath.Join(base, "lab")
	out := filepath.Join(base, "reader.token")
	command := func(args ...string) error { os.Args = append([]string{"custodia"}, args...); return run() }
	if err := command("init-lab", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if err := command("issue-lab", "--dir", dir, "--subject", "reader", "--connections", "missing", "--out", out); err == nil {
		t.Fatal("invalid scope accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed issue left a credential file")
	}
	if err := command("issue-lab", "--dir", dir, "--subject", "reader", "--connections", "acme-identity", "--out", out); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != 64 {
		t.Fatal("credential file not private")
	}
	if err := command("issue-lab", "--dir", dir, "--subject", "reader", "--connections", "acme-netskope", "--out", out); err == nil {
		t.Fatal("credential file overwritten")
	}
	if err := command("revoke-lab", "--dir", dir, "--subject", "reader"); err != nil {
		t.Fatal(err)
	}
	if err := command("migrate-lab", "--dir", dir); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyInstallationHasNoLaboratoryFallback(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	dir := filepath.Join(t.TempDir(), "installation")
	command := func(args ...string) error { os.Args = append([]string{"custodia"}, args...); return run() }
	if err := command("init", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lab.token")); !os.IsNotExist(err) {
		t.Fatal("real installation created a lab credential")
	}
	if err := command("serve", "--dir", dir); err == nil {
		t.Fatal("real mode started without gateway configuration")
	}
	if err := command("serve-lab", "--dir", dir); err == nil {
		t.Fatal("empty real installation started as a laboratory")
	}
	if err := command("serve-lab", "--dir", dir, "--gateway-config", "not-used.json"); err == nil {
		t.Fatal("mixed laboratory and gateway configuration accepted")
	}
	if err := command("bootstrap-admin", "--dir", dir); err == nil {
		t.Fatal("bootstrap accepted missing issuer configuration")
	}
	for _, addr := range []string{"0.0.0.0:8000", "[::]:8000", "localhost:8000"} {
		if err := command("serve", "--dir", dir, "--listen", addr); err == nil {
			t.Fatal("real mode accepted non-numeric-loopback listener")
		}
	}
}
