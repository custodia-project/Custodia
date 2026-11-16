package lab

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type Preferences struct {
	Visible []string `json:"visible_workspaces"`
	Hidden  []string `json:"hidden_workspaces"`
	Recent  []string `json:"recent_workspaces"`
	Updated *string  `json:"updated_at"`
}

// allowedWorkspaces mirrors the frontend workspace catalog. Preferences are
// per-actor, so this is a contract check, not an authorization boundary: an
// unknown ID is rejected instead of being echoed into the stored document.
var allowedWorkspaces = map[string]bool{
	"general": true, "posture": true, "graph": true, "person": true, "safes": true,
	"roles": true, "platforms": true, "connectors": true, "pamCleanup": true,
	"identityUsers": true, "identityGroups": true, "identityRoles": true,
	"identityFederations": true, "sia": true, "siaResources": true,
	"siaDatabaseAccounts": true, "siaConnectors": true, "identity": true,
	"assets": true, "inventory": true, "users": true, "activity": true,
	"help": true, "pamHealth": true, "netskopeInventory": true,
	"netskopeUsers": true, "netskopeDevices": true, "netskopeDlp": true,
	"netskopePosture": true, "netskopeOperations": true, "netskopeOfficeExperience": true,
	"netskopeChains": true, "netskopeAdmins": true, "netskopeUci": true,
	"netskopeDemSites": true,
}

func (s *Store) preferences(w http.ResponseWriter, r *http.Request, now time.Time, principal Principal) {
	fail := func(code int) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"detail": "No se pudieron consultar o guardar las preferencias del laboratorio."})
	}
	var patch struct {
		Visible *[]string `json:"visible_workspaces"`
		Hidden  *[]string `json:"hidden_workspaces"`
		Recent  *[]string `json:"recent_workspaces"`
	}
	if r.Method == "PUT" {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&patch); err != nil {
			fail(400)
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			fail(400)
			return
		}
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(503)
		return
	}
	defer tx.Rollback()
	var enabled int
	if err = tx.QueryRowContext(r.Context(), "SELECT enabled FROM lab_principals WHERE id=?", principal.ID).Scan(&enabled); err != nil || enabled != 1 {
		fail(403)
		return
	}
	before := Preferences{Visible: []string{"person"}, Hidden: []string{}, Recent: []string{}}
	var data []byte
	err = tx.QueryRowContext(r.Context(), "SELECT document FROM lab_preferences WHERE actor=?", principal.Actor()).Scan(&data)
	if err != nil && err != sql.ErrNoRows {
		fail(503)
		return
	}
	if err == nil {
		if err = json.Unmarshal(data, &before); err != nil {
			fail(503)
			return
		}
	}
	after := before
	if r.Method == "PUT" {
		for _, field := range []struct {
			src *[]string
			dst *[]string
		}{{patch.Visible, &after.Visible}, {patch.Hidden, &after.Hidden}, {patch.Recent, &after.Recent}} {
			if field.src != nil {
				if len(*field.src) > 64 {
					fail(400)
					return
				}
				seen := map[string]bool{}
				values := make([]string, 0, len(*field.src))
				for _, view := range *field.src {
					if !allowedWorkspaces[view] {
						fail(403)
						return
					}
					if seen[view] {
						continue
					}
					seen[view] = true
					values = append(values, view)
				}
				*field.dst = values
			}
		}
		stamp := now.UTC().Format(time.RFC3339)
		after.Updated = &stamp
		a, _ := json.Marshal(after)
		b, _ := json.Marshal(before)
		if _, err = tx.ExecContext(r.Context(), "INSERT INTO lab_preferences VALUES(?,?) ON CONFLICT(actor) DO UPDATE SET document=excluded.document", principal.Actor(), a); err != nil {
			fail(503)
			return
		}
		if _, err = tx.ExecContext(r.Context(), "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,?,?,?,?)", principal.Actor(), "workspace_preferences", b, a, stamp); err != nil {
			fail(503)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		fail(503)
		return
	}
	json.NewEncoder(w).Encode(after)
}
