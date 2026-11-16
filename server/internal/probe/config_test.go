package probe

import (
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		BaseURL:       "https://pam.acme.example",
		CredentialRef: "env:CUSTODIA_PAM_CLIENT_SECRET",
		CAFile:        "/etc/custodia/acme-ca.pem",
		Endpoints:     []string{"/API/Platforms", "/API/PSM/Servers"},
		Authorized:    true,
		Timeout:       20 * time.Second,
	}
}

func TestNewPlanAcceptsAuthorizedReadOnlyProbe(t *testing.T) {
	plan, err := NewPlan(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if plan.BaseURL != "https://pam.acme.example" || plan.CredentialRef != "env:CUSTODIA_PAM_CLIENT_SECRET" || len(plan.Endpoints) != 2 || plan.Timeout != 20*time.Second {
		t.Fatal(plan)
	}
	if strings.Contains(plan.BaseURL, "CUSTODIA") || strings.Contains(plan.BaseURL, "secret") {
		t.Fatal("plan leaked a credential")
	}
}

func TestNewPlanRequiresAuthorizationAndCredentialReference(t *testing.T) {
	config := validConfig()
	config.Authorized = false
	if _, err := NewPlan(config); err == nil {
		t.Fatal("probe accepted without owner authorization")
	}
	for _, reference := range []string{"", "literal-secret-value", "env:", "file:", "secret:", "env:with\ncontrol"} {
		config = validConfig()
		config.CredentialRef = reference
		if _, err := NewPlan(config); err == nil {
			t.Fatal("unsafe credential reference accepted", reference)
		}
	}
}

func TestNewPlanRequiresTLSWithoutUserInfoQueryOrPath(t *testing.T) {
	for _, base := range []string{
		"http://pam.acme.example",
		"https://user:pass@pam.acme.example",
		"https://pam.acme.example/pvwa",
		"https://pam.acme.example?x=1",
		"https://pam.acme.example#fragment",
		"https://",
	} {
		config := validConfig()
		config.BaseURL = base
		if _, err := NewPlan(config); err == nil {
			t.Fatal("unsafe base URL accepted", base)
		}
	}
}

func TestNewPlanRejectsUnlistedEndpointsAndBoundsTimeout(t *testing.T) {
	for _, endpoint := range []string{"/API/Accounts/admin", "/API/PSM/Sessions", "/API/Accounts/1/Password", "https://evil.example/API/Platforms"} {
		config := validConfig()
		config.Endpoints = []string{endpoint}
		if _, err := NewPlan(config); err == nil {
			t.Fatal("unlisted endpoint accepted", endpoint)
		}
	}
	config := validConfig()
	config.Timeout = 3 * time.Minute
	if _, err := NewPlan(config); err == nil {
		t.Fatal("oversized timeout accepted")
	}
	config = validConfig()
	config.Timeout = 500 * time.Millisecond
	if _, err := NewPlan(config); err == nil {
		t.Fatal("undersized timeout accepted")
	}
}

func TestNewPlanDeduplicatesEndpointsAndDefaultsTimeout(t *testing.T) {
	config := validConfig()
	config.Endpoints = []string{"/API/Platforms", "/API/Platforms", "/API/PSM/Connectors"}
	config.Timeout = 0
	config.CAFile = ""
	plan, err := NewPlan(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Endpoints) != 2 || plan.Endpoints[0] != "/API/Platforms" || plan.Endpoints[1] != "/API/PSM/Connectors" {
		t.Fatal(plan.Endpoints)
	}
	if plan.Timeout != defaultTimeout {
		t.Fatal(plan.Timeout)
	}
}

func TestNewPlanRejectsMalformedCAReference(t *testing.T) {
	for _, ca := range []string{"https://ca.example/root.pem", "file\nname", "env:CA"} {
		config := validConfig()
		config.CAFile = ca
		if _, err := NewPlan(config); err == nil {
			t.Fatal("invalid CA path accepted", ca)
		}
	}
}
