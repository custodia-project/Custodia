package gateway

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"custodia/server/internal/lab"
)

func assertionHeader(r *http.Request) (string, error) {
	count := 0
	value := ""
	for name, values := range r.Header {
		if strings.EqualFold(strings.ReplaceAll(name, "_", "-"), "X-Ozal-Assertion") {
			if !strings.EqualFold(name, "X-Ozal-Assertion") {
				return "", ErrInvalid
			}
			count += len(values)
			if len(values) == 1 {
				value = values[0]
			}
		}
	}
	if count != 1 {
		return "", ErrInvalid
	}
	return value, nil
}

// NewHandler is independent of the laboratory authenticator: no bearer fallback,
// no session created by Custodia and no trusted user/email headers.
func NewHandler(store *lab.Store, config Config, now func() time.Time, files http.Handler) (http.Handler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	pinned := map[string]ed25519.PublicKey{}
	for id, key := range config.Keys {
		pinned[id] = append(ed25519.PublicKey(nil), key...)
	}
	config.Keys = pinned
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		send := func(code int, message string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"detail": message})
		}
		if r.URL.Path == "/api/health" && r.Method == "GET" {
			version := config.Version
			if version == "" {
				version = "dev"
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": version, "mode": "ozal"})
			return
		}
		token, err := assertionHeader(r)
		if err != nil {
			send(401, "Identidad verificada de Ozal requerida.")
			return
		}
		current := now()
		claims, err := Verify(token, config.Keys, config.Issuer, config.Audience, current)
		if err != nil {
			send(401, "La aserción de identidad no es válida.")
			return
		}
		err = store.ConsumeAssertion(r.Context(), claims.Issuer, claims.ID, claims.Expiry+int64(Skew/time.Second), current.Unix())
		if err == lab.ErrReplay {
			send(401, "La aserción de identidad ya fue utilizada.")
			return
		}
		if err != nil {
			send(503, "No se pudo verificar el acceso.")
			return
		}
		principal, err := store.ResolvePrincipal(r.Context(), claims.Issuer, claims.Subject)
		if err != nil {
			send(403, "Tu cuenta no está habilitada en Custodia. Contactá al administrador.")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != config.PublicOrigin {
				send(403, "Origen de la operación no autorizado.")
				return
			}
		}
		if r.URL.Path == "/api/auth/logout" {
			if r.Method != "POST" {
				send(405, "Usá POST para cerrar sesión.")
				return
			}
			json.NewEncoder(w).Encode(map[string]bool{"gateway_logout": true})
			return
		}
		if r.URL.Path == "/api/access/readers" {
			if !store.IsAdministrator(r.Context(), principal) {
				send(403, "Permiso de administración requerido.")
				return
			}
			if r.Method != "POST" && r.Method != "DELETE" {
				send(405, "Operación no disponible.")
				return
			}
			var input struct {
				Subject     string   `json:"subject"`
				Connections []string `json:"connections"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil {
				send(400, "Datos de acceso inválidos.")
				return
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF || len(input.Connections) > 128 {
				send(400, "Datos de acceso inválidos.")
				return
			}
			if r.Method == "POST" {
				err = store.CreateReader(r.Context(), principal, input.Subject, input.Connections)
			} else {
				if len(input.Connections) != 0 {
					send(400, "La revocación sólo requiere el sujeto.")
					return
				}
				err = store.DisableReader(r.Context(), principal, input.Subject)
			}
			if err == lab.ErrDenied {
				send(403, "La operación excede tu alcance de administración.")
				return
			}
			if err != nil {
				send(409, "No se pudo aplicar el cambio de acceso.")
				return
			}
			w.WriteHeader(204)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			name := claims.Name
			if name == "" {
				name = claims.Subject
			}
			store.ServePrincipal(w, r, current, principal, lab.Profile{Mode: "ozal", Name: name, Email: claims.Email})
			return
		}
		if files == nil {
			send(404, "Interfaz no configurada.")
			return
		}
		w.Header().Del("Content-Type")
		files.ServeHTTP(w, r)
	}), nil
}
