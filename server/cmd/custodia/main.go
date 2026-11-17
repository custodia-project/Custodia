// Custodia serves local accounts, verified Ozal identities or an explicit
// synthetic laboratory. The authentication mode is always chosen explicitly.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"custodia/server/internal/gateway"
	"custodia/server/internal/lab"
	"custodia/server/internal/localauth"
	"custodia/server/internal/probe"
	"custodia/server/internal/secrets"
	"custodia/server/internal/sqlitebackup"
	"custodia/server/internal/updater"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded *exitCodeError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}

type repeatedFlag []string

func (values *repeatedFlag) String() string { return strings.Join(*values, ",") }
func (values *repeatedFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func validateRestoredDatabase(path string) error {
	store, err := lab.Open(path)
	if err != nil {
		return fmt.Errorf("restored database is not usable: %w", err)
	}
	defer store.DB.Close()
	if err = store.CheckSchema(context.Background()); err != nil {
		return fmt.Errorf("restored database schema: %w", err)
	}
	return nil
}

// readPassword takes the first line of a private regular file, or of a
// non-terminal standard input. A password is never a command-line argument.
func readPassword(path string, stdin *os.File) (string, error) {
	var source io.Reader = stdin
	if path != "" {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("--password-file must be a regular file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("--password-file must not be readable by group or others")
		}
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("cannot open --password-file")
		}
		defer file.Close()
		source = file
	} else if info, err := stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("pipe the password on standard input or use --password-file; terminal input would echo it")
	}
	line, err := bufio.NewReader(io.LimitReader(source, 4097)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("cannot read password")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) > 4096 || line == "" {
		return "", fmt.Errorf("password missing or too long")
	}
	return line, nil
}

func openInstallation(dir string) (*lab.Store, error) {
	path := filepath.Join(dir, "state.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	s, err := lab.Open(path)
	if err != nil {
		return nil, err
	}
	if err = s.CheckSchema(context.Background()); err != nil {
		s.DB.Close()
		return nil, err
	}
	return s, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(err))
	}
}

var version = "dev"

// releasePublicKey is embedded by the release pipeline and used by privileged
// update helpers to pin the signing key. It is public material only.
var releasePublicKey = ""

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: custodia version|init|migrate|bootstrap-admin|create-admin|reset-password|serve|backup|restore|init-lab|migrate-lab|issue-lab|revoke-lab|add-http-lab|grant-sync-lab|serve-lab|probe-pam --dir PATH")
	}
	f := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	dir := f.String("dir", "", "private Custodia data directory")
	webDir := f.String("web-dir", "", "compiled real frontend directory (optional)")
	gatewayConfig := f.String("gateway-config", "", "Ozal assertion configuration (public key references only)")
	authMode := f.String("auth-mode", "", "serve authentication mode: local, oidc or ozal (required, no default)")
	publicOrigin := f.String("public-origin", "", "local/oidc public origin (HTTPS, or numeric HTTP loopback for tests)")
	masterKey := f.String("master-key", "", "master key file; default <dir>/master.key")
	tlsCert := f.String("tls-cert", "", "TLS certificate path for direct HTTPS")
	tlsKey := f.String("tls-key", "", "TLS private key path for direct HTTPS")
	updatePackage := f.String("package", "", "signed release archive to apply")
	updateSHA := f.String("sha256", "", "release archive SHA-256")
	updateSignature := f.String("signature", "", "signed manifest file")
	updateSigners := f.String("allowed-signers", "", "OpenSSH allowed_signers file for the release key")
	updateBinary := f.String("binary", "", "installed binary path to replace")
	updateVersion := f.String("update-version", "", "version expected in the release manifest")
	updateService := f.String("service", "custodia.service", "systemd service to restart")
	updateHealth := f.String("health-url", "", "health URL checked after the update")
	updateHealthCA := f.String("health-ca", "", "CA for the health URL")
	updateHealthName := f.String("health-server-name", "", "TLS name expected on the health certificate (the public host)")
	updateNoSystemd := f.Bool("no-systemd", false, "test mode: do not call systemctl")
	updateManifest := f.String("update-manifest", "", "release manifest path or HTTPS URL shown in the console")
	updateRequest := f.String("request", "", "console update request to apply")
	username := f.String("username", "", "local account username")
	displayName := f.String("display-name", "", "local account display name")
	passwordFile := f.String("password-file", "", "private file whose first line is the password (default: standard input)")
	mustChange := f.Bool("must-change", false, "force a password change at the first sign-in")
	subject := f.String("subject", "", "opaque subject (synthetic only for laboratory commands)")
	connections := f.String("connections", "", "comma-separated allowed connection IDs")
	output := f.String("out", "", "new private credential file or backup destination")
	from := f.String("from", "", "backup file to restore")
	addr := f.String("listen", "127.0.0.1:8000", "numeric loopback address")
	connectionID := f.String("connection", "", "laboratory sync connection ID")
	connector := f.String("connector", "http-inventory-v1", "read-only connector: http-inventory-v1 or cyberark-pam-safes-v1")
	baseURL := f.String("url", "", "numeric-loopback HTTP source or authorized probe HTTPS base URL")
	collection := f.String("collection", "", "normalized collection: users, devices, safes, safe_members, accounts, platforms, psm_servers, psm_connectors or platform_psm_links")
	scope := f.String("scope", "", "explicit extraction scope for baseline comparison")
	generation := f.String("generation", "", "data contract generation; defaults to the connector name")
	pageSize := f.Int("page-size", 100, "bounded page size")
	maxPages := f.Int("max-pages", 100, "bounded page count")
	maxRecords := f.Int("max-records", 10000, "bounded record count")
	intervalMinutes := f.Int("interval-minutes", 0, "automatic schedule in minutes; 0 disables it")
	grants := f.String("grant", "", "comma-separated laboratory subjects with read/run access")
	canRun := f.Bool("can-run", true, "grant the explicit sync run capability")
	probeAuthorized := f.Bool("authorized", false, "explicit owner authorization for this exact probe tenant")
	probeAuth := f.String("auth", "bearer", "probe auth mode: bearer, basic-exchange, oauth-client-credentials or mtls")
	probeAuthUser := f.String("auth-user", "", "basic-exchange API user (read-only)")
	credentialRef := f.String("credential-ref", "", "bearer credential reference: env:NAME, file:ABSOLUTE_PATH or secret:NAME")
	probeTokenURL := f.String("token-url", "", "OAuth token endpoint URL")
	probeClientIDRef := f.String("client-id-ref", "", "OAuth client ID reference")
	probeClientSecretRef := f.String("client-secret-ref", "", "OAuth client secret reference")
	probeClientCert := f.String("client-cert-file", "", "absolute client certificate path for mTLS")
	probeClientKey := f.String("client-key-file", "", "absolute client private key path for mTLS")
	probeScopes := repeatedFlag{}
	f.Var(&probeScopes, "oauth-scope", "OAuth scope (repeatable)")
	caFile := f.String("ca-file", "", "optional local CA bundle for the probe")
	probeTimeout := f.Duration("timeout", 15*time.Second, "probe timeout per endpoint")
	probeEndpoints := repeatedFlag{}
	f.Var(&probeEndpoints, "endpoint", "read-only probe endpoint (repeatable)")
	if err := f.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *dir == "" && os.Args[1] != "probe-pam" && os.Args[1] != "version" {
		return fmt.Errorf("--dir is required")
	}
	if strings.HasSuffix(os.Args[1], "-lab") && (*gatewayConfig != "" || *authMode != "" || *publicOrigin != "") {
		return fmt.Errorf("laboratory commands cannot use --gateway-config, --auth-mode or --public-origin")
	}
	switch os.Args[1] {
	case "probe-pam":
		plan, err := probe.NewPlan(probe.Config{
			BaseURL: *baseURL, AuthMode: *probeAuth, AuthUser: *probeAuthUser, CredentialRef: *credentialRef,
			TokenURL: *probeTokenURL, ClientIDRef: *probeClientIDRef, ClientSecretRef: *probeClientSecretRef,
			Scopes: []string(probeScopes), ClientCertFile: *probeClientCert, ClientKeyFile: *probeClientKey,
			CAFile: *caFile, Endpoints: []string(probeEndpoints), Authorized: *probeAuthorized, Timeout: *probeTimeout,
		})
		if err != nil {
			return &exitCodeError{code: 2, err: err}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		report, err := probe.RunPlan(ctx, plan)
		if err != nil {
			return &exitCodeError{code: 2, err: err}
		}
		encoded, err := report.SanitizedJSON()
		if err != nil {
			return &exitCodeError{code: 2, err: err}
		}
		fmt.Println(string(encoded))
		if failed := report.Failed(); failed > 0 {
			return &exitCodeError{code: 1, err: fmt.Errorf("%d probe endpoint(s) failed", failed)}
		}
		return nil
	case "backup":
		if *output == "" {
			return fmt.Errorf("--out is required")
		}
		source, err := filepath.Abs(filepath.Join(*dir, "state.db"))
		if err != nil {
			return err
		}
		if _, err = os.Stat(source); err != nil {
			return err
		}
		destination, err := filepath.Abs(*output)
		if err != nil {
			return err
		}
		if err = sqlitebackup.Backup(context.Background(), source, destination); err != nil {
			return err
		}
		fmt.Println("Backup written:", destination)
		return nil
	case "restore":
		if *from == "" {
			return fmt.Errorf("--from is required")
		}
		info, err := os.Stat(*dir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("restore directory must already exist")
		}
		directory, err := filepath.Abs(*dir)
		if err != nil {
			return err
		}
		source, err := filepath.Abs(*from)
		if err != nil {
			return err
		}
		destination := filepath.Join(directory, "state.db")
		if err = sqlitebackup.Restore(context.Background(), source, destination); err != nil {
			return err
		}
		if err = validateRestoredDatabase(destination); err != nil {
			os.Remove(destination)
			os.Remove(destination + "-wal")
			os.Remove(destination + "-shm")
			return err
		}
		fmt.Println("Backup restored:", destination)
		return nil
	case "init", "init-lab":
		if err := os.Mkdir(*dir, 0700); err != nil {
			return fmt.Errorf("refusing existing or inaccessible directory: %w", err)
		}
		file, err := os.OpenFile(filepath.Join(*dir, "state.db"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		file.Close()
		s, err := lab.Open(filepath.Join(*dir, "state.db"))
		if err != nil {
			return err
		}
		defer s.DB.Close()
		if err = s.Migrate(context.Background()); err != nil {
			return err
		}
		if _, err = secrets.Generate(secrets.PathFor(*dir)); err != nil {
			return fmt.Errorf("master key: %w", err)
		}
		if os.Args[1] == "init" {
			fmt.Println("Initialized empty Custodia database. Bootstrap a verified administrator explicitly before serving.")
			return nil
		}
		if err = s.Seed(context.Background(), time.Now()); err != nil {
			return err
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(*dir, "lab.token"), []byte(hex.EncodeToString(key)), 0600); err != nil {
			return err
		}
		fmt.Println("Synthetic lab initialized. Token is in lab.token; no external tools are connected.")
		return nil
	case "issue-lab", "revoke-lab":
		if _, err := os.Stat(filepath.Join(*dir, "lab.token")); err != nil {
			return fmt.Errorf("lab directory required")
		}
		path := filepath.Join(*dir, "state.db")
		if _, err := os.Stat(path); err != nil {
			return err
		}
		s, err := lab.Open(path)
		if err != nil {
			return err
		}
		defer s.DB.Close()
		if _, err = s.Load(context.Background()); err != nil {
			return err
		}
		if os.Args[1] == "revoke-lab" {
			return s.RevokePrincipal(context.Background(), *subject)
		}
		if *output == "" {
			return fmt.Errorf("--out is required")
		}
		file, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		success := false
		defer func() {
			file.Close()
			if !success {
				os.Remove(*output)
			}
		}()
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		value := hex.EncodeToString(key)
		if _, err = file.WriteString(value); err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			return err
		}
		scope := []string{}
		if *connections != "" {
			scope = strings.Split(*connections, ",")
		}
		if err = s.IssuePrincipal(context.Background(), *subject, value, scope); err != nil {
			return err
		}
		success = true
		fmt.Println("Laboratory credential written to", *output)
		return nil
	case "add-http-lab":
		if _, err := os.Stat(filepath.Join(*dir, "lab.token")); err != nil {
			return fmt.Errorf("lab directory required")
		}
		if *connectionID == "" || *baseURL == "" || *collection == "" || *scope == "" {
			return fmt.Errorf("--connection, --url, --collection and --scope are required")
		}
		path := filepath.Join(*dir, "state.db")
		if _, err := os.Stat(path); err != nil {
			return err
		}
		s, err := lab.Open(path)
		if err != nil {
			return err
		}
		defer s.DB.Close()
		if _, err = s.Load(context.Background()); err != nil {
			return err
		}
		source := lab.SyncSource{
			ConnectionID: *connectionID, Connector: *connector, BaseURL: *baseURL,
			Collection: *collection, Scope: *scope, Generation: *generation,
			PageSize: *pageSize, MaxPages: *maxPages, MaxRecords: *maxRecords, Interval: *intervalMinutes,
		}
		if source.Generation == "" {
			source.Generation = source.Connector
		}
		grantList := []string{}
		if *grants != "" {
			grantList = strings.Split(*grants, ",")
		}
		if err = s.AddHTTPSource(context.Background(), "host", source, grantList, time.Now()); err != nil {
			return err
		}
		fmt.Println("Laboratory HTTP source added:", *connectionID)
		return nil
	case "grant-sync-lab":
		if _, err := os.Stat(filepath.Join(*dir, "lab.token")); err != nil {
			return fmt.Errorf("lab directory required")
		}
		if *connectionID == "" || *subject == "" {
			return fmt.Errorf("--connection and --subject are required")
		}
		path := filepath.Join(*dir, "state.db")
		if _, err := os.Stat(path); err != nil {
			return err
		}
		s, err := lab.Open(path)
		if err != nil {
			return err
		}
		defer s.DB.Close()
		if _, err = s.Load(context.Background()); err != nil {
			return err
		}
		if err = s.GrantSync(context.Background(), "host", "custodia-lab", *subject, *connectionID, *canRun, time.Now()); err != nil {
			return err
		}
		fmt.Println("Laboratory sync grant written:", *connectionID, *subject)
		return nil
	case "migrate", "migrate-lab":
		if os.Args[1] == "migrate-lab" {
			if _, err := os.Stat(filepath.Join(*dir, "lab.token")); err != nil {
				return fmt.Errorf("lab directory required")
			}
		}
		path := filepath.Join(*dir, "state.db")
		if _, err := os.Stat(path); err != nil {
			return err
		}
		s, err := lab.Open(path)
		if err != nil {
			return err
		}
		defer s.DB.Close()
		return s.Migrate(context.Background())
	case "create-admin", "reset-password":
		if *gatewayConfig != "" || *authMode != "" {
			return fmt.Errorf("local account commands do not take --gateway-config or --auth-mode")
		}
		normalized, ok := lab.NormalizeLocalUsername(*username)
		if !ok {
			return fmt.Errorf("--username must be 1-128 characters of a-z 0-9 . _ @ -")
		}
		if os.Args[1] == "create-admin" && strings.TrimSpace(*displayName) == "" {
			return fmt.Errorf("--display-name is required")
		}
		s, err := openInstallation(*dir)
		if err != nil {
			return err
		}
		defer s.DB.Close()
		password, err := readPassword(*passwordFile, os.Stdin)
		if err != nil {
			return err
		}
		if err = localauth.CheckPolicy(normalized, password); err != nil {
			return fmt.Errorf("password must have %d-%d characters and differ from the username", localauth.MinPasswordLength, localauth.MaxPasswordLength)
		}
		hash, err := localauth.HashPassword(context.Background(), password)
		if err != nil {
			return err
		}
		if os.Args[1] == "reset-password" {
			if err = s.ResetLocalPassword(context.Background(), normalized, hash, time.Now()); err != nil {
				return fmt.Errorf("cannot reset password for that local account")
			}
			fmt.Println("Temporary password set; sessions revoked. The user must change it at next sign-in.")
			return nil
		}
		scope := []string{}
		if *connections != "" {
			scope = strings.Split(*connections, ",")
		}
		if err = s.CreateLocalAdministrator(context.Background(), normalized, *displayName, hash, scope, *mustChange, time.Now()); err != nil {
			if errors.Is(err, lab.ErrLocalBootstrap) {
				return err
			}
			return fmt.Errorf("cannot create local administrator: check username and connections")
		}
		fmt.Println("Local administrator created:", normalized)
		return nil
	case "bootstrap-admin":
		config, err := gateway.LoadConfig(*gatewayConfig)
		if err != nil {
			return err
		}
		path := filepath.Join(*dir, "state.db")
		if _, err = os.Stat(path); err != nil {
			return err
		}
		s, err := lab.Open(path)
		if err != nil {
			return err
		}
		defer s.DB.Close()
		if err = s.CheckSchema(context.Background()); err != nil {
			return err
		}
		scope := []string{}
		if *connections != "" {
			scope = strings.Split(*connections, ",")
		}
		return s.BootstrapAdministrator(context.Background(), config.Issuer, *subject, scope)
	case "update":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if *updateRequest != "" {
			if err := updater.ApplyRequest(ctx, *updateRequest, updater.Options{
				DataDir: *dir, Binary: *updateBinary, WebDir: *webDir, HealthURL: *updateHealth,
				HealthCA: *updateHealthCA, HealthServerName: *updateHealthName, Service: *updateService, NoSystemd: *updateNoSystemd,
				AllowedSigners: *updateSigners, CurrentVersion: version,
			}); err != nil {
				return err
			}
			fmt.Println("Console update applied")
			return nil
		}
		if err := updater.Apply(ctx, updater.Options{
			DataDir: *dir, Binary: *updateBinary, WebDir: *webDir, Package: *updatePackage,
			SHA256: *updateSHA, Signature: *updateSignature, AllowedSigners: *updateSigners,
			Version: *updateVersion, CurrentVersion: version, HealthURL: *updateHealth, HealthCA: *updateHealthCA, HealthServerName: *updateHealthName,
			Service: *updateService, NoSystemd: *updateNoSystemd,
		}); err != nil {
			return err
		}
		fmt.Println("Updated to", *updateVersion)
		return nil
	case "version":
		fmt.Println(version)
		return nil
	case "serve", "serve-lab":
		host, _, err := net.SplitHostPort(*addr)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if (*tlsCert == "") != (*tlsKey == "") {
			return fmt.Errorf("--tls-cert and --tls-key must be provided together")
		}
		if ip == nil || (!ip.IsLoopback() && *tlsCert == "") {
			return fmt.Errorf("Custodia backend requires a numeric loopback address unless direct TLS is configured")
		}
		path := filepath.Join(*dir, "state.db")
		if _, err = os.Stat(path); err != nil {
			return err
		}
		s, err := lab.Open(path)
		if err != nil {
			return err
		}
		defer s.DB.Close()
		if err = s.CheckSchema(context.Background()); err != nil {
			return err
		}
		keyPath := *masterKey
		if keyPath == "" {
			keyPath = secrets.PathFor(*dir)
		}
		if key, keyErr := secrets.Load(keyPath); keyErr == nil {
			s.SetMasterKey(key)
		} else if errors.Is(keyErr, secrets.ErrKeyMissing) {
			fmt.Println("No master key found: encrypted connection secrets are unavailable. Run init or provide --master-key.")
		} else {
			return fmt.Errorf("master key: %w", keyErr)
		}
		var handler http.Handler
		if os.Args[1] == "serve-lab" {
			token, err := os.ReadFile(filepath.Join(*dir, "lab.token"))
			if err != nil || len(token) != 64 {
				return fmt.Errorf("valid lab token file required")
			}
			if _, err = s.Load(context.Background()); err != nil {
				return err
			}
			handler = s.Handler(string(token), time.Now)
			if *webDir != "" {
				handler, err = lab.WithWeb(handler, *webDir)
				if err != nil {
					return err
				}
			}
		} else {
			var files http.Handler
			if *webDir != "" {
				files, err = lab.WithWeb(http.NotFoundHandler(), *webDir)
				if err != nil {
					return err
				}
			}
			// The mode is explicit; one never falls back to the other.
			switch *authMode {
			case "ozal":
				if *publicOrigin != "" {
					return fmt.Errorf("ozal mode takes public_origin from --gateway-config")
				}
				config, err := gateway.LoadConfig(*gatewayConfig)
				if err != nil {
					return err
				}
				config.Version = version
				handler, err = gateway.NewHandler(s, config, time.Now, files)
				if err != nil {
					return err
				}
			case "local":
				if *gatewayConfig != "" {
					return fmt.Errorf("local mode does not accept --gateway-config")
				}
				handler, err = localauth.NewHandler(s, localauth.Config{PublicOrigin: *publicOrigin, Version: version, DataDir: *dir, UpdateManifest: *updateManifest}, time.Now, files)
				if err != nil {
					return err
				}
				if exists, err := s.LocalAccountsExist(context.Background()); err == nil && !exists {
					fmt.Println("No local accounts yet: run create-admin on this host.")
				}
			case "oidc":
				if *gatewayConfig != "" {
					return fmt.Errorf("oidc mode does not accept --gateway-config")
				}
				handler, err = localauth.NewOIDCHandler(s, localauth.Config{PublicOrigin: *publicOrigin, Mode: "oidc", Version: version, DataDir: *dir, UpdateManifest: *updateManifest}, time.Now, files)
				if err != nil {
					return err
				}
				if exists, err := s.LocalAccountsExist(context.Background()); err == nil && !exists {
					fmt.Println("No local administrator yet: run create-admin on this host before enabling OIDC.")
				}
			default:
				return fmt.Errorf("serve requires --auth-mode local, --auth-mode oidc or --auth-mode ozal")
			}
		}
		srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		workerCtx, stopWorker := context.WithCancel(context.Background())
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			s.RunSyncWorker(workerCtx, 2*time.Second)
		}()
		defer func() {
			stopWorker()
			<-workerDone
		}()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			stopWorker()
			// Give an in-flight bounded sync time to write its terminal audit
			// before the HTTP server and SQLite close.
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Minute+30*time.Second)
			defer cancel()
			srv.Shutdown(shutdown)
		}()
		listener, err := net.Listen("tcp", *addr)
		if err != nil {
			return err
		}
		fmt.Println("Custodia "+os.Args[1]+" listening on", listener.Addr())
		if *tlsCert != "" {
			err = srv.ServeTLS(listener, *tlsCert, *tlsKey)
		} else {
			err = srv.Serve(listener)
		}
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unknown command")
	}
}
