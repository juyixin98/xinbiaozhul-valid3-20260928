package broker_test

import (
	"bytes"
	"testing"
	"time"

	"mqttd/internal/brokertest"
	"mqttd/internal/mqttclient"
	"mqttd/internal/packet"
)

// readConnackOrClose performs a raw connect and returns either the CONNACK
// payload (2 bytes) or nil if the broker closed the connection without one.
func rawConnect(t *testing.T, h *brokertest.Harness, frame []byte) (connack []byte, closed bool) {
	t.Helper()
	nc := h.RawConn()
	if _, err := nc.Write(frame); err != nil {
		t.Fatalf("write: %v", err)
	}
	nc.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	fr, err := packet.ReadFrame(nc, 4096)
	if err != nil {
		return nil, true // closed / no response
	}
	typ, _ := packet.FrameHeader(fr[0])
	if typ != packet.TypeConnack {
		t.Fatalf("expected CONNACK or close, got type %d", typ)
	}
	_, used, err := packet.DecodeRemainingLength(fr[1:])
	if err != nil {
		t.Fatal(err)
	}
	return fr[1+used:], false
}

func connectFrame(level byte, flags byte, clientID string, extra ...byte) []byte {
	var b packet.Buffer
	b.String(packet.ProtocolName)
	b.Byte(level)
	b.Byte(flags)
	b.Uint16(30)
	b.String(clientID)
	b.Bytes(extra)
	return prepend(packet.TypeConnect<<4, b.B)
}

func prepend(first byte, body []byte) []byte {
	rl, _ := packet.EncodeRemainingLength(len(body))
	out := append([]byte{first}, rl...)
	return append(out, body...)
}

// 1) Wrong protocol level -> CONNACK return code 1, then close.
func TestRejectBadProtocolLevel(t *testing.T) {
	h := brokertest.Start(t)
	ca, closed := rawConnect(t, h, connectFrame(3, 0x02, "c"))
	if closed || ca == nil {
		t.Fatal("expected CONNACK code 1, connection closed instead")
	}
	if ca[1] != packet.ConnackBadProto {
		t.Fatalf("return code = %d, want 1", ca[1])
	}
}

// 2) Wrong protocol name -> CONNACK code 1.
func TestRejectBadProtocolName(t *testing.T) {
	h := brokertest.Start(t)
	var b packet.Buffer
	b.String("MQIsdp") // 3.1 name
	b.Byte(4)
	b.Byte(0x02)
	b.Uint16(30)
	b.String("c")
	ca, closed := rawConnect(t, h, prepend(packet.TypeConnect<<4, b.B))
	if closed || ca == nil || ca[1] != packet.ConnackBadProto {
		t.Fatalf("bad protocol name: ca=%v closed=%v", ca, closed)
	}
}

// 3) Empty client id with clean session=false -> CONNACK code 2.
func TestRejectEmptyIDPersistent(t *testing.T) {
	h := brokertest.Start(t)
	ca, _ := rawConnect(t, h, connectFrame(4, 0x00, ""))
	if ca == nil || ca[1] != packet.ConnackIDRejected {
		t.Fatalf("empty id non-clean: ca=%v", ca)
	}
}

// 4) Empty client id with clean=true is accepted (broker assigns an id).
func TestEmptyIDCleanAccepted(t *testing.T) {
	h := brokertest.Start(t)
	ca, closed := rawConnect(t, h, connectFrame(4, 0x02, ""))
	if closed || ca == nil || ca[1] != packet.ConnackAccepted {
		t.Fatalf("empty id clean: ca=%v closed=%v", ca, closed)
	}
}

// 5) QoS 2 will -> refused (subset), and the connection is closed.
func TestRejectWillQoS2(t *testing.T) {
	h := brokertest.Start(t)
	var b packet.Buffer
	b.String(packet.ProtocolName)
	b.Byte(4)
	b.Byte(0x04 | 0x10 | 0x02) // will flag, will qos=2, clean
	b.Uint16(30)
	b.String("q2will")
	b.String("w/t")
	b.Binary([]byte("x"))
	ca, _ := rawConnect(t, h, prepend(packet.TypeConnect<<4, b.B))
	if ca == nil {
		t.Fatal("expected a CONNACK refusal for QoS 2 will")
	}
	if ca[1] != packet.ConnackServerUnavailable {
		t.Fatalf("will qos2 return code = %d, want 3", ca[1])
	}
}

// 6) Malformed first packet: not CONNECT -> silent protocol close, no CONNACK.
func TestRejectFirstPacketNotConnect(t *testing.T) {
	h := brokertest.Start(t)
	nc := h.RawConn()
	nc.Write((&packet.Pingreq{}).Encode())
	nc.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4)
	if _, err := nc.Read(buf); err == nil {
		t.Fatalf("broker must close on PINGREQ-before-CONNECT, got bytes % x", buf)
	}
	if h.Metrics().FramesInvalid == 0 {
		t.Fatal("invalid frame counter not incremented")
	}
}

// 7) CONNECT reserved flag bit -> close (malformed, no CONNACK).
func TestRejectConnectReservedBit(t *testing.T) {
	h := brokertest.Start(t)
	if _, closed := rawConnect(t, h, connectFrame(4, 0x03, "c")); !closed {
		t.Fatal("reserved-bit CONNECT must be closed without CONNACK")
	}
}

// 8) Malformed remaining length: continuation on 4th byte -> close.
func TestRejectBadRemainingLength(t *testing.T) {
	h := brokertest.Start(t)
	nc := h.RawConn()
	// First byte CONNECT, then a remaining-length encoding that requires a
	// 5th byte, which the spec forbids.
	nc.Write([]byte{0x10, 0x80, 0x80, 0x80, 0x80, 0x01})
	nc.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 8)
	if _, err := nc.Read(buf); err == nil {
		t.Fatal("broker must close on over-long remaining length")
	}
	if h.Metrics().FramesInvalid == 0 {
		t.Fatal("invalid frame counter not incremented")
	}
}

// 9) Advertised remaining length beyond broker max -> close, limit metric.
func TestRejectOversizedPacket(t *testing.T) {
	h := brokertest.Start(t, brokertest.WithMaxPacket(64))
	nc := h.RawConn()
	// CONNECT claiming 200 bytes.
	nc.Write([]byte{0x10, 0xC8, 0x01})
	nc.Write(make([]byte, 200))
	nc.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 8)
	if _, err := nc.Read(buf); err == nil {
		t.Fatal("oversized packet must close the connection")
	}
}

// 10) QoS 2 PUBLISH (inbound) -> connection closed as unsupported.
func TestRejectPublishQoS2(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("q2pub")
	pub := &packet.Publish{QoS: 2, PacketID: 1, Topic: "x", Payload: []byte("y")}
	if err := c.SendRaw(pub.Encode()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("connection must be closed after QoS2 PUBLISH")
	}
	if h.Metrics().FramesUnsupported == 0 {
		t.Fatal("unsupported counter not incremented")
	}
}

// 11) QoS 2 SUBSCRIBE filter -> per-filter SUBACK 0x80, connection stays up.
func TestRejectSubscribeQoS2PerFilter(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("q2sub")
	sub := &packet.Subscribe{
		PacketID: 1,
		Filters: []packet.SubFilter{
			{Topic: "ok/x", QoS: 0},
			{Topic: "q2/x", QoS: 2},
			{Topic: "bad#x", QoS: 0}, // malformed filter
		},
	}
	if err := c.SendRaw(sub.Encode()); err != nil {
		t.Fatal(err)
	}
	ev, err := c.NextEvent(time.Second)
	if err != nil || ev.Kind != mqttclient.EvSuback {
		t.Fatalf("want SUBACK, got %+v err=%v", ev, err)
	}
	want := []byte{0x00, packet.SubackFailure, packet.SubackFailure}
	if !bytes.Equal(ev.Sub.ReturnCodes, want) {
		t.Fatalf("suback codes = % x, want % x", ev.Sub.ReturnCodes, want)
	}
	// Connection still usable: PING works.
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	if ev, err := c.NextEvent(time.Second); err != nil || ev.Kind != mqttclient.EvPingresp {
		t.Fatalf("connection not usable after partial SUBACK refusal: %v", err)
	}
}

// 12) SUBSCRIBE with wrong fixed flags -> protocol close.
func TestRejectSubscribeWrongFlags(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("subflags")
	sub := (&packet.Subscribe{PacketID: 1, Filters: []packet.SubFilter{{Topic: "x", QoS: 0}}}).Encode()
	sub[0] = (packet.TypeSubscribe << 4) | 0x00 // flags must be 0x2
	if err := c.SendRaw(sub); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("wrong SUBSCRIBE flags must close the connection")
	}
}

// 13) QoS1 PUBLISH with packet id 0 -> protocol close.
func TestRejectPublishZeroPacketID(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("zero")
	pub := &packet.Publish{QoS: 1, PacketID: 0, Topic: "x"}
	if err := c.SendRaw(pub.Encode()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("zero packet id must close the connection")
	}
}

// 14) Wildcard in PUBLISH topic -> protocol close.
func TestRejectPublishWildcardTopic(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("wc")
	if err := c.Publish0("a/+/c", []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("wildcard PUBLISH topic must close the connection")
	}
}

// 15) PUBACK packet id 0 -> protocol close.
func TestRejectPubackZero(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("pa0")
	if err := c.SendRaw((&packet.Puback{PacketID: 0}).Encode()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("PUBACK id 0 must close the connection")
	}
}

// 16) Unsupported packet type (PUBREL, type 6) -> unsupported close.
func TestRejectQoS2FamilyPacket(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("pubrel")
	// PUBREL: type 6, fixed flags 0x2, payload packet id 1.
	frame := []byte{0x62, 0x02, 0x00, 0x01}
	if err := c.SendRaw(frame); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("PUBREL must close the connection")
	}
	if h.Metrics().FramesInvalid == 0 {
		t.Fatal("invalid/unsupported counter not incremented")
	}
}

// 17) UNSUBSCRIBE (defined feature outside this subset) -> unsupported close.
func TestRejectUnsubscribe(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("unsub")
	// Type 10, fixed flags 0x2, payload packet id 1 + filter "x". Built from
	// raw bytes deliberately so the codec needs no unsupported encoder.
	var body packet.Buffer
	body.Uint16(1)
	body.String("x")
	unsub := []byte{0xA2}
	rl, _ := packet.EncodeRemainingLength(len(body.B))
	unsub = append(unsub, rl...)
	unsub = append(unsub, body.B...)
	if err := c.SendRaw(unsub); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("UNSUBSCRIBE must close the connection")
	}
	if h.Metrics().FramesUnsupported == 0 {
		t.Fatal("unsupported counter not incremented for UNSUBSCRIBE")
	}
}

// 18) Reserved packet type 15 -> protocol close.
func TestRejectReservedType15(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("r15")
	if err := c.SendRaw([]byte{0xF0, 0x00}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("reserved type 15 must close the connection")
	}
}

// 19) Server->client packet (SUBACK type 9) arriving from a client -> close.
func TestRejectWrongDirectionPacket(t *testing.T) {
	h := brokertest.Start(t)
	c := h.Dial("wrongdir")
	frame := (&packet.Suback{PacketID: 1, ReturnCodes: []byte{0}}).Encode()
	if err := c.SendRaw(frame); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("SUBACK from client must close the connection")
	}
}
