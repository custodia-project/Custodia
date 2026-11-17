package localauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"custodia/server/internal/lab"
	"custodia/server/internal/sqlitebackup"
)

type releaseManifest struct {
	Version   string `json:"version"`
	Package   string `json:"package"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	// allowed_signers is deliberately absent: the release key is pinned by
	// the installer for the root updater and never travels with a release.
}

func (h *handler) readReleaseManifest(ctx context.Context) (releaseManifest, []byte, error) {
	var manifest releaseManifest
	location := strings.TrimSpace(h.config.UpdateManifest)
	if location == "" {
		return manifest, nil, errors.New("update manifest is not configured")
	}
	var body []byte
	if strings.HasPrefix(location, "https://") {
		client := &http.Client{Timeout: 10 * time.Second}
		request, err := http.NewRequestWithContext(ctx, "GET", location, nil)
		if err != nil {
			return manifest, nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return manifest, nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return manifest, nil, fmt.Errorf("manifest returned %d", response.StatusCode)
		}
		body, err = io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return manifest, nil, err
		}
	} else {
		if !filepath.IsAbs(location) {
			return manifest, nil, errors.New("manifest path must be absolute")
		}
		var err error
		body, err = os.ReadFile(location)
		if err != nil {
			return manifest, nil, err
		}
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return manifest, nil, err
	}
	if strings.TrimSpace(manifest.Version) == "" || manifest.Package == "" || manifest.SHA256 == "" || manifest.Signature == "" {
		return manifest, nil, errors.New("manifest is incomplete")
	}
	return manifest, body, nil
}

func (h *handler) system(w http.ResponseWriter, r *http.Request, session lab.LocalSession, current time.Time) {
	if !h.store.IsAdministrator(r.Context(), session.Principal) {
		detail(w, 403, "Permiso de administración requerido.")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/system/backups") {
		h.backups(w, r, session, current)
		return
	}
	if r.URL.Path == "/api/system/diagnostics" && r.Method == "GET" {
		groups, err := h.store.ListConnectionGroups(r.Context(), session.Principal)
		if err != nil {
			detail(w, 503, "No se pudo consultar el diagnóstico.")
			return
		}
		send(w, 200, map[string]any{"rows": groups})
		return
	}
	manifest, raw, err := h.readReleaseManifest(r.Context())
	if r.URL.Path == "/api/system/version" {
		body := map[string]any{
			"current": h.config.Version, "available": "", "update_available": false,
			"manifest_configured": err == nil, "mode": h.config.mode(),
		}
		if err == nil {
			body["available"] = manifest.Version
			body["update_available"] = manifest.Version != h.config.Version
		}
		send(w, 200, body)
		return
	}
	if r.Method != "POST" {
		detail(w, 405, "Usá POST para solicitar la actualización.")
		return
	}
	if err != nil {
		detail(w, 409, "No hay un manifiesto de actualización válido.")
		return
	}
	if manifest.Version == h.config.Version {
		detail(w, 409, "Ya estás en la versión disponible.")
		return
	}
	if h.config.DataDir == "" {
		detail(w, 503, "El directorio de datos no está configurado.")
		return
	}
	directory := filepath.Join(h.config.DataDir, ".update")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		detail(w, 503, "No se pudo preparar la actualización.")
		return
	}
	request := map[string]any{
		"manifest": json.RawMessage(raw), "requested_by": session.Actor(),
		"requested_at": current.UTC().Format(time.RFC3339),
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		detail(w, 503, "No se pudo preparar la actualización.")
		return
	}
	temporary := filepath.Join(directory, "request.json.tmp")
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		detail(w, 503, "No se pudo preparar la actualización.")
		return
	}
	if err := os.Rename(temporary, filepath.Join(directory, "request.json")); err != nil {
		detail(w, 503, "No se pudo preparar la actualización.")
		return
	}
	if err := h.store.RecordAudit(r.Context(), session.Actor(), "system_update_requested", map[string]any{"version": manifest.Version}, current); err != nil {
		detail(w, 503, "No se pudo auditar la actualización.")
		return
	}
	send(w, 202, map[string]any{"requested": true, "version": manifest.Version})
}

type backupFile struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
}

func (h *handler) backups(w http.ResponseWriter, r *http.Request, session lab.LocalSession, current time.Time) {
	if h.config.DataDir == "" {
		detail(w, 503, "El directorio de datos no está configurado.")
		return
	}
	directory := filepath.Join(h.config.DataDir, "backups")
	if r.URL.Path == "/api/system/backups" {
		switch r.Method {
		case "GET":
			entries, err := os.ReadDir(directory)
			if err != nil && !os.IsNotExist(err) {
				detail(w, 503, "No se pudieron listar los backups.")
				return
			}
			rows := []backupFile{}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					continue
				}
				rows = append(rows, backupFile{Name: entry.Name(), Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339)})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt > rows[j].CreatedAt })
			send(w, 200, map[string]any{"rows": rows})
			return
		case "POST":
			if err := os.MkdirAll(directory, 0o700); err != nil {
				detail(w, 503, "No se pudo preparar el backup.")
				return
			}
			name := "custodia-" + current.UTC().Format("20060102-150405") + ".db"
			target := filepath.Join(directory, name)
			for suffix := 1; ; suffix++ {
				if _, err := os.Stat(target); os.IsNotExist(err) {
					break
				}
				target = filepath.Join(directory, "custodia-"+current.UTC().Format("20060102-150405")+"-"+strconv.Itoa(suffix)+".db")
			}
			if err := sqlitebackup.Backup(r.Context(), filepath.Join(h.config.DataDir, "state.db"), target); err != nil {
				detail(w, 503, "No se pudo crear el backup.")
				return
			}
			info, _ := os.Stat(target)
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			if err := h.store.RecordAudit(r.Context(), session.Actor(), "system_backup_created", map[string]any{"name": filepath.Base(target), "size": size}, current); err != nil {
				detail(w, 503, "No se pudo auditar el backup.")
				return
			}
			send(w, 201, backupFile{Name: filepath.Base(target), Size: size, CreatedAt: current.UTC().Format(time.RFC3339)})
			return
		default:
			detail(w, 405, "Operación no disponible.")
			return
		}
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/system/backups/")
	if name == "" || strings.ContainsAny(name, "/\\\"\r\n") || !strings.HasSuffix(name, ".db") {
		detail(w, 404, "Backup no encontrado.")
		return
	}
	if r.Method != "GET" {
		detail(w, 405, "La descarga usa GET.")
		return
	}
	file, err := os.Open(filepath.Join(directory, name))
	if err != nil {
		detail(w, 404, "Backup no encontrado.")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		detail(w, 404, "Backup no encontrado.")
		return
	}
	// A backup carries password hashes, encrypted secrets and session digests;
	// reading it is an auditable administrative action, not a silent download.
	if err := h.store.RecordAudit(r.Context(), session.Actor(), "system_backup_downloaded", map[string]any{"name": name, "size": info.Size()}, current); err != nil {
		detail(w, 503, "No se pudo auditar la descarga.")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, file)
}
