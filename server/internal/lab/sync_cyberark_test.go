package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type pamMemberPage struct {
	values []map[string]any
	next   string
	count  int
}

type fakePAMSource struct {
	mu                 sync.Mutex
	first              []map[string]any
	second             []map[string]any
	status             map[string]int
	members            map[string]map[string]pamMemberPage
	memberStatus       map[string]int
	accounts           map[string]pamMemberPage
	accountStatus      map[string]int
	platforms          []map[string]any
	platformTotal      int
	platformStatus     int
	psmServers         []map[string]any
	psmServerTotal     int
	psmServerStatus    int
	psmConnectors      []map[string]any
	psmConnectorTotal  int
	psmConnectorStatus int
}

func newFakePAMSource() *fakePAMSource {
	return &fakePAMSource{
		status:        map[string]int{},
		members:       map[string]map[string]pamMemberPage{},
		memberStatus:  map[string]int{},
		accounts:      map[string]pamMemberPage{},
		accountStatus: map[string]int{},
	}
}

func (f *fakePAMSource) set(first, second []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.first, f.second = first, second
}

func (f *fakePAMSource) setMembers(safe string, pages map[string]pamMemberPage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[safe] = pages
}

func (f *fakePAMSource) setMemberStatus(safe, offset string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memberStatus[safe+"|"+offset] = status
}

func (f *fakePAMSource) setAccounts(pages map[string]pamMemberPage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts = pages
}

func (f *fakePAMSource) setAccountStatus(offset string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accountStatus[offset] = status
}

func (f *fakePAMSource) setPlatforms(values []map[string]any, total int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.platforms, f.platformTotal = values, total
}

func (f *fakePAMSource) setPlatformStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.platformStatus = status
}

func (f *fakePAMSource) setPSMServers(values []map[string]any, total int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.psmServers, f.psmServerTotal = values, total
}

func (f *fakePAMSource) setPSMServerStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.psmServerStatus = status
}

func (f *fakePAMSource) setPSMConnectors(values []map[string]any, total int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.psmConnectors, f.psmConnectorTotal = values, total
}

func (f *fakePAMSource) setPSMConnectorStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.psmConnectorStatus = status
}

func (f *fakePAMSource) setStatus(offset string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[offset] = status
}

func (f *fakePAMSource) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/API/Safes/") && strings.HasSuffix(r.URL.Path, "/Members") {
		safe := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/API/Safes/"), "/Members")
		offset := r.URL.Query().Get("offset")
		if status := f.memberStatus[safe+"|"+offset]; status != 0 {
			w.WriteHeader(status)
			return
		}
		page, found := f.members[safe][offset]
		if !found {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": page.values, "count": page.count, "nextLink": page.next,
		})
		return
	}
	if r.URL.Path == "/API/PSM/Servers" {
		if f.psmServerStatus != 0 {
			w.WriteHeader(f.psmServerStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"PSMServers": f.psmServers, "Total": f.psmServerTotal})
		return
	}
	if r.URL.Path == "/API/PSM/Connectors" {
		if f.psmConnectorStatus != 0 {
			w.WriteHeader(f.psmConnectorStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"PSMConnectors": f.psmConnectors, "Total": f.psmConnectorTotal})
		return
	}
	if r.URL.Path == "/API/Platforms" {
		if f.platformStatus != 0 {
			w.WriteHeader(f.platformStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"Platforms": f.platforms, "Total": f.platformTotal})
		return
	}
	if r.URL.Path == "/API/Accounts" {
		offset := r.URL.Query().Get("offset")
		if status := f.accountStatus[offset]; status != 0 {
			w.WriteHeader(status)
			return
		}
		page, found := f.accounts[offset]
		if !found {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": page.values, "count": page.count, "nextLink": page.next,
		})
		return
	}
	if r.URL.Path != "/API/Safes" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	offset := r.URL.Query().Get("offset")
	if status := f.status[offset]; status != 0 {
		w.WriteHeader(status)
		return
	}
	var values []map[string]any
	next := ""
	switch offset {
	case "":
		values = f.first
		if len(f.second) > 0 {
			next = "/API/Safes?limit=2&offset=2"
		}
	case "2":
		values = f.second
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"value": values, "count": len(f.first) + len(f.second), "nextLink": next,
	})
}

func pamSafe(number int, name, urlID string) map[string]any {
	return map[string]any{
		"safeNumber": number, "safeName": name, "safeUrlId": urlID,
		"location": "Acme DC", "creatorName": "Alice Admin",
		"olacEnabled": true, "numberOfVersionsRetention": 5, "numberOfDaysRetention": 30,
		"autoPurgeEnabled": false, "creationTime": 1767323045, "lastModificationTime": 1767409445,
		"description": "Synthetic safe", "managingCPM": "PasswordManager", "isExpiredMember": false,
		"secret": "must not be stored",
	}
}

func pamMember(safe int, id, name, kind string, permissions map[string]any) map[string]any {
	return map[string]any{
		"safeNumber": safe, "memberId": id, "memberName": name, "memberType": kind,
		"membershipExpirationDate": 1767323045, "isExpiredMembershipEnable": false,
		"isPredefinedUser": true, "isReadOnly": false, "permissions": permissions,
		"secret": "must not be stored",
	}
}

func pamAccount(id, name, safe, platform string, secret map[string]any) map[string]any {
	return map[string]any{
		"id": id, "name": name, "userName": name, "address": "db.acme.example",
		"safeName": safe, "platformId": platform, "accountType": "Service", "category": "DB",
		"secretManagement": secret, "secret": "must not be stored",
		"platformAccountProperties": map[string]any{"port": "5432"},
		"manualManagementReason":    "local only",
	}
}

func pamPlatform(id, name string, active bool, psm string) map[string]any {
	return map[string]any{
		"general": map[string]any{
			"id": id, "name": name, "active": active,
			"platformBaseID": id, "platformType": "Regular", "systemType": "Windows",
		},
		"sessionManagement": map[string]any{"PSMServerID": psm},
		"properties":        map[string]any{"secret": "must not be stored"},
		"secret":            "must not be stored",
	}
}

func pamPSMServer(id, name, address string) map[string]any {
	return map[string]any{
		"ID": id, "Name": name, "Address": address,
		"Properties": map[string]any{"secret": "must not be stored"},
		"secret":     "must not be stored",
	}
}

func pamPSMConnector(id, name string) map[string]any {
	return map[string]any{
		"ID": id, "DisplayName": name,
		"Type": "session", "secret": "must not be stored",
	}
}

func newPAMLab(t *testing.T) (*Store, Principal, *httptest.Server, *fakePAMSource, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	s, p := newSyncStore(t, now)
	fake := newFakePAMSource()
	fake.set([]map[string]any{pamSafe(101, "Alpha", "alpha"), pamSafe(102, "Beta", "beta")}, []map[string]any{pamSafe(103, "Gamma", "gamma")})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	if err := s.AddHTTPSource(ctx, "host", SyncSource{
		ConnectionID: "pam-safes", Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
		Collection: "safes", Scope: "synthetic-safes-v1", Generation: "cyberark-pam-safes-v1",
		PageSize: 2, MaxPages: 10, MaxRecords: 100,
	}, []string{p.Subject}, now); err != nil {
		t.Fatal(err)
	}
	return s, p, server, fake, now
}

func processPAMRun(t *testing.T, s *Store, p Principal, runID string, now time.Time) SyncRun {
	t.Helper()
	if err := s.ProcessNextSyncRun(context.Background(), "pam-worker", func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	run, err := s.SyncRun(context.Background(), p, runID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCyberArkPAMSafesEndToEndAndIsolation(t *testing.T) {
	s, p, _, fake, now := newPAMLab(t)
	h := s.Handler("lab-key", func() time.Time { return now })
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/auth/status", "")
	if code != 200 || !strings.Contains(body, `"ingestion:run"`) {
		t.Fatal("run capability not advertised", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/pam-safes/runs", "{}")
	if code != 202 || !strings.Contains(body, `"status":"queued"`) {
		t.Fatal(code, body)
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal([]byte(body), &started)
	if started.RunID == "" {
		t.Fatal(body)
	}
	run := processPAMRun(t, s, p, started.RunID, now)
	if run.RunID != started.RunID || run.Status != "completed" || run.Pages != 2 || run.Received != 3 || run.Connector != syncConnectorCyberArkPAMSafes {
		t.Fatal(run)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+run.RunID, "")
	if code != 200 || !strings.Contains(body, `"source_system":"cyberark"`) || !strings.Contains(body, `"source_file":"cyberark-pam-safes:pam-safes"`) {
		t.Fatal("run source system", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-safes/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("safe records", code, body)
	}
	for _, want := range []string{`"safe_url_id":"alpha"`, `"safe_name":"Alpha"`, `"creation_time":"2026-01-02T03:04:05Z"`, `"versions_retention":"5"`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing allowlisted attribute", want, body)
		}
	}
	if strings.Contains(body, "must not be stored") || strings.Contains(body, "secret") {
		t.Fatal("provider extra field leaked", body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/sources/cyberark/stages/history?runs=5", "")
	if code != 200 || !strings.Contains(body, run.RunID) {
		t.Fatal("cyberark stage history missing", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/sources/netskope/stages/history?runs=5", "")
	if code != 200 || strings.Contains(body, run.RunID) {
		t.Fatal("cyberark run leaked into netskope history", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/schedules", "")
	if code != 200 || !strings.Contains(body, `"source_system":"cyberark"`) || !strings.Contains(body, `"task_key":"pam-safes"`) {
		t.Fatal("cyberark schedule missing", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "POST", "/api/ingestion/all/runs?scope=cyberark", `{}`)
	if code != 202 || !strings.Contains(body, `"source_system":"cyberark"`) {
		t.Fatal("cyberark all/runs", code, body)
	}
	var batch struct {
		Runs []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(body), &batch); err != nil || len(batch.Runs) != 1 || batch.Runs[0].RunID == "" {
		t.Fatal(err, body)
	}
	if completed := processPAMRun(t, s, p, batch.Runs[0].RunID, now); completed.Status != "completed" || completed.RunID == run.RunID {
		t.Fatal("all/runs did not execute a new PAM run", completed)
	}
	// A later page failure keeps the complete baseline and does not retire.
	fake.setStatus("2", http.StatusInternalServerError)
	partialID, err := s.EnqueueSyncRun(context.Background(), p, "pam-safes", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	partial := processPAMRun(t, s, p, partialID, now.Add(time.Minute))
	if partial.Status != "partial" || partial.ErrorCode != "http_status_500" || partial.Received != 2 {
		t.Fatal(partial)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-safes/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 || !strings.Contains(body, `"gamma"`) {
		t.Fatal("partial run retired the baseline", code, body)
	}
	// A complete comparable run can now retire the absent Safe.
	fake.setStatus("2", 0)
	fake.set([]map[string]any{pamSafe(101, "Alpha", "alpha"), pamSafe(102, "Beta", "beta")}, nil)
	completedID, err := s.EnqueueSyncRun(context.Background(), p, "pam-safes", "", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	completed := processPAMRun(t, s, p, completedID, now.Add(2*time.Minute))
	if completed.Status != "completed" || completed.Retired != 1 {
		t.Fatal(completed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-safes/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"gamma"`) || !strings.Contains(body, `"retired_by_run_id"`) {
		t.Fatal("complete run did not retire gamma", code, body)
	}
	// A first-page 403 fails without publishing and keeps the baseline.
	fake.setStatus("", http.StatusForbidden)
	failedID, err := s.EnqueueSyncRun(context.Background(), p, "pam-safes", "", now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	failed := processPAMRun(t, s, p, failedID, now.Add(3*time.Minute))
	if failed.Status != "failed" || failed.ErrorCode != "permission_denied" {
		t.Fatal(failed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-safes/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 2 {
		t.Fatal("failed run changed active records", code, body)
	}
}

func TestCyberArkPAMSafeMembersEndToEnd(t *testing.T) {
	s, p, server, fake, now := newPAMLab(t)
	fake.setMembers("Alpha", map[string]pamMemberPage{
		"": {
			values: []map[string]any{
				pamMember(101, "a1", "Alice", "User", map[string]any{"useAccounts": true}),
				pamMember(101, "a2", "Group A", "Group", map[string]any{"retrieveAccounts": true}),
			},
			next:  "/API/Safes/Alpha/Members?limit=2&offset=2",
			count: 3,
		},
		"2": {
			values: []map[string]any{pamMember(101, "a3", "Bob", "User", map[string]any{"listAccounts": true})},
			count:  3,
		},
	})
	fake.setMembers("Beta", map[string]pamMemberPage{
		"": {values: []map[string]any{pamMember(102, "b1", "Carol", "User", map[string]any{})}, count: 1},
	})
	fake.setMembers("Gamma", map[string]pamMemberPage{
		"": {values: []map[string]any{}, count: 0},
	})
	ctx := context.Background()
	if err := s.AddHTTPSource(ctx, "host", SyncSource{
		ConnectionID: "pam-members", Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
		Collection: "safe_members", Scope: "synthetic-members-v1", Generation: "cyberark-pam-safes-v1",
		PageSize: 2, MaxPages: 10, MaxRecords: 100,
	}, []string{p.Subject}, now); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("lab-key", func() time.Time { return now })
	startMembers := func() string {
		t.Helper()
		code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/pam-members/runs", "{}")
		if code != 202 {
			t.Fatal(code, body)
		}
		var started struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" {
			t.Fatal(err, body)
		}
		return started.RunID
	}
	run := processPAMRun(t, s, p, startMembers(), now)
	if run.Status != "completed" || run.Pages != 4 || run.Received != 4 || run.Connector != syncConnectorCyberArkPAMSafes {
		t.Fatal(run)
	}
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+run.RunID, "")
	if code != 200 || !strings.Contains(body, `"collection":"safe_members"`) || !strings.Contains(body, `"source_system":"cyberark"`) || !strings.Contains(body, `"received_member_count":4`) {
		t.Fatal("member run contract", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-members/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 4 {
		t.Fatal("member records", code, body)
	}
	for _, want := range []string{`"external_id":"101:a1"`, `"safe_name":"Alpha"`, `"member_id":"a1"`, `"member_type":"User"`, `"permissions":"{\"useAccounts\":true}"`, `"membership_expiration_date":"2026-01-02T03:04:05Z"`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing member attribute", want, body)
		}
	}
	if strings.Contains(body, "must not be stored") || strings.Contains(body, `"secret"`) {
		t.Fatal("member provider extra field leaked", body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/schedules", "")
	if code != 200 || !strings.Contains(body, `"task_key":"pam-members"`) || !strings.Contains(body, `"source_file":"cyberark-pam-safes:pam-members"`) {
		t.Fatal("member schedule", code, body)
	}
	// A failed member page keeps the complete baseline and does not retire.
	fake.setMemberStatus("Alpha", "2", http.StatusInternalServerError)
	partial := processPAMRun(t, s, p, startMembers(), now.Add(time.Minute))
	if partial.Status != "partial" || partial.ErrorCode != "http_status_500" || partial.Received != 2 {
		t.Fatal(partial)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-members/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 4 || !strings.Contains(body, `"b1"`) {
		t.Fatal("partial member run retired the baseline", code, body)
	}
	// A complete comparable run can retire a member absent from the source.
	fake.setMemberStatus("Alpha", "2", 0)
	fake.setMembers("Beta", map[string]pamMemberPage{
		"": {values: []map[string]any{}, count: 0},
	})
	completed := processPAMRun(t, s, p, startMembers(), now.Add(2*time.Minute))
	if completed.Status != "completed" || completed.Received != 3 || completed.Retired != 1 {
		t.Fatal(completed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-members/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"102:b1"`) || !strings.Contains(body, `"retired_by_run_id"`) {
		t.Fatal("complete member run did not retire b1", code, body)
	}
	// A member count larger than the delivered members fails without publish.
	fake.setMembers("Alpha", map[string]pamMemberPage{
		"": {values: []map[string]any{pamMember(101, "a1", "Alice", "User", map[string]any{})}, count: 2},
	})
	failed := processPAMRun(t, s, p, startMembers(), now.Add(3*time.Minute))
	if failed.Status != "failed" || failed.ErrorCode != "incomplete_collection" {
		t.Fatal(failed)
	}
	// Read access is separate from the explicit run capability.
	if err := s.IssuePrincipal(ctx, "pam-member-reader", "member-reader-key", []string{"pam-members"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(ctx, "host", "custodia-lab", "pam-member-reader", "pam-members", false, now); err != nil {
		t.Fatal(err)
	}
	if code, body := syncRequest(t, h, "member-reader-key", "POST", "/api/ingestion/schedules/pam-members/runs", "{}"); code != 403 {
		t.Fatal("reader started a member run", code, body)
	}
	if code, body := syncRequest(t, h, "member-reader-key", "GET", "/api/ingestion/connections/pam-members/records", ""); code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("reader cannot read member records", code, body)
	}
}

func TestCyberArkPAMAccountsEndToEnd(t *testing.T) {
	s, p, server, fake, now := newPAMLab(t)
	fake.setAccounts(map[string]pamMemberPage{
		"": {
			values: []map[string]any{
				pamAccount("acct-a1", "svc-alpha", "Alpha", "Unix", map[string]any{"automaticManagementEnabled": true, "status": "Success", "lastModifiedTime": 1767323045}),
				pamAccount("acct-b1", "svc-beta", "Beta", "Windows", map[string]any{"automaticManagementEnabled": false, "status": "failure", "lastModifiedTime": 1767409445}),
			},
			next:  "/API/Accounts?limit=2&offset=2",
			count: 3,
		},
		"2": {
			values: []map[string]any{pamAccount("acct-a2", "svc-audit", "Alpha", "Unix", map[string]any{"automaticManagementEnabled": true, "status": "in_process"})},
			count:  3,
		},
	})
	ctx := context.Background()
	if err := s.AddHTTPSource(ctx, "host", SyncSource{
		ConnectionID: "pam-accounts", Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
		Collection: "accounts", Scope: "synthetic-accounts-v1", Generation: "cyberark-pam-safes-v1",
		PageSize: 2, MaxPages: 10, MaxRecords: 100,
	}, []string{p.Subject}, now); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("lab-key", func() time.Time { return now })
	startAccounts := func() string {
		t.Helper()
		code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/pam-accounts/runs", "{}")
		if code != 202 {
			t.Fatal(code, body)
		}
		var started struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" {
			t.Fatal(err, body)
		}
		return started.RunID
	}
	run := processPAMRun(t, s, p, startAccounts(), now)
	if run.Status != "completed" || run.Pages != 2 || run.Received != 3 || run.Connector != syncConnectorCyberArkPAMSafes {
		t.Fatal(run)
	}
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+run.RunID, "")
	if code != 200 || !strings.Contains(body, `"collection":"accounts"`) || !strings.Contains(body, `"source_system":"cyberark"`) {
		t.Fatal("account run contract", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-accounts/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("account records", code, body)
	}
	for _, want := range []string{`"external_id":"101:acct-a1"`, `"account_name":"svc-alpha"`, `"platform_id":"Unix"`, `"automatic_management_enabled":"true"`, `"management_status":"success"`, `"secret_modified_at":"2026-01-02T03:04:05Z"`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing account attribute", want, body)
		}
	}
	for _, forbidden := range []string{"must not be stored", `"secret"`, "platformAccountProperties", "manualManagementReason"} {
		if strings.Contains(body, forbidden) {
			t.Fatal("forbidden account field leaked", forbidden, body)
		}
	}
	// A later page failure keeps the complete baseline and does not retire.
	fake.setAccountStatus("2", http.StatusInternalServerError)
	partial := processPAMRun(t, s, p, startAccounts(), now.Add(time.Minute))
	if partial.Status != "partial" || partial.ErrorCode != "http_status_500" || partial.Received != 2 {
		t.Fatal(partial)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-accounts/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("partial account run retired the baseline", code, body)
	}
	// An unmapped account is skipped: the run is partial and cannot retire.
	fake.setAccountStatus("2", 0)
	fake.setAccounts(map[string]pamMemberPage{
		"": {
			values: []map[string]any{
				pamAccount("acct-a1", "svc-alpha", "Alpha", "Unix", map[string]any{"automaticManagementEnabled": true}),
				pamAccount("acct-hidden", "svc-hidden", "Hidden", "Unix", map[string]any{"automaticManagementEnabled": true}),
			},
			count: 2,
		},
	})
	skipped := processPAMRun(t, s, p, startAccounts(), now.Add(2*time.Minute))
	if skipped.Status != "partial" || skipped.Received != 1 || len(skipped.Warnings) != 1 || skipped.Warnings[0] != "skipped_accounts" {
		t.Fatal(skipped)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-accounts/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("skipped account run retired the baseline", code, body)
	}
	// A complete comparable run can retire absent accounts.
	fake.setAccounts(map[string]pamMemberPage{
		"": {values: []map[string]any{pamAccount("acct-a1", "svc-alpha", "Alpha", "Unix", map[string]any{"automaticManagementEnabled": true})}, count: 1},
	})
	completed := processPAMRun(t, s, p, startAccounts(), now.Add(3*time.Minute))
	if completed.Status != "completed" || completed.Received != 1 || completed.Retired != 2 {
		t.Fatal(completed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-accounts/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"101:acct-a2"`) || !strings.Contains(body, `"102:acct-b1"`) {
		t.Fatal("complete account run did not retire absent accounts", code, body)
	}
	// Read access is separate from the explicit run capability.
	if err := s.IssuePrincipal(ctx, "pam-account-reader", "account-reader-key", []string{"pam-accounts"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(ctx, "host", "custodia-lab", "pam-account-reader", "pam-accounts", false, now); err != nil {
		t.Fatal(err)
	}
	if code, body := syncRequest(t, h, "account-reader-key", "POST", "/api/ingestion/schedules/pam-accounts/runs", "{}"); code != 403 {
		t.Fatal("reader started an account run", code, body)
	}
	if code, body := syncRequest(t, h, "account-reader-key", "GET", "/api/ingestion/connections/pam-accounts/records", ""); code != 200 || strings.Count(body, `"external_id"`) != 1 {
		t.Fatal("reader cannot read account records", code, body)
	}
}

func TestCyberArkPAMPlatformsEndToEnd(t *testing.T) {
	s, p, server, fake, now := newPAMLab(t)
	fake.setPlatforms([]map[string]any{
		pamPlatform("WinDomain", "Windows Domain", true, "PSMServer_acme"),
		pamPlatform("Unix", "Unix", false, ""),
		pamPlatform("Database", "Database", true, "PSMServer_acme"),
	}, 3)
	ctx := context.Background()
	if err := s.AddHTTPSource(ctx, "host", SyncSource{
		ConnectionID: "pam-platforms", Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
		Collection: "platforms", Scope: "synthetic-platforms-v1", Generation: "cyberark-pam-safes-v1",
		PageSize: 2, MaxPages: 10, MaxRecords: 100,
	}, []string{p.Subject}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHTTPSource(ctx, "host", SyncSource{
		ConnectionID: "pam-platforms-partial", Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
		Collection: "platforms", Scope: "synthetic-platforms-v1", Generation: "cyberark-pam-safes-v1",
		PageSize: 2, MaxPages: 1, MaxRecords: 100,
	}, []string{p.Subject}, now); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("lab-key", func() time.Time { return now })
	startPlatforms := func(connection string) string {
		t.Helper()
		code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/"+connection+"/runs", "{}")
		if code != 202 {
			t.Fatal(code, body)
		}
		var started struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" {
			t.Fatal(err, body)
		}
		return started.RunID
	}
	run := processPAMRun(t, s, p, startPlatforms("pam-platforms"), now)
	if run.Status != "completed" || run.Pages != 2 || run.Received != 3 {
		t.Fatal(run)
	}
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/runs/"+run.RunID, "")
	if code != 200 || !strings.Contains(body, `"collection":"platforms"`) || !strings.Contains(body, `"source_system":"cyberark"`) {
		t.Fatal("platform run contract", code, body)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-platforms/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("platform records", code, body)
	}
	for _, want := range []string{`"external_id":"WinDomain"`, `"platform_name":"Windows Domain"`, `"active":"true"`, `"platform_base_id":"WinDomain"`, `"platform_type":"Regular"`, `"system_type":"Windows"`, `"psm_server_id":"PSMServer_acme"`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing platform attribute", want, body)
		}
	}
	for _, forbidden := range []string{"must not be stored", `"properties"`, `"secret"`} {
		if strings.Contains(body, forbidden) {
			t.Fatal("forbidden platform field leaked", forbidden, body)
		}
	}
	// A provider failure keeps the complete baseline.
	fake.setPlatformStatus(http.StatusForbidden)
	failed := processPAMRun(t, s, p, startPlatforms("pam-platforms"), now.Add(time.Minute))
	if failed.Status != "failed" || failed.ErrorCode != "permission_denied" {
		t.Fatal(failed)
	}
	fake.setPlatformStatus(0)
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-platforms/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("failed platform run changed active records", code, body)
	}
	// A bounded snapshot publishes its local pages and can never retire.
	partial := processPAMRun(t, s, p, startPlatforms("pam-platforms-partial"), now.Add(2*time.Minute))
	if partial.Status != "partial" || partial.Received != 2 || len(partial.Warnings) != 1 || partial.Warnings[0] != "max_pages" {
		t.Fatal(partial)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-platforms-partial/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 2 {
		t.Fatal("partial platform run did not publish its pages", code, body)
	}
	// A complete comparable run can retire absent platforms.
	fake.setPlatforms([]map[string]any{pamPlatform("WinDomain", "Windows Domain", true, "PSMServer_acme")}, 1)
	completed := processPAMRun(t, s, p, startPlatforms("pam-platforms"), now.Add(3*time.Minute))
	if completed.Status != "completed" || completed.Received != 1 || completed.Retired != 2 {
		t.Fatal(completed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-platforms/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"Unix"`) || !strings.Contains(body, `"Database"`) {
		t.Fatal("complete platform run did not retire absent platforms", code, body)
	}
	// Read access is separate from the explicit run capability.
	if err := s.IssuePrincipal(ctx, "pam-platform-reader", "platform-reader-key", []string{"pam-platforms"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(ctx, "host", "custodia-lab", "pam-platform-reader", "pam-platforms", false, now); err != nil {
		t.Fatal(err)
	}
	if code, body := syncRequest(t, h, "platform-reader-key", "POST", "/api/ingestion/schedules/pam-platforms/runs", "{}"); code != 403 {
		t.Fatal("reader started a platform run", code, body)
	}
	if code, body := syncRequest(t, h, "platform-reader-key", "GET", "/api/ingestion/connections/pam-platforms/records", ""); code != 200 || strings.Count(body, `"external_id"`) != 1 {
		t.Fatal("reader cannot read platform records", code, body)
	}
}

func TestCyberArkPAMPSMCatalogsEndToEnd(t *testing.T) {
	s, p, server, fake, now := newPAMLab(t)
	fake.setPSMServers([]map[string]any{
		pamPSMServer("PSMServer_acme", "PSM Acme", "psm.acme.example"),
		pamPSMServer("PSMServer_secondary", "PSM Secondary", "psm2.acme.example"),
		pamPSMServer("PSMServer_unnamed", "", ""),
	}, 3)
	fake.setPSMConnectors([]map[string]any{
		pamPSMConnector("PSM-RDP", "RDP"),
		pamPSMConnector("PSM-SSH", "SSH"),
	}, 2)
	ctx := context.Background()
	add := func(connection, collection string, pageSize, maxPages int) {
		t.Helper()
		if err := s.AddHTTPSource(ctx, "host", SyncSource{
			ConnectionID: connection, Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
			Collection: collection, Scope: "synthetic-psm-v1", Generation: "cyberark-pam-safes-v1",
			PageSize: pageSize, MaxPages: maxPages, MaxRecords: 100,
		}, []string{p.Subject}, now); err != nil {
			t.Fatal(err)
		}
	}
	add("pam-psm-servers", "psm_servers", 2, 10)
	add("pam-psm-servers-partial", "psm_servers", 2, 1)
	add("pam-psm-connectors", "psm_connectors", 1, 10)
	h := s.Handler("lab-key", func() time.Time { return now })
	start := func(connection string) string {
		t.Helper()
		code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/"+connection+"/runs", "{}")
		if code != 202 {
			t.Fatal(code, body)
		}
		var started struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" {
			t.Fatal(err, body)
		}
		return started.RunID
	}
	serversRun := processPAMRun(t, s, p, start("pam-psm-servers"), now)
	if serversRun.Status != "completed" || serversRun.Pages != 2 || serversRun.Received != 3 {
		t.Fatal(serversRun)
	}
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-servers/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("PSM server records", code, body)
	}
	for _, want := range []string{`"external_id":"PSMServer_acme"`, `"psm_name":"PSM Acme"`, `"address":"psm.acme.example"`, `"external_id":"PSMServer_unnamed"`, `"psm_name":"PSMServer_unnamed"`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing PSM server attribute", want, body)
		}
	}
	for _, forbidden := range []string{"must not be stored", `"Properties"`, `"secret"`} {
		if strings.Contains(body, forbidden) {
			t.Fatal("forbidden PSM server field leaked", forbidden, body)
		}
	}
	connectorsRun := processPAMRun(t, s, p, start("pam-psm-connectors"), now)
	if connectorsRun.Status != "completed" || connectorsRun.Pages != 2 || connectorsRun.Received != 2 {
		t.Fatal(connectorsRun)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-connectors/records", "")
	if code != 200 || !strings.Contains(body, `"external_id":"PSM-RDP"`) || !strings.Contains(body, `"connector_name":"RDP"`) {
		t.Fatal("PSM connector records", code, body)
	}
	for _, forbidden := range []string{"must not be stored", `"Type"`, `"secret"`} {
		if strings.Contains(body, forbidden) {
			t.Fatal("forbidden PSM connector field leaked", forbidden, body)
		}
	}
	// A bounded catalog publishes its local pages and cannot retire.
	partial := processPAMRun(t, s, p, start("pam-psm-servers-partial"), now.Add(time.Minute))
	if partial.Status != "partial" || partial.Received != 2 || len(partial.Warnings) != 1 || partial.Warnings[0] != "max_pages" {
		t.Fatal(partial)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-servers-partial/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 2 {
		t.Fatal("partial PSM catalog did not publish its pages", code, body)
	}
	// A provider failure keeps the complete baseline.
	fake.setPSMServerStatus(http.StatusForbidden)
	failed := processPAMRun(t, s, p, start("pam-psm-servers"), now.Add(2*time.Minute))
	if failed.Status != "failed" || failed.ErrorCode != "permission_denied" {
		t.Fatal(failed)
	}
	fake.setPSMServerStatus(0)
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-servers/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("failed PSM run changed active records", code, body)
	}
	// A complete comparable run can retire absent PSM servers.
	fake.setPSMServers([]map[string]any{pamPSMServer("PSMServer_acme", "PSM Acme", "psm.acme.example")}, 1)
	completed := processPAMRun(t, s, p, start("pam-psm-servers"), now.Add(3*time.Minute))
	if completed.Status != "completed" || completed.Received != 1 || completed.Retired != 2 {
		t.Fatal(completed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-servers/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"PSMServer_secondary"`) || !strings.Contains(body, `"PSMServer_unnamed"`) {
		t.Fatal("complete PSM run did not retire absent servers", code, body)
	}
	// Read access is separate from the explicit run capability.
	if err := s.IssuePrincipal(ctx, "psm-reader", "psm-reader-key", []string{"pam-psm-servers"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(ctx, "host", "custodia-lab", "psm-reader", "pam-psm-servers", false, now); err != nil {
		t.Fatal(err)
	}
	if code, body := syncRequest(t, h, "psm-reader-key", "POST", "/api/ingestion/schedules/pam-psm-servers/runs", "{}"); code != 403 {
		t.Fatal("reader started a PSM run", code, body)
	}
	if code, body := syncRequest(t, h, "psm-reader-key", "GET", "/api/ingestion/connections/pam-psm-servers/records", ""); code != 200 || strings.Count(body, `"external_id"`) != 1 {
		t.Fatal("reader cannot read PSM records", code, body)
	}
}

func TestCyberArkPAMPlatformPSMLinksEndToEnd(t *testing.T) {
	s, p, server, fake, now := newPAMLab(t)
	fake.setPlatforms([]map[string]any{
		pamPlatform("WinDomain", "Windows Domain", true, "PSMServer_acme"),
		pamPlatform("Unix", "Unix", true, "PSMSERVER_ACME"),
		pamPlatform("Database", "Database", true, "PSMServer_missing"),
		pamPlatform("Legacy", "Legacy", true, "PSMSERVER_CASE"),
		pamPlatform("NoRef", "No Reference", true, ""),
	}, 5)
	fake.setPSMServers([]map[string]any{
		pamPSMServer("PSMServer_acme", "PSM Acme", "psm.acme.example"),
		pamPSMServer("PSMServer_Case", "Case A", "case-a.acme.example"),
		pamPSMServer("PSMServer_case", "Case B", "case-b.acme.example"),
	}, 3)
	ctx := context.Background()
	add := func(connection string, pageSize, maxPages int) {
		t.Helper()
		if err := s.AddHTTPSource(ctx, "host", SyncSource{
			ConnectionID: connection, Connector: syncConnectorCyberArkPAMSafes, BaseURL: server.URL,
			Collection: "platform_psm_links", Scope: "synthetic-psm-links-v1", Generation: "cyberark-pam-safes-v1",
			PageSize: pageSize, MaxPages: maxPages, MaxRecords: 100,
		}, []string{p.Subject}, now); err != nil {
			t.Fatal(err)
		}
	}
	add("pam-psm-links", 2, 10)
	add("pam-psm-links-partial", 2, 1)
	h := s.Handler("lab-key", func() time.Time { return now })
	start := func(connection string) string {
		t.Helper()
		code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/"+connection+"/runs", "{}")
		if code != 202 {
			t.Fatal(code, body)
		}
		var started struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" {
			t.Fatal(err, body)
		}
		return started.RunID
	}
	run := processPAMRun(t, s, p, start("pam-psm-links"), now)
	if run.Status != "completed" || run.Pages != 2 || run.Received != 4 {
		t.Fatal(run)
	}
	code, body := syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-links/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 4 {
		t.Fatal("platform PSM links", code, body)
	}
	for _, want := range []string{
		`"external_id":"WinDomain:PSMServer_acme"`, `"resolution":"exact"`, `"psm_server_name":"PSM Acme"`,
		`"external_id":"Unix:PSMSERVER_ACME"`, `"resolution":"casefold"`, `"psm_server_id":"PSMServer_acme"`,
		`"external_id":"Database:PSMServer_missing"`, `"resolution":"missing"`,
		`"external_id":"Legacy:PSMSERVER_CASE"`, `"resolution":"ambiguous"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatal("missing link evidence", want, body)
		}
	}
	if strings.Contains(body, `"external_id":"NoRef:`) {
		t.Fatal("platform without PSM reference produced a link", body)
	}
	// A bounded link run publishes its pages and cannot retire.
	partial := processPAMRun(t, s, p, start("pam-psm-links-partial"), now.Add(time.Minute))
	if partial.Status != "partial" || partial.Received != 2 || len(partial.Warnings) != 1 || partial.Warnings[0] != "max_pages" {
		t.Fatal(partial)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-links-partial/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 2 {
		t.Fatal("partial link run did not publish its pages", code, body)
	}
	// A PSM catalog failure keeps the complete baseline.
	fake.setPSMServerStatus(http.StatusForbidden)
	failed := processPAMRun(t, s, p, start("pam-psm-links"), now.Add(2*time.Minute))
	if failed.Status != "failed" || failed.ErrorCode != "permission_denied" {
		t.Fatal(failed)
	}
	fake.setPSMServerStatus(0)
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-links/records", "")
	if code != 200 || strings.Count(body, `"external_id"`) != 4 {
		t.Fatal("failed PSM catalog changed link baseline", code, body)
	}
	// A complete comparable run retires a link whose reference disappeared.
	fake.setPlatforms([]map[string]any{
		pamPlatform("WinDomain", "Windows Domain", true, "PSMServer_acme"),
		pamPlatform("Database", "Database", true, "PSMServer_missing"),
		pamPlatform("Legacy", "Legacy", true, "PSMSERVER_CASE"),
	}, 3)
	completed := processPAMRun(t, s, p, start("pam-psm-links"), now.Add(3*time.Minute))
	if completed.Status != "completed" || completed.Received != 3 || completed.Retired != 1 {
		t.Fatal(completed)
	}
	code, body = syncRequest(t, h, "lab-key", "GET", "/api/ingestion/connections/pam-psm-links/records?status=retired", "")
	if code != 200 || !strings.Contains(body, `"Unix:PSMSERVER_ACME"`) {
		t.Fatal("complete link run did not retire the removed reference", code, body)
	}
	// Read access is separate from the explicit run capability.
	if err := s.IssuePrincipal(ctx, "psm-link-reader", "psm-link-reader-key", []string{"pam-psm-links"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(ctx, "host", "custodia-lab", "psm-link-reader", "pam-psm-links", false, now); err != nil {
		t.Fatal(err)
	}
	if code, body := syncRequest(t, h, "psm-link-reader-key", "POST", "/api/ingestion/schedules/pam-psm-links/runs", "{}"); code != 403 {
		t.Fatal("reader started a link run", code, body)
	}
	if code, body := syncRequest(t, h, "psm-link-reader-key", "GET", "/api/ingestion/connections/pam-psm-links/records", ""); code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("reader cannot read link records", code, body)
	}
}

func TestCyberArkPAMSafesPermissions(t *testing.T) {
	s, p, _, _, now := newPAMLab(t)
	ctx := context.Background()
	if err := s.IssuePrincipal(ctx, "pam-reader", "reader-key", []string{"pam-safes"}); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSync(ctx, "host", "custodia-lab", "pam-reader", "pam-safes", false, now); err != nil {
		t.Fatal(err)
	}
	if err := s.IssuePrincipal(ctx, "pam-none", "none-key", nil); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("lab-key", func() time.Time { return now })
	if code, body := syncRequest(t, h, "reader-key", "POST", "/api/ingestion/schedules/pam-safes/runs", "{}"); code != 403 {
		t.Fatal("reader started a PAM run", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "POST", "/api/ingestion/all/runs?scope=cyberark", "{}"); code != 403 {
		t.Fatal("reader started a PAM batch", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/auth/status", ""); code != 200 || strings.Contains(body, `"ingestion:run"`) {
		t.Fatal("reader received the run capability", code, body)
	}
	code, body := syncRequest(t, h, "lab-key", "POST", "/api/ingestion/schedules/pam-safes/runs", "{}")
	if code != 202 {
		t.Fatal(code, body)
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal([]byte(body), &started); err != nil || started.RunID == "" {
		t.Fatal(err, body)
	}
	run := processPAMRun(t, s, p, started.RunID, now)
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/runs/"+run.RunID, ""); code != 200 || !strings.Contains(body, `"source_system":"cyberark"`) {
		t.Fatal("reader cannot read its run", code, body)
	}
	if code, body := syncRequest(t, h, "reader-key", "GET", "/api/ingestion/connections/pam-safes/records", ""); code != 200 || strings.Count(body, `"external_id"`) != 3 {
		t.Fatal("reader cannot read its records", code, body)
	}
	if code, body := syncRequest(t, h, "none-key", "GET", "/api/ingestion/connections/pam-safes/records", ""); code != 403 {
		t.Fatal("ungranted principal read PAM records", code, body)
	}
}
