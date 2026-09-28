// Package server hosts the TCP listener and runs the per-connection MQTT
// state machine on top of the protocol codec and the broker business logic.
//
// Connection state machine (per network connection):
//
//	NEW ──CONNECT(ok)──► READY ──DISCONNECT──► CLOSED-CLEAN
//	 │                   │ │
//	 │ malformed/        │ ├─ PUBLISH/SUBSCRIBE/PUBACK/PINGREQ handled
//	 │ policy reject     │ ├─ protocol violation / unsupported ─► CLOSED-ERROR (Will published)
//	 ▼                   │ ├─ keep-alive timeout ─► CLOSED-KEEPALIVE (Will published)
//	CLOSED              │ └─ EOF / network failure ─► CLOSED-ERROR (Will published)
//	                    └─ take-over by same ClientID ─► CLOSED-TAKEOVER (no Will)
//
// Before CONNECT only one packet is accepted: CONNECT itself; any other
// packet is a state conflict and closes the transport.
package server

import (
	"context"
	"errors"
	"net"
	"sync"

	"mqttlocal/internal/broker"
	"mqttlocal/internal/config"
	"mqttlocal/internal/diag"
)

// Server owns the listener and accepts connections.
type Server struct {
	cfg config.Config
	br  *broker.Broker
	log *diag.Logger

	mu     sync.Mutex
	ln     net.Listener
	closed bool
	wg     sync.WaitGroup
	conns  map[*connState]struct{}
}

// New creates a server (Serve binds the socket).
func New(cfg config.Config, br *broker.Broker, log *diag.Logger) *Server {
	if log == nil {
		log = diag.Discard()
	}
	return &Server{cfg: cfg, br: br, log: log, conns: map[*connState]struct{}{}}
}

// Addr returns the bound address (nil before Serve).
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Serve binds and accepts until ctx is canceled or Close is called.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.log.Event("server.listen", 0, "addr", ln.Addr().String())

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		raw, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				s.wg.Wait()
				return nil
			}
			s.log.Failure("server.accept", 0, err)
			continue
		}
		connID := s.log.NextConnID()
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			cs := newConnState(s.cfg, s.br, s.log, c, connID)
			s.registerConn(cs)
			defer s.unregisterConn(cs)
			cs.run(ctx)
		}(raw)
	}
}

func (s *Server) registerConn(cs *connState) {
	s.mu.Lock()
	s.conns[cs] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) unregisterConn(cs *connState) {
	s.mu.Lock()
	delete(s.conns, cs)
	s.mu.Unlock()
} // Close stops accepting and detaches sessions without publishing Wills. It
// closes every live transport so blocked readers unblock and Serve returns.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	ln := s.ln
	snapshot := make([]*connState, 0, len(s.conns))
	for cs := range s.conns {
		snapshot = append(snapshot, cs)
	}
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	n := s.br.CloseAll()
	for _, cs := range snapshot {
		cs.requestAsyncClose(broker.NewCloseError(broker.ReasonServerShutdown, "server closing"))
	}
	s.log.Event("server.shutdown", 0, "detached", n)
}
