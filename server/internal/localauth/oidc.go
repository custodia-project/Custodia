package localauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"custodia/server/internal/lab"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcCallbackPath = "/api/auth/oidc/callback"
	oidcTestTimeout  = 10 * time.Second
	oidcBodyLimit    = 1 << 20
	oidcSecretLimit  = 8192
)

type oidcClient struct {
	store        *lab.Store
	publicOrigin string
	httpClient   *http.Client
}

// NewOIDCHandler serves the explicit OIDC mode. OIDC is the primary login;
// a host-created local administrator keeps an explicit password form, not an
// automatic fallback. Ozal assertions and identity headers are ignored.
func NewOIDCHandler(store *lab.Store, config Config, now func() time.Time, files http.Handler) (http.Handler, error) {
	config.Mode = "oidc"
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &handler{
		store: store, config: config, now: now, files: files,
		oidc: &oidcClient{store: store, publicOrigin: config.PublicOrigin, httpClient: &http.Client{Timeout: oidcTestTimeout}},
	}, nil
}

func (h *handler) oidcMode() bool { return h.config.mode() == "oidc" }

func (h *handler) oidcStatus(ctx context.Context) map[string]any {
	configured, enabled, err := h.store.OIDCConfigured(ctx)
	if err != nil {
		return map[string]any{"configured": false, "enabled": false, "login_url": ""}
	}
	return map[string]any{"configured": configured, "enabled": enabled, "login_url": "/api/auth/oidc/start"}
}

// resolveSession accepts both explicit credentials of OIDC mode: an OIDC
// session for an approved external identity and a local session for the
// break-glass administrator. They are separate records, never a fallback
// between providers.
func (h *handler) resolveSession(r *http.Request, token string, current time.Time) (lab.LocalSession, error) {
	session, err := h.store.ResolveLocalSession(r.Context(), TokenHash(token), IdleTimeout, current)
	if !h.oidcMode() || err != lab.ErrNoSession {
		return session, err
	}
	config, err := h.store.GetOIDCConfig(r.Context())
	if errors.Is(err, lab.ErrOIDCNotConfigured) {
		return session, lab.ErrNoSession
	}
	if err != nil {
		return session, err
	}
	return h.store.ResolveOIDCSession(r.Context(), TokenHash(token), IdleTimeout, current, config.Issuer)
}

func randomValue() (string, error) {
	return newToken()
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (h *handler) oidcStateCookieName() string {
	if strings.HasPrefix(h.config.PublicOrigin, "https://") {
		return "__Secure-custodia_oidc_state"
	}
	return "custodia_oidc_state"
}

func (h *handler) oidcStateCookie(value string) *http.Cookie {
	return &http.Cookie{Name: h.oidcStateCookieName(), Value: value, Path: "/api/auth/oidc", MaxAge: int(lab.OIDCStateLifetime / time.Second), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
}

func (h *handler) clearOIDCStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: h.oidcStateCookieName(), Value: "", Path: "/api/auth/oidc", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func (h *handler) oidcStateFromRequest(r *http.Request) string {
	value, count := "", 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == h.oidcStateCookieName() {
			count++
			value = cookie.Value
		}
	}
	if count != 1 {
		return ""
	}
	return value
}

func oidcErrorRedirect(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?auth_error="+url.QueryEscape(code), http.StatusSeeOther)
}

func (h *handler) oidcStart(w http.ResponseWriter, r *http.Request, current time.Time) {
	if r.Method != "GET" {
		detail(w, 405, "Usá GET para iniciar el acceso.")
		return
	}
	config, err := h.store.GetOIDCConfig(r.Context())
	if errors.Is(err, lab.ErrOIDCNotConfigured) || (err == nil && !config.Enabled) {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	if err = validateOIDCConfig(config, h.config.PublicOrigin); err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	oauthConfig, err := h.oauthConfig(config, provider)
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	state, err := randomValue()
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	nonce, err := randomValue()
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	verifier, err := randomValue()
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	if err = h.store.CreateOIDCState(r.Context(), TokenHash(state), nonce, verifier, current); err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	http.SetCookie(w, h.oidcStateCookie(state))
	authURL := oauthConfig.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge(verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *handler) oidcCallback(w http.ResponseWriter, r *http.Request, current time.Time) {
	if r.Method != "GET" {
		detail(w, 405, "Usá GET como callback del proveedor.")
		return
	}
	query := r.URL.Query()
	if query.Get("error") != "" {
		oidcErrorRedirect(w, r, "oidc_denied")
		return
	}
	code, state := query.Get("code"), query.Get("state")
	cookieState := h.oidcStateFromRequest(r)
	h.clearOIDCStateCookie(w)
	if code == "" || state == "" || cookieState == "" {
		oidcErrorRedirect(w, r, "oidc_state")
		return
	}
	stateHash := TokenHash(state)
	if subtle.ConstantTimeCompare([]byte(TokenHash(cookieState)), []byte(stateHash)) != 1 {
		oidcErrorRedirect(w, r, "oidc_state")
		return
	}
	nonce, verifier, err := h.store.ConsumeOIDCState(r.Context(), stateHash, current)
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_state")
		return
	}
	config, err := h.store.GetOIDCConfig(r.Context())
	if err != nil || !config.Enabled {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	oauthConfig, err := h.oauthConfig(config, provider)
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	token, err := oauthConfig.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_exchange")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		oidcErrorRedirect(w, r, "oidc_exchange")
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: config.ClientID}).Verify(ctx, rawIDToken)
	if err != nil || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		oidcErrorRedirect(w, r, "oidc_token")
		return
	}
	claims := map[string]any{}
	_ = idToken.Claims(&claims)
	subject := idToken.Subject
	if config.SubjectClaim != "" && config.SubjectClaim != "sub" {
		subject, _ = claims[config.SubjectClaim].(string)
	}
	email, _ := claims[config.EmailClaim].(string)
	name, _ := claims[config.NameClaim].(string)
	if strings.TrimSpace(subject) == "" || len(subject) > 512 {
		oidcErrorRedirect(w, r, "oidc_token")
		return
	}
	principal, account, err := h.store.OIDCIdentity(r.Context(), config.Issuer, subject)
	if errors.Is(err, lab.ErrOIDCUnknown) {
		if pendingErr := h.store.RecordOIDCPending(r.Context(), config.Issuer, subject, email, name, current); pendingErr != nil {
			oidcErrorRedirect(w, r, "oidc_pending_full")
			return
		}
		oidcErrorRedirect(w, r, "oidc_pending")
		return
	}
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	if !account.Enabled {
		oidcErrorRedirect(w, r, "oidc_disabled")
		return
	}
	sessionToken, err := newToken()
	if err != nil {
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	if err = h.store.StartOIDCSession(r.Context(), principal, config.Issuer, TokenHash(sessionToken), current, current.Add(AbsoluteLifetime)); err != nil {
		if errors.Is(err, lab.ErrDenied) {
			oidcErrorRedirect(w, r, "oidc_disabled")
			return
		}
		oidcErrorRedirect(w, r, "oidc_unavailable")
		return
	}
	http.SetCookie(w, h.sessionCookie(sessionToken))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) oauthConfig(config lab.OIDCConfig, provider *oidc.Provider) (*oauth2.Config, error) {
	secret, err := resolveOIDCSecret(config.ClientSecretRef)
	if err != nil {
		return nil, err
	}
	scopes := strings.Fields(config.Scopes)
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	return &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: secret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  config.RedirectURI,
		Scopes:       scopes,
	}, nil
}

func resolveOIDCSecret(reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", nil
	}
	switch {
	case strings.HasPrefix(reference, "env:"):
		name := strings.TrimPrefix(reference, "env:")
		if !validOIDCRefName(name) {
			return "", errors.New("referencia de entorno inválida")
		}
		value := strings.TrimRight(os.Getenv(name), "\r\n")
		if value == "" {
			return "", errors.New("la variable de entorno del secreto está vacía")
		}
		return value, nil
	case strings.HasPrefix(reference, "file:"):
		path := strings.TrimPrefix(reference, "file:")
		if !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
			return "", errors.New("la referencia de archivo debe ser una ruta absoluta")
		}
		return readOIDCSecret(path)
	case strings.HasPrefix(reference, "secret:"):
		name := strings.TrimPrefix(reference, "secret:")
		if !validOIDCRefName(name) {
			return "", errors.New("referencia de secreto inválida")
		}
		directory := strings.TrimSpace(os.Getenv("CUSTODIA_OIDC_SECRET_DIR"))
		if directory == "" || !filepath.IsAbs(directory) {
			return "", errors.New("el proveedor de secretos no está configurado")
		}
		return readOIDCSecret(filepath.Join(directory, name))
	default:
		return "", errors.New("referencia de secreto no soportada")
	}
}

func validOIDCRefName(name string) bool {
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

func readOIDCSecret(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > oidcSecretLimit {
		return "", errors.New("la fuente del secreto no está disponible")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("no se pudo leer el secreto")
	}
	return strings.TrimRight(string(content), "\r\n"), nil
}

// validateOIDCConfig is the contract shared by save, test, activation and
// login. It never resolves or logs the secret.
func validateOIDCConfig(c lab.OIDCConfig, publicOrigin string) error {
	issuer, err := url.Parse(c.Issuer)
	if err != nil || !issuer.IsAbs() || issuer.Host == "" || issuer.Hostname() == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || strings.HasSuffix(c.Issuer, "/") {
		return fmt.Errorf("el emisor debe ser una URL absoluta HTTPS sin query, fragmento ni barra final")
	}
	if !allowedOIDCURL(issuer) {
		return fmt.Errorf("el emisor debe usar HTTPS o HTTP en IP numérica de loopback")
	}
	if strings.TrimSpace(c.ClientID) == "" || len(c.ClientID) > 512 || strings.IndexFunc(c.ClientID, unicode.IsControl) >= 0 {
		return fmt.Errorf("el client ID es obligatorio")
	}
	if c.RedirectURI != publicOrigin+oidcCallbackPath {
		return fmt.Errorf("la URL de retorno debe ser %s", publicOrigin+oidcCallbackPath)
	}
	if c.ClientSecretRef != "" {
		switch {
		case strings.HasPrefix(c.ClientSecretRef, "env:"):
			if !validOIDCRefName(strings.TrimPrefix(c.ClientSecretRef, "env:")) {
				return fmt.Errorf("la referencia de entorno del secreto es inválida")
			}
		case strings.HasPrefix(c.ClientSecretRef, "secret:"):
			if !validOIDCRefName(strings.TrimPrefix(c.ClientSecretRef, "secret:")) {
				return fmt.Errorf("la referencia de secreto es inválida")
			}
		case strings.HasPrefix(c.ClientSecretRef, "file:"):
			path := strings.TrimPrefix(c.ClientSecretRef, "file:")
			if !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
				return fmt.Errorf("la referencia de archivo del secreto debe ser una ruta absoluta")
			}
		default:
			return fmt.Errorf("la referencia del secreto debe usar env:, file: o secret:")
		}
	}
	scopes := strings.Fields(c.Scopes)
	hasOpenID := false
	for _, scope := range scopes {
		if !validScope(scope) {
			return fmt.Errorf("los scopes contienen caracteres no válidos")
		}
		if scope == oidc.ScopeOpenID {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		return fmt.Errorf("el scope openid es obligatorio")
	}
	for _, claim := range []string{c.SubjectClaim, c.EmailClaim, c.NameClaim} {
		if claim == "" || len(claim) > 128 || strings.IndexFunc(claim, unicode.IsControl) >= 0 {
			return fmt.Errorf("los nombres de claim son obligatorios")
		}
	}
	return nil
}

func allowedOIDCURL(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func validScope(scope string) bool {
	if scope == "" || len(scope) > 128 {
		return false
	}
	for _, r := range scope {
		if !(r == '_' || r == '-' || r == '.' || r == ':' || r == '/' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

type oidcDiscovery struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	JWKSURI                string   `json:"jwks_uri"`
	ResponseTypesSupported []string `json:"response_types_supported"`
}

func (c *oidcClient) test(ctx context.Context, config lab.OIDCConfig) (bool, string) {
	if err := validateOIDCConfig(config, c.publicOrigin); err != nil {
		return false, err.Error()
	}
	discoveryURL := strings.TrimSuffix(config.Issuer, "/") + "/.well-known/openid-configuration"
	var doc oidcDiscovery
	if err := c.getJSON(ctx, discoveryURL, &doc); err != nil {
		return false, "no se pudo leer el descubrimiento del emisor: " + err.Error()
	}
	if doc.Issuer != config.Issuer {
		return false, "el emisor del descubrimiento no coincide con el configurado"
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return false, "el descubrimiento no publica authorization, token y jwks"
	}
	supportsCode := false
	for _, value := range doc.ResponseTypesSupported {
		if value == "code" {
			supportsCode = true
		}
	}
	if len(doc.ResponseTypesSupported) != 0 && !supportsCode {
		return false, "el emisor no anuncia el flujo authorization code"
	}
	for _, raw := range []string{doc.AuthorizationEndpoint, doc.TokenEndpoint, doc.JWKSURI} {
		parsed, err := url.Parse(raw)
		if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || !allowedOIDCURL(parsed) {
			return false, "el descubrimiento publica un endpoint no permitido"
		}
	}
	var keys struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
		} `json:"keys"`
	}
	if err := c.getJSON(ctx, doc.JWKSURI, &keys); err != nil {
		return false, "no se pudieron leer las claves del emisor: " + err.Error()
	}
	usable := 0
	for _, key := range keys.Keys {
		if key.Kty != "RSA" && key.Kty != "EC" && key.Kty != "OKP" {
			continue
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(key.Alg), "hs") || string(key.Alg) == "none" {
			continue
		}
		usable++
	}
	if usable == 0 {
		return false, "el emisor no publica claves de firma asimétricas"
	}
	return true, fmt.Sprintf("Descubrimiento y claves verificados (%d claves). No se probó el login interactivo.", usable)
}

func (c *oidcClient) getJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("estado %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, oidcBodyLimit))
	return decoder.Decode(target)
}

// oidcAdmin serves the console contract. Every mutation is CSRF-checked by
// the caller and audited by the store in the same transaction.
func (h *handler) oidcAdmin(w http.ResponseWriter, r *http.Request, session lab.LocalSession, current time.Time) {
	if !h.store.IsAdministrator(r.Context(), session.Principal) {
		detail(w, 403, "Permiso de administración requerido.")
		return
	}
	path := r.URL.Path
	switch {
	case path == "/api/access/oidc":
		if r.Method == "GET" {
			config, err := h.store.GetOIDCConfig(r.Context())
			if errors.Is(err, lab.ErrOIDCNotConfigured) {
				send(w, 200, map[string]any{"config": nil})
				return
			}
			if err != nil {
				detail(w, 503, "No se pudo leer la configuración OIDC.")
				return
			}
			healthy, _ := h.store.OIDCHealthy(r.Context(), current)
			send(w, 200, map[string]any{"config": config, "healthy": healthy})
			return
		}
		if r.Method != "PUT" {
			detail(w, 405, "Operación no disponible.")
			return
		}
		var input struct {
			Issuer          string `json:"issuer"`
			ClientID        string `json:"client_id"`
			ClientSecretRef string `json:"client_secret_ref"`
			RedirectURI     string `json:"redirect_uri"`
			Scopes          string `json:"scopes"`
			SubjectClaim    string `json:"subject_claim"`
			EmailClaim      string `json:"email_claim"`
			NameClaim       string `json:"name_claim"`
		}
		if !decode(w, r, &input) {
			detail(w, 400, "Datos de configuración inválidos.")
			return
		}
		config := lab.OIDCConfig{
			Issuer: strings.TrimSpace(input.Issuer), ClientID: strings.TrimSpace(input.ClientID),
			ClientSecretRef: strings.TrimSpace(input.ClientSecretRef), RedirectURI: strings.TrimSpace(input.RedirectURI),
			Scopes: strings.TrimSpace(input.Scopes), SubjectClaim: strings.TrimSpace(input.SubjectClaim),
			EmailClaim: strings.TrimSpace(input.EmailClaim), NameClaim: strings.TrimSpace(input.NameClaim),
		}
		if config.Scopes == "" {
			config.Scopes = "openid profile email"
		}
		if config.SubjectClaim == "" {
			config.SubjectClaim = "sub"
		}
		if config.EmailClaim == "" {
			config.EmailClaim = "email"
		}
		if config.NameClaim == "" {
			config.NameClaim = "name"
		}
		if err := validateOIDCConfig(config, h.config.PublicOrigin); err != nil {
			detail(w, 400, err.Error())
			return
		}
		if err := h.store.SaveOIDCConfig(r.Context(), session.Principal, config, current); err != nil {
			detail(w, 403, "No se pudo guardar la configuración OIDC.")
			return
		}
		send(w, 200, map[string]any{"saved": true, "enabled": false})
	case path == "/api/access/oidc/test":
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para probar el proveedor.")
			return
		}
		config, err := h.store.GetOIDCConfig(r.Context())
		if err != nil {
			detail(w, 409, "Guardá la configuración antes de probarla.")
			return
		}
		ok, message := h.oidc.test(r.Context(), config)
		if err = h.store.RecordOIDCTest(r.Context(), session.Principal, ok, message, current); err != nil {
			detail(w, 503, "No se pudo registrar la prueba.")
			return
		}
		send(w, 200, map[string]any{"ok": ok, "detail": message})
	case path == "/api/access/oidc/enable", path == "/api/access/oidc/disable":
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para cambiar el estado.")
			return
		}
		var err error
		if path == "/api/access/oidc/enable" {
			err = h.store.EnableOIDC(r.Context(), session.Principal, current)
		} else {
			err = h.store.DisableOIDC(r.Context(), session.Principal, current)
		}
		switch {
		case errors.Is(err, lab.ErrOIDCTestStale):
			detail(w, 409, "Probá la configuración actual antes de activar el proveedor.")
		case err != nil:
			detail(w, 409, "No se pudo cambiar el estado del proveedor.")
		default:
			send(w, 200, map[string]any{"enabled": path == "/api/access/oidc/enable"})
		}
	case path == "/api/access/oidc/pending":
		if r.Method != "GET" {
			detail(w, 405, "Operación no disponible.")
			return
		}
		rows, err := h.store.ListOIDCPending(r.Context(), session.Principal)
		if err != nil {
			detail(w, 403, "No se pudo consultar la cola de aprobación.")
			return
		}
		send(w, 200, map[string]any{"rows": rows})
	case path == "/api/access/oidc/pending/approve":
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para aprobar una identidad.")
			return
		}
		var input struct {
			Issuer      string   `json:"issuer"`
			Subject     string   `json:"subject"`
			Connections []string `json:"connections"`
		}
		if !decode(w, r, &input) || input.Issuer == "" || input.Subject == "" || len(input.Connections) > 128 {
			detail(w, 400, "Indicá el emisor, la identidad y sus conexiones.")
			return
		}
		if input.Connections == nil {
			input.Connections = []string{}
		}
		if err := h.store.ApproveOIDCPending(r.Context(), session.Principal, input.Issuer, input.Subject, input.Connections, current); err != nil {
			h.storeError(w, err)
			return
		}
		w.WriteHeader(204)
	case path == "/api/access/oidc/pending/deny":
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para rechazar una identidad.")
			return
		}
		var input struct {
			Issuer  string `json:"issuer"`
			Subject string `json:"subject"`
		}
		if !decode(w, r, &input) || input.Issuer == "" || input.Subject == "" {
			detail(w, 400, "Indicá el emisor y la identidad.")
			return
		}
		if err := h.store.DenyOIDCPending(r.Context(), session.Principal, input.Issuer, input.Subject, current); err != nil {
			h.storeError(w, err)
			return
		}
		w.WriteHeader(204)
	case path == "/api/access/oidc/users":
		if r.Method != "GET" {
			detail(w, 405, "Operación no disponible.")
			return
		}
		rows, err := h.store.ListOIDCUsers(r.Context(), session.Principal, current)
		if err != nil {
			detail(w, 403, "No se pudo consultar las cuentas OIDC.")
			return
		}
		send(w, 200, map[string]any{"rows": rows})
	case strings.HasPrefix(path, "/api/access/oidc/users/"):
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para administrar una cuenta OIDC.")
			return
		}
		var input struct {
			Issuer  string `json:"issuer"`
			Subject string `json:"subject"`
		}
		if !decode(w, r, &input) || input.Issuer == "" || input.Subject == "" {
			detail(w, 400, "Indicá el emisor y la identidad.")
			return
		}
		var err error
		switch strings.TrimPrefix(path, "/api/access/oidc/users/") {
		case "disable":
			err = h.store.SetOIDCUserEnabled(r.Context(), session.Principal, input.Issuer, input.Subject, false, current)
		case "enable":
			err = h.store.SetOIDCUserEnabled(r.Context(), session.Principal, input.Issuer, input.Subject, true, current)
		case "revoke-sessions":
			err = h.store.RevokeOIDCSessions(r.Context(), session.Principal, input.Issuer, input.Subject, current)
		default:
			detail(w, 404, "Operación no disponible.")
			return
		}
		if err != nil {
			h.storeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		detail(w, 404, "Operación no disponible.")
	}
}
