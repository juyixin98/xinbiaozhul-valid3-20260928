package e2e

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"h1parse/internal/handler"
	"h1parse/internal/logging"
	"h1parse/internal/protocol"
	"h1parse/internal/server"
	"h1parse/internal/storage"
)

// testServer 是一条已启动的回环服务 + 其依赖。
type testServer struct {
	addr   string
	logBuf *lineBuffer
	dbPath string
	srv    *server.Server
}

func startServer(t *testing.T, lim protocol.Limits) *testServer {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	lb := newLineBuffer()
	log := logging.New(lb, "run-e2e-0001", false)
	h := &handler.Handler{Store: store, Log: log, MaxBuf: lim.MaxBodyBytes}
	srv := &server.Server{
		Addr:        "127.0.0.1:0",
		Limits:      lim,
		Handler:     h.Serve,
		Log:         log,
		IdleTO:      5 * time.Second,
		ReadySignal: make(chan struct{}),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()
	select {
	case <-srv.Ready():
	case err := <-serveErr:
		t.Fatalf("serve: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server never bound")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return &testServer{addr: srv.AddrActual(), logBuf: lb, dbPath: dbPath, srv: srv}
}

// dial 建立一条原始 TCP 连接（不使用 net/http，保证逐字节可控）。
func (s *testServer) dial(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", s.addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}
