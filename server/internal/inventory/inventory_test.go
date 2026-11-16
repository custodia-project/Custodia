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
