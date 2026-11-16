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

// ProbePlan builds the same validated, authenticated plan used by the probe
// CLI. Credential values are resolved only when the client is prepared.
func (source SyncSource) ProbePlan() (probe.Plan, error) {
	descriptor, found := connectors.Get(source.Connector)
	if !found {
		return probe.Plan{}, errors.New("unsupported connector")
	}
	if !connectors.Connectable(descriptor) {
		return probe.Plan{}, errors.New("connector is planned, not connectable")
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
		Endpoints:       connectors.ProbeEndpointsFor(descriptor, source.Collection),
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

// RegisterConnection is the onboarding path: an administrator names a
// connector, a tenant URL and credential references, and Custodia creates the
// connection without contacting the tenant. The creator receives read and run
// grants for that connection.
func (s *Store) RegisterConnection(ctx context.Context, actor string, principal Principal, source SyncSource, now time.Time) (SyncSource, error) {
	if !s.IsAdministrator(ctx, principal) {
		return SyncSource{}, ErrDenied
	}
	if source.ConnectionID == "" {
		source.ConnectionID = generatedConnectionID(source.Connector)
	}
	if source.Generation == "" {
		source.Generation = source.Connector
	}
	if source.Collection == "" {
		descriptor, found := connectors.Get(source.Connector)
		if !found || len(descriptor.Collections) == 0 {
			return SyncSource{}, errors.New("connector has no implemented collection")
		}
		source.Collection = descriptor.Collections[0]
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
	grants := []string{principal.Subject}
	if err := validateConnectionConfiguration(source, grants); err != nil {
		return SyncSource{}, err
	}
	source.CreatedBy = actor
	source.CreatedAt = now.UTC().Format(time.RFC3339)
	source.Enabled = true
	scopes, err := json.Marshal(source.Scopes)
	if err != nil {
		return SyncSource{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SyncSource{}, err
	}
	defer tx.Rollback()
	// kind stays "netskope" for external sources: it is the legacy label the
	// read models already understand. The connector ID is the authoritative
	// tool identity and is exposed by /api/connections.
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_connections(id,kind) VALUES(?,?)", source.ConnectionID, "netskope"); err != nil {
		return SyncSource{}, fmt.Errorf("connection ID already exists or is invalid")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO lab_sync_sources(connection_id,connector,base_url,collection,scope,generation,enabled,page_size,max_pages,max_records,interval_minutes,auth_mode,credential_ref,token_url,client_id_ref,client_secret_ref,scopes_json,ca_file,client_cert_file,client_key_file,created_by,created_at)
		VALUES(?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		source.ConnectionID, source.Connector, source.BaseURL, source.Collection, source.Scope, source.Generation,
		source.PageSize, source.MaxPages, source.MaxRecords, source.Interval,
		normalizedAuthMode(source.AuthMode), source.CredentialRef, source.TokenURL, source.ClientIDRef, source.ClientSecretRef,
		string(scopes), source.CAFile, source.ClientCertFile, source.ClientKeyFile, source.CreatedBy, source.CreatedAt); err != nil {
		return SyncSource{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_grants(principal_id,connection_id) VALUES(?,?)", principal.ID, source.ConnectionID); err != nil {
		return SyncSource{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_sync_grants(principal_id,connection_id,can_run) VALUES(?,?,1)", principal.ID, source.ConnectionID); err != nil {
		return SyncSource{}, err
	}
	after, _ := json.Marshal(map[string]any{
		"connection_id": source.ConnectionID, "connector": source.Connector, "base_url": source.BaseURL,
		"collection": source.Collection, "scope": source.Scope, "generation": source.Generation,
		"auth_mode": normalizedAuthMode(source.AuthMode), "credential_configured": source.CredentialRef != "",
		"ca_configured": source.CAFile != "", "mtls_configured": source.ClientCertFile != "",
	})
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'connection_register','{}',?,?)", actor, after, now.UTC().Format(time.RFC3339)); err != nil {
		return SyncSource{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'sync_grant','{}',?,?)", actor, after, now.UTC().Format(time.RFC3339)); err != nil {
		return SyncSource{}, err
	}
	if err = tx.Commit(); err != nil {
		return SyncSource{}, err
	}
	return source, nil
}

// ProbeConnection runs the read-only probe for one registered connection and
// records only a sanitized summary. It never persists observations and never
// queues a sync run.
func (s *Store) ProbeConnection(ctx context.Context, principal Principal, connectionID string, now time.Time) (probe.Report, error) {
	if !s.IsAdministrator(ctx, principal) {
		return probe.Report{}, ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return probe.Report{}, err
	}
	defer tx.Rollback()
	source, err := loadSyncSourceTx(ctx, tx, connectionID)
	if err != nil {
		return probe.Report{}, ErrSyncNotFound
	}
	if err = tx.Commit(); err != nil {
		return probe.Report{}, err
	}
	plan, err := source.ProbePlan()
	if err != nil {
		return probe.Report{}, err
	}
	report, err := probe.RunPlan(ctx, plan)
	if err != nil {
		return probe.Report{}, err
	}
	after, _ := json.Marshal(map[string]any{
		"connection_id": connectionID, "connector": source.Connector,
		"auth_mode": string(plan.AuthMode), "failed_endpoints": report.Failed(),
		"endpoints": report.Endpoints,
	})
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO lab_audit(actor,action,before_json,after_json,at) VALUES(?,'connection_probe','{}',?,?)", principal.Actor(), after, now.UTC().Format(time.RFC3339)); err != nil {
		return probe.Report{}, err
	}
	return report, nil
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
		"connection_id": source.ConnectionID, "connector": source.Connector,
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
	base := strings.ToLower(connector)
	base = strings.NewReplacer("_", "-", ".", "-").Replace(base)
	for strings.Contains(base, "--") {
		base = strings.ReplaceAll(base, "--", "-")
	}
	base = strings.Trim(base, "-")
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
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
