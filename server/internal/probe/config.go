// Package probe implements an explicitly authorized, read-only TLS probe.
//
// A probe plan only validates references and endpoints. Credential values are
// resolved outside the repository at run time and never persisted.
package probe

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	defaultTimeout = 15 * time.Second
	maxTimeout     = 2 * time.Minute
	maxCredential  = 256
)

// AuthMode selects how the probe authenticates to the tenant.
type AuthMode string

const (
	AuthBearer           AuthMode = "bearer"
	AuthOAuthClientCreds AuthMode = "oauth-client-credentials"
	AuthMTLS             AuthMode = "mtls"
)

// Endpoints are the only read-only PAM endpoints this probe may plan. Mutating
// endpoints, account secret material and session recordings are excluded.
var Endpoints = map[string]bool{
	"/API/Accounts":       true,
	"/API/Platforms":      true,
	"/API/PSM/Connectors": true,
	"/API/PSM/Servers":    true,
	"/API/Safes":          true,
}

// Config is the caller-supplied probe plan. All credential fields are
// references, never values. Authorized must be true and represents explicit
// owner authorization for this exact tenant and endpoint list.
type Config struct {
	BaseURL       string
	AuthMode      string
	CredentialRef string

	// OAuth client credentials.
	TokenURL        string
	ClientIDRef     string
	ClientSecretRef string
	Scopes          []string

	// mTLS client identity.
	ClientCertFile string
	ClientKeyFile  string

	CAFile     string
	Endpoints  []string
	Authorized bool
	Timeout    time.Duration
}

// Plan is a validated, redacted probe plan. Credential references are safe to
// log; credential values, certificate contents and private keys are not part
// of the plan.
type Plan struct {
	BaseURL         string
	AuthMode        AuthMode
	CredentialRef   string
	TokenURL        string
	ClientIDRef     string
	ClientSecretRef string
	Scopes          []string
	ClientCertFile  string
	ClientKeyFile   string
	CAFile          string
	Endpoints       []string
	Timeout         time.Duration

	validated bool
}

// NewPlan validates an authorized probe request without resolving credentials
// or contacting the tenant.
func NewPlan(config Config) (Plan, error) {
	if !config.Authorized {
		return Plan{}, errors.New("probe requires explicit owner authorization")
	}
	base, err := parseHTTPSOrigin(config.BaseURL, true)
	if err != nil {
		return Plan{}, err
	}
	mode, err := parseAuthMode(config.AuthMode)
	if err != nil {
		return Plan{}, err
	}
	if config.CAFile != "" && !validLocalPath(config.CAFile) {
		return Plan{}, errors.New("probe CA file must be an absolute local path")
	}
	plan := Plan{
		BaseURL:   base,
		AuthMode:  mode,
		CAFile:    config.CAFile,
		Endpoints: nil,
		Timeout:   normalizedTimeout(config.Timeout),
	}
	if plan.Timeout < time.Second || plan.Timeout > maxTimeout {
		return Plan{}, errors.New("probe timeout must be between 1s and 2m")
	}
	switch mode {
	case AuthBearer:
		if err = validReference(config.CredentialRef, "probe requires a bounded credential reference"); err != nil {
			return Plan{}, err
		}
		plan.CredentialRef = strings.TrimSpace(config.CredentialRef)
	case AuthOAuthClientCreds:
		tokenURL, err := parseHTTPSOrigin(config.TokenURL, true)
		if err != nil {
			return Plan{}, fmt.Errorf("probe token URL: %w", err)
		}
		if err = validReference(config.ClientIDRef, "probe requires a client ID reference"); err != nil {
			return Plan{}, err
		}
		if err = validReference(config.ClientSecretRef, "probe requires a client secret reference"); err != nil {
			return Plan{}, err
		}
		scopes, err := normalizedScopes(config.Scopes)
		if err != nil {
			return Plan{}, err
		}
		plan.TokenURL = tokenURL
		plan.ClientIDRef = strings.TrimSpace(config.ClientIDRef)
		plan.ClientSecretRef = strings.TrimSpace(config.ClientSecretRef)
		plan.Scopes = scopes
	case AuthMTLS:
		if !validLocalPath(config.ClientCertFile) || !validLocalPath(config.ClientKeyFile) {
			return Plan{}, errors.New("probe mTLS requires absolute client certificate and key paths")
		}
		plan.ClientCertFile = config.ClientCertFile
		plan.ClientKeyFile = config.ClientKeyFile
	}
	if len(config.Endpoints) == 0 {
		return Plan{}, errors.New("probe requires at least one read-only endpoint")
	}
	seen := map[string]bool{}
	endpoints := make([]string, 0, len(config.Endpoints))
	for _, endpoint := range config.Endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if !Endpoints[endpoint] {
			return Plan{}, fmt.Errorf("probe endpoint is not allowlisted: %s", endpoint)
		}
		if seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		endpoints = append(endpoints, endpoint)
	}
	plan.Endpoints = endpoints
	plan.validated = true
	return plan, nil
}

func parseAuthMode(raw string) (AuthMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "bearer":
		return AuthBearer, nil
	case "oauth", "oauth-client-credentials", "oauth_client_credentials", "client-credentials":
		return AuthOAuthClientCreds, nil
	case "mtls", "m-tls", "mutual-tls":
		return AuthMTLS, nil
	default:
		return "", errors.New("probe auth mode must be bearer, oauth-client-credentials or mtls")
	}
}

func parseHTTPSOrigin(raw string, allowPath bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("URL is invalid")
	}
	if parsed.Scheme != "https" {
		return "", errors.New("URL must use https")
	}
	if parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("URL must be an absolute https URL without user information")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("URL must not include a query or fragment")
	}
	if !allowPath && parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("URL must not include a path prefix")
	}
	result := parsed.Scheme + "://" + parsed.Host
	if allowPath && parsed.Path != "" {
		result += parsed.Path
	}
	return result, nil
}

func normalizedTimeout(timeout time.Duration) time.Duration {
	if timeout == 0 {
		return defaultTimeout
	}
	return timeout
}

func validLocalPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && strings.IndexFunc(path, unicode.IsControl) < 0
}

func validReference(reference, message string) error {
	reference = strings.TrimSpace(reference)
	if reference == "" || len(reference) > maxCredential || strings.IndexFunc(reference, unicode.IsControl) >= 0 {
		return errors.New(message)
	}
	if !referencePrefix(reference) {
		return errors.New("credential reference must use env:, file: or secret:")
	}
	return nil
}

func normalizedScopes(scopes []string) ([]string, error) {
	result := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if len(scope) > 256 || strings.IndexFunc(scope, unicode.IsControl) >= 0 || strings.ContainsAny(scope, " \t\r\n") {
			return nil, errors.New("probe OAuth scope is invalid")
		}
		if seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	return result, nil
}

func referencePrefix(reference string) bool {
	for _, prefix := range []string{"env:", "file:", "secret:"} {
		if strings.HasPrefix(reference, prefix) && len(reference) > len(prefix) {
			return true
		}
	}
	return false
}
