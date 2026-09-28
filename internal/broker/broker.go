// Package broker implements the MQTT 3.1.1 subset business logic:
// sessions, subscriptions, wildcard matching, retained messages, QoS 1
// inflight tracking with durable redelivery, resource limits and Last Will.
//
// Threading model: one broker mu guards all session/subscription state.
// SQLite is accessed with a single connection from the pool. Each connection
// owns a goroutine that serializes writes to its transport (send queue);
// broker->client delivery is non-blocking against a bounded queue, and a
// full queue closes the slow subscriber (documented policy).
package broker

import (
	"sync"

	"mqttlocal/internal/config"
	"mqttlocal/internal/diag"
	"mqttlocal/internal/store"
)

// Stats is a point-in-time snapshot of broker counters.
type Stats struct {
	AcceptedConnects  int64
	RejectedConnects  int64
	ActiveConnections int64
	Subscriptions     int64
	ReceivedQos0      int64
	ReceivedQos1      int64
	SentQos0          int64
	SentQos1          int64
	PubacksReceived   int64
	Redeliveries      int64
	RetainedCount     int64
	DroppedOffline    int64
	ClosedProtocol    int64
	ClosedResource    int64
	ClosedKeepAlive   int64
	ClosedNetwork     int64
	TakenOver         int64
}

// Broker coordinates all connected clients and durable state.
type Broker struct {
	cfg config.Config
	st  *store.Store
	log *diag.Logger

	mu      sync.Mutex
	clients map[string]*Conn
	stats   Stats
	closed  bool
}

// New constructs a broker over an opened store.
func New(cfg config.Config, st *store.Store, log *diag.Logger) *Broker {
	if log == nil {
		log = diag.Discard()
	}
	return &Broker{
		cfg:     cfg,
		st:      st,
		log:     log,
		clients: make(map[string]*Conn),
	}
}

// Close marks the broker closed. Active connections are stopped by the
// server layer through CloseAll; retained/inflight state stays persisted.
func (b *Broker) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

// Stats returns a copy of the counters.
func (b *Broker) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stats
	s.ActiveConnections = int64(len(b.clients))
	if n, err := b.st.AllRetained(); err == nil {
		s.RetainedCount = int64(len(n))
	}
	return s
}

// ClientCount returns active connections (test helper).
func (b *Broker) ClientCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}

// HasClient reports whether a connection for clientID is currently attached
// (test helper).
func (b *Broker) HasClient(clientID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.clients[clientID]
	return ok
}

// InflightCount returns persisted+memory inflight count for a session
// (test/observation helper).
func (b *Broker) InflightCount(clientID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.clients[clientID]; c != nil && !c.clean {
		return len(c.inflight)
	}
	n, err := b.st.InflightCount(clientID)
	if err != nil {
		return -1
	}
	return n
}

// OfflineCount returns the durable offline queue size for a session.
func (b *Broker) OfflineCount(clientID string) int {
	n, err := b.st.OfflineCount(clientID)
	if err != nil {
		return -1
	}
	return n
}
