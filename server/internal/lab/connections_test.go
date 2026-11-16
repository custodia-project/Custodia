package lab

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pamFixtureHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/API/Safes" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value":    []map[string]any{pamSafe(9001, "Alpha", "alpha"), pamSafe(9002, "Beta", "beta")},
			"count":    2,
			"nextLink": "",
		})
	})
}

func tenantCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tenant-ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func adminStore(t *testing.T, now time.Time) (*Store, Principal) {
	t.Helper()
	s, p := newSyncStore(t, now)
	if _, err := s.DB.Exec("INSERT INTO gateway_administrators(principal_id) VALUES(?)", p.ID); err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestConnectorCatalogSeparatesImplementedAndPlanned(t *testing.T) {
	s, _ := adminStore(t, time.Now().UTC())
	h := s.Handler("lab-key", time.Now)
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connectors", "")
	if code != 200 {
		t.Fatal(code, body)
	}
	for _, want := range []string{`"id":"cyberark-pam-safes-v1"`, `"status":"available"`, `"id":"qradar"`, `"status":"planned"`} {
		if !strings.Contains(body, want) {
			t.Fatal("catalog missing", want)
		}
	}
	// A planned tool exposes no endpoints or auth claim and cannot be created.
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections",
		`{"connector":"qradar","base_url":"https://siem.acme.example","collection":"events","scope":"s1"}`)
	if code != 400 || !strings.Contains(body, "planned") {
		t.Fatal(code, body)
	}
}

func TestTenantOnboardingProbeAndCollection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s, p := adminStore(t, now)
	server := httptest.NewTLSServer(pamFixtureHandler(t))
	defer server.Close()
	t.Setenv("CUSTODIA_TEST_TENANT_TOKEN", "tenant-token")
	ca := tenantCA(t, server)
	h := s.Handler("lab-key", func() time.Time { return now })

	payload, _ := json.Marshal(map[string]any{
		"connector": "cyberark-pam-safes-v1", "base_url": server.URL,
		"collection": "safes", "scope": "tenant-safes-v1",
		"auth_mode": "bearer", "credential_ref": "env:CUSTODIA_TEST_TENANT_TOKEN",
		"ca_file": ca, "page_size": 2,
	})
	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections", string(payload))
	if code != 201 {
		t.Fatal(code, body)
	}
	if strings.Contains(body, "CUSTODIA_TEST_TENANT_TOKEN") || strings.Contains(body, "tenant-token") {
		t.Fatal("registration echoed a credential reference or value", body)
	}
	var registered struct {
		ConnectionID string `json:"connection_id"`
		AuthMode     string `json:"auth_mode"`
		Credential   bool   `json:"credential_configured"`
		CA           bool   `json:"ca_configured"`
	}
	if err := json.Unmarshal([]byte(body), &registered); err != nil {
		t.Fatal(err, body)
	}
	if registered.ConnectionID == "" || registered.AuthMode != "bearer" || !registered.Credential || !registered.CA {
		t.Fatal(registered)
	}

	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections/"+registered.ConnectionID+"/probe", "")
	if code != 200 || !strings.Contains(body, `"failed_endpoints":0`) || !strings.Contains(body, `"objects":2`) {
		t.Fatal(code, body)
	}
	if strings.Contains(body, "tenant-token") {
		t.Fatal("probe report leaked the credential", body)
	}
	var audit int
	if err := s.DB.QueryRow("SELECT count(*) FROM lab_audit WHERE action='connection_probe'").Scan(&audit); err != nil || audit != 1 {
		t.Fatal(err, audit)
	}

	// The registered connection is collectable through the normal worker.
	runID, err := s.EnqueueSyncRun(context.Background(), p, registered.ConnectionID, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessNextSyncRun(context.Background(), "tenant-worker", func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	run, err := s.SyncRun(context.Background(), p, runID)
	if err != nil || run.Status != "completed" || run.Received != 2 {
		t.Fatal(err, run)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/"+registered.ConnectionID+"/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 2 || !strings.Contains(body, `"safe_url_id":"alpha"`) {
		t.Fatal("tenant records", code, body)
	}
}

func TestTenantOnboardingRejectsUnsafeConfiguration(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s, p := adminStore(t, now)
	h := s.Handler("lab-key", func() time.Time { return now })
	base := map[string]any{
		"connector": "cyberark-pam-safes-v1", "collection": "safes", "scope": "s1",
		"auth_mode": "bearer", "credential_ref": "env:TENANT_TOKEN",
	}
	for name, override := range map[string]map[string]any{
		"http-tenant":        {"base_url": "http://pam.acme.example"},
		"loopback-http":      {"base_url": "http://127.0.0.1:18080"},
		"no-credential":      {"base_url": "https://pam.acme.example", "credential_ref": ""},
		"literal-secret":     {"base_url": "https://pam.acme.example", "credential_ref": "secret-value"},
		"unknown-collection": {"base_url": "https://pam.acme.example", "collection": "events"},
		"unknown-auth":       {"base_url": "https://pam.acme.example", "auth_mode": "basic"},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{}
			for key, value := range base {
				body[key] = value
			}
			for key, value := range override {
				body[key] = value
			}
			encoded, _ := json.Marshal(body)
			code, response := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections", string(encoded))
			if code != 400 {
				t.Fatal(code, response)
			}
			if strings.Contains(response, "secret-value") {
				t.Fatal("validation echoed a literal credential", response)
			}
		})
	}
	// A non-administrator cannot register or probe.
	if err := s.IssuePrincipal(context.Background(), "reader", "reader-key", nil); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]any{
		"connector": "cyberark-pam-safes-v1", "base_url": "https://pam.acme.example",
		"collection": "safes", "scope": "s1", "auth_mode": "bearer", "credential_ref": "env:TENANT_TOKEN",
	})
	if code, body := syncRequest(t, h, "reader-key", "POST", "/api/ingestion/connections", string(encoded)); code != 403 {
		t.Fatal("non-administrator registered a connection", code, body)
	}
	_ = p
}

func TestTenantProbeFailsWhenCredentialSourceIsAbsent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s, _ := adminStore(t, now)
	server := httptest.NewTLSServer(pamFixtureHandler(t))
	defer server.Close()
	h := s.Handler("lab-key", func() time.Time { return now })
	payload, _ := json.Marshal(map[string]any{
		"connector": "cyberark-pam-safes-v1", "base_url": server.URL,
		"collection": "safes", "scope": "s1",
		"auth_mode": "bearer", "credential_ref": "env:CUSTODIA_TEST_MISSING_TOKEN",
		"ca_file": tenantCA(t, server),
	})
	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections", string(payload))
	if code != 201 {
		t.Fatal("registration must not contact or resolve the tenant", code, body)
	}
	var registered struct {
		ConnectionID string `json:"connection_id"`
	}
	_ = json.Unmarshal([]byte(body), &registered)
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/connections/"+registered.ConnectionID+"/probe", "")
	if code != 400 || !strings.Contains(body, "credential") {
		t.Fatal("probe accepted a missing credential source", code, body)
	}
}
