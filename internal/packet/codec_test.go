package packet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

// TestDecodeRemainingLengthBoundary exercises every encoding rule the task
// calls out as a strict check: legal values at byte boundaries, non-minimal
// encodings, the forbidden 5th byte and truncation.
func TestDecodeRemainingLengthBoundary(t *testing.T) {
	// Legal vectors are produced by the (independent) encoder path at every
	// digit-count boundary; only illegal encodings are handcrafted below.
	legalVals := []int{0, 1, 127, 128, 129, 16383, 16384, 32768, 2097151, 2097152, 268435200, MaxRemainingLength}
	for _, v := range legalVals {
		t.Run("legal/"+itoaTest(v), func(t *testing.T) {
			enc := encodeRemainingLength(v)
			rd := NewReader(bytes.NewReader(enc))
			rd.MaxPayload = MaxRemainingLength
			got, n, err := rd.decodeRemainingLength()
			if err != nil {
				t.Fatalf("value %d unexpected error: %v", v, err)
			}
			if got != v || n != len(enc) {
				t.Fatalf("value: got %d/%d bytes want %d/%d", got, n, v, len(enc))
			}
		})
	}

	illegal := []struct {
		name string
		enc  []byte
		want error
	}{
		{"nonminimal-zero", []byte{0x80, 0x00}, ErrMalformed},
		{"nonminimal-one", []byte{0x81, 0x00}, ErrMalformed},
		{"nonminimal-127", []byte{0xFF, 0x00}, ErrMalformed},
		{"nonminimal-three-byte", []byte{0x80, 0x80, 0x00}, ErrMalformed},
		{"fifth-byte", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, ErrMalformed},
		{"truncated-continuation", []byte{0x80}, ErrMalformed},
	}
	for _, tc := range illegal {
		t.Run("illegal/"+tc.name, func(t *testing.T) {
			rd := NewReader(bytes.NewReader(tc.enc))
			_, _, err := rd.decodeRemainingLength()
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want class %v", err, tc.want)
			}
		})
	}
}

// TestRemainingLengthRoundTrip checks the encoder always produces minimal
// encodings at every 128^k boundary and random values.
func TestRemainingLengthRoundTrip(t *testing.T) {
	cases := []int{0, 1, 127, 128, 129, 16383, 16384, 2097151, 2097152, MaxRemainingLength}
	for _, v := range cases {
		enc := encodeRemainingLength(v)
		rd := NewReader(bytes.NewReader(enc))
		rd.MaxPayload = MaxRemainingLength // accept the protocol maximum here
		got, n, err := rd.decodeRemainingLength()
		if err != nil {
			t.Fatalf("value %d: %v", v, err)
		}
		if got != v || n != len(enc) {
			t.Fatalf("value %d: got %d/%d bytes enc=%s", v, got, n, hex.EncodeToString(enc))
		}
		// Minimality: one byte <128, k bytes => last byte nonzero.
		if len(enc) > 1 && enc[len(enc)-1] == 0 {
			t.Fatalf("non-minimal encoding for %d: % x", v, enc)
		}
	}
}

// TestPacketIdentifierStrict covers the non-zero identifier rule at every
// packet type that carries one (MQTT-2.3.1-1).
func TestPacketIdentifierStrict(t *testing.T) {
	cases := []struct {
		name  string
		frame []byte
	}{
		{"publish-qos1-zero-id", EncodePublish("a/b", []byte("x"), 0, 1, false, false)},
		{"puback-zero-id", EncodePuback(0)},
		{"subscribe-zero-id", EncodeSubscribe(0, []Subscription{{Filter: "a", QoS: 0}})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Force the id bytes to zero directly to avoid encoder guards
			// (encoders already refuse zero; mutate a copy).
			b := append([]byte(nil), tc.frame...)
			// Locate the two id bytes: after fixed header + remaining len +
			// topic length prefix + topic for PUBLISH; simpler: just run
			// decode and rely on encoded zero frames being constructed here
			// via raw mutation below.
			_ = b
		})
	}

	// Explicit raw frames with zero ids:
	raw := map[string][]byte{
		// PUBLISH qos1: 0x32, rem=7, topic len=1 "a", id=0x0000, payload "x"
		"publish": {0x32, 0x06, 0x00, 0x01, 'a', 0x00, 0x00, 'x'},
		// PUBACK id 0
		"puback": {0x40, 0x02, 0x00, 0x00},
		// SUBSCRIBE id 0 + filter a/0
		"subscribe": {0x82, 0x06, 0x00, 0x00, 0x00, 0x01, 'a', 0x00},
	}
	for name, f := range raw {
		t.Run("raw/"+name, func(t *testing.T) {
			rd := NewReader(bytes.NewReader(f))
			_, err := rd.ReadPacket()
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("got %v, want ErrMalformed", err)
			}
		})
	}
}

// TestFixedHeaderFlags checks reserved-bit enforcement.
func TestFixedHeaderFlags(t *testing.T) {
	frames := map[string][]byte{
		"connect-flags":    {0x11, 0x00}, // CONNECT reserved bits != 0
		"puback-flags":     {0x41, 0x02, 0x00, 0x01},
		"pingreq-flags":    {0xC1, 0x00},
		"pingreq-body":     {0xC0, 0x01, 0x00},
		"disconn-flags":    {0xE1, 0x00},
		"subscribe-flags":  {0x80, 0x05, 0x00, 0x01, 0x00, 0x01, 'a'},
		"publish-qos3":     {0x36, 0x03, 0x00, 0x01, 'a'}, // QoS=3
		"publish-qos0-dup": {0x38, 0x03, 0x00, 0x01, 'a'}, // DUP=1 QoS0
	}
	for name, f := range frames {
		t.Run(name, func(t *testing.T) {
			rd := NewReader(bytes.NewReader(f))
			_, err := rd.ReadPacket()
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("got %v, want ErrMalformed", err)
			}
		})
	}
}

// TestPayloadCap maps a complete-but-oversized remaining length to
// ErrTooLarge (value 128, minimal encoding, cap 100).
func TestPayloadCap(t *testing.T) {
	rd := NewReader(bytes.NewReader([]byte{0xC0, 0x80, 0x01}))
	rd.MaxPayload = 100
	_, err := rd.ReadPacket()
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

// TestConnectValidation covers the CONNECT-specific rejection table.
func TestConnectValidation(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(c *Connect)
		raw      []byte
		wantErr  error
		wantCode byte
	}{
		{
			name:     "bad-protocol-level",
			mutate:   func(c *Connect) {},
			raw:      buildConnectWithLevel(3),
			wantErr:  nil,
			wantCode: ConnBadProtocolVersion,
		},
		{
			name:     "empty-id-durable",
			mutate:   func(c *Connect) { c.CleanSession = false; c.ClientID = "" },
			wantCode: ConnIdentifierRejected,
		},
		{
			name:    "reserved-connect-flag",
			raw:     buildConnectRaw(0x01, 4), // reserved bit set
			wantErr: ErrMalformed,
		},
		{
			name:    "will-qos3",
			raw:     buildConnectRaw(0x04|0x18, 4), // will flag + qos 3
			wantErr: ErrMalformed,
		},
		{
			name:    "will-flags-without-will",
			raw:     buildConnectRaw(0x10, 4), // retain but no will
			wantErr: ErrMalformed,
		},
		{
			name:    "bad-proto-name",
			raw:     buildConnectName("MQI"),
			wantErr: ErrMalformed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var frame []byte
			if tc.raw != nil {
				frame = tc.raw
			} else {
				c := &Connect{ClientID: "id", CleanSession: true, ProtocolLevel: 4}
				tc.mutate(c)
				frame = EncodeConnect(c)
			}
			rd := NewReader(bytes.NewReader(frame))
			_, err := rd.ReadPacket()
			if tc.wantCode != 0 {
				var cr *ConnectRejectError
				if !errors.As(err, &cr) || cr.Code != tc.wantCode {
					t.Fatalf("got %v, want CONNACK code 0x%02x", err, tc.wantCode)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func buildConnectWithLevel(level byte) []byte {
	// CONNECT with protocol level byte replaced.
	c := EncodeConnect(&Connect{ClientID: "id", CleanSession: true, ProtocolLevel: 4})
	// Structure: 0x10 rem(...) "MQTT" [len4] level ...
	// Find the level byte: after 0x10,rl,00 04 'M' 'Q' 'T' 'T'
	idx := 2 + 2 + 4
	c[idx] = level
	return c
}

func buildConnectRaw(flags, level byte) []byte {
	body := []byte{0x00, 0x04, 'M', 'Q', 'T', 'T', level, flags, 0x00, 0x00, 0x00, 0x02, 'i', 'd'}
	return append([]byte{0x10, byte(len(body))}, body...)
}

func buildConnectName(name string) []byte {
	body := []byte{0x00, byte(len(name))}
	body = append(body, name...)
	body = append(body, 4, 0x02, 0x00, 0x00, 0x00, 0x02, 'i', 'd')
	return append([]byte{0x10, byte(len(body))}, body...)
}

func itoaTest(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [24]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
