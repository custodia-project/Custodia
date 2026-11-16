package probe

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tlsCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func tlsPlan(t *testing.T, server *httptest.Server, ca string, endpoints ...string) Plan {
	t.Helper()
	t.Setenv("CUSTODIA_PROBE_TEST_TOKEN", "test-bearer-token")
	plan, err := NewPlan(Config{
		BaseURL: server.URL, CredentialRef: "env:CUSTODIA_PROBE_TEST_TOKEN",
		CAFile: ca, Endpoints: endpoints, Authorized: true, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRunPlanTLSFixtureSanitized(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-bearer-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/API/Safes":
			_, _ = w.Write([]byte(`{"value":[{"secret":"provider-body"},{"x":2}],"count":2}`))
		case "/API/Platforms":
			_, _ = w.Write([]byte(`{"Platforms":[{"general":{"id":"A"}}],"Total":1}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	plan := tlsPlan(t, server, tlsCA(t, server), "/API/Safes", "/API/Platforms")
	report, err := RunPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Endpoints) != 2 {
		t.Fatal(report)
	}
	for _, result := range report.Endpoints {
		if result.Status != 200 || result.Error != "" {
			t.Fatal(result)
		}
	}
	if report.Endpoints[0].Objects != 2 || report.Endpoints[1].Objects != 1 {
		t.Fatal(report)
	}
	encoded, err := report.SanitizedJSON()
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "test-bearer-token") || strings.Contains(text, "provider-body") {
		t.Fatal("sanitized report leaked a credential or body", text)
	}
	if !strings.Contains(text, `"credential_ref":"env:CUSTODIA_PROBE_TEST_TOKEN"`) {
		t.Fatal("credential reference missing from report", text)
	}
}

func TestRunPlanRejectsRedirectAndRecordsStatus(t *testing.T) {
	var followed atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/API/Safes":
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(http.StatusFound)
		case "/elsewhere":
			followed.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	plan := tlsPlan(t, server, tlsCA(t, server), "/API/Safes", "/API/PSM/Servers")
	report, err := RunPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Endpoints[0].Status != http.StatusFound || report.Endpoints[0].Error != "redirect_not_followed" {
		t.Fatal(report.Endpoints[0])
	}
	if report.Endpoints[1].Status != http.StatusUnauthorized || report.Endpoints[1].Error != "authentication_failed" {
		t.Fatal(report.Endpoints[1])
	}
	if followed.Load() != 0 {
		t.Fatal("probe followed a redirect")
	}
}

func TestRunPlanDistinguishesPermissionAndRateLimit(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	plan := tlsPlan(t, server, tlsCA(t, server), "/API/Safes", "/API/Accounts")
	report, err := RunPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Endpoints[0].Error != "permission_denied" || report.Endpoints[1].Error != "rate_limited" {
		t.Fatal(report)
	}
}

func TestRunPlanRejectsOversizedAndMalformedBodies(t *testing.T) {
	for name, tc := range map[string]struct {
		contentType string
		body        string
		code        string
	}{
		"too-large":        {"application/json", strings.Repeat("x", maxBodyBytes+1), "body_too_large"},
		"wrong-type":       {"text/plain", `{"value":[]}`, "invalid_content_type"},
		"invalid-json":     {"application/json", `{"value":`, "invalid_json"},
		"missing-array":    {"application/json", `{"count":0}`, "invalid_payload"},
		"array-wrong-type": {"application/json", `{"value":{}}`, "invalid_payload"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			plan := tlsPlan(t, server, tlsCA(t, server), "/API/Safes")
			report, err := RunPlan(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			if report.Endpoints[0].Error != tc.code {
				t.Fatal(report.Endpoints[0])
			}
		})
	}
}

func TestRunPlanWithoutTrustedCAReportsTransportError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	defer server.Close()
	plan := tlsPlan(t, server, "", "/API/Safes")
	report, err := RunPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Endpoints[0].Error != "transport_error" {
		t.Fatal(report.Endpoints[0])
	}
}

func TestRunPlanRequiresValidatedPlan(t *testing.T) {
	if _, err := RunPlan(context.Background(), Plan{BaseURL: "https://example.test", Endpoints: []string{"/API/Safes"}}); err == nil {
		t.Fatal("unvalidated plan was executed")
	}
}

func TestResolveCredentialReferences(t *testing.T) {
	t.Setenv("CUSTODIA_PROBE_ENV_TOKEN", "  env-token-value\n")
	value, err := ResolveCredential("env:CUSTODIA_PROBE_ENV_TOKEN")
	if err != nil || value != "env-token-value" {
		t.Fatal(value, err)
	}
	path := filepath.Join(t.TempDir(), "token")
	if err = os.WriteFile(path, []byte("file-token-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err = ResolveCredential("file:" + path)
	if err != nil || value != "file-token-value" {
		t.Fatal(value, err)
	}
	for _, reference := range []string{
		"env:CUSTODIA_PROBE_MISSING_TOKEN",
		"env:",
		"file:relative-token",
		"secret:tenant-token",
		"literal-secret",
	} {
		if _, err := ResolveCredential(reference); err == nil {
			t.Fatal("unsafe credential reference accepted", reference)
		}
	}
	t.Setenv("CUSTODIA_PROBE_EMPTY_TOKEN", "  ")
	if _, err := ResolveCredential("env:CUSTODIA_PROBE_EMPTY_TOKEN"); err == nil {
		t.Fatal("empty credential accepted")
	}
	t.Setenv("CUSTODIA_PROBE_CONTROL_TOKEN", "token\nvalue")
	if _, err := ResolveCredential("env:CUSTODIA_PROBE_CONTROL_TOKEN"); err == nil {
		t.Fatal("control character credential accepted")
	}
}

func TestResolveCredentialRejectsUnreadableAndOversizedFiles(t *testing.T) {
	if _, err := ResolveCredential("file:" + filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing credential file accepted")
	}
	path := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxCredentialBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCredential("file:" + path); err == nil {
		t.Fatal("oversized credential file accepted")
	}
}

func TestReportJSONIsPlainObject(t *testing.T) {
	report := Report{BaseURL: "https://pam.example", CredentialRef: "env:X", Endpoints: []EndpointResult{{Endpoint: "/API/Safes", Status: 200, Objects: 1}}}
	encoded, err := report.SanitizedJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["base_url"] != "https://pam.example" || decoded["credential_ref"] != "env:X" {
		t.Fatal(decoded)
	}
}
