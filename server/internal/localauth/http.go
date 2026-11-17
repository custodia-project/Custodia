package localauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"custodia/server/internal/lab"
)

const (
	AbsoluteLifetime = 12 * time.Hour
	IdleTimeout      = 30 * time.Minute
	CSRFHeader       = "X-Custodia-CSRF"
	maxBody          = 8192
)

var (
	UserAttempts = lab.AttemptPolicy{Limit: 5, Window: 15 * time.Minute, Lock: 15 * time.Minute}
	IPAttempts   = lab.AttemptPolicy{Limit: 50, Window: 15 * time.Minute, Lock: 15 * time.Minute}
)

// Config is the explicit authentication-mode configuration. Mode is "local"
// or "oidc"; an empty value means local for existing callers.
type Config struct {
	PublicOrigin   string
	Mode           string
	Version        string
	DataDir        string
	UpdateManifest string
}

func (c Config) mode() string {
	if c.Mode == "" {
		return "local"
	}
	return c.Mode
}

// Validate accepts an HTTPS origin, or numeric HTTP loopback for the harness.
func (c Config) Validate() error {
	if c.mode() != "local" && c.mode() != "oidc" {
		return fmt.Errorf("authentication mode must be local or oidc")
	}
	u, err := url.Parse(c.PublicOrigin)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.ForceQuery || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("public origin must be an HTTPS origin or numeric HTTP loopback without path")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("public origin must be an HTTPS origin or numeric HTTP loopback without path")
}

// CookieName uses the __Host- prefix whenever the origin is HTTPS.
func (c Config) CookieName() string {
	if strings.HasPrefix(c.PublicOrigin, "https://") {
		return "__Host-custodia_session"
	}
	return "custodia_session"
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenHash is the only form of a session token stored in SQLite.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CSRFToken is derived from the session token, so it needs no storage and
// changes whenever the session is rotated.
func CSRFToken(token string) string {
	sum := sha256.Sum256([]byte("custodia-csrf:" + token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type handler struct {
	store  *lab.Store
	config Config
	now    func() time.Time
	files  http.Handler
	oidc   *oidcClient
}

// NewHandler serves the password mode. Ozal assertions, laboratory bearers
// and identity headers are ignored: the session cookie is the only credential.
func NewHandler(store *lab.Store, config Config, now func() time.Time, files http.Handler) (http.Handler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	// NewHandler keeps its historical local contract even if a caller sets a
	// different mode by mistake.
	config.Mode = "local"
	return &handler{store: store, config: config, now: now, files: files}, nil
}

func send(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
func detail(w http.ResponseWriter, code int, message string) {
	send(w, code, map[string]string{"detail": message})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(v) != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF
}

func (h *handler) sessionCookie(token string) *http.Cookie {
	return &http.Cookie{Name: h.config.CookieName(), Value: token, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}
func (h *handler) expireCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: h.config.CookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

// sessionToken returns the single session cookie; duplicates are rejected.
func (h *handler) sessionToken(r *http.Request) string {
	value := ""
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == h.config.CookieName() {
			count++
			value = cookie.Value
		}
	}
	if count != 1 || len(value) != 43 {
		return ""
	}
	return value
}

func (h *handler) sameOrigin(r *http.Request) bool {
	return len(r.Header.Values("Origin")) == 1 && r.Header.Get("Origin") == h.config.PublicOrigin
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		// The compiled interface holds no data; it must load to show the login.
		if h.files == nil {
			detail(w, 404, "Interfaz no configurada.")
			return
		}
		h.files.ServeHTTP(w, r)
		return
	}
	mutation := r.Method != "GET" && r.Method != "HEAD"
	if mutation && !h.sameOrigin(r) {
		detail(w, 403, "Origen de la operación no autorizado.")
		return
	}
	current := h.now()
	if r.URL.Path == "/api/health" && r.Method == "GET" {
		version := h.config.Version
		if version == "" {
			version = "dev"
		}
		send(w, 200, map[string]any{"status": "ok", "version": version, "mode": h.config.mode()})
		return
	}
	if h.oidcMode() {
		switch r.URL.Path {
		case "/api/auth/oidc/start":
			h.oidcStart(w, r, current)
			return
		case "/api/auth/oidc/callback":
			h.oidcCallback(w, r, current)
			return
		}
	}
	if r.URL.Path == "/api/auth/setup" {
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para la configuración inicial.")
			return
		}
		h.setup(w, r, current)
		return
	}
	if r.URL.Path == "/api/auth/login" {
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para iniciar sesión.")
			return
		}
		h.login(w, r, current)
		return
	}
	token := h.sessionToken(r)
	var session lab.LocalSession
	err := lab.ErrNoSession
	if token != "" {
		session, err = h.resolveSession(r, token, current)
	}
	if err != nil && err != lab.ErrNoSession {
		detail(w, 503, "No se pudo verificar la sesión.")
		return
	}
	if err == lab.ErrNoSession {
		if token != "" {
			h.expireCookie(w)
		}
		if r.URL.Path == "/api/auth/status" && r.Method == "GET" {
			setupAvailable := false
			if h.config.mode() == "local" {
				available, err := h.store.SetupAvailable(r.Context(), current)
				if err != nil {
					detail(w, 503, "No se pudo verificar la configuración inicial.")
					return
				}
				setupAvailable = available
			}
			body := map[string]any{"mode": h.config.mode(), "authenticated": false, "setup_available": setupAvailable, "permissions": []string{}, "api_docs_enabled": false, "user": nil}
			if h.oidcMode() {
				body["oidc"] = h.oidcStatus(r.Context())
			}
			send(w, 200, body)
			return
		}
		detail(w, 401, "Iniciá sesión para continuar.")
		return
	}
	csrf := CSRFToken(token)
	if mutation {
		supplied := r.Header.Values(CSRFHeader)
		if len(supplied) != 1 || subtle.ConstantTimeCompare([]byte(supplied[0]), []byte(csrf)) != 1 {
			detail(w, 403, "Falta la verificación de la sesión. Recargá la página.")
			return
		}
	}
	switch r.URL.Path {
	case "/api/auth/logout":
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para cerrar sesión.")
			return
		}
		end := h.store.EndLocalSession
		if session.Issuer != lab.LocalIssuer {
			end = h.store.EndOIDCSession
		}
		if err := end(r.Context(), session.Principal, TokenHash(token), current); err != nil {
			detail(w, 503, "No se pudo cerrar la sesión.")
			return
		}
		h.expireCookie(w)
		send(w, 200, map[string]bool{"logged_out": true})
		return
	case "/api/auth/password":
		if r.Method != "POST" {
			detail(w, 405, "Usá POST para cambiar la contraseña.")
			return
		}
		if session.Issuer != lab.LocalIssuer {
			detail(w, 400, "El cambio de contraseña no aplica a una cuenta externa.")
			return
		}
		h.changePassword(w, r, session, current)
		return
	}
	if session.MustChange && r.URL.Path != "/api/auth/status" {
		detail(w, 403, "Cambiá tu contraseña temporal antes de continuar.")
		return
	}
	if h.oidcMode() && (r.URL.Path == "/api/access/oidc" || strings.HasPrefix(r.URL.Path, "/api/access/oidc/")) {
		h.oidcAdmin(w, r, session, current)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/system/") {
		h.system(w, r, session, current)
		return
	}
	if r.URL.Path == "/api/access/users" || strings.HasPrefix(r.URL.Path, "/api/access/users/") {
		h.users(w, r, session, current)
		return
	}
	adminPermissions := []string{"access:local_users_manage"}
	if h.oidcMode() {
		adminPermissions = append(adminPermissions, "access:oidc_manage")
	}
	authSource := "local"
	if session.Issuer != lab.LocalIssuer {
		authSource = "oidc"
	}
	extra := map[string]any{}
	if h.oidcMode() {
		extra["oidc"] = h.oidcStatus(r.Context())
	}
	h.store.ServePrincipal(w, r, current, session.Principal, lab.Profile{
		Mode: h.config.mode(), Name: session.DisplayName, CSRFToken: csrf, MustChangePassword: session.MustChange,
		AuthSource: authSource, AdminPermissions: adminPermissions, Extra: extra,
	})
}

const invalidLogin = "Usuario o contraseña incorrectos."
const tooMany = "Demasiados intentos. Esperá unos minutos antes de reintentar."

func (h *handler) login(w http.ResponseWriter, r *http.Request, current time.Time) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) || input.Password == "" || len(input.Username) > 256 {
		detail(w, 400, "Completá usuario y contraseña.")
		return
	}
	username, valid := lab.NormalizeLocalUsername(input.Username)
	ipKey := "ip:" + clientIP(r)
	keys := []string{ipKey}
	if valid {
		keys = append(keys, "user:"+username)
	}
	locked, err := h.store.LoginLocked(r.Context(), keys, UserAttempts.Window, current)
	if err == lab.ErrAttemptCapacity || (err == nil && locked) {
		detail(w, 429, tooMany)
		return
	}
	if err != nil {
		detail(w, 503, "No se pudo verificar el acceso.")
		return
	}
	var credential lab.LocalCredential
	matched := false
	if valid {
		credential, err = h.store.LocalCredential(r.Context(), username)
	} else {
		err = sql.ErrNoRows
	}
	switch {
	case err == sql.ErrNoRows:
		if err = burnDummy(r.Context(), input.Password); err != nil {
			detail(w, 503, "No se pudo verificar el acceso.")
			return
		}
	case err != nil:
		detail(w, 503, "No se pudo verificar el acceso.")
		return
	default:
		if matched, err = VerifyPassword(r.Context(), credential.PasswordHash, input.Password); err != nil {
			detail(w, 503, "No se pudo verificar el acceso.")
			return
		}
	}
	if !matched {
		if valid {
			if _, err = h.store.RecordLoginFailure(r.Context(), "user:"+username, UserAttempts, "anonymous", current); err != nil {
				detail(w, 503, "No se pudo verificar el acceso.")
				return
			}
		}
		if _, err = h.store.RecordLoginFailure(r.Context(), ipKey, IPAttempts, "anonymous", current); err != nil {
			detail(w, 503, "No se pudo verificar el acceso.")
			return
		}
		detail(w, 401, invalidLogin)
		return
	}
	if !credential.Enabled {
		detail(w, 403, "Tu cuenta no está habilitada en Custodia. Contactá al administrador.")
		return
	}
	// OIDC mode never falls back to password authentication for end users.
	// A host-created local administrator remains the explicit break-glass path.
	if h.oidcMode() && !h.store.IsAdministrator(r.Context(), credential.Principal) {
		detail(w, 403, "En modo OIDC el acceso con contraseña está reservado a administradores locales.")
		return
	}
	if err = h.store.ClearLoginFailures(r.Context(), "user:"+username); err != nil {
		detail(w, 503, "No se pudo verificar el acceso.")
		return
	}
	token, err := newToken()
	if err != nil {
		detail(w, 503, "No se pudo iniciar la sesión.")
		return
	}
	if err = h.store.StartLocalSession(r.Context(), credential.Principal, TokenHash(token), current, current.Add(AbsoluteLifetime)); err != nil {
		if errors.Is(err, lab.ErrDenied) {
			detail(w, 403, "Tu cuenta no está habilitada en Custodia. Contactá al administrador.")
			return
		}
		detail(w, 503, "No se pudo iniciar la sesión.")
		return
	}
	http.SetCookie(w, h.sessionCookie(token))
	send(w, 200, map[string]any{"authenticated": true, "csrf_token": CSRFToken(token), "must_change_password": credential.MustChange})
}

func (h *handler) changePassword(w http.ResponseWriter, r *http.Request, session lab.LocalSession, current time.Time) {
	var input struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &input) || input.Current == "" || input.New == "" {
		detail(w, 400, "Completá tu contraseña actual y la nueva.")
		return
	}
	userKey := "user:" + session.Subject
	locked, err := h.store.LoginLocked(r.Context(), []string{userKey}, UserAttempts.Window, current)
	if err == lab.ErrAttemptCapacity || (err == nil && locked) {
		detail(w, 429, tooMany)
		return
	}
	if err != nil {
		detail(w, 503, "No se pudo verificar la contraseña.")
		return
	}
	credential, err := h.store.LocalCredential(r.Context(), session.Subject)
	if err != nil {
		detail(w, 503, "No se pudo verificar la contraseña.")
		return
	}
	matched, err := VerifyPassword(r.Context(), credential.PasswordHash, input.Current)
	if err != nil {
		detail(w, 503, "No se pudo verificar la contraseña.")
		return
	}
	if !matched {
		if _, err = h.store.RecordLoginFailure(r.Context(), userKey, UserAttempts, session.Actor(), current); err != nil {
			detail(w, 503, "No se pudo verificar la contraseña.")
			return
		}
		detail(w, 400, "La contraseña actual no es correcta.")
		return
	}
	if input.New == input.Current || CheckPolicy(session.Subject, input.New) != nil {
		detail(w, 400, fmt.Sprintf("La nueva contraseña debe tener entre %d y %d caracteres y ser distinta del usuario y de la actual.", MinPasswordLength, MaxPasswordLength))
		return
	}
	hash, err := HashPassword(r.Context(), input.New)
	if err != nil {
		detail(w, 503, "No se pudo guardar la contraseña.")
		return
	}
	token, err := newToken()
	if err != nil {
		detail(w, 503, "No se pudo guardar la contraseña.")
		return
	}
	err = h.store.ChangeLocalPassword(r.Context(), session.Principal, credential.PasswordHash, hash, TokenHash(token), current, current.Add(AbsoluteLifetime))
	if err == lab.ErrLocalConflict {
		detail(w, 409, "La contraseña cambió mientras tanto. Volvé a intentarlo.")
		return
	}
	if err != nil {
		detail(w, 503, "No se pudo guardar la contraseña.")
		return
	}
	http.SetCookie(w, h.sessionCookie(token))
	send(w, 200, map[string]any{"changed": true, "csrf_token": CSRFToken(token)})
}

func (h *handler) users(w http.ResponseWriter, r *http.Request, session lab.LocalSession, current time.Time) {
	ctx := r.Context()
	if !h.store.IsAdministrator(ctx, session.Principal) {
		detail(w, 403, "Permiso de administración requerido.")
		return
	}
	if r.URL.Path == "/api/access/users" {
		switch r.Method {
		case "GET":
			users, err := h.store.ListLocalUsers(ctx, session.Principal, current)
			if err != nil {
				h.storeError(w, err)
				return
			}
			send(w, 200, map[string]any{"rows": users})
		case "POST":
			h.createUser(ctx, w, r, session, current)
		default:
			detail(w, 405, "Operación no disponible.")
		}
		return
	}
	if r.Method != "POST" {
		detail(w, 405, "Operación no disponible.")
		return
	}
	var input struct {
		Username string `json:"username"`
	}
	if !decode(w, r, &input) || input.Username == "" {
		detail(w, 400, "Indicá el usuario.")
		return
	}
	var err error
	switch strings.TrimPrefix(r.URL.Path, "/api/access/users/") {
	case "disable":
		err = h.store.SetLocalUserEnabled(ctx, session.Principal, input.Username, false, current)
	case "enable":
		err = h.store.SetLocalUserEnabled(ctx, session.Principal, input.Username, true, current)
	case "revoke-sessions":
		err = h.store.RevokeLocalSessions(ctx, session.Principal, input.Username, current)
	default:
		detail(w, 404, "Operación no disponible.")
		return
	}
	if err != nil {
		h.storeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *handler) createUser(ctx context.Context, w http.ResponseWriter, r *http.Request, session lab.LocalSession, current time.Time) {
	var input struct {
		Username    string   `json:"username"`
		DisplayName string   `json:"display_name"`
		Password    string   `json:"password"`
		Connections []string `json:"connections"`
	}
	if !decode(w, r, &input) || len(input.Connections) > 128 {
		detail(w, 400, "Datos de usuario inválidos.")
		return
	}
	username, ok := lab.NormalizeLocalUsername(input.Username)
	if !ok || strings.TrimSpace(input.DisplayName) == "" {
		detail(w, 400, "Usuario o nombre inválidos: usá minúsculas, dígitos y . _ @ - (hasta 128).")
		return
	}
	if CheckPolicy(username, input.Password) != nil {
		detail(w, 400, fmt.Sprintf("La contraseña temporal debe tener entre %d y %d caracteres y ser distinta del usuario.", MinPasswordLength, MaxPasswordLength))
		return
	}
	if input.Connections == nil {
		input.Connections = []string{}
	}
	hash, err := HashPassword(ctx, input.Password)
	if err != nil {
		detail(w, 503, "No se pudo crear el usuario.")
		return
	}
	if err = h.store.CreateLocalUser(ctx, session.Principal, username, input.DisplayName, hash, input.Connections, current); err != nil {
		h.storeError(w, err)
		return
	}
	send(w, 201, map[string]any{"username": username, "must_change_password": true})
}

func (h *handler) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lab.ErrDenied):
		detail(w, 403, "La operación excede tu alcance de administración.")
	case errors.Is(err, lab.ErrLocalExists):
		detail(w, 409, "Ese usuario ya existe.")
	default:
		detail(w, 409, "No se pudo aplicar el cambio de acceso.")
	}
}
