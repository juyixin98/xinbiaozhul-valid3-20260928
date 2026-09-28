package brokertest

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mqttd/internal/broker"
	"mqttd/internal/logx"
	"mqttd/internal/mqttclient"
)

// Harness owns a running broker and its captured diagnostic log.
type Harness struct {
	T      *testing.T
	Broker *broker.Broker
	Addr   string
	logBuf *threadSafeBuffer
	log    *logx.Logger
}

type threadSafeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *threadSafeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *threadSafeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// WithMaxPacket overrides the max packet size.
func WithMaxPacket(n int) func(*broker.Config) {
	return func(c *broker.Config) { c.MaxPacketBytes = n }
}

// WithLimits overrides the resource caps.
func WithLimits(subs, inflight, retained, sessions, queue int) func(*broker.Config) {
	return func(c *broker.Config) {
		c.MaxSubscriptionsPerClient = subs
		c.MaxInflightPerClient = inflight
		c.MaxRetained = retained
		c.MaxSessions = sessions
		c.SendQueueDepth = queue
	}
}

// startAt builds a broker using dbPath (shared between restarts).
func startAt(t *testing.T, dbPath string, mods ...func(*broker.Config)) *Harness {
	t.Helper()
	cfg := broker.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DBPath = dbPath
	cfg.RetransmitInterval = 80 * time.Millisecond
	cfg.SendQueueDepth = 8
	cfg.SendWriteTimeout = 400 * time.Millisecond
	cfg.MaxInflightPerClient = 16
	cfg.MaxSubscriptionsPerClient = 8
	cfg.MaxSessions = 32
	cfg.MaxRetained = 32
	cfg.MaxPacketBytes = 4096
	for _, m := range mods {
		m(&cfg)
	}
	buf := &threadSafeBuffer{}
	lg := logx.New(&testWriter{t: t, buf: buf}, t.Name())
	b, err := broker.New(cfg, lg)
	if err != nil {
		t.Fatalf("broker new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = b.Close()
	})
	if err := b.Serve(ctx); err != nil {
		t.Fatalf("broker serve: %v", err)
	}
	return &Harness{T: t, Broker: b, Addr: b.Addr().String(), logBuf: buf, log: lg}
}

// StartWithDB starts a broker backed by an explicit database path.
func StartWithDB(t *testing.T, dbPath string, mods ...func(*broker.Config)) *Harness {
	return startAt(t, dbPath, mods...)
}

// RestartWithDB closes the old harness broker and starts a new one on the same
// database file (new ephemeral port).
func RestartWithDB(t *testing.T, old *Harness, dbPath string, mods ...func(*broker.Config)) *Harness {
	t.Helper()
	if err := old.Broker.Close(); err != nil {
		t.Fatalf("close old broker: %v", err)
	}
	return startAt(t, dbPath, mods...)
}

// Start builds a broker on an ephemeral port with small, fast limits so
// resource-bound tests are cheap. Override cfg before Start with WithConfig.
func Start(t *testing.T, mods ...func(*broker.Config)) *Harness {
	t.Helper()
	cfg := broker.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DBPath = filepath.Join(t.TempDir(), "broker.db")
	cfg.RetransmitInterval = 80 * time.Millisecond
	cfg.SendQueueDepth = 8
	cfg.SendWriteTimeout = 400 * time.Millisecond
	cfg.MaxInflightPerClient = 16
	cfg.MaxSubscriptionsPerClient = 8
	cfg.MaxSessions = 32
	cfg.MaxRetained = 32
	cfg.MaxPacketBytes = 4096
	for _, m := range mods {
		m(&cfg)
	}
	buf := &threadSafeBuffer{}
	// Tee broker logs to both the test output and the captured buffer so a
	// failed test shows the request/record/run id/sequence trail directly.
	runID := t.Name()
	lg := logx.New(&testWriter{t: t, buf: buf}, runID)
	b, err := broker.New(cfg, lg)
	if err != nil {
		t.Fatalf("broker new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = b.Close()
	})
	if err := b.Serve(ctx); err != nil {
		t.Fatalf("broker serve: %v", err)
	}
	return &Harness{T: t, Broker: b, Addr: b.Addr().String(), logBuf: buf, log: lg}
}

type testWriter struct {
	t   *testing.T
	buf *threadSafeBuffer
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Logs returns everything the broker logged.
func (h *Harness) Logs() string { return h.logBuf.String() }

// WaitFor polls cond until true or fails the test after timeout.
func (h *Harness) WaitFor(what string, timeout time.Duration, cond func() bool) {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if cond() {
		return
	}
	h.T.Fatalf("timed out waiting for %s\n--- broker log ---\n%s", what, h.Logs())
}

// Metrics returns the current metric snapshot.
func (h *Harness) Metrics() broker.Snapshot { return h.Broker.Metrics() }

// Dial connects a clean-session client by default.
func (h *Harness) Dial(id string) *mqttclient.Client {
	h.T.Helper()
	c, _, err := mqttclient.Dial(h.Addr, mqttclient.Options{ClientID: id, CleanSession: true})
	if err != nil {
		h.T.Fatalf("dial %s: %v", id, err)
	}
	h.T.Cleanup(func() { c.Close() })
	return c
}

// DialDurable connects with clean=false, returning the CONNACK event so tests
// can assert SessionPresent.
func (h *Harness) DialDurable(id string) (*mqttclient.Client, bool) {
	h.T.Helper()
	c, ev, err := mqttclient.Dial(h.Addr, mqttclient.Options{ClientID: id, CleanSession: false})
	if err != nil {
		h.T.Fatalf("dial durable %s: %v", id, err)
	}
	h.T.Cleanup(func() { c.Close() })
	return c, ev.Conn.SessionPresent
}

// DialOpts connects with explicit options.
func (h *Harness) DialOpts(opt mqttclient.Options) (*mqttclient.Client, *mqttclient.Event, error) {
	return mqttclient.Dial(h.Addr, opt)
}

// RawConn opens a bare TCP socket to the broker.
func (h *Harness) RawConn() net.Conn {
	h.T.Helper()
	c, err := net.Dial("tcp", h.Addr)
	if err != nil {
		h.T.Fatalf("raw dial: %v", err)
	}
	h.T.Cleanup(func() { c.Close() })
	return c
}

// ExpectPublish reads the next inbound PUBLISH or fails.
func (h *Harness) ExpectPublish(c *mqttclient.Client, timeout time.Duration) *mqttclient.Event {
	h.T.Helper()
	ev, err := c.NextEvent(timeout)
	if err != nil {
		h.T.Fatalf("expected PUBLISH: %v", err)
	}
	if ev.Kind != mqttclient.EvPublish {
		h.T.Fatalf("expected PUBLISH, got kind %d", ev.Kind)
	}
	return &ev
}

// AssertNoPublish ensures no PUBLISH arrives within d.
func (h *Harness) AssertNoPublish(c *mqttclient.Client, d time.Duration) {
	h.T.Helper()
	ev, err := c.NextEvent(d)
	if err == nil && ev.Kind == mqttclient.EvPublish {
		h.T.Fatalf("unexpected PUBLISH on %s: %q", ev.Pub.Topic, ev.Pub.Payload)
	}
}
