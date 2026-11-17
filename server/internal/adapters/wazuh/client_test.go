package wazuh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func fixture(t *testing.T) *httptest.Server {
	t.Helper()
	agents := []map[string]any{
		{"id": "001", "name": "acme-app-01", "status": "active", "ip": "192.0.2.10", "registerIP": "any", "version": "Wazuh v4.14.10", "group": []string{"default", "linux"}, "lastKeepAlive": "2026-11-16T11:55:00Z", "dateAdd": "2026-10-01T09:00:00Z", "manager": "acme-wazuh", "node_name": "master", "os": map[string]any{"name": "Ubuntu", "platform": "ubuntu", "version": "24.04", "arch": "x86_64"}},
		{"id": "002", "name": "acme-db-01", "status": "disconnected", "ip": "192.0.2.20", "version": "Wazuh v4.13.0", "group": []string{"default"}, "lastKeepAlive": "2026-11-10T09:00:00Z", "os": map[string]any{"name": "Debian", "platform": "debian", "version": "12"}},
		{"id": "003", "name": "acme-new-01", "status": "never_connected", "group": []string{}},
	}
	groups := []map[string]any{
		{"name": "default", "count": 3},
		{"name": "linux", "count": 1},
		{"name": "empty", "count": 0},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 1 {
			limit = 1
		}
		end := offset + limit
		if end > len(agents) {
			end = len(agents)
		}
		page := agents[offset:end]
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"affected_items": page, "total_affected_items": len(agents), "total_failed_items": 0}, "error": 0})
	})
	mux.HandleFunc("/groups", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"affected_items": groups, "total_affected_items": len(groups), "total_failed_items": 0}, "error": 0})
	})
	return httptest.NewServer(mux)
}

func TestWazuhAgentsPaginationAndAllowlist(t *testing.T) {
	server := fixture(t)
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	cursor, seen := "", map[string]bool{}
	for pages := 0; cursor != "" || pages == 0; pages++ {
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		page, err := client.FetchPage(context.Background(), CollectionAgents, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if page.Expected != 3 {
			t.Fatal(page.Expected)
		}
		for _, object := range page.Objects {
			seen[object.ExternalID] = true
			if object.Attributes["secret"] != "" || object.Attributes["properties"] != "" {
				t.Fatal("forbidden attribute emitted")
			}
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 || !seen["001"] || !seen["002"] || !seen["003"] {
		t.Fatal(seen)
	}
}

func TestWazuhAgentNormalization(t *testing.T) {
	server := fixture(t)
	defer server.Close()
	client, _ := New(server.URL, Options{})
	page, err := client.FetchPage(context.Background(), CollectionAgents, "0", 1)
	if err != nil {
		t.Fatal(err)
	}
	agent := page.Objects[0]
	if agent.Attributes["status"] != "active" || agent.Attributes["os_name"] != "Ubuntu" || agent.Attributes["group_count"] != "2" || agent.Attributes["groups"] != "default,linux" {
		t.Fatal(agent.Attributes)
	}
	if agent.Attributes["last_keep_alive"] != "2026-11-16T11:55:00Z" {
		t.Fatal(agent.Attributes)
	}
	page, err = client.FetchPage(context.Background(), CollectionGroups, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 3 || page.Objects[2].Attributes["agent_count"] != "0" || page.NextCursor != "" {
		t.Fatal(page)
	}
}

func TestWazuhRejectsBadObjectsAndTotals(t *testing.T) {
	for name, body := range map[string]string{
		"bad status": `{"data":{"affected_items":[{"id":"1","name":"x","status":"unknown"}],"total_affected_items":1,"total_failed_items":0},"error":0}`,
		"no name":    `{"data":{"affected_items":[{"id":"1","status":"active"}],"total_affected_items":1,"total_failed_items":0},"error":0}`,
		"bad total":  `{"data":{"affected_items":[{"id":"1","name":"x","status":"active"},{"id":"2","name":"y","status":"active"}],"total_affected_items":1,"total_failed_items":0},"error":0}`,
		"failed":     `{"data":{"affected_items":[],"total_affected_items":0,"total_failed_items":1},"error":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			}))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.FetchPage(context.Background(), CollectionAgents, "", 10); err == nil {
				t.Fatal("invalid Wazuh payload accepted")
			}
		})
	}
}

func TestWazuhRejectsRemoteHTTP(t *testing.T) {
	if _, err := New("http://wazuh.example:55000", Options{AllowRemote: true}); err == nil {
		t.Fatal("remote HTTP accepted")
	}
	if _, err := New("https://wazuh.example:55000", Options{AllowRemote: true}); err != nil {
		t.Fatal(err)
	}
}
