package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

type inventoryToolsBody struct {
	Tools []struct {
		ConnectionID string `json:"connection_id"`
		Connector    string `json:"connector"`
		Name         string `json:"name"`
		Completeness string `json:"completeness"`
		LastRun      *struct {
			Status string `json:"status"`
		} `json:"last_run"`
		Collections []struct {
			Collection   string `json:"collection"`
			ConnectionID string `json:"connection_id"`
			State        string `json:"state"`
			Records      *int   `json:"records"`
		} `json:"collections"`
		Metrics []inventoryMetricBody `json:"metrics"`
	} `json:"tools"`
}

type inventoryMetricBody struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Value       *float64 `json:"value"`
	Partial     bool     `json:"partial"`
	Reason      string   `json:"reason"`
	Numerator   *int     `json:"numerator"`
	Denominator *int     `json:"denominator"`
	DataAsOf    string   `json:"data_as_of"`
	Sources     []struct {
		Collection string `json:"collection"`
		RunID      string `json:"run_id"`
		Status     string `json:"status"`
	} `json:"sources"`
}

type inventoryRecordsBody struct {
	Metric  inventoryMetricBody `json:"metric"`
	Total   int                 `json:"total"`
	Records []struct {
		ExternalID string `json:"external_id"`
		Collection string `json:"collection"`
	} `json:"records"`
}

const (
	metricSafes          = "cyberark-pam.safes.total.v1"
	metricSafesNoMembers = "cyberark-pam.safes.without_members.v1"
	metricAccounts       = "cyberark-pam.accounts.total.v1"
	metricAutoDisabled   = "cyberark-pam.accounts.auto_management_disabled.v1"
	metricAutoRatio      = "cyberark-pam.accounts.auto_management_disabled_ratio.v1"
	metricLastFailed     = "cyberark-pam.accounts.last_change_failed.v1"
	metricInactive       = "cyberark-pam.platforms.inactive.v1"
	metricPSMUnresolved  = "cyberark-pam.platforms.psm_unresolved.v1"
)

func inventoryTools(t *testing.T, h http.Handler, token string) inventoryToolsBody {
	t.Helper()
	code, body := syncRequest(t, h, token, "GET", "/api/inventory/tools", "")
	if code != 200 {
		t.Fatal(code, body)
	}
	var result inventoryToolsBody
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err, body)
	}
	return result
}

func inventoryMetric(t *testing.T, tools inventoryToolsBody, connection, id string) inventoryMetricBody {
	t.Helper()
	for _, tool := range tools.Tools {
		if tool.ConnectionID != connection {
			continue
		}
		for _, metric := range tool.Metrics {
			if metric.ID == id {
				return metric
			}
		}
	}
	t.Fatalf("metric %s missing for %s: %+v", id, connection, tools)
	return inventoryMetricBody{}
}

func inventoryRecords(t *testing.T, h http.Handler, token, id, connection string) inventoryRecordsBody {
	t.Helper()
	code, body := syncRequest(t, h, token, "GET", "/api/inventory/metrics/"+id+"/records?connection="+connection, "")
	if code != 200 {
		t.Fatal(code, body)
	}
	var result inventoryRecordsBody
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err, body)
	}
	return result
}

func recordIDs(body inventoryRecordsBody) []string {
	ids := []string{}
	for _, record := range body.Records {
		ids = append(ids, record.ExternalID)
	}
	return ids
}

func expectValue(t *testing.T, metric inventoryMetricBody, status string, value float64, partial bool) {
	t.Helper()
	if metric.Status != status || metric.Value == nil || *metric.Value != value || metric.Partial != partial {
		t.Fatalf("%s: want %s %v partial=%v, got %+v", metric.ID, status, value, partial, metric)
	}
}

func expectUnavailable(t *testing.T, metric inventoryMetricBody, reason string) {
	t.Helper()
	if metric.Status != "unavailable" || metric.Value != nil || metric.Reason != reason {
		t.Fatalf("%s: want unavailable %s, got %+v", metric.ID, reason, metric)
	}
}

// newInventoryPAMLab connects one synthetic PAM tenant as the group
// pam-tenant with safes, members, accounts and platforms. The PSM link
// collection is added by the test when it needs it.
func newInventoryPAMLab(t *testing.T) (*Store, Principal, *fakePAMSource, func(string, string), time.Time) {
	t.Helper()
	s, p, server, fake, now := newPAMLab(t)
	fake.setMembers("Alpha", map[string]pamMemberPage{"": {values: []map[string]any{
		pamMember(101, "a1", "Alice", "User", map[string]any{"useAccounts": true}),
		pamMember(101, "a2", "Group A", "Group", map[string]any{"listAccounts": true}),
	}, count: 2}})
	fake.setMembers("Beta", map[string]pamMemberPage{"": {values: []map[string]any{pamMember(102, "b1", "Carol", "User", map[string]any{})}, count: 1}})
	fake.setMembers("Gamma", map[string]pamMemberPage{"": {values: []map[string]any{}, count: 0}})
	fake.setAccounts(map[string]pamMemberPage{
		"": {
			values: []map[string]any{
				pamAccount("acct-a1", "svc-alpha", "Alpha", "Unix", map[string]any{"automaticManagementEnabled": true, "status": "Success"}),
				pamAccount("acct-b1", "svc-beta", "Beta", "Windows", map[string]any{"automaticManagementEnabled": false, "status": "failure"}),
			},
			next:  "/API/Accounts?limit=2&offset=2",
			count: 4,
		},
		"2": {
			values: []map[string]any{
				pamAccount("acct-a2", "svc-audit", "Alpha", "Unix", map[string]any{"automaticManagementEnabled": true, "status": "in_process"}),
				pamAccount("acct-c1", "svc-gamma", "Gamma", "Unix", nil),
			},
			count: 4,
		},
	})
	fake.setPlatforms([]map[string]any{
		pamPlatform("WinDomain", "Windows Domain", true, "PSMServer_acme"),
		pamPlatform("Unix", "Unix", true, "PSMSERVER_ACME"),
		pamPlatform("Database", "Database", true, "PSMServer_missing"),
		pamPlatform("Legacy", "Legacy", true, "PSMSERVER_CASE"),
		pamPlatform("Retired", "Retired", false, ""),
	}, 5)
	fake.setPSMServers([]map[string]any{
		pamPSMServer("PSMServer_acme", "PSM Acme", "psm.acme.example"),
		pamPSMServer("PSMServer_Case", "Case A", "case-a.acme.example"),
		pamPSMServer("PSMServer_case", "Case B", "case-b.acme.example"),
	}, 3)
	ctx := context.Background()
	add := func(connection, collection string) {
		t.Helper()
		if err := s.AddHTTPSource(ctx, "host", SyncSource{
			ConnectionID: connection, Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
			Collection: collection, Scope: "synthetic-" + collection + "-v1", Generation: "cyberark-pam-safes-v1",
			PageSize: 2, MaxPages: 10, MaxRecords: 100,
		}, []string{p.Subject}, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec("UPDATE lab_sync_sources SET group_id='pam-tenant' WHERE connection_id=?", connection); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec("UPDATE lab_sync_sources SET group_id='pam-tenant' WHERE connection_id='pam-safes'"); err != nil {
		t.Fatal(err)
	}
	add("pam-members", "safe_members")
	add("pam-accounts", "accounts")
	add("pam-platforms", "platforms")
	return s, p, fake, add, now
}

func runInventorySource(t *testing.T, s *Store, p Principal, connection, status string, now time.Time) {
	t.Helper()
	runID, err := s.EnqueueSyncRun(context.Background(), p, connection, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if run := processPAMRun(t, s, p, runID, now); run.Status != status {
		t.Fatalf("%s: want %s, got %+v", connection, status, run)
	}
}

func TestInventoryMetricsOverPAMFixture(t *testing.T) {
	s, p, fake, add, now := newInventoryPAMLab(t)
	h := s.Handler("lab-key", func() time.Time { return now })

	// Connected but never run: every metric is unavailable, never zero.
	tools := inventoryTools(t, h, "lab-key")
	if len(tools.Tools) != 1 || tools.Tools[0].ConnectionID != "pam-tenant" || tools.Tools[0].Completeness != "unpublished" || len(tools.Tools[0].Collections) != 4 {
		t.Fatalf("%+v", tools)
	}
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricSafes), "no_publication")
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricAccounts), "no_publication")
	// platform_psm_links is not configured in this connection.
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricPSMUnresolved), "collection_unavailable")
	if body := inventoryRecords(t, h, "lab-key", metricSafes, "pam-tenant"); body.Total != 0 || len(body.Records) != 0 || body.Metric.Value != nil {
		t.Fatal("unavailable drill-down returned records", body)
	}

	for _, connection := range []string{"pam-safes", "pam-members", "pam-accounts", "pam-platforms"} {
		runInventorySource(t, s, p, connection, "completed", now)
	}
	tools = inventoryTools(t, h, "lab-key")
	if tools.Tools[0].Completeness != "complete" || tools.Tools[0].LastRun == nil || tools.Tools[0].LastRun.Status != "completed" {
		t.Fatalf("%+v", tools.Tools[0])
	}
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricSafes), "available", 3, false)
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricSafesNoMembers), "available", 1, false)
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricAccounts), "available", 4, false)
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricAutoDisabled), "available", 1, false)
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricLastFailed), "available", 1, false)
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricInactive), "available", 1, false)
	ratio := inventoryMetric(t, tools, "pam-tenant", metricAutoRatio)
	expectValue(t, ratio, "available", 33.3, false)
	if *ratio.Numerator != 1 || *ratio.Denominator != 3 {
		t.Fatal("ratio terms", ratio)
	}
	safes := inventoryMetric(t, tools, "pam-tenant", metricSafes)
	if safes.DataAsOf != now.Format(time.RFC3339) || len(safes.Sources) != 1 || safes.Sources[0].Status != "completed" || safes.Sources[0].RunID == "" {
		t.Fatal("data date and run status", safes)
	}

	// The drill-down returns exactly the records that compose each value.
	for id, want := range map[string][]string{
		metricSafes:          {"101", "102", "103"},
		metricSafesNoMembers: {"103"},
		metricAccounts:       {"101:acct-a1", "102:acct-b1", "101:acct-a2", "103:acct-c1"},
		metricAutoDisabled:   {"102:acct-b1"},
		metricLastFailed:     {"102:acct-b1"},
		metricAutoRatio:      {"102:acct-b1"},
		metricInactive:       {"Retired"},
	} {
		body := inventoryRecords(t, h, "lab-key", id, "pam-tenant")
		counted := float64(len(want))
		if id == metricAutoRatio {
			counted = float64(*body.Metric.Numerator)
		}
		if got := recordIDs(body); !slices.Equal(got, want) || body.Total != len(want) || counted != float64(len(want)) || (id != metricAutoRatio && *body.Metric.Value != counted) {
			t.Fatalf("%s: want %v, got %v (%+v)", id, want, got, body)
		}
	}

	// Adding the PSM link collection to the same connection makes it available.
	add("pam-psm-links", "platform_psm_links")
	runInventorySource(t, s, p, "pam-psm-links", "completed", now)
	tools = inventoryTools(t, h, "lab-key")
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricPSMUnresolved), "available", 2, false)
	if got := recordIDs(inventoryRecords(t, h, "lab-key", metricPSMUnresolved, "pam-tenant")); !slices.Equal(got, []string{"Database:PSMServer_missing", "Legacy:PSMSERVER_CASE"}) {
		t.Fatal("psm unresolved records", got)
	}

	// A partial accounts run keeps the baseline: counts are marked partial,
	// the proportion is withheld because its denominator would be partial.
	fake.setAccountStatus("2", http.StatusInternalServerError)
	runInventorySource(t, s, p, "pam-accounts", "partial", now.Add(time.Minute))
	tools = inventoryTools(t, h, "lab-key")
	if tools.Tools[0].Completeness != "partial" {
		t.Fatal("completeness", tools.Tools[0].Completeness)
	}
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricAccounts), "partial", 4, true)
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricAutoDisabled), "partial", 1, true)
	ratio = inventoryMetric(t, tools, "pam-tenant", metricAutoRatio)
	if ratio.Status != "partial" || ratio.Value != nil || !ratio.Partial || ratio.Reason != "partial_denominator" {
		t.Fatal("ratio over a partial publication", ratio)
	}
	if body := inventoryRecords(t, h, "lab-key", metricAutoRatio, "pam-tenant"); body.Total != 0 || len(body.Records) != 0 {
		t.Fatal("withheld ratio returned records", body)
	}
	if body := inventoryRecords(t, h, "lab-key", metricAccounts, "pam-tenant"); body.Total != 4 || !body.Metric.Partial {
		t.Fatal("partial drill-down", body)
	}
	// Other collections are untouched by the accounts run.
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricSafes), "available", 3, false)

	// A partial members run withholds "Safes without members".
	fake.setMemberStatus("Beta", "", http.StatusInternalServerError)
	runInventorySource(t, s, p, "pam-members", "partial", now.Add(2*time.Minute))
	tools = inventoryTools(t, h, "lab-key")
	noMembers := inventoryMetric(t, tools, "pam-tenant", metricSafesNoMembers)
	if noMembers.Status != "partial" || noMembers.Value != nil || noMembers.Reason != "partial_reference" {
		t.Fatal("count_without over a partial reference", noMembers)
	}

	// A later failed run keeps the last publication as the data source.
	fake.setStatus("", http.StatusForbidden)
	runInventorySource(t, s, p, "pam-safes", "failed", now.Add(3*time.Minute))
	tools = inventoryTools(t, h, "lab-key")
	safes = inventoryMetric(t, tools, "pam-tenant", metricSafes)
	expectValue(t, safes, "available", 3, false)
	if safes.DataAsOf != now.Format(time.RFC3339) {
		t.Fatal("failed run replaced the data date", safes)
	}
	for _, collection := range tools.Tools[0].Collections {
		if collection.Collection == "safes" && collection.State != "completed" {
			t.Fatal("safes state", collection)
		}
	}
	if tools.Tools[0].LastRun == nil || tools.Tools[0].LastRun.Status != "failed" {
		t.Fatal("tool last run must report the failed attempt", tools.Tools[0].LastRun)
	}

	// Unknown routes, metrics of another connector and writes are refused.
	for _, path := range []string{"/api/inventory/metrics/" + metricSafes + "/records", "/api/inventory/metrics/unknown.v1/records?connection=pam-tenant", "/api/inventory/other"} {
		if code, body := syncRequest(t, h, "lab-key", "GET", path, ""); code != 400 && code != 404 {
			t.Fatal(path, code, body)
		}
	}
	if code, _ := syncRequest(t, h, "lab-key", "POST", "/api/inventory/tools", "{}"); code != 405 {
		t.Fatal("write accepted", code)
	}
}

func TestInventoryMetricsUseTheLastPublicationScope(t *testing.T) {
	s, p, fake, _, now := newInventoryPAMLab(t)
	h := s.Handler("lab-key", func() time.Time { return now })
	runInventorySource(t, s, p, "pam-safes", "completed", now)
	expectValue(t, inventoryMetric(t, inventoryTools(t, h, "lab-key"), "pam-tenant", metricSafes), "available", 3, false)

	// A completed run with a new scope starts a new baseline: rows from the
	// previous scope stay active but are not part of this publication, so the
	// metric must not count them.
	if _, err := s.DB.Exec("UPDATE lab_sync_sources SET scope='synthetic-safes-v2' WHERE connection_id='pam-safes'"); err != nil {
		t.Fatal(err)
	}
	fake.set([]map[string]any{pamSafe(101, "Alpha", "alpha")}, nil)
	runInventorySource(t, s, p, "pam-safes", "completed", now.Add(time.Minute))
	tools := inventoryTools(t, h, "lab-key")
	safes := inventoryMetric(t, tools, "pam-tenant", metricSafes)
	expectValue(t, safes, "available", 1, false)
	if safes.DataAsOf != now.Add(time.Minute).Format(time.RFC3339) {
		t.Fatal("data date", safes.DataAsOf)
	}
	if got := recordIDs(inventoryRecords(t, h, "lab-key", metricSafes, "pam-tenant")); !slices.Equal(got, []string{"101"}) {
		t.Fatal("old-scope records leaked into the drill-down", got)
	}
	for _, collection := range tools.Tools[0].Collections {
		if collection.Collection == "safes" && (collection.Records == nil || *collection.Records != 1) {
			t.Fatal("collection count", collection)
		}
	}
}

func TestInventoryReportsAnUnfinishedLastRun(t *testing.T) {
	s, p, _, _, now := newInventoryPAMLab(t)
	runInventorySource(t, s, p, "pam-safes", "completed", now)
	if _, err := s.EnqueueSyncRun(context.Background(), p, "pam-safes", "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	tools := inventoryTools(t, s.Handler("lab-key", func() time.Time { return now.Add(time.Minute) }), "lab-key")
	if len(tools.Tools) != 1 || tools.Tools[0].LastRun == nil || tools.Tools[0].LastRun.Status != "queued" {
		t.Fatalf("the latest attempt must be reported even before it completes: %+v", tools.Tools)
	}
	// The queued attempt has not published anything: the metric still reads
	// the previous completed publication.
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricSafes), "available", 3, false)
}

func TestInventoryFirstPartialPublicationIsMarked(t *testing.T) {
	s, p, fake, _, now := newInventoryPAMLab(t)
	fake.setAccountStatus("2", 500)
	runInventorySource(t, s, p, "pam-accounts", "partial", now)
	h := s.Handler("lab-key", func() time.Time { return now })
	tools := inventoryTools(t, h, "lab-key")
	accounts := inventoryMetric(t, tools, "pam-tenant", metricAccounts)
	expectValue(t, accounts, "partial", 2, true)
	ratio := inventoryMetric(t, tools, "pam-tenant", metricAutoRatio)
	if ratio.Status != "partial" || ratio.Value != nil || ratio.Reason != "partial_denominator" {
		t.Fatal("a proportion is never computed over a partial denominator", ratio)
	}
	if body := inventoryRecords(t, h, "lab-key", metricAccounts, "pam-tenant"); body.Total != 2 || len(body.Records) != 2 || !body.Metric.Partial {
		t.Fatal("the partial drill-down must return what was published", body)
	}
	// A collection with no run at all is not zero: it is unavailable.
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricSafes), "no_publication")
}

func TestInventoryConfirmedZeroIsARealZero(t *testing.T) {
	s, p, fake, _, now := newInventoryPAMLab(t)
	fake.setAccounts(map[string]pamMemberPage{"": {values: []map[string]any{}, count: 0}})
	runInventorySource(t, s, p, "pam-accounts", "completed", now)
	h := s.Handler("lab-key", func() time.Time { return now })
	tools := inventoryTools(t, h, "lab-key")
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricAccounts), "available", 0, false)
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricLastFailed), "available", 0, false)
	// A proportion over a confirmed empty denominator has no value.
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricAutoRatio), "empty_denominator")
	if body := inventoryRecords(t, h, "lab-key", metricAccounts, "pam-tenant"); body.Total != 0 || len(body.Records) != 0 || body.Metric.Value == nil || *body.Metric.Value != 0 {
		t.Fatal("confirmed zero drill-down", body)
	}
	// Safes were never run in this test: still unavailable, not zero.
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricSafes), "no_publication")
}

func TestInventoryHidesConnectionsWithoutReadGrant(t *testing.T) {
	s, p, _, _, now := newInventoryPAMLab(t)
	for _, connection := range []string{"pam-safes", "pam-members", "pam-accounts", "pam-platforms"} {
		runInventorySource(t, s, p, connection, "completed", now)
	}
	ctx := context.Background()
	if err := s.IssuePrincipal(ctx, "inventory-none", "none-key", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.IssuePrincipal(ctx, "inventory-accounts", "accounts-key", []string{"pam-accounts"}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("lab-key", func() time.Time { return now })

	// No grant: no tool, no metric, and the drill-down cannot tell the
	// connection exists.
	code, body := syncRequest(t, h, "none-key", "GET", "/api/inventory/tools", "")
	if code != 200 || strings.Contains(body, "pam-tenant") || strings.Contains(body, `"pam-`) || !strings.Contains(body, `"tools":[]`) {
		t.Fatal("hidden connection leaked", code, body)
	}
	hidden, hiddenBody := syncRequest(t, h, "none-key", "GET", "/api/inventory/metrics/"+metricSafes+"/records?connection=pam-tenant", "")
	missing, missingBody := syncRequest(t, h, "none-key", "GET", "/api/inventory/metrics/"+metricSafes+"/records?connection=does-not-exist", "")
	if hidden != 404 || missing != 404 || hiddenBody != missingBody {
		t.Fatal("hidden and missing connections differ", hidden, hiddenBody, missing, missingBody)
	}

	// Read on accounts only: the tool shows that collection alone; metrics
	// over other collections are unavailable without naming their rows.
	code, body = syncRequest(t, h, "accounts-key", "GET", "/api/inventory/tools", "")
	if code != 200 {
		t.Fatal(code, body)
	}
	for _, leaked := range []string{"pam-safes", "pam-members", "pam-platforms"} {
		if strings.Contains(body, `"`+leaked+`"`) {
			t.Fatal("unreadable collection row leaked", leaked, body)
		}
	}
	var tools inventoryToolsBody
	if err := json.Unmarshal([]byte(body), &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || len(tools.Tools[0].Collections) != 1 || tools.Tools[0].Collections[0].Collection != "accounts" {
		t.Fatalf("%+v", tools)
	}
	expectValue(t, inventoryMetric(t, tools, "pam-tenant", metricAccounts), "available", 4, false)
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricSafes), "collection_unavailable")
	expectUnavailable(t, inventoryMetric(t, tools, "pam-tenant", metricSafesNoMembers), "collection_unavailable")
	if got := recordIDs(inventoryRecords(t, h, "accounts-key", metricLastFailed, "pam-tenant")); !slices.Equal(got, []string{"102:acct-b1"}) {
		t.Fatal("reader drill-down", got)
	}
	if body := inventoryRecords(t, h, "accounts-key", metricSafes, "pam-tenant"); len(body.Records) != 0 || body.Metric.Value != nil {
		t.Fatal("unreadable collection returned records", body)
	}

	// Revoking the principal removes everything.
	if err := s.RevokePrincipal(ctx, "inventory-accounts"); err != nil {
		t.Fatal(err)
	}
	if code, _ := syncRequest(t, h, "accounts-key", "GET", "/api/inventory/tools", ""); code != 401 {
		t.Fatal("revoked principal still reads", code)
	}
}
