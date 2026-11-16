package lab

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Handler only serves a synthetic lab. Token possession is NOT product identity.
func (s *Store) Handler(token string, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		send := func(code int, v any) { w.WriteHeader(code); json.NewEncoder(w).Encode(v) }
		supplied, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !bearer || len(r.Header.Values("Authorization")) != 1 {
			send(401, map[string]string{"detail": "Credencial de laboratorio requerida."})
			return
		}
		var principal Principal
		var authErr error
		if token != "" && subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) == 1 {
			principal, authErr = s.principal(r.Context(), "synthetic-lab")
		} else {
			principal, authErr = s.credential(r.Context(), supplied)
		}
		if authErr != nil {
			send(401, map[string]string{"detail": "Credencial de laboratorio requerida."})
			return
		}
		s.ServePrincipal(w, r, now(), principal, Profile{Mode: "local-dev", Name: "Laboratorio sintético: " + principal.Subject, Email: "lab@acme.example"})
	})
}

type Profile struct{ Mode, Name, Email string }

// ServePrincipal receives identity only from a composition-root authentication adapter.
func (s *Store) ServePrincipal(w http.ResponseWriter, r *http.Request, current time.Time, principal Principal, profile Profile) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	send := func(code int, v any) { w.WriteHeader(code); json.NewEncoder(w).Encode(v) }
	if r.URL.Path == "/api/workspaces/preferences" && (r.Method == "GET" || r.Method == "PUT") {
		s.preferences(w, r, current, principal)
		return
	}
	if s.serveSyncAPI(w, r, current, principal) {
		return
	}
	if r.Method != "GET" {
		send(405, map[string]string{"detail": "Laboratorio de sólo lectura."})
		return
	}
	var c Company
	if strings.HasPrefix(r.URL.Path, "/api/chains/person") {
		query := r.URL.Query()
		identity, netskope := query.Get("identity_connection"), query.Get("netskope_connection")
		if identity == "" {
			identity = IdentityConnection
		}
		if netskope == "" {
			netskope = NetskopeConnection
		}
		both := strings.HasPrefix(r.URL.Path, "/api/chains/person/coverage")
		var err error
		c, err = s.View(r.Context(), principal, identity, netskope, query.Has("identity_connection"), query.Has("netskope_connection"), both)
		if err == ErrDenied {
			send(403, map[string]string{"detail": "Alcance de fuentes insuficiente para este recorrido."})
			return
		}
		if err != nil {
			send(503, map[string]string{"detail": "No se pudo consultar la evidencia autorizada."})
			return
		}
	}
	switch r.URL.Path {
	case "/api/ingestion/schedules", "/api/events":
		send(200, map[string]any{"rows": []any{}})
	case "/api/connections":
		connections, err := s.Connections(r.Context(), principal)
		if err != nil {
			send(503, map[string]string{"detail": "No se pudo consultar el alcance."})
			return
		}
		send(200, map[string]any{"rows": connections})
	case "/api/auth/status":
		connections, err := s.Connections(r.Context(), principal)
		if err != nil {
			send(503, map[string]string{"detail": "No se pudo consultar el alcance."})
			return
		}
		permissions := []string{}
		identity, netskope := false, false
		for _, connection := range connections {
			if connection.Connector != "" {
				// A synthetic HTTP inventory source is not the provider-specific
				// read scope used by Personas/cobertura.
				continue
			}
			if connection.Kind == "identity" {
				identity = true
			}
			if connection.Kind == "netskope" {
				netskope = true
			}
		}
		if identity {
			permissions = append(permissions, "identity:catalogs_read")
		}
		if netskope {
			permissions = append(permissions, "netskope:read")
		}
		if identity && netskope {
			permissions = append(permissions, "coverage:read")
		}
		roles := []string{"reader"}
		roleNames := []string{"Lector"}
		if s.IsAdministrator(r.Context(), principal) {
			permissions = append(permissions, "installation:manage")
			roles = []string{"admin"}
			roleNames = []string{"Administrador"}
		}
		if canRun, err := s.SyncCanRunAny(r.Context(), principal); err == nil && canRun {
			permissions = append(permissions, "ingestion:run")
		}
		send(200, map[string]any{"mode": profile.Mode, "authenticated": true, "permissions": permissions, "api_docs_enabled": false, "user": map[string]any{"actor_id": principal.Actor(), "name": profile.Name, "email": profile.Email, "roles": roles, "role_names": roleNames}})

	case "/api/chains/person/coverage":
		send(200, c.Summary(current))

	case "/api/chains/person/search":
		q := r.URL.Query().Get("q")
		rows := []any{}
		for _, p := range c.People {
			if strings.Contains(strings.ToLower(p.Name+" "+p.Email), strings.ToLower(q)) {
				sources := []string{}
				if len(p.IdentityIDs) > 0 {
					sources = append(sources, "identity")
				}
				if c.userCounts[p.ID] > 0 {
					sources = append(sources, "netskope")
				}
				rows = append(rows, map[string]any{"name": p.Name, "email": p.Email, "sources": sources})
			}
		}
		send(200, map[string]any{"query": q, "people": rows})
	case "/api/chains/person/coverage/detail":
		send(200, c.Detail(r.URL.Query().Get("email"), current))
	case "/api/chains/person":
		email := r.URL.Query().Get("email")
		tools := map[string]any{}
		name := ""
		for _, p := range c.People {
			if p.Email != email {
				continue
			}
			name = p.Name
			for source, ids := range map[string][]int{"identity": p.IdentityIDs, "netskope": {p.NetskopeID}} {
				if _, visible := c.runs[source]; !visible {
					continue
				}
				status := "unmatched"
				if len(ids) > 1 {
					status = "ambiguous"
				} else if len(ids) == 1 && ids[0] != 0 {
					status = "matched"
				}
				if source == "netskope" && c.userCounts[p.ID] > 1 {
					status = "ambiguous"
				}
				basis := "email_exact"
				if status == "ambiguous" {
					basis = "multiple_active_candidates"
				}
				tool := map[string]any{"status": status, "basis": basis, "device_count": 0, "stale_device_count": 0, "company": "Acme Corp", "client_versions": []string{}, "operating_systems": []string{}, "policy_count": 0, "role_count": 0}
				if status == "matched" {
					tool["user"] = map[string]any{"object_id": ids[0], "display_name": p.Name, "source_id": c.sourceIDs[ids[0]], "active": true}
				}
				if source == "netskope" {
					recent, old := c.DeviceCounts(p, current)
					tool["device_count"] = recent
					tool["stale_device_count"] = old
				}
				tools[source] = tool
			}
		}
		send(200, map[string]any{"email": email, "name": name, "tools": tools})
	default:
		send(404, map[string]string{"detail": "Recorrido aún no migrado en este laboratorio."})
	}
}
