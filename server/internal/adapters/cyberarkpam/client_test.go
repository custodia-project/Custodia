package cyberarkpam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"custodia/server/internal/inventory"
	inventorysync "custodia/server/internal/sync"
)

func respond(t *testing.T, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func fetch(t *testing.T, client *Client) inventorysync.Outcome {
	t.Helper()
	return inventorysync.Fetch(context.Background(), client, CollectionSafes, inventorysync.Limits{
		PageSize: 2, MaxPages: 20, MaxRecords: 100,
	}, nil)
}

func TestFetchSafesPaginationAndAllowlist(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/API/Safes" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("adapter sent credentials")
		}
		switch r.URL.Query().Get("offset") {
		case "":
			if r.URL.Query().Get("limit") != "2" {
				t.Fatalf("first page limit = %q", r.URL.Query().Get("limit"))
			}
			respond(t, `{"value":[
				{"safeNumber":101,"safeName":"Alpha","safeUrlId":"alpha","location":"Acme DC","creatorName":"Alice","olacEnabled":true,"numberOfVersionsRetention":5,"numberOfDaysRetention":30,"autoPurgeEnabled":false,"creationTime":1767323045,"lastModificationTime":"1767409445","description":"Synthetic safe","managingCPM":"PasswordManager","isExpiredMember":false,"secret":"never stored"},
				{"safeNumber":102,"safeName":"Beta","safeUrlId":"beta","location":"","creatorName":"Bob","olacEnabled":false,"numberOfVersionsRetention":0,"numberOfDaysRetention":0,"autoPurgeEnabled":true,"creationTime":null,"lastModificationTime":null,"description":"","managingCPM":"","isExpiredMember":true}
			],"count":3,"nextLink":"/API/Safes?limit=2&offset=2"}`)(w, r)
		case "2":
			respond(t, `{"value":[{"safeNumber":103,"safeName":"Gamma","safeUrlId":"gamma"}],"count":3,"nextLink":""}`)(w, r)
		default:
			t.Fatalf("unexpected offset %q", r.URL.Query().Get("offset"))
		}
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetch(t, client)
	if outcome.Status != inventory.StatusCompleted || outcome.Pages != 2 || outcome.Received != 3 {
		t.Fatal(outcome)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
	alpha := outcome.Objects[0]
	if alpha.ExternalID != "101" || alpha.Attributes["safe_name"] != "Alpha" || alpha.Attributes["safe_url_id"] != "alpha" {
		t.Fatal(alpha)
	}
	if alpha.Attributes["creation_time"] != "2026-01-02T03:04:05Z" || alpha.Attributes["last_modification_time"] != "2026-01-03T03:04:05Z" {
		t.Fatal(alpha.Attributes)
	}
	if alpha.Attributes["versions_retention"] != "5" || alpha.Attributes["days_retention"] != "30" || alpha.Attributes["olac_enabled"] != "true" {
		t.Fatal(alpha.Attributes)
	}
	if _, found := alpha.Attributes["secret"]; found {
		t.Fatal("unlisted provider field reached storage")
	}
	beta := outcome.Objects[1]
	if _, found := beta.Attributes["creation_time"]; found {
		t.Fatal("null epoch was stored")
	}
	if beta.Attributes["expired_member"] != "true" || beta.Attributes["auto_purge_enabled"] != "true" {
		t.Fatal(beta.Attributes)
	}
}

func TestFetchRejectsIncompleteOrChangingTotal(t *testing.T) {
	for name, body := range map[string]string{
		"short":    `{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"}],"count":2,"nextLink":""}`,
		"negative": `{"value":[],"count":-1,"nextLink":""}`,
		"string":   `{"value":[],"count":"many","nextLink":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(respond(t, body))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetch(t, client)
			if outcome.Status != inventory.StatusFailed || outcome.Objects != nil || outcome.ErrorCode != "incomplete_collection" && outcome.ErrorCode != "invalid_total" {
				t.Fatal(outcome)
			}
		})
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"}],"count":1,"nextLink":"/API/Safes?offset=1"}`)(w, nil)
			return
		}
		respond(t, `{"value":[{"safeNumber":2,"safeName":"B","safeUrlId":"b"}],"count":2,"nextLink":""}`)(w, nil)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetch(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "total_changed" {
		t.Fatal(outcome)
	}
}

func TestFetchSafesPartialAfterFirstPageAndFirstPageFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"},{"safeNumber":2,"safeName":"B","safeUrlId":"b"}],"count":3,"nextLink":"/API/Safes?offset=2"}`)(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetch(t, client)
	if outcome.Status != inventory.StatusPartial || outcome.Received != 2 || outcome.ErrorCode != "http_status_500" {
		t.Fatal(outcome)
	}

	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusForbidden, "permission_denied"},
		{http.StatusUnauthorized, "authentication_failed"},
		{http.StatusTooManyRequests, "http_status_429"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetch(t, client)
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != tc.code || outcome.Objects != nil {
				t.Fatal(outcome)
			}
		})
	}
}

func TestFetchRejectsForeignOrRepeatedNextLink(t *testing.T) {
	for name, next := range map[string]string{
		"foreign":  "http://198.51.100.10/API/Safes?offset=2",
		"path":     "/API/Accounts?offset=2",
		"fragment": "/API/Safes?offset=2#x",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(respond(t, `{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"}],"count":1,"nextLink":"`+next+`"}`))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetch(t, client)
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "foreign_cursor" {
				t.Fatal(outcome)
			}
		})
	}

	server := httptest.NewServer(respond(t, `{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"}],"count":1,"nextLink":"/API/Safes?offset=1"}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetch(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "cursor_repeated" {
		t.Fatal(outcome)
	}
}

func TestFetchAcceptsSameOriginAbsoluteNextLink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") == "" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"}],"count":2,"nextLink":"http://`+r.Host+`/API/Safes?offset=1"}`)(w, r)
			return
		}
		respond(t, `{"value":[{"safeNumber":2,"safeName":"B","safeUrlId":"b"}],"count":2,"nextLink":""}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetch(t, client)
	if outcome.Status != inventory.StatusCompleted || outcome.Received != 2 {
		t.Fatal(outcome)
	}
}

func TestFetchZeroConfirmedAndPayloadValidation(t *testing.T) {
	server := httptest.NewServer(respond(t, `{"value":[],"count":0,"nextLink":""}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetch(t, client)
	if outcome.Status != inventory.StatusCompleted || outcome.Received != 0 || outcome.Pages != 1 {
		t.Fatal(outcome)
	}

	for name, tc := range map[string]struct {
		body string
		code string
	}{
		"missing-value":      {`{"count":0,"nextLink":""}`, "invalid_payload"},
		"null-value":         {`{"value":null,"count":0,"nextLink":""}`, "invalid_payload"},
		"bad-next":           {`{"value":[],"count":0,"nextLink":2}`, "invalid_cursor"},
		"zero-total":         {`{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"}],"count":0,"nextLink":""}`, "invalid_total"},
		"short-total":        {`{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"},{"safeNumber":2,"safeName":"B","safeUrlId":"b"}],"count":1,"nextLink":""}`, "invalid_total"},
		"too-many":           {`{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a"},{"safeNumber":2,"safeName":"B","safeUrlId":"b"},{"safeNumber":3,"safeName":"C","safeUrlId":"c"}],"count":3,"nextLink":""}`, "page_too_large"},
		"missing-url-id":     {`{"value":[{"safeNumber":1,"safeName":"A"}],"count":1,"nextLink":""}`, "invalid_object"},
		"zero-number":        {`{"value":[{"safeNumber":0,"safeName":"A","safeUrlId":"a"}],"count":1,"nextLink":""}`, "invalid_object"},
		"bad-epoch":          {`{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a","creationTime":"yesterday"}],"count":1,"nextLink":""}`, "invalid_object"},
		"negative-retention": {`{"value":[{"safeNumber":1,"safeName":"A","safeUrlId":"a","numberOfDaysRetention":-1}],"count":1,"nextLink":""}`, "invalid_object"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(respond(t, tc.body))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetch(t, client)
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != tc.code {
				t.Fatal(outcome)
			}
		})
	}
}

func TestFetchRejectsRedirectAndBadContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") == "redirect" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetch(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "invalid_content_type" {
		t.Fatal(outcome)
	}

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusFound)
	}))
	defer redirectServer.Close()
	redirectClient, err := New(redirectServer.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The Go client surfaces a 302 without following it.
	response, err := redirectClient.FetchPage(context.Background(), CollectionSafes, "", 2)
	if err == nil || response.Objects != nil {
		t.Fatal(response, err)
	}
	coded, ok := err.(*inventory.Error)
	if !ok || coded.Code != "redirect" {
		t.Fatal(err)
	}
}

func TestMapSafeEpochAndNumericStrings(t *testing.T) {
	raw := []byte(`{"safeNumber":"42","safeName":" A ","safeUrlId":" a ","numberOfVersionsRetention":"0","creationTime":"0","secret":"ignored"}`)
	object, err := mapSafe(raw)
	if err != nil {
		t.Fatal(err)
	}
	if object.ExternalID != "42" || object.Attributes["safe_name"] != "A" || object.Attributes["versions_retention"] != "0" {
		t.Fatal(object)
	}
	if object.Attributes["creation_time"] != time.Unix(0, 0).UTC().Format(time.RFC3339) {
		t.Fatal(object.Attributes)
	}
	if _, found := object.Attributes["secret"]; found {
		t.Fatal("provider extra field persisted")
	}
	for _, invalid := range []string{`{"safeNumber":1,"safeName":"A","safeUrlId":"a","creationTime":253402300800}`, `{"safeNumber":1,"safeName":"A","safeUrlId":"a","creationTime":1.5}`} {
		if _, err := mapSafe([]byte(invalid)); err == nil {
			t.Fatal("invalid epoch accepted", invalid)
		}
	}
}

func TestNewRejectsNonLoopbackAndUnsafeBase(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1:8443",
		"http://192.0.2.10:8080",
		"http://localhost:8080",
		"http://user@127.0.0.1:8080",
		"http://127.0.0.1:8080/pvwa",
		"http://127.0.0.1:8080?x=1",
		"http://127.0.0.1:8080#x",
	} {
		if _, err := New(raw, Options{}); err == nil {
			t.Fatal("unsafe base accepted", raw)
		}
	}
}

func TestDecodePageRejectsTrailingData(t *testing.T) {
	if _, err := decodePage([]byte(`{"value":[],"count":0,"nextLink":""}{}`), 2); err == nil {
		t.Fatal("trailing data accepted")
	}
	if _, err := decodePage([]byte(`[]`), 2); err == nil {
		t.Fatal("non-object payload accepted")
	}
	// Unknown top-level provider fields are tolerated; only value/count/nextLink
	// participate in the bounded contract.
	if page, err := decodePage([]byte(`{"value":[],"count":0,"nextLink":"","extra":true}`), 2); err != nil || page.Expected != 0 {
		t.Fatal(page, err)
	}
}

func TestFetchPageRejectsUnsupportedCollectionAndLimit(t *testing.T) {
	server := httptest.NewServer(respond(t, `{"value":[],"count":0,"nextLink":""}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.FetchPage(context.Background(), "users", "", 2); err == nil {
		t.Fatal("users accepted by PAM adapter")
	} else if coded := err.(*inventory.Error); coded.Code != "unsupported_collection" {
		t.Fatal(err)
	}
	if _, err = client.FetchPage(context.Background(), CollectionSafes, "", 0); err == nil {
		t.Fatal("zero limit accepted")
	}
}

func TestDecodePageCountAsJSONNumberAndString(t *testing.T) {
	for _, body := range []string{
		`{"value":[],"count":0,"nextLink":""}`,
		`{"value":[],"count":"0","nextLink":""}`,
		`{"value":[],"count":null,"nextLink":null}`,
	} {
		page, err := decodePage([]byte(body), 2)
		if err != nil {
			t.Fatal(body, err)
		}
		if page.Expected != 0 || page.NextCursor != "" {
			t.Fatal(page)
		}
	}
}

func TestDecodePageKeepsProviderOrder(t *testing.T) {
	body := `{"value":[{"safeNumber":2,"safeName":"B","safeUrlId":"b"},{"safeNumber":1,"safeName":"A","safeUrlId":"a"}],"count":2,"nextLink":""}`
	page, err := decodePage([]byte(body), 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.Objects[0].ExternalID != "2" || page.Objects[1].ExternalID != "1" {
		t.Fatal(page.Objects)
	}
	if strings.Join([]string{page.Objects[0].Attributes["safe_name"], page.Objects[1].Attributes["safe_name"]}, ",") != "B,A" {
		t.Fatal(page.Objects)
	}
}

func member(number int, id, name, kind string, permissions string) string {
	return `{"safeNumber":` + strconv.Itoa(number) + `,"memberId":"` + id + `","memberName":"` + name +
		`","memberType":"` + kind + `","membershipExpirationDate":1767323045,"isExpiredMembershipEnable":false,` +
		`"isPredefinedUser":true,"isReadOnly":false,"permissions":` + permissions + `,"secret":"ignored"}`
}

func fetchMembers(t *testing.T, client *Client) inventorysync.Outcome {
	t.Helper()
	return inventorysync.Fetch(context.Background(), client, CollectionSafeMembers, inventorysync.Limits{
		PageSize: 2, MaxPages: 20, MaxRecords: 100,
	}, nil)
}

func TestFetchSafeMembersAcrossSafesAndPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/API/Safes":
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"},{"safeNumber":2,"safeName":"Beta"}],"count":2,"nextLink":""}`)(w, r)
		case "/API/Safes/Alpha/Members":
			if r.URL.Query().Get("offset") == "" {
				respond(t, `{"value":[`+member(1, "m1", "Alice", "User", `{"useAccounts":true}`)+`,`+member(1, "m2", "Group A", "Group", `{"retrieveAccounts":true}`)+`],"count":3,"nextLink":"/API/Safes/Alpha/Members?limit=2&offset=2"}`)(w, r)
				return
			}
			respond(t, `{"value":[`+member(1, "m3", "Bob", "User", `{"listAccounts":true}`)+`],"count":3,"nextLink":""}`)(w, r)
		case "/API/Safes/Beta/Members":
			respond(t, `{"value":[`+member(2, "m4", "Carol", "User", `{}`)+`],"count":1,"nextLink":""}`)(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchMembers(t, client)
	if outcome.Status != inventory.StatusCompleted || outcome.Pages != 3 || outcome.Received != 4 {
		t.Fatal(outcome)
	}
	first := outcome.Objects[0]
	if first.ExternalID != "1:m1" || first.Attributes["safe_number"] != "1" || first.Attributes["safe_name"] != "Alpha" || first.Attributes["member_name"] != "Alice" || first.Attributes["member_type"] != "User" {
		t.Fatal(first)
	}
	if first.Attributes["membership_expiration_date"] != "2026-01-02T03:04:05Z" || first.Attributes["predefined_user"] != "true" || first.Attributes["read_only"] != "false" {
		t.Fatal(first.Attributes)
	}
	if !strings.Contains(first.Attributes["permissions"], `"useAccounts":true`) || strings.Contains(first.Attributes["permissions"], "secret") {
		t.Fatal(first.Attributes["permissions"])
	}
	last := outcome.Objects[3]
	if last.ExternalID != "2:m4" || last.Attributes["safe_name"] != "Beta" || last.Attributes["permissions"] != "{}" {
		t.Fatal(last)
	}
}

func TestFetchSafeMembersPartialAndEnumerationFailures(t *testing.T) {
	var memberCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
			return
		}
		if memberCalls.Add(1) == 1 {
			respond(t, `{"value":[`+member(1, "m1", "Alice", "User", `{}`)+`],"count":2,"nextLink":"/API/Safes/Alpha/Members?offset=1"}`)(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchMembers(t, client)
	if outcome.Status != inventory.StatusPartial || outcome.Received != 1 || outcome.ErrorCode != "http_status_500" {
		t.Fatal(outcome)
	}

	enumerationFailure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer enumerationFailure.Close()
	client, err = New(enumerationFailure.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchMembers(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "permission_denied" {
		t.Fatal(outcome)
	}
}

func TestFetchSafeMembersRejectsIncompleteOrChangingTotals(t *testing.T) {
	for name, memberBody := range map[string]string{
		"short": `{"value":[` + member(1, "m1", "Alice", "User", `{}`) + `],"count":2,"nextLink":""}`,
		"zero":  `{"value":[` + member(1, "m1", "Alice", "User", `{}`) + `],"count":0,"nextLink":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/API/Safes" {
					respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
					return
				}
				respond(t, memberBody)(w, r)
			}))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetchMembers(t, client)
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "invalid_total" && outcome.ErrorCode != "incomplete_collection" {
				t.Fatal(outcome)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
			return
		}
		if r.URL.Query().Get("offset") == "" {
			respond(t, `{"value":[`+member(1, "m1", "Alice", "User", `{}`)+`],"count":2,"nextLink":"/API/Safes/Alpha/Members?offset=1"}`)(w, r)
			return
		}
		respond(t, `{"value":[`+member(1, "m2", "Bob", "User", `{}`)+`],"count":3,"nextLink":""}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchMembers(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "invalid_total" {
		t.Fatal(outcome)
	}

	incompleteEnumeration := httptest.NewServer(respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":2,"nextLink":""}`))
	defer incompleteEnumeration.Close()
	client, err = New(incompleteEnumeration.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchMembers(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "incomplete_collection" {
		t.Fatal(outcome)
	}
}

func TestFetchSafeMembersRejectsForeignCursorAndInvalidMembers(t *testing.T) {
	for name, next := range map[string]string{
		"cross-safe": "/API/Safes/Beta/Members?offset=1",
		"foreign":    "http://198.51.100.10/API/Safes/Alpha/Members?offset=1",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/API/Safes" {
					respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
					return
				}
				respond(t, `{"value":[`+member(1, "m1", "Alice", "User", `{}`)+`],"count":1,"nextLink":"`+next+`"}`)(w, r)
			}))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetchMembers(t, client)
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "foreign_cursor" {
				t.Fatal(outcome)
			}
		})
	}

	for name, body := range map[string]string{
		"wrong-safe":    `{"value":[` + member(2, "m1", "Alice", "User", `{}`) + `],"count":1,"nextLink":""}`,
		"no-perms":      `{"value":[{"safeNumber":1,"memberId":"m1","memberName":"Alice","memberType":"User","membershipExpirationDate":null}],"count":1,"nextLink":""}`,
		"no-expiration": `{"value":[{"safeNumber":1,"memberId":"m1","memberName":"Alice","memberType":"User","permissions":{}}],"count":1,"nextLink":""}`,
		"bad-member":    `{"value":[{"safeNumber":1,"memberId":"","memberName":"Alice","memberType":"User","permissions":{}}],"count":1,"nextLink":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/API/Safes" {
					respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
					return
				}
				respond(t, body)(w, r)
			}))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetchMembers(t, client)
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "invalid_object" {
				t.Fatal(outcome)
			}
		})
	}
}

func TestFetchSafeMembersZeroSafesAndUnsupportedCollection(t *testing.T) {
	server := httptest.NewServer(respond(t, `{"value":[],"count":0,"nextLink":""}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchMembers(t, client)
	if outcome.Status != inventory.StatusCompleted || outcome.Received != 0 || outcome.Pages != 1 {
		t.Fatal(outcome)
	}
	if _, err = client.FetchPage(context.Background(), "users", "", 2); err == nil {
		t.Fatal("users accepted by PAM adapter")
	}
}

func fetchAccounts(t *testing.T, client *Client) inventorysync.Outcome {
	t.Helper()
	return inventorysync.Fetch(context.Background(), client, CollectionAccounts, inventorysync.Limits{
		PageSize: 2, MaxPages: 20, MaxRecords: 100,
	}, nil)
}

func TestFetchAccountsPaginationAndSecretAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"},{"safeNumber":2,"safeName":"Beta"}],"count":2,"nextLink":""}`)(w, r)
			return
		}
		if r.URL.Path != "/API/Accounts" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("offset") == "" {
			respond(t, `{"value":[
				{"id":"a1","name":"svc-alpha","userName":"svc-alpha","address":"db.acme.example","safeName":"Alpha","platformId":"Unix","accountType":"Service","category":"DB","secretManagement":{"automaticManagementEnabled":true,"status":"Success","lastModifiedTime":1767323045,"manualManagementReason":"never","secretType":"password"},"secret":"never stored"},
				{"id":2,"userName":"svc-beta","safeName":"Beta","platformId":42,"secretManagement":{"automaticManagementEnabled":"yes","status":"other"}}
			],"count":3,"nextLink":"/API/Accounts?limit=2&offset=2"}`)(w, r)
			return
		}
		respond(t, `{"value":[{"id":"a3","name":"svc-gamma","safeName":"Alpha","platformId":""}],"count":3,"nextLink":""}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchAccounts(t, client)
	if outcome.Status != inventory.StatusCompleted || outcome.Pages != 2 || outcome.Received != 3 || len(outcome.Warnings) != 0 {
		t.Fatal(outcome)
	}
	first := outcome.Objects[0]
	if first.ExternalID != "1:a1" || first.Attributes["safe_number"] != "1" || first.Attributes["safe_name"] != "Alpha" || first.Attributes["account_name"] != "svc-alpha" || first.Attributes["user_name"] != "svc-alpha" || first.Attributes["address"] != "db.acme.example" || first.Attributes["platform_id"] != "Unix" || first.Attributes["account_type"] != "Service" || first.Attributes["category"] != "DB" {
		t.Fatal(first)
	}
	if first.Attributes["automatic_management_enabled"] != "true" || first.Attributes["management_status"] != "success" || first.Attributes["secret_modified_at"] != "2026-01-02T03:04:05Z" {
		t.Fatal(first.Attributes)
	}
	for _, forbidden := range []string{"secret", "manualManagementReason", "secretType"} {
		if _, found := first.Attributes[forbidden]; found {
			t.Fatal("forbidden account field stored", forbidden)
		}
	}
	second := outcome.Objects[1]
	if second.ExternalID != "2:2" || second.Attributes["account_name"] != "svc-beta" || second.Attributes["platform_id"] != "42" {
		t.Fatal(second)
	}
	if _, found := second.Attributes["automatic_management_enabled"]; found {
		t.Fatal("non-boolean management flag stored")
	}
	if _, found := second.Attributes["management_status"]; found {
		t.Fatal("unknown management status stored")
	}
	if outcome.Objects[2].ExternalID != "1:a3" {
		t.Fatal(outcome.Objects[2])
	}
}

func TestFetchAccountsPartialSkippedAndFailures(t *testing.T) {
	var accountCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
			return
		}
		if accountCalls.Add(1) == 1 {
			respond(t, `{"value":[{"id":"a1","name":"one","safeName":"Alpha"},{"id":"a2","name":"two","safeName":"Alpha"}],"count":3,"nextLink":"/API/Accounts?offset=2"}`)(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchAccounts(t, client)
	if outcome.Status != inventory.StatusPartial || outcome.Received != 2 || outcome.ErrorCode != "http_status_500" {
		t.Fatal(outcome)
	}

	skipped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
			return
		}
		respond(t, `{"value":[{"id":"a1","name":"one","safeName":"Alpha"},{"id":"a2","name":"two","safeName":"Hidden"},{"name":"no-id","safeName":"Alpha"}],"count":3,"nextLink":""}`)(w, r)
	}))
	defer skipped.Close()
	client, err = New(skipped.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = inventorysync.Fetch(context.Background(), client, CollectionAccounts, inventorysync.Limits{
		PageSize: 3, MaxPages: 20, MaxRecords: 100,
	}, nil)
	if outcome.Status != inventory.StatusPartial || outcome.Received != 1 || len(outcome.Objects) != 1 || len(outcome.Warnings) != 1 || outcome.Warnings[0] != "skipped_accounts" {
		t.Fatal(outcome)
	}
	if outcome.Objects[0].ExternalID != "1:a1" {
		t.Fatal(outcome.Objects)
	}

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbidden.Close()
	client, err = New(forbidden.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchAccounts(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "permission_denied" {
		t.Fatal(outcome)
	}
}

func TestFetchAccountsRejectsIncompleteTotalAndForeignCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
			return
		}
		respond(t, `{"value":[{"id":"a1","name":"one","safeName":"Alpha"}],"count":2,"nextLink":""}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchAccounts(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "incomplete_collection" {
		t.Fatal(outcome)
	}

	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
			return
		}
		respond(t, `{"value":[{"id":"a1","name":"one","safeName":"Alpha"}],"count":1,"nextLink":"/API/Safes?offset=2"}`)(w, r)
	}))
	defer foreign.Close()
	client, err = New(foreign.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchAccounts(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "foreign_cursor" {
		t.Fatal(outcome)
	}
}

func TestFetchAccountsZeroConfirmedAndInvalidObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[],"count":0,"nextLink":""}`)(w, r)
			return
		}
		respond(t, `{"value":[],"count":0,"nextLink":""}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchAccounts(t, client)
	if outcome.Status != inventory.StatusCompleted || outcome.Received != 0 || outcome.Pages != 1 {
		t.Fatal(outcome)
	}

	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Safes" {
			respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha"}],"count":1,"nextLink":""}`)(w, r)
			return
		}
		respond(t, `{"value":["not-an-object"],"count":1,"nextLink":""}`)(w, r)
	}))
	defer invalid.Close()
	client, err = New(invalid.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchAccounts(t, client)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "invalid_object" {
		t.Fatal(outcome)
	}
}

func fetchPlatforms(t *testing.T, client *Client, limits inventorysync.Limits) inventorysync.Outcome {
	t.Helper()
	return inventorysync.Fetch(context.Background(), client, CollectionPlatforms, limits, nil)
}

func TestFetchPlatformsAllowlistAndLocalPagination(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/API/Platforms" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		respond(t, `{"Platforms":[
			{"general":{"id":"WinDomain","name":"Windows Domain","active":true,"platformBaseID":"WinDomain","platformType":"Regular","systemType":"Windows"},"sessionManagement":{"PSMServerID":"PSMServer_acme"},"properties":{"secret":"never stored"},"secret":"never"},
			{"general":{"id":"Unix","name":"Unix","active":false,"platformBaseID":"Unix","platformType":"Regular","systemType":"Unix"},"sessionManagement":null},
			{"general":{"id":"Database","active":true,"platformBaseID":"Database","platformType":"Regular","systemType":"Database"}}
		],"Total":3}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchPlatforms(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusCompleted || outcome.Pages != 2 || outcome.Received != 3 {
		t.Fatal(outcome)
	}
	if requests.Load() != 1 {
		t.Fatalf("platforms were fetched %d times", requests.Load())
	}
	first := outcome.Objects[0]
	if first.ExternalID != "WinDomain" || first.Attributes["platform_name"] != "Windows Domain" || first.Attributes["active"] != "true" || first.Attributes["platform_base_id"] != "WinDomain" || first.Attributes["platform_type"] != "Regular" || first.Attributes["system_type"] != "Windows" || first.Attributes["psm_server_id"] != "PSMServer_acme" {
		t.Fatal(first)
	}
	for _, forbidden := range []string{"secret", "properties"} {
		if _, found := first.Attributes[forbidden]; found {
			t.Fatal("forbidden platform field stored", forbidden)
		}
	}
	second := outcome.Objects[1]
	if second.ExternalID != "Unix" || second.Attributes["active"] != "false" || second.Attributes["platform_name"] != "Unix" {
		t.Fatal(second)
	}
	third := outcome.Objects[2]
	if third.ExternalID != "Database" || third.Attributes["platform_name"] != "Database" {
		t.Fatal(third)
	}
}

func TestFetchPlatformsTotalAndPermissionFailures(t *testing.T) {
	server := httptest.NewServer(respond(t, `{"Platforms":[{"general":{"id":"WinDomain","name":"Windows"}}],"Total":2}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchPlatforms(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "incomplete_collection" {
		t.Fatal(outcome)
	}

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbidden.Close()
	client, err = New(forbidden.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchPlatforms(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "permission_denied" {
		t.Fatal(outcome)
	}
}

func TestFetchPlatformsPartialByPageBound(t *testing.T) {
	server := httptest.NewServer(respond(t, `{"Platforms":[
		{"general":{"id":"WinDomain","name":"Windows"}},
		{"general":{"id":"Unix","name":"Unix"}},
		{"general":{"id":"Database","name":"Database"}}
	],"Total":3}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchPlatforms(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 1, MaxRecords: 100})
	if outcome.Status != inventory.StatusPartial || outcome.Received != 2 || len(outcome.Warnings) != 1 || outcome.Warnings[0] != "max_pages" {
		t.Fatal(outcome)
	}
}

func TestFetchPlatformsZeroInvalidAndCursor(t *testing.T) {
	server := httptest.NewServer(respond(t, `{"Platforms":[],"Total":0}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchPlatforms(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusCompleted || outcome.Received != 0 || outcome.Pages != 1 {
		t.Fatal(outcome)
	}
	if _, err = client.FetchPage(context.Background(), CollectionPlatforms, "bad-cursor", 2); err == nil {
		t.Fatal("invalid platform cursor accepted")
	}

	for name, body := range map[string]string{
		"missing-general": `{"Platforms":[{"sessionManagement":{}}],"Total":1}`,
		"bad-active":      `{"Platforms":[{"general":{"id":"WinDomain","name":"Windows","active":"yes"}}],"Total":1}`,
		"non-object":      `{"Platforms":["not-an-object"],"Total":1}`,
		"missing-list":    `{"Total":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(respond(t, body))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := fetchPlatforms(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "invalid_object" && outcome.ErrorCode != "invalid_payload" {
				t.Fatal(outcome)
			}
		})
	}
}

func fetchPSMServers(t *testing.T, client *Client, limits inventorysync.Limits) inventorysync.Outcome {
	t.Helper()
	return inventorysync.Fetch(context.Background(), client, CollectionPSMServers, limits, nil)
}

func fetchPSMConnectors(t *testing.T, client *Client, limits inventorysync.Limits) inventorysync.Outcome {
	t.Helper()
	return inventorysync.Fetch(context.Background(), client, CollectionPSMConnectors, limits, nil)
}

func TestFetchPSMServersAllowlistAndPagination(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/API/PSM/Servers" || r.URL.Query().Get("limit") != "1000" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		respond(t, `{"PSMServers":[
			{"ID":"PSMServer_acme","Name":"PSM Acme","Address":"psm.acme.example","Properties":{"secret":"never stored"},"secret":"never"},
			{"ID":"PSMServer_secondary","Name":"PSM Secondary","Address":"psm2.acme.example"},
			{"ID":"PSMServer_unnamed"}
		],"Total":3}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchPSMServers(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusCompleted || outcome.Pages != 2 || outcome.Received != 3 {
		t.Fatal(outcome)
	}
	if requests.Load() != 1 {
		t.Fatalf("PSM servers were fetched %d times", requests.Load())
	}
	first := outcome.Objects[0]
	if first.ExternalID != "PSMServer_acme" || first.Attributes["psm_name"] != "PSM Acme" || first.Attributes["address"] != "psm.acme.example" {
		t.Fatal(first)
	}
	for _, forbidden := range []string{"secret", "Properties"} {
		if _, found := first.Attributes[forbidden]; found {
			t.Fatal("forbidden PSM field stored", forbidden)
		}
	}
	unnamed := outcome.Objects[2]
	if unnamed.ExternalID != "PSMServer_unnamed" || unnamed.Attributes["psm_name"] != "PSMServer_unnamed" {
		t.Fatal(unnamed)
	}
	if _, found := unnamed.Attributes["address"]; found {
		t.Fatal("empty address stored")
	}
}

func TestFetchPSMConnectorsAllowlistAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/API/PSM/Connectors" || r.URL.Query().Get("limit") != "1000" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		respond(t, `{"PSMConnectors":[
			{"ID":"PSM-RDP","DisplayName":"RDP","Type":"RDP","secret":"never"},
			{"ID":"PSM-SSH","DisplayName":"SSH"}
		],"Total":2}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchPSMConnectors(t, client, inventorysync.Limits{PageSize: 1, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusCompleted || outcome.Pages != 2 || outcome.Received != 2 {
		t.Fatal(outcome)
	}
	if outcome.Objects[0].ExternalID != "PSM-RDP" || outcome.Objects[0].Attributes["connector_name"] != "RDP" || outcome.Objects[1].ExternalID != "PSM-SSH" {
		t.Fatal(outcome.Objects)
	}
	if _, found := outcome.Objects[0].Attributes["Type"]; found {
		t.Fatal("forbidden connector field stored")
	}
}

func TestFetchPSMTotalsPermissionsAndPartial(t *testing.T) {
	server := httptest.NewServer(respond(t, `{"PSMServers":[{"ID":"PSMServer_acme","Name":"PSM Acme"}],"Total":2}`))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := fetchPSMServers(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "incomplete_collection" {
		t.Fatal(outcome)
	}

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbidden.Close()
	client, err = New(forbidden.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchPSMConnectors(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100})
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "permission_denied" {
		t.Fatal(outcome)
	}

	partialServer := httptest.NewServer(respond(t, `{"PSMConnectors":[{"ID":"PSM-RDP","DisplayName":"RDP"},{"ID":"PSM-SSH","DisplayName":"SSH"},{"ID":"PSM-SQL","DisplayName":"SQL"}],"Total":3}`))
	defer partialServer.Close()
	client, err = New(partialServer.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = fetchPSMConnectors(t, client, inventorysync.Limits{PageSize: 2, MaxPages: 1, MaxRecords: 100})
	if outcome.Status != inventory.StatusPartial || outcome.Received != 2 || len(outcome.Warnings) != 1 || outcome.Warnings[0] != "max_pages" {
		t.Fatal(outcome)
	}
}

func TestFetchPSMInvalidObjectsAndMissingLists(t *testing.T) {
	for name, tc := range map[string]struct {
		collection string
		body       string
		code       string
	}{
		"missing-servers":    {CollectionPSMServers, `{"Total":0}`, "invalid_payload"},
		"missing-id":         {CollectionPSMServers, `{"PSMServers":[{"Name":"No ID"}],"Total":1}`, "invalid_object"},
		"non-object":         {CollectionPSMConnectors, `{"PSMConnectors":["bad"],"Total":1}`, "invalid_object"},
		"missing-connectors": {CollectionPSMConnectors, `{"Total":0}`, "invalid_payload"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(respond(t, tc.body))
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			outcome := inventorysync.Fetch(context.Background(), client, tc.collection, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100}, nil)
			if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != tc.code {
				t.Fatal(outcome)
			}
		})
	}
}

func TestResolvePSMLink(t *testing.T) {
	catalog := []string{
		"PSMServer_acme", "PSMServer_acme",
		"PSMServer_Case", "PSMServer_case",
		"PSMServer_Other",
	}
	for _, tc := range []struct {
		reference  string
		target     string
		resolution string
	}{
		{"PSMServer_acme", "PSMServer_acme", "exact"},
		{"PSMSERVER_ACME", "PSMServer_acme", "casefold"},
		{"psmserver_case", "", "ambiguous"},
		{"PSMServer_Case", "PSMServer_Case", "exact"},
		{"PSMServer_missing", "PSMServer_missing", "missing"},
	} {
		target, resolution := resolvePSMLink(tc.reference, catalog)
		if target != tc.target || resolution != tc.resolution {
			t.Fatal(tc.reference, target, resolution)
		}
	}
}

func TestFetchPlatformPSMLinksResolutions(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/API/Platforms":
			respond(t, `{"Platforms":[
				{"general":{"id":"WinDomain","name":"Windows"},"sessionManagement":{"PSMServerID":"PSMServer_acme"}},
				{"general":{"id":"Unix","name":"Unix"},"sessionManagement":{"PSMServerID":"PSMSERVER_ACME"}},
				{"general":{"id":"Database","name":"Database"},"sessionManagement":{"PSMServerID":"PSMServer_missing"}},
				{"general":{"id":"Legacy","name":"Legacy"},"sessionManagement":{"PSMServerID":"PSMSERVER_CASE"}},
				{"general":{"id":"NoRef","name":"No Reference"}}
			],"Total":5}`)(w, r)
		case "/API/PSM/Servers":
			respond(t, `{"PSMServers":[
				{"ID":"PSMServer_acme","Name":"PSM Acme"},
				{"ID":"PSMServer_Case","Name":"Case A"},
				{"ID":"PSMServer_case","Name":"Case B"}
			],"Total":3}`)(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := inventorysync.Fetch(context.Background(), client, CollectionPlatformPSMLinks, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100}, nil)
	if outcome.Status != inventory.StatusCompleted || outcome.Pages != 2 || outcome.Received != 4 {
		t.Fatal(outcome)
	}
	if requests.Load() != 2 {
		t.Fatalf("catalogs fetched %d times", requests.Load())
	}
	links := map[string]inventory.Object{}
	for _, object := range outcome.Objects {
		links[object.ExternalID] = object
	}
	exact := links["WinDomain:PSMServer_acme"]
	if exact.Attributes["resolution"] != "exact" || exact.Attributes["psm_server_id"] != "PSMServer_acme" || exact.Attributes["psm_server_name"] != "PSM Acme" {
		t.Fatal(exact)
	}
	casefold := links["Unix:PSMSERVER_ACME"]
	if casefold.Attributes["resolution"] != "casefold" || casefold.Attributes["psm_server_id"] != "PSMServer_acme" || casefold.Attributes["psm_server_name"] != "PSM Acme" {
		t.Fatal(casefold)
	}
	missing := links["Database:PSMServer_missing"]
	if missing.Attributes["resolution"] != "missing" || missing.Attributes["psm_server_id"] != "PSMServer_missing" || missing.Attributes["psm_server_name"] != "PSMServer_missing" {
		t.Fatal(missing)
	}
	ambiguous := links["Legacy:PSMSERVER_CASE"]
	if ambiguous.Attributes["resolution"] != "ambiguous" || ambiguous.Attributes["psm_server_id"] != "" || ambiguous.Attributes["psm_server_name"] != "" {
		t.Fatal(ambiguous)
	}
	if _, found := links["NoRef:"]; found {
		t.Fatal("platform without PSM reference produced a link")
	}
	for _, object := range outcome.Objects {
		if object.Attributes["platform_id"] == "" || object.Attributes["platform_name"] == "" || object.Attributes["reference_psm_id"] == "" {
			t.Fatal(object)
		}
	}
}

func TestFetchPlatformPSMLinksPartialAndFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/API/Platforms":
			respond(t, `{"Platforms":[
				{"general":{"id":"A","name":"A"},"sessionManagement":{"PSMServerID":"PSMServer_acme"}},
				{"general":{"id":"B","name":"B"},"sessionManagement":{"PSMServerID":"PSMServer_acme"}},
				{"general":{"id":"C","name":"C"},"sessionManagement":{"PSMServerID":"PSMServer_acme"}}
			],"Total":3}`)(w, r)
		case "/API/PSM/Servers":
			respond(t, `{"PSMServers":[{"ID":"PSMServer_acme","Name":"PSM Acme"}],"Total":1}`)(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome := inventorysync.Fetch(context.Background(), client, CollectionPlatformPSMLinks, inventorysync.Limits{PageSize: 2, MaxPages: 1, MaxRecords: 100}, nil)
	if outcome.Status != inventory.StatusPartial || outcome.Received != 2 || len(outcome.Warnings) != 1 || outcome.Warnings[0] != "max_pages" {
		t.Fatal(outcome)
	}

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Platforms" {
			respond(t, `{"Platforms":[{"general":{"id":"A","name":"A"},"sessionManagement":{"PSMServerID":"PSMServer_acme"}}],"Total":1}`)(w, r)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbidden.Close()
	client, err = New(forbidden.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = inventorysync.Fetch(context.Background(), client, CollectionPlatformPSMLinks, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100}, nil)
	if outcome.Status != inventory.StatusFailed || outcome.ErrorCode != "permission_denied" {
		t.Fatal(outcome)
	}

	noRefs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/API/Platforms" {
			respond(t, `{"Platforms":[{"general":{"id":"A","name":"A"}}],"Total":1}`)(w, r)
			return
		}
		respond(t, `{"PSMServers":[],"Total":0}`)(w, r)
	}))
	defer noRefs.Close()
	client, err = New(noRefs.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	outcome = inventorysync.Fetch(context.Background(), client, CollectionPlatformPSMLinks, inventorysync.Limits{PageSize: 2, MaxPages: 10, MaxRecords: 100}, nil)
	if outcome.Status != inventory.StatusCompleted || outcome.Received != 0 || outcome.Pages != 1 {
		t.Fatal(outcome)
	}
}

func TestRemoteTenantHTTPSWithPathPrefix(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/PasswordVault/API/Safes" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		respond(t, `{"value":[{"safeNumber":1,"safeName":"Alpha","safeUrlId":"alpha"}],"count":1,"nextLink":""}`)(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL+"/PasswordVault", Options{HTTP: server.Client(), AllowRemote: true})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.FetchPage(context.Background(), CollectionSafes, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 1 || page.Objects[0].ExternalID != "1" {
		t.Fatal(page)
	}
}

func TestRemoteTenantRejectsUnsafeBase(t *testing.T) {
	for _, raw := range []string{
		"http://pam.acme.example",
		"https://user:pass@pam.acme.example",
		"https://pam.acme.example?x=1",
		"https://pam.acme.example#fragment",
		"https://pam.acme.example/a/../b",
	} {
		if _, err := New(raw, Options{AllowRemote: true}); err == nil {
			t.Fatal("unsafe tenant base accepted", raw)
		}
	}
}
