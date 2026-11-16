package inventory

import "testing"

func TestValidateObjectAllowlistAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name       string
		collection string
		object     Object
		code       string
	}{
		{"users-ok", "users", Object{ExternalID: "u1", Attributes: map[string]string{"email": "a@acme.example", "name": "A"}}, ""},
		{"devices-ok", "devices", Object{ExternalID: "d1", Attributes: map[string]string{"user_id": "u1", "last_signal_at": "2026-11-16T12:00:00Z"}}, ""},
		{"business-ok", "safes", Object{ExternalID: "42", Attributes: map[string]string{"safe_name": "Alpha", "safe_url_id": "alpha", "creation_time": "2026-01-02T03:04:05Z", "olac_enabled": "true", "versions_retention": "5"}}, ""},
		{"safe-bad-number", "safes", Object{ExternalID: "zero", Attributes: map[string]string{"safe_name": "Alpha", "safe_url_id": "alpha"}}, "invalid_external_id"},
		{"safe-missing-url", "safes", Object{ExternalID: "42", Attributes: map[string]string{"safe_name": "Alpha"}}, "missing_attribute"},
		{"safe-bad-bool", "safes", Object{ExternalID: "42", Attributes: map[string]string{"safe_name": "Alpha", "safe_url_id": "alpha", "auto_purge_enabled": "yes"}}, "invalid_attribute"},
		{"safe-bad-retention", "safes", Object{ExternalID: "42", Attributes: map[string]string{"safe_name": "Alpha", "safe_url_id": "alpha", "days_retention": "-1"}}, "invalid_attribute"},
		{"safe-bad-time", "safes", Object{ExternalID: "42", Attributes: map[string]string{"safe_name": "Alpha", "safe_url_id": "alpha", "creation_time": "yesterday"}}, "invalid_timestamp"},
		{"member-ok", "safe_members", Object{ExternalID: "42:m1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "member_id": "m1", "member_name": "Alice", "member_type": "User", "permissions": `{"useAccounts":true}`}}, ""},
		{"member-bad-parent", "safe_members", Object{ExternalID: "43:m1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "member_id": "m1", "member_name": "Alice", "member_type": "User", "permissions": `{}`}}, "invalid_external_id"},
		{"member-no-permissions", "safe_members", Object{ExternalID: "42:m1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "member_id": "m1", "member_name": "Alice", "member_type": "User"}}, "missing_attribute"},
		{"member-bad-permissions", "safe_members", Object{ExternalID: "42:m1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "member_id": "m1", "member_name": "Alice", "member_type": "User", "permissions": "yes"}}, "invalid_attribute"},
		{"member-bad-time", "safe_members", Object{ExternalID: "42:m1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "member_id": "m1", "member_name": "Alice", "member_type": "User", "permissions": `{}`, "membership_expiration_date": "tomorrow"}}, "invalid_timestamp"},
		{"account-ok", "accounts", Object{ExternalID: "42:a1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "account_id": "a1", "account_name": "svc-alpha", "user_name": "svc-alpha", "address": "db.acme.example", "platform_id": "Unix", "automatic_management_enabled": "true", "management_status": "success", "secret_modified_at": "2026-01-02T03:04:05Z"}}, ""},
		{"account-bad-parent", "accounts", Object{ExternalID: "43:a1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "account_id": "a1", "account_name": "svc-alpha"}}, "invalid_external_id"},
		{"account-bad-status", "accounts", Object{ExternalID: "42:a1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "account_id": "a1", "account_name": "svc-alpha", "management_status": "unknown"}}, "invalid_attribute"},
		{"account-bad-bool", "accounts", Object{ExternalID: "42:a1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "account_id": "a1", "account_name": "svc-alpha", "automatic_management_enabled": "yes"}}, "invalid_attribute"},
		{"account-bad-time", "accounts", Object{ExternalID: "42:a1", Attributes: map[string]string{"safe_number": "42", "safe_name": "Alpha", "account_id": "a1", "account_name": "svc-alpha", "secret_modified_at": "yesterday"}}, "invalid_timestamp"},
		{"platform-ok", "platforms", Object{ExternalID: "WinDomain", Attributes: map[string]string{"platform_name": "Windows Domain", "active": "true", "platform_base_id": "WinDomain", "platform_type": "Regular", "system_type": "Windows", "psm_server_id": "PSMServer_acme"}}, ""},
		{"platform-bad-active", "platforms", Object{ExternalID: "WinDomain", Attributes: map[string]string{"platform_name": "Windows Domain", "active": "yes"}}, "invalid_attribute"},
		{"platform-no-name", "platforms", Object{ExternalID: "WinDomain", Attributes: map[string]string{}}, "missing_attribute"},
		{"psm-server-ok", "psm_servers", Object{ExternalID: "PSMServer_acme", Attributes: map[string]string{"psm_name": "PSM Acme", "address": "psm.acme.example"}}, ""},
		{"psm-server-no-name", "psm_servers", Object{ExternalID: "PSMServer_acme", Attributes: map[string]string{}}, "missing_attribute"},
		{"psm-connector-ok", "psm_connectors", Object{ExternalID: "PSM-RDP", Attributes: map[string]string{"connector_name": "RDP"}}, ""},
		{"psm-connector-no-name", "psm_connectors", Object{ExternalID: "PSM-RDP", Attributes: map[string]string{}}, "missing_attribute"},
		{"psm-link-exact", "platform_psm_links", Object{ExternalID: "WinDomain:PSMServer_acme", Attributes: map[string]string{"platform_id": "WinDomain", "platform_name": "Windows", "reference_psm_id": "PSMServer_acme", "psm_server_id": "PSMServer_acme", "psm_server_name": "PSM Acme", "resolution": "exact"}}, ""},
		{"psm-link-missing", "platform_psm_links", Object{ExternalID: "WinDomain:PSMServer_missing", Attributes: map[string]string{"platform_id": "WinDomain", "platform_name": "Windows", "reference_psm_id": "PSMServer_missing", "psm_server_id": "PSMServer_missing", "psm_server_name": "PSMServer_missing", "resolution": "missing"}}, ""},
		{"psm-link-ambiguous", "platform_psm_links", Object{ExternalID: "WinDomain:PSMServer_case", Attributes: map[string]string{"platform_id": "WinDomain", "platform_name": "Windows", "reference_psm_id": "PSMServer_case", "resolution": "ambiguous"}}, ""},
		{"psm-link-ambiguous-target", "platform_psm_links", Object{ExternalID: "WinDomain:PSMServer_case", Attributes: map[string]string{"platform_id": "WinDomain", "platform_name": "Windows", "reference_psm_id": "PSMServer_case", "psm_server_id": "PSMServer_case", "resolution": "ambiguous"}}, "invalid_attribute"},
		{"psm-link-resolved-no-target", "platform_psm_links", Object{ExternalID: "WinDomain:PSMServer_acme", Attributes: map[string]string{"platform_id": "WinDomain", "platform_name": "Windows", "reference_psm_id": "PSMServer_acme", "resolution": "exact"}}, "invalid_attribute"},
		{"psm-link-bad-prefix", "platform_psm_links", Object{ExternalID: "Other:PSMServer_acme", Attributes: map[string]string{"platform_id": "WinDomain", "platform_name": "Windows", "reference_psm_id": "PSMServer_acme", "psm_server_id": "PSMServer_acme", "resolution": "exact"}}, "invalid_external_id"},
		{"unknown-attribute", "users", Object{ExternalID: "u1", Attributes: map[string]string{"email": "a@acme.example", "secret": "x"}}, "attribute_not_allowed"},
		{"missing-email", "users", Object{ExternalID: "u1", Attributes: map[string]string{"name": "A"}}, "missing_attribute"},
		{"bad-email", "users", Object{ExternalID: "u1", Attributes: map[string]string{"email": "not-an-email"}}, "invalid_email"},
		{"bad-device-time", "devices", Object{ExternalID: "d1", Attributes: map[string]string{"user_id": "u1", "last_signal_at": "yesterday"}}, "invalid_timestamp"},
		{"empty-id", "users", Object{ExternalID: " ", Attributes: map[string]string{"email": "a@acme.example"}}, "invalid_external_id"},
		{"control-id", "users", Object{ExternalID: "u\x00", Attributes: map[string]string{"email": "a@acme.example"}}, "invalid_external_id"},
		{"unsupported-collection", "secrets", Object{ExternalID: "s1", Attributes: map[string]string{}}, "unsupported_collection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateObject(tc.collection, tc.object)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			coded, ok := err.(*Error)
			if !ok || coded.Code != tc.code {
				t.Fatal(err)
			}
		})
	}
}

func TestDeduplicateIdenticalAndConflicting(t *testing.T) {
	objects := []Object{
		{ExternalID: "u1", Attributes: map[string]string{"email": "a@acme.example", "name": "A"}},
		{ExternalID: "u1", Attributes: map[string]string{"name": "A", "email": "a@acme.example"}},
	}
	unique, duplicates, err := Deduplicate("users", objects)
	if err != nil || len(unique) != 1 || duplicates != 1 {
		t.Fatal(unique, duplicates, err)
	}
	objects = append(objects, Object{ExternalID: "u1", Attributes: map[string]string{"email": "a@acme.example", "name": "B"}})
	if _, _, err = Deduplicate("users", objects); err == nil {
		t.Fatal("conflicting duplicate accepted")
	} else if coded, ok := err.(*Error); !ok || coded.Code != "duplicate_conflict" {
		t.Fatal(err)
	}
}
