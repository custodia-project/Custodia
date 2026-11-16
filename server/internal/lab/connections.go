package lab

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"custodia/server/internal/connectors"
	"custodia/server/internal/probe"
)

// tenantNamePattern bounds generated connection IDs. The operator never has to
// invent an ID: Custodia derives it from the connector.
var generatedConnection = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,53}$`)

// migrateConnections is migration 7. It is additive: every existing row keeps
// its data and defaults to "no credential", which the loopback laboratory
// connector expects.
func migrateConnections(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version >= 7 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE lab_sync_sources ADD COLUMN auth_mode TEXT NOT NULL DEFAULT 'none';
ALTER TABLE lab_sync_sources ADD COLUMN credential_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_sync_sources ADD COLUMN token_url TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_sync_sources ADD COLUMN client_id_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_sync_sources ADD COLUMN client_secret_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_sync_sources ADD COLUMN scopes_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE lab_sync_sources ADD COLUMN ca_file TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_sync_sources ADD COLUMN client_cert_file TEXT NOT NULL DEFAULT '';
ALTER TABLE lab_sync_sources ADD COLUMN client_key_file TEXT NOT NULL DEFAULT '';
INSERT INTO lab_schema_migrations VALUES(7);`)
	return err
}

// migrateConnectionGroups is migration 8. It adds the tenant group shared by
// the collection rows of one connection. Existing rows become their own group,
// so nothing changes for a single-collection connection.
func migrateConnectionGroups(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version >= 8 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE lab_sync_sources ADD COLUMN group_id TEXT NOT NULL DEFAULT '';
UPDATE lab_sync_sources SET group_id = connection_id WHERE group_id = '';
CREATE INDEX IF NOT EXISTS lab_sync_sources_group ON lab_sync_sources(group_id);
INSERT INTO lab_schema_migrations VALUES(8);`)
	return err
}

// ProbePlan builds the same validated, authenticated plan used by the probe
// CLI. Credential values are resolved only when the client is prepared.
func (source SyncSource) ProbePlan() (probe.Plan, error) {
	return source.probePlan(nil)
}

func (source SyncSource) probePlan(endpoints []string) (probe.Plan, error) {
	descriptor, found := connectors.Get(source.Connector)
	if !found {
		return probe.Plan{}, errors.New("unsupported connector")
	}
	if !connectors.Connectable(descriptor) {
		return probe.Plan{}, errors.New("connector is planned, not connectable")
	}
	if len(endpoints) == 0 {
		endpoints = connectors.ProbeEndpointsFor(descriptor, source.Collection)
	}
	return probe.NewPlan(probe.Config{
		BaseURL:         source.BaseURL,
		AuthMode:        source.AuthMode,
		CredentialRef:   source.CredentialRef,
		TokenURL:        source.TokenURL,
		ClientIDRef:     source.ClientIDRef,
		ClientSecretRef: source.ClientSecretRef,
		Scopes:          source.Scopes,
		ClientCertFile:  source.ClientCertFile,
		ClientKeyFile:   source.ClientKeyFile,
		CAFile:          source.CAFile,
		Endpoints:       endpoints,
		Authorized:      true,
		Timeout:         0,
	})
}

// validateConnectionConfiguration checks a source against its connector
// descriptor. The synthetic connector keeps the loopback-only laboratory
// contract; an implemented vendor connector requires an HTTPS tenant URL and a
// full auth plan, validated by the same code the probe uses.
func validateConnectionConfiguration(source SyncSource, grants []string) error {
	descriptor, found := connectors.Get(source.Connector)
	if !found {
		return errors.New("unsupported connector")
	}
	if !connectors.Connectable(descriptor) {
		return fmt.Errorf("connector %s is planned, not connectable", descriptor.ID)
	}
	if err := validateCommonConfiguration(source, grants); err != nil {
		return err
	}
	if !connectors.SupportsCollection(descriptor, source.Collection) {
		return fmt.Errorf("connector %s does not implement collection %s", descriptor.ID, source.Collection)
	}
	if descriptor.Status == connectors.StatusLaboratory {
		if normalizedAuthMode(source.AuthMode) != "none" || source.CredentialRef != "" ||
			source.CAFile != "" || source.ClientCertFile != "" || source.ClientKeyFile != "" {
			return errors.New("the laboratory connector sends no credentials")
		}
		return validLabBaseURL(source.BaseURL)
	}
	if !connectors.SupportsAuthMode(descriptor, source.AuthMode) {
		return fmt.Errorf("auth mode %s is not supported by %s", normalizedAuthMode(source.AuthMode), descriptor.ID)
	}
	if _, err := source.ProbePlan(); err != nil {
		return fmt.Errorf("connection auth: %w", err)
	}
	return nil
}

// ConnectionRegistration is one tenant connection with one or more
// collection rows. The group is what the operator sees; each collection keeps
// its own durable run, baseline and grants.
type ConnectionRegistration struct {
	GroupID     string       `json:"group_id"`
	Collections []string     `json:"collections"`
	Connections []SyncSource `json:"connections"`
}

// RegisterConnection is the onboarding path: an administrator names a
// connector, a tenant URL, credential references and one or more collections.
// Custodia creates one source per collection under a shared group without
// contacting the tenant. The creator receives read and run grants for every
// collection.
func (s *Store) RegisterConnection(ctx context.Context, actor string, principal Principal, source SyncSource, collections []string, now time.Time) (ConnectionRegistration, error) {
	if !s.IsAdministrator(ctx, principal) {
		return ConnectionRegistration{}, ErrDenied
	}
	descriptor, found := connectors.Get(source.Connector)
	if !found {
		return ConnectionRegistration{}, errors.New("unsupported connector")
	}
	if !connectors.Connectable(descriptor) {
		return ConnectionRegistration{}, fmt.Errorf("connector %s is planned, not connectable", descriptor.ID)
	}
	collections = normalizeCollections(collections, source.Collection, descriptor)
	if len(collections) == 0 {
		return ConnectionRegistration{}, errors.New("choose at least one implemented collection")
	}
	if source.Generation == "" {
		source.Generation = source.Connector
	}
	if source.PageSize == 0 {
		source.PageSize = syncDefaultPage
	}
	if source.MaxPages == 0 {
		source.MaxPages = syncDefaultPages
	}
	if source.MaxRecords == 0 {
		source.MaxRecords = syncDefaultRecords
	}
	longest := 0
	for _, collection := range collections {
		if len(collectionSlug(collection)) > longest {
			longest = len(collectionSlug(collection))
		}
	}
	groupID := generatedConnectionIDFor(source.Connector, longest+1)
	grants := []string{principal.Subject}
	children := make([]SyncSource, 0, len(collections))
	for _, collection := range collections {
		child := source
		child.GroupID = groupID
		child.Collection = collection
		if len(collections) == 1 {
			child.ConnectionID = groupID
		} else {
			child.ConnectionID = groupID + "-" + collectionSlug(collection)
		}
		if !connectionPattern.MatchString(child.ConnectionID) || len(child.ConnectionID) > 64 {
			return ConnectionRegistration{}, errors.New("generated collection connection ID is too long")
		}
		if err := validateConnectionConfiguration(child, grants); err != nil {
			return ConnectionRegistration{}, err
		}
		children = append(children, child)
	}
	source.CreatedBy = actor
	source.CreatedAt = now.UTC().Format(time.RFC3339)
	scopes, err := json.Marshal(source.Scopes)
	if err != nil {
		return ConnectionRegistration{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ConnectionRegistration{}, err
	}
	defer tx.Rollback()
	for _, child := range children {
		child.CreatedBy = source.CreatedBy
		child.CreatedAt = source.CreatedAt
		// kind stays "netskope" for external sources: it is the legacy label
		// the read models already understand. The connector ID is the
		// authoritative tool identity.
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_connections(id,kind) VALUES(?,?)", child.ConnectionID, "netskope"); err != nil {
			return ConnectionRegistration{}, fmt.Errorf("connection ID already exists or is invalid")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO lab_sync_sources(connection_id,group_id,connector,base_url,collection,scope,generation,enabled,page_size,max_pages,max_records,interval_minutes,auth_mode,credential_ref,token_url,client_id_ref,client_secret_ref,scopes_json,ca_file,client_cert_file,client_key_file,created_by,created_at)
			VALUES(?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			child.ConnectionID, child.GroupID, child.Connector, child.BaseURL, child.Collection, child.Scope, child.Generation,
			child.PageSize, child.MaxPages, child.MaxRecords, child.Interval,
			normalizedAuthMode(child.AuthMode), child.CredentialRef, child.TokenURL, child.ClientIDRef, child.ClientSecretRef,
			string(scopes), child.CAFile, child.ClientCertFile, child.ClientKeyFile, child.CreatedBy, child.CreatedAt); err != nil {
			return ConnectionRegistration{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_grants(principal_id,connection_id) VALUES(?,?)", principal.ID, child.ConnectionID); err != nil {
			return ConnectionRegistration{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO lab_sync_grants(principal_id,connection_id,can_run) VALUES(?,?,1)", principal.ID, child.ConnectionID); err != nil {
			return ConnectionRegistration{}, err
		}
	}
	childIDs := make([]string, 0, len(children))
	for _, child := range children {
		childIDs = append(childIDs, child.ConnectionID)
	}
	after, _ := json.Marshal(map[string]any{
		"group_id": groupID, "connection_ids": childIDs, "connector": source.Connector,
		"base_url": source.BaseURL, "collections": collections, "scope": source.Scope,
		"generation": source.Generation, "auth_mode": normalizedAuthMode(source.AuthMode),
		"credential_configured": source.CredentialRef != "", "ca_configured": source.CAFile != "",
		"mtls_configured": source.ClientCertFile != "",
	})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'connection_register','{}',?,?)", actor, after, now.UTC().Format(time.RFC3339)); err != nil {
		return ConnectionRegistration{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_grant','{}',?,?)", actor, after, now.UTC().Format(time.RFC3339)); err != nil {
		return ConnectionRegistration{}, err
	}
	if err = tx.Commit(); err != nil {
		return ConnectionRegistration{}, err
	}
	return ConnectionRegistration{GroupID: groupID, Collections: collections, Connections: children}, nil
}

// ProbeConnection runs the read-only probe for one connection group or one
// collection row and records only a sanitized summary. It never persists
// observations and never queues a sync run.
func (s *Store) ProbeConnection(ctx context.Context, principal Principal, connectionID string, now time.Time) (probe.Report, error) {
	if !s.IsAdministrator(ctx, principal) {
		return probe.Report{}, ErrDenied
	}
	sources, err := loadGroupSources(ctx, s.DB, connectionID)
	if err != nil {
		return probe.Report{}, err
	}
	if len(sources) == 0 {
		child, err := s.loadSyncSource(ctx, connectionID)
		if err != nil {
			return probe.Report{}, ErrSyncNotFound
		}
		sources = []SyncSource{child}
	}
	first := sources[0]
	endpoints := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		if !sameConnectionConfig(first, source) {
			return probe.Report{}, errors.New("collection rows of this group have different connection settings")
		}
		descriptor, found := connectors.Get(source.Connector)
		if !found {
			return probe.Report{}, errors.New("unsupported connector")
		}
		for _, endpoint := range connectors.ProbeEndpointsFor(descriptor, source.Collection) {
			if seen[endpoint] {
				continue
			}
			seen[endpoint] = true
			endpoints = append(endpoints, endpoint)
		}
	}
	plan, err := first.probePlan(endpoints)
	if err != nil {
		return probe.Report{}, err
	}
	report, err := probe.RunPlan(ctx, plan)
	if err != nil {
		return probe.Report{}, err
	}
	after, _ := json.Marshal(map[string]any{
		"connection_id": connectionID, "group_id": first.GroupID, "connector": first.Connector,
		"auth_mode": string(plan.AuthMode), "failed_endpoints": report.Failed(),
		"endpoints": report.Endpoints,
	})
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'connection_probe','{}',?,?)", principal.Actor(), after, now.UTC().Format(time.RFC3339)); err != nil {
		return probe.Report{}, err
	}
	return report, nil
}

func sameConnectionConfig(left, right SyncSource) bool {
	return left.Connector == right.Connector && left.BaseURL == right.BaseURL &&
		normalizedAuthMode(left.AuthMode) == normalizedAuthMode(right.AuthMode) &&
		left.CredentialRef == right.CredentialRef && left.TokenURL == right.TokenURL &&
		left.ClientIDRef == right.ClientIDRef && left.ClientSecretRef == right.ClientSecretRef &&
		left.CAFile == right.CAFile && left.ClientCertFile == right.ClientCertFile &&
		left.ClientKeyFile == right.ClientKeyFile && slices.Equal(left.Scopes, right.Scopes)
}

func normalizeCollections(collections []string, single string, descriptor connectors.Descriptor) []string {
	candidates := collections
	if len(candidates) == 0 && single != "" {
		candidates = []string{single}
	}
	if len(candidates) == 0 && len(descriptor.Collections) > 0 {
		candidates = []string{descriptor.Collections[0]}
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(candidates))
	for _, collection := range candidates {
		collection = strings.TrimSpace(collection)
		if collection == "" || seen[collection] || !connectors.SupportsCollection(descriptor, collection) {
			continue
		}
		seen[collection] = true
		result = append(result, collection)
	}
	return result
}

func collectionSlug(collection string) string {
	return strings.ReplaceAll(strings.ToLower(collection), "_", "-")
}

func normalizedAuthMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "none"
	}
	return mode
}

// syncSourceJSON is the administrative view of a connection. Credential
// references, token URLs and certificate paths are never echoed; only whether
// they are configured.
func syncSourceJSON(source SyncSource) map[string]any {
	return map[string]any{
		"connection_id": source.ConnectionID, "group_id": source.GroupID, "connector": source.Connector,
		"base_url": source.BaseURL, "collection": source.Collection,
		"scope": source.Scope, "generation": source.Generation,
		"auth_mode":             normalizedAuthMode(source.AuthMode),
		"credential_configured": source.CredentialRef != "",
		"ca_configured":         source.CAFile != "",
		"mtls_configured":       source.ClientCertFile != "" && source.ClientKeyFile != "",
		"page_size":             source.PageSize, "max_pages": source.MaxPages,
		"max_records": source.MaxRecords, "interval_minutes": source.Interval,
		"enabled": source.Enabled,
	}
}

// safeConfigurationError keeps a bounded, single-line message; validation
// errors never contain credential values.
func safeConfigurationError(err error) string {
	text := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, err.Error())
	runes := []rune(text)
	if len(runes) > 160 {
		runes = runes[:160]
	}
	return string(runes)
}

func generatedConnectionID(connector string) string {
	return generatedConnectionIDFor(connector, 0)
}

func generatedConnectionIDFor(connector string, reserve int) string {
	base := strings.ToLower(connector)
	base = strings.NewReplacer("_", "-", ".", "-").Replace(base)
	for strings.Contains(base, "--") {
		base = strings.ReplaceAll(base, "--", "-")
	}
	base = strings.Trim(base, "-")
	limit := 46 - reserve
	if limit < 12 {
		limit = 12
	}
	if len(base) > limit {
		base = strings.Trim(base[:limit], "-")
	}
	if !generatedConnection.MatchString(base) {
		base = "connection"
	}
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return base
	}
	return base + "-" + hex.EncodeToString(suffix)
}
