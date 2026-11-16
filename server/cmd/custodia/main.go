// Custodia serves verified Ozal identities or an explicit synthetic laboratory.
package main

import (
	"context"
	"crypto/rand"
	"custodia/server/internal/gateway"
	"custodia/server/internal/lab"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: custodia init|migrate|bootstrap-admin|serve|init-lab|migrate-lab|issue-lab|revoke-lab|serve-lab --dir PATH")
	}
	f := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	dir := f.String("dir", "", "private Custodia data directory")
	webDir := f.String("web-dir", "", "compiled real frontend directory (optional)")
	gatewayConfig := f.String("gateway-config", "", "Ozal assertion configuration (public key references only)")
	subject := f.String("subject", "", "opaque subject (synthetic only for laboratory commands)")
	connections := f.String("connections", "", "comma-separated allowed connection IDs")
	output := f.String("out", "", "new private credential file")
	addr := f.String("listen", "127.0.0.1:8000", "numeric loopback address")
	if err := f.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("--dir is required")
	}
	if strings.HasSuffix(os.Args[1], "-lab") && *gatewayConfig != "" {
		return fmt.Errorf("laboratory commands cannot use --gateway-config")
	}
	switch os.Args[1] {
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
	case "serve", "serve-lab":
		host, _, err := net.SplitHostPort(*addr)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("Custodia backend requires a numeric loopback address")
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
			config, err := gateway.LoadConfig(*gatewayConfig)
			if err != nil {
				return err
			}
			var files http.Handler
			if *webDir != "" {
				files, err = lab.WithWeb(http.NotFoundHandler(), *webDir)
				if err != nil {
					return err
				}
			}
			handler, err = gateway.NewHandler(s, config, time.Now, files)
			if err != nil {
				return err
			}
		}
		srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			srv.Shutdown(shutdown)
		}()
		listener, err := net.Listen("tcp", *addr)
		if err != nil {
			return err
		}
		fmt.Println("Custodia "+os.Args[1]+" listening on", listener.Addr())
		err = srv.Serve(listener)
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unknown command")
	}
}
