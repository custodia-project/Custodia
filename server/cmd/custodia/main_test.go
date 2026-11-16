package main

import (
	"os"
	"path/filepath"
	"testing"

	"custodia/server/internal/lab"
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

func TestLaboratoryHTTPConfigurationIsExplicitAndLoopbackOnly(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	base := t.TempDir()
	dir := filepath.Join(base, "lab")
	command := func(args ...string) error { os.Args = append([]string{"custodia"}, args...); return run() }
	if err := command("init-lab", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	args := func(url, connection, collection, scope string) []string {
		return []string{"add-http-lab", "--dir", dir, "--url", url, "--connection", connection, "--collection", collection, "--scope", scope, "--grant", "synthetic-lab"}
	}
	if err := command(args("http://192.0.2.1:18080", "lab-http-a", "users", "scope-a")...); err == nil {
		t.Fatal("non-loopback source accepted")
	}
	if err := command(args("https://127.0.0.1:18080", "lab-http-a", "users", "scope-a")...); err == nil {
		t.Fatal("non-HTTP source accepted")
	}
	for _, raw := range []string{"http://user@127.0.0.1:18080", "http://127.0.0.1:18080?x=1", "http://127.0.0.1:99999", "http://127.0.0.1:18080#fragment"} {
		if err := command(args(raw, "lab-http-a", "users", "scope-a")...); err == nil {
			t.Fatal("invalid source URL accepted", raw)
		}
	}
	if err := command(args("http://127.0.0.1:18080", "lab-http-a", "secrets", "scope-a")...); err == nil {
		t.Fatal("unsupported collection accepted")
	}
	if err := command(args("http://127.0.0.1:18080", "lab-http-a", "users", "")...); err == nil {
		t.Fatal("missing scope accepted")
	}
	if err := command(args("http://127.0.0.1:18080", "lab-http-a", "users", "scope-a")...); err != nil {
		t.Fatal(err)
	}
	if err := command(args("http://127.0.0.1:18080", "lab-http-a", "users", "scope-a")...); err == nil {
		t.Fatal("duplicate source accepted")
	}
	out := filepath.Join(base, "reader.token")
	if err := command("issue-lab", "--dir", dir, "--subject", "sync-reader", "--connections", "lab-http-a", "--out", out); err != nil {
		t.Fatal(err)
	}
	if err := command("grant-sync-lab", "--dir", dir, "--connection", "lab-http-a", "--subject", "sync-reader", "--can-run=false"); err != nil {
		t.Fatal(err)
	}
	s, err := lab.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var canRun int
	if err = s.DB.QueryRow("SELECT r.can_run FROM lab_sync_grants r JOIN lab_principals p ON p.id=r.principal_id WHERE p.subject='sync-reader'").Scan(&canRun); err != nil || canRun != 0 {
		t.Fatal("read-only sync grant not stored", err, canRun)
	}
	if err := command("grant-sync-lab", "--dir", dir, "--connection", "lab-http-a", "--subject", "sync-reader"); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow("SELECT r.can_run FROM lab_sync_grants r JOIN lab_principals p ON p.id=r.principal_id WHERE p.subject='sync-reader'").Scan(&canRun); err != nil || canRun != 1 {
		t.Fatal("sync run grant not stored", err, canRun)
	}
	if err := command("add-http-lab", "--dir", dir, "--gateway-config", "not-used.json", "--url", "http://127.0.0.1:18080", "--connection", "lab-http-b", "--collection", "users", "--scope", "scope-b"); err == nil {
		t.Fatal("laboratory source mixed with gateway configuration")
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
