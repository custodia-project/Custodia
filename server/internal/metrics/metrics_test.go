package metrics

import (
	"strings"
	"testing"
	"time"

	"custodia/server/internal/connectors"
)

func record(id string, attributes map[string]string) Record {
	return Record{ExternalID: id, Attributes: attributes}
}

func published(records ...Record) Snapshot {
	return Snapshot{Connected: true, Published: true, PublishedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), Records: records}
}

func TestDeclaredMetricsAreValid(t *testing.T) {
	if err := Validate(All()); err != nil {
		t.Fatal(err)
	}
	pam := ForConnector(connectors.CyberArkPAM)
	if len(pam) != 8 {
		t.Fatalf("expected eight PAM metrics, got %d", len(pam))
	}
	if len(ForConnector(connectors.HTTPInventory)) != 0 {
		t.Fatal("the synthetic connector declares no metrics")
	}
	if _, found := Get("cyberark-pam.safes.total.v1"); !found {
		t.Fatal("safes total missing")
	}
}

func TestValidateRejectsUndeclaredFieldsAndUnversionedIDs(t *testing.T) {
	base := Definition{ID: "cyberark-pam.accounts.x.v1", Connector: connectors.CyberArkPAM, Name: "x", Description: "x", Unit: UnitAccounts, Collection: "accounts", Kind: KindCountWhere}
	cases := map[string]Definition{
		"unversioned": func() Definition {
			d := base
			d.ID = "cyberark-pam.accounts.x"
			d.Where = &Condition{Field: "management_status", Values: []string{"failure"}}
			return d
		}(),
		"not allowlist": func() Definition { d := base; d.Where = &Condition{Field: "password", Values: []string{"x"}}; return d }(),
		"collection":    func() Definition { d := base; d.Collection = "sessions"; d.Kind = KindCount; return d }(),
		"planned":       func() Definition { d := base; d.Connector = "qradar"; d.Kind = KindCount; return d }(),
		"no threshold":  func() Definition { d := base; d.Kind = KindAgeOver; d.AgeField = "secret_modified_at"; return d }(),
		"mixed params": func() Definition {
			d := base
			d.Where = &Condition{Field: "management_status", Values: []string{"failure"}}
			d.Denominator = &Condition{Field: "management_status", Values: []string{"success"}}
			return d
		}(),
		"without self": func() Definition {
			d := base
			d.Kind = KindCountWithout
			d.Without = &Reference{Collection: "accounts", Field: "safe_number"}
			return d
		}(),
	}
	for name, definition := range cases {
		if err := Validate([]Definition{definition}); err == nil {
			t.Fatal(name, "accepted")
		}
	}
	duplicate := base
	duplicate.Where = &Condition{Field: "management_status", Values: []string{"failure"}}
	if err := Validate([]Definition{duplicate, duplicate}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal("duplicate accepted", err)
	}
}

func TestEvaluateNeverInventsZero(t *testing.T) {
	definition, _ := Get("cyberark-pam.accounts.total.v1")
	if result := Evaluate(definition, map[string]Snapshot{}); result.Status != StatusUnavailable || result.Reason != ReasonCollectionUnavailable || result.Value != nil {
		t.Fatal("missing collection", result)
	}
	if result := Evaluate(definition, map[string]Snapshot{"accounts": {Connected: true}}); result.Status != StatusUnavailable || result.Reason != ReasonNoPublication || result.Value != nil {
		t.Fatal("unpublished collection", result)
	}
	result := Evaluate(definition, map[string]Snapshot{"accounts": published()})
	if result.Status != StatusAvailable || result.Value == nil || *result.Value != 0 || len(result.Matched) != 0 {
		t.Fatal("confirmed zero", result)
	}
}

func TestEvaluateKinds(t *testing.T) {
	accounts := published(
		record("1:a", map[string]string{"automatic_management_enabled": "false", "management_status": "failure"}),
		record("1:b", map[string]string{"automatic_management_enabled": "true", "management_status": "success"}),
		record("2:c", map[string]string{"automatic_management_enabled": "false"}),
		record("2:d", map[string]string{}),
	)
	disabled, _ := Get("cyberark-pam.accounts.auto_management_disabled.v1")
	if result := Evaluate(disabled, map[string]Snapshot{"accounts": accounts}); *result.Value != 2 || result.Matched[0].ExternalID != "1:a" || result.Matched[1].ExternalID != "2:c" {
		t.Fatal("count_where", result)
	}
	ratio, _ := Get("cyberark-pam.accounts.auto_management_disabled_ratio.v1")
	result := Evaluate(ratio, map[string]Snapshot{"accounts": accounts})
	if result.Status != StatusAvailable || *result.Value != 66.7 || *result.Numerator != 2 || *result.Denominator != 3 || len(result.Matched) != 2 {
		t.Fatal("ratio", result)
	}
	partial := accounts
	partial.Partial = true
	if result := Evaluate(ratio, map[string]Snapshot{"accounts": partial}); result.Status != StatusPartial || result.Value != nil || result.Reason != ReasonPartialDenominator {
		t.Fatal("ratio over partial", result)
	}
	if result := Evaluate(disabled, map[string]Snapshot{"accounts": partial}); result.Status != StatusPartial || result.Value == nil || *result.Value != 2 {
		t.Fatal("partial count must be marked, not hidden", result)
	}
	if result := Evaluate(ratio, map[string]Snapshot{"accounts": published(record("x", map[string]string{}))}); result.Status != StatusUnavailable || result.Reason != ReasonEmptyDenominator || result.Value != nil {
		t.Fatal("empty denominator", result)
	}

	without, _ := Get("cyberark-pam.safes.without_members.v1")
	safes := published(record("101", map[string]string{"safe_name": "Alpha"}), record("102", map[string]string{"safe_name": "Beta"}))
	members := published(record("101:a1", map[string]string{"safe_number": "101"}))
	if result := Evaluate(without, map[string]Snapshot{"safes": safes, "safe_members": members}); *result.Value != 1 || result.Matched[0].ExternalID != "102" {
		t.Fatal("count_without", result)
	}
	members.Partial = true
	if result := Evaluate(without, map[string]Snapshot{"safes": safes, "safe_members": members}); result.Status != StatusPartial || result.Value != nil || result.Reason != ReasonPartialReference {
		t.Fatal("partial reference", result)
	}
	if result := Evaluate(without, map[string]Snapshot{"safes": safes}); result.Reason != ReasonCollectionUnavailable {
		t.Fatal("missing reference collection", result)
	}

	age := Definition{ID: "cyberark-pam.accounts.stale.v1", Connector: connectors.CyberArkPAM, Name: "x", Description: "x", Unit: UnitAccounts,
		Collection: "accounts", Kind: KindAgeOver, AgeField: "secret_modified_at", ThresholdDays: 90}
	if err := Validate([]Definition{age}); err != nil {
		t.Fatal(err)
	}
	aged := published(
		record("old", map[string]string{"secret_modified_at": "2026-01-01T00:00:00Z"}),
		record("new", map[string]string{"secret_modified_at": "2026-11-01T00:00:00Z"}),
		record("none", map[string]string{}),
	)
	if result := Evaluate(age, map[string]Snapshot{"accounts": aged}); *result.Value != 1 || result.Matched[0].ExternalID != "old" || result.Skipped != 1 {
		t.Fatal("age_over", result)
	}
}
