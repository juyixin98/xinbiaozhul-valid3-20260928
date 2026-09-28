package broker_test

import (
	"sync/atomic"
	"testing"
	"time"

	"mqttd/internal/brokertest"
	"mqttd/internal/mqttclient"
	"mqttd/internal/packet"
)

// TestDurableSessionReplaysUnackedOnReconnect: while a durable subscriber is
// offline (no DISCONNECT — simulated crash), a QoS 1 message is persisted; on
// reconnect it must arrive with DUP=1 and the original semantics.
func TestDurableSessionReplaysUnackedOnReconnect(t *testing.T) {
	h := brokertest.Start(t)
	sub, sp := h.DialDurable("d1")
	if sp {
		t.Fatal("first connect should not see a session")
	}
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "offline/+", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	// Simulate abrupt client loss (no DISCONNECT).
	sub.Close()

	pub := h.Dial("d1-pub")
	if err := pub.Publish1(1, "offline/x", []byte("persisted"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	// The row must be durable while the client is offline.
	h.WaitFor("offline inflight persisted", time.Second, func() bool {
		n, _ := h.Broker.Store().CountInflight("d1")
		return n == 1
	})

	sub2, sp2 := h.DialDurable("d1")
	if !sp2 {
		t.Fatal("reconnect must report session present")
	}
	ev := h.ExpectPublish(sub2, 2*time.Second)
	if string(ev.Pub.Payload) != "persisted" {
		t.Fatalf("payload = %q", ev.Pub.Payload)
	}
	if !ev.Pub.Dup {
		t.Fatal("offline replay must carry DUP=1")
	}
	if err := sub2.Puback(ev.Pub.PacketID); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("replayed inflight acked", time.Second, func() bool {
		n, _ := h.Broker.Store().CountInflight("d1")
		return n == 0
	})
}

// TestAckLossRetransmitDup: deliver QoS1, do not PUBACK; the broker must keep
// re-sending with DUP=1 until acknowledged, then stop.
func TestAckLossRetransmitDup(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("slow")
	pub := h.Dial("slow-pub")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "rt/x", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish1(1, "rt/x", []byte("r"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	first := h.ExpectPublish(sub, time.Second)
	pid := first.Pub.PacketID
	if first.Pub.Dup {
		t.Fatal("first send must have DUP=0")
	}

	var dupCount atomic.Int32
	h.WaitFor("multiple DUP retransmissions observed", 3*time.Second, func() bool {
		// Drain retransmits in a short loop inside the poller.
		for {
			ev, err := sub.NextEvent(60 * time.Millisecond)
			if err != nil {
				break
			}
			if ev.Kind == mqttclient.EvPublish {
				if ev.Pub.PacketID != pid || !ev.Pub.Dup {
					h.T.Fatalf("retransmit must reuse pid and set DUP: %+v", ev.Pub)
				}
				dupCount.Add(1)
			}
		}
		return dupCount.Load() >= 2
	})

	// Now acknowledge: retransmits must stop and the row be removed.
	if err := sub.Puback(pid); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("inflight row removed", time.Second, func() bool {
		n, _ := h.Broker.Store().CountInflight("slow")
		return n == 0
	})
	before := h.Metrics().Retransmits
	time.Sleep(400 * time.Millisecond)
	if h.Metrics().Retransmits != before {
		t.Fatalf("retransmits continued after PUBACK: before=%d after=%d", before, h.Metrics().Retransmits)
	}
	if dupCount.Load() < 2 {
		t.Fatalf("expected >=2 DUP copies, got %d", dupCount.Load())
	}
}

// TestDuplicateOutboundPacketIDAllowed verifies at-least-once handling on the
// INBOUND side: a publisher may re-use a packet identifier for two DIFFERENT
// messages (the broker cannot detect application-level reuse); both messages
// are routed and the broker answers each PUBACK. Delivery to subscribers may
// therefore be duplicated, which the broker never claims is exactly-once.
func TestDuplicateOutboundPacketIDAllowed(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("reuse-sub")
	pub := h.Dial("reuse-pub")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "reuse/x", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	for i, payload := range []string{"first", "second"} {
		// Same packet id 42 used twice; the second marks DUP=1 as the client
		// would when unsure whether the first PUBACK was lost.
		err := pub.Publish1(42, "reuse/x", []byte(payload), false, i == 1, time.Second)
		if err != nil {
			t.Fatalf("publish %s: %v", payload, err)
		}
		ev := h.ExpectPublish(sub, time.Second)
		if string(ev.Pub.Payload) != payload {
			t.Fatalf("copy %d payload = %q", i, ev.Pub.Payload)
		}
		if err := sub.Puback(ev.Pub.PacketID); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDuplicatePubackIsIdempotent: acknowledging an unknown/already-acked
// packet id must not error or create negative accounting.
func TestDuplicatePubackIsIdempotent(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("idem")
	pub := h.Dial("idem-pub")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "idem/x", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish1(1, "idem/x", []byte("m"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	ev := h.ExpectPublish(sub, time.Second)
	if err := sub.Puback(ev.Pub.PacketID); err != nil {
		t.Fatal(err)
	}
	if err := sub.Puback(ev.Pub.PacketID); err != nil {
		t.Fatalf("duplicate PUBACK errored: %v", err)
	}
	// A PUBACK for a never-used id must also be tolerated.
	if err := sub.Puback(60000); err != nil {
		t.Fatalf("unknown PUBACK errored: %v", err)
	}
}

// TestCleanSessionWipesState: after a clean DISCONNECT, subscriptions and
// inflight rows must be gone and no replay occurs.
func TestCleanSessionWipesState(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("clean1")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "w/+", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := sub.Disconnect(); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("clean state wiped", time.Second, func() bool {
		n, _ := h.Broker.Store().CountInflight("clean1")
		s, _ := h.Broker.Store().CountSubscriptions("clean1")
		_, err := h.Broker.Store().SessionClean("clean1")
		return n == 0 && s == 0 && err != nil
	})
}
