// Command mqttlocal runs the local MQTT 3.1.1 subset broker.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mqttlocal/internal/broker"
	"mqttlocal/internal/config"
	"mqttlocal/internal/diag"
	"mqttlocal/internal/server"
	"mqttlocal/internal/store"
)

// Version is the protocol/implementation identifier printed in logs.
const Version = "mqttlocal-0.1.0 (MQTT 3.1.1 subset: CONNECT/SUBSCRIBE/PUBLISH QoS0-1/PUBACK/PING)"

func main() {
	var (
		listen   = flag.String("listen", "", "TCP address (overrides config)")
		dbPath   = flag.String("db", "", "SQLite DSN/file (overrides config)")
		confPath = flag.String("config", "", "path to JSON config file")
		runID    = flag.String("run-id", "", "explicit run identifier (default: timestamp)")
	)
	flag.Parse()

	cfg := config.Default()
	if *confPath != "" {
		var err error
		cfg, err = config.LoadFile(*confPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
			os.Exit(2)
		}
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *dbPath != "" {
		cfg.Database = *dbPath
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		os.Exit(2)
	}

	rid := *runID
	if rid == "" {
		rid = time.Now().UTC().Format("run-20060102T150405Z")
	}
	log := diag.New(os.Stderr, rid, 0)
	log.Event("startup", 0, "version", Version, "listen", cfg.Listen,
		"db", cfg.Database, "max_connections", cfg.MaxConnections,
		"max_inflight", cfg.MaxInflightPerSession,
		"max_offline", cfg.MaxOfflinePerSession,
		"max_packet_bytes", cfg.MaxPacketBytes)

	st, err := store.Open(cfg.Database)
	if err != nil {
		log.Failure("startup.store_open", 0, err)
		os.Exit(1)
	}
	defer st.Close()

	br := broker.New(cfg, st, log)
	if err := br.RecoverDurableWills(); err != nil {
		log.Failure("startup.recover", 0, err)
		os.Exit(1)
	}

	srv := server.New(cfg, br, log)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err != nil {
			log.Failure("server.exit", 0, err)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Event("shutdown", 0, "signal", "received")
		srv.Close()
		<-errCh
	}
	br.Close()
	log.Event("stopped", 0, "stats", fmt.Sprintf("%+v", br.Stats()))
}
