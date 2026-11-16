// Package httpinventory implements the bounded, read-only HTTP adapter for the
// normalized laboratory inventory source. It only accepts numeric loopback
// origins, never follows redirects and never sends credentials.
package httpinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"custodia/server/internal/inventory"
)

const (
	defaultTimeout      = 10 * time.Second
	defaultMaxBodyBytes = 1 << 20
	userAgent           = "custodia-lab-sync/1"
	maxCursorLength     = 1024
	minPageLimit        = 1
	maxPageLimit        = 500
)

// Options configures the laboratory source. Zero values select the bounded
// defaults. The caller's HTTP client is copied before use: its timeout is kept
// when non-zero, redirects are always rejected and its cookie jar is dropped.
type Options struct {
	HTTP         *http.Client
	MaxBodyBytes int64
}

// Client reads normalized pages from one pinned loopback source.
type Client struct {
	base         *url.URL
	client       *http.Client
	maxBodyBytes int64
}

// New validates the laboratory source URL and builds a read-only client.
// Only http URLs whose host is a numeric loopback address are accepted.
func New(baseURL string, options Options) (*Client, error) {
	base, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	client := http.Client{}
	if options.HTTP != nil {
		client = *options.HTTP
	}
	if client.Timeout == 0 {
		client.Timeout = defaultTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client.Jar = nil
	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = defaultMaxBodyBytes
	}
	return &Client{base: base, client: &client, maxBodyBytes: options.MaxBodyBytes}, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("base URL is invalid")
	}
	if parsed.Scheme != "http" {
		return nil, errors.New("base URL must use http")
	}
	if parsed.Opaque != "" {
		return nil, errors.New("base URL must be hierarchical")
	}
	if parsed.User != nil {
		return nil, errors.New("base URL must not include user information")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, errors.New("base URL must not include a query")
	}
	if parsed.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("base URL must not include a fragment")
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, errors.New("base URL requires an explicit host")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("base URL host must be a numeric loopback address")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("base URL port is invalid")
		}
	}
	base := *parsed
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawPath = ""
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	return &base, nil
}

// FetchPage reads one bounded page of a supported collection. An empty cursor
// starts at the beginning; an empty response cursor asserts completion.
func (c *Client) FetchPage(ctx context.Context, collection, cursor string, limit int) (inventory.Page, error) {
	if _, found := inventory.CollectionFor(collection); !found {
		return inventory.Page{}, &inventory.Error{Code: "unsupported_collection", Detail: "unsupported inventory collection"}
	}
	if limit < minPageLimit || limit > maxPageLimit {
		return inventory.Page{}, &inventory.Error{
			Code:   "invalid_limit",
			Detail: fmt.Sprintf("limit must be between %d and %d", minPageLimit, maxPageLimit),
		}
	}
	if !validCursor(cursor) {
		return inventory.Page{}, &inventory.Error{Code: "invalid_cursor", Detail: "inventory cursor exceeds its bounds"}
	}
	target := *c.base
	target.Path = c.base.Path + "/v1/collections/" + collection
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return inventory.Page{}, &inventory.Error{Code: "request_failed", Detail: "could not build the inventory request"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := c.client.Do(request)
	if err != nil {
		return inventory.Page{}, transportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return inventory.Page{}, &inventory.Error{Code: "redirect", Detail: "inventory source redirects are not followed"}
	}
	if response.StatusCode != http.StatusOK {
		return inventory.Page{}, &inventory.Error{
			Code:   fmt.Sprintf("http_status_%d", response.StatusCode),
			Detail: fmt.Sprintf("inventory source returned HTTP %d", response.StatusCode),
		}
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return inventory.Page{}, &inventory.Error{Code: "invalid_content_type", Detail: "inventory response must be application/json"}
	}
	readLimit := c.maxBodyBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, readLimit))
	if err != nil {
		return inventory.Page{}, transportError(err)
	}
	if int64(len(body)) > c.maxBodyBytes {
		return inventory.Page{}, &inventory.Error{
			Code:   "body_too_large",
			Detail: fmt.Sprintf("inventory response exceeds %d bytes", c.maxBodyBytes),
		}
	}
	var payload struct {
		Objects    *[]inventory.Object `json:"objects"`
		NextCursor *string             `json:"next_cursor"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return inventory.Page{}, invalidJSONError()
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return inventory.Page{}, invalidJSONError()
	}
	if payload.Objects == nil || payload.NextCursor == nil {
		// Presence is part of the contract: an absent/null objects or
		// next_cursor is not a confirmed empty collection.
		return inventory.Page{}, invalidJSONError()
	}
	objects := *payload.Objects
	if objects == nil {
		objects = []inventory.Object{}
	}
	if len(objects) > limit {
		return inventory.Page{}, &inventory.Error{
			Code:   "page_too_large",
			Detail: fmt.Sprintf("inventory page exceeds the requested limit of %d objects", limit),
		}
	}
	validated := make([]inventory.Object, 0, len(objects))
	for index, object := range objects {
		normalized, err := inventory.ValidateObject(collection, object)
		if err != nil {
			return inventory.Page{}, &inventory.Error{
				Code:   "invalid_object",
				Detail: fmt.Sprintf("object %d is not valid for collection %q", index, collection),
			}
		}
		validated = append(validated, normalized)
	}
	nextCursor := *payload.NextCursor
	if !validCursor(nextCursor) {
		return inventory.Page{}, &inventory.Error{Code: "invalid_cursor", Detail: "inventory cursor exceeds its bounds"}
	}
	return inventory.Page{Objects: validated, NextCursor: nextCursor}, nil
}

func validCursor(cursor string) bool {
	return len(cursor) <= maxCursorLength && strings.IndexFunc(cursor, unicode.IsControl) < 0
}

func invalidJSONError() *inventory.Error {
	return &inventory.Error{Code: "invalid_json", Detail: "inventory response is not valid JSON"}
}

func transportError(err error) *inventory.Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &inventory.Error{Code: "timeout", Detail: "inventory source request timed out"}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return &inventory.Error{Code: "timeout", Detail: "inventory source request timed out"}
	}
	return &inventory.Error{Code: "request_failed", Detail: "inventory source request failed"}
}
