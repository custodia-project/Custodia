// Package inventory defines the small normalized record contract used by the
// first read-only synchronization. It has no SQL or HTTP dependencies.
package inventory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	StatusCompleted = "completed"
	StatusPartial   = "partial"
	StatusFailed    = "failed"
)

// Object is one observation emitted by an adapter. Attributes are restricted by
// the collection allowlist so a provider payload never reaches storage whole.
type Object struct {
	ExternalID string            `json:"external_id"`
	Attributes map[string]string `json:"attributes"`
}

// Page is one bounded page of a collection. An empty NextCursor means the
// adapter asserts the collection was read to its end.
type Page struct {
	Objects    []Object `json:"objects"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// Error is a safe adapter/validation error. Detail never contains a provider
// body or credentials; Code is stable for run history and tests.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return e.Detail }
func (e *Error) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

// Collection is the allowlist for one normalized family. Required names must
// be present and non-empty; any other attribute is rejected.
type Collection struct {
	Name     string
	Required []string
	Allowed  map[string]bool
}

var collections = map[string]Collection{
	"users": {
		Name:     "users",
		Required: []string{"email"},
		Allowed:  map[string]bool{"name": true, "email": true},
	},
	"devices": {
		Name:     "devices",
		Required: []string{"user_id"},
		Allowed:  map[string]bool{"user_id": true, "last_signal_at": true},
	},
}

// CollectionFor returns only explicitly supported normalized collections.
func CollectionFor(name string) (Collection, bool) {
	collection, found := collections[name]
	return collection, found
}

// CollectionNames returns the stable supported list for configuration checks.
func CollectionNames() []string { return []string{"devices", "users"} }

// ValidateObject enforces the collection allowlist, opaque external IDs and
// the timestamp shape used by the existing observation model.
func ValidateObject(collectionName string, object Object) (Object, error) {
	collection, found := CollectionFor(collectionName)
	if !found {
		return Object{}, &Error{Code: "unsupported_collection", Detail: "unsupported inventory collection"}
	}
	external := strings.TrimSpace(object.ExternalID)
	if external == "" || len(external) > 512 {
		return Object{}, &Error{Code: "invalid_external_id", Detail: "inventory object requires a bounded external ID"}
	}
	if strings.IndexFunc(external, unicode.IsControl) >= 0 {
		return Object{}, &Error{Code: "invalid_external_id", Detail: "inventory external ID contains control characters"}
	}
	attributes := make(map[string]string, len(object.Attributes))
	for key, value := range object.Attributes {
		if !collection.Allowed[key] {
			return Object{}, &Error{Code: "attribute_not_allowed", Detail: fmt.Sprintf("attribute %q is not allowed for collection %q", key, collectionName)}
		}
		value = strings.TrimSpace(value)
		if len(value) > 1024 {
			return Object{}, &Error{Code: "attribute_too_long", Detail: fmt.Sprintf("attribute %q exceeds its limit", key)}
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return Object{}, &Error{Code: "attribute_control_char", Detail: fmt.Sprintf("attribute %q contains control characters", key)}
		}
		attributes[key] = value
	}
	for _, required := range collection.Required {
		if attributes[required] == "" {
			return Object{}, &Error{Code: "missing_attribute", Detail: fmt.Sprintf("collection %q requires attribute %q", collectionName, required)}
		}
	}
	switch collectionName {
	case "users":
		email := attributes["email"]
		if len(email) > 320 || strings.Count(email, "@") != 1 || strings.ContainsAny(email, " \t") {
			return Object{}, &Error{Code: "invalid_email", Detail: "user email is not a bounded address"}
		}
	case "devices":
		if value := attributes["last_signal_at"]; value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return Object{}, &Error{Code: "invalid_timestamp", Detail: "device last_signal_at must be RFC3339"}
			}
		}
	}
	return Object{ExternalID: external, Attributes: attributes}, nil
}

// Deduplicate removes identical observations and rejects contradictory ones,
// matching the Python normalization boundary before persistence.
func Deduplicate(collectionName string, objects []Object) ([]Object, int, error) {
	unique := make([]Object, 0, len(objects))
	seen := make(map[string]Object, len(objects))
	duplicates := 0
	for _, raw := range objects {
		object, err := ValidateObject(collectionName, raw)
		if err != nil {
			return nil, 0, err
		}
		existing, found := seen[object.ExternalID]
		if !found {
			seen[object.ExternalID] = object
			unique = append(unique, object)
			continue
		}
		a, _ := json.Marshal(existing.Attributes)
		b, _ := json.Marshal(object.Attributes)
		if string(a) != string(b) {
			return nil, 0, &Error{Code: "duplicate_conflict", Detail: fmt.Sprintf("conflicting observations for external ID %q", object.ExternalID)}
		}
		duplicates++
	}
	return unique, duplicates, nil
}
