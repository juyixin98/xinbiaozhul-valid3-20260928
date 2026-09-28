// Package broker implements the MQTT 3.1.1 subset broker: TCP listener,
// per-connection state machine, subscription routing, retained messages,
// durable sessions with unacknowledged-QoS1 persistence and retransmission.
//
// Layering:
//
//   - packet:  wire encode/decode and strict well-formedness checks
//   - topics:  filter validation and wildcard matching
//   - store:   SQLite persistence boundary (sessions/subs/inflight/retained)
//   - broker:  connection state machine and business semantics (this package)
//
// Error contract: protocol violations that MQTT mandates to close the
// connection for are translated into connError; a CONNECT-specific rejection
// is answered with the proper CONNACK code first. packet.New errors carry a
// Category (invalid/unsupported/limit/state/internal) which the state machine
// maps onto these outcomes and structured log dispositions.
package broker

import (
	"context"
	"errors"
	"net"
	"sync"

	"mqttd/internal/logx"
	"mqttd/internal/store"
)

// Broker is the server. Create with New, run with Serve, stop with Close.
type Broker struct {
	cfg Config
	st  *store.Store
	log *logx.Logger
	mtx *metricsAndIndex

	listener net.Listener
	wg       sync.WaitGroup

	closeOnce sync.Once
	closed    chan struct{}
}

// mtx bundles shared mutable state guarded by mu.
type metricsAndIndex struct {
	mu      sync.Mutex
	index   *trie
	clients map[string]*client // connected clients by client id
	metrics Metrics
}

// New constructs a broker and opens the store.
func New(cfg Config, log *logx.Logger) (*Broker, error) {
	st, err := store.New(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	b := &Broker{
		cfg:    cfg,
		st:     st,
		log:    log,
		mtx:    &metricsAndIndex{index: newTrie(), clients: map[string]*client{}},
		closed: make(chan struct{}),
	}
	if err := b.rebuildIndex(); err != nil {
		st.Close()
		return nil, err
	}
	if n, err := st.CountRetained(); err == nil {
		b.mtx.metrics.RetainedCurrent.Store(int64(n))
	}
	if n, err := st.CountInflightTotal(); err == nil {
		b.mtx.metrics.InflightCurrent.Store(int64(n))
	}
	return b, nil
}

// rebuildIndex loads all persisted subscriptions into the in-memory trie.
func (b *Broker) rebuildIndex() error {
	subs, err := b.st.AllSubscriptions()
	if err != nil {
		return err
	}
	var n int64
	for _, s := range subs {
		b.mtx.index.add(splitLevels(s.Filter), s.ClientID, s.QoS)
		n++
	}
	b.mtx.metrics.SubscriptionsCurrent.Store(n)
	return nil
}

// Store exposes the persistence layer (used by tests/diagnostics).
func (b *Broker) Store() *store.Store { return b.st }

// Metrics returns a point-in-time snapshot.
func (b *Broker) Metrics() Snapshot { return b.mtx.metrics.snapshot() }

// Serve starts accepting connections. It blocks until Close.
func (b *Broker) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", b.cfg.Listen)
	if err != nil {
		return err
	}
	b.listener = ln
	b.log.Event("startup", logx.DispositionAccept,
		"broker listening",
		"version", logx.Version, "addr", b.cfg.Listen, "db", b.cfg.DBPath,
		"max_packet", b.cfg.MaxPacketBytes,
		"max_inflight", b.cfg.MaxInflightPerClient,
		"max_subs", b.cfg.MaxSubscriptionsPerClient,
		"retransmit_ms", b.cfg.RetransmitInterval.Milliseconds())
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) || isClosedChan(b.closed) {
					return
				}
				b.log.Event("accept", logx.DispositionInternalError, "accept failed", "err", err)
				continue
			}
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				b.serveConn(conn)
			}()
		}
	}()
	go func() {
		<-ctx.Done()
		b.Close()
	}()
	return nil
}

// Addr reports the bound address (useful when listener used port 0).
func (b *Broker) Addr() net.Addr {
	if b.listener == nil {
		return nil
	}
	return b.listener.Addr()
}

func isClosedChan(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// Close stops the listener, closes connected clients and the store.
func (b *Broker) Close() error {
	var firstErr error
	b.closeOnce.Do(func() {
		close(b.closed)
		if b.listener != nil {
			firstErr = b.listener.Close()
		}
		b.mtx.mu.Lock()
		cs := make([]*client, 0, len(b.mtx.clients))
		for _, c := range b.mtx.clients {
			cs = append(cs, c)
		}
		b.mtx.mu.Unlock()
		for _, c := range cs {
			c.shutdown(reasonServer)
		}
		b.wg.Wait()
		if err := b.st.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	})
	return firstErr
}
