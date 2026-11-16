package lab

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"custodia/server/internal/connectors"
)

// serveSyncAPI is the HTTP boundary for the first read-only synchronization.
// It returns true only for /api/ingestion/... so the existing person routes
// remain untouched. The lab endpoint is not a provider-specific API.
func (s *Store) serveSyncAPI(w http.ResponseWriter, r *http.Request, current time.Time, principal Principal) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/ingestion/") {
		return false
	}
	send := func(code int, value any) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(value)
	}
	fail := func(code int, detail string) { send(code, map[string]string{"detail": detail}) }
	path := strings.TrimPrefix(r.URL.Path, "/api/ingestion/")
	switch {
	case path == "connectors":
		if r.Method != "GET" {
			fail(405, "Catálogo de sólo lectura.")
			return true
		}
		send(200, map[string]any{"rows": connectors.List()})
		return true
	case path == "connections":
		if r.Method != "POST" {
			fail(405, "Alta de conexiones por POST.")
			return true
		}
		if !s.IsAdministrator(r.Context(), principal) {
			fail(403, "Sólo un administrador puede conectar un tenant.")
			return true
		}
		var input struct {
			Connector       string   `json:"connector"`
			BaseURL         string   `json:"base_url"`
			Collection      string   `json:"collection"`
			Scope           string   `json:"scope"`
			Generation      string   `json:"generation"`
			AuthMode        string   `json:"auth_mode"`
			CredentialRef   string   `json:"credential_ref"`
			TokenURL        string   `json:"token_url"`
			ClientIDRef     string   `json:"client_id_ref"`
			ClientSecretRef string   `json:"client_secret_ref"`
			Scopes          []string `json:"scopes"`
			CAFile          string   `json:"ca_file"`
			ClientCertFile  string   `json:"client_cert_file"`
			ClientKeyFile   string   `json:"client_key_file"`
			PageSize        int      `json:"page_size"`
			MaxPages        int      `json:"max_pages"`
			MaxRecords      int      `json:"max_records"`
			Interval        int      `json:"interval_minutes"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			fail(400, "Configuración de conexión inválida.")
			return true
		}
		source, err := s.RegisterConnection(r.Context(), principal.Actor(), principal, SyncSource{
			Connector: input.Connector, BaseURL: input.BaseURL, Collection: input.Collection,
			Scope: input.Scope, Generation: input.Generation, AuthMode: input.AuthMode,
			CredentialRef: input.CredentialRef, TokenURL: input.TokenURL,
			ClientIDRef: input.ClientIDRef, ClientSecretRef: input.ClientSecretRef,
			Scopes: input.Scopes, CAFile: input.CAFile,
			ClientCertFile: input.ClientCertFile, ClientKeyFile: input.ClientKeyFile,
			PageSize: input.PageSize, MaxPages: input.MaxPages, MaxRecords: input.MaxRecords,
			Interval: input.Interval,
		}, current)
		if err != nil {
			if errors.Is(err, ErrDenied) {
				fail(403, "Sólo un administrador puede conectar un tenant.")
			} else {
				fail(400, "No se pudo registrar la conexión: "+safeConfigurationError(err))
			}
			return true
		}
		send(201, syncSourceJSON(source))
		return true
	case strings.HasPrefix(path, "connections/") && strings.HasSuffix(path, "/probe"):
		if r.Method != "POST" {
			fail(405, "Probe de conexiones por POST.")
			return true
		}
		connectionID := strings.TrimSuffix(strings.TrimPrefix(path, "connections/"), "/probe")
		if connectionID == "" || strings.Contains(connectionID, "/") {
			fail(404, "Conexión no encontrada.")
			return true
		}
		report, err := s.ProbeConnection(r.Context(), principal, connectionID, current)
		if err != nil {
			switch {
			case errors.Is(err, ErrDenied):
				fail(403, "Sólo un administrador puede probar la conexión.")
			case errors.Is(err, ErrSyncNotFound):
				fail(404, "Conexión no encontrada.")
			default:
				fail(400, "No se pudo probar la conexión: "+safeConfigurationError(err))
			}
			return true
		}
		send(200, map[string]any{"connection_id": connectionID, "report": report, "failed_endpoints": report.Failed()})
		return true
	case path == "runs":
		if r.Method != "GET" {
			fail(405, "Sincronización de sólo lectura en este recorrido.")
			return true
		}
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 100 {
				fail(400, "Límite de corridas inválido.")
				return true
			}
			limit = value
		}
		runs, err := s.SyncRuns(r.Context(), principal, limit)
		if err != nil {
			fail(503, "No se pudo consultar el historial autorizado.")
			return true
		}
		rows := make([]map[string]any, 0, len(runs))
		for _, run := range runs {
			rows = append(rows, syncRunJSON(run))
		}
		send(200, map[string]any{"rows": rows})
		return true
	case strings.HasPrefix(path, "runs/"):
		if r.Method != "GET" {
			fail(405, "Sincronización de sólo lectura en este recorrido.")
			return true
		}
		rest := strings.TrimPrefix(path, "runs/")
		runID, stages := rest, false
		if strings.HasSuffix(rest, "/stages") {
			runID, stages = strings.TrimSuffix(rest, "/stages"), true
		}
		if runID == "" || strings.Contains(runID, "/") {
			fail(404, "Corrida no encontrada.")
			return true
		}
		run, err := s.SyncRun(r.Context(), principal, runID)
		if err != nil {
			fail(404, "Corrida no encontrada.")
			return true
		}
		if stages {
			send(200, map[string]any{"rows": syncStageRows(run)})
			return true
		}
		send(200, syncRunJSON(run))
		return true
	case path == "schedules":
		if r.Method != "GET" {
			fail(405, "Cambiar la agenda aún no está migrado.")
			return true
		}
		schedules, err := s.SyncSchedules(r.Context(), principal, current)
		if err != nil {
			fail(503, "No se pudo consultar el estado autorizado.")
			return true
		}
		rows := make([]map[string]any, 0, len(schedules))
		for _, schedule := range schedules {
			rows = append(rows, syncScheduleJSON(schedule))
		}
		send(200, map[string]any{"rows": rows})
		return true
	case strings.HasPrefix(path, "schedules/"):
		rest := strings.TrimPrefix(path, "schedules/")
		if strings.HasSuffix(rest, "/runs") {
			connectionID := strings.TrimSuffix(rest, "/runs")
			if connectionID == "" || strings.Contains(connectionID, "/") {
				fail(404, "Conexión de sincronización no encontrada.")
				return true
			}
			if r.Method != "POST" {
				fail(405, "Usá POST para iniciar una sincronización.")
				return true
			}
			if err := validateEmptySyncBody(w, r); err != nil {
				fail(400, "El pedido de sincronización no admite opciones en este recorrido.")
				return true
			}
			idempotency, err := idempotencyKey(r)
			if err != nil {
				fail(400, "La clave de idempotencia no es válida.")
				return true
			}
			runID, err := s.EnqueueSyncRun(r.Context(), principal, connectionID, idempotency, current)
			if errors.Is(err, ErrDenied) {
				if sources, listErr := s.SyncSourcesFor(r.Context(), principal); listErr == nil {
					for _, source := range sources {
						if source.ConnectionID == connectionID {
							fail(403, "Permiso para iniciar esta sincronización insuficiente.")
							return true
						}
					}
				}
				fail(404, "Conexión de sincronización no encontrada.")
				return true
			}
			if errors.Is(err, ErrSyncActive) {
				if _, err = s.requeueExpiredSyncRuns(r.Context(), connectionID, principal.Actor(), current); err != nil {
					fail(503, "No se pudo revisar la cola de sincronización.")
					return true
				}
				active, activeErr := s.activeSyncRun(r.Context(), connectionID)
				if activeErr != nil || active.Status != "queued" {
					fail(409, "La conexión ya tiene una corrida en cola o en curso.")
					return true
				}
				runID = active.RunID
			} else if err != nil {
				fail(503, "No se pudo encolar la sincronización.")
				return true
			}
			// The worker owns the execution; the API only returns the durable
			// queued run so the UI can poll it.
			send(202, map[string]any{"run_id": runID, "status": "queued"})
			return true
		}
		connectionID := rest
		if connectionID == "" || strings.Contains(connectionID, "/") {
			fail(404, "Conexión de sincronización no encontrada.")
			return true
		}
		if r.Method != "PUT" {
			fail(405, "Usá PUT para cambiar la agenda.")
			return true
		}
		interval, err := syncScheduleBody(w, r)
		if err != nil {
			fail(400, err.Error())
			return true
		}
		if err = s.SetSyncSchedule(r.Context(), principal, connectionID, interval, current); errors.Is(err, ErrDenied) {
			fail(403, "Permiso para administrar esta agenda insuficiente.")
			return true
		} else if err != nil {
			fail(503, "No se pudo guardar la agenda.")
			return true
		}
		send(200, map[string]any{"updated": true, "task_key": connectionID, "interval_minutes": interval})
		return true
	case path == "all/runs":
		if r.Method != "POST" {
			fail(405, "Usá POST para encolar la sincronización.")
			return true
		}
		if err := validateAllSyncBody(w, r); err != nil {
			fail(400, "El pedido de sincronización no es válido.")
			return true
		}
		scope := r.URL.Query().Get("scope")
		if scope != "" && scope != "all" && scope != "cyberark" {
			fail(400, "Alcance de sincronización inválido.")
			return true
		}
		sources, err := s.SyncSourcesFor(r.Context(), principal)
		if err != nil {
			fail(503, "No se pudo consultar el alcance de sincronización.")
			return true
		}
		runs := []map[string]any{}
		anyRunnable := false
		for _, source := range sources {
			allowed, runErr := s.SyncCanRun(r.Context(), principal, source.ConnectionID)
			if runErr != nil {
				fail(503, "No se pudo consultar el permiso de sincronización.")
				return true
			}
			if !allowed {
				continue
			}
			anyRunnable = true
			if scope == "cyberark" && syncSourceSystem(source) != "cyberark" {
				continue
			}
			runID, enqueueErr := s.EnqueueSyncRun(r.Context(), principal, source.ConnectionID, "", current)
			if errors.Is(enqueueErr, ErrSyncActive) {
				continue
			}
			if enqueueErr != nil {
				continue
			}
			runs = append(runs, map[string]any{
				"run_id": runID, "status": "queued", "source_system": syncSourceSystem(source),
				"task_key": source.ConnectionID,
			})
		}
		if !anyRunnable {
			fail(403, "Permiso para iniciar sincronizaciones insuficiente.")
			return true
		}
		send(202, map[string]any{"runs": runs})
		return true
	case strings.HasPrefix(path, "sources/") && strings.Contains(path, "/stages/"):
		rest := strings.TrimPrefix(path, "sources/")
		parts := strings.Split(rest, "/stages/")
		if len(parts) != 2 || (parts[1] != "history" && parts[1] != "latest") {
			fail(404, "Recorrido de etapas no encontrado.")
			return true
		}
		if r.Method != "GET" {
			fail(405, "Lectura de etapas de sólo lectura.")
			return true
		}
		if parts[0] != "netskope" && parts[0] != "cyberark" {
			send(200, map[string]any{"rows": []any{}})
			return true
		}
		if parts[1] == "history" {
			limit := 10
			if raw := r.URL.Query().Get("runs"); raw != "" {
				value, err := strconv.Atoi(raw)
				if err != nil || value < 1 || value > 50 {
					fail(400, "Límite de etapas inválido.")
					return true
				}
				limit = value
			}
			runs, err := s.SyncRuns(r.Context(), principal, 100)
			if err != nil {
				fail(503, "No se pudo consultar el historial autorizado.")
				return true
			}
			send(200, map[string]any{"rows": syncStageHistoryJSON(runs, parts[0], limit)})
			return true
		}
		schedules, err := s.SyncSchedules(r.Context(), principal, current)
		if err != nil {
			fail(503, "No se pudo consultar la frescura autorizada.")
			return true
		}
		send(200, map[string]any{"rows": syncStageLatestJSON(schedules, parts[0])})
		return true
	case strings.HasPrefix(path, "connections/") && strings.HasSuffix(path, "/records"):
		connectionID := strings.TrimSuffix(strings.TrimPrefix(path, "connections/"), "/records")
		if connectionID == "" || strings.Contains(connectionID, "/") {
			fail(404, "Conexión de sincronización no encontrada.")
			return true
		}
		if r.Method != "GET" {
			fail(405, "Lectura de registros de sólo lectura.")
			return true
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 1000 {
				fail(400, "Límite de registros inválido.")
				return true
			}
			limit = value
		}
		records, err := s.SyncRecords(r.Context(), principal, connectionID, r.URL.Query().Get("collection"), r.URL.Query().Get("status"), limit)
		if errors.Is(err, ErrDenied) {
			fail(403, "Alcance de la conexión insuficiente.")
			return true
		}
		if err != nil {
			fail(400, "No se pudieron consultar los registros autorizados.")
			return true
		}
		rows := make([]map[string]any, 0, len(records))
		for _, record := range records {
			rows = append(rows, map[string]any{
				"external_id": record.ExternalID, "attributes": record.Attributes, "status": record.Status,
				"first_run_id": record.FirstRunID, "last_run_id": record.LastRunID, "scope": record.Scope,
				"generation": record.Generation, "updated_at": record.UpdatedAt, "retired_at": optional(record.RetiredAt),
				"retired_by_run_id": optional(record.RetiredByRunID),
			})
		}
		send(200, map[string]any{"connection_id": connectionID, "rows": rows})
		return true
	case path == "schedule":
		if r.Method != "GET" {
			fail(405, "Cambiar la agenda aún no está migrado.")
			return true
		}
		send(200, map[string]any{"enabled": false, "interval_minutes": 0, "last_scheduled_at": nil, "next_scheduled_at": nil})
		return true
	default:
		fail(404, "Recorrido de sincronización aún no migrado.")
		return true
	}
}

func optional(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func newSyncWorkerID() string {
	id, err := newRunID()
	if err != nil {
		return "lab-sync-unknown"
	}
	return "lab-sync-" + id
}

func syncRunJSON(run SyncRun) map[string]any {
	details := []map[string]string{}
	if run.Status == "failed" && run.ErrorCode != "" {
		details = append(details, map[string]string{"error": run.ErrorDetail})
	}
	errorCount := 0
	if run.Status == "failed" {
		errorCount = 1
	} else if run.Status == "partial" {
		errorCount = len(run.Warnings)
		if errorCount == 0 {
			errorCount = 1
		}
	}
	receivedSafes, receivedMembers := 0, 0
	switch run.Collection {
	case "safes":
		receivedSafes = run.Received
	case "safe_members":
		receivedMembers = run.Received
	}
	return map[string]any{
		"run_id": run.RunID, "source_system": syncSourceSystemForConnector(run.Connector), "source_file": syncSourceFile(run.Connector, run.ConnectionID),
		"task_key": run.ConnectionID, "connection_id": run.ConnectionID, "collection": run.Collection,
		"scope": run.Scope, "generation": run.Generation, "status": run.Status,
		"enqueued_at": run.EnqueuedAt, "started_at": optional(run.StartedAt), "completed_at": optional(run.CompletedAt),
		"status_message": run.StatusMessage, "heartbeat_at": optional(run.HeartbeatAt), "lease_expires_at": optional(run.LeaseExpires),
		"attempt_count": run.AttemptCount, "expected_safe_count": nil, "received_safe_count": receivedSafes, "received_member_count": receivedMembers,
		"received_object_count": run.Received, "received_relationship_count": 0, "retired_object_count": run.Retired,
		"retired_relationship_count": 0, "retained_object_count": run.Retained, "duplicate_row_count": run.DuplicateRows,
		"pages": run.Pages, "error_count": errorCount, "error_details": details, "error_code": run.ErrorCode,
		"error_detail": run.ErrorDetail, "warnings": run.Warnings, "triggered_by": run.RequestedBy,
		"safes_created": 0, "safes_updated": 0, "safes_retired": 0, "members_created": 0, "members_updated": 0,
		"members_retired": 0, "pam_synced": false,
	}
}

func syncStageRows(run SyncRun) []map[string]any {
	status := "completed"
	required := true
	if run.Status == "failed" {
		status = "failed"
	} else if run.Status == "partial" {
		// The received pages were written, but the layer did not complete and
		// no absence was reconciled. Treating it as an optional failed layer
		// keeps the UI from claiming the run wrote nothing.
		status, required = "failed", false
	} else if run.Status == "queued" || run.Status == "running" {
		status, required = "skipped", false
	}
	var duration int64
	if run.StartedAt != "" && run.CompletedAt != "" {
		start, errStart := time.Parse(time.RFC3339, run.StartedAt)
		end, errEnd := time.Parse(time.RFC3339, run.CompletedAt)
		if errStart == nil && errEnd == nil && !end.Before(start) {
			duration = end.Sub(start).Milliseconds()
		}
	}
	var itemCount any
	if status != "skipped" && run.Status != "failed" {
		itemCount = run.Received
	}
	var detail any
	if run.ErrorDetail != "" {
		detail = run.ErrorDetail
	} else if len(run.Warnings) > 0 {
		detail = strings.Join(run.Warnings, ", ")
	}
	var errorKind any
	if run.ErrorCode != "" {
		errorKind = run.ErrorCode
	}
	return []map[string]any{{
		"stage": "extract", "required": required, "status": status, "item_count": itemCount,
		"duration_ms": duration, "error_kind": errorKind, "detail": detail,
	}}
}

func syncScheduleJSON(schedule SyncSchedule) map[string]any {
	intervalSource := "global_default"
	var adminOverride any
	if schedule.IntervalMinutes > 0 {
		intervalSource = "administrative"
		adminOverride = schedule.IntervalMinutes
	}
	return map[string]any{
		"task_key": schedule.ConnectionID, "source_system": schedule.SourceSystem,
		"source_file": syncSourceFile(schedule.Connector, schedule.ConnectionID), "configured": schedule.Enabled,
		"active": schedule.Active, "interval_minutes": schedule.IntervalMinutes, "interval_source": intervalSource,
		"admin_override": adminOverride, "updated_at": nil, "last_success_at": optional(schedule.LastSuccessAt),
		"last_attempt_at": optional(schedule.LastAttemptAt), "last_status": optional(schedule.LastStatus),
		"consecutive_failures": schedule.ConsecutiveFailure, "freshness": schedule.Freshness,
		"intervals_since_success": schedule.IntervalsSince, "stale_after_intervals": schedule.StaleAfterIntervals,
		"next_scheduled_at": optional(schedule.NextScheduledAt),
		"collection":        schedule.Collection, "scope": schedule.Scope,
	}
}

func syncStageHistoryJSON(runs []SyncRun, source string, limit int) []map[string]any {
	rows := []map[string]any{}
	for _, run := range runs {
		if syncSourceSystemForConnector(run.Connector) != source {
			continue
		}
		if run.Status != "completed" && run.Status != "partial" && run.Status != "failed" {
			continue
		}
		if run.CompletedAt == "" {
			continue
		}
		if len(rows) >= limit {
			break
		}
		status := "failed"
		if run.Status == "completed" {
			status = "completed"
		}
		var itemCount any
		if run.Status != "failed" {
			itemCount = run.Received
		}
		var duration int64
		if run.StartedAt != "" {
			if start, errStart := time.Parse(time.RFC3339, run.StartedAt); errStart == nil {
				if end, errEnd := time.Parse(time.RFC3339, run.CompletedAt); errEnd == nil && !end.Before(start) {
					duration = end.Sub(start).Milliseconds()
				}
			}
		}
		rows = append(rows, map[string]any{
			"run_id": run.RunID, "completed_at": run.CompletedAt, "stage": "extract",
			"status": status, "item_count": itemCount, "duration_ms": duration,
		})
	}
	return rows
}

func syncStageLatestJSON(schedules []SyncSchedule, source string) []map[string]any {
	rows := []map[string]any{}
	for _, schedule := range schedules {
		if schedule.SourceSystem != source || schedule.LastAttemptAt == "" || schedule.Active {
			continue
		}
		status := "failed"
		if schedule.LastStatus == "completed" {
			status = "completed"
		}
		rows = append(rows, map[string]any{
			"stage": "extract", "last_success_at": optional(schedule.LastSuccessAt),
			"last_status": status, "last_attempt_at": schedule.LastAttemptAt,
		})
	}
	return rows
}

func syncScheduleBody(w http.ResponseWriter, r *http.Request) (int, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	var payload struct {
		IntervalMinutes *float64 `json:"interval_minutes"`
	}
	if err := decoder.Decode(&payload); err != nil {
		return 0, errors.New("La agenda requiere interval_minutes.")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, errors.New("La agenda tiene datos adicionales.")
	}
	if payload.IntervalMinutes == nil {
		return 0, nil
	}
	value := *payload.IntervalMinutes
	if value != float64(int(value)) || (value != 0 && (value < 5 || value > 525600)) {
		return 0, errors.New("Usá 0 para deshabilitar o un intervalo de al menos 5 minutos.")
	}
	return int(value), nil
}

func validateAllSyncBody(w http.ResponseWriter, r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil && err != io.EOF {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing sync options")
	}
	return nil
}

func validateEmptySyncBody(w http.ResponseWriter, r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	var payload struct{}
	if err := decoder.Decode(&payload); err != nil && err != io.EOF {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing sync options")
	}
	return nil
}

func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 128 || strings.ContainsAny(values[0], "\r\n") {
		return "", errors.New("invalid idempotency key")
	}
	return values[0], nil
}
