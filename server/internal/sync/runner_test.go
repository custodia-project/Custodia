package sync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"custodia/server/internal/inventory"
)

type fakeCall struct {
	collection string
	cursor     string
	limit      int
}

type fakePage struct {
	objects  []inventory.Object
	next     string
	expected int
	warnings []string
	err      error
}

type fakeSource struct {
	pages   []fakePage
	dynamic func(ctx context.Context, call fakeCall) (inventory.Page, error)
	calls   []fakeCall
}

func (source *fakeSource) FetchPage(ctx context.Context, collection, cursor string, limit int) (inventory.Page, error) {
	call := fakeCall{collection: collection, cursor: cursor, limit: limit}
	source.calls = append(source.calls, call)
	if source.dynamic != nil {
		return source.dynamic(ctx, call)
	}
	index := len(source.calls) - 1
	if index >= len(source.pages) {
		return inventory.Page{}, fmt.Errorf("unexpected page %d", index+1)
	}
	page := source.pages[index]
	if page.err != nil {
		return inventory.Page{}, page.err
	}
	return inventory.Page{Objects: page.objects, NextCursor: page.next, Expected: page.expected, Warnings: page.warnings}, nil
}

func userObject(id, email string) inventory.Object {
	return inventory.Object{ExternalID: id, Attributes: map[string]string{"email": email}}
}

func deviceObject(id, userID, lastSignal string) inventory.Object {
	attributes := map[string]string{"user_id": userID}
	if lastSignal != "" {
		attributes["last_signal_at"] = lastSignal
	}
	return inventory.Object{ExternalID: id, Attributes: attributes}
}

func objectIDs(objects []inventory.Object) []string {
	ids := make([]string, len(objects))
	for index := range objects {
		ids[index] = objects[index].ExternalID
	}
	return ids
}

func callCursors(calls []fakeCall) []string {
	cursors := make([]string, len(calls))
	for index := range calls {
		cursors[index] = calls[index].cursor
	}
	return cursors
}

func requireStatus(t *testing.T, outcome Outcome, status, code string) {
	t.Helper()
	if outcome.Status != status {
		t.Fatalf("status = %q, want %q", outcome.Status, status)
	}
	if outcome.ErrorCode != code {
		t.Fatalf("error code = %q, want %q", outcome.ErrorCode, code)
	}
	if len([]rune(outcome.ErrorDetail)) > 200 {
		t.Fatalf("error detail is not bounded: %d runes", len([]rune(outcome.ErrorDetail)))
	}
}

func requireObjectIDs(t *testing.T, objects []inventory.Object, want ...string) {
	t.Helper()
	if got := objectIDs(objects); !slices.Equal(got, want) {
		t.Fatalf("object IDs = %v, want %v", got, want)
	}
}

func requireFailedWithoutObjects(t *testing.T, outcome Outcome, code string) {
	t.Helper()
	requireStatus(t, outcome, inventory.StatusFailed, code)
	if outcome.Objects != nil {
		t.Fatalf("failed outcome exposed objects: %v", objectIDs(outcome.Objects))
	}
	if outcome.Received != 0 || outcome.Duplicates != 0 {
		t.Fatalf("failed outcome counters = received %d duplicates %d, want zero", outcome.Received, outcome.Duplicates)
	}
}

func TestFetchCompletesPagination(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
		{objects: []inventory.Object{userObject("user-2", "two@acme.example")}, next: "cursor-2"},
		{objects: nil, next: ""},
	}}
	var progress [][2]int
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, func(page, received int) {
		progress = append(progress, [2]int{page, received})
	})
	requireStatus(t, outcome, inventory.StatusCompleted, "")
	if outcome.Pages != 3 || outcome.Received != 2 || outcome.Duplicates != 0 {
		t.Fatalf("outcome = pages %d received %d duplicates %d", outcome.Pages, outcome.Received, outcome.Duplicates)
	}
	requireObjectIDs(t, outcome.Objects, "user-1", "user-2")
	if len(outcome.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", outcome.Warnings)
	}
	if got, want := callCursors(source.calls), []string{"", "cursor-1", "cursor-2"}; !slices.Equal(got, want) {
		t.Fatalf("cursors = %v, want %v", got, want)
	}
	for _, call := range source.calls {
		if call.collection != "users" || call.limit != 10 {
			t.Fatalf("call = %+v, want users limit 10", call)
		}
	}
	if want := [][2]int{{1, 1}, {2, 2}, {3, 2}}; !slices.Equal(progress, want) {
		t.Fatalf("progress = %v, want %v", progress, want)
	}
}

func TestFetchFirstPageError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{name: "plain", err: errors.New("upstream unavailable"), code: "fetch_failed"},
		{name: "coded", err: &inventory.Error{Code: "network_error", Detail: "upstream unavailable"}, code: "network_error"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeSource{pages: []fakePage{{err: test.err}}}
			outcome := Fetch(context.Background(), source, "users", Limits{}, nil)
			requireFailedWithoutObjects(t, outcome, test.code)
			if outcome.Pages != 0 {
				t.Fatalf("pages = %d, want 0", outcome.Pages)
			}
			if outcome.ErrorDetail == "" {
				t.Fatal("empty error detail")
			}
		})
	}
}

func TestFetchRejectsOutOfBoundsLimits(t *testing.T) {
	for _, limits := range []Limits{
		{PageSize: 501, MaxPages: 10, MaxRecords: 1000},
		{PageSize: 10, MaxPages: 1001, MaxRecords: 1000},
		{PageSize: 10, MaxPages: 10, MaxRecords: 100001},
	} {
		outcome := Fetch(context.Background(), &fakeSource{}, "users", limits, nil)
		requireFailedWithoutObjects(t, outcome, "invalid_limits")
	}
}

func TestFetchBodyTooLargeAfterPageFailsWithoutObjects(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
		{err: &inventory.Error{Code: "body_too_large", Detail: "response exceeds the body limit"}},
	}}
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
	requireFailedWithoutObjects(t, outcome, "body_too_large")
}

func TestFetchSecondPageErrorReturnsPartial(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
		{err: &inventory.Error{Code: "network_error", Detail: "connection reset"}},
	}}
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
	requireStatus(t, outcome, inventory.StatusPartial, "network_error")
	if outcome.Pages != 1 || outcome.Received != 1 || outcome.Duplicates != 0 {
		t.Fatalf("outcome = pages %d received %d duplicates %d", outcome.Pages, outcome.Received, outcome.Duplicates)
	}
	requireObjectIDs(t, outcome.Objects, "user-1")
	if outcome.ErrorDetail != "connection reset" {
		t.Fatalf("error detail = %q", outcome.ErrorDetail)
	}
	if len(outcome.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", outcome.Warnings)
	}
}

func TestFetchRemovesIdenticalDuplicatesAcrossPages(t *testing.T) {
	identical := userObject("user-1", "one@acme.example")
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{identical}, next: "cursor-1"},
		{objects: []inventory.Object{identical}, next: ""},
	}}
	var progress [][2]int
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, func(page, received int) {
		progress = append(progress, [2]int{page, received})
	})
	requireStatus(t, outcome, inventory.StatusCompleted, "")
	if outcome.Pages != 2 || outcome.Received != 1 || outcome.Duplicates != 1 {
		t.Fatalf("outcome = pages %d received %d duplicates %d", outcome.Pages, outcome.Received, outcome.Duplicates)
	}
	requireObjectIDs(t, outcome.Objects, "user-1")
	// Progress is raw: the duplicate is counted until the final dedupe.
	if want := [][2]int{{1, 1}, {2, 2}}; !slices.Equal(progress, want) {
		t.Fatalf("progress = %v, want %v", progress, want)
	}
}

func TestFetchRejectsContradictoryDuplicate(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
		{objects: []inventory.Object{userObject("user-1", "changed@acme.example")}, next: ""},
	}}
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
	requireFailedWithoutObjects(t, outcome, "duplicate_conflict")
	if outcome.Pages != 2 {
		t.Fatalf("pages = %d, want 2", outcome.Pages)
	}
}

func TestFetchRejectsRepeatedCursor(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "same"},
		{objects: []inventory.Object{userObject("user-2", "two@acme.example")}, next: "same"},
	}}
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
	requireFailedWithoutObjects(t, outcome, "cursor_repeated")
	if outcome.Pages != 2 {
		t.Fatalf("pages = %d, want 2", outcome.Pages)
	}
	if len(source.calls) != 2 {
		t.Fatalf("fetches = %d, want 2", len(source.calls))
	}
}

func TestFetchStopsAtMaxPages(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
		{objects: []inventory.Object{userObject("user-2", "two@acme.example")}, next: "cursor-2"},
		{objects: []inventory.Object{userObject("user-3", "three@acme.example")}, next: ""},
	}}
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 2, MaxRecords: 100}, nil)
	requireStatus(t, outcome, inventory.StatusPartial, "")
	if outcome.Pages != 2 || outcome.Received != 2 {
		t.Fatalf("outcome = pages %d received %d", outcome.Pages, outcome.Received)
	}
	requireObjectIDs(t, outcome.Objects, "user-1", "user-2")
	if want := []string{"max_pages"}; !slices.Equal(outcome.Warnings, want) {
		t.Fatalf("warnings = %v, want %v", outcome.Warnings, want)
	}
	if len(source.calls) != 2 {
		t.Fatalf("fetches = %d, want 2", len(source.calls))
	}
}

func TestFetchStopsBeforeExceedingMaxRecords(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("user-1", "one@acme.example"), userObject("user-2", "two@acme.example")}, next: "cursor-1"},
		{objects: []inventory.Object{userObject("user-3", "three@acme.example"), userObject("user-4", "four@acme.example")}, next: "cursor-2"},
	}}
	var progress [][2]int
	outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 2, MaxPages: 10, MaxRecords: 3}, func(page, received int) {
		progress = append(progress, [2]int{page, received})
	})
	requireStatus(t, outcome, inventory.StatusPartial, "")
	if outcome.Pages != 2 || outcome.Received != 2 || outcome.Duplicates != 0 {
		t.Fatalf("outcome = pages %d received %d duplicates %d", outcome.Pages, outcome.Received, outcome.Duplicates)
	}
	// The overflowing page is not appended, not truncated.
	requireObjectIDs(t, outcome.Objects, "user-1", "user-2")
	if want := []string{"max_records"}; !slices.Equal(outcome.Warnings, want) {
		t.Fatalf("warnings = %v, want %v", outcome.Warnings, want)
	}
	if want := [][2]int{{1, 2}, {2, 2}}; !slices.Equal(progress, want) {
		t.Fatalf("progress = %v, want %v", progress, want)
	}
	for _, pair := range progress {
		if pair[1] > 3 {
			t.Fatalf("progress exceeded max records: %v", pair)
		}
	}
	if len(source.calls) != 2 {
		t.Fatalf("fetches = %d, want 2", len(source.calls))
	}
}

func TestFetchRejectsOversizedPage(t *testing.T) {
	three := []inventory.Object{
		userObject("user-1", "one@acme.example"),
		userObject("user-2", "two@acme.example"),
		userObject("user-3", "three@acme.example"),
	}
	cases := []struct {
		name      string
		pages     []fakePage
		wantPages int
	}{
		{name: "first page", pages: []fakePage{{objects: three}}, wantPages: 0},
		{name: "after earlier page", pages: []fakePage{
			{objects: []inventory.Object{userObject("user-0", "zero@acme.example")}, next: "cursor-1"},
			{objects: three},
		}, wantPages: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeSource{pages: test.pages}
			outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 2, MaxPages: 10, MaxRecords: 10}, nil)
			requireFailedWithoutObjects(t, outcome, "page_too_large")
			if outcome.Pages != test.wantPages {
				t.Fatalf("pages = %d, want %d", outcome.Pages, test.wantPages)
			}
			if outcome.ErrorDetail == "" {
				t.Fatal("empty error detail")
			}
		})
	}
}

func TestFetchIntegrityFailureDiscardsEarlierPages(t *testing.T) {
	notAllowed := inventory.Object{ExternalID: "user-2", Attributes: map[string]string{"email": "two@acme.example", "role": "admin"}}
	cases := []struct {
		name       string
		collection string
		pages      []fakePage
		code       string
		wantPages  int
	}{
		{
			name:       "malformed JSON from source",
			collection: "users",
			pages: []fakePage{
				{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
				{err: &inventory.Error{Code: "invalid_json", Detail: "response body is not valid JSON"}},
			},
			code:      "invalid_json",
			wantPages: 1,
		},
		{
			name:       "invalid email",
			collection: "users",
			pages: []fakePage{
				{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
				{objects: []inventory.Object{{ExternalID: "user-2", Attributes: map[string]string{"email": "not-an-email"}}}},
			},
			code:      "invalid_email",
			wantPages: 2,
		},
		{
			name:       "invalid timestamp",
			collection: "devices",
			pages: []fakePage{
				{objects: []inventory.Object{deviceObject("device-1", "user-1", "2026-11-16T12:00:00Z")}, next: "cursor-1"},
				{objects: []inventory.Object{deviceObject("device-2", "user-1", "yesterday")}},
			},
			code:      "invalid_timestamp",
			wantPages: 2,
		},
		{
			name:       "attribute not allowed",
			collection: "users",
			pages: []fakePage{
				{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
				{objects: []inventory.Object{notAllowed}},
			},
			code:      "attribute_not_allowed",
			wantPages: 2,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeSource{pages: test.pages}
			outcome := Fetch(context.Background(), source, test.collection, Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
			requireFailedWithoutObjects(t, outcome, test.code)
			if outcome.Pages != test.wantPages {
				t.Fatalf("pages = %d, want %d", outcome.Pages, test.wantPages)
			}
			if outcome.ErrorDetail == "" {
				t.Fatal("empty error detail")
			}
		})
	}
}

func TestFetchRejectsInvalidCursors(t *testing.T) {
	cases := []struct {
		name   string
		cursor string
	}{
		{name: "control character", cursor: "cursor\x00next"},
		{name: "too long", cursor: strings.Repeat("x", maximumCursorBytes+1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeSource{pages: []fakePage{
				{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: test.cursor},
			}}
			outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
			requireFailedWithoutObjects(t, outcome, "invalid_cursor")
			if outcome.Pages != 1 {
				t.Fatalf("pages = %d, want 1", outcome.Pages)
			}
			if len(source.calls) != 1 {
				t.Fatalf("fetches = %d, want 1", len(source.calls))
			}
		})
	}
}

func TestFetchContextCancellation(t *testing.T) {
	t.Run("before first page", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		source := &fakeSource{pages: []fakePage{{objects: []inventory.Object{userObject("user-1", "one@acme.example")}}}}
		outcome := Fetch(ctx, source, "users", Limits{}, nil)
		requireFailedWithoutObjects(t, outcome, "canceled")
		if outcome.Pages != 0 {
			t.Fatalf("pages = %d, want 0", outcome.Pages)
		}
		if len(source.calls) != 0 {
			t.Fatalf("fetches = %d, want 0", len(source.calls))
		}
	})

	t.Run("between pages", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		source := &fakeSource{}
		source.dynamic = func(context.Context, fakeCall) (inventory.Page, error) {
			cancel()
			return inventory.Page{
				Objects:    []inventory.Object{userObject("user-1", "one@acme.example")},
				NextCursor: "cursor-1",
			}, nil
		}
		outcome := Fetch(ctx, source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
		requireStatus(t, outcome, inventory.StatusPartial, "canceled")
		if outcome.Pages != 1 || outcome.Received != 1 {
			t.Fatalf("outcome = pages %d received %d", outcome.Pages, outcome.Received)
		}
		requireObjectIDs(t, outcome.Objects, "user-1")
		if len(source.calls) != 1 {
			t.Fatalf("fetches = %d, want 1", len(source.calls))
		}
	})

	t.Run("fetch returns canceled", func(t *testing.T) {
		source := &fakeSource{pages: []fakePage{
			{objects: []inventory.Object{userObject("user-1", "one@acme.example")}, next: "cursor-1"},
			{err: context.Canceled},
		}}
		outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
		requireStatus(t, outcome, inventory.StatusPartial, "canceled")
		if outcome.Pages != 1 || outcome.Received != 1 {
			t.Fatalf("outcome = pages %d received %d", outcome.Pages, outcome.Received)
		}
		requireObjectIDs(t, outcome.Objects, "user-1")
	})

	t.Run("coded transport error after cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		fetches := 0
		source := &fakeSource{}
		source.dynamic = func(context.Context, fakeCall) (inventory.Page, error) {
			fetches++
			if fetches == 1 {
				return inventory.Page{
					Objects:    []inventory.Object{userObject("user-1", "one@acme.example")},
					NextCursor: "cursor-1",
				}, nil
			}
			cancel()
			return inventory.Page{}, &inventory.Error{Code: "request_failed", Detail: "request canceled"}
		}
		outcome := Fetch(ctx, source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
		requireStatus(t, outcome, inventory.StatusPartial, "canceled")
		if outcome.Pages != 1 || outcome.Received != 1 {
			t.Fatalf("outcome = pages %d received %d", outcome.Pages, outcome.Received)
		}
		requireObjectIDs(t, outcome.Objects, "user-1")
	})

	t.Run("final page returned after cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		source := &fakeSource{}
		source.dynamic = func(context.Context, fakeCall) (inventory.Page, error) {
			cancel()
			return inventory.Page{Objects: []inventory.Object{userObject("user-1", "one@acme.example")}}, nil
		}
		outcome := Fetch(ctx, source, "users", Limits{PageSize: 10, MaxPages: 10, MaxRecords: 100}, nil)
		requireStatus(t, outcome, inventory.StatusPartial, "canceled")
		if outcome.Pages != 1 || outcome.Received != 1 {
			t.Fatalf("outcome = pages %d received %d", outcome.Pages, outcome.Received)
		}
		requireObjectIDs(t, outcome.Objects, "user-1")
	})
}

func TestFetchDefaultLimits(t *testing.T) {
	source := &fakeSource{}
	source.dynamic = func(context.Context, fakeCall) (inventory.Page, error) {
		return inventory.Page{NextCursor: fmt.Sprintf("cursor-%d", len(source.calls))}, nil
	}
	outcome := Fetch(context.Background(), source, "users", Limits{}, nil)
	requireStatus(t, outcome, inventory.StatusPartial, "")
	if outcome.Pages != 100 || outcome.Received != 0 {
		t.Fatalf("outcome = pages %d received %d", outcome.Pages, outcome.Received)
	}
	if want := []string{"max_pages"}; !slices.Equal(outcome.Warnings, want) {
		t.Fatalf("warnings = %v, want %v", outcome.Warnings, want)
	}
	if len(source.calls) != 100 {
		t.Fatalf("fetches = %d, want 100", len(source.calls))
	}
	for _, call := range source.calls {
		if call.limit != 100 {
			t.Fatalf("default page size = %d, want 100", call.limit)
		}
	}
}

func TestLimitsDefaults(t *testing.T) {
	want := Limits{PageSize: 100, MaxPages: 100, MaxRecords: 10000}
	for _, limits := range []Limits{{}, {PageSize: -1, MaxPages: -10, MaxRecords: -100}} {
		if got := limits.normalized(); got != want {
			t.Fatalf("normalized(%+v) = %+v, want %+v", limits, got, want)
		}
	}
}

func TestFetchRejectsInvalidLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
	}{
		{name: "page size too large", limits: Limits{PageSize: 501, MaxRecords: 10000}},
		{name: "max records below page size", limits: Limits{PageSize: 100, MaxRecords: 99}},
		{name: "default page size exceeds max records", limits: Limits{PageSize: 0, MaxRecords: 50}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeSource{pages: []fakePage{{}}}
			outcome := Fetch(context.Background(), source, "users", test.limits, nil)
			requireFailedWithoutObjects(t, outcome, "invalid_limits")
			if outcome.Pages != 0 {
				t.Fatalf("pages = %d, want 0", outcome.Pages)
			}
			if len(source.calls) != 0 {
				t.Fatalf("fetches = %d, want 0", len(source.calls))
			}
		})
	}

	t.Run("maximum accepted", func(t *testing.T) {
		source := &fakeSource{pages: []fakePage{{}}}
		outcome := Fetch(context.Background(), source, "users", Limits{PageSize: 500, MaxPages: 1, MaxRecords: 500}, nil)
		requireStatus(t, outcome, inventory.StatusCompleted, "")
		if outcome.Pages != 1 {
			t.Fatalf("pages = %d, want 1", outcome.Pages)
		}
		if len(source.calls) != 1 || source.calls[0].limit != 500 {
			t.Fatalf("calls = %+v", source.calls)
		}
	})
}

func TestFetchRejectsUnsupportedCollection(t *testing.T) {
	source := &fakeSource{pages: []fakePage{{}}}
	outcome := Fetch(context.Background(), source, "assets", Limits{}, nil)
	requireFailedWithoutObjects(t, outcome, "unsupported_collection")
	if outcome.Pages != 0 {
		t.Fatalf("pages = %d, want 0", outcome.Pages)
	}
	if len(source.calls) != 0 {
		t.Fatalf("fetches = %d, want 0", len(source.calls))
	}
}

func TestFetchBoundsErrorDetail(t *testing.T) {
	source := &fakeSource{pages: []fakePage{{err: errors.New(strings.Repeat("x", 500) + "\nsecret-payload")}}}
	outcome := Fetch(context.Background(), source, "users", Limits{}, nil)
	requireFailedWithoutObjects(t, outcome, "fetch_failed")
	if len([]rune(outcome.ErrorDetail)) > 200 {
		t.Fatalf("detail is not bounded: %d runes", len([]rune(outcome.ErrorDetail)))
	}
	if strings.Contains(outcome.ErrorDetail, "\n") {
		t.Fatal("detail contains a control character")
	}
	if strings.Contains(outcome.ErrorDetail, "secret-payload") {
		t.Fatal("detail was not truncated")
	}
}

func TestFetchPageWarningsFinishPartial(t *testing.T) {
	source := &fakeSource{pages: []fakePage{{
		objects:  []inventory.Object{userObject("u1", "a@acme.example")},
		warnings: []string{"skipped_records", "skipped_records"},
	}}}
	outcome := Fetch(context.Background(), source, "users", Limits{}, nil)
	requireStatus(t, outcome, inventory.StatusPartial, "")
	requireObjectIDs(t, outcome.Objects, "u1")
	if len(outcome.Warnings) != 1 || outcome.Warnings[0] != "skipped_records" {
		t.Fatal(outcome.Warnings)
	}
}

func TestFetchChecksProviderReportedTotal(t *testing.T) {
	source := &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("u1", "a@acme.example")}, next: "next", expected: 2},
		{objects: []inventory.Object{userObject("u2", "b@acme.example")}, expected: 2},
	}}
	outcome := Fetch(context.Background(), source, "users", Limits{}, nil)
	requireStatus(t, outcome, inventory.StatusCompleted, "")
	requireObjectIDs(t, outcome.Objects, "u1", "u2")

	source = &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("u1", "a@acme.example")}, expected: 2},
	}}
	outcome = Fetch(context.Background(), source, "users", Limits{}, nil)
	requireFailedWithoutObjects(t, outcome, "incomplete_collection")

	source = &fakeSource{pages: []fakePage{
		{objects: []inventory.Object{userObject("u1", "a@acme.example")}, next: "next", expected: 2},
		{objects: []inventory.Object{userObject("u2", "b@acme.example")}, expected: 3},
	}}
	outcome = Fetch(context.Background(), source, "users", Limits{}, nil)
	requireFailedWithoutObjects(t, outcome, "total_changed")

	source = &fakeSource{pages: []fakePage{{expected: -1}}}
	outcome = Fetch(context.Background(), source, "users", Limits{}, nil)
	requireFailedWithoutObjects(t, outcome, "invalid_total")
}
