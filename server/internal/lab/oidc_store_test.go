package lab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func oidcStore(t *testing.T) (*Store, Principal, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "oidc-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateLocalAdministrator(ctx, "ada.admin", "Ada Admin", "hash-placeholder", []string{IdentityConnection}, false, now); err != nil {
		t.Fatal(err)
	}
	admin, err := s.ResolvePrincipal(ctx, LocalIssuer, "ada.admin")
	if err != nil {
		t.Fatal(err)
	}
	return s, admin, now
}

func oidcDraft() OIDCConfig {
	return OIDCConfig{
		Issuer: "https://idp.acme.example", ClientID: "custodia",
		RedirectURI: "https://custodia.acme.example/api/auth/oidc/callback",
		Scopes:      "openid profile email", SubjectClaim: "sub", EmailClaim: "email", NameClaim: "name",
	}
}

func TestOIDCMigrationIsIdempotentAndKeepsData(t *testing.T) {
	s, admin, now := oidcStore(t)
	ctx := context.Background()
	if err := s.SaveOIDCConfig(ctx, admin, oidcDraft(), now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOIDCPending(ctx, oidcDraft().Issuer, "subject-1", "one@acme.example", "One", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.DB.QueryRow("SELECT max(version) FROM lab_schema_migrations").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	config, err := s.GetOIDCConfig(ctx)
	if err != nil || config.Issuer != oidcDraft().Issuer {
		t.Fatal(config, err)
	}
	pending, err := s.ListOIDCPending(ctx, admin)
	if err != nil || len(pending) != 1 || pending[0].Subject != "subject-1" {
		t.Fatal(pending, err)
	}
}

func TestOIDCActivationRequiresAFreshTestOfTheCurrentConfig(t *testing.T) {
	s, admin, now := oidcStore(t)
	ctx := context.Background()
	if err := s.SaveOIDCConfig(ctx, admin, oidcDraft(), now); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableOIDC(ctx, admin, now); !errors.Is(err, ErrOIDCTestStale) {
		t.Fatal("activation without a test was accepted", err)
	}
	if err := s.RecordOIDCTest(ctx, admin, true, "ok", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableOIDC(ctx, admin, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	config, _ := s.GetOIDCConfig(ctx)
	if !config.Enabled {
		t.Fatal("provider was not enabled")
	}
	// A failed test never enables the provider.
	if err := s.DisableOIDC(ctx, admin, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOIDCTest(ctx, admin, false, "bad", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableOIDC(ctx, admin, now.Add(5*time.Second)); !errors.Is(err, ErrOIDCTestStale) {
		t.Fatal("a failed test enabled the provider", err)
	}
	// An old successful test cannot authorize a later change or activation.
	if err := s.RecordOIDCTest(ctx, admin, true, "ok", now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableOIDC(ctx, admin, now.Add(7*time.Second+OIDCTestValidity)); !errors.Is(err, ErrOIDCTestStale) {
		t.Fatal("stale test enabled the provider", err)
	}
	draft := oidcDraft()
	draft.ClientID = "changed"
	if err := s.SaveOIDCConfig(ctx, admin, draft, now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	// A test recorded before the change cannot authorize the new configuration.
	if err := s.RecordOIDCTest(ctx, admin, true, "ok", now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableOIDC(ctx, admin, now.Add(9*time.Second)); !errors.Is(err, ErrOIDCTestStale) {
		t.Fatal("test of a previous configuration enabled the new one", err)
	}
}

func TestOIDCPendingApprovalGrantsOnlyTheAdministratorScope(t *testing.T) {
	s, admin, now := oidcStore(t)
	ctx := context.Background()
	issuer := oidcDraft().Issuer
	if err := s.RecordOIDCPending(ctx, issuer, "subject-1", "one@acme.example", "One", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveOIDCPending(ctx, admin, issuer, "subject-1", []string{NetskopeConnection}, now); !errors.Is(err, ErrDenied) {
		t.Fatal("approval exceeded the administrator's grants", err)
	}
	if err := s.ApproveOIDCPending(ctx, admin, issuer, "subject-1", []string{IdentityConnection}, now); err != nil {
		t.Fatal(err)
	}
	principal, account, err := s.OIDCIdentity(ctx, issuer, "subject-1")
	if err != nil || !account.Enabled || account.DisplayName != "One" {
		t.Fatal(principal, account, err)
	}
	var grants int
	if err := s.DB.QueryRow("SELECT count(*) FROM lab_grants WHERE principal_id=?", principal.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal(grants, err)
	}
	// Sessions for an approved identity resolve only in its issuer.
	token := "oidc-session-token"
	if err := s.StartOIDCSession(ctx, principal, issuer, oidcTokenHash(token), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveOIDCSession(ctx, oidcTokenHash(token), 30*time.Minute, now.Add(time.Minute), issuer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveOIDCSession(ctx, oidcTokenHash(token), 30*time.Minute, now.Add(time.Minute), "https://other.example"); !errors.Is(err, ErrNoSession) {
		t.Fatal("session resolved under another issuer", err)
	}
	if err := s.SetOIDCUserEnabled(ctx, admin, issuer, "subject-1", false, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveOIDCSession(ctx, oidcTokenHash(token), 30*time.Minute, now.Add(3*time.Minute), issuer); !errors.Is(err, ErrNoSession) {
		t.Fatal("disabled account kept its session", err)
	}
	// Approval is audited without tokens and cannot be repeated.
	var audited string
	if err := s.DB.QueryRow("SELECT group_concat(after_json) FROM lab_audit WHERE action='approve_oidc_user'").Scan(&audited); err != nil || audited == "" {
		t.Fatal(audited, err)
	}
	if err := s.ApproveOIDCPending(ctx, admin, issuer, "subject-1", nil, now); !errors.Is(err, ErrOIDCPending) {
		t.Fatal("repeated approval succeeded", err)
	}
}

// oidcTokenHash mirrors localauth.TokenHash without importing it, keeping the
// lab tests independent from the HTTP package.
func oidcTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
