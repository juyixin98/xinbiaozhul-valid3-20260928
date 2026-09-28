package integration_test

import (
	"strconv"
	"testing"
	"time"

	"mqttlocal/internal/packet"
	"mqttlocal/internal/testsupport"
)

// TestBasicQoS0AndPing: connect-clean, subscribe, qos0 fan-out to multiple
// subscribers, ping round trip, clean disconnect leaves no durable state.
func TestBasicQoS0AndPing(t *testing.T) {
	h := testsupport.StartHarness(t, defaultConfig(t))
	pub := h.NewClient()
	if ca, err := pub.Connect(testsupport.BasicConnectOpts("pub-base", true, 0)); err != nil || ca.Code != 0 || ca.SessionPresent {
		t.Fatalf("pub connect: %+v %v", ca, err)
	}
	var subs []*testsupport.Client
	for i := 0; i < 2; i++ {
		c := h.NewClient()
		if ca, err := c.Connect(testsupport.BasicConnectOpts("sub-base-"+strconv.Itoa(i), true, 0)); err != nil || ca.Code != 0 {
			t.Fatalf("sub %d: %+v %v", i, ca, err)
		}
		if err := c.SendSubscribe(uint16(i+1), "base/+", 0); err != nil {
			t.Fatal(err)
		}
		if id, granted, err := c.ReadSuback(2 * time.Second); err != nil || int(id) != i+1 || len(granted) != 1 || granted[0] != 0 {
			t.Fatalf("sub %d suback id=%d granted=%v err=%v", i, id, granted, err)
		}
		subs = append(subs, c)
	}

	if err := pub.SendPublish("base/a", []byte("q0"), 0, 0, false, false); err != nil {
		t.Fatal(err)
	}
	for i, c := range subs {
		f, err := c.ReadFrame(2 * time.Second)
		if err != nil {
			t.Fatalf("sub %d delivery: %v", i, err)
		}
		p, err := f.ParsePublish()
		if err != nil || p.QoS != 0 || p.PacketID != 0 || string(p.Payload) != "q0" || p.Retain || p.Dup {
			t.Fatalf("sub %d bad qos0 publish: %+v err=%v", i, p, err)
		}
	}

	// Non-matching topic goes nowhere.
	if err := pub.SendPublish("other/x", []byte("nope"), 0, 0, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := subs[0].ReadFrame(300 * time.Millisecond); err == nil {
		t.Fatal("non-matching topic delivered")
	}

	// PINGREQ -> PINGRESP.
	if err := pub.SendPing(); err != nil {
		t.Fatal(err)
	}
	f, err := pub.ReadFrame(2 * time.Second)
	if err != nil || f.Type != packet.TypePINGRESP {
		t.Fatalf("ping: %s %v", f.Type, err)
	}

	// Clean disconnect: no session remains. End() runs asynchronously after
	// the reader sees EOF, so wait for the broker to detach the session.
	if err := pub.SendDisconnect(); err != nil {
		t.Fatal(err)
	}
	if err := pub.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("close after DISCONNECT: %v", err)
	}
	waitUntil(t, func() bool { return !h.Broker().HasClient("pub-base") }, 2*time.Second)
}

// TestCleanSessionNoState: a clean session that received QoS1 unacked data
// leaves nothing in durable storage and reconnecting with a clean slate
// shows SessionPresent=false and no redelivery.
func TestCleanSessionNoState(t *testing.T) {
	h := testsupport.StartHarness(t, defaultConfig(t))
	pub := connectDurable(t, h, "pub-clean")
	sub := h.NewClient()
	if ca, err := sub.Connect(testsupport.BasicConnectOpts("ephemeral", true, 0)); err != nil || ca.Code != 0 || ca.SessionPresent {
		t.Fatalf("connect: %+v %v", ca, err)
	}
	if err := sub.SendSubscribe(1, "c/+", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := pub.SendPublish("c/1", []byte("v"), 1, 5, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pub.ReadFrame(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.ReadFrame(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	_ = sub.Close()
	waitUntil(t, func() bool { return !h.Broker().HasClient("ephemeral") }, 2*time.Second)
	if n := h.Broker().InflightCount("ephemeral"); n != 0 {
		t.Fatalf("clean session left inflight=%d", n)
	}
	if n := h.Broker().OfflineCount("ephemeral"); n != 0 {
		t.Fatalf("clean session left offline=%d", n)
	}
	sub2 := h.NewClient()
	ca, err := sub2.Connect(testsupport.BasicConnectOpts("ephemeral", true, 0))
	if err != nil || ca.Code != 0 || ca.SessionPresent {
		t.Fatalf("reconnect clean: %+v %v", ca, err)
	}
	if _, err := sub2.ReadFrame(300 * time.Millisecond); err == nil {
		t.Fatal("clean session must not redeliver")
	}
}
