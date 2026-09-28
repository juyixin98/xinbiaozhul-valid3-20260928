package testsupport

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mqttlocal/internal/broker"
	"mqttlocal/internal/config"
	"mqttlocal/internal/diag"
	"mqttlocal/internal/server"
	"mqttlocal/internal/store"
)

// RunID identifies the test run; every protocol step logged by the clients
// carries it. Set by the harness before dialing.
var RunID = "testrun"

// Harness owns one broker+server pair backed by a file SQLite database.
type Harness struct {
	t      *testing.T
	cfg    config.Config
	st     *store.Store
	br     *broker.Broker
	srv    *server.Server
	log    *diag.Logger
	addr   string
	name   atomic.Uint64
	cancel context.CancelFunc
	errCh  chan error
}

// StartHarness boots a broker on an ephemeral local port using cfg.
func StartHarness(t *testing.T, cfg config.Config) *Harness {
	t.Helper()
	if cfg.Database == "" {
		cfg.Database = filepath.Join(t.TempDir(), "mqttlocal.db")
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:0"
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("invalid config: %v", err)
	}
	h := &Harness{t: t, cfg: cfg}
	h.boot()
	t.Cleanup(h.Stop)
	return h
}

func (h *Harness) boot() {
	log := diag.New(&testWriter{t: h.t}, RunID, 0)
	st, err := store.Open(h.cfg.Database)
	if err != nil {
		h.t.Fatalf("open store: %v", err)
	}
	br := broker.New(h.cfg, st, log)
	if err := br.RecoverDurableWills(); err != nil {
		h.t.Fatalf("recover: %v", err)
	}
	srv := server.New(h.cfg, br, log)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	var addr string
	for time.Now().Before(deadline) {
		if a := srv.Addr(); a != nil {
			addr = a.String()
			break
		}
		select {
		case err := <-errCh:
			h.t.Fatalf("server Serve exited: %v", err)
		default:
		}
		time.Sleep(2 * time.Millisecond)
	}
	if addr == "" {
		cancel()
		h.t.Fatalf("server did not bind")
	}
	h.st, h.br, h.srv, h.log, h.addr, h.cancel, h.errCh = st, br, srv, log, addr, cancel, errCh
}

// Stop shuts the broker down (idempotent). Durable SQLite state remains.
func (h *Harness) Stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	h.srv.Close()
	<-h.errCh
	h.br.Close()
	_ = h.st.Close()
	h.cancel = nil
}

// Restart stops the broker and starts a new process-equivalent over the same
// database file; it publishes crash-recovery Wills and listens on a new port.
func (h *Harness) Restart() {
	h.t.Helper()
	h.Stop()
	h.boot()
}

// Addr returns the listener address.
func (h *Harness) Addr() string { return h.addr }

// Broker exposes the broker for state assertions.
func (h *Harness) Broker() *broker.Broker { return h.br }

// Store exposes the persistence layer for state assertions.
func (h *Harness) Store() *store.Store { return h.st }

// Config returns the active config.
func (h *Harness) Config() config.Config { return h.cfg }

// NewClient dials a uniquely named client.
func (h *Harness) NewClient() *testsupportClient {
	h.t.Helper()
	n := h.name.Add(1)
	c, err := Dial(h.addr, fmt.Sprintf("c%d", n))
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	return c
}

// Client dialing with an explicit name (for take-over pairs).
func (h *Harness) ClientNamed(name string) *testsupportClient {
	h.t.Helper()
	c, err := Dial(h.addr, name)
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	return c
}

// testsupportClient is a constructor alias of Client used by Harness.
type testsupportClient = Client

// testWriter funnels slog records through t.Log with request/run ids.
type testWriter struct{ t *testing.T }

func (w *testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", string(p))
	return len(p), nil
}
