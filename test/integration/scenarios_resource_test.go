package integration_test

import (
	"path/filepath"
	"testing"
	"time"

	"mqttlocal/internal/packet"
	"mqttlocal/internal/testsupport"
)

// TestSlowSubscriber verifies the documented resource policy:
//
//   - per-session inflight cap (4) and bounded send queue (4),
//   - a subscriber that cannot keep up is CLOSED with a resource-exhaustion
//     reason; in-flight messages remain PERSISTED,
//   - messages published while it is down go to the durable offline queue,
//   - reconnect redelivers inflight with DUP=1 and promotes offline msgs.
func TestSlowSubscriber(t *testing.T) {
	db := filepath.Join(t.TempDir(), "slow.db")
	h := testsupport.StartHarness(t, smallConfig(db))

	pub := connectDurable(t, h, "pub-slow")
	sub := connectDurable(t, h, "sub-slow")
	if err := sub.SendSubscribe(1, "slow/+", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	// The subscriber never reads anything after SUBACK.
	logStep(t, nil, "POLICY", "cap: inflight=%d sendqueue=%d offline=%d",
		h.Config().MaxInflightPerSession, h.Config().SendQueueDepth, h.Config().MaxOfflinePerSession)

	// Publish more messages than inflight+queue can hold. Each is acked to
	// the publisher regardless of subscriber fate.
	const N = 9
	for i := 1; i <= N; i++ {
		if err := pub.SendPublish("slow/m", []byte{byte(i)}, 1, uint16(100+i), false, false); err != nil {
			t.Fatal(err)
		}
		// PUBACK to publisher should still arrive.
		f, err := pub.ReadFrame(2 * time.Second)
		if err != nil {
			t.Fatalf("publisher PUBACK %d: %v", i, err)
		}
		if f.Type != packet.TypePUBACK {
			t.Fatalf("msg %d: expected PUBACK got %s", i, f.Type)
		}
	}

	logStep(t, sub, "KICK", "slow subscriber must be closed (resource_exhaustion)")
	// Frames already handed to the TCP stack remain buffered: drain them,
	// the next read after the delivered set must show the close.
	drained := 0
	if err := drainUntilClose(sub, 3*time.Second, &drained); err != nil {
		t.Fatalf("slow subscriber not closed (drained %d frames): %v", drained, err)
	}
	logStep(t, nil, "DRAINED", "%d frames consumed before observing close", drained)
	waitUntil(t, func() bool { return !h.Broker().HasClient("sub-slow") }, 2*time.Second)

	// Deterministic accounting: at most the inflight cap persists as
	// inflight; the rest (up to the offline cap) queue offline; overflow is
	// oldest-evicted and counted.
	inflight := h.Broker().InflightCount("sub-slow")
	offline := h.Broker().OfflineCount("sub-slow")
	logStep(t, nil, "STATE", "after kick: inflight=%d offline=%d dropped=%d",
		inflight, offline, h.Broker().Stats().DroppedOffline)
	if inflight != h.Config().MaxInflightPerSession {
		t.Fatalf("inflight=%d want exactly cap %d", inflight, h.Config().MaxInflightPerSession)
	}
	if offline > h.Config().MaxOfflinePerSession {
		t.Fatalf("offline=%d exceeds cap %d", offline, h.Config().MaxOfflinePerSession)
	}
	if inflight+offline == 0 {
		t.Fatalf("no persisted state for slow subscriber")
	}

	// Reconnect: inflight redelivered with DUP, offline promoted as acks
	// arrive.
	sub2 := h.NewClient()
	ca, err := sub2.Connect(testsupport.ConnectOpts{ClientID: "sub-slow", CleanSession: false})
	if err != nil || !ca.SessionPresent {
		t.Fatalf("reconnect: %+v err=%v", ca, err)
	}
	// First frames are the persisted inflight, all DUP=1.
	dupCount := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && dupCount < inflight {
		f, err := sub2.ReadFrame(500 * time.Millisecond)
		if err != nil {
			break
		}
		p, perr := f.ParsePublish()
		if perr != nil || !p.Dup {
			t.Fatalf("expected DUP redelivery, got %+v err=%v", p, perr)
		}
		if err := sub2.SendPuback(p.PacketID); err != nil {
			t.Fatal(err)
		}
		dupCount++
	}
	if dupCount != inflight {
		t.Fatalf("redelivered inflight=%d want %d", dupCount, inflight)
	}
	// After acks, offline messages should promote and deliver (not DUP).
	waitUntil(t, func() bool { return h.Broker().OfflineCount("sub-slow") == 0 }, 3*time.Second)
	got := 0
	for {
		f, err := sub2.ReadFrame(500 * time.Millisecond)
		if err != nil {
			break
		}
		p, _ := f.ParsePublish()
		if p.Dup {
			t.Fatalf("offline-promoted message must not be DUP: %+v", p)
		}
		if err := sub2.SendPuback(p.PacketID); err != nil {
			t.Fatal(err)
		}
		got++
	}
	if got != offline {
		t.Fatalf("promoted offline deliveries=%d want %d", got, offline)
	}
	if n := h.Broker().InflightCount("sub-slow"); n != 0 {
		t.Fatalf("residual inflight %d", n)
	}
}

// TestConnectionCap verifies MaxConnections produces CONNACK 0x03.
func TestConnectionCap(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "cap.db")))
	var clients []*testsupport.Client
	// Limit is 4; establish 4 clean sessions.
	for i := 0; i < h.Config().MaxConnections; i++ {
		c := h.NewClient()
		ca, err := c.Connect(testsupport.ConnectOpts{ClientID: "", CleanSession: true})
		if err != nil || ca.Code != packet.ConnAccepted {
			t.Fatalf("connect %d: code=0x%02x err=%v", i, ca.Code, err)
		}
		clients = append(clients, c)
	}
	// Fifth must be rejected 0x03 and the connection closed.
	extra := h.NewClient()
	ca, err := extra.Connect(testsupport.ConnectOpts{ClientID: "", CleanSession: true})
	if err != nil {
		t.Fatalf("rejected connect should still read CONNACK: %v", err)
	}
	logStep(t, extra, "CAP", "code=0x%02x expected 0x03", ca.Code)
	if ca.Code != packet.ConnServerUnavailable {
		t.Fatalf("got code 0x%02x want 0x03", ca.Code)
	}
	if err := extra.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("rejected connection not closed: %v", err)
	}
	for _, c := range clients {
		_ = c.Close()
	}
}

// TestOfflineCapEviction verifies oldest-first eviction of the durable
// offline queue at MaxOfflinePerSession.
func TestOfflineCapEviction(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "off.db")))
	pub := connectDurable(t, h, "pub-ev")
	sub := connectDurable(t, h, "sub-ev")
	if err := sub.SendSubscribe(1, "ev/+", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	logStep(t, sub, "OFFLINE", "subscriber goes offline before publishing")
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return !h.Broker().HasClient("sub-ev") }, 2*time.Second)

	capN := h.Config().MaxOfflinePerSession
	for i := 1; i <= capN+3; i++ {
		if err := pub.SendPublish("ev/m", []byte{byte(i)}, 1, uint16(i), false, false); err != nil {
			t.Fatal(err)
		}
		f, err := pub.ReadFrame(2 * time.Second)
		if err != nil || f.Type != packet.TypePUBACK {
			t.Fatalf("puback %d: %v %s", i, err, f.Type)
		}
	}
	if n := h.Broker().OfflineCount("sub-ev"); n != capN {
		t.Fatalf("offline=%d want cap %d", n, capN)
	}
	dropped := h.Broker().Stats().DroppedOffline
	if dropped < 3 {
		t.Fatalf("dropped counter=%d want >=3", dropped)
	}

	// Reconnect: the 3 oldest payloads (1,2,3) were evicted; 4..capN+3 stay.
	sub2 := h.NewClient()
	ca, err := sub2.Connect(testsupport.ConnectOpts{ClientID: "sub-ev", CleanSession: false})
	if err != nil || !ca.SessionPresent {
		t.Fatalf("reconnect: %+v %v", ca, err)
	}
	seen := map[byte]bool{}
	for i := 0; i < capN; i++ {
		f, err := sub2.ReadFrame(2 * time.Second)
		if err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
		p, _ := f.ParsePublish()
		seen[p.Payload[0]] = true
		if err := sub2.SendPuback(p.PacketID); err != nil {
			t.Fatal(err)
		}
	}
	for _, old := range []byte{1, 2, 3} {
		if seen[old] {
			t.Fatalf("evicted payload %d was delivered", old)
		}
	}
	if !seen[4] || !seen[byte(capN+3)] {
		t.Fatalf("expected retained newest payloads, got %v", seen)
	}
}
