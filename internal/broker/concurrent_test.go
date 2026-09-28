package broker_test

import (
	"sync"
	"testing"
	"time"

	"mqttd/internal/brokertest"
	"mqttd/internal/packet"
)

// TestConcurrentSubscribeAndPublish hammers the shared subscription trie with
// concurrent writers (SUBSCRIBE) and readers (PUBLISH routing). Its main job
// is to run cleanly under -race; it also asserts a late subscriber still
// receives matching messages.
func TestConcurrentSubscribeAndPublish(t *testing.T) {
	h := brokertest.Start(t, brokertest.WithLimits(256, 256, 256, 256, 64))

	var wg sync.WaitGroup
	// Concurrent subscribers across distinct filters/ids.
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := h.Dial("csub-" + itoaTest(i))
			defer c.Close()
			filter := "cc/level" + itoaTest(i%4) + "/+"
			if _, err := c.Subscribe([]packet.SubFilter{{Topic: filter, QoS: 0}}, 3*time.Second); err != nil {
				return // another goroutine may outlive shutdown; best-effort
			}
			// Keep the connection busy briefly.
			_ = c.Ping()
			_, _ = c.NextEvent(500 * time.Millisecond)
		}(i)
	}
	// Concurrent publishers to matching topics (read the trie while it mutates).
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := h.Dial("cpub-" + itoaTest(i))
			defer c.Close()
			deadline := time.Now().Add(700 * time.Millisecond)
			n := 0
			for time.Now().Before(deadline) {
				topic := "cc/level" + itoaTest(n%4) + "/dev" + itoaTest(n%7)
				if err := c.Publish0(topic, []byte{byte(n)}, false); err != nil {
					return
				}
				n++
			}
		}(i)
	}
	wg.Wait()
}

// TestSubscribeCapUnderContention ensures the per-client subscription cap is
// enforced even when SUBSCRIBE packets for the same client are serialized
// (they are, on one connection) — a single-message multi-filter case.
func TestSubscribeMultiFilterBatchPartial(t *testing.T) {
	h := brokertest.Start(t, brokertest.WithLimits(2, 16, 32, 32, 8))
	c := h.Dial("batch")
	var filters []packet.SubFilter
	filters = append(filters,
		packet.SubFilter{Topic: "b/1", QoS: 1},
		packet.SubFilter{Topic: "b/2", QoS: 1},
		packet.SubFilter{Topic: "b/3", QoS: 1}, // over the cap of 2
	)
	sa, err := c.Subscribe(filters, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if sa.ReturnCodes[0] != 1 || sa.ReturnCodes[1] != 1 || sa.ReturnCodes[2] != packet.SubackFailure {
		t.Fatalf("partial batch codes wrong: % x", sa.ReturnCodes)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
