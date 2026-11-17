// Package wazuh reads the bounded agent and group inventory from the Wazuh
// manager REST API. It never reads alerts (those live in the indexer), never
// sends credentials after the probe prepared the client, and only emits the
// normalized allowlisted fields.
package wazuh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"custodia/server/internal/inventory"
)

const (
	CollectionAgents = "agents"
	CollectionGroups = "groups"

	defaultTimeout      = 15 * time.Second
	defaultMaxBodyBytes = 1 << 20
	userAgent           = "custodia-wazuh/1"
	maxPageLimit        = 500
	agentsPath          = "/agents"
	groupsPath          = "/groups"
)

// Options configures the read-only Wazuh client. The default profile accepts
// only numeric loopback HTTP for fixtures; a tenant connection sets
// AllowRemote with an HTTPS client prepared by the probe.
type Options struct {
	HTTP         *http.Client
	MaxBodyBytes int64
	AllowRemote  bool
}

type Client struct {
	base         *url.URL
	client       *http.Client
	maxBodyBytes int64
}

func New(baseURL string, options Options) (*Client, error) {
	base, err := parseBaseURL(baseURL, options.AllowRemote)
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
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = defaultMaxBodyBytes
	}
	return &Client{base: base, client: &client, maxBodyBytes: options.MaxBodyBytes}, nil
}

func parseBaseURL(raw string, allowRemote bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, errors.New("wazuh base URL is invalid")
	}
	if allowRemote {
		if parsed.Scheme != "https" {
			return nil, errors.New("wazuh tenant URL must use https")
		}
		if strings.Contains(parsed.Path, "..") || strings.Contains(parsed.Path, "//") {
			return nil, errors.New("wazuh tenant URL path is invalid")
		}
	} else {
		if parsed.Scheme != "http" {
			return nil, errors.New("wazuh fixture URL must use http")
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return nil, errors.New("wazuh fixture URL must not include a path")
		}
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("wazuh fixture host must be a numeric loopback address")
		}
	}
	base := *parsed
	base.Path = strings.TrimSuffix(base.Path, "/")
	return &base, nil
}

func (c *Client) FetchPage(ctx context.Context, collection, cursor string, limit int) (inventory.Page, error) {
	if collection != CollectionAgents && collection != CollectionGroups {
		return inventory.Page{}, adapterError("unsupported_collection", "Wazuh collection is not implemented")
	}
	if limit < 1 {
		limit = maxPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	offset := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			return inventory.Page{}, adapterError("invalid_cursor", "Wazuh cursor is invalid")
		}
		offset = parsed
	}
	path := agentsPath
	sortKey := "id"
	if collection == CollectionGroups {
		path = groupsPath
		sortKey = "name"
	}
	target := *c.base
	target.Path = c.base.Path + path
	query := target.Query()
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	query.Set("sort", sortKey)
	target.RawQuery = query.Encode()
	body, err := c.get(ctx, target.String())
	if err != nil {
		return inventory.Page{}, err
	}
	envelope, err := decodeEnvelope(body)
	if err != nil {
		return inventory.Page{}, err
	}
	if len(envelope.Data.Affected) > limit {
		return inventory.Page{}, adapterError("page_too_large", "Wazuh returned more objects than the requested limit")
	}
	if envelope.Data.Total < offset+len(envelope.Data.Affected) {
		return inventory.Page{}, adapterError("invalid_total", "Wazuh total is smaller than the returned page")
	}
	objects := make([]inventory.Object, 0, len(envelope.Data.Affected))
	for index, raw := range envelope.Data.Affected {
		var object inventory.Object
		if collection == CollectionAgents {
			object, err = mapAgent(raw)
		} else {
			object, err = mapGroup(raw)
		}
		if err != nil {
			return inventory.Page{}, adapterError("invalid_object", fmt.Sprintf("Wazuh %s object %d does not match the bounded contract", collection, index))
		}
		objects = append(objects, object)
	}
	next := ""
	if offset+len(objects) < envelope.Data.Total {
		next = strconv.Itoa(offset + len(objects))
	}
	return inventory.Page{Objects: objects, NextCursor: next, Expected: envelope.Data.Total}, nil
}

type envelope struct {
	Data struct {
		Affected []json.RawMessage `json:"affected_items"`
		Total    int               `json:"total_affected_items"`
		Failed   int               `json:"total_failed_items"`
	} `json:"data"`
	Error int `json:"error"`
}

func decodeEnvelope(body []byte) (envelope, error) {
	var result envelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&result); err != nil {
		return envelope{}, adapterError("invalid_json", "Wazuh response is not valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return envelope{}, adapterError("invalid_json", "Wazuh response has trailing data")
	}
	if result.Error != 0 || result.Data.Failed != 0 {
		return envelope{}, adapterError("provider_error", "Wazuh reported failed items")
	}
	if result.Data.Total < 0 {
		return envelope{}, adapterError("invalid_total", "Wazuh total is invalid")
	}
	return result, nil
}

func (c *Client) get(ctx context.Context, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, adapterError("request", "Wazuh request could not be built")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, adapterError("transport", "Wazuh request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, adapterError("http_status", fmt.Sprintf("Wazuh returned %d", response.StatusCode))
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		if parsed := strings.Split(contentType, ";")[0]; parsed != "application/json" && parsed != "text/plain" {
			return nil, adapterError("content_type", "Wazuh response is not JSON")
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxBodyBytes+1))
	if err != nil {
		return nil, adapterError("read", "Wazuh response could not be read")
	}
	if int64(len(body)) > c.maxBodyBytes {
		return nil, adapterError("body_too_large", "Wazuh response exceeds the body limit")
	}
	return body, nil
}

type agentPayload struct {
	ID            json.RawMessage `json:"id"`
	Name          string          `json:"name"`
	Status        string          `json:"status"`
	IP            string          `json:"ip"`
	RegisterIP    string          `json:"registerIP"`
	Version       string          `json:"version"`
	Group         []string        `json:"group"`
	LastKeepAlive string          `json:"lastKeepAlive"`
	DateAdd       string          `json:"dateAdd"`
	Manager       string          `json:"manager"`
	NodeName      string          `json:"node_name"`
	OS            struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
		Version  string `json:"version"`
		Arch     string `json:"arch"`
	} `json:"os"`
}

func mapAgent(raw json.RawMessage) (inventory.Object, error) {
	var payload agentPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return inventory.Object{}, err
	}
	id, err := scalarString(payload.ID)
	if err != nil || id == "" || len(id) > 64 {
		return inventory.Object{}, errors.New("agent id is invalid")
	}
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	switch status {
	case "active", "pending", "never_connected", "disconnected":
	default:
		return inventory.Object{}, errors.New("agent status is invalid")
	}
	name := clean(payload.Name, 256)
	if name == "" {
		return inventory.Object{}, errors.New("agent name is invalid")
	}
	attributes := map[string]string{"name": name, "status": status}
	put(attributes, "ip", payload.IP, 128)
	put(attributes, "register_ip", payload.RegisterIP, 128)
	put(attributes, "os_name", payload.OS.Name, 128)
	put(attributes, "os_platform", payload.OS.Platform, 128)
	put(attributes, "os_version", payload.OS.Version, 128)
	put(attributes, "os_arch", payload.OS.Arch, 64)
	put(attributes, "agent_version", payload.Version, 64)
	put(attributes, "manager", payload.Manager, 256)
	put(attributes, "node_name", payload.NodeName, 256)
	if stamp := timestamp(payload.LastKeepAlive); stamp != "" {
		attributes["last_keep_alive"] = stamp
	}
	if stamp := timestamp(payload.DateAdd); stamp != "" {
		attributes["date_add"] = stamp
	}
	groups := make([]string, 0, len(payload.Group))
	for _, group := range payload.Group {
		if value := clean(group, 128); value != "" {
			groups = append(groups, value)
		}
	}
	attributes["groups"] = strings.Join(groups, ",")
	attributes["group_count"] = strconv.Itoa(len(groups))
	return inventory.Object{ExternalID: id, Attributes: attributes}, nil
}

type groupPayload struct {
	Name  string          `json:"name"`
	Count json.RawMessage `json:"count"`
}

func mapGroup(raw json.RawMessage) (inventory.Object, error) {
	var payload groupPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return inventory.Object{}, err
	}
	name := clean(payload.Name, 128)
	if name == "" {
		return inventory.Object{}, errors.New("group name is invalid")
	}
	count, err := scalarInteger(payload.Count)
	if err != nil || count < 0 || count > math.MaxInt32 {
		return inventory.Object{}, errors.New("group count is invalid")
	}
	return inventory.Object{ExternalID: name, Attributes: map[string]string{"name": name, "agent_count": strconv.Itoa(count)}}, nil
}

func scalarString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("missing")
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return "", errors.New("not a scalar")
	}
	return number.String(), nil
}

func scalarInteger(raw json.RawMessage) (int, error) {
	value, err := scalarString(raw)
	if err != nil {
		return 0, err
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return number, nil
}

func clean(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return ""
	}
	return value
}

func put(attributes map[string]string, key, value string, max int) {
	if cleaned := clean(value, max); cleaned != "" {
		attributes[key] = cleaned
	}
}

func timestamp(value string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}

func adapterError(code, detail string) error {
	return &inventory.Error{Code: code, Detail: detail}
}
