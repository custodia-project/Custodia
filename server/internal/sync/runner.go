// Package sync implements the bounded read-only extraction loop for normalized
// inventory collections. It owns no transport, storage or credentials.
package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"custodia/server/internal/inventory"
)

const (
	defaultPageSize   = 100
	defaultMaxPages   = 100
	defaultMaxRecords = 10000

	maximumPageSize    = 500
	maximumMaxPages    = 1000
	maximumMaxRecords  = 100000
	maximumCursorBytes = 1024
	maximumErrorDetail = 200
	maximumErrorCode   = 64

	warningMaxPages   = "max_pages"
	warningMaxRecords = "max_records"
)

// Source is one read-only adapter. FetchPage must never return more observations
// than limit and must treat the cursor as opaque. An empty NextCursor asserts
// that the collection was read to its end.
type Source interface {
	FetchPage(ctx context.Context, collection, cursor string, limit int) (inventory.Page, error)
}

// Limits bound one run. Non-positive values take their defaults.
type Limits struct {
	PageSize   int
	MaxPages   int
	MaxRecords int
}

// Outcome is the extracted candidate set and its run history fields. A failed
// run never exposes partial observations; a partial run never implies
// retirements.
type Outcome struct {
	Status      string
	Objects     []inventory.Object
	Pages       int
	Received    int
	Duplicates  int
	ErrorCode   string
	ErrorDetail string
	Warnings    []string
}

func (limits Limits) normalized() Limits {
	if limits.PageSize <= 0 {
		limits.PageSize = defaultPageSize
	}
	if limits.MaxPages <= 0 {
		limits.MaxPages = defaultMaxPages
	}
	if limits.MaxRecords <= 0 {
		limits.MaxRecords = defaultMaxRecords
	}
	return limits
}

func (limits Limits) validationDetail() string {
	if limits.PageSize > maximumPageSize {
		return fmt.Sprintf("page size must not exceed %d", maximumPageSize)
	}
	if limits.MaxPages > maximumMaxPages {
		return fmt.Sprintf("max pages must not exceed %d", maximumMaxPages)
	}
	if limits.MaxRecords > maximumMaxRecords {
		return fmt.Sprintf("max records must not exceed %d", maximumMaxRecords)
	}
	if limits.MaxRecords < limits.PageSize {
		return fmt.Sprintf("max records (%d) must be at least the page size (%d)", limits.MaxRecords, limits.PageSize)
	}
	return ""
}

// Fetch reads one collection from an adapter. Integrity errors discard the
// entire run even when earlier pages were valid; transport and cancellation
// errors after a page return only the observations collected so far.
func Fetch(ctx context.Context, source Source, collection string, limits Limits, progress func(page, received int)) Outcome {
	limits = limits.normalized()
	if detail := limits.validationDetail(); detail != "" {
		return failedOutcome("invalid_limits", detail, 0)
	}
	if _, found := inventory.CollectionFor(collection); !found {
		return failedOutcome("unsupported_collection", "unsupported inventory collection", 0)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if source == nil {
		return failedOutcome("invalid_source", "inventory source is required", 0)
	}

	var collected []inventory.Object
	var warnings []string
	requested := make(map[string]struct{})
	pages := 0
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return failFromError(err, pages, collection, collected, warnings)
		}
		if _, seen := requested[cursor]; seen {
			return failedOutcome("cursor_repeated", "inventory source repeated a cursor", pages)
		}
		requested[cursor] = struct{}{}

		page, err := source.FetchPage(ctx, collection, cursor, limits.PageSize)
		if err != nil {
			// An adapter may translate an in-flight cancellation into a
			// transport error, so the context still owns cancellation unless
			// the adapter reported an integrity failure.
			if ctxErr := ctx.Err(); ctxErr != nil {
				code, _ := classifyError(err)
				if !isIntegrityCode(code) {
					err = ctxErr
				}
			}
			return failFromError(err, pages, collection, collected, warnings)
		}
		if len(page.Objects) > limits.PageSize {
			return failedOutcome("page_too_large", fmt.Sprintf("inventory page has %d objects; page size is %d", len(page.Objects), limits.PageSize), pages)
		}
		pages++

		if len(collected)+len(page.Objects) > limits.MaxRecords {
			// The page was read successfully but is not appended. Progress uses
			// the raw collected count, before final deduplication.
			reportProgress(progress, pages, len(collected))
			warnings = append(warnings, warningMaxRecords)
			return finalize(collection, inventory.StatusPartial, pages, collected, warnings, "", "")
		}
		collected = append(collected, page.Objects...)
		// Progress reports raw observations before final dedupe so a duplicate
		// count never rewrites what the source actually returned.
		reportProgress(progress, pages, len(collected))

		// A source may ignore cancellation and return a page. Keep that complete
		// page but never continue as if the run were uninterrupted.
		if err := ctx.Err(); err != nil {
			return failFromError(err, pages, collection, collected, warnings)
		}

		if page.NextCursor == "" {
			return finalize(collection, inventory.StatusCompleted, pages, collected, warnings, "", "")
		}
		if detail := cursorValidationDetail(page.NextCursor); detail != "" {
			return failedOutcome("invalid_cursor", detail, pages)
		}
		if _, seen := requested[page.NextCursor]; seen {
			return failedOutcome("cursor_repeated", "inventory source repeated a cursor", pages)
		}
		if pages >= limits.MaxPages {
			warnings = append(warnings, warningMaxPages)
			return finalize(collection, inventory.StatusPartial, pages, collected, warnings, "", "")
		}
		cursor = page.NextCursor
	}
}

func reportProgress(progress func(page, received int), pages, received int) {
	if progress != nil {
		progress(pages, received)
	}
}

func cursorValidationDetail(cursor string) string {
	if len(cursor) > maximumCursorBytes {
		return "inventory cursor exceeds its bound"
	}
	if strings.IndexFunc(cursor, unicode.IsControl) >= 0 {
		return "inventory cursor contains control characters"
	}
	return ""
}

func failFromError(err error, pages int, collection string, collected []inventory.Object, warnings []string) Outcome {
	code, detail := classifyError(err)
	if pages == 0 || isIntegrityCode(code) {
		return failedOutcome(code, detail, pages)
	}
	return finalize(collection, inventory.StatusPartial, pages, collected, warnings, code, detail)
}

func isIntegrityCode(code string) bool {
	switch code {
	case "invalid_json",
		"invalid_content_type",
		"body_too_large",
		"page_too_large",
		"invalid_cursor",
		"cursor_repeated",
		"invalid_limits",
		"invalid_object",
		"attribute_not_allowed",
		"attribute_too_long",
		"attribute_control_char",
		"missing_attribute",
		"invalid_email",
		"invalid_timestamp",
		"invalid_external_id",
		"duplicate_conflict",
		"unsupported_collection":
		return true
	default:
		return false
	}
}

func classifyError(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return "canceled", "inventory sync canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "inventory sync timed out"
	}
	type codedError interface {
		error
		ErrorCode() string
	}
	var coded codedError
	if errors.As(err, &coded) {
		if code := boundedCode(coded.ErrorCode()); code != "" {
			return code, boundedDetail(err.Error(), code)
		}
	}
	return "fetch_failed", boundedDetail(err.Error(), "inventory source failed")
}

func finalize(collection, status string, pages int, collected []inventory.Object, warnings []string, code, detail string) Outcome {
	unique, duplicates, err := inventory.Deduplicate(collection, collected)
	if err != nil {
		code, detail := classifyError(err)
		return failedOutcome(code, detail, pages)
	}
	return Outcome{
		Status:      status,
		Objects:     unique,
		Pages:       pages,
		Received:    len(unique),
		Duplicates:  duplicates,
		ErrorCode:   code,
		ErrorDetail: detail,
		Warnings:    warnings,
	}
}

func failedOutcome(code, detail string, pages int) Outcome {
	code = boundedCode(code)
	if code == "" {
		code = "unknown"
	}
	return Outcome{
		Status:      inventory.StatusFailed,
		Pages:       pages,
		ErrorCode:   code,
		ErrorDetail: boundedDetail(detail, code),
	}
}

func boundedCode(code string) string {
	var result strings.Builder
	for _, r := range strings.TrimSpace(code) {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		result.WriteRune(r)
		if result.Len() >= maximumErrorCode {
			break
		}
	}
	return result.String()
}

func boundedDetail(detail, fallback string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, detail)
	clean = strings.Join(strings.Fields(clean), " ")
	if clean == "" {
		clean = fallback
	}
	runes := []rune(clean)
	if len(runes) > maximumErrorDetail {
		clean = string(runes[:maximumErrorDetail])
	}
	return clean
}
