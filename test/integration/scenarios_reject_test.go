package integration_test

import (
	"path/filepath"
	"testing"
	"time"

	"mqttlocal/internal/packet"
	"mqttlocal/internal/testsupport"
)

// expectRejectedConnect sends a raw/encoded CONNECT and asserts the exact
// CONNACK code followed by transport close.
func expectRejectedConnect(t *testing.T, h *testsupport.Harness, raw []byte, wantCode byte, note string) {
	t.Helper()
	c := h.NewClient()
	if err := c.Send(raw); err != nil {
		t.Fatalf("%s: send: %v", note, err)
	}
	f, err := c.ReadFrame(2 * time.Second)
	if err != nil {
		t.Fatalf("%s: expected CONNACK, got read err %v", note, err)
	}
	if f.Type != packet.TypeCONNACK || len(f.Body) != 2 {
		t.Fatalf("%s: expected CONNACK got %s", note, f.Type)
	}
	if f.Body[1] != wantCode {
		t.Fatalf("%s: code=0x%02x want 0x%02x", note, f.Body[1], wantCode)
	}
	if f.Body[0] != 0 {
		t.Fatalf("%s: Session Present must be 0 on rejection", note)
	}
	if err := c.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("%s: connection not closed after rejection: %v", note, err)
	}
}

// TestRejectConnectConditions covers every CONNECT rejection contract.
func TestRejectConnectConditions(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "rej.db")))

	// Level 3 (MQTT 3.1) -> 0x01.
	level3 := packet.EncodeConnect(&packet.Connect{ClientID: "x", CleanSession: true, ProtocolLevel: 3})
	// EncodeConnect hardcodes level 4; mutate the protocol-level byte at the
	// documented offset (2 fixed hdr + 2 name len + 4 name chars).
	level3[8] = 3
	expectRejectedConnect(t, h, level3, packet.ConnBadProtocolVersion, "protocol level 3")

	// Empty Client ID with CleanSession=0 -> 0x02.
	emptyDurable := packet.EncodeConnect(&packet.Connect{ClientID: "", CleanSession: false, ProtocolLevel: 4})
	expectRejectedConnect(t, h, emptyDurable, packet.ConnIdentifierRejected, "empty id durable")

	// Will QoS 2 -> explicit subset refusal 0x03.
	willQ2 := packet.EncodeConnect(&packet.Connect{
		ClientID: "w", CleanSession: true, ProtocolLevel: 4,
		HasWill: true, WillTopic: "w/t", WillMessage: []byte("bye"), WillQoS: 2,
	})
	expectRejectedConnect(t, h, willQ2, packet.ConnServerUnavailable, "will qos2")
}

// TestRejectMalformedConnect asserts structural violations close the
// transport WITHOUT a CONNACK (the server cannot trust the frame).
func TestRejectMalformedConnect(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "mal.db")))

	cases := map[string][]byte{
		// reserved Connect Flag bit set
		"reserved-flag": {0x10, 0x0D, 0x00, 0x04, 'M', 'Q', 'T', 'T', 4, 0x01, 0x00, 0x00, 0x00, 0x01, 'x'},
		// non-minimal remaining length: 0x80 0x00
		"nonminimal-rl": {0x10, 0x80, 0x00},
		// bad protocol name
		"bad-name": {0x10, 0x0D, 0x00, 0x04, 'M', 'Q', 'T', 'X', 4, 0x02, 0x00, 0x00, 0x00, 0x01, 'x'},
		// truncated body (declares 10 bytes, gives 2)
		"truncated": {0x10, 0x0A, 0x00, 0x04},
		// will qos 3
		"will-qos3": {0x10, 0x10, 0x00, 0x04, 'M', 'Q', 'T', 'T', 4, 0x1C, 0x00, 0x00, 0x00, 0x01, 'x', 0x00, 0x01, 'a'},
		// password flag without username (MQTT-3.1.2-20)
		"password-no-username": {0x10, 0x0D, 0x00, 0x04, 'M', 'Q', 'T', 'T', 4, 0x42, 0x00, 0x00, 0x00, 0x01, 'x'},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			c := h.NewClient()
			if err := c.Send(raw); err != nil {
				t.Fatal(err)
			}
			// Either close with no reply, or in no case a successful CONNACK.
			f, err := c.ReadFrame(2 * time.Second)
			if err == nil && f.Type == packet.TypeCONNACK && f.Body[1] == 0 {
				t.Fatalf("%s: violation produced successful CONNACK", name)
			}
			if err := c.ExpectClose(2 * time.Second); err != nil {
				t.Fatalf("%s: violation did not close connection: %v", name, err)
			}
		})
	}
}

// TestRejectPacketTooLarge maps over-cap remaining length to a close.
func TestRejectPacketTooLarge(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "big.db")))
	c := h.NewClient()
	// Remaining length 5000 (minimal: 0x88 0x27) exceeds 4096 cap.
	if err := c.Send([]byte{0x10, 0x88, 0x27}); err != nil {
		t.Fatal(err)
	}
	if err := c.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("oversized CONNECT not closed: %v", err)
	}
}

// TestSubscribeQoS2FailureCode verifies QoS 2 in SUBSCRIBE is refused per
// filter with SUBACK 0x80 while the connection stays alive.
func TestSubscribeQoS2FailureCode(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "q2sub.db")))
	c := h.NewClient()
	ca, err := c.Connect(testsupport.ConnectOpts{ClientID: "q2", CleanSession: true})
	if err != nil || ca.Code != 0 {
		t.Fatalf("connect: %+v %v", ca, err)
	}
	// Mixed filters: qos1 accepted, qos2 -> 0x80.
	mixed := packet.EncodeSubscribe(7, []packet.Subscription{{Filter: "ok/+", QoS: 1}, {Filter: "want2/+", QoS: 2}})
	if err := c.Send(mixed); err != nil {
		t.Fatal(err)
	}
	id, granted, err := c.ReadSuback(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if id != 7 || len(granted) != 2 || granted[0] != 1 || granted[1] != packet.SubAckFailure {
		t.Fatalf("suback id=%d granted=%v", id, granted)
	}
	// Connection is still alive: PING works.
	if err := c.SendPing(); err != nil {
		t.Fatal(err)
	}
	pf, err := c.ReadFrame(2 * time.Second)
	if err != nil || pf.Type != packet.TypePINGRESP {
		t.Fatalf("post-SUBACK connection not alive: frame=%s err=%v", pf.Type, err)
	}
}

// TestRejectProtocolViolations exercises each close-producing violation
// after a successful CONNECT.
func TestRejectProtocolViolations(t *testing.T) {
	cases := []struct {
		name string
		send func(c *testsupport.Client) error
	}{
		{
			"publish-qos2",
			func(c *testsupport.Client) error {
				return c.Send(packet.EncodePublish("q2/a", []byte("z"), 5, 2, false, false))
			},
		},
		{
			"publish-qos0-dup",
			func(c *testsupport.Client) error {
				// 0x38 = PUBLISH DUP=1 QoS0
				return c.Send([]byte{0x38, 0x05, 0x00, 0x01, 'a', 0x00, 'z'})
			},
		},
		{
			"publish-wildcard-topic",
			func(c *testsupport.Client) error {
				return c.Send([]byte{0x30, 0x05, 0x00, 0x01, '+', 0x00, 'z'})
			},
		},
		{
			"pubrec",
			func(c *testsupport.Client) error { return c.Send([]byte{0x50, 0x02, 0x00, 0x01}) },
		},
		{
			"pubrel",
			func(c *testsupport.Client) error { return c.Send([]byte{0x62, 0x02, 0x00, 0x01}) },
		},
		{
			"pubcomp",
			func(c *testsupport.Client) error { return c.Send([]byte{0x70, 0x02, 0x00, 0x01}) },
		},
		{
			"unsubscribe",
			func(c *testsupport.Client) error {
				return c.Send([]byte{0xA2, 0x05, 0x00, 0x01, 0x00, 0x01, 'a'})
			},
		},
		{
			"subscribe-bad-reserved",
			func(c *testsupport.Client) error {
				// SUBSCRIBE with reserved bits 0000 instead of 0010.
				return c.Send([]byte{0x80, 0x06, 0x00, 0x01, 0x00, 0x01, 'a', 0x00})
			},
		},
		{
			"publish-qos3",
			func(c *testsupport.Client) error {
				return c.Send([]byte{0x36, 0x05, 0x00, 0x01, 'a', 0x00, 'z'})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "v.db")))
			c := h.NewClient()
			ca, err := c.Connect(testsupport.ConnectOpts{ClientID: "v-" + tc.name, CleanSession: true})
			if err != nil || ca.Code != 0 {
				t.Fatalf("connect: %+v %v", ca, err)
			}
			if err := tc.send(c); err != nil {
				t.Fatal(err)
			}
			if err := c.ExpectClose(2 * time.Second); err != nil {
				t.Fatalf("violation %q did not close: %v", tc.name, err)
			}
		})
	}
}

// TestStateConflicts covers packets that are invalid by connection state.
func TestStateConflicts(t *testing.T) {
	h := testsupport.StartHarness(t, smallConfig(filepath.Join(t.TempDir(), "state.db")))

	// First packet not CONNECT.
	c1 := h.NewClient()
	if err := c1.SendPing(); err != nil {
		t.Fatal(err)
	}
	if err := c1.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("PING before CONNECT must close: %v", err)
	}

	// Second CONNECT on one transport.
	c2 := h.NewClient()
	ca, err := c2.Connect(testsupport.ConnectOpts{ClientID: "two", CleanSession: true})
	if err != nil || ca.Code != 0 {
		t.Fatalf("connect: %+v %v", ca, err)
	}
	if err := c2.Send(packet.EncodeConnect(&packet.Connect{ClientID: "two", CleanSession: true, ProtocolLevel: 4})); err != nil {
		t.Fatal(err)
	}
	if err := c2.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("second CONNECT must close: %v", err)
	}

	// PUBACK referring to an unknown identifier.
	c3 := h.NewClient()
	if ca, err := c3.Connect(testsupport.ConnectOpts{ClientID: "unk", CleanSession: true}); err != nil || ca.Code != 0 {
		t.Fatalf("connect: %+v %v", ca, err)
	}
	if err := c3.SendPuback(999); err != nil {
		t.Fatal(err)
	}
	if err := c3.ExpectClose(2 * time.Second); err != nil {
		t.Fatalf("PUBACK for unknown id must close: %v", err)
	}
}

// TestKeepAliveTimeout verifies a silent client past 1.5x Keep Alive is
// disconnected with the keep-alive reason.
func TestKeepAliveTimeout(t *testing.T) {
	cfg := smallConfig(filepath.Join(t.TempDir(), "ka.db"))
	h := testsupport.StartHarness(t, cfg)
	c := h.NewClient()
	ca, err := c.Connect(testsupport.ConnectOpts{ClientID: "ka", CleanSession: true, KeepAlive: 1})
	if err != nil || ca.Code != 0 {
		t.Fatalf("connect: %+v %v", ca, err)
	}
	// Stay silent; deadline is 1*1.5 (+1s scheduling margin in the server).
	start := time.Now()
	if err := c.ExpectClose(5 * time.Second); err != nil {
		t.Fatalf("keep-alive timeout did not close: %v", err)
	}
	if elapsed := time.Since(start); elapsed < time.Duration(1.4*float64(time.Second)) {
		t.Fatalf("closed too early at %s (must honor ~1.5x)", elapsed)
	}
	// A pinging client must survive beyond the deadline.
	c2 := h.NewClient()
	if ca, err := c2.Connect(testsupport.ConnectOpts{ClientID: "ka2", CleanSession: true, KeepAlive: 1}); err != nil || ca.Code != 0 {
		t.Fatalf("connect: %+v %v", ca, err)
	}
	for i := 0; i < 4; i++ {
		if err := c2.SendPing(); err != nil {
			t.Fatal(err)
		}
		if _, err := c2.ReadFrame(2 * time.Second); err != nil {
			t.Fatalf("pingresp %d: %v", i, err)
		}
		time.Sleep(700 * time.Millisecond)
	}
}
