package broker_test

import (
	"testing"
	"time"

	"mqttd/internal/brokertest"
	"mqttd/internal/mqttclient"
	"mqttd/internal/packet"
)

// TestTakeover: a second connection with the same client id disconnects the
// first; the first observes closure with a takeover reason, the second works.
func TestTakeover(t *testing.T) {
	h := brokertest.Start(t)
	first := h.Dial("taken")
	if err := first.Ping(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.NextEvent(time.Second); err != nil {
		t.Fatal(err) // drain PINGRESP
	}
	second := h.Dial("taken")
	select {
	case <-first.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("first connection should be closed on takeover")
	}
	if err := second.Ping(); err != nil {
		t.Fatalf("new connection must work after takeover: %v", err)
	}
	if ev, err := second.NextEvent(time.Second); err != nil || ev.Kind != mqttclient.EvPingresp {
		t.Fatalf("no PINGRESP after takeover: %v", err)
	}
	h.WaitFor("takeover metric", time.Second, func() bool { return h.Metrics().TakenOver == 1 })
}

// TestWillOnAbnormalClose: abrupt close (no DISCONNECT) publishes the will;
// a clean DISCONNECT suppresses it.
func TestWillOnAbnormalCloseAndCleanSuppress(t *testing.T) {
	h := brokertest.Start(t)
	watcher := h.Dial("watch")
	if _, err := watcher.Subscribe([]packet.SubFilter{{Topic: "will/+", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}

	// Client with a will, closed abruptly (no DISCONNECT).
	dying, _, err := h.DialOpts(mqttclient.Options{
		ClientID: "dying", CleanSession: true,
		Will: &mqttclient.Will{Topic: "will/dying", Payload: []byte("bye"), QoS: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	dying.Close()
	ev := h.ExpectPublish(watcher, 2*time.Second)
	if ev.Pub.Topic != "will/dying" || string(ev.Pub.Payload) != "bye" {
		t.Fatalf("will not delivered correctly: %+v", ev.Pub)
	}
	if err := watcher.Puback(ev.Pub.PacketID); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("will metric", time.Second, func() bool { return h.Metrics().WillPublished == 1 })

	// A client that DISCONNECTs cleanly must NOT trigger its will.
	graceful, _, err := h.DialOpts(mqttclient.Options{
		ClientID: "graceful", CleanSession: true,
		Will: &mqttclient.Will{Topic: "will/graceful", Payload: []byte("nope"), QoS: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graceful.Disconnect(); err != nil {
		t.Fatal(err)
	}
	h.AssertNoPublish(watcher, 400*time.Millisecond)
}

// TestKeepAliveTimeout: a client that advertises a small keep-alive and then
// goes silent is disconnected by the broker (1.5x factor).
func TestKeepAliveTimeout(t *testing.T) {
	h := brokertest.Start(t)
	c, _, err := h.DialOpts(mqttclient.Options{
		ClientID: "ka", CleanSession: true, KeepAlive: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the client socket open but send no PINGREQ. Broker deadline is
	// 1.5s; allow generous slack.
	select {
	case <-c.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("silent client with keep-alive=1 should have been disconnected")
	}
}

// TestKeepAliveResetByPing: periodic PINGREQ keeps the connection alive past
// the deadline.
func TestKeepAliveResetByPing(t *testing.T) {
	h := brokertest.Start(t)
	c, _, err := h.DialOpts(mqttclient.Options{
		ClientID: "ka2", CleanSession: true, KeepAlive: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.Ping(); err != nil {
			t.Fatalf("ping: %v", err)
		}
		ev, err := c.NextEvent(time.Second)
		if err != nil || ev.Kind != mqttclient.EvPingresp {
			t.Fatalf("want PINGRESP: %+v err=%v", ev, err)
		}
		time.Sleep(600 * time.Millisecond)
	}
	select {
	case <-c.Done():
		t.Fatal("connection should stay alive with periodic PING")
	default:
	}
}

// TestRestartPersistence: stop the broker, reopen the same database file with
// a new broker instance; durable subscriptions and unacked inflight messages
// survive and are replayed (with DUP=1).
func TestRestartPersistence(t *testing.T) {
	dbPath := t.TempDir() + "/restart.db"

	h := brokertest.StartWithDB(t, dbPath)
	sub, sp := h.DialDurable("persist-sub")
	if sp {
		t.Fatal("unexpected SP on first connect")
	}
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "p/+", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	sub.Close()
	pub := h.Dial("persist-pub")
	if err := pub.Publish1(1, "p/x", []byte("across-restart"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("offline inflight durable", time.Second, func() bool {
		n, _ := h.Broker.Store().CountInflight("persist-sub")
		return n == 1
	})

	// Restart broker against the same DB.
	h2 := brokertest.RestartWithDB(t, h, dbPath)
	c2, ev, err := mqttclient.Dial(h2.Addr, mqttclient.Options{ClientID: "persist-sub", CleanSession: false})
	if err != nil {
		t.Fatalf("dial restarted broker: %v", err)
	}
	if !ev.Conn.SessionPresent {
		t.Fatal("session present must survive broker restart")
	}
	// Subscriptions were reloaded into the trie; unacked inflight is
	// replayed with DUP=1.
	ev2 := h2.ExpectPublish(c2, 3*time.Second)
	if string(ev2.Pub.Payload) != "across-restart" || !ev2.Pub.Dup {
		t.Fatalf("post-restart replay wrong: %+v", ev2.Pub)
	}
}
