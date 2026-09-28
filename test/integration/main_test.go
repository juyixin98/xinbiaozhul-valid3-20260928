// Package integration_test contains the end-to-end protocol tests. They
// exercise the broker over real TCP sockets using the hand-rolled test
// client (internal/testsupport), which can inject malformed bytes.
//
// Every scenario logs a run id, connection name, monotonic step number,
// the protocol stage and the assertion basis, so a reviewer can replay the
// decision trail.
package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mqttlocal/internal/config"
	"mqttlocal/internal/packet"
	"mqttlocal/internal/testsupport"
)

// runID is stamped on every logged protocol step and into brokers'
// diagnostic records for this test invocation.
var runID = "it-" + time.Now().UTC().Format("20060102T150405Z")

func TestMain(m *testing.M) {
	testsupport.RunID = runID
	os.Exit(m.Run())
}

// smallConfig returns tight limits used by resource tests.
func smallConfig(db string) config.Config {
	c := config.Default()
	c.Listen = "127.0.0.1:0"
	c.Database = db
	c.MaxConnections = 4
	c.MaxInflightPerSession = 4
	c.MaxOfflinePerSession = 6
	c.SendQueueDepth = 4
	c.MaxPacketBytes = 4096
	c.KeepAliveMultiplier = 1.5
	return c
}

// defaultConfig returns production-default limits on an ephemeral port/database
// (tests never bind 1883, which may be in use by another broker).
func defaultConfig(tb testing.TB) config.Config {
	c := config.Default()
	c.Listen = "127.0.0.1:0"
	c.Database = filepath.Join(tb.TempDir(), "default.db")
	return c
}

func logStep(t *testing.T, c *testsupport.Client, stage, format string, args ...any) {
	t.Helper()
	verdict := fmt.Sprintf(format, args...)
	if c == nil {
		t.Logf("[run=%s] stage=%s :: %s", runID, stage, verdict)
		return
	}
	t.Logf("%s | %s", c.Step(stage, format, args...), verdict)
}

// drainUntilClose reads frames until the connection closes, counting them.
func drainUntilClose(c *testsupport.Client, timeout time.Duration, n *int) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.SetReadDeadline(300 * time.Millisecond)
		_, err := c.ReadFrame(300 * time.Millisecond)
		if err != nil {
			return c.ExpectClose(0)
		}
		*n = *n + 1
	}
	return fmt.Errorf("still open after %s", timeout)
}

// collectPublishes drains frames until no frame arrives within idle. It
// returns PUBLISH frames and any non-PUBLISH frames separately.
func collectPublishes(t *testing.T, c *testsupport.Client, idle time.Duration, max int) (pubs []*testsupport.PublishFrame, others []*testsupport.Frame) {
	t.Helper()
	for i := 0; i < max; i++ {
		f, err := c.ReadFrame(idle)
		if err != nil {
			return pubs, others
		}
		switch f.Type {
		case packet.TypePUBLISH:
			p, err := f.ParsePublish()
			if err != nil {
				t.Fatalf("parse publish: %v", err)
			}
			pubs = append(pubs, p)
		default:
			others = append(others, f)
		}
	}
	return pubs, others
}
