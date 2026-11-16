package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildProbeBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "custodia")
	command := exec.Command("go", "build", "-o", binary, ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build custodia: %v\n%s", err, output)
	}
	return binary
}

func runProbeBinary(t *testing.T, binary, token string, args ...string) (string, int) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Env = append(os.Environ(), "CUSTODIA_PROBE_BINARY_TOKEN="+token)
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("run probe binary: %v\n%s", err, output)
	}
	return string(output), exit.ExitCode()
}

func TestProbePAMBinaryExitCodes(t *testing.T) {
	binary := buildProbeBinary(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer deny" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{}]}`))
	}))
	defer server.Close()
	ca := probeCA(t, server)
	args := []string{
		"probe-pam", "--authorized", "--url", server.URL,
		"--credential-ref", "env:CUSTODIA_PROBE_BINARY_TOKEN",
		"--ca-file", ca, "--endpoint", "/API/Safes",
	}
	output, code := runProbeBinary(t, binary, "allow", args...)
	if code != 0 || !strings.Contains(output, `"status":200`) {
		t.Fatal(code, output)
	}
	output, code = runProbeBinary(t, binary, "deny", args...)
	if code != 1 || !strings.Contains(output, `"status":403`) || !strings.Contains(output, `"error":"permission_denied"`) {
		t.Fatal(code, output)
	}
	planArgs := []string{"probe-pam", "--url", server.URL, "--credential-ref", "env:CUSTODIA_PROBE_BINARY_TOKEN", "--ca-file", ca, "--endpoint", "/API/Safes"}
	output, code = runProbeBinary(t, binary, "allow", planArgs...)
	if code != 2 || strings.Contains(output, `"endpoints"`) {
		t.Fatal(code, output)
	}
}
