package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	maxBodyBytes       = 1 << 20
	maxCredentialBytes = 8192
)

// Report is the sanitized result of one authorized probe. It never contains
// response bodies, headers, tokens or secret values.
type Report struct {
	BaseURL       string           `json:"base_url"`
	AuthMode      string           `json:"auth_mode"`
	CredentialRef string           `json:"credential_ref,omitempty"`
	Endpoints     []EndpointResult `json:"endpoints"`
}

// EndpointResult records only status, bounded counts and a stable error code.
type EndpointResult struct {
	Endpoint   string `json:"endpoint"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Objects    int    `json:"objects"`
	Error      string `json:"error,omitempty"`
}

// ResolveCredential resolves a reference outside the repository. The returned
// value is used only in memory for authentication and is never written to the
// report. secret: reads a regular file from CUSTODIA_PROBE_SECRET_DIR.
func ResolveCredential(reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	switch {
	case strings.HasPrefix(reference, "env:"):
		name := strings.TrimPrefix(reference, "env:")
		if !validReferenceName(name) {
			return "", errors.New("probe environment reference is invalid")
		}
		return credentialValue(os.Getenv(name))
	case strings.HasPrefix(reference, "file:"):
		path := strings.TrimPrefix(reference, "file:")
		if path == "" || !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
			return "", errors.New("probe credential file must be an absolute local path")
		}
		directory, err := probeSecretDirectory()
		if err != nil {
			return "", err
		}
		if !pathInside(directory, path) {
			return "", errors.New("probe credential file must be inside CUSTODIA_PROBE_SECRET_DIR")
		}
		return readSecretFile(path)
	case strings.HasPrefix(reference, "vault:"):
		return "", errors.New("vault references require the Custodia secret store")
	case strings.HasPrefix(reference, "secret:"):
		name := strings.TrimPrefix(reference, "secret:")
		if !validReferenceName(name) {
			return "", errors.New("probe secret reference is invalid")
		}
		directory, err := probeSecretDirectory()
		if err != nil {
			return "", err
		}
		return readSecretFile(filepath.Join(directory, name))
	default:
		return "", errors.New("probe credential reference is not supported")
	}
}

func probeSecretDirectory() (string, error) {
	directory := strings.TrimSpace(os.Getenv("CUSTODIA_PROBE_SECRET_DIR"))
	if directory == "" || !filepath.IsAbs(directory) || strings.IndexFunc(directory, unicode.IsControl) >= 0 {
		return "", errors.New("probe secret provider is not configured")
	}
	return filepath.Clean(directory), nil
}

func pathInside(directory, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(directory), filepath.Clean(path))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func readSecretFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCredentialBytes {
		return "", errors.New("probe credential source is unavailable or oversized")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("probe credential source could not be read")
	}
	return credentialValue(string(content))
}

func validReferenceName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		if !(r == '_' || r == '-' || r == '.' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func credentialValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxCredentialBytes {
		return "", errors.New("probe credential is empty or oversized")
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("probe credential contains control characters")
	}
	return value, nil
}

// RunPlan resolves authentication outside the repository and probes every
// allowlisted endpoint sequentially. Transport failures are reported per
// endpoint; plan, secret, CA or authentication errors fail before a report.
// Resolver resolves a credential reference to a value. The default resolver
// reads env:, file: and secret: sources; Custodia passes its encrypted store
// for vault: references.
type Resolver func(reference string) (string, error)

// Prepare resolves authentication outside the repository and builds a
// read-only HTTP client for one validated plan. The client injects the bearer
// token itself, so a sync adapter reusing it cannot forget authorization.
func Prepare(ctx context.Context, plan Plan) (*http.Client, error) {
	return PrepareWithResolver(ctx, plan, nil)
}

// PrepareWithResolver is Prepare with an explicit credential resolver. The
// resolver runs in memory and its values are never stored in the plan.
func PrepareWithResolver(ctx context.Context, plan Plan, resolver Resolver) (*http.Client, error) {
	if !plan.validated {
		return nil, errors.New("probe plan is not validated")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	roots, err := loadRootCAs(plan.CAFile)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	if plan.AuthMode == AuthMTLS {
		certPEM, err := readTLSFile(plan.ClientCertFile, "probe client certificate")
		if err != nil {
			return nil, err
		}
		keyPEM, err := readTLSFile(plan.ClientKeyFile, "probe client key")
		if err != nil {
			return nil, err
		}
		certificate, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, errors.New("probe client certificate could not be loaded")
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: plan.Timeout,
	}
	resolve := ResolveCredential
	if resolver != nil {
		resolve = resolver
	}
	authHeader := ""
	switch plan.AuthMode {
	case AuthBearer:
		token, err := resolve(plan.CredentialRef)
		if err != nil {
			return nil, err
		}
		authHeader = "Bearer " + token
	case AuthBasicExchange:
		password, err := resolve(plan.CredentialRef)
		if err != nil {
			return nil, err
		}
		token, err := fetchBasicExchangeToken(ctx, client, plan, plan.AuthUser, password)
		if err != nil {
			return nil, err
		}
		authHeader = "Bearer " + token
	case AuthOAuthClientCreds:
		clientID, err := resolve(plan.ClientIDRef)
		if err != nil {
			return nil, err
		}
		secret, err := resolve(plan.ClientSecretRef)
		if err != nil {
			return nil, err
		}
		token, err := fetchOAuthToken(ctx, client, plan, clientID, secret)
		if err != nil {
			return nil, err
		}
		authHeader = "Bearer " + token
	case AuthMTLS:
	default:
		return nil, errors.New("probe auth mode is not supported")
	}
	if authHeader != "" {
		client.Transport = bearerTransport{base: client.Transport, header: authHeader}
	}
	return client, nil
}

// bearerTransport attaches the authorization header to every read-only
// request. The token stays in memory only.
type bearerTransport struct {
	base   http.RoundTripper
	header string
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", t.header)
	return t.base.RoundTrip(clone)
}

// RunPlan resolves authentication outside the repository and probes every
// allowlisted endpoint sequentially. Transport failures are reported per
// endpoint; plan, secret, CA or authentication errors fail before a report.
func RunPlan(ctx context.Context, plan Plan) (Report, error) {
	return RunPlanWithResolver(ctx, plan, nil)
}

// RunPlanWithResolver probes every endpoint with the given credential
// resolver. It is the same read-only plan and transport as RunPlan.
func RunPlanWithResolver(ctx context.Context, plan Plan, resolver Resolver) (Report, error) {
	client, err := PrepareWithResolver(ctx, plan, resolver)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		BaseURL: plan.BaseURL, AuthMode: string(plan.AuthMode), CredentialRef: plan.CredentialRef,
		Endpoints: make([]EndpointResult, 0, len(plan.Endpoints)),
	}
	base := strings.TrimRight(plan.BaseURL, "/")
	for _, endpoint := range plan.Endpoints {
		report.Endpoints = append(report.Endpoints, probeEndpoint(ctx, client, base, endpoint, plan.Timeout))
	}
	return report, nil
}

func fetchBasicExchangeToken(ctx context.Context, client *http.Client, plan Plan, user, password string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(plan.BaseURL, "/")+"/security/user/authenticate", nil)
	if err != nil {
		return "", errors.New("basic exchange request is invalid")
	}
	request.SetBasicAuth(user, password)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("basic exchange login failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("basic exchange login returned %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return "", errors.New("basic exchange response could not be read")
	}
	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&payload); err != nil || payload.Data.Token == "" || len(payload.Data.Token) > maxCredentialBytes {
		return "", errors.New("basic exchange response did not contain a token")
	}
	return payload.Data.Token, nil
}

func fetchOAuthToken(ctx context.Context, client *http.Client, plan Plan, clientID, secret string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientID)
	form.Set("client_secret", secret)
	if len(plan.Scopes) > 0 {
		form.Set("scope", strings.Join(plan.Scopes, " "))
	}
	tokenCtx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(tokenCtx, http.MethodPost, plan.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("probe OAuth token request could not be built")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "custodia-probe/1")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("probe OAuth token request failed: %s", transportCode(tokenCtx, err))
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return "", errors.New("probe OAuth token endpoint redirects are not followed")
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("probe OAuth token endpoint returned HTTP %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return "", errors.New("probe OAuth token response must be application/json")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return "", errors.New("probe OAuth token response could not be read")
	}
	if len(body) > maxBodyBytes {
		return "", errors.New("probe OAuth token response exceeds its bound")
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", errors.New("probe OAuth token response is not valid JSON")
	}
	token, err := credentialValue(payload.AccessToken)
	if err != nil {
		return "", errors.New("probe OAuth token response has no usable access_token")
	}
	tokenType := strings.TrimSpace(payload.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	if strings.IndexFunc(tokenType, unicode.IsControl) >= 0 || len(tokenType) > 32 {
		return "", errors.New("probe OAuth token type is invalid")
	}
	if !strings.EqualFold(tokenType, "Bearer") {
		return "", errors.New("probe OAuth token type is not supported")
	}
	return token, nil
}

const maxTLSFileBytes = 1 << 20

func readTLSFile(path, description string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxTLSFileBytes {
		return nil, errors.New(description + " must be a small regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New(description + " could not be read")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, errors.New(description + " changed while reading")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxTLSFileBytes+1))
	if err != nil || len(content) > maxTLSFileBytes {
		return nil, errors.New(description + " is oversized")
	}
	return content, nil
}

func loadRootCAs(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	content, err := readTLSFile(path, "probe CA file")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, errors.New("probe CA file contains no certificate")
	}
	return pool, nil
}

func probeEndpoint(ctx context.Context, client *http.Client, base, endpoint string, timeout time.Duration) EndpointResult {
	result := EndpointResult{Endpoint: endpoint}
	start := time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base+endpoint, nil)
	if err != nil {
		result.Error = "request_failed"
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "custodia-probe/1")
	response, err := client.Do(request)
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = transportCode(requestCtx, err)
		return result
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		result.Error = "redirect_not_followed"
		return result
	}
	if response.StatusCode != http.StatusOK {
		result.Error = statusCode(response.StatusCode)
		return result
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		result.Error = "invalid_content_type"
		return result
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		result.Error = "response_read_failed"
		return result
	}
	if len(body) > maxBodyBytes {
		result.Error = "body_too_large"
		return result
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Error = "invalid_json"
		return result
	}
	key := countKey(endpoint)
	raw, found := payload[key]
	if !found || len(raw) == 0 || string(raw) == "null" {
		// Wazuh wraps read-only collections in data.affected_items and reports
		// the collection total. The probe accepts only that shape as a
		// fallback; provider-specific adapters still parse the full contract.
		var wazuh struct {
			Affected []json.RawMessage `json:"affected_items"`
			Total    int               `json:"total_affected_items"`
		}
		if data, ok := payload["data"]; ok && json.Unmarshal(data, &wazuh) == nil && wazuh.Affected != nil {
			result.Objects = len(wazuh.Affected)
			if wazuh.Total > result.Objects {
				result.Objects = wazuh.Total
			}
			return result
		}
		result.Error = "invalid_payload"
		return result
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		result.Error = "invalid_payload"
		return result
	}
	result.Objects = len(values)
	return result
}

func countKey(endpoint string) string {
	switch endpoint {
	case "/API/Platforms":
		return "Platforms"
	case "/API/PSM/Servers":
		return "PSMServers"
	case "/API/PSM/Connectors":
		return "PSMConnectors"
	default:
		return "value"
	}
}

func statusCode(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_failed"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		return fmt.Sprintf("http_status_%d", status)
	}
}

func transportCode(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "transport_error"
}

// Failed returns the number of endpoints that did not complete with HTTP 200
// and no stable error code.
func (report Report) Failed() int {
	failed := 0
	for _, result := range report.Endpoints {
		if result.Status != http.StatusOK || result.Error != "" {
			failed++
		}
	}
	return failed
}

// SanitizedJSON serializes the report. It is safe to log because the report
// contains only endpoint names, statuses, counts, durations and stable codes.
func (report Report) SanitizedJSON() ([]byte, error) {
	return json.Marshal(report)
}
