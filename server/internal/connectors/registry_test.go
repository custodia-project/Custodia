package connectors

import "testing"

func TestRegistrySeparatesImplementedFromPlanned(t *testing.T) {
	cyberArk, found := Get(CyberArkPAM)
	if !found || cyberArk.Status != StatusAvailable || len(cyberArk.Collections) == 0 || len(cyberArk.AuthModes) == 0 {
		t.Fatal(cyberArk, found)
	}
	for _, id := range []string{
		"netskope", "qradar", "paloalto", "fortigate", "fortimail", "crowdstrike", "tenable",
		"wazuh", "fleet-osquery", "defectdojo", "crowdsec", "opnsense", "keycloak", "misp", "openbao",
	} {
		descriptor, found := Get(id)
		if !found || descriptor.Status != StatusPlanned {
			t.Fatal(id, descriptor, found)
		}
		// Planned never implies a contract: no endpoints, no collections and
		// no credentials may be guessed.
		if len(descriptor.ProbeEndpoints) != 0 || len(descriptor.Collections) != 0 || len(descriptor.AuthModes) != 0 {
			t.Fatal("planned connector invented a contract", id)
		}
		if Connectable(descriptor) {
			t.Fatal("planned connector is connectable", id)
		}
	}
	// Every vendor tool says whether it is open source or commercial; only the
	// synthetic laboratory contract carries no distribution label.
	for _, descriptor := range List() {
		if descriptor.Status == StatusLaboratory {
			continue
		}
		if descriptor.Distribution != DistributionOpenSource && descriptor.Distribution != DistributionCommercial {
			t.Fatal("vendor connector without distribution label", descriptor.ID)
		}
	}
	if Connectable(Descriptor{Status: StatusLaboratory}) != true {
		t.Fatal("laboratory connector must stay connectable for the gate")
	}
}

func TestRegistryCollectionAndAuthChecks(t *testing.T) {
	descriptor, _ := Get(CyberArkPAM)
	for _, collection := range []string{"safes", "safe_members", "accounts", "platforms", "psm_servers", "psm_connectors", "platform_psm_links"} {
		if !SupportsCollection(descriptor, collection) {
			t.Fatal("missing collection", collection)
		}
	}
	if SupportsCollection(descriptor, "events") {
		t.Fatal("unknown collection accepted")
	}
	for _, mode := range []string{"bearer", "oauth-client-credentials", "mtls"} {
		if !SupportsAuthMode(descriptor, mode) {
			t.Fatal("missing auth mode", mode)
		}
	}
	if SupportsAuthMode(descriptor, "basic") {
		t.Fatal("unknown auth mode accepted")
	}
	laboratory, _ := Get(HTTPInventory)
	if !SupportsAuthMode(laboratory, "") || !SupportsAuthMode(laboratory, "none") {
		t.Fatal("laboratory connector must declare no credentials")
	}
}

func TestProbeEndpointsFollowTheCollection(t *testing.T) {
	descriptor, _ := Get(CyberArkPAM)
	cases := map[string]string{
		"safes":              "/API/Safes",
		"safe_members":       "/API/Safes",
		"accounts":           "/API/Accounts",
		"platforms":          "/API/Platforms",
		"psm_servers":        "/API/PSM/Servers",
		"psm_connectors":     "/API/PSM/Connectors",
		"platform_psm_links": "/API/Platforms",
	}
	for collection, endpoint := range cases {
		endpoints := ProbeEndpointsFor(descriptor, collection)
		if len(endpoints) == 0 || endpoints[0] != endpoint {
			t.Fatal(collection, endpoints)
		}
	}
	if endpoints := ProbeEndpointsFor(descriptor, "platform_psm_links"); len(endpoints) != 2 {
		t.Fatal("platform link probe must read both catalogs", endpoints)
	}
}
