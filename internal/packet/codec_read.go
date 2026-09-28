package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxRemainingLength is the MQTT 3.1.1 limit: a remaining length field may
// encode at most 268,435,455 (MQTT-2.2.3-1).
const MaxRemainingLength = 268_435_455

// maxRemainingBytes is the hard cap on bytes materialised into memory. The
// protocol allows 256 MiB; a local subset broker refuses anything near that
// (configurable on the reader). ReadPacket takes an explicit limit.
const defaultMaxPacketPayload = 1 << 24 // 16 MiB

type byteReader struct {
	r io.Reader
}

func (br *byteReader) readByte() (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(br.r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// Reader decodes inbound packets. MaxPayload caps accepted remaining length;
// zero means the 16 MiB default.
type Reader struct {
	r          io.Reader
	br         byteReader
	MaxPayload int
}

// NewReader wraps r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r, br: byteReader{r: r}, MaxPayload: defaultMaxPacketPayload}
}

// Read reads exactly n bytes into a newly allocated slice, mapping truncated
// frames to ErrMalformed only when at least one body byte already arrived.
func (rd *Reader) readBody(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rd.r, buf); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("%w: packet body truncated: %v", ErrMalformed, err)
	}
	return buf, nil
}

// decodeRemainingLength implements MQTT 3.1.1 §2.2.3 and additionally
// rejects:
//   - non-minimal encodings: any encoding of more than one byte whose final
//     digit is zero (e.g. [0x80 0x00] for 0, [0x81 0x00] for 1). The minimal
//     encoding of 128 is [0x80 0x01] — its FIRST digit is zero, which is
//     fine; only a zero FINAL digit carries no bits.
//   - encodings requiring a 5th byte, which cannot represent legal values
//     (MQTT-2.2.3-2),
//   - values above the reader's own resource cap (ErrTooLarge).
//
// It returns (value, bytesConsumed, error).
func (rd *Reader) decodeRemainingLength() (int, int, error) {
	value := 0
	multiplier := 1
	consumed := 0
	lastDigit := 0
	for {
		b, err := rd.br.readByte()
		if err != nil {
			if consumed > 0 && errors.Is(err, io.EOF) {
				// First packet byte was already read; a length field that
				// ends mid-encoding is a malformed/truncated frame, not a
				// clean end-of-stream.
				return 0, consumed, fmt.Errorf("%w: remaining length truncated: %w", ErrMalformed, io.ErrUnexpectedEOF)
			}
			return 0, consumed, err
		}
		consumed++
		digit := int(b & 0x7F)
		value += digit * multiplier
		lastDigit = digit

		if b&0x80 == 0 {
			break
		}
		if consumed >= 4 {
			// The continuation bit is set ON the 4th byte: a 5th byte
			// would be required, which is forbidden (MQTT-2.2.3-2). The
			// maximum 268435455 is encoded 0xFF 0xFF 0xFF 0x7F with the
			// 4th bit clear.
			return 0, consumed, fmt.Errorf("%w: remaining length exceeds 4 bytes / 268435455", ErrMalformed)
		}
		multiplier *= 128
	}
	if consumed > 1 && lastDigit == 0 {
		return 0, consumed, fmt.Errorf("%w: non-minimal remaining length encoding", ErrMalformed)
	}
	if value > MaxRemainingLength {
		return 0, consumed, fmt.Errorf("%w: remaining length %d > 268435455", ErrMalformed, value)
	}
	cap := rd.MaxPayload
	if cap <= 0 {
		cap = defaultMaxPacketPayload
	}
	if value > cap {
		return 0, consumed, fmt.Errorf("%w: remaining length %d exceeds broker cap %d", ErrTooLarge, value, cap)
	}
	return value, consumed, nil
}

// ReadPacket reads one complete packet. A clean transport EOF before the first
// byte returns io.EOF; every other framing violation wraps ErrMalformed.
func (rd *Reader) ReadPacket() (*Packet, error) {
	first, err := rd.br.readByte()
	if err != nil {
		return nil, err // io.EOF propagates unchanged
	}
	pType := Type(first >> 4)
	flags := first & 0x0F

	rem, _, err := rd.decodeRemainingLength()
	if err != nil {
		return nil, err
	}
	body, err := rd.readBody(rem)
	if err != nil {
		return nil, err
	}
	d := &decoder{buf: body}

	pk := &Packet{Type: pType, Flags: flags}
	switch pType {
	case TypeCONNECT:
		pk.Connect, err = d.decodeConnect(flags)
	case TypePUBLISH:
		pk.Publish, err = d.decodePublish(flags)
	case TypePUBACK:
		pk.PubAck, err = d.decodePubAck(flags)
	case TypeSUBSCRIBE:
		pk.Subscribe, err = d.decodeSubscribe(flags)
	case TypePINGREQ:
		err = d.expectEmpty(flags, TypePINGREQ, 0b0000)
		if err == nil {
			pk.PingReq = true
		}
	case TypeDISCONNECT:
		err = d.expectEmpty(flags, TypeDISCONNECT, 0b0000)
		if err == nil {
			pk.Disconnect = true
		}
	case TypePUBREC, TypePUBREL, TypePUBCOMP, TypeUNSUBSCRIBE, TypeUNSUBACK:
		err = fmt.Errorf("%w: %s is not part of this subset", ErrUnsupported, pType)
	case TypeCONNACK, TypeSUBACK, TypePINGRESP:
		err = fmt.Errorf("%w: %s is server-to-client only", ErrUnsupported, pType)
	default:
		err = fmt.Errorf("%w: reserved packet type %d", ErrMalformed, pType)
	}
	if err != nil {
		return nil, err
	}
	return pk, nil
}

// decoder is a cursor over the fixed remaining-length body.
type decoder struct {
	buf []byte
	pos int
}

func (d *decoder) remaining() int { return len(d.buf) - d.pos }

func (d *decoder) byte_(what string) (byte, error) {
	if d.remaining() < 1 {
		return 0, fmt.Errorf("%w: truncated %s", ErrMalformed, what)
	}
	b := d.buf[d.pos]
	d.pos++
	return b, nil
}

func (d *decoder) u16(what string) (uint16, error) {
	if d.remaining() < 2 {
		return 0, fmt.Errorf("%w: truncated %s", ErrMalformed, what)
	}
	v := binary.BigEndian.Uint16(d.buf[d.pos:])
	d.pos += 2
	return v, nil
}

func (d *decoder) bytes(what string) ([]byte, error) {
	n, err := d.u16(what + " length prefix")
	if err != nil {
		return nil, err
	}
	if int(n) > d.remaining() {
		return nil, fmt.Errorf("%w: %s length %d exceeds body (%d bytes left)", ErrMalformed, what, n, d.remaining())
	}
	out := make([]byte, n)
	copy(out, d.buf[d.pos:d.pos+int(n)])
	d.pos += int(n)
	return out, nil
}

func (d *decoder) mqttString(what string) (string, error) {
	b, err := d.bytes(what)
	if err != nil {
		return "", err
	}
	if !ValidMQTTUTF8(b) {
		return "", fmt.Errorf("%w: %s contains a forbidden UTF-8 sequence (MQTT-1.5.3)", ErrMalformed, what)
	}
	return string(b), nil
}

func (d *decoder) done() error {
	if d.remaining() != 0 {
		return fmt.Errorf("%w: %d trailing byte(s) past declared fields", ErrMalformed, d.remaining())
	}
	return nil
}

func (d *decoder) expectEmpty(flags byte, t Type, wantFlags byte) error {
	if flags != wantFlags {
		return fmt.Errorf("%w: %s fixed header flags must be %04b, got %04b (MQTT fixed header rules)", ErrMalformed, t, wantFlags, flags)
	}
	if len(d.buf) != 0 {
		return fmt.Errorf("%w: %s must have zero remaining length, got %d", ErrMalformed, t, len(d.buf))
	}
	return nil
}

func (d *decoder) decodeConnect(flags byte) (*Connect, error) {
	if flags != 0b0000 {
		return nil, fmt.Errorf("%w: CONNECT reserved fixed-header flags must be 0, got %04b", ErrMalformed, flags)
	}
	// Protocol Name: "MQTT" (MQTT 3.1.1 §3.1.2.1).
	protoName, err := d.mqttString("Protocol Name")
	if err != nil {
		return nil, err
	}
	if protoName != "MQTT" {
		return nil, fmt.Errorf("%w: protocol name %q is not \"MQTT\"", ErrMalformed, protoName)
	}
	level, err := d.byte_("Protocol Level")
	if err != nil {
		return nil, err
	}
	if level != 4 {
		return nil, &ConnectRejectError{Code: ConnBadProtocolVersion, Reason: fmt.Sprintf("unsupported protocol level %d (only MQTT 3.1.1 level 4)", level)}
	}
	c := &Connect{ProtocolLevel: level}

	connectFlags, err := d.byte_("Connect Flags")
	if err != nil {
		return nil, err
	}
	c.CleanSession = connectFlags&0x02 != 0
	c.HasWill = connectFlags&0x04 != 0
	willQoS := (connectFlags >> 3) & 0x03
	c.WillRetain = connectFlags&0x20 != 0
	c.HasPassword = connectFlags&0x40 != 0
	c.HasUsername = connectFlags&0x80 != 0

	// Reserved bit of the Connect Flags MUST be 0 (MQTT-3.1.2-3).
	if connectFlags&0x01 != 0 {
		return nil, fmt.Errorf("%w: CONNECT reserved flag bit is set (MQTT-3.1.2-3)", ErrMalformed)
	}
	// Will QoS 3 (0b11) is a protocol violation (MQTT-3.1.2-12).
	if willQoS == 3 {
		return nil, fmt.Errorf("%w: Will QoS 3 is a reserved value (MQTT-3.1.2-12)", ErrMalformed)
	}
	// Will flags consistency (MQTT-3.1.2-9/10/11).
	if !c.HasWill && (willQoS != 0 || c.WillRetain) {
		return nil, fmt.Errorf("%w: Will QoS/Retain set while Will Flag is 0 (MQTT-3.1.2-9)", ErrMalformed)
	}
	// Password Flag MUST be 0 if User Name Flag is 0 (MQTT-3.1.2-19/20).
	if c.HasPassword && !c.HasUsername {
		return nil, fmt.Errorf("%w: Password Flag set without User Name Flag (MQTT-3.1.2-20)", ErrMalformed)
	}
	c.WillQoS = willQoS

	c.KeepAlive, err = d.u16("Keep Alive")
	if err != nil {
		return nil, err
	}
	c.ClientID, err = d.mqttString("Client ID")
	if err != nil {
		return nil, err
	}

	if c.HasWill {
		c.WillTopic, err = d.mqttString("Will Topic")
		if err != nil {
			return nil, err
		}
		if !ValidTopicName(c.WillTopic) {
			return nil, fmt.Errorf("%w: Will Topic is not a valid topic name", ErrMalformed)
		}
		c.WillMessage, err = d.bytes("Will Message")
		if err != nil {
			return nil, err
		}
	}
	if c.HasUsername {
		c.Username, err = d.mqttString("User Name")
		if err != nil {
			return nil, err
		}
	}
	if c.HasPassword {
		c.Password, err = d.bytes("Password")
		if err != nil {
			return nil, err
		}
	}
	if err := d.done(); err != nil {
		return nil, err
	}
	// Empty Client ID is allowed only with CleanSession=1 (MQTT-3.1.3-7/8).
	if c.ClientID == "" && !c.CleanSession {
		return nil, &ConnectRejectError{Code: ConnIdentifierRejected, Reason: "empty Client ID requires CleanSession=1 (MQTT-3.1.3-8)"}
	}
	return c, nil
}

func (d *decoder) decodePublish(flags byte) (*Publish, error) {
	p := &Publish{
		Dup:    flags&0x08 != 0,
		QoS:    (flags >> 1) & 0x03,
		Retain: flags&0x01 != 0,
	}
	// MQTT-3.3.1-2..8:
	switch {
	case p.QoS == 3:
		return nil, fmt.Errorf("%w: PUBLISH QoS 3 is reserved (MQTT-3.3.1-4)", ErrMalformed)
	case p.QoS == 0 && p.Dup:
		return nil, fmt.Errorf("%w: DUP=1 on QoS 0 PUBLISH is a protocol violation (MQTT-3.3.1-2)", ErrMalformed)
	}
	topic, err := d.mqttString("PUBLISH topic name")
	if err != nil {
		return nil, err
	}
	if !ValidTopicName(topic) {
		return nil, fmt.Errorf("%w: PUBLISH topic %q is not a valid topic name (MQTT-4.7)", ErrMalformed, topic)
	}
	p.Topic = topic
	if p.QoS > 0 {
		p.PacketID, err = d.u16("PUBLISH packet identifier")
		if err != nil {
			return nil, err
		}
		if p.PacketID == 0 {
			return nil, fmt.Errorf("%w: PUBLISH (QoS %d) packet identifier must be non-zero (MQTT-2.3.1-1)", ErrMalformed, p.QoS)
		}
	} else {
		p.PacketID = 0
	}
	// Everything remaining is the payload (MQTT-3.3.1-4 body rule: payload
	// MAY be empty; no trailing-field ambiguity for QoS 0/1).
	p.Payload = make([]byte, d.remaining())
	copy(p.Payload, d.buf[d.pos:])
	d.pos = len(d.buf)
	return p, nil
}

func (d *decoder) decodePubAck(flags byte) (*PubAck, error) {
	if flags != 0b0000 {
		return nil, fmt.Errorf("%w: PUBACK reserved flags must be 0, got %04b", ErrMalformed, flags)
	}
	id, err := d.u16("PUBACK packet identifier")
	if err != nil {
		return nil, err
	}
	if id == 0 {
		return nil, fmt.Errorf("%w: PUBACK packet identifier must be non-zero (MQTT-2.3.1-1)", ErrMalformed)
	}
	if err := d.done(); err != nil {
		return nil, err
	}
	return &PubAck{PacketID: id}, nil
}

func (d *decoder) decodeSubscribe(flags byte) (*Subscribe, error) {
	// SUBSCRIBE fixed header reserved bits MUST be 0010 (MQTT-3.8.1-1).
	if flags != 0b0010 {
		return nil, fmt.Errorf("%w: SUBSCRIBE fixed header reserved bits must be 0010, got %04b (MQTT-3.8.1-1)", ErrMalformed, flags)
	}
	s := &Subscribe{}
	var err error
	s.PacketID, err = d.u16("SUBSCRIBE packet identifier")
	if err != nil {
		return nil, err
	}
	if s.PacketID == 0 {
		return nil, fmt.Errorf("%w: SUBSCRIBE packet identifier must be non-zero (MQTT-2.3.1-1)", ErrMalformed)
	}
	if d.remaining() == 0 {
		return nil, fmt.Errorf("%w: SUBSCRIBE must contain at least one topic filter (MQTT-3.8.3-3)", ErrMalformed)
	}
	for d.remaining() > 0 {
		filter, err := d.mqttString("topic filter")
		if err != nil {
			return nil, err
		}
		qos, err := d.byte_("subscription requested QoS")
		if err != nil {
			return nil, err
		}
		switch qos {
		case 0, 1:
			// supported granted levels
		case 2:
			// Structurally legal in MQTT 3.1.1 but this subset does not
			// implement QoS 2; the broker grants failure 0x80 in SUBACK
			// rather than closing (documented rejection contract).
		default:
			return nil, fmt.Errorf("%w: subscription QoS %d is reserved (MQTT-3.8.3-4)", ErrMalformed, qos)
		}
		s.Subscriptions = append(s.Subscriptions, Subscription{Filter: filter, QoS: qos})
	}
	if len(s.Subscriptions) == 0 {
		return nil, fmt.Errorf("%w: SUBSCRIBE payload contains no filters", ErrMalformed)
	}
	return s, nil
}
