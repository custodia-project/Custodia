package main

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func probeCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runProbeCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	oldArgs, oldStdout := os.Args, os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"custodia"}, args...)
	os.Stdout = writer
	defer func() {
		os.Args, os.Stdout = oldArgs, oldStdout
	}()
	runErr := run()
	writer.Close()
	output, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(output), runErr
}

func TestProbePAMCLIEnvCredentialTLSFixture(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cli-env-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/API/Safes":
			_, _ = w.Write([]byte(`{"value":[{},{}]}`))
		case "/API/Platforms":
			_, _ = w.Write([]byte(`{"Platforms":[{}],"Total":1}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("CUSTODIA_PROBE_CLI_TOKEN", "cli-env-token")
	output, err := runProbeCLI(t,
		"probe-pam", "--authorized", "--url", server.URL,
		"--credential-ref", "env:CUSTODIA_PROBE_CLI_TOKEN",
		"--ca-file", probeCA(t, server),
		"--endpoint", "/API/Safes", "--endpoint", "/API/Platforms",
		"--timeout", "5s",
	)
	if err != nil {
		t.Fatal(err)
	}
	if exitCode(err) != 0 {
		t.Fatal(exitCode(err))
	}
	for _, want := range []string{`"endpoint":"/API/Safes"`, `"endpoint":"/API/Platforms"`, `"status":200`, `"objects":2`, `"objects":1`} {
		if !strings.Contains(output, want) {
			t.Fatal("missing probe output", want, output)
		}
	}
	if strings.Contains(output, "cli-env-token") {
		t.Fatal("probe CLI leaked the credential", output)
	}
}

func TestProbePAMCLIFileCredentialTLSFixture(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cli-file-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("cli-file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := runProbeCLI(t,
		"probe-pam", "--authorized", "--url", server.URL,
		"--credential-ref", "file:"+tokenFile,
		"--ca-file", probeCA(t, server),
		"--endpoint", "/API/Safes",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"status":200`) || strings.Contains(output, "cli-file-token") {
		t.Fatal(output)
	}
}

func TestProbePAMCLIEndpointFailureExitCode(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	t.Setenv("CUSTODIA_PROBE_CLI_TOKEN", "token")
	output, err := runProbeCLI(t,
		"probe-pam", "--authorized", "--url", server.URL,
		"--credential-ref", "env:CUSTODIA_PROBE_CLI_TOKEN",
		"--ca-file", probeCA(t, server),
		"--endpoint", "/API/Safes",
	)
	if err == nil {
		t.Fatal("endpoint failure returned success")
	}
	if exitCode(err) != 1 {
		t.Fatal(exitCode(err))
	}
	if !strings.Contains(output, `"status":403`) || !strings.Contains(output, `"error":"permission_denied"`) {
		t.Fatal(output)
	}
}

func TestProbePAMCLIOAuthSecretReference(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.PostForm.Get("client_id") != "oauth-client" || r.PostForm.Get("client_secret") != "oauth-secret" || r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("scope") != "pam.read" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"oauth-token","token_type":"Bearer"}`))
		case "/API/Safes":
			if r.Header.Get("Authorization") != "Bearer oauth-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":[{}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("CUSTODIA_PROBE_OAUTH_ID", "oauth-client")
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "oauth-secret"), []byte("oauth-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CUSTODIA_PROBE_SECRET_DIR", secretDir)
	output, err := runProbeCLI(t,
		"probe-pam", "--authorized", "--auth", "oauth-client-credentials",
		"--url", server.URL, "--token-url", server.URL+"/oauth2/token",
		"--client-id-ref", "env:CUSTODIA_PROBE_OAUTH_ID",
		"--client-secret-ref", "secret:oauth-secret", "--oauth-scope", "pam.read",
		"--ca-file", probeCA(t, server), "--endpoint", "/API/Safes",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"auth_mode":"oauth-client-credentials"`) || !strings.Contains(output, `"status":200`) {
		t.Fatal(output)
	}
	for _, forbidden := range []string{"oauth-secret", "oauth-token"} {
		if strings.Contains(output, forbidden) {
			t.Fatal("OAuth secret leaked through CLI", forbidden, output)
		}
	}
}

func TestProbePAMCLIPlanErrorExitCode(t *testing.T) {
	for name, args := range map[string][]string{
		"unauthorized": {"probe-pam", "--url", "https://pam.example", "--credential-ref", "env:TOKEN", "--endpoint", "/API/Safes"},
		"http-url":     {"probe-pam", "--authorized", "--url", "http://pam.example", "--credential-ref", "env:TOKEN", "--endpoint", "/API/Safes"},
		"no-endpoint":  {"probe-pam", "--authorized", "--url", "https://pam.example", "--credential-ref", "env:TOKEN"},
	} {
		t.Run(name, func(t *testing.T) {
			output, err := runProbeCLI(t, args...)
			if err == nil {
				t.Fatal("invalid probe plan returned success")
			}
			if exitCode(err) != 2 {
				t.Fatal(exitCode(err), err)
			}
			if output != "" {
				t.Fatal("invalid plan produced report output", output)
			}
		})
	}
}
