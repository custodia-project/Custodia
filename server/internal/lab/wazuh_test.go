package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func newWazuhFixture(t *testing.T) *httptest.Server {
	t.Helper()
	agents := []map[string]any{
		{"id": "001", "name": "acme-app-01", "status": "active", "ip": "192.0.2.10", "version": "Wazuh v4.14.10", "group": []string{"default", "linux"}, "lastKeepAlive": "2026-11-16T11:55:00Z", "os": map[string]any{"name": "Ubuntu", "platform": "ubuntu", "version": "24.04"}},
		{"id": "002", "name": "acme-db-01", "status": "disconnected", "ip": "192.0.2.20", "group": []string{"default"}, "lastKeepAlive": "2026-11-01T09:00:00Z", "os": map[string]any{"name": "Debian", "platform": "debian", "version": "12"}},
		{"id": "003", "name": "acme-new-01", "status": "never_connected", "group": []string{}},
	}
	groups := []map[string]any{{"name": "default", "count": 2}, {"name": "linux", "count": 1}, {"name": "empty", "count": 0}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/agents":
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit < 1 || limit > len(agents) {
				limit = len(agents)
			}
			end := offset + limit
			if end > len(agents) {
				end = len(agents)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"affected_items": agents[offset:end], "total_affected_items": len(agents), "total_failed_items": 0}, "error": 0})
		case "/groups":
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit < 1 || limit > len(groups) {
				limit = len(groups)
			}
			end := offset + limit
			if end > len(groups) {
				end = len(groups)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"affected_items": groups[offset:end], "total_affected_items": len(groups), "total_failed_items": 0}, "error": 0})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestWazuhSyncAndDeclaredMetrics(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s, p := newSyncStore(t, now)
	server := newWazuhFixture(t)
	defer server.Close()
	for _, collection := range []string{"agents", "groups"} {
		if err := s.AddHTTPSource(ctx, "host", SyncSource{
			ConnectionID: "wazuh-" + collection, GroupID: "wazuh-acme", Connector: syncConnectorWazuh,
			BaseURL: server.URL, Collection: collection, Scope: "acme-wazuh",
			Generation: "wazuh-v1", PageSize: 2, MaxPages: 10, MaxRecords: 100,
		}, []string{p.Subject}, now); err != nil {
			t.Fatal(err)
		}
	}
	for index, collection := range []string{"agents", "groups"} {
		runID, err := s.EnqueueSyncRun(ctx, p, "wazuh-"+collection, "", now.Add(time.Duration(index)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ProcessNextSyncRun(ctx, "wazuh-worker", func() time.Time { return now.Add(time.Duration(index) * time.Minute) }); err != nil {
			t.Fatal(err)
		}
		run, err := s.SyncRun(ctx, p, runID)
		if err != nil || run.Status != "completed" || run.Received != 3 {
			t.Fatal(run, err)
		}
	}
	tools, err := s.InventoryTools(ctx, p)
	if err != nil || len(tools) != 1 || tools[0].Connector != syncConnectorWazuh {
		t.Fatal(tools, err)
	}
	values := map[string]float64{}
	for _, metric := range tools[0].Metrics {
		if metric.Value != nil {
			values[metric.ID] = *metric.Value
		}
	}
	expected := map[string]float64{
		"wazuh.agents.total.v1": 3, "wazuh.agents.active.v1": 1,
		"wazuh.agents.disconnected.v1": 1, "wazuh.agents.never_connected.v1": 1,
		"wazuh.agents.stale_7d.v1": 1, "wazuh.groups.total.v1": 3, "wazuh.groups.empty.v1": 1,
	}
	for id, value := range expected {
		if values[id] != value {
			t.Fatal(id, values[id], value, values)
		}
	}
}
