package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"rootlens/internal/hub"
	"rootlens/internal/protocol"
	"rootlens/internal/storage"
	"strings"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("rootlens "+command, flag.ContinueOnError)
	data := flags.String("data", "data", "Private local data directory")
	listen := flags.String("listen", "127.0.0.1:8443", "Listener address")
	origin := flags.String("origin", "https://localhost:8443", "Canonical browser origin")
	names := flags.String("tls-names", "localhost,127.0.0.1", "Certificate DNS names and IPs")
	destination := flags.String("output", "", "Backup destination or local agent configuration directory")
	hostName := flags.String("host-name", "Local host", "Local agent display name")
	docker := flags.Bool("docker", false, "Enable the restricted Docker proxy for the local agent")
	check := flags.Bool("check", false, "Check local agent enrollment and telemetry")
	finish := flags.Bool("finish", false, "Remove the consumed local enrollment token")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if command == "version" {
		fmt.Println(protocol.Version)
		return nil
	}
	if command == "local-agent" {
		if *destination == "" || (*check && *finish) {
			return fmt.Errorf("local-agent requires --output; --check and --finish are mutually exclusive")
		}
		return hub.LocalAgent(context.Background(), *data, *destination, *hostName, *docker, *check, *finish)
	}
	if command == "init" {
		u, e := url.Parse(*origin)
		if e != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("origin must be an HTTPS origin")
		}
		if e = hub.Initialize(*data, *origin, strings.Split(*names, ",")); e != nil {
			return e
		}
		fmt.Printf("Initialized %s. Trust ca.pem, then read setup.token locally to create the administrator. Keep all private files protected.\n", *data)
		return nil
	}
	if command != "serve" && command != "demo" && command != "backup" {
		return fmt.Errorf("usage: rootlens init|serve|demo|backup|local-agent|version")
	}
	if command == "demo" {
		host, _, e := net.SplitHostPort(*listen)
		if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("demo requires an explicit loopback address")
		}
		*origin = "http://" + *listen
	}
	dbName := "rootlens.db"
	if command == "demo" {
		dbName = "demo.db"
	}
	s, e := storage.Open(filepath.Join(*data, dbName))
	if e != nil {
		return e
	}
	defer s.Close()
	if command == "backup" {
		if *destination == "" {
			return fmt.Errorf("--output is required")
		}
		return s.Backup(context.Background(), *destination)
	}
	h := hub.New(s, *origin)
	if command == "demo" {
		h.Demo = true
		h.DevLoopback = true
		if e = hub.SeedDemo(context.Background(), s); e != nil {
			return e
		}
	} else {
		b, e := os.ReadFile(filepath.Join(*data, "hub.json"))
		if e != nil {
			return fmt.Errorf("run rootlens init before serving")
		}
		var config hub.Configuration
		if e = json.Unmarshal(b, &config); e != nil {
			return e
		}
		h.Origin = config.Origin
		h.BootstrapHash = config.BootstrapHash
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if h.Demo {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if e := hub.AdvanceDemo(ctx, s); e != nil {
						log.Print("demo_replay_failed")
					}
				}
			}
		}()
	}
	server := &http.Server{Addr: *listen, Handler: h.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		count := 0
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if !h.Demo {
					if e := s.Tick(ctx, now); e != nil {
						log.Print("diagnostic_maintenance_failed")
					}
				}
				count++
				if count%4 == 0 {
					if e := s.Cleanup(ctx, now); e != nil {
						log.Print("retention_maintenance_failed")
					}
					_ = s.Checkpoint(ctx)
				}
			}
		}
	}()
	log.Printf("RootLens %s listening on %s", protocol.Version, *listen)
	if command == "demo" {
		e = server.ListenAndServe()
	} else {
		e = server.ListenAndServeTLS(filepath.Join(*data, "server.pem"), filepath.Join(*data, "server.key"))
	}
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
