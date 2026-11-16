package lab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkspacePreferencesAcceptKnownViewsAndRejectUnknown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s, p := newSyncStore(t, now)
	h := s.Handler("lab-key", func() time.Time { return now })

	code, body := syncRequest(t, h, "lab-key", "PUT", "/api/workspaces/preferences",
		`{"visible_workspaces":["inventory","person","inventory"],"hidden_workspaces":["safes"],"recent_workspaces":[]}`)
	if code != 200 {
		t.Fatal(code, body)
	}
	var stored Preferences
	if err := json.Unmarshal([]byte(body), &stored); err != nil {
		t.Fatal(err, body)
	}
	if len(stored.Visible) != 2 || stored.Visible[0] != "inventory" || stored.Visible[1] != "person" {
		t.Fatal(stored.Visible)
	}
	if len(stored.Hidden) != 1 || stored.Hidden[0] != "safes" {
		t.Fatal(stored.Hidden)
	}
	var actor string
	if err := s.DB.QueryRow("SELECT actor FROM lab_preferences WHERE actor=?", p.Actor()).Scan(&actor); err != nil || actor != p.Actor() {
		t.Fatal(err, actor)
	}
	code, body = syncRequest(t, h, "lab-key", "PUT", "/api/workspaces/preferences", `{"visible_workspaces":["not-a-workspace"]}`)
	if code != 403 || strings.Contains(body, "not-a-workspace") {
		t.Fatal("unknown workspace accepted or echoed", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/workspaces/preferences", "")
	if code != 200 || !strings.Contains(body, "inventory") {
		t.Fatal("stored preferences were lost", code, body)
	}
	_ = context.Background()
}
