package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestRemainingLengthRoundTrip(t *testing.T) {
	cases := []struct {
		value int
		bytes []byte
	}{
		{0, []byte{0x00}},
		{127, []byte{0x7F}},
		{128, []byte{0x80, 0x01}},
		{16383, []byte{0xFF, 0x7F}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{2097151, []byte{0xFF, 0xFF, 0x7F}},
		{2097152, []byte{0x80, 0x80, 0x80, 0x01}},
		{MaxRemainingLength, []byte{0xFF, 0xFF, 0xFF, 0x7F}},
	}
	for _, tc := range cases {
		got, err := EncodeRemainingLength(tc.value)
		if err != nil || !bytes.Equal(got, tc.bytes) {
			t.Fatalf("encode %d = %v, err=%v; want %v", tc.value, got, err, tc.bytes)
		}
		v, used, err := DecodeRemainingLength(tc.bytes)
		if err != nil || v != tc.value || used != len(tc.bytes) {
			t.Fatalf("decode %v = (%d,%d,%v); want %d", tc.bytes, v, used, err, tc.value)
		}
	}
}

func TestRemainingLengthRejectConditions(t *testing.T) {
	bad := map[string][]byte{
		"truncated continuation":        {0x80},
		"continuation on 4th byte":      {0x80, 0x80, 0x80, 0x80},
		"fifth byte required":           {0x80, 0x80, 0x80, 0x80, 0x01},
		"non-minimal zero contribution": {0x80, 0x00},
		"non-minimal 3 byte":            {0xFF, 0x80, 0x00},
	}
	for name, b := range bad {
		_, _, err := DecodeRemainingLength(b)
		if err == nil {
			t.Errorf("%s: expected rejection, got nil", name)
			continue
		}
		if pe := AsError(err); pe == nil || pe.Cat != CatInvalid {
			t.Errorf("%s: want CatInvalid, got %v", name, err)
		}
	}
}

func TestEncodeRemainingLengthBounds(t *testing.T) {
	if _, err := EncodeRemainingLength(-1); err == nil {
		t.Fatal("negative remaining length accepted")
	}
	if _, err := EncodeRemainingLength(MaxRemainingLength + 1); err == nil {
		t.Fatal("oversized remaining length accepted")
	}
}

func TestReadFrameRejectsOverLimit(t *testing.T) {
	// CONNECT first byte + remaining length 200 encoded as 2 bytes; limit 100.
	var buf bytes.Buffer
	buf.WriteByte(0x10)
	buf.Write([]byte{0xC8, 0x01})
	buf.Write(make([]byte, 200))
	_, err := ReadFrame(&buf, 100)
	if err == nil {
		t.Fatal("frame above limit accepted")
	}
	pe := AsError(err)
	if pe == nil || pe.Cat != CatLimit {
		t.Fatalf("want CatLimit, got %v", err)
	}
}

func TestReadFrameTruncatedPayload(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x30)
	buf.Write([]byte{0x0A}) // claims 10 bytes
	buf.Write([]byte{0, 3, 'a', '/', 'b'})
	_, err := ReadFrame(&buf, 1024)
	if err == nil {
		t.Fatal("truncated frame accepted")
	}
}

func TestConnectDecodeRejects(t *testing.T) {
	base := func(flags, level byte, extra ...byte) []byte {
		var b Buffer
		b.String(ProtocolName)
		b.Byte(level)
		b.Byte(flags)
		b.Uint16(30) // keep alive
		b.String("cid")
		b.Bytes(extra)
		return b.B
	}
	t.Run("bad protocol name", func(t *testing.T) {
		var b Buffer
		b.String("MQ")
		b.Byte(ProtocolLevel)
		b.Byte(0x02)
		b.Uint16(30)
		b.String("cid")
		_, err := DecodeConnect(b.B)
		if err == nil || AsError(err) == nil || AsError(err).Cat != CatUnsupported {
			t.Fatalf("want unsupported protocol name, got %v", err)
		}
	})
	t.Run("bad protocol level", func(t *testing.T) {
		_, err := DecodeConnect(base(0x02, 3))
		if _, ok := IsProtoLevel(err); !ok {
			t.Fatalf("want protoLevelError, got %v", err)
		}
	})
	t.Run("reserved bit set", func(t *testing.T) {
		if _, err := DecodeConnect(base(0x03, 4)); err == nil {
			t.Fatal("reserved flag bit accepted")
		}
	})
	t.Run("will qos 3", func(t *testing.T) {
		if _, err := DecodeConnect(base(0x04|0x18, 4)); err == nil {
			t.Fatal("will QoS 3 accepted")
		}
	})
	t.Run("will fields without will flag", func(t *testing.T) {
		// retain set, flag clear
		if _, err := DecodeConnect(base(0x20, 4)); err == nil {
			t.Fatal("will retain without will flag accepted")
		}
	})
	t.Run("password without username", func(t *testing.T) {
		if _, err := DecodeConnect(base(0x80|0x02, 4)); err == nil {
			t.Fatal("password flag without username accepted")
		}
	})
	t.Run("will qos 2 structurally decodes (broker rejects by policy)", func(t *testing.T) {
		// Packet layer accepts QoS 2 as well-formed; the subset policy refusal
		// belongs to the broker. Verify packet layer does not reject it.
		var b Buffer
		b.String(ProtocolName)
		b.Byte(4)
		b.Byte(0x04 | 0x10 | 0x02) // will flag + will qos 2 + clean
		b.Uint16(30)
		b.String("cid")
		b.String("wt")
		b.Binary([]byte("m"))
		p, err := DecodeConnect(b.B)
		if err != nil {
			t.Fatalf("QoS 2 will should be structurally valid, got %v", err)
		}
		if p.WillQoS != 2 {
			t.Fatalf("will qos = %d, want 2", p.WillQoS)
		}
	})
}

func TestPublishDecodeValidation(t *testing.T) {
	mk := func(flags byte, topic string, pid *uint16, payload []byte) []byte {
		var b Buffer
		b.String(topic)
		if pid != nil {
			b.Uint16(*pid)
		}
		b.Bytes(payload)
		return b.B
	}
	pid := uint16(7)
	t.Run("qos1 ok", func(t *testing.T) {
		p, err := DecodePublish(0x02, mk(0x02, "a/b", &pid, []byte("x")))
		if err != nil || p.PacketID != 7 || p.QoS != 1 {
			t.Fatalf("decode = %+v, %v", p, err)
		}
	})
	t.Run("qos1 zero pid", func(t *testing.T) {
		zero := uint16(0)
		if _, err := DecodePublish(0x02, mk(0x02, "a/b", &zero, nil)); err == nil {
			t.Fatal("zero packet identifier accepted")
		}
	})
	t.Run("qos2 unsupported", func(t *testing.T) {
		_, err := DecodePublish(0x04, mk(0x04, "a/b", &pid, nil))
		pe := AsError(err)
		if pe == nil || pe.Cat != CatUnsupported {
			t.Fatalf("want unsupported, got %v", err)
		}
	})
	t.Run("qos3 reserved", func(t *testing.T) {
		if _, err := DecodePublish(0x06, mk(0x06, "a/b", &pid, nil)); err == nil {
			t.Fatal("QoS 3 accepted")
		}
	})
	t.Run("dup on qos0", func(t *testing.T) {
		if _, err := DecodePublish(0x08, mk(0x08, "a/b", nil, nil)); err == nil {
			t.Fatal("DUP=1 with QoS 0 accepted")
		}
	})
	t.Run("qos0 missing pid is fine", func(t *testing.T) {
		p, err := DecodePublish(0x00, mk(0, "a/b", nil, []byte("hi")))
		if err != nil || p.QoS != 0 || string(p.Payload) != "hi" {
			t.Fatalf("decode = %+v, %v", p, err)
		}
	})
	t.Run("wildcard topic rejected at broker layer not packet", func(t *testing.T) {
		// Packet layer is transport-valid; topics.ValidateName rejects '#'.
		p, err := DecodePublish(0x00, mk(0, "a/+", nil, nil))
		if err != nil {
			t.Fatalf("packet decode should not care about wildcard, got %v", err)
		}
		_ = p
	})
}

func TestSubscribeValidation(t *testing.T) {
	good := func() []byte {
		var b Buffer
		b.Uint16(1)
		b.String("a/+")
		b.Byte(1)
		return b.B
	}
	s, err := DecodeSubscribe(good())
	if err != nil || len(s.Filters) != 1 || s.Filters[0].QoS != 1 {
		t.Fatalf("decode good subscribe failed: %v %+v", err, s)
	}

	t.Run("zero packet id", func(t *testing.T) {
		var b Buffer
		b.Uint16(0)
		b.String("x")
		b.Byte(0)
		if _, err := DecodeSubscribe(b.B); err == nil {
			t.Fatal("zero packet id accepted")
		}
	})
	t.Run("qos 3", func(t *testing.T) {
		var b Buffer
		b.Uint16(1)
		b.String("x")
		b.Byte(3)
		if _, err := DecodeSubscribe(b.B); err == nil {
			t.Fatal("QoS 3 accepted")
		}
	})
	t.Run("reserved bits", func(t *testing.T) {
		var b Buffer
		b.Uint16(1)
		b.String("x")
		b.Byte(0x04)
		if _, err := DecodeSubscribe(b.B); err == nil {
			t.Fatal("reserved subscribe option bit accepted")
		}
	})
	t.Run("empty filter list", func(t *testing.T) {
		var b Buffer
		b.Uint16(1)
		if _, err := DecodeSubscribe(b.B); err == nil {
			t.Fatal("subscribe without filters accepted")
		}
	})
	t.Run("qos2 structurally decodes (broker refuses in SUBACK)", func(t *testing.T) {
		var b Buffer
		b.Uint16(2)
		b.String("x")
		b.Byte(2)
		s, err := DecodeSubscribe(b.B)
		if err != nil {
			t.Fatalf("QoS2 filter should be structurally valid: %v", err)
		}
		if s.Filters[0].QoS != 2 {
			t.Fatalf("qos = %d", s.Filters[0].QoS)
		}
	})
}

func TestPubackValidation(t *testing.T) {
	if _, err := DecodePuback([]byte{0, 0}); err == nil {
		t.Fatal("PUBACK id 0 accepted")
	}
	if _, err := DecodePuback([]byte{0}); err == nil {
		t.Fatal("1-byte PUBACK accepted")
	}
	if _, err := DecodePuback([]byte{0, 1, 2}); err == nil {
		t.Fatal("3-byte PUBACK accepted")
	}
}

func TestConnackSessionPresentBit(t *testing.T) {
	// SP=0.
	if p, err := DecodeConnack([]byte{0x00, 0x00}); err != nil || p.SessionPresent || p.ReturnCode != 0 {
		t.Fatalf("connack sp0: %+v err=%v", p, err)
	}
	// SP=1 accepted (this regression caused reconnect CONNACKs to be rejected).
	if p, err := DecodeConnack([]byte{0x01, 0x00}); err != nil || !p.SessionPresent {
		t.Fatalf("connack sp1: %+v err=%v", p, err)
	}
	// Reserved bits 7..1 set must be rejected.
	if _, err := DecodeConnack([]byte{0x02, 0x00}); err == nil {
		t.Fatal("connack reserved bit 1 accepted")
	}
	if _, err := DecodeConnack([]byte{0x01}); err == nil {
		t.Fatal("truncated connack accepted")
	}
}

func TestConnectEncodeRoundTrip(t *testing.T) {
	orig := &Connect{
		CleanSession: true, KeepAlive: 60, ClientID: "abc",
		WillFlag: true, WillQoS: 1, WillRetain: true,
		WillTopic: "w/t", WillMessage: []byte("bye"),
		UsernameFlag: true, Username: "u",
		PasswordFlag: true, Password: []byte("p"),
	}
	frame := orig.Encode()
	if frame[0] != TypeConnect<<4 {
		t.Fatalf("first byte = 0x%02x", frame[0])
	}
	_, used, err := DecodeRemainingLength(frame[1:])
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeConnect(frame[1+used:])
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "abc" || !got.CleanSession || got.KeepAlive != 60 ||
		!got.WillFlag || got.WillQoS != 1 || !got.WillRetain ||
		got.WillTopic != "w/t" || string(got.WillMessage) != "bye" ||
		got.Username != "u" || string(got.Password) != "p" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestSubscribeFixedFlagsEncoding(t *testing.T) {
	frame := (&Subscribe{PacketID: 1, Filters: []SubFilter{{Topic: "x", QoS: 1}}}).Encode()
	if frame[0] != (TypeSubscribe<<4)|0x02 {
		t.Fatalf("SUBSCRIBE first byte = 0x%02x, want 0x82", frame[0])
	}
}

func TestUTF8Validation(t *testing.T) {
	var b Buffer
	b.String(ProtocolName)
	b.Byte(ProtocolLevel)
	b.Byte(0x02)
	b.Uint16(0)
	// Length-prefixed invalid UTF-8 client id.
	b.Bytes([]byte{0x00, 0x03, 0xFF, 0xFE, 0xFD})
	if _, err := DecodeConnect(b.B); err == nil {
		t.Fatal("invalid UTF-8 in client id accepted")
	}
}

// guard against accidental drift in the 16-bit length encoding.
func TestUint16Encoding(t *testing.T) {
	var b Buffer
	b.Uint16(0x0102)
	if !bytes.Equal(b.B, []byte{0x01, 0x02}) {
		t.Fatalf("big-endian encoding wrong: %v", b.B)
	}
	if binary.BigEndian.Uint16(b.B) != 0x0102 {
		t.Fatal("endianness drift")
	}
}
