// Package inventory defines the small normalized record contract used by the
// first read-only synchronization. It has no SQL or HTTP dependencies.
package inventory

import (
	"encoding/json"
	"fmt"
	"strconv"
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
// adapter asserts the collection was read to its end. Expected carries a
// provider-reported total for the whole collection when the contract exposes
// one; zero means unknown. The runner refuses to call a run complete when the
// reported total does not match what it read.
type Page struct {
	Objects    []Object `json:"objects"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Expected   int      `json:"total,omitempty"`
	// Warnings are non-fatal source warnings. A page warning forces the run to
	// finish as partial so a skipped record never authorizes retirements.
	Warnings []string `json:"warnings,omitempty"`
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
	// safes is the first provider-specific normalized collection. The adapter
	// selects this allowlist from CyberArk /API/Safes; no other payload field
	// reaches storage.
	"safes": {
		Name:     "safes",
		Required: []string{"safe_name", "safe_url_id"},
		Allowed: map[string]bool{
			"safe_name": true, "safe_url_id": true, "location": true,
			"creator_name": true, "olac_enabled": true, "versions_retention": true,
			"days_retention": true, "auto_purge_enabled": true, "creation_time": true,
			"last_modification_time": true, "description": true, "managing_cpm": true,
			"expired_member": true,
		},
	},
	// safe_members is the child collection of a Safe PAM connection. The key
	// includes the parent safe number because member IDs are only unique
	// within a Safe.
	"safe_members": {
		Name: "safe_members",
		Required: []string{
			"safe_number", "safe_name", "member_id", "member_name", "member_type", "permissions",
		},
		Allowed: map[string]bool{
			"safe_number": true, "safe_name": true, "member_id": true, "member_name": true,
			"member_type": true, "membership_expiration_date": true,
			"expired_membership_enabled": true, "predefined_user": true, "read_only": true,
			"permissions": true,
		},
	},
	// accounts is the non-secret PAM account inventory. The optional
	// secret_management subset never includes the password, platform account
	// properties or free-form management reasons.
	"accounts": {
		Name: "accounts",
		Required: []string{
			"safe_number", "safe_name", "account_id", "account_name",
		},
		Allowed: map[string]bool{
			"safe_number": true, "safe_name": true, "account_id": true, "account_name": true,
			"user_name": true, "address": true, "platform_id": true, "account_type": true,
			"category": true, "automatic_management_enabled": true, "management_status": true,
			"secret_modified_at": true,
		},
	},
	// platforms is the non-secret PAM platform catalog. AllowedSafes and
	// platform properties from the detail endpoint are not part of this slice.
	"platforms": {
		Name:     "platforms",
		Required: []string{"platform_name"},
		Allowed: map[string]bool{
			"platform_name": true, "active": true, "platform_base_id": true,
			"platform_type": true, "system_type": true, "psm_server_id": true,
		},
	},
	// psm_servers and psm_connectors are the PSM reference catalogs. They do
	// not create relationships or expose component capacity/health.
	"psm_servers": {
		Name:     "psm_servers",
		Required: []string{"psm_name"},
		Allowed:  map[string]bool{"psm_name": true, "address": true},
	},
	"psm_connectors": {
		Name:     "psm_connectors",
		Required: []string{"connector_name"},
		Allowed:  map[string]bool{"connector_name": true},
	},
	// platform_psm_links is the explainable edge between a platform's
	// PSMServerID reference and the PSM catalog. Ambiguous references are
	// published without a resolved target instead of being guessed.
	"platform_psm_links": {
		Name: "platform_psm_links",
		Required: []string{
			"platform_id", "platform_name", "reference_psm_id", "resolution",
		},
		Allowed: map[string]bool{
			"platform_id": true, "platform_name": true, "reference_psm_id": true,
			"psm_server_id": true, "psm_server_name": true, "resolution": true,
		},
	},
}

// CollectionFor returns only explicitly supported normalized collections.
func CollectionFor(name string) (Collection, bool) {
	collection, found := collections[name]
	return collection, found
}

// CollectionNames returns the stable supported list for configuration checks.
func CollectionNames() []string {
	return []string{"accounts", "devices", "platform_psm_links", "platforms", "psm_connectors", "psm_servers", "safe_members", "safes", "users"}
}

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
	case "safes":
		number, err := strconv.ParseInt(external, 10, 64)
		if err != nil || number < 1 {
			return Object{}, &Error{Code: "invalid_external_id", Detail: "safe external ID must be a positive safe number"}
		}
		for _, field := range []string{"creation_time", "last_modification_time"} {
			if value := attributes[field]; value != "" {
				if _, err := time.Parse(time.RFC3339, value); err != nil {
					return Object{}, &Error{Code: "invalid_timestamp", Detail: fmt.Sprintf("safe %s must be RFC3339", field)}
				}
			}
		}
		for _, field := range []string{"olac_enabled", "auto_purge_enabled", "expired_member"} {
			if value := attributes[field]; value != "" && value != "true" && value != "false" {
				return Object{}, &Error{Code: "invalid_attribute", Detail: fmt.Sprintf("safe %s must be true or false", field)}
			}
		}
		for _, field := range []string{"versions_retention", "days_retention"} {
			if value := attributes[field]; value != "" {
				number, err := strconv.Atoi(value)
				if err != nil || number < 0 {
					return Object{}, &Error{Code: "invalid_attribute", Detail: fmt.Sprintf("safe %s must be a non-negative integer", field)}
				}
			}
		}
	case "safe_members":
		number, err := strconv.ParseInt(attributes["safe_number"], 10, 64)
		if err != nil || number < 1 {
			return Object{}, &Error{Code: "invalid_attribute", Detail: "safe member requires a positive safe number"}
		}
		prefix := attributes["safe_number"] + ":"
		if !strings.HasPrefix(external, prefix) || len(external) <= len(prefix) {
			return Object{}, &Error{Code: "invalid_external_id", Detail: "safe member external ID must include its parent safe number"}
		}
		if value := attributes["membership_expiration_date"]; value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return Object{}, &Error{Code: "invalid_timestamp", Detail: "safe member expiration must be RFC3339"}
			}
		}
		for _, field := range []string{"expired_membership_enabled", "predefined_user", "read_only"} {
			if value := attributes[field]; value != "" && value != "true" && value != "false" {
				return Object{}, &Error{Code: "invalid_attribute", Detail: fmt.Sprintf("safe member %s must be true or false", field)}
			}
		}
		var permissions map[string]any
		if err := json.Unmarshal([]byte(attributes["permissions"]), &permissions); err != nil || permissions == nil {
			return Object{}, &Error{Code: "invalid_attribute", Detail: "safe member permissions must be a JSON object"}
		}
	case "accounts":
		number, err := strconv.ParseInt(attributes["safe_number"], 10, 64)
		if err != nil || number < 1 {
			return Object{}, &Error{Code: "invalid_attribute", Detail: "account requires a positive safe number"}
		}
		prefix := attributes["safe_number"] + ":"
		if !strings.HasPrefix(external, prefix) || len(external) <= len(prefix) {
			return Object{}, &Error{Code: "invalid_external_id", Detail: "account external ID must include its parent safe number"}
		}
		if value := attributes["automatic_management_enabled"]; value != "" && value != "true" && value != "false" {
			return Object{}, &Error{Code: "invalid_attribute", Detail: "account automatic management flag must be true or false"}
		}
		switch attributes["management_status"] {
		case "", "success", "failure", "in_process":
		default:
			return Object{}, &Error{Code: "invalid_attribute", Detail: "account management status is not allowlisted"}
		}
		if value := attributes["secret_modified_at"]; value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return Object{}, &Error{Code: "invalid_timestamp", Detail: "account secret modification time must be RFC3339"}
			}
		}
	case "platforms":
		if value := attributes["active"]; value != "" && value != "true" && value != "false" {
			return Object{}, &Error{Code: "invalid_attribute", Detail: "platform active flag must be true or false"}
		}
	case "platform_psm_links":
		platformID := attributes["platform_id"]
		reference := attributes["reference_psm_id"]
		if platformID == "" || reference == "" || !strings.HasPrefix(external, platformID+":") || len(external) <= len(platformID)+1 {
			return Object{}, &Error{Code: "invalid_external_id", Detail: "platform PSM link requires platform and reference IDs"}
		}
		switch attributes["resolution"] {
		case "exact", "casefold", "missing":
			if attributes["psm_server_id"] == "" {
				return Object{}, &Error{Code: "invalid_attribute", Detail: "resolved platform PSM link requires a catalog target"}
			}
		case "ambiguous":
			if attributes["psm_server_id"] != "" {
				return Object{}, &Error{Code: "invalid_attribute", Detail: "ambiguous platform PSM link must not choose a target"}
			}
		default:
			return Object{}, &Error{Code: "invalid_attribute", Detail: "platform PSM link resolution is not allowlisted"}
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
