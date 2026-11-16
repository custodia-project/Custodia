// Package cyberarkpam reads the bounded Safes collection from the CyberArk
// PAM REST contract. This first slice only supports /API/Safes over a numeric
// loopback HTTP fixture: it sends no credentials, reads no accounts/members or
// secrets and never stores the provider payload. The allowlisted Safe fields
// are the same metadata DS Hub persists, translated to the normalized
// inventory page contract.
package cyberarkpam

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"custodia/server/internal/inventory"
)

const (
	// CollectionSafes, CollectionSafeMembers, CollectionAccounts and
	// CollectionPlatforms are the collections emitted by this adapter.
	CollectionSafes            = "safes"
	CollectionSafeMembers      = "safe_members"
	CollectionAccounts         = "accounts"
	CollectionPlatforms        = "platforms"
	CollectionPSMServers       = "psm_servers"
	CollectionPSMConnectors    = "psm_connectors"
	CollectionPlatformPSMLinks = "platform_psm_links"

	defaultTimeout       = 15 * time.Second
	defaultMaxBodyBytes  = 1 << 20
	userAgent            = "custodia-cyberark-pam-safes/1"
	maxCursorLength      = 1024
	maxMemberNextLength  = 700
	minPageLimit         = 1
	maxPageLimit         = 500
	safesPath            = "/API/Safes"
	accountsPath         = "/API/Accounts"
	platformsPath        = "/API/Platforms"
	psmServersPath       = "/API/PSM/Servers"
	psmConnectorsPath    = "/API/PSM/Connectors"
	maxEpochSeconds      = int64(253402300799) // 9999-12-31T23:59:59Z
	maxSecretModified    = int64(4102444800)   // 2100-01-01T00:00:00Z
	maxCatalogObjects    = 5000
	safesEnumerationSize = 100
	maxSafeEnumeration   = 10000
	maxSafePages         = 100
)

// Options configures the read-only Safes client. Zero values select bounded
// defaults. The caller's HTTP client is copied; redirects are always rejected
// and its cookie jar is dropped.
type Options struct {
	HTTP         *http.Client
	MaxBodyBytes int64
}

// Client reads one bounded page of CyberArk Safes or Safe members from a
// pinned origin. The member collection caches the Safe list and per-Safe
// member counts for the lifetime of one run.
type Client struct {
	base         *url.URL
	client       *http.Client
	maxBodyBytes int64

	mu               sync.Mutex
	safes            []safeRef
	safesLoaded      bool
	memberCounts     map[int]int
	memberSeen       map[int]int
	memberKnown      bool
	accountsSkipped  bool
	platforms        catalogState
	psmServers       catalogState
	psmConnectors    catalogState
	platformPSMLinks catalogState
}

type catalogState struct {
	objects []inventory.Object
	loaded  bool
	total   int
}

type catalogSpec struct {
	path      string
	query     string
	key       string
	max       int
	mapObject func(json.RawMessage) (inventory.Object, error)
}

type safeRef struct {
	Number int64
	Name   string
}

// New validates the fixture URL and builds a read-only client. The laboratory
// profile deliberately accepts only http on a numeric loopback address: TLS,
// provider credentials and a real tenant profile belong to a later,
// explicitly authorized slice.
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
	return &Client{
		base: base, client: &client, maxBodyBytes: options.MaxBodyBytes,
		memberCounts: map[int]int{}, memberSeen: map[int]int{},
	}, nil
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
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("base URL must not include a path prefix")
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
	base.Path = ""
	base.RawPath = ""
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	return &base, nil
}

// FetchPage reads one bounded page of Safes or Safe members. The cursor is
// opaque to the runner: for Safes it is the provider nextLink; for members it
// also carries the Safe being traversed.
func (c *Client) FetchPage(ctx context.Context, collection, cursor string, limit int) (inventory.Page, error) {
	if limit < minPageLimit || limit > maxPageLimit {
		return inventory.Page{}, adapterError("invalid_limit", fmt.Sprintf("limit must be between %d and %d", minPageLimit, maxPageLimit))
	}
	switch collection {
	case CollectionSafes:
		target, err := c.pageURL(cursor, limit)
		if err != nil {
			return inventory.Page{}, err
		}
		body, err := c.fetchBody(ctx, target)
		if err != nil {
			return inventory.Page{}, err
		}
		return decodePage(body, limit)
	case CollectionSafeMembers:
		return c.fetchMembersPage(ctx, cursor, limit)
	case CollectionAccounts:
		return c.fetchAccountsPage(ctx, cursor, limit)
	case CollectionPlatforms:
		return c.fetchCatalogPage(ctx, cursor, limit, platformCatalog, &c.platforms)
	case CollectionPSMServers:
		return c.fetchCatalogPage(ctx, cursor, limit, psmServerCatalog, &c.psmServers)
	case CollectionPSMConnectors:
		return c.fetchCatalogPage(ctx, cursor, limit, psmConnectorCatalog, &c.psmConnectors)
	case CollectionPlatformPSMLinks:
		return c.fetchPlatformPSMLinksPage(ctx, cursor, limit)
	default:
		return inventory.Page{}, adapterError("unsupported_collection", "unsupported inventory collection")
	}
}

func (c *Client) fetchBody(ctx context.Context, target *url.URL) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, adapterError("request_failed", "could not build the CyberArk request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, transportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, adapterError("redirect", "CyberArk redirects are not followed")
	}
	if response.StatusCode != http.StatusOK {
		return nil, statusError(response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, adapterError("invalid_content_type", "CyberArk response must be application/json")
	}
	readLimit := c.maxBodyBytes
	body, err := io.ReadAll(io.LimitReader(response.Body, readLimit+1))
	if err != nil {
		return nil, transportError(err)
	}
	if int64(len(body)) > c.maxBodyBytes {
		return nil, adapterError("body_too_large", fmt.Sprintf("CyberArk response exceeds %d bytes", c.maxBodyBytes))
	}
	return body, nil
}

func (c *Client) pageURL(cursor string, limit int) (*url.URL, error) {
	return c.collectionPageURL(safesPath, cursor, limit)
}

func (c *Client) collectionPageURL(path, cursor string, limit int) (*url.URL, error) {
	target := *c.base
	if cursor == "" {
		target.Path = path
		query := url.Values{}
		query.Set("limit", strconv.Itoa(limit))
		target.RawQuery = query.Encode()
		return &target, nil
	}
	if len(cursor) > maxCursorLength || strings.IndexFunc(cursor, unicode.IsControl) >= 0 {
		return nil, adapterError("invalid_cursor", "CyberArk nextLink exceeds its bounds")
	}
	parsed, err := url.Parse(cursor)
	if err != nil {
		return nil, adapterError("invalid_cursor", "CyberArk nextLink is invalid")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, adapterError("foreign_cursor", "CyberArk nextLink must not carry user information or a fragment")
	}
	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, c.base.Scheme) || !strings.EqualFold(parsed.Host, c.base.Host) {
			return nil, adapterError("foreign_cursor", "CyberArk nextLink points to another origin")
		}
	}
	if parsed.Host != "" && !parsed.IsAbs() {
		return nil, adapterError("foreign_cursor", "CyberArk nextLink points to another origin")
	}
	if parsed.Path != path && parsed.Path != path+"/" {
		return nil, adapterError("foreign_cursor", "CyberArk nextLink must stay on "+path)
	}
	target.Path = parsed.Path
	target.RawQuery = parsed.RawQuery
	target.ForceQuery = parsed.ForceQuery
	return &target, nil
}

type pageEnvelope struct {
	Value    json.RawMessage `json:"value"`
	Count    json.RawMessage `json:"count"`
	NextLink json.RawMessage `json:"nextLink"`
}

type decodedEnvelope struct {
	Values   []json.RawMessage
	Expected *int
	NextLink string
}

type safePayload struct {
	SafeNumber             json.RawMessage `json:"safeNumber"`
	SafeName               string          `json:"safeName"`
	SafeURLID              string          `json:"safeUrlId"`
	Location               string          `json:"location"`
	CreatorName            string          `json:"creatorName"`
	OLACEnabled            *bool           `json:"olacEnabled"`
	VersionsRetention      json.RawMessage `json:"numberOfVersionsRetention"`
	DaysRetention          json.RawMessage `json:"numberOfDaysRetention"`
	AutoPurgeEnabled       *bool           `json:"autoPurgeEnabled"`
	CreationTime           json.RawMessage `json:"creationTime"`
	LastModificationTime   json.RawMessage `json:"lastModificationTime"`
	Description            string          `json:"description"`
	ManagingCPM            string          `json:"managingCPM"`
	IsExpiredMemberEnabled *bool           `json:"isExpiredMember"`
}

func decodeEnvelope(body []byte) (decodedEnvelope, error) {
	var envelope pageEnvelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&envelope); err != nil {
		return decodedEnvelope{}, adapterError("invalid_json", "CyberArk response is not valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return decodedEnvelope{}, adapterError("invalid_json", "CyberArk response has trailing data")
	}
	if len(envelope.Value) == 0 || string(envelope.Value) == "null" {
		return decodedEnvelope{}, adapterError("invalid_payload", "CyberArk response does not contain value[]")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(envelope.Value, &values); err != nil {
		return decodedEnvelope{}, adapterError("invalid_payload", "CyberArk response does not contain value[]")
	}
	var expected *int
	if len(envelope.Count) > 0 && string(envelope.Count) != "null" {
		parsed, found, err := parseInteger(envelope.Count)
		if err != nil || !found || parsed < 0 || parsed > int64(^uint(0)>>1) {
			return decodedEnvelope{}, adapterError("invalid_total", "CyberArk response contains an invalid count")
		}
		value := int(parsed)
		expected = &value
	}
	if expected != nil && *expected > 0 && *expected < len(values) {
		return decodedEnvelope{}, adapterError("invalid_total", "CyberArk count is smaller than the returned page")
	}
	if expected != nil && *expected == 0 && len(values) > 0 {
		return decodedEnvelope{}, adapterError("invalid_total", "CyberArk count is zero but the page is not empty")
	}
	nextLink := ""
	if len(envelope.NextLink) > 0 && string(envelope.NextLink) != "null" {
		if err := json.Unmarshal(envelope.NextLink, &nextLink); err != nil {
			return decodedEnvelope{}, adapterError("invalid_cursor", "CyberArk nextLink must be a string")
		}
	}
	return decodedEnvelope{Values: values, Expected: expected, NextLink: nextLink}, nil
}

func decodePage(body []byte, limit int) (inventory.Page, error) {
	envelope, err := decodeEnvelope(body)
	if err != nil {
		return inventory.Page{}, err
	}
	if len(envelope.Values) > limit {
		return inventory.Page{}, adapterError("page_too_large", fmt.Sprintf("CyberArk page has %d Safes; limit is %d", len(envelope.Values), limit))
	}
	objects := make([]inventory.Object, 0, len(envelope.Values))
	for index, raw := range envelope.Values {
		object, err := mapSafe(raw)
		if err != nil {
			return inventory.Page{}, adapterError("invalid_object", fmt.Sprintf("Safe %d does not match the bounded contract", index))
		}
		objects = append(objects, object)
	}
	expected := 0
	if envelope.Expected != nil {
		expected = *envelope.Expected
	}
	return inventory.Page{Objects: objects, NextCursor: envelope.NextLink, Expected: expected}, nil
}

func mapSafe(raw json.RawMessage) (inventory.Object, error) {
	var payload safePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return inventory.Object{}, err
	}
	number, found, err := parseInteger(payload.SafeNumber)
	if err != nil || !found || number < 1 || number > int64(^uint(0)>>1) {
		return inventory.Object{}, errors.New("safeNumber is invalid")
	}
	safeName := strings.TrimSpace(payload.SafeName)
	safeURLID := strings.TrimSpace(payload.SafeURLID)
	if safeName == "" || safeURLID == "" {
		return inventory.Object{}, errors.New("safeName and safeUrlId are required")
	}
	attributes := map[string]string{"safe_name": safeName, "safe_url_id": safeURLID}
	putString(attributes, "location", payload.Location)
	putString(attributes, "creator_name", payload.CreatorName)
	putString(attributes, "description", payload.Description)
	putString(attributes, "managing_cpm", payload.ManagingCPM)
	putBool(attributes, "olac_enabled", payload.OLACEnabled)
	putBool(attributes, "auto_purge_enabled", payload.AutoPurgeEnabled)
	putBool(attributes, "expired_member", payload.IsExpiredMemberEnabled)
	if err := putInteger(attributes, "versions_retention", payload.VersionsRetention); err != nil {
		return inventory.Object{}, err
	}
	if err := putInteger(attributes, "days_retention", payload.DaysRetention); err != nil {
		return inventory.Object{}, err
	}
	if err := putEpoch(attributes, "creation_time", payload.CreationTime); err != nil {
		return inventory.Object{}, err
	}
	if err := putEpoch(attributes, "last_modification_time", payload.LastModificationTime); err != nil {
		return inventory.Object{}, err
	}
	return inventory.Object{ExternalID: strconv.FormatInt(number, 10), Attributes: attributes}, nil
}

type memberPayload struct {
	SafeNumber                json.RawMessage `json:"safeNumber"`
	MemberID                  string          `json:"memberId"`
	MemberName                string          `json:"memberName"`
	MemberType                string          `json:"memberType"`
	MembershipExpirationDate  json.RawMessage `json:"membershipExpirationDate"`
	IsExpiredMembershipEnable *bool           `json:"isExpiredMembershipEnable"`
	IsPredefinedUser          *bool           `json:"isPredefinedUser"`
	IsReadOnly                *bool           `json:"isReadOnly"`
	Permissions               json.RawMessage `json:"permissions"`
}

func mapMember(raw json.RawMessage, safe safeRef) (inventory.Object, error) {
	var payload memberPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return inventory.Object{}, err
	}
	number, found, err := parseInteger(payload.SafeNumber)
	if err != nil || !found || number != safe.Number {
		return inventory.Object{}, errors.New("member safeNumber does not match the parent Safe")
	}
	memberID := strings.TrimSpace(payload.MemberID)
	memberName := strings.TrimSpace(payload.MemberName)
	memberType := strings.TrimSpace(payload.MemberType)
	if memberID == "" || memberName == "" || memberType == "" {
		return inventory.Object{}, errors.New("memberId, memberName and memberType are required")
	}
	permissions, err := encodePermissions(payload.Permissions)
	if err != nil {
		return inventory.Object{}, err
	}
	if len(payload.MembershipExpirationDate) == 0 {
		return inventory.Object{}, errors.New("membershipExpirationDate is required")
	}
	attributes := map[string]string{
		"safe_number": strconv.FormatInt(safe.Number, 10),
		"safe_name":   safe.Name,
		"member_id":   memberID,
		"member_name": memberName,
		"member_type": memberType,
		"permissions": permissions,
	}
	if err := putEpoch(attributes, "membership_expiration_date", payload.MembershipExpirationDate); err != nil {
		return inventory.Object{}, err
	}
	putBool(attributes, "expired_membership_enabled", payload.IsExpiredMembershipEnable)
	putBool(attributes, "predefined_user", payload.IsPredefinedUser)
	putBool(attributes, "read_only", payload.IsReadOnly)
	return inventory.Object{
		ExternalID: strconv.FormatInt(safe.Number, 10) + ":" + memberID,
		Attributes: attributes,
	}, nil
}

func encodePermissions(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("permissions object is required")
	}
	var permissions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &permissions); err != nil || permissions == nil {
		return "", errors.New("permissions must be an object")
	}
	encoded, err := json.Marshal(permissions)
	if err != nil {
		return "", errors.New("permissions cannot be encoded")
	}
	return string(encoded), nil
}

type memberCursor struct {
	Safe int    `json:"safe"`
	Next string `json:"next"`
}

func encodeMemberCursor(cursor memberCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(encoded)
	if len(value) > maxCursorLength {
		return "", errors.New("member cursor exceeds its bound")
	}
	return value, nil
}

func decodeMemberCursor(raw string) (memberCursor, error) {
	if raw == "" {
		return memberCursor{}, nil
	}
	if len(raw) > maxCursorLength || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return memberCursor{}, adapterError("invalid_cursor", "member cursor exceeds its bounds")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return memberCursor{}, adapterError("invalid_cursor", "member cursor is invalid")
	}
	var cursor memberCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Safe < 0 {
		return memberCursor{}, adapterError("invalid_cursor", "member cursor is invalid")
	}
	if len(cursor.Next) > maxMemberNextLength || strings.IndexFunc(cursor.Next, unicode.IsControl) >= 0 {
		return memberCursor{}, adapterError("invalid_cursor", "member nextLink exceeds its bounds")
	}
	return cursor, nil
}

func (c *Client) fetchMembersPage(ctx context.Context, cursor string, limit int) (inventory.Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadSafesLocked(ctx); err != nil {
		return inventory.Page{}, err
	}
	if cursor == "" {
		// A new traversal starts from the first Safe and resets the run-local
		// totals. The Safe list itself is cached for this client/run.
		c.memberCounts = map[int]int{}
		c.memberSeen = map[int]int{}
		c.memberKnown = true
	}
	state, err := decodeMemberCursor(cursor)
	if err != nil {
		return inventory.Page{}, err
	}
	if state.Safe >= len(c.safes) {
		if cursor == "" {
			return inventory.Page{Objects: []inventory.Object{}, NextCursor: ""}, nil
		}
		return inventory.Page{}, adapterError("invalid_cursor", "member cursor points beyond the Safe list")
	}
	objects := []inventory.Object{}
	for {
		if state.Safe >= len(c.safes) {
			return c.memberPage(objects), nil
		}
		safe := c.safes[state.Safe]
		values, expected, next, err := c.fetchMemberPage(ctx, safe, state.Next, limit)
		if err != nil {
			return inventory.Page{}, err
		}
		if len(values) > limit {
			return inventory.Page{}, adapterError("page_too_large", fmt.Sprintf("CyberArk member page exceeds the requested limit of %d", limit))
		}
		if expected != nil {
			if *expected < len(values) {
				return inventory.Page{}, adapterError("invalid_total", "CyberArk member count is smaller than the returned page")
			}
			if previous, found := c.memberCounts[state.Safe]; found && previous != *expected {
				return inventory.Page{}, adapterError("invalid_total", "CyberArk changed the member total for a Safe during the run")
			}
			c.memberCounts[state.Safe] = *expected
		} else {
			c.memberKnown = false
		}
		c.memberSeen[state.Safe] += len(values)
		objects = append(objects, values...)
		if next != "" {
			return c.memberPageWithCursor(objects, memberCursor{Safe: state.Safe, Next: next})
		}
		if expected, known := c.memberCounts[state.Safe]; known && c.memberSeen[state.Safe] != expected {
			return inventory.Page{}, adapterError("incomplete_collection", "CyberArk returned fewer members than its reported Safe total")
		}
		state.Safe++
		state.Next = ""
		if len(objects) > 0 {
			if state.Safe >= len(c.safes) {
				return c.memberPage(objects), nil
			}
			return c.memberPageWithCursor(objects, memberCursor{Safe: state.Safe, Next: ""})
		}
	}
}

func (c *Client) memberPage(objects []inventory.Object) inventory.Page {
	expected := 0
	if c.memberKnown {
		total := 0
		for index := range c.safes {
			count, found := c.memberCounts[index]
			if !found {
				c.memberKnown = false
				break
			}
			total += count
		}
		if c.memberKnown {
			expected = total
		}
	}
	return inventory.Page{Objects: objects, NextCursor: "", Expected: expected}
}

func (c *Client) memberPageWithCursor(objects []inventory.Object, cursor memberCursor) (inventory.Page, error) {
	encoded, err := encodeMemberCursor(cursor)
	if err != nil {
		return inventory.Page{}, adapterError("invalid_cursor", "member cursor exceeds its bound")
	}
	return inventory.Page{Objects: objects, NextCursor: encoded}, nil
}

func (c *Client) loadSafesLocked(ctx context.Context) error {
	if c.safesLoaded {
		return nil
	}
	target, err := c.pageURL("", safesEnumerationSize)
	if err != nil {
		return err
	}
	cursor := ""
	seen := map[string]struct{}{}
	expected := -1
	safes := []safeRef{}
	numbers := map[int64]bool{}
	names := map[string]bool{}
	for page := 0; ; page++ {
		if page >= maxSafePages {
			return adapterError("too_many_safes", "CyberArk Safe enumeration exceeds its page bound")
		}
		body, err := c.fetchBody(ctx, target)
		if err != nil {
			return err
		}
		envelope, err := decodeEnvelope(body)
		if err != nil {
			return err
		}
		if envelope.Expected != nil {
			if expected >= 0 && expected != *envelope.Expected {
				return adapterError("total_changed", "CyberArk changed its Safe total during member enumeration")
			}
			expected = *envelope.Expected
		}
		if len(envelope.Values) > safesEnumerationSize {
			return adapterError("page_too_large", "CyberArk Safe enumeration page exceeds its limit")
		}
		for index, raw := range envelope.Values {
			ref, err := decodeSafeRef(raw)
			if err != nil {
				return adapterError("invalid_object", fmt.Sprintf("Safe %d cannot be used for member enumeration", index))
			}
			if numbers[ref.Number] || names[ref.Name] {
				return adapterError("duplicate_safe", "CyberArk returned a duplicate Safe during member enumeration")
			}
			numbers[ref.Number] = true
			names[ref.Name] = true
			safes = append(safes, ref)
			if len(safes) > maxSafeEnumeration {
				return adapterError("too_many_safes", "CyberArk Safe enumeration exceeds its record bound")
			}
		}
		if envelope.NextLink == "" {
			break
		}
		if _, repeated := seen[envelope.NextLink]; repeated {
			return adapterError("cursor_repeated", "CyberArk repeated a Safe enumeration cursor")
		}
		seen[envelope.NextLink] = struct{}{}
		cursor = envelope.NextLink
		target, err = c.pageURL(cursor, safesEnumerationSize)
		if err != nil {
			return err
		}
	}
	if expected >= 0 && expected != len(safes) {
		return adapterError("incomplete_collection", fmt.Sprintf("CyberArk reported %d Safes and returned %d", expected, len(safes)))
	}
	c.safes = safes
	c.safesLoaded = true
	return nil
}

func decodeSafeRef(raw json.RawMessage) (safeRef, error) {
	var payload struct {
		SafeNumber json.RawMessage `json:"safeNumber"`
		SafeName   string          `json:"safeName"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return safeRef{}, err
	}
	number, found, err := parseInteger(payload.SafeNumber)
	if err != nil || !found || number < 1 || number > int64(^uint(0)>>1) {
		return safeRef{}, errors.New("safeNumber is invalid")
	}
	name := strings.TrimSpace(payload.SafeName)
	if name == "" {
		return safeRef{}, errors.New("safeName is required")
	}
	return safeRef{Number: number, Name: name}, nil
}

func (c *Client) fetchMemberPage(ctx context.Context, safe safeRef, cursor string, limit int) ([]inventory.Object, *int, string, error) {
	target, err := c.memberPageURL(safe, cursor, limit)
	if err != nil {
		return nil, nil, "", err
	}
	body, err := c.fetchBody(ctx, target)
	if err != nil {
		return nil, nil, "", err
	}
	envelope, err := decodeEnvelope(body)
	if err != nil {
		return nil, nil, "", err
	}
	objects := make([]inventory.Object, 0, len(envelope.Values))
	for index, raw := range envelope.Values {
		object, err := mapMember(raw, safe)
		if err != nil {
			return nil, nil, "", adapterError("invalid_object", fmt.Sprintf("Safe %s member %d does not match the bounded contract", safe.Name, index))
		}
		objects = append(objects, object)
	}
	return objects, envelope.Expected, envelope.NextLink, nil
}

func (c *Client) memberPageURL(safe safeRef, cursor string, limit int) (*url.URL, error) {
	expectedPath := safesPath + "/" + safe.Name + "/Members"
	target := *c.base
	if cursor == "" {
		target.Path = expectedPath
		target.RawPath = safesPath + "/" + url.PathEscape(safe.Name) + "/Members"
		query := url.Values{}
		query.Set("limit", strconv.Itoa(limit))
		target.RawQuery = query.Encode()
		return &target, nil
	}
	if len(cursor) > maxMemberNextLength || strings.IndexFunc(cursor, unicode.IsControl) >= 0 {
		return nil, adapterError("invalid_cursor", "CyberArk member nextLink exceeds its bounds")
	}
	parsed, err := url.Parse(cursor)
	if err != nil {
		return nil, adapterError("invalid_cursor", "CyberArk member nextLink is invalid")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, adapterError("foreign_cursor", "CyberArk member nextLink must not carry user information or a fragment")
	}
	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, c.base.Scheme) || !strings.EqualFold(parsed.Host, c.base.Host) {
			return nil, adapterError("foreign_cursor", "CyberArk member nextLink points to another origin")
		}
	}
	if parsed.Host != "" && !parsed.IsAbs() {
		return nil, adapterError("foreign_cursor", "CyberArk member nextLink points to another origin")
	}
	if parsed.Path != expectedPath && parsed.Path != expectedPath+"/" {
		return nil, adapterError("foreign_cursor", "CyberArk member nextLink must stay on the current Safe")
	}
	target.Path = parsed.Path
	target.RawQuery = parsed.RawQuery
	target.ForceQuery = parsed.ForceQuery
	return &target, nil
}

func (c *Client) fetchAccountsPage(ctx context.Context, cursor string, limit int) (inventory.Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadSafesLocked(ctx); err != nil {
		return inventory.Page{}, err
	}
	if cursor == "" {
		c.accountsSkipped = false
	}
	target, err := c.collectionPageURL(accountsPath, cursor, limit)
	if err != nil {
		return inventory.Page{}, err
	}
	body, err := c.fetchBody(ctx, target)
	if err != nil {
		return inventory.Page{}, err
	}
	envelope, err := decodeEnvelope(body)
	if err != nil {
		return inventory.Page{}, err
	}
	if len(envelope.Values) > limit {
		return inventory.Page{}, adapterError("page_too_large", fmt.Sprintf("CyberArk account page exceeds the requested limit of %d", limit))
	}
	byName := make(map[string]safeRef, len(c.safes))
	for _, safe := range c.safes {
		byName[safe.Name] = safe
	}
	objects := make([]inventory.Object, 0, len(envelope.Values))
	for index, raw := range envelope.Values {
		object, kept, err := mapAccount(raw, byName)
		if err != nil {
			return inventory.Page{}, adapterError("invalid_object", fmt.Sprintf("Account %d does not match the bounded contract", index))
		}
		if !kept {
			c.accountsSkipped = true
			continue
		}
		objects = append(objects, object)
	}
	page := inventory.Page{Objects: objects, NextCursor: envelope.NextLink}
	if len(envelope.NextLink) == 0 && !c.accountsSkipped && envelope.Expected != nil {
		page.Expected = *envelope.Expected
	}
	if c.accountsSkipped {
		// Skipped rows are not a complete snapshot: publish what was read, but
		// never retire accounts nor replace the baseline.
		page.Warnings = []string{"skipped_accounts"}
	}
	return page, nil
}

func mapAccount(raw json.RawMessage, safes map[string]safeRef) (inventory.Object, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return inventory.Object{}, false, errors.New("account must be an object")
	}
	safeName := accountText(fields, "safeName", "safe_name")
	safe, found := safes[safeName]
	if !found {
		return inventory.Object{}, false, nil
	}
	accountID := accountIdentifier(fields, "id", "accountId", "accountID", "account_id")
	if accountID == "" {
		return inventory.Object{}, false, nil
	}
	accountName := accountText(fields, "name", "accountName", "account_name", "userName", "username")
	if accountName == "" {
		accountName = accountID
	}
	attributes := map[string]string{
		"safe_number":  strconv.FormatInt(safe.Number, 10),
		"safe_name":    safe.Name,
		"account_id":   accountID,
		"account_name": accountName,
	}
	putString(attributes, "user_name", accountText(fields, "userName", "username", "user_name"))
	putString(attributes, "address", accountText(fields, "address"))
	putString(attributes, "platform_id", accountIdentifier(fields, "platformId", "platformID", "platform_id"))
	putString(attributes, "account_type", accountText(fields, "accountType", "account_type"))
	putString(attributes, "category", accountText(fields, "category"))
	putSecretManagement(fields, attributes)
	return inventory.Object{
		ExternalID: strconv.FormatInt(safe.Number, 10) + ":" + accountID,
		Attributes: attributes,
	}, true, nil
}

func putSecretManagement(fields map[string]json.RawMessage, attributes map[string]string) {
	raw, found := fields["secretManagement"]
	if !found || len(raw) == 0 || string(raw) == "null" {
		return
	}
	var secret map[string]json.RawMessage
	if err := json.Unmarshal(raw, &secret); err != nil || secret == nil {
		return
	}
	if rawEnabled, found := secret["automaticManagementEnabled"]; found {
		var enabled bool
		if json.Unmarshal(rawEnabled, &enabled) == nil {
			attributes["automatic_management_enabled"] = strconv.FormatBool(enabled)
		}
	}
	if rawStatus, found := secret["status"]; found {
		var status string
		if json.Unmarshal(rawStatus, &status) == nil {
			switch strings.ToLower(strings.TrimSpace(status)) {
			case "success":
				attributes["management_status"] = "success"
			case "failure":
				attributes["management_status"] = "failure"
			case "inprocess", "in_process":
				attributes["management_status"] = "in_process"
			}
		}
	}
	if rawModified, found := secret["lastModifiedTime"]; found {
		value, ok, err := parseInteger(rawModified)
		if err == nil && ok && value > 0 && value < maxSecretModified {
			attributes["secret_modified_at"] = time.Unix(value, 0).UTC().Format(time.RFC3339)
		}
	}
}

func accountText(fields map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, found := fields[key]
		if !found {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func accountIdentifier(fields map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, found := fields[key]
		if !found {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
		number, ok, err := parseInteger(raw)
		if err == nil && ok {
			return strconv.FormatInt(number, 10)
		}
	}
	return ""
}

var platformCatalog = catalogSpec{
	path: platformsPath, key: "Platforms", max: maxCatalogObjects, mapObject: mapPlatform,
}

var psmServerCatalog = catalogSpec{
	path: psmServersPath, query: "limit=1000", key: "PSMServers", max: maxCatalogObjects, mapObject: mapPSMServer,
}

var psmConnectorCatalog = catalogSpec{
	path: psmConnectorsPath, query: "limit=1000", key: "PSMConnectors", max: maxCatalogObjects, mapObject: mapPSMConnector,
}

func (c *Client) fetchCatalogPage(ctx context.Context, cursor string, limit int, spec catalogSpec, state *catalogState) (inventory.Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !state.loaded {
		if err := c.loadCatalogLocked(ctx, spec, state); err != nil {
			return inventory.Page{}, err
		}
	}
	return serveCatalogPageLocked(cursor, limit, state)
}

func serveCatalogPageLocked(cursor string, limit int, state *catalogState) (inventory.Page, error) {
	offset := 0
	if cursor != "" {
		parsed, err := decodeCatalogCursor(cursor)
		if err != nil {
			return inventory.Page{}, err
		}
		offset = parsed
	}
	if offset < 0 || offset > len(state.objects) || (cursor != "" && offset == 0) {
		return inventory.Page{}, adapterError("invalid_cursor", "catalog cursor is outside the validated snapshot")
	}
	end := offset + limit
	if end > len(state.objects) {
		end = len(state.objects)
	}
	next := ""
	if end < len(state.objects) {
		next = encodeCatalogCursor(end)
	}
	return inventory.Page{Objects: state.objects[offset:end], NextCursor: next, Expected: state.total}, nil
}

func (c *Client) fetchPlatformPSMLinksPage(ctx context.Context, cursor string, limit int) (inventory.Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.platformPSMLinks.loaded {
		if err := c.loadPlatformPSMLinksLocked(ctx); err != nil {
			return inventory.Page{}, err
		}
	}
	return serveCatalogPageLocked(cursor, limit, &c.platformPSMLinks)
}

func (c *Client) loadPlatformPSMLinksLocked(ctx context.Context) error {
	if !c.platforms.loaded {
		if err := c.loadCatalogLocked(ctx, platformCatalog, &c.platforms); err != nil {
			return err
		}
	}
	if !c.psmServers.loaded {
		if err := c.loadCatalogLocked(ctx, psmServerCatalog, &c.psmServers); err != nil {
			return err
		}
	}
	names := make(map[string]string, len(c.psmServers.objects))
	catalog := make([]string, 0, len(c.psmServers.objects))
	seen := map[string]bool{}
	for _, server := range c.psmServers.objects {
		names[server.ExternalID] = server.Attributes["psm_name"]
		if seen[server.ExternalID] {
			continue
		}
		seen[server.ExternalID] = true
		catalog = append(catalog, server.ExternalID)
	}
	links := make([]inventory.Object, 0, len(c.platforms.objects))
	for _, platform := range c.platforms.objects {
		reference := platform.Attributes["psm_server_id"]
		if reference == "" {
			continue
		}
		target, resolution := resolvePSMLink(reference, catalog)
		name := ""
		switch resolution {
		case "exact", "casefold":
			name = names[target]
		case "missing":
			name = target
		}
		links = append(links, inventory.Object{
			ExternalID: platform.ExternalID + ":" + reference,
			Attributes: map[string]string{
				"platform_id":      platform.ExternalID,
				"platform_name":    platform.Attributes["platform_name"],
				"reference_psm_id": reference,
				"psm_server_id":    target,
				"psm_server_name":  name,
				"resolution":       resolution,
			},
		})
	}
	if len(links) > maxCatalogObjects {
		return adapterError("too_many_objects", "platform PSM links exceed their record bound")
	}
	c.platformPSMLinks.objects = links
	c.platformPSMLinks.loaded = true
	c.platformPSMLinks.total = len(links)
	return nil
}

// resolvePSMLink mirrors DS Hub: an exact catalog ID wins; one case-insensitive
// candidate is a casefold match; two distinct candidates are ambiguous and no
// target is chosen; no candidate is a missing catalog reference.
func resolvePSMLink(reference string, catalog []string) (string, string) {
	for _, candidate := range catalog {
		if candidate == reference {
			return reference, "exact"
		}
	}
	candidates := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, candidate := range catalog {
		if seen[candidate] || !strings.EqualFold(candidate, reference) {
			continue
		}
		seen[candidate] = true
		candidates = append(candidates, candidate)
	}
	switch len(candidates) {
	case 0:
		return reference, "missing"
	case 1:
		return candidates[0], "casefold"
	default:
		return "", "ambiguous"
	}
}

func (c *Client) loadCatalogLocked(ctx context.Context, spec catalogSpec, state *catalogState) error {
	target := *c.base
	target.Path = spec.path
	target.RawQuery = spec.query
	body, err := c.fetchBody(ctx, &target)
	if err != nil {
		return err
	}
	var envelope map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&envelope); err != nil {
		return adapterError("invalid_json", "CyberArk catalog response is not valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return adapterError("invalid_json", "CyberArk catalog response has trailing data")
	}
	rawValues, found := envelope[spec.key]
	if !found || len(rawValues) == 0 || string(rawValues) == "null" {
		return adapterError("invalid_payload", "CyberArk catalog response does not contain "+spec.key+"[]")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(rawValues, &values); err != nil {
		return adapterError("invalid_payload", "CyberArk catalog response does not contain "+spec.key+"[]")
	}
	total := 0
	known := false
	if rawTotal, found := envelope["Total"]; found && len(rawTotal) > 0 && string(rawTotal) != "null" {
		parsed, found, err := parseInteger(rawTotal)
		if err != nil || !found || parsed < 0 || parsed > int64(^uint(0)>>1) {
			return adapterError("invalid_total", "CyberArk catalog response contains an invalid Total")
		}
		total, known = int(parsed), true
		if total != len(values) {
			return adapterError("incomplete_collection", fmt.Sprintf("CyberArk reported %d catalog objects and returned %d", total, len(values)))
		}
	}
	if len(values) > spec.max {
		return adapterError("too_many_objects", "CyberArk catalog response exceeds its record bound")
	}
	objects := make([]inventory.Object, 0, len(values))
	for index, raw := range values {
		object, err := spec.mapObject(raw)
		if err != nil {
			return adapterError("invalid_object", fmt.Sprintf("catalog object %d does not match the bounded contract", index))
		}
		objects = append(objects, object)
	}
	state.objects = objects
	state.loaded = true
	state.total = 0
	if known {
		state.total = total
	}
	return nil
}

func mapPlatform(raw json.RawMessage) (inventory.Object, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return inventory.Object{}, errors.New("platform must be an object")
	}
	general, err := rawObject(fields, "general")
	if err != nil {
		return inventory.Object{}, err
	}
	id := accountIdentifier(general, "id")
	if id == "" {
		return inventory.Object{}, errors.New("general.id is required")
	}
	name := accountText(general, "name")
	if name == "" {
		name = id
	}
	attributes := map[string]string{"platform_name": name}
	if rawActive, found := general["active"]; found && string(rawActive) != "null" {
		var active bool
		if json.Unmarshal(rawActive, &active) != nil {
			return inventory.Object{}, errors.New("general.active must be a boolean")
		}
		attributes["active"] = strconv.FormatBool(active)
	}
	putString(attributes, "platform_base_id", accountText(general, "platformBaseID"))
	putString(attributes, "platform_type", accountText(general, "platformType"))
	putString(attributes, "system_type", accountText(general, "systemType"))
	if session, err := rawObject(fields, "sessionManagement"); err == nil {
		putString(attributes, "psm_server_id", accountText(session, "PSMServerID"))
	}
	return inventory.Object{ExternalID: id, Attributes: attributes}, nil
}

func mapPSMServer(raw json.RawMessage) (inventory.Object, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return inventory.Object{}, errors.New("PSM server must be an object")
	}
	id := accountIdentifier(fields, "ID", "id")
	if id == "" {
		return inventory.Object{}, errors.New("PSM server ID is required")
	}
	name := accountText(fields, "Name", "name")
	if name == "" {
		name = id
	}
	attributes := map[string]string{"psm_name": name}
	putString(attributes, "address", accountText(fields, "Address", "address"))
	return inventory.Object{ExternalID: id, Attributes: attributes}, nil
}

func mapPSMConnector(raw json.RawMessage) (inventory.Object, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return inventory.Object{}, errors.New("PSM connector must be an object")
	}
	id := accountIdentifier(fields, "ID", "id")
	if id == "" {
		return inventory.Object{}, errors.New("PSM connector ID is required")
	}
	name := accountText(fields, "DisplayName", "Name", "displayName", "name")
	if name == "" {
		name = id
	}
	return inventory.Object{ExternalID: id, Attributes: map[string]string{"connector_name": name}}, nil
}

func rawObject(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	raw, found := fields[key]
	if !found || len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return object, nil
}

func encodeCatalogCursor(offset int) string {
	return "items:" + strconv.Itoa(offset)
}

func decodeCatalogCursor(cursor string) (int, error) {
	if len(cursor) > maxCursorLength || strings.IndexFunc(cursor, unicode.IsControl) >= 0 {
		return 0, adapterError("invalid_cursor", "catalog cursor exceeds its bounds")
	}
	rest, found := strings.CutPrefix(cursor, "items:")
	if !found {
		return 0, adapterError("invalid_cursor", "catalog cursor is invalid")
	}
	offset, err := strconv.Atoi(rest)
	if err != nil || offset < 0 {
		return 0, adapterError("invalid_cursor", "catalog cursor is invalid")
	}
	return offset, nil
}

func putString(attributes map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		attributes[key] = value
	}
}

func putBool(attributes map[string]string, key string, value *bool) {
	if value != nil {
		attributes[key] = strconv.FormatBool(*value)
	}
}

func putInteger(attributes map[string]string, key string, raw json.RawMessage) error {
	value, found, err := parseInteger(raw)
	if err != nil {
		return err
	}
	if found {
		if value < 0 {
			return errors.New("retention must not be negative")
		}
		attributes[key] = strconv.FormatInt(value, 10)
	}
	return nil
}

func putEpoch(attributes map[string]string, key string, raw json.RawMessage) error {
	value, found, err := parseInteger(raw)
	if err != nil || (found && (value < 0 || value > maxEpochSeconds)) {
		return errors.New("epoch is invalid")
	}
	if found {
		attributes[key] = time.Unix(value, 0).UTC().Format(time.RFC3339)
	}
	return nil
}

// parseInteger accepts the provider's JSON integer or its decimal string form,
// matching DS Hub's validation. Missing/null returns found=false.
func parseInteger(raw json.RawMessage) (int64, bool, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, false, nil
	}
	if strings.HasPrefix(text, `"`) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, false, err
		}
		text = strings.TrimSpace(value)
		if text == "" {
			return 0, false, nil
		}
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
}

func statusError(status int) *inventory.Error {
	switch status {
	case http.StatusUnauthorized:
		return adapterError("authentication_failed", "CyberArk rejected the request identity")
	case http.StatusForbidden:
		return adapterError("permission_denied", "CyberArk denied read access to Safes")
	default:
		return adapterError(fmt.Sprintf("http_status_%d", status), fmt.Sprintf("CyberArk returned HTTP %d", status))
	}
}

func transportError(err error) *inventory.Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return adapterError("timeout", "CyberArk Safes request timed out")
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return adapterError("timeout", "CyberArk Safes request timed out")
	}
	return adapterError("request_failed", "CyberArk Safes request failed")
}

func adapterError(code, detail string) *inventory.Error {
	return &inventory.Error{Code: code, Detail: detail}
}
