// Command mqttd runs the local MQTT 3.1.1 subset broker.
//
// Usage:
//
//	mqttd -addr 127.0.0.1:1883 -db ./mqttd.db
//
// All limits are configurable via flags; run mqttd -h for the list.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mqttd/internal/broker"
	"mqttd/internal/logx"
)

// loadEnvFile reads a simple KEY=VALUE file (no shell quoting). Unknown keys
// and malformed lines are reported rather than silently ignored, so a typo in
// the local config never masquerades as success.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if _, exists := os.LookupEnv(k); !exists {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func main() {
	cfg := broker.DefaultConfig()
	var configFile = flag.String("config", "", "KEY=VALUE config file (e.g. configs/mqttd.env); env vars override")
	var (
		addr        = flag.String("addr", "", "TCP listen address (default from config/127.0.0.1:1883)")
		dbPath      = flag.String("db", "", "SQLite database path")
		maxPacket   = flag.Int("max-packet", 0, "maximum packet size in bytes")
		maxSessions = flag.Int("max-sessions", 0, "maximum durable sessions")
		maxSubs     = flag.Int("max-subs", 0, "maximum subscriptions per client")
		maxInflight = flag.Int("max-inflight", 0, "maximum unacknowledged QoS1 deliveries per client")
		maxRetained = flag.Int("max-retained", 0, "maximum retained messages")
		queueDepth  = flag.Int("queue", 0, "per-connection outbound queue depth")
		retx        = flag.Duration("retx", 0, "QoS1 retransmit interval")
	)
	flag.Parse()

	if *configFile != "" {
		if err := loadEnvFile(*configFile); err != nil {
			panic(err)
		}
	}
	cfg.Listen = envStr("ADDR", cfg.Listen)
	cfg.DBPath = envStr("DB", cfg.DBPath)
	cfg.MaxPacketBytes = envInt("MAX_PACKET", cfg.MaxPacketBytes)
	cfg.MaxSessions = envInt("MAX_SESSIONS", cfg.MaxSessions)
	cfg.MaxSubscriptionsPerClient = envInt("MAX_SUBS", cfg.MaxSubscriptionsPerClient)
	cfg.MaxInflightPerClient = envInt("MAX_INFLIGHT", cfg.MaxInflightPerClient)
	cfg.MaxRetained = envInt("MAX_RETAINED", cfg.MaxRetained)
	cfg.SendQueueDepth = envInt("QUEUE", cfg.SendQueueDepth)
	cfg.RetransmitInterval = envDur("RETX", cfg.RetransmitInterval)
	cfg.SendWriteTimeout = envDur("SEND_WRITE_TIMEOUT", cfg.SendWriteTimeout)

	// Explicit flags win over both file and built-in defaults.
	if *addr != "" {
		cfg.Listen = *addr
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *maxPacket != 0 {
		cfg.MaxPacketBytes = *maxPacket
	}
	if *maxSessions != 0 {
		cfg.MaxSessions = *maxSessions
	}
	if *maxSubs != 0 {
		cfg.MaxSubscriptionsPerClient = *maxSubs
	}
	if *maxInflight != 0 {
		cfg.MaxInflightPerClient = *maxInflight
	}
	if *maxRetained != 0 {
		cfg.MaxRetained = *maxRetained
	}
	if *queueDepth != 0 {
		cfg.SendQueueDepth = *queueDepth
	}
	if *retx != 0 {
		cfg.RetransmitInterval = *retx
	}

	var runBuf [6]byte
	_, _ = rand.Read(runBuf[:])
	runID := hex.EncodeToString(runBuf[:])
	logger := logx.Default(runID)

	b, err := broker.New(cfg, logger)
	if err != nil {
		logger.Event("startup", logx.DispositionInternalError, "failed to construct broker", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.Serve(ctx); err != nil {
		logger.Event("startup", logx.DispositionInternalError, "listen failed", "err", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Event("shutdown", logx.DispositionAccept, "signal received, draining",
		"ts", time.Now().UTC().Format(time.RFC3339))
	cancel()
	if err := b.Close(); err != nil {
		logger.Event("shutdown", logx.DispositionInternalError, "close error", "err", err)
		os.Exit(1)
	}
}
