package broker_test

import (
	"testing"
	"time"

	"mqttd/internal/brokertest"
	"mqttd/internal/mqttclient"
	"mqttd/internal/packet"
)

func TestConnectPingDisconnect(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("ping-client")
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	ev, err := c.NextEvent(time.Second)
	if err != nil || ev.Kind != mqttclient.EvPingresp {
		t.Fatalf("want PINGRESP, got %+v err=%v", ev, err)
	}
	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("active conns to drop", time.Second, func() bool {
		return h.Metrics().ConnectionsActive == 0
	})
}

func TestPubSubQoS0(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("sub0")
	pub := h.Dial("pub0")
	sa, err := sub.Subscribe([]packet.SubFilter{{Topic: "q0/+", QoS: 0}}, time.Second)
	if err != nil || sa.ReturnCodes[0] != 0 {
		t.Fatalf("subscribe = %+v err=%v", sa, err)
	}
	if err := pub.Publish0("q0/temp", []byte("21.5"), false); err != nil {
		t.Fatal(err)
	}
	ev := h.ExpectPublish(sub, time.Second)
	if string(ev.Pub.Payload) != "21.5" || ev.Pub.QoS != 0 || ev.Pub.Topic != "q0/temp" {
		t.Fatalf("unexpected delivery: %+v", ev.Pub)
	}
}

func TestPubSubQoS1RoundTripAndPuback(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("sub1")
	pub := h.Dial("pub1")
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "q1/x", QoS: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	// Publisher QoS 1: broker must PUBACK the inbound publish.
	if err := pub.Publish1(100, "q1/x", []byte("hello-1"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	ev := h.ExpectPublish(sub, time.Second)
	if ev.Pub.QoS != 1 || ev.Pub.PacketID == 0 || ev.Pub.Dup {
		t.Fatalf("first delivery must be QoS1 non-DUP with non-zero id: %+v", ev.Pub)
	}
	if err := sub.Puback(ev.Pub.PacketID); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("inflight cleared after puback", time.Second, func() bool {
		n, _ := h.Broker.Store().CountInflight("sub1")
		return n == 0
	})
}

func TestQoSDowngrade(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("down")
	pub := h.Dial("down-pub")
	// Subscribe at QoS 0, publish at QoS 1: delivery must be downgraded to
	// QoS 0 (no packet id, no broker inflight).
	if _, err := sub.Subscribe([]packet.SubFilter{{Topic: "d/g", QoS: 0}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish1(1, "d/g", []byte("z"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	ev := h.ExpectPublish(sub, time.Second)
	if ev.Pub.QoS != 0 || ev.Pub.PacketID != 0 {
		t.Fatalf("expected downgraded QoS0, got %+v", ev.Pub)
	}
	if n, _ := h.Broker.Store().CountInflight("down"); n != 0 {
		t.Fatalf("downgraded delivery must not create inflight, got %d", n)
	}
}

func TestMultipleFiltersDeliveredOnceAtHighestQoS(t *testing.T) {
	h := brokertest.Start(t)
	sub := h.Dial("multi")
	// Two matching filters, QoS 0 and QoS 1: exactly one QoS 1 delivery.
	if _, err := sub.Subscribe([]packet.SubFilter{
		{Topic: "m/#", QoS: 0},
		{Topic: "m/a", QoS: 1},
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	pub := h.Dial("multi-pub")
	if err := pub.Publish1(1, "m/a", []byte("one"), false, false, time.Second); err != nil {
		t.Fatal(err)
	}
	ev := h.ExpectPublish(sub, 500*time.Millisecond)
	if ev.Pub.QoS != 1 {
		t.Fatalf("expected one QoS1 delivery, got %+v", ev.Pub)
	}
	// ACK it.
	if err := sub.Puback(ev.Pub.PacketID); err != nil {
		t.Fatal(err)
	}
	h.AssertNoPublish(sub, 300*time.Millisecond)
}

func TestSessionPresentSemantics(t *testing.T) {
	h := brokertest.Start(t)
	// First durable connect: SP=false.
	c1, sp := h.DialDurable("sp1")
	if sp {
		t.Fatal("new durable session must report session present=false")
	}
	if err := c1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	h.WaitFor("cleanup", time.Second, func() bool { return h.Metrics().ConnectionsActive == 0 })

	// Reconnect: SP=true.
	c2, sp := h.DialDurable("sp1")
	if !sp {
		t.Fatal("reconnecting durable client must report session present=true")
	}
	c2.Close()

	// A clean session connect must always see SP=false, even taking over a
	// clean live client.
	a := h.Dial("sp2")
	defer a.Close()
	b, ev, err := h.DialOpts(mqttclient.Options{ClientID: "sp2", CleanSession: true})
	if err != nil || ev.Conn.SessionPresent {
		t.Fatalf("clean takeover: SP must be false, ev=%+v err=%v", ev, err)
	}
	b.Close()
}
