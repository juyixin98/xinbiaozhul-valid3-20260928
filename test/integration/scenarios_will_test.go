package integration_test

import (
	"path/filepath"
	"testing"
	"time"

	"mqttlocal/internal/testsupport"
)

// TestWillOnAbnormalClose: Will publishes when the client vanishes without
// DISCONNECT; a clean DISCONNECT suppresses it; a retained Will is both
// published and stored.
func TestWillOnAbnormalClose(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "will.db")))

	// Subscriber (clean session is fine for observing).
	sub := h.NewClient()
	if ca, err := sub.Connect(testsupport.BasicConnectOpts("wsub", true, 0)); err != nil || ca.Code != 0 {
		t.Fatalf("sub connect: %+v %v", ca, err)
	}
	if err := sub.SendSubscribe(1, "will/#", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	// Will owner: abrupt close.
	w1 := h.NewClient()
	if ca, err := w1.Connect(testsupport.WillConnectOpts("will-owner", true, 0, "will/abrupt", []byte("gone"), 1, false)); err != nil || ca.Code != 0 {
		t.Fatalf("owner connect: %+v %v", ca, err)
	}
	logStep(t, w1, "WILL", "register qos1 will then vanish (raw close)")
	if err := w1.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := sub.ReadFrame(3 * time.Second)
	if err != nil {
		t.Fatalf("will delivery: %v", err)
	}
	p, _ := f.ParsePublish()
	if p.Topic != "will/abrupt" || string(p.Payload) != "gone" || p.QoS != 1 || p.Retain {
		t.Fatalf("will frame wrong: %+v", p)
	}
	if err := sub.SendPuback(fParseID(f)); err != nil {
		t.Fatal(err)
	}

	// Clean DISCONNECT: no Will.
	w2 := h.NewClient()
	if ca, err := w2.Connect(testsupport.WillConnectOpts("will-clean", true, 0, "will/clean", []byte("nope"), 0, false)); err != nil || ca.Code != 0 {
		t.Fatalf("w2 connect: %+v %v", ca, err)
	}
	if err := w2.SendDisconnect(); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.ReadFrame(400 * time.Millisecond); err == nil {
		t.Fatal("clean DISCONNECT must not publish Will")
	}

	// Retained Will on abrupt close.
	w3 := h.NewClient()
	if ca, err := w3.Connect(testsupport.WillConnectOpts("will-ret", true, 0, "will/retained", []byte("rmsg"), 0, true)); err != nil || ca.Code != 0 {
		t.Fatalf("w3 connect: %+v %v", ca, err)
	}
	if err := w3.Close(); err != nil {
		t.Fatal(err)
	}
	f2, err := sub.ReadFrame(3 * time.Second)
	if err != nil {
		t.Fatalf("retained will: %v", err)
	}
	p2, _ := f2.ParsePublish()
	// The live Will delivery follows normal publish semantics: its RETAIN
	// flag is false (only the stored copy delivered on subscribe carries it).
	if p2.Topic != "will/retained" || p2.Retain {
		t.Fatalf("live will frame wrong: %+v", p2)
	}
	// A brand-new subscriber to the topic must receive the stored retained.
	fresh := h.NewClient()
	if ca, err := fresh.Connect(testsupport.BasicConnectOpts("fresh", true, 0)); err != nil || ca.Code != 0 {
		t.Fatalf("fresh: %+v %v", ca, err)
	}
	if err := fresh.SendSubscribe(1, "will/retained", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	rf, err := fresh.ReadFrame(2 * time.Second)
	if err != nil {
		t.Fatalf("stored will retained missing: %v", err)
	}
	rp, _ := rf.ParsePublish()
	if string(rp.Payload) != "rmsg" || !rp.Retain {
		t.Fatalf("stored retained wrong: %+v", rp)
	}
}

// TestReconnectClearsWill: a durable client that registered a Will,
// reconnected without one must NOT have its previous Will fired when it later
// vanishes.
func TestReconnectClearsWill(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "willclear.db")))
	sub := h.NewClient()
	if ca, err := sub.Connect(testsupport.BasicConnectOpts("wcsub", true, 0)); err != nil || ca.Code != 0 {
		t.Fatalf("sub: %+v %v", ca, err)
	}
	if err := sub.SendSubscribe(1, "wc/#", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	w := h.NewClient()
	if ca, err := w.Connect(testsupport.WillConnectOpts("wc-id", false, 0, "wc/t", []byte("old-will"), 0, false)); err != nil || ca.Code != 0 {
		t.Fatalf("will connect: %+v %v", ca, err)
	}
	_ = w.Close()
	waitUntil(t, func() bool { return !h.Broker().HasClient("wc-id") }, 2*time.Second)

	// First abnormal close DID publish the Will — drain it.
	old, err := sub.ReadFrame(2 * time.Second)
	if err != nil {
		t.Fatalf("first will not delivered: %v", err)
	}
	op, _ := old.ParsePublish()
	if op.Topic != "wc/t" || string(op.Payload) != "old-will" {
		t.Fatalf("first will wrong: %+v", op)
	}

	// Reconnect durable WITHOUT a will.
	w2 := h.NewClient()
	ca, err := w2.Connect(testsupport.BasicConnectOpts("wc-id", false, 0))
	if err != nil || !ca.SessionPresent {
		t.Fatalf("reconnect: %+v %v", ca, err)
	}
	_ = w2.Close()
	waitUntil(t, func() bool { return !h.Broker().HasClient("wc-id") }, 2*time.Second)

	if _, err := sub.ReadFrame(400 * time.Millisecond); err == nil {
		t.Fatal("old Will must not fire after a reconnect with no Will")
	}
}

// TestTakeOver: same ClientID reconnecting closes the old connection
// without publishing its Will; the new one owns the session.
func TestTakeOver(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "take.db")))
	sub := h.NewClient()
	if ca, err := sub.Connect(testsupport.BasicConnectOpts("wsub2", true, 0)); err != nil || ca.Code != 0 {
		t.Fatalf("sub: %+v %v", ca, err)
	}
	if err := sub.SendSubscribe(1, "to/will", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	old := h.NewClient()
	if ca, err := old.Connect(testsupport.WillConnectOpts("same-id", false, 0, "to/will", []byte("will-must-not-fire"), 0, false)); err != nil || ca.Code != 0 {
		t.Fatalf("old: %+v %v", ca, err)
	}
	new := h.NewClient()
	if ca, err := new.Connect(testsupport.BasicConnectOpts("same-id", false, 0)); err != nil || ca.Code != 0 {
		t.Fatalf("new: %+v %v", ca, err)
	}
	if err := old.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("old connection not closed on take-over: %v", err)
	}
	waitUntil(t, func() bool { return h.Broker().ClientCount() >= 2 }, 2*time.Second)
	if _, err := sub.ReadFrame(400 * time.Millisecond); err == nil {
		t.Fatal("take-over must not publish Will")
	}
	// New connection is functional.
	if err := new.SendPing(); err != nil {
		t.Fatal(err)
	}
	pf, err := new.ReadFrame(2 * time.Second)
	if err != nil || pf.Type.String() != "PINGRESP" {
		t.Fatalf("new conn ping: %s %v", pf.Type, err)
	}
}

// TestBrokerRestartPersistence verifies subscriptions, inflight, offline
// queues and Will recovery across a full broker process restart (same DB
// file, new listener).
func TestBrokerRestartPersistence(t *testing.T) {
	db := filepath.Join(t.TempDir(), "restart.db")
	cfg := smallConfig(db)
	h := testsupport.StartHarness(t, cfg)

	pub := connectDurable(t, h, "rpub")
	sub := connectDurable(t, h, "rsub")

	// Durable QoS1 watcher that is OFFLINE at the moment of the simulated
	// crash: the crash-recovery Will must land in its offline queue.
	watcher := connectDurable(t, h, "rwatch")
	if err := watcher.SendSubscribe(1, "rwill", 1); err != nil {
		t.Fatal(err)
	}
	if id, _, err := watcher.ReadSuback(2 * time.Second); err != nil || id != 1 {
		t.Fatalf("watcher suback: %v", err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return !h.Broker().HasClient("rwatch") }, 2*time.Second)

	if err := sub.SendSubscribe(1, "r/+", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sub.ReadSuback(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	// A will owner with a durable Will that survives the crash.
	will := h.NewClient()
	if ca, err := will.Connect(testsupport.WillConnectOpts("rwillowner", false, 0, "rwill", []byte("crash-will"), 1, false)); err != nil || ca.Code != 0 {
		t.Fatalf("will connect: %+v %v", ca, err)
	}

	// One inflight message: delivered but not acked.
	if err := pub.SendPublish("r/a", []byte("first"), 1, 10, false, false); err != nil {
		t.Fatal(err)
	}
	pf, err := pub.ReadFrame(2 * time.Second)
	if err != nil || pf.Type.String() != "PUBACK" {
		t.Fatalf("publisher puback: %s %v", pf.Type, err)
	}
	sf, err := sub.ReadFrame(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sf.ParsePublish(); err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return !h.Broker().HasClient("rsub") }, 2*time.Second)
	// Offline message while down.
	if err := pub.SendPublish("r/b", []byte("second"), 1, 11, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pub.ReadFrame(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	logStep(t, nil, "RESTART", "killing broker process equivalent and reopening DB")
	h.Restart()

	// Crash Will is recovered once on startup and queued to the offline
	// durable watcher; it must not be re-published on a second restart.
	w2 := h.NewClient()
	if ca, err := w2.Connect(testsupport.BasicConnectOpts("rwatch", false, 0)); err != nil || !ca.SessionPresent {
		t.Fatalf("watcher reconnect: %+v %v", ca, err)
	}
	// The watcher had no inflight; the crash Will comes as a normal
	// (non-DUP) QoS1 delivery promoted from its offline queue.
	wf, err := w2.ReadFrame(3 * time.Second)
	if err != nil {
		t.Fatalf("crash will not delivered: %v", err)
	}
	wp, err := wf.ParsePublish()
	if err != nil || wp.Topic != "rwill" || string(wp.Payload) != "crash-will" || wp.Dup {
		t.Fatalf("crash will wrong: %+v err=%v", wp, err)
	}
	if err := w2.SendPuback(wp.PacketID); err != nil {
		t.Fatal(err)
	}

	// Durable subscriber reconnects: inflight DUP redelivery + offline msg.
	sub2 := h.NewClient()
	ca, err := sub2.Connect(testsupport.BasicConnectOpts("rsub", false, 0))
	if err != nil || !ca.SessionPresent {
		t.Fatalf("sub reconnect: %+v %v", ca, err)
	}
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		f, err := sub2.ReadFrame(3 * time.Second)
		if err != nil {
			t.Fatalf("post-restart delivery %d: %v", i, err)
		}
		p, _ := f.ParsePublish()
		seen[string(p.Payload)] = p.Dup
		if err := sub2.SendPuback(p.PacketID); err != nil {
			t.Fatal(err)
		}
	}
	if !seen["first"] {
		t.Fatalf("inflight message not redelivered DUP: %v", seen)
	}
	if dup, ok := seen["second"]; !ok || dup {
		t.Fatalf("offline message should deliver non-DUP, got %v", seen)
	}

	// Second restart: crash Will must NOT fire again (it was cleared).
	h.Restart()
	w3 := h.NewClient()
	if ca, err := w3.Connect(testsupport.BasicConnectOpts("rwatch", false, 0)); err != nil || !ca.SessionPresent {
		t.Fatalf("watcher reconnect 2: %+v %v", ca, err)
	}
	if _, err := w3.ReadFrame(500 * time.Millisecond); err == nil {
		t.Fatal("crash Will must fire at most once")
	}
}

func fParseID(f *testsupport.Frame) uint16 {
	p, err := f.ParsePublish()
	if err != nil {
		return 0
	}
	return p.PacketID
}
