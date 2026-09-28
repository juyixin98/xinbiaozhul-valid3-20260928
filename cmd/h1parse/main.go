// Command h1parse 启动 HTTP/1.1 子集服务。
//
// 用法（默认 127.0.0.1:8080，文件型 SQLite h1parse.db）：
//
//	go run ./cmd/h1parse
//
// 环境变量见 internal/config（H1PARSE_ADDR / H1PARSE_DB /
// H1PARSE_MAX_HEADER_BYTES / H1PARSE_MAX_BODY_BYTES /
// H1PARSE_MAX_CHUNK_BYTES / H1PARSE_IDLE_TIMEOUT / H1PARSE_LOG_JSON）。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"h1parse/internal/config"
	"h1parse/internal/handler"
	"h1parse/internal/logging"
	"h1parse/internal/server"
	"h1parse/internal/storage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "h1parse:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv(config.Default())
	if err != nil {
		return err
	}
	runID := newRunID()
	log := logging.Std(runID, cfg.LogJSON)
	log.Info("startup", "h1parse starting", logging.Fields{
		"version": Version, "run": runID, "addr": cfg.Addr, "db": cfg.DB,
	})

	store, err := storage.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer store.Close()

	h := &handler.Handler{
		Store:  store,
		Log:    log,
		MaxBuf: cfg.Limits.MaxBodyBytes,
	}
	srv := &server.Server{
		Addr:    cfg.Addr,
		Limits:  cfg.Limits,
		Handler: h.Serve,
		Log:     log,
		IdleTO:  cfg.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		log.Info("shutdown", "signal received, shutting down", logging.Fields{
			"signal": sig.String(), "timeout": cfg.ShutdownTO.String()})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTO)
		defer cancel()
		_ = srv.Shutdown(ctx)
		select {
		case <-errCh:
		case <-ctx.Done():
		}
		time.Sleep(50 * time.Millisecond) // 让在途连接日志落盘
		return nil
	}
}

// Version 是题目相关的版本标识，写入启动日志与测试断言。
const Version = "h1parse-1.0.0"

func newRunID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "run-" + hex.EncodeToString(b[:])
}
