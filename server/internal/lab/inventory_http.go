package lab

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// serveInventoryAPI is the read-only HTTP boundary of the tool inventory:
//
//	GET /api/inventory/tools
//	GET /api/inventory/metrics/{metric_id}/records?connection={connection_id}&limit=N
//
// It returns true only for /api/inventory/... Every response is filtered by
// the principal's read grants before any metric is computed.
func (s *Store) serveInventoryAPI(w http.ResponseWriter, r *http.Request, current time.Time, principal Principal) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/inventory/") {
		return false
	}
	send := func(code int, value any) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(value)
	}
	fail := func(code int, detail string) { send(code, map[string]string{"detail": detail}) }
	if r.Method != "GET" {
		fail(405, "Inventario de sólo lectura.")
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/inventory/")
	if path == "tools" {
		tools, err := s.InventoryTools(r.Context(), principal)
		if err != nil {
			fail(503, "No se pudo consultar el inventario autorizado.")
			return true
		}
		send(200, map[string]any{"generated_at": current.UTC().Format(time.RFC3339), "tools": tools})
		return true
	}
	rest, found := strings.CutPrefix(path, "metrics/")
	metricID, records := strings.CutSuffix(rest, "/records")
	if !found || !records || metricID == "" || strings.Contains(metricID, "/") {
		fail(404, "Ruta de inventario desconocida.")
		return true
	}
	if unescaped, err := url.PathUnescape(metricID); err == nil {
		metricID = unescaped
	}
	connection := r.URL.Query().Get("connection")
	if connection == "" {
		fail(400, "Indicá la conexión (connection).")
		return true
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			fail(400, "Límite inválido.")
			return true
		}
		limit = parsed
	}
	response, err := s.InventoryMetricRecords(r.Context(), principal, metricID, connection, limit)
	if errors.Is(err, ErrSyncNotFound) {
		// A hidden connection is indistinguishable from a missing one.
		fail(404, "Métrica o conexión no encontrada.")
		return true
	}
	if err != nil {
		fail(503, "No se pudo consultar el detalle autorizado.")
		return true
	}
	send(200, response)
	return true
}
