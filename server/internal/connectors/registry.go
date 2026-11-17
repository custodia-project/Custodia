// Package connectors is the static registry of source tools Custodia can
// onboard. A descriptor states only what is implemented and verified: planned
// tools carry no endpoints, collections or credentials contract, so a
// connection cannot be created for them by guessing.
package connectors

import (
	"sort"
	"strings"
)

// Descriptor describes one source tool and the connection fields it accepts.
type Descriptor struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Vendor         string   `json:"vendor"`
	Category       string   `json:"category"`
	Status         string   `json:"status"`
	Distribution   string   `json:"distribution,omitempty"`
	AuthModes      []string `json:"auth_modes"`
	Collections    []string `json:"collections"`
	ProbeEndpoints []string `json:"probe_endpoints"`
	Notes          string   `json:"notes"`
}

// Distribution says whether the source tool itself is open source (its API
// can be exercised against a self-hosted instance) or commercial (it needs a
// customer tenant). It is a catalog label, not a support claim.
const (
	DistributionOpenSource = "open-source"
	DistributionCommercial = "commercial"
)

const (
	StatusAvailable  = "available"
	StatusLaboratory = "laboratory"
	StatusPlanned    = "planned"
)

// CyberArkPAM is the implemented read-only PAM connector.
const CyberArkPAM = "cyberark-pam-safes-v1"

// Wazuh is the implemented read-only Wazuh manager connector.
const Wazuh = "wazuh"

// HTTPInventory is the synthetic normalized connector used by the laboratory
// and the browser gate. It is not a vendor connector.
const HTTPInventory = "http-inventory-v1"

var cyberArkCollections = []string{
	"safes", "safe_members", "accounts", "platforms",
	"psm_servers", "psm_connectors", "platform_psm_links",
}

var cyberArkEndpoints = []string{
	"/API/Accounts", "/API/Platforms", "/API/PSM/Connectors", "/API/PSM/Servers", "/API/Safes",
}

var registry = []Descriptor{
	{
		ID:           CyberArkPAM,
		Distribution: DistributionCommercial,
		Name:         "CyberArk PAM",
		Vendor:       "CyberArk",
		Category:     "PAM",
		Status:       StatusAvailable,
		AuthModes: []string{
			"bearer", "oauth-client-credentials", "mtls",
		},
		Collections:    cyberArkCollections,
		ProbeEndpoints: cyberArkEndpoints,
		Notes:          "Read-only Safes, members, non-secret account metadata, platforms and the PSM catalogs. No secret retrieval, sessions or account passwords.",
	},
	{
		ID:       HTTPInventory,
		Name:     "Normalized HTTP inventory",
		Vendor:   "Custodia",
		Category: "laboratory",
		Status:   StatusLaboratory,
		AuthModes: []string{
			"none",
		},
		Collections:    []string{"users", "devices"},
		ProbeEndpoints: nil,
		Notes:          "Synthetic loopback contract for tests and the local browser gate. Not a vendor connector and not for tenant onboarding.",
	},
	{
		ID:           "netskope",
		Distribution: DistributionCommercial,
		Name:         "Netskope",
		Vendor:       "Netskope",
		Category:     "SSE",
		Status:       StatusPlanned,
		Notes:        "The DS Hub contract exists, but no Go adapter is implemented or accepted. Planned status does not authorize endpoints or credentials.",
	},
	{
		ID:           "qradar",
		Distribution: DistributionCommercial,
		Name:         "IBM QRadar",
		Vendor:       "IBM",
		Category:     "SIEM",
		Status:       StatusPlanned,
		Notes:        "No Custodia contract. Endpoints, auth and collections must be approved before a connection can be created.",
	},
	{
		ID:           "paloalto",
		Distribution: DistributionCommercial,
		Name:         "Palo Alto Networks",
		Vendor:       "Palo Alto Networks",
		Category:     "Network security",
		Status:       StatusPlanned,
		Notes:        "No Custodia contract. Firewall or Panorama scope must be decided per tenant before implementation.",
	},
	{
		ID:           "fortigate",
		Distribution: DistributionCommercial,
		Name:         "FortiGate",
		Vendor:       "Fortinet",
		Category:     "Network security",
		Status:       StatusPlanned,
		Notes:        "No Custodia contract. FortiGate and FortiManager expose different APIs and neither is approved yet.",
	},
	{
		ID:           "crowdstrike",
		Distribution: DistributionCommercial,
		Name:         "CrowdStrike Falcon",
		Vendor:       "CrowdStrike",
		Category:     "EDR",
		Status:       StatusPlanned,
		Notes:        "No Custodia contract in this backend. Planned status carries no endpoint or scope claim.",
	},
	{
		ID:           "tenable",
		Distribution: DistributionCommercial,
		Name:         "Tenable",
		Vendor:       "Tenable",
		Category:     "Vulnerability management",
		Status:       StatusPlanned,
		Notes:        "No Custodia contract. Asset and vulnerability scopes must be approved before onboarding.",
	},
	{
		ID:           "fortimail",
		Name:         "FortiMail",
		Vendor:       "Fortinet",
		Category:     "Email security",
		Status:       StatusPlanned,
		Distribution: DistributionCommercial,
		Notes:        "No Custodia contract. Requires a customer appliance or tenant to validate its REST API before implementation.",
	},
	{
		ID:             Wazuh,
		Name:           "Wazuh",
		Vendor:         "Wazuh",
		Category:       "SIEM/XDR",
		Status:         StatusAvailable,
		Distribution:   DistributionOpenSource,
		AuthModes:      []string{"basic-exchange"},
		Collections:    []string{"agents", "groups"},
		ProbeEndpoints: []string{"/agents", "/groups", "/manager/info"},
		Notes:          "Read-only agent and group inventory from the Wazuh manager API. Alerts live in the indexer and are outside this connector.",
	},
	{
		ID:           "fleet-osquery",
		Name:         "Fleet (osquery)",
		Vendor:       "Fleet",
		Category:     "Endpoint inventory",
		Status:       StatusPlanned,
		Distribution: DistributionOpenSource,
		Notes:        "Open-source priority candidate. REST API to be verified against a self-hosted instance before a contract is approved.",
	},
	{
		ID:           "defectdojo",
		Name:         "DefectDojo",
		Vendor:       "OWASP",
		Category:     "Vulnerability management",
		Status:       StatusPlanned,
		Distribution: DistributionOpenSource,
		Notes:        "Open-source priority candidate. REST API v2 to be verified against a self-hosted instance before a contract is approved.",
	},
	{
		ID:           "crowdsec",
		Name:         "CrowdSec",
		Vendor:       "CrowdSec",
		Category:     "Intrusion prevention",
		Status:       StatusPlanned,
		Distribution: DistributionOpenSource,
		Notes:        "Open-source candidate. Local API to be verified against a self-hosted instance; the hosted console is out of scope.",
	},
	{
		ID:           "opnsense",
		Name:         "OPNsense",
		Vendor:       "Deciso",
		Category:     "Network security",
		Status:       StatusPlanned,
		Distribution: DistributionOpenSource,
		Notes:        "Open-source candidate. REST API to be verified against a self-hosted instance before a contract is approved.",
	},
	{
		ID:           "keycloak",
		Name:         "Keycloak",
		Vendor:       "Keycloak",
		Category:     "IAM",
		Status:       StatusPlanned,
		Distribution: DistributionOpenSource,
		Notes:        "Open-source candidate. Admin REST API to be verified against a self-hosted instance before a contract is approved.",
	},
	{
		ID:           "misp",
		Name:         "MISP",
		Vendor:       "MISP Project",
		Category:     "Threat intelligence",
		Status:       StatusPlanned,
		Distribution: DistributionOpenSource,
		Notes:        "Open-source candidate. REST API to be verified against a self-hosted instance before a contract is approved.",
	},
	{
		ID:           "openbao",
		Name:         "OpenBao",
		Vendor:       "OpenBao",
		Category:     "Secrets management",
		Status:       StatusPlanned,
		Distribution: DistributionOpenSource,
		Notes:        "Open-source candidate. Metadata-only scope (mounts, auth methods, policies); secret values are never read.",
	},
}

// List returns the registry ordered by category then name. Planned tools are
// included so an operator can see the roadmap, but they are not connectable.
func List() []Descriptor {
	result := make([]Descriptor, len(registry))
	copy(result, registry)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Category != result[j].Category {
			return result[i].Category < result[j].Category
		}
		return result[i].Name < result[j].Name
	})
	return result
}

// Get returns one descriptor by ID.
func Get(id string) (Descriptor, bool) {
	for _, descriptor := range registry {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// Connectable reports whether a connection may be created for this connector.
func Connectable(descriptor Descriptor) bool {
	return descriptor.Status == StatusAvailable || descriptor.Status == StatusLaboratory
}

// SupportsCollection reports whether the descriptor declares the collection.
func SupportsCollection(descriptor Descriptor, collection string) bool {
	for _, candidate := range descriptor.Collections {
		if candidate == collection {
			return true
		}
	}
	return false
}

// SupportsAuthMode reports whether the descriptor declares the auth mode.
// The synthetic connector authenticates with no credential and declares none.
func SupportsAuthMode(descriptor Descriptor, mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "none"
	}
	for _, candidate := range descriptor.AuthModes {
		if candidate == mode {
			return true
		}
	}
	return false
}

// ProbeEndpointsFor returns the read-only endpoints a probe needs for one
// collection, so a tenant is not asked for permissions it does not use.
func ProbeEndpointsFor(descriptor Descriptor, collection string) []string {
	if descriptor.ID == CyberArkPAM {
		switch collection {
		case "safes", "safe_members":
			return []string{"/API/Safes"}
		case "accounts":
			return []string{"/API/Accounts"}
		case "platforms":
			return []string{"/API/Platforms"}
		case "psm_servers":
			return []string{"/API/PSM/Servers"}
		case "psm_connectors":
			return []string{"/API/PSM/Connectors"}
		case "platform_psm_links":
			return []string{"/API/Platforms", "/API/PSM/Servers"}
		}
	}
	return descriptor.ProbeEndpoints
}

// CollectionNames returns every implemented collection across available
// connectors, for configuration checks.
func CollectionNames() []string {
	seen := map[string]bool{}
	result := []string{}
	for _, descriptor := range registry {
		if !Connectable(descriptor) {
			continue
		}
		for _, collection := range descriptor.Collections {
			if seen[collection] {
				continue
			}
			seen[collection] = true
			result = append(result, collection)
		}
	}
	sort.Strings(result)
	return result
}
