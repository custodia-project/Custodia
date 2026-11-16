package probe

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Custodia Probe Test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func testCert(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, commonName string, server bool) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

func writeTemp(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPlanOAuthClientCredentialsWithSecretReference(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("client_id") != "oauth-client" || r.PostForm.Get("client_secret") != "oauth-secret" || r.PostForm.Get("scope") != "pam.read" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"oauth-token","token_type":"Bearer","expires_in":3600}`))
		case "/API/Safes":
			if r.Header.Get("Authorization") != "Bearer oauth-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":[{"external_id":"safe-1"}]}`))
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
	plan, err := NewPlan(Config{
		BaseURL: server.URL, AuthMode: "oauth-client-credentials",
		TokenURL: server.URL + "/oauth2/token", ClientIDRef: "env:CUSTODIA_PROBE_OAUTH_ID", ClientSecretRef: "secret:oauth-secret",
		Scopes: []string{"pam.read"}, CAFile: tlsCA(t, server), Endpoints: []string{"/API/Safes"}, Authorized: true, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := RunPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Endpoints) != 1 || report.Endpoints[0].Status != 200 || report.Endpoints[0].Objects != 1 || report.AuthMode != "oauth-client-credentials" {
		t.Fatal(report)
	}
	encoded, err := report.SanitizedJSON()
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"oauth-secret", "oauth-token"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("OAuth secret leaked", forbidden, text)
		}
	}
}

func TestRunPlanOAuthTokenFailureCredentialNotLeaked(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	t.Setenv("CUSTODIA_PROBE_OAUTH_ID", "oauth-client")
	t.Setenv("CUSTODIA_PROBE_OAUTH_SECRET", "oauth-secret")
	plan, err := NewPlan(Config{
		BaseURL: server.URL, AuthMode: "oauth-client-credentials",
		TokenURL: server.URL + "/oauth2/token", ClientIDRef: "env:CUSTODIA_PROBE_OAUTH_ID", ClientSecretRef: "env:CUSTODIA_PROBE_OAUTH_SECRET",
		CAFile: tlsCA(t, server), Endpoints: []string{"/API/Safes"}, Authorized: true, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunPlan(context.Background(), plan)
	if err == nil || strings.Contains(err.Error(), "oauth-secret") {
		t.Fatal(err)
	}
}

func TestRunPlanMTLSFixture(t *testing.T) {
	ca, caKey, caPEM := testCA(t)
	serverCertPEM, serverKeyPEM := testCert(t, ca, caKey, "probe-server", true)
	clientCertPEM, clientKeyPEM := testCert(t, ca, caKey, "probe-client", false)
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientPool := x509.NewCertPool()
	clientPool.AppendCertsFromPEM(caPEM)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "probe-client" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{}]}`))
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientPool,
	}
	server.StartTLS()
	defer server.Close()
	plan, err := NewPlan(Config{
		BaseURL: server.URL, AuthMode: "mtls",
		ClientCertFile: writeTemp(t, "client.pem", clientCertPEM),
		ClientKeyFile:  writeTemp(t, "client.key", clientKeyPEM),
		CAFile:         writeTemp(t, "ca.pem", caPEM),
		Endpoints:      []string{"/API/Safes"}, Authorized: true, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := RunPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Endpoints) != 1 || report.Endpoints[0].Status != 200 || report.AuthMode != "mtls" {
		t.Fatal(report)
	}
}

func TestResolveCredentialSecretDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("CUSTODIA_PROBE_SECRET_DIR", directory)
	if err := os.WriteFile(filepath.Join(directory, "bearer"), []byte("secret-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := ResolveCredential("secret:bearer")
	if err != nil || value != "secret-value" {
		t.Fatal(value, err)
	}
	for _, reference := range []string{"secret:../escape", "secret:", "secret:missing"} {
		if _, err := ResolveCredential(reference); err == nil {
			t.Fatal("invalid secret reference accepted", reference)
		}
	}
}

func TestNewPlanValidatesAuthModes(t *testing.T) {
	for name, config := range map[string]Config{
		"oauth-no-token":   {BaseURL: "https://pam.example", AuthMode: "oauth-client-credentials", ClientIDRef: "env:ID", ClientSecretRef: "env:SECRET", Endpoints: []string{"/API/Safes"}, Authorized: true},
		"oauth-http-token": {BaseURL: "https://pam.example", AuthMode: "oauth-client-credentials", TokenURL: "http://pam.example/token", ClientIDRef: "env:ID", ClientSecretRef: "env:SECRET", Endpoints: []string{"/API/Safes"}, Authorized: true},
		"oauth-no-client":  {BaseURL: "https://pam.example", AuthMode: "oauth-client-credentials", TokenURL: "https://pam.example/token", ClientSecretRef: "env:SECRET", Endpoints: []string{"/API/Safes"}, Authorized: true},
		"mtls-relative":    {BaseURL: "https://pam.example", AuthMode: "mtls", ClientCertFile: "client.pem", ClientKeyFile: "client.key", Endpoints: []string{"/API/Safes"}, Authorized: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPlan(config); err == nil {
				t.Fatal("invalid auth plan accepted")
			}
		})
	}
	plan, err := NewPlan(Config{BaseURL: "https://pam.example", AuthMode: "mtls", ClientCertFile: "/tmp/client.pem", ClientKeyFile: "/tmp/client.key", Endpoints: []string{"/API/Safes"}, Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.AuthMode != AuthMTLS {
		t.Fatal(plan.AuthMode)
	}
	encoded, _ := json.Marshal(plan)
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("plan leaked key material")
	}
}
