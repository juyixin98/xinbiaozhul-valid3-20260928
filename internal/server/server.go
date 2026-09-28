// Package server wires the strict HTTP/1.1 connection machine to a
// TCP listener with per-connection deadlines.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"http11subset/internal/config"
	"http11subset/internal/httpx"
)

// Server listens and serves one Conn per accepted TCP connection.
type Server struct {
	cfg    *config.Config
	hdl    httpx.Handler
	logger httpx.Logger

	ln      net.Listener
	wg      sync.WaitGroup
	closed  atomic.Bool
	connSeq atomic.Uint64
}

// New constructs a server.
func New(cfg *config.Config, hdl httpx.Handler, logger httpx.Logger) *Server {
	return &Server{cfg: cfg, hdl: hdl, logger: logger}
}

// Addr returns the bound address after Start.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Start binds the listener.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.Listen, err)
	}
	s.ln = ln
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if s.closed.Load() {
				return
			}
			s.logger.ConnEvent("listener", "accept_error", map[string]any{"error": err.Error()})
			return
		}
		s.wg.Add(1)
		go s.serve(c)
	}
}

func (s *Server) serve(c net.Conn) {
	defer s.wg.Done()
	n := s.connSeq.Add(1)
	id := fmt.Sprintf("c%06d", n)
	// Deadlines: generous read deadline, reset per request by the
	// parser reads (SetReadDeadline applies to the whole conn). For
	// this local subset a single idle timeout is enforced.
	_ = c.SetDeadline(time.Now().Add(time.Duration(s.cfg.IdleTimeoutMS) * time.Millisecond))
	cc := httpx.NewConn(id, &deadlineConn{Conn: c, cfg: s.cfg}, httpx.ServerConfig{
		Limits:            s.cfg.Limits(),
		Logger:            s.logger,
		Handler:           s.hdl,
		InitialBuffer:     8 * 1024,
		Enable100Continue: s.cfg.Continue100,
	})
	cc.Serve()
}

// Shutdown stops accepting and waits for in-flight connections.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closed.Store(true)
	if s.ln != nil {
		s.ln.Close()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// deadlineConn refreshes the read deadline before every Read so that a
// slow-but-alive peer is not killed mid-message, while a stalled peer
// still hits ReadTimeoutMS.
type deadlineConn struct {
	net.Conn
	cfg *config.Config
}

// Read refreshes the deadline and delegates.
func (d *deadlineConn) Read(p []byte) (int, error) {
	_ = d.Conn.SetReadDeadline(time.Now().Add(time.Duration(d.cfg.ReadTimeoutMS) * time.Millisecond))
	n, err := d.Conn.Read(p)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return n, err
		}
	}
	return n, err
}

// Write applies the write deadline and delegates.
func (d *deadlineConn) Write(p []byte) (int, error) {
	_ = d.Conn.SetWriteDeadline(time.Now().Add(time.Duration(d.cfg.WriteTimeoutMS) * time.Millisecond))
	return d.Conn.Write(p)
}
