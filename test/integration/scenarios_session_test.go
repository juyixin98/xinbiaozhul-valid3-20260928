package integration_test

import (
	"bytes"
	"testing"
	"time"

	"mqttlocal/internal/packet"
	"mqttlocal/internal/testsupport"
)

// helper: connect durable.
func connectDurable(t *testing.T, h *testsupport.Harness, id string) *testsupport.Client {
	t.Helper()
	c := h.NewClient()
	ca, err := c.Connect(testsupport.ConnectOpts{ClientID: id, CleanSession: false, KeepAlive: 0})
	if err != nil || ca.Code != 0 {
		t.Fatalf("connect %s: err=%v connack=0x%02x", id, err, ca.Code)
	}
	return c
}

// TestReconnectRedeliveryDup covers scenario 1+2: a durable subscriber
// drops after receiving QoS1 but before PUBACK ("ack loss"). Reconnect must
// redeliver with the SAME packet id and DUP=1; settling the ACK finally
// clears inflight. This is at-least-once — QoS 2 / exactly-once is out of
// scope.
func TestReconnectRedeliveryDup(t *testing.T) {
	h := testsupport.StartHarness(t, defaultConfig(t))
	pub := connectDurable(t, h, "pub-rd")
	sub := connectDurable(t, h, "sub-rd")

	logStep(t, sub, "SUBSCRIBE", "durable subscription to rd/+")
	if err := sub.SendSubscribe(1, "rd/+", 1); err != nil {
		t.Fatal(err)
	}
	id2, granted, err := sub.ReadSuback(2 * time.Second)
	if err != nil || id2 != 1 || len(granted) != 1 || granted[0] != 1 {
		t.Fatalf("suback: id=%d granted=%v err=%v", id2, granted, err)
	}

	logStep(t, pub, "PUBLISH-QoS1", "publisher sends one qos1 message; broker must PUBACK")
	if err := pub.SendPublish("rd/a", []byte("hello"), 1, 100, false, false); err != nil {
		t.Fatal(err)
	}
	f, err := pub.ReadFrame(2 * time.Second)
	if err != nil {
		t.Fatalf("puback frame: %v", err)
	}
	if f.Type != packet.TypePUBACK {
		t.Fatalf("expected PUBACK to publisher, got %s", f.Type)
	}
	pubAckID, _ := f.PacketID()
	if pubAckID != 100 {
		t.Fatalf("PUBACK must echo the publisher's id 100, got %d", pubAckID)
	}

	logStep(t, sub, "DELIVERY", "subscriber receives qos1 PUBLISH; does NOT ack (ack loss)")
	sf, err := sub.ReadFrame(2 * time.Second)
	if err != nil {
		t.Fatalf("delivered: %v", err)
	}
	dp, err := sf.ParsePublish()
	if err != nil {
		t.Fatal(err)
	}
	if dp.QoS != 1 || dp.PacketID == 0 || string(dp.Payload) != "hello" || dp.Dup {
		t.Fatalf("first delivery malformed: %+v", dp)
	}
	serverPacketID := dp.PacketID
	logStep(t, sub, "STATE", "inflight=1 persisted; reading session/inflight status")
	if n := h.Broker().InflightCount("sub-rd"); n != 1 {
		t.Fatalf("inflight after delivery: got %d want 1", n)
	}

	logStep(t, sub, "DROP", "raw TCP close without DISCONNECT/PUBACK")
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	pubAckWait := func() {
		f, err := pub.ReadFrame(200 * time.Millisecond)
		if err == nil {
			t.Fatalf("publisher must not receive delivery to its own topic: %s", f.Type)
		}
	}
	_ = pubAckWait
	waitUntil(t, func() bool { return !h.Broker().HasClient("sub-rd") }, 2*time.Second)

	logStep(t, nil, "RECONNECT", "same client id, CleanSession=0; CONNACK SessionPresent=1")
	sub2 := h.NewClient()
	ca, err := sub2.Connect(testsupport.ConnectOpts{ClientID: "sub-rd", CleanSession: false})
	if err != nil {
		t.Fatal(err)
	}
	if ca.Code != 0 || !ca.SessionPresent {
		t.Fatalf("reconnect connack: code=0x%02x sp=%v", ca.Code, ca.SessionPresent)
	}

	logStep(t, sub2, "REDELIVERY", "expect DUP=1 SAME packet id %d", serverPacketID)
	rf, err := sub2.ReadFrame(3 * time.Second)
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	rp, err := rf.ParsePublish()
	if err != nil {
		t.Fatal(err)
	}
	if rp.Dup != true || rp.PacketID != serverPacketID || bytes.Equal(rp.Payload, []byte("hello")) == false {
		t.Fatalf("redelivery must reuse id and set DUP: %+v", rp)
	}
	stats := h.Broker().Stats()
	if stats.Redeliveries < 1 {
		t.Fatalf("redelivery counter not recorded: %+v", stats)
	}

	logStep(t, sub2, "PUBACK", "settle the duplicated delivery; inflight must clear")
	if err := sub2.SendPuback(serverPacketID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return h.Broker().InflightCount("sub-rd") == 0 }, 2*time.Second)
	if n := h.Broker().InflightCount("sub-rd"); n != 0 {
		t.Fatalf("inflight after ack: %d", n)
	}

	// Idempotency: a duplicate PUBACK for the same (now settled) id is a
	// protocol violation and closes the connection.
	logStep(t, sub2, "DUPLICATE-ACK", "repeated PUBACK for settled id -> protocol violation close")
	if err := sub2.SendPuback(serverPacketID); err != nil {
		t.Fatal(err)
	}
	if err := sub2.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("duplicate PUBACK must close connection: %v", err)
	}
}

// TestCleanStartPurgesDurableSession verifies MQTT-3.1.4-4: a clean
// CONNECT to an id that has a stored durable session discards the stored
// state; a subsequent durable reconnect shows SessionPresent=false.
func TestCleanStartPurgesDurableSession(t *testing.T) {
	h := testsupport.StartHarness(t, defaultConfig(t))

	// Build durable state: subscribe, receive an unacked QoS1, go offline.
	pub := connectDurable(t, h, "pur-purge")
	sub := connectDurable(t, h, "purge-id")
	if err := sub.SendSubscribe(1, "p/+", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := pub.SendPublish("p/1", []byte("z"), 1, 9, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pub.ReadFrame(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.ReadFrame(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if n := h.Broker().InflightCount("purge-id"); n != 1 {
		t.Fatalf("precondition inflight=%d", n)
	}
	_ = sub.Close()
	waitUntil(t, func() bool { return !h.Broker().HasClient("purge-id") }, 2*time.Second)

	// Clean start with the same id: SessionPresent must be false.
	clean := h.NewClient()
	ca, err := clean.Connect(testsupport.BasicConnectOpts("purge-id", true, 0))
	if err != nil || ca.Code != 0 || ca.SessionPresent {
		t.Fatalf("clean connect: %+v err=%v", ca, err)
	}
	if _, err := clean.ReadFrame(300 * time.Millisecond); err == nil {
		t.Fatal("clean start must not redeliver persisted inflight")
	}
	_ = clean.Close()
	waitUntil(t, func() bool { return !h.Broker().HasClient("purge-id") }, 2*time.Second)

	// Durable reconnect afterwards: state is gone.
	d := h.NewClient()
	ca2, err := d.Connect(testsupport.BasicConnectOpts("purge-id", false, 0))
	if err != nil || ca2.SessionPresent {
		t.Fatalf("expected fresh durable session (sp=false), got %+v err=%v", ca2, err)
	}
	if n := h.Broker().InflightCount("purge-id"); n != 0 {
		t.Fatalf("durable state survived purge: inflight=%d", n)
	}
}

// TestDuplicatePublisherPacketID documents that QoS1 allows a publisher to
// reuse/repeat packet identifiers on independent flows; the broker PUBACKs
// each and never claims exactly-once.
func TestDuplicatePublisherPacketID(t *testing.T) {
	h := testsupport.StartHarness(t, defaultConfig(t))
	pub := connectDurable(t, h, "pub-dup")
	sub := connectDurable(t, h, "sub-dup")
	if err := sub.SendSubscribe(1, "dup/+", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		logStep(t, pub, "PUBLISH-DUP-ALLOWED", "publisher reuses id=42, attempt %d", i+1)
		if err := pub.SendPublish("dup/x", []byte{byte('A' + i)}, 1, 42, i > 0, false); err != nil {
			t.Fatal(err)
		}
		f, err := pub.ReadFrame(2 * time.Second)
		if err != nil {
			t.Fatalf("puback %d: %v", i, err)
		}
		id, _ := f.PacketID()
		if f.Type != packet.TypePUBACK || id != 42 {
			t.Fatalf("attempt %d: got type=%s id=%d", i, f.Type, id)
		}
	}
	// Subscriber receives three qos1 deliveries with three DISTINCT server
	// ids (publisher-side ids are a separate flow namespace).
	got := map[uint16]bool{}
	for i := 0; i < 3; i++ {
		sf, err := sub.ReadFrame(2 * time.Second)
		if err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
		p, _ := sf.ParsePublish()
		if p.QoS != 1 {
			t.Fatalf("delivery %d not qos1", i)
		}
		if got[p.PacketID] {
			t.Fatalf("server reused packet id %d", p.PacketID)
		}
		got[p.PacketID] = true
		if err := sub.SendPuback(p.PacketID); err != nil {
			t.Fatal(err)
		}
	}
}

func waitUntil(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
