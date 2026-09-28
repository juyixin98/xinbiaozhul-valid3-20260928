package packet

import (
	"bytes"
	"testing"
)

// refDecodeRemainingLength is a third, deliberately different
// implementation of the §2.2.3 decode used ONLY as a test oracle: it
// computes each digit's weight with an inner power loop and validates
// termination/minimality by position — no shared state with the
// production stateful decoder.
func refDecodeRemainingLength(b []byte) (int, bool) {
	if len(b) == 0 || len(b) > 4 {
		return 0, false
	}
	var value int
	for i := 0; i < len(b); i++ {
		digit := int(b[i] & 0x7F)
		pow := 1
		for k := 0; k < i; k++ {
			pow *= 128
		}
		value += digit * pow
		cont := b[i]&0x80 != 0
		if i == len(b)-1 {
			if cont {
				return 0, false
			}
			if len(b) > 1 && digit == 0 {
				return 0, false
			}
		} else if !cont {
			return 0, false
		}
	}
	if value > MaxRemainingLength {
		return 0, false
	}
	return value, true
}

// refEncodeRemainingLength derives the encoding via %128 /128 —
// an independent expression of the §2.2.3 math.
func refEncodeRemainingLength(v int) []byte {
	if v < 0 || v > MaxRemainingLength {
		return nil
	}
	var out []byte
	for {
		d := byte(v % 128)
		v /= 128
		if v > 0 {
			d |= 0x80
		}
		out = append(out, d)
		if v == 0 {
			break
		}
	}
	return out
}

func decodeLen(t *testing.T, b []byte) (int, error) {
	t.Helper()
	rd := NewReader(bytes.NewReader(b))
	rd.MaxPayload = MaxRemainingLength
	got, _, err := rd.decodeRemainingLength()
	return got, err
}

// TestReferenceRemainingLength compares production and reference encoders
// across 0..30000 and every digit-count boundary. Expected values come
// from the independent reference above, never from the code under test.
func TestReferenceRemainingLength(t *testing.T) {
	check := func(v int) {
		enc := encodeRemainingLength(v)
		ref := refEncodeRemainingLength(v)
		if len(enc) != len(ref) {
			t.Fatalf("encode len mismatch v=%d prod=%x ref=%x", v, enc, ref)
		}
		for i := range enc {
			if enc[i] != ref[i] {
				t.Fatalf("encode mismatch v=%d prod=%x ref=%x", v, enc, ref)
			}
		}
		rv, ok := refDecodeRemainingLength(ref)
		if !ok || rv != v {
			t.Fatalf("ref decode failed v=%d rv=%d ok=%v", v, rv, ok)
		}
		got, err := decodeLen(t, ref)
		if err != nil || got != v {
			t.Fatalf("prod decode failed v=%d got=%d err=%v", v, got, err)
		}
	}
	for v := 0; v <= 30000; v++ {
		check(v)
	}
	for _, v := range []int{16383, 16384, 2097151, 2097152, 268435200, 268435454, MaxRemainingLength} {
		check(v)
	}
}

// TestReferenceIllegalEncodings enumerates illegal byte patterns the
// reference rejects and ensures the production decoder agrees.
func TestReferenceIllegalEncodings(t *testing.T) {
	bad := [][]byte{
		{0x80, 0x00},             // non-minimal 0
		{0x81, 0x00},             // non-minimal 1
		{0xFF, 0x00},             // non-minimal 127
		{0x80, 0x80, 0x00},       // non-minimal 3-byte
		{0x80, 0x80, 0x80, 0x00}, // non-minimal 4-byte
		{0xFF, 0xFF, 0xFF, 0xFF}, // continuation on 4th -> 5th required
	}
	for _, b := range bad {
		if _, ok := refDecodeRemainingLength(b); ok {
			t.Fatalf("reference accepted illegal % x", b)
		}
		if _, err := decodeLen(t, b); err == nil {
			t.Fatalf("production accepted illegal % x", b)
		}
	}
}
