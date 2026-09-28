package packet

import (
	"encoding/binary"
	"io"
)

// MaxRemainingLength is the MQTT 3.1.1 ceiling: the 4-byte remaining length
// field can represent at most 268 435 455.
const MaxRemainingLength = 268_435_455

// EncodeRemainingLength encodes a remaining length using the variable-length
// encoding of MQTT 3.1.1 section 2.2.3 (1..4 bytes, base 128).
func EncodeRemainingLength(value int) ([]byte, error) {
	if value < 0 {
		return nil, New(CatInvalid, "negative remaining length %d", value)
	}
	if value > MaxRemainingLength {
		return nil, New(CatLimit, "remaining length %d exceeds %d", value, MaxRemainingLength)
	}
	out := make([]byte, 0, 4)
	for {
		enc := byte(value % 128)
		value /= 128
		if value > 0 {
			enc |= 0x80
		}
		out = append(out, enc)
		if value == 0 {
			break
		}
	}
	return out, nil
}

// DecodeRemainingLength decodes the variable-length field from the beginning of
// b. It is strict on the MQTT 3.1.1 wire rules:
//   - at most 4 encoded bytes; a continuation bit on the 4th byte is malformed;
//   - only the canonical minimal encoding is accepted (a zero contribution
//     byte followed by more bytes is malformed);
//   - a truncated encoding (b ends with the continuation bit set) is malformed.
//
// It returns the value and the number of bytes consumed.
func DecodeRemainingLength(b []byte) (value int, used int, err error) {
	multiplier := 1
	for i := 0; i < 4; i++ {
		if i >= len(b) {
			return 0, 0, ErrTruncated
		}
		encoded := b[i]
		digit := encoded & 0x7F
		value += int(digit) * multiplier
		used++
		if encoded&0x80 == 0 {
			// Non-minimal encoding: the terminating byte contributes zero
			// while earlier bytes exist (e.g. 127 encoded as 0xFF 0x00).
			// Canonical encoders always use the fewest bytes.
			if digit == 0 && used > 1 {
				return 0, 0, New(CatInvalid, "non-minimal remaining length encoding")
			}
			return value, used, nil
		}
		multiplier *= 128
	}
	// Continuation bit set on a 5th byte: violates the 4-byte maximum.
	return 0, 0, New(CatInvalid, "remaining length exceeds 4 encoded bytes")
}

// ReadFrame reads one complete MQTT control packet from r. The returned slice
// contains the full frame (fixed header + variable header + payload).
//
// maxPacket bounds the advertised remaining length; exceeding it is reported
// as CatLimit so callers can distinguish resource exhaustion from malformed
// bytes. A stream that ends mid-frame is CatInvalid (truncated).
func ReadFrame(r io.Reader, maxPacket int) ([]byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return nil, err
	}
	var lenBuf [4]byte
	used := 0
	for {
		if _, err := io.ReadFull(r, lenBuf[used:used+1]); err != nil {
			return nil, err
		}
		used++
		if lenBuf[used-1]&0x80 == 0 {
			break // terminating byte reached
		}
		if used == 4 {
			// A continuation bit set on the 4th byte would require a 5th
			// byte, which MQTT forbids.
			return nil, New(CatInvalid, "remaining length exceeds 4 encoded bytes")
		}
	}
	rem, _, err := DecodeRemainingLength(lenBuf[:used])
	if err != nil {
		return nil, err
	}
	if rem > maxPacket {
		return nil, New(CatLimit, "remaining length %d exceeds broker max packet size %d", rem, maxPacket)
	}
	frame := make([]byte, 1+used+rem)
	frame[0] = first[0]
	copy(frame[1:], lenBuf[:used])
	if rem > 0 {
		if _, err := io.ReadFull(r, frame[1+used:]); err != nil {
			return nil, New(CatInvalid, "truncated packet payload: %v", err)
		}
	}
	return frame, nil
}

// FrameHeader parses the first byte of a frame into type and flags.
func FrameHeader(first byte) (typ byte, flags byte) {
	return first >> 4, first & 0x0F
}

// --- small encoder buffer ---------------------------------------------------

// Buffer is a growable byte buffer with MQTT primitive encoders.
type Buffer struct{ B []byte }

func (w *Buffer) Byte(b byte)    { w.B = append(w.B, b) }
func (w *Buffer) Bytes(b []byte) { w.B = append(w.B, b...) }
func (w *Buffer) Uint16(v uint16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	w.B = append(w.B, tmp[:]...)
}
func (w *Buffer) String(s string) { w.Uint16(uint16(len(s))); w.B = append(w.B, s...) }
func (w *Buffer) Binary(d []byte) { w.Uint16(uint16(len(d))); w.B = append(w.B, d...) }

// Frame emits the control first byte, followed by the body prefixed by its
// remaining length.
func (w *Buffer) Frame(first byte, body []byte) {
	rl, err := EncodeRemainingLength(len(body))
	if err != nil {
		// All bodies emitted by this implementation are within bounds.
		panic(err)
	}
	w.Byte(first)
	w.Bytes(rl)
	w.Bytes(body)
}
