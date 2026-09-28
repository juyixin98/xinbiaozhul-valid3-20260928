package broker_test

import (
	"testing"
	"time"

	"mqttd/internal/brokertest"
	"mqttd/internal/packet"
)

// TestInflightCapRejected: a slow durable subscriber that never acks is
// bounded by MaxInflightPerClient; once saturated, further QoS1 publishes to
// it cannot allocate packet ids and the rejection is observable as a stable
// inflight count plus a reject metric rather than unbounded growth.
func TestInflightCapRejected(t *testing.T) {
	// Subscriber offline with durable session: fills inflight without a
	// bounded send queue interfering.
	h := brokertest.Start(t, brokertest.WithLimits(8, 4, 32, 32, 8))
	sub, sp := h.DialDurable("cap-sub")
	if sp {
		t.Fatal("unexpected existing session")
	}
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "cap/+", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	sub.Close() // abrupt; durable state survives

	pub := h.Dial("cap-pub")
	const sent = 12
	for i := 0; i < sent; i++ {
		topic := "cap/" + string(rune('a'+i))
		if err := pub.Publish1(uint16(i+1), topic, []byte{byte('a' + i)}, false, false, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	h.WaitFor("inflight reaches cap", time.Second, func() bool {
		n, _ := h.Broker.Store().CountInflight("cap-sub")
		return n == 4
	})
	// Explicitly assert it never exceeds the configured bound.
	time.Sleep(150 * time.Millisecond)
	n, _ := h.Broker.Store().CountInflight("cap-sub")
	if n != 4 {
		t.Fatalf("inflight must be capped at 4, got %d", n)
	}
	if h.Metrics().RejectedInflight == 0 {
		t.Fatal("rejected-inflight metric must be incremented past the cap")
	}
}

// TestSubscriptionCap: SUBACK reports 0x80 once the per-client subscription
// limit is reached; the accepted subset still works.
func TestSubscriptionCap(t *testing.T) {
	h := brokertest.Start(t, brokertest.WithLimits(3, 16, 32, 32, 16))
	c := h.Dial("subcap")
	var filters []packet.SubFilter
	for i := 0; i < 5; i++ {
		filters = append(filters, packet.SubFilter{
			Topic: "cap/f" + string(rune('0'+i)), QoS: 0,
		})
	}
	sa, err := c.Subscribe(filters, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	accepted, refused := 0, 0
	for _, rc := range sa.ReturnCodes {
		if rc == packet.SubackFailure {
			refused++
		} else {
			accepted++
		}
	}
	if accepted != 3 || refused != 2 {
		t.Fatalf("accepted=%d refused=%d, want 3/2 codes=% x", accepted, refused, sa.ReturnCodes)
	}
	n, _ := h.Broker.Store().CountSubscriptions("subcap")
	if n != 3 {
		t.Fatalf("persisted subs = %d, want 3", n)
	}
}

// TestSessionCap: exceeding MaxSessions rejects NEW durable connects with
// CONNACK code 3; clean sessions are unaffected.
func TestSessionCap(t *testing.T) {
	h := brokertest.Start(t, brokertest.WithLimits(8, 16, 32, 2, 8))
	if _, sp := h.DialDurable("s1"); sp {
		t.Fatal("sp1 unexpected")
	}
	if _, sp := h.DialDurable("s2"); sp {
		t.Fatal("sp2 unexpected")
	}
	// Third new durable session must be rejected, but the connection should
	// carry CONNACK code 3.
	nc := h.RawConn()
	nc.Write(connectFrame(4, 0x00, "s3"))
	fr, err := packet.ReadFrame(nc, 4096)
	if err != nil {
		t.Fatalf("expected CONNACK, got close: %v", err)
	}
	_, used, _ := packet.DecodeRemainingLength(fr[1:])
	ca := fr[1+used:]
	if ca[1] != packet.ConnackServerUnavailable {
		t.Fatalf("session cap return code = %d, want 3", ca[1])
	}
	if h.Metrics().RejectedSessions == 0 {
		t.Fatal("rejected sessions metric not incremented")
	}
	// Reconnecting an EXISTING durable client is still allowed (not a new
	// session), and a clean client is unaffected.
	c1, sp := h.DialDurable("s1")
	if !sp {
		t.Fatal("existing durable client should reconnect with SP=true")
	}
	c1.Close()
	clean := h.Dial("clean-anyway")
	if err := clean.Ping(); err != nil {
		t.Fatalf("clean session should bypass durable cap: %v", err)
	}
}

// TestRetainedCap: a new retained topic past MaxRetained is rejected and the
// publish closes the connection as a resource limit; overwriting an existing
// topic stays allowed.
func TestRetainedCap(t *testing.T) {
	h := brokertest.Start(t, brokertest.WithLimits(8, 16, 2, 32, 8))
	pub := h.Dial("rcap")
	if err := pub.Publish0("r1", []byte("v1"), true); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish0("r2", []byte("v2"), true); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("2 retained", time.Second, func() bool { return h.Metrics().RetainedCurrent == 2 })
	// Overwrite existing topic: allowed.
	if err := pub.Publish0("r1", []byte("v1b"), true); err != nil {
		t.Fatalf("overwriting retained topic should succeed: %v", err)
	}
	// New third topic at QoS0 retain -> broker must reject (close).
	if err := pub.Publish0("r3", []byte("v3"), true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pub.Done():
	case <-time.After(time.Second):
		t.Fatal("connection must be closed when retained limit is hit")
	}
	if n, _ := h.Broker.Store().CountRetained(); n != 2 {
		t.Fatalf("retained count = %d, must remain 2", n)
	}
}

// TestSlowSubscriberDropsQoS0: a subscriber that stops draining its socket
// (real TCP back-pressure, not just an unread event channel) fills the
// bounded outbound queue; excess QoS 0 messages are dropped and counted while
// the broker stays healthy. QoS 0 delivery is best-effort and never blocks
// the publisher.
func TestSlowSubscriberDropsQoS0(t *testing.T) {
	h := brokertest.Start(t, brokertest.WithLimits(8, 16, 32, 32, 4))
	sub := h.Dial("slowq0")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "slow/+", QoS: 0}}, 1*time.Second); err != nil {
		t.Fatal(err)
	}
	// Fill the kernel receive window by genuinely not reading the socket.
	sub.PauseRead()

	pub := h.Dial("fastpub")
	// Publish until a drop is recorded. 1 KiB payloads stay well under the
	// harness 4 KiB packet cap but quickly exhaust the TCP receive window and
	// the depth-4 broker queue.
	payload := make([]byte, 1024)
	h.WaitFor("slow subscriber queue fills and QoS0 is dropped", 8*time.Second, func() bool {
		for i := 0; i < 50; i++ {
			if h.Metrics().DroppedQueueFull > 0 {
				return true
			}
			if err := pub.Publish0("slow/x", payload, false); err != nil {
				// The dead slow connection may have been torn down; that
				// itself only happens after the queue filled, so a write
				// error here is an acceptable terminal condition.
				break
			}
		}
		return h.Metrics().DroppedQueueFull > 0
	})
	if h.Metrics().DroppedQueueFull == 0 {
		t.Fatal("expected QoS0 drops for the slow subscriber")
	}

	// Broker remains healthy: a fresh publisher and an unrelated subscriber
	// complete a clean QoS0 round trip. A NEW publisher is used on purpose so
	// the assertion does not depend on the old connection surviving the slow
	// consumer's teardown.
	healthyPub := h.Dial("healthy-pub")
	sub2 := h.Dial("healthy")
	if _, err := sub2.Subscribe([]packet.SubFilter{{Topic: "ok/+", QoS: 0}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := healthyPub.Publish0("ok/1", []byte("fine"), false); err != nil {
		t.Fatal(err)
	}
	if ev := h.ExpectPublish(sub2, 2*time.Second); string(ev.Pub.Payload) != "fine" {
		t.Fatalf("healthy subscriber delivery wrong: %q", ev.Pub.Payload)
	}
}
