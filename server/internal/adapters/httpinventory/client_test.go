package httpinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"custodia/server/internal/inventory"
)

func mustClient(t *testing.T, baseURL string, options Options) *Client {
	t.Helper()
	client, err := New(baseURL, options)
	if err != nil {
		t.Fatalf("New(%q): %v", baseURL, err)
	}
	return client
}

func mustErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var inventoryError *inventory.Error
	if !errors.As(err, &inventoryError) {
		t.Fatalf("error %v is not an inventory.Error", err)
	}
	if inventoryError.Code != want {
		t.Fatalf("error code = %q, want %q (detail %q)", inventoryError.Code, want, inventoryError.Detail)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestFetchPageTwoPages(t *testing.T) {
	const cursor = "next page/+&"
	requests := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q", r.Method)
		}
		if r.URL.Path != "/source/v1/collections/users" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if limit := r.URL.Query().Get("limit"); limit != "2" {
			t.Errorf("limit = %q", limit)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Errorf("Accept = %q", accept)
		}
		if agent := r.Header.Get("User-Agent"); agent != "custodia-lab-sync/1" {
			t.Errorf("User-Agent = %q", agent)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("Authorization = %q", authorization)
		}
		if cookies := r.Header.Values("Cookie"); len(cookies) != 0 {
			t.Errorf("Cookie = %q", cookies)
		}
		requests <- r.URL.RequestURI()
		switch r.URL.Query().Get("cursor") {
		case "":
			writeJSON(w, map[string]any{
				"objects": []any{
					map[string]any{"external_id": "u-1", "attributes": map[string]string{"email": "ana@acme.example"}},
					map[string]any{"external_id": "u-2", "attributes": map[string]string{"email": "bo@acme.example"}},
				},
				"next_cursor": cursor,
			})
		case cursor:
			writeJSON(w, map[string]any{
				"objects": []any{
					map[string]any{"external_id": "u-3", "attributes": map[string]string{"email": "cy@acme.example"}},
				},
				"next_cursor": "",
			})
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := mustClient(t, server.URL+"/source/", Options{})
	first, err := client.FetchPage(context.Background(), "users", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Objects) != 2 || first.Objects[0].ExternalID != "u-1" || first.Objects[1].ExternalID != "u-2" {
		t.Fatalf("first page = %#v", first.Objects)
	}
	if first.Objects[0].Attributes["email"] != "ana@acme.example" {
		t.Fatalf("first attributes = %#v", first.Objects[0].Attributes)
	}
	if first.NextCursor != cursor {
		t.Fatalf("next cursor = %q, want %q", first.NextCursor, cursor)
	}
	second, err := client.FetchPage(context.Background(), "users", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Objects) != 1 || second.Objects[0].ExternalID != "u-3" {
		t.Fatalf("second page = %#v", second.Objects)
	}
	if second.NextCursor != "" {
		t.Fatalf("second next cursor = %q", second.NextCursor)
	}
	if got, want := <-requests, "/source/v1/collections/users?limit=2"; got != want {
		t.Fatalf("first request = %q, want %q", got, want)
	}
	wantQuery := url.Values{"limit": {"2"}, "cursor": {cursor}}.Encode()
	if got, want := <-requests, "/source/v1/collections/users?"+wantQuery; got != want {
		t.Fatalf("second request = %q, want %q", got, want)
	}
	if len(requests) != 0 {
		t.Fatalf("unexpected extra requests: %d", len(requests))
	}
}

func TestFetchPageEmptyCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"objects":[],"next_cursor":""}`)
	}))
	defer server.Close()
	client := mustClient(t, server.URL, Options{})
	page, err := client.FetchPage(context.Background(), "users", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 0 {
		t.Fatalf("objects = %#v", page.Objects)
	}
	if page.NextCursor != "" {
		t.Fatalf("next cursor = %q", page.NextCursor)
	}
}

func TestFetchPageRejectsMissingEnvelopeFields(t *testing.T) {
	for _, body := range []string{`{}`, `{"objects":null,"next_cursor":""}`, `{"objects":[]}`, `{"next_cursor":""}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			client := mustClient(t, server.URL, Options{})
			if _, err := client.FetchPage(context.Background(), "users", "", 10); err == nil {
				t.Fatal("missing envelope field accepted as a confirmed empty page")
			} else {
				mustErrorCode(t, err, "invalid_json")
			}
		})
	}
}

func TestFetchPageHTTPStatuses(t *testing.T) {
	const secret = "provider-secret-payload"
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, secret)
			}))
			defer server.Close()
			client := mustClient(t, server.URL, Options{})
			_, err := client.FetchPage(context.Background(), "users", "", 10)
			mustErrorCode(t, err, fmt.Sprintf("http_status_%d", status))
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks the response body: %q", err)
			}
		})
	}
}

func TestFetchPageRejectsInvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed", `{"objects":[`},
		{"unknown field", `{"objects":[],"provider-secret":true}`},
		{"unknown object field", `{"objects":[{"external_id":"u-1","attributes":{"email":"ana@acme.example"},"provider-secret":true}]}`},
		{"trailing document", `{"objects":[]}{"objects":[]}`},
		{"empty body", ``},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := mustClient(t, server.URL, Options{})
			_, err := client.FetchPage(context.Background(), "users", "", 10)
			mustErrorCode(t, err, "invalid_json")
			if strings.Contains(err.Error(), "provider-secret") {
				t.Fatalf("error leaks the response body: %q", err)
			}
		})
	}
}

func TestFetchPageBodyLimit(t *testing.T) {
	body := `{"objects":[],"next_cursor":""}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	exact := mustClient(t, server.URL, Options{MaxBodyBytes: int64(len(body))})
	if _, err := exact.FetchPage(context.Background(), "users", "", 10); err != nil {
		t.Fatalf("body at the limit rejected: %v", err)
	}
	small := mustClient(t, server.URL, Options{MaxBodyBytes: int64(len(body)) - 1})
	_, err := small.FetchPage(context.Background(), "users", "", 10)
	mustErrorCode(t, err, "body_too_large")
}

func TestFetchPageDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			followed.Store(true)
			writeJSON(w, map[string]any{"objects": []any{}, "next_cursor": ""})
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	}))
	defer server.Close()
	client := mustClient(t, server.URL, Options{})
	_, err := client.FetchPage(context.Background(), "users", "", 10)
	mustErrorCode(t, err, "redirect")
	if followed.Load() {
		t.Fatal("redirect target was requested")
	}
}

func TestFetchPageRejectsOversizedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"objects": []any{
				map[string]any{"external_id": "u-1", "attributes": map[string]string{"email": "ana@acme.example"}},
				map[string]any{"external_id": "u-2", "attributes": map[string]string{"email": "bo@acme.example"}},
				map[string]any{"external_id": "u-3", "attributes": map[string]string{"email": "cy@acme.example"}},
			},
			"next_cursor": "",
		})
	}))
	defer server.Close()
	client := mustClient(t, server.URL, Options{})
	_, err := client.FetchPage(context.Background(), "users", "", 2)
	mustErrorCode(t, err, "page_too_large")
}

func TestFetchPageEncodesCursor(t *testing.T) {
	cursor := "page two/+& ü"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/collections/devices" {
			t.Errorf("path = %q", r.URL.Path)
		}
		wantQuery := url.Values{"limit": {"5"}, "cursor": {cursor}}.Encode()
		if r.URL.RawQuery != wantQuery {
			t.Errorf("raw query = %q, want %q", r.URL.RawQuery, wantQuery)
		}
		if got := r.URL.Query().Get("cursor"); got != cursor {
			t.Errorf("decoded cursor = %q, want %q", got, cursor)
		}
		writeJSON(w, map[string]any{"objects": []any{}, "next_cursor": ""})
	}))
	defer server.Close()
	client := mustClient(t, server.URL, Options{})
	if _, err := client.FetchPage(context.Background(), "devices", cursor, 5); err != nil {
		t.Fatal(err)
	}
}

func TestNewValidatesBaseURL(t *testing.T) {
	valid := []string{
		"http://127.0.0.1:8080",
		"http://127.0.0.1:8080/source",
		"http://127.9.9.9/source",
		"http://[::1]:8080/source",
		"HTTP://127.0.0.1:8080",
	}
	for _, raw := range valid {
		if _, err := New(raw, Options{}); err != nil {
			t.Errorf("New(%q) rejected a loopback URL: %v", raw, err)
		}
	}
	invalid := []string{
		"",
		"https://127.0.0.1:8080",
		"ftp://127.0.0.1:8080",
		"//127.0.0.1:8080",
		"http://localhost:8080",
		"http://127.0.0.1.example:8080",
		"http://10.1.2.3:8080",
		"http://192.168.1.10:8080",
		"http://8.8.8.8:8080",
		"http://[::2]:8080",
		"http://[2001:db8::1]:8080",
		"http://2130706433:8080",
		"http://user:pass@127.0.0.1:8080",
		"http://user@127.0.0.1:8080",
		"http://127.0.0.1:8080/?token=secret",
		"http://127.0.0.1:8080/source?",
		"http://127.0.0.1:8080/#fragment",
		"http://127.0.0.1:8080/source#",
		"http://127.0.0.1:notaport",
	}
	for _, raw := range invalid {
		_, err := New(raw, Options{})
		if err == nil {
			t.Errorf("New(%q) accepted an invalid URL", raw)
			continue
		}
		if strings.Contains(raw, "user:pass") && strings.Contains(err.Error(), "pass") {
			t.Errorf("New(%q) leaks user information: %v", raw, err)
		}
	}
}

func TestNewCopiesCallerClient(t *testing.T) {
	var followed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			followed.Store(true)
			writeJSON(w, map[string]any{"objects": []any{}, "next_cursor": ""})
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	}))
	defer server.Close()

	caller := &http.Client{
		Timeout: 7 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return nil
		},
	}
	policy := reflect.ValueOf(caller.CheckRedirect).Pointer()
	client := mustClient(t, server.URL, Options{HTTP: caller})
	if caller.Timeout != 7*time.Second {
		t.Fatalf("caller timeout mutated to %v", caller.Timeout)
	}
	if reflect.ValueOf(caller.CheckRedirect).Pointer() != policy {
		t.Fatal("caller redirect policy mutated")
	}
	if err := caller.CheckRedirect(nil, nil); err != nil {
		t.Fatalf("caller redirect policy changed: %v", err)
	}
	_, err := client.FetchPage(context.Background(), "users", "", 10)
	mustErrorCode(t, err, "redirect")
	if followed.Load() {
		t.Fatal("adapter followed a redirect")
	}

	zero := &http.Client{}
	if _, err := New(server.URL, Options{HTTP: zero}); err != nil {
		t.Fatal(err)
	}
	if zero.Timeout != 0 {
		t.Fatalf("caller zero timeout mutated to %v", zero.Timeout)
	}
}

func TestFetchPageSendsNoCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("Authorization = %q", authorization)
		}
		if cookies := r.Header.Values("Cookie"); len(cookies) != 0 {
			t.Errorf("Cookie = %q", cookies)
		}
		writeJSON(w, map[string]any{"objects": []any{}, "next_cursor": ""})
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(base, []*http.Cookie{{Name: "session", Value: "provider-secret-cookie"}})
	caller := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	client := mustClient(t, server.URL, Options{HTTP: caller})
	if _, err := client.FetchPage(context.Background(), "users", "", 10); err != nil {
		t.Fatal(err)
	}
	if caller.Jar != jar {
		t.Fatal("caller cookie jar mutated")
	}
}

func TestFetchPageContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"plain text", "text/plain", `{"objects":[]}`},
		{"problem json", "application/problem+json", `{"objects":[]}`},
		{"missing", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.contentType != "" {
					w.Header().Set("Content-Type", test.contentType)
				}
				if test.body == "" {
					w.WriteHeader(http.StatusOK)
					return
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := mustClient(t, server.URL, Options{})
			_, err := client.FetchPage(context.Background(), "users", "", 10)
			mustErrorCode(t, err, "invalid_content_type")
		})
	}
}

func TestFetchPageTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()

	t.Run("client timeout", func(t *testing.T) {
		client := mustClient(t, server.URL, Options{HTTP: &http.Client{Timeout: 25 * time.Millisecond}})
		_, err := client.FetchPage(context.Background(), "users", "", 10)
		mustErrorCode(t, err, "timeout")
	})
	t.Run("context deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		client := mustClient(t, server.URL, Options{})
		_, err := client.FetchPage(ctx, "users", "", 10)
		mustErrorCode(t, err, "timeout")
	})
}

func TestFetchPageTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := server.URL
	server.Close()
	client := mustClient(t, baseURL, Options{})
	_, err := client.FetchPage(context.Background(), "users", "", 10)
	mustErrorCode(t, err, "request_failed")
}

func TestFetchPageRejectsInvalidObjects(t *testing.T) {
	tests := []struct {
		name       string
		collection string
		body       string
		secret     string
	}{
		{
			name:       "missing required attribute",
			collection: "users",
			body:       `{"objects":[{"external_id":"u-1","attributes":{"name":"Ana"}}],"next_cursor":""}`,
		},
		{
			name:       "disallowed attribute",
			collection: "users",
			body:       `{"objects":[{"external_id":"u-1","attributes":{"email":"ana@acme.example","ssn":"123-45-6789"}}],"next_cursor":""}`,
			secret:     "123-45-6789",
		},
		{
			name:       "invalid timestamp",
			collection: "devices",
			body:       `{"objects":[{"external_id":"d-1","attributes":{"user_id":"u-1","last_signal_at":"yesterday"}}],"next_cursor":""}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := mustClient(t, server.URL, Options{})
			_, err := client.FetchPage(context.Background(), test.collection, "", 10)
			mustErrorCode(t, err, "invalid_object")
			if test.secret != "" && strings.Contains(err.Error(), test.secret) {
				t.Fatalf("error leaks an attribute value: %q", err)
			}
		})
	}
}

func TestFetchPageCursorBounds(t *testing.T) {
	tests := []struct {
		name     string
		cursor   string
		wantCode string
	}{
		{"at limit", strings.Repeat("a", maxCursorLength), ""},
		{"too long", strings.Repeat("a", maxCursorLength+1), "invalid_cursor"},
		{"control character", "cursor\u0001", "invalid_cursor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cursor := test.cursor
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, map[string]any{"objects": []any{}, "next_cursor": cursor})
			}))
			defer server.Close()
			client := mustClient(t, server.URL, Options{})
			page, err := client.FetchPage(context.Background(), "users", "", 10)
			if test.wantCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if page.NextCursor != cursor {
					t.Fatalf("next cursor = %q, want %q", page.NextCursor, cursor)
				}
				return
			}
			mustErrorCode(t, err, test.wantCode)
		})
	}
}

func TestFetchPageInputValidation(t *testing.T) {
	client := mustClient(t, "http://127.0.0.1:1", Options{})
	tests := []struct {
		name       string
		collection string
		cursor     string
		limit      int
		wantCode   string
	}{
		{"unsupported collection", "groups", "", 10, "unsupported_collection"},
		{"zero limit", "users", "", 0, "invalid_limit"},
		{"limit too large", "users", "", 501, "invalid_limit"},
		{"long cursor", "users", strings.Repeat("a", maxCursorLength+1), 10, "invalid_cursor"},
		{"control cursor", "users", "a\u0001", 10, "invalid_cursor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.FetchPage(context.Background(), test.collection, test.cursor, test.limit)
			mustErrorCode(t, err, test.wantCode)
		})
	}
}
