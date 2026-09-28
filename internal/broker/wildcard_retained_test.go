package broker_test

import (
	"testing"
	"time"

	"mqttd/internal/brokertest"
	"mqttd/internal/packet"
)

// TestWildcardBoundaries exercises the same boundary matrix end-to-end that
// the topics package checks in isolation: plus/hash, empty levels, and the
// '$' server namespace.
func TestWildcardBoundaries(t *testing.T) {
	h := brokertest.Start(t)
	plus := h.Dial("p")
	hash := h.Dial("hs")
	dollar := h.Dial("dl")
	plainHash := h.Dial("ph") // '#' — must NOT receive $SYS
	pub := h.Dial("p2")

	if _, err := plus.Subscribe([]packet.SubFilter{{Topic: "a/+", QoS: 0}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := hash.Subscribe([]packet.SubFilter{{Topic: "a/#", QoS: 0}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := dollar.Subscribe([]packet.SubFilter{{Topic: "$SYS/#", QoS: 0}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := plainHash.Subscribe([]packet.SubFilter{{Topic: "#", QoS: 0}}, time.Second); err != nil {
		t.Fatal(err)
	}

	// a/b: plus, hash and plain '#' all receive; $SYS filter does not.
	if err := pub.Publish0("a/b", []byte("ab"), false); err != nil {
		t.Fatal(err)
	}
	if ev := h.ExpectPublish(plus, time.Second); ev.Pub.Topic != "a/b" {
		t.Fatal("a/+ should receive a/b")
	}
	if ev := h.ExpectPublish(hash, time.Second); ev.Pub.Topic != "a/b" {
		t.Fatal("a/# should receive a/b")
	}
	if ev := h.ExpectPublish(plainHash, time.Second); ev.Pub.Topic != "a/b" {
		t.Fatal("# should receive a/b")
	}
	h.AssertNoPublish(dollar, 200*time.Millisecond)

	// a: hash matches (zero remaining levels); plus does not.
	if err := pub.Publish0("a", []byte("a0"), false); err != nil {
		t.Fatal(err)
	}
	h.AssertNoPublish(plus, 200*time.Millisecond)
	if ev := h.ExpectPublish(hash, time.Second); ev.Pub.Topic != "a" {
		t.Fatal("a/# must receive a (zero levels absorbed by #)")
	}
	if ev := h.ExpectPublish(plainHash, time.Second); ev.Pub.Topic != "a" {
		t.Fatal("# must receive a")
	}

	// a/: empty trailing level is matched by '+'.
	if err := pub.Publish0("a/", []byte("ae"), false); err != nil {
		t.Fatal(err)
	}
	if ev := h.ExpectPublish(plus, time.Second); ev.Pub.Topic != "a/" {
		t.Fatal("a/+ must receive the empty trailing level a/")
	}
	h.ExpectPublish(hash, time.Second)
	h.ExpectPublish(plainHash, time.Second)

	// $SYS/x: only explicit '$' filters; wildcard-first '#' never matches.
	if err := pub.Publish0("$SYS/x", []byte("sys"), false); err != nil {
		t.Fatal(err)
	}
	if ev := h.ExpectPublish(dollar, time.Second); ev.Pub.Topic != "$SYS/x" {
		t.Fatal("$SYS/# must receive $SYS/x")
	}
	h.AssertNoPublish(plainHash, 300*time.Millisecond)
	h.AssertNoPublish(plus, 100*time.Millisecond)
	h.AssertNoPublish(hash, 100*time.Millisecond)
}

// TestRetainedStoreReplaceClearAndNewSubscriber covers retained semantics.
func TestRetainedStoreReplaceClearAndNewSubscriber(t *testing.T) {
	h := brokertest.Start(t)
	pub := h.Dial("ret-pub")

	// Publish retained QoS 1.
	if err := pub.Publish1(1, "r/temp", []byte("10"), true, false, time.Second); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("retained stored", time.Second, func() bool { return h.Metrics().RetainedCurrent == 1 })

	// A new subscriber immediately receives the retained image with RETAIN=1.
	sub := h.Dial("ret-sub")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "r/+", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	ev := h.ExpectPublish(sub, time.Second)
	if string(ev.Pub.Payload) != "10" || !ev.Pub.Retain {
		t.Fatalf("retained delivery must carry RETAIN=1 and payload 10: %+v", ev.Pub)
	}
	if err := sub.Puback(ev.Pub.PacketID); err != nil {
		t.Fatal(err)
	}

	// A subsequently published normal message arrives with RETAIN=0 even
	// though a retained image exists.
	if err := pub.Publish1(2, "r/temp", []byte("11"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	ev2 := h.ExpectPublish(sub, time.Second)
	if string(ev2.Pub.Payload) != "11" || ev2.Pub.Retain {
		t.Fatalf("normal delivery must carry RETAIN=0: %+v", ev2.Pub)
	}
	if err := sub.Puback(ev2.Pub.PacketID); err != nil {
		t.Fatal(err)
	}

	// Empty retained payload clears it.
	if err := pub.Publish1(4, "r/temp", nil, true, false, time.Second); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("retained cleared", time.Second, func() bool { return h.Metrics().RetainedCurrent == 0 })

	// A fresh subscriber receives nothing.
	sub2 := h.Dial("ret-sub2")
	if _, err := sub2.Subscribe([]packet.SubFilter{{Topic: "r/+", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	h.AssertNoPublish(sub2, 300*time.Millisecond)
}

// TestRetainedWildcardDelivery delivers multiple retained images via a '#'
// filter and asserts each carries RETAIN=1.
func TestRetainedWildcardDelivery(t *testing.T) {
	h := brokertest.Start(t)
	pub := h.Dial("rp")
	if err := pub.Publish0("house/room1/temp", []byte("20"), true); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish0("house/room2/temp", []byte("21"), true); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("two retained", time.Second, func() bool { return h.Metrics().RetainedCurrent == 2 })
	sub := h.Dial("rs")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "house/#", QoS: 0}}, time.Second); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		ev := h.ExpectPublish(sub, time.Second)
		if !ev.Pub.Retain {
			t.Fatal("retained wildcard delivery must set RETAIN=1")
		}
		got[string(ev.Pub.Payload)] = true
	}
	if !got["20"] || !got["21"] {
		t.Fatalf("missing retained images: %v", got)
	}
}
