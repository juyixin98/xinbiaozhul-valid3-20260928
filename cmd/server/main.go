// Command http11subset runs a local HTTP/1.1 backend that parses
// requests with a strict, self-contained framing implementation
// (persistent connections, Content-Length and chunked bodies) and
// stores JSON records in a local SQLite database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"http11subset/internal/app"
	"http11subset/internal/config"
	"http11subset/internal/httpx"
	"http11subset/internal/server"
	"http11subset/internal/storage"
)

func main() {
	cfgPath := flag.String("config", "configs/local.json", "path to JSON config (optional)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	if dir := filepath.Dir(cfg.SQLitePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir db dir: %v\n", err)
			os.Exit(2)
		}
	}
	store, err := storage.Open(cfg.SQLitePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage: %v\n", err)
		os.Exit(2)
	}
	defer store.Close()

	logger := httpx.NewJSONLogger(os.Stderr)
	hdl := app.New(store)
	srv := server.New(cfg, hdl, logger)
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
	logger.ConnEvent("main", "listening", map[string]any{
		"addr":        srv.Addr().String(),
		"sqlite":      cfg.SQLitePath,
		"max_header":  cfg.MaxHeaderBytes,
		"max_body":    cfg.MaxBodyBytes,
		"max_chunkln": cfg.MaxChunkLine,
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logger.ConnEvent("main", "shutting_down", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
