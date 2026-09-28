package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Connect is an MQTT CONNECT packet (3.1).
type Connect struct {
	CleanSession bool
	WillFlag     bool
	WillQoS      byte
	WillRetain   bool
	WillTopic    string
	WillMessage  []byte
	UsernameFlag bool
	PasswordFlag bool
	Username     string
	Password     []byte
	KeepAlive    uint16
	ClientID     string
}

// Connack is an MQTT CONNACK packet (3.2).
type Connack struct {
	SessionPresent bool
	ReturnCode     byte
}

// Publish is an MQTT PUBLISH packet (3.3).
type Publish struct {
	Dup      bool
	QoS      byte
	Retain   bool
	Topic    string
	PacketID uint16 // 0 for QoS 0
	Payload  []byte
}

// Puback is an MQTT PUBACK packet (3.4).
type Puback struct{ PacketID uint16 }

// Subscribe is an MQTT SUBSCRIBE packet (3.8).
type Subscribe struct {
	PacketID uint16
	Filters  []SubFilter
}

// SubFilter is one filter/QoS pair in a SUBSCRIBE.
type SubFilter struct {
	Topic string
	QoS   byte
}

// Suback is an MQTT SUBACK packet (3.9).
type Suback struct {
	PacketID uint16
	// ReturnCodes carries 0 (QoS0 granted) / 1 (QoS1 granted) / 0x80 failure.
	ReturnCodes []byte
}

// Pingreq, Pingresp, Disconnect carry no fields.
type Pingreq struct{}
type Pingresp struct{}
type Disconnect struct{}

// cursor parses a fixed-size payload with bounds checks and emits CatInvalid
// errors on every violation.
type cursor struct {
	b   []byte
	pos int
}

func (c *cursor) remaining() int { return len(c.b) - c.pos }

func (c *cursor) need(n int, what string) error {
	if c.remaining() < n {
		return New(CatInvalid, "%s: need %d bytes, have %d", what, n, c.remaining())
	}
	return nil
}

func (c *cursor) byte_(what string) (byte, error) {
	if err := c.need(1, what); err != nil {
		return 0, err
	}
	v := c.b[c.pos]
	c.pos++
	return v, nil
}

func (c *cursor) u16(what string) (uint16, error) {
	if err := c.need(2, what); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint16(c.b[c.pos:])
	c.pos += 2
	return v, nil
}

// stringData reads a length-prefixed UTF-8 string. MQTT string validation
// (1.5.3/1.5.4): valid UTF-8, no embedded U+0000.
func (c *cursor) stringData(what string) (string, error) {
	n, err := c.u16(what + " length")
	if err != nil {
		return "", err
	}
	if err := c.need(int(n), what); err != nil {
		return "", err
	}
	s := c.b[c.pos : c.pos+int(n)]
	c.pos += int(n)
	if !utf8.Valid(s) {
		return "", New(CatInvalid, "%s: invalid UTF-8", what)
	}
	for _, ch := range string(s) {
		if ch == 0 {
			return "", New(CatInvalid, "%s: embedded NUL is forbidden", what)
		}
	}
	return string(s), nil
}

func (c *cursor) binaryData(what string) ([]byte, error) {
	n, err := c.u16(what + " length")
	if err != nil {
		return nil, err
	}
	if err := c.need(int(n), what); err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, c.b[c.pos:c.pos+int(n)])
	c.pos += int(n)
	return out, nil
}

// exactEnd ensures no trailing bytes remain.
func (c *cursor) exactEnd(what string) error {
	if c.pos != len(c.b) {
		return New(CatInvalid, "%s: %d trailing byte(s)", what, len(c.b)-c.pos)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Decode
// ---------------------------------------------------------------------------

// DecodeConnect parses the payload (everything after the fixed header) of a
// CONNECT packet. Payload field order and flag combinations are enforced per
// MQTT 3.1.1 3.1.2/3.1.3.
func DecodeConnect(body []byte) (*Connect, error) {
	c := &cursor{b: body}
	name, err := c.stringData("protocol name")
	if err != nil {
		return nil, err
	}
	if name != ProtocolName {
		return nil, New(CatUnsupported, "unknown protocol name %q", name)
	}
	level, err := c.byte_("protocol level")
	if err != nil {
		return nil, err
	}
	if level != ProtocolLevel {
		return nil, &protoLevelError{level: level}
	}
	flags, err := c.byte_("connect flags")
	if err != nil {
		return nil, err
	}
	ka, err := c.u16("keep alive")
	if err != nil {
		return nil, err
	}

	p := &Connect{
		CleanSession: flags&0x02 != 0,
		WillFlag:     flags&0x04 != 0,
		WillQoS:      (flags >> 3) & 0x03,
		WillRetain:   flags&0x20 != 0,
		PasswordFlag: flags&0x80 != 0,
		UsernameFlag: flags&0x40 != 0,
		KeepAlive:    ka,
	}
	if flags&0x01 != 0 {
		return nil, New(CatInvalid, "CONNECT reserved flag bit must be 0")
	}
	if p.WillQoS == 3 {
		return nil, New(CatInvalid, "will QoS 3 is reserved")
	}
	if !p.WillFlag && (p.WillQoS != 0 || p.WillRetain) {
		return nil, New(CatInvalid, "will qos/retain set while will flag is 0")
	}
	// MQTT-3.1.2-22.
	if p.PasswordFlag && !p.UsernameFlag {
		return nil, New(CatInvalid, "password flag set without username flag")
	}

	if p.ClientID, err = c.stringData("client id"); err != nil {
		return nil, err
	}
	if p.WillFlag {
		if p.WillTopic, err = c.stringData("will topic"); err != nil {
			return nil, err
		}
		if p.WillMessage, err = c.binaryData("will message"); err != nil {
			return nil, err
		}
	}
	if p.UsernameFlag {
		if p.Username, err = c.stringData("username"); err != nil {
			return nil, err
		}
	}
	if p.PasswordFlag {
		if p.Password, err = c.binaryData("password"); err != nil {
			return nil, err
		}
	}
	if err := c.exactEnd("CONNECT"); err != nil {
		return nil, err
	}
	return p, nil
}

// protoLevelError lets the broker map a bad level to CONNACK code 1 without
// misclassifying other malformed packets.
type protoLevelError struct{ level byte }

func (e *protoLevelError) Error() string {
	return fmt.Sprintf("unsupported protocol level: %d", e.level)
}

// IsProtoLevel reports whether err is an unsupported protocol level error.
func IsProtoLevel(err error) (byte, bool) {
	var ple *protoLevelError
	if errors.As(err, &ple) {
		return ple.level, true
	}
	return 0, false
}

// DecodeConnack parses a CONNACK payload (2 bytes). Byte 0 is the Connect
// Acknowledge Flags: only bit 0 (Session Present) is defined; bits 7..1 are
// reserved and must be zero (3.2.2.1).
func DecodeConnack(body []byte) (*Connack, error) {
	if len(body) != 2 {
		return nil, New(CatInvalid, "CONNACK payload must be 2 bytes, got %d", len(body))
	}
	if body[0]&0xFE != 0 {
		return nil, New(CatInvalid, "CONNACK acknowledge flags reserved bits 7..1 must be 0 (got 0x%02x)", body[0])
	}
	return &Connack{SessionPresent: body[0]&0x01 != 0, ReturnCode: body[1]}, nil
}

// DecodePublish parses a PUBLISH payload. The broker calls this only after
// verifying the fixed-header flags: dup/qos/retain.
func DecodePublish(flags byte, body []byte) (*Publish, error) {
	c := &cursor{b: body}
	topic, err := c.stringData("topic name")
	if err != nil {
		return nil, err
	}
	p := &Publish{
		Dup:    flags&0x08 != 0,
		QoS:    (flags >> 1) & 0x03,
		Retain: flags&0x01 != 0,
		Topic:  topic,
	}
	switch {
	case p.QoS == 3:
		return nil, New(CatInvalid, "PUBLISH QoS 3 is reserved")
	case p.QoS == 2:
		return nil, New(CatUnsupported, "PUBLISH QoS 2 is not supported by this broker")
	}
	if p.QoS > 0 {
		pid, err := c.u16("packet identifier")
		if err != nil {
			return nil, err
		}
		if pid == 0 {
			return nil, New(CatInvalid, "PUBLISH QoS%d packet identifier must be non-zero", p.QoS)
		}
		p.PacketID = pid
	} else if p.Dup {
		return nil, New(CatInvalid, "PUBLISH DUP flag must be 0 for QoS 0")
	}
	p.Payload = append([]byte(nil), c.b[c.pos:]...)
	return p, nil
}

// DecodePuback parses a PUBACK payload (exactly packet identifier).
func DecodePuback(body []byte) (*Puback, error) {
	if len(body) != 2 {
		return nil, New(CatInvalid, "PUBACK payload must be 2 bytes, got %d", len(body))
	}
	pid := binary.BigEndian.Uint16(body)
	if pid == 0 {
		return nil, New(CatInvalid, "PUBACK packet identifier must be non-zero")
	}
	return &Puback{PacketID: pid}, nil
}

// DecodeSubscribe parses a SUBSCRIBE payload.
func DecodeSubscribe(body []byte) (*Subscribe, error) {
	c := &cursor{b: body}
	pid, err := c.u16("packet identifier")
	if err != nil {
		return nil, err
	}
	if pid == 0 {
		return nil, New(CatInvalid, "SUBSCRIBE packet identifier must be non-zero")
	}
	s := &Subscribe{PacketID: pid}
	for c.remaining() > 0 {
		filter, err := c.stringData("topic filter")
		if err != nil {
			return nil, err
		}
		opts, err := c.byte_("subscribe options")
		if err != nil {
			return nil, err
		}
		qos := opts & 0x03
		if qos == 3 {
			return nil, New(CatInvalid, "SUBSCRIBE QoS 3 is reserved")
		}
		if opts&0xFC != 0 {
			return nil, New(CatInvalid, "SUBSCRIBE reserved option bits must be 0 (got 0x%02x)", opts)
		}
		s.Filters = append(s.Filters, SubFilter{Topic: filter, QoS: qos})
	}
	if len(s.Filters) == 0 {
		return nil, New(CatInvalid, "SUBSCRIBE must contain at least one topic filter")
	}
	return s, nil
}

// DecodeSuback parses a SUBACK payload (used by test clients).
func DecodeSuback(body []byte) (*Suback, error) {
	if len(body) < 3 {
		return nil, New(CatInvalid, "SUBACK payload too short: %d bytes", len(body))
	}
	pid := binary.BigEndian.Uint16(body[:2])
	if pid == 0 {
		return nil, New(CatInvalid, "SUBACK packet identifier must be non-zero")
	}
	for i, rc := range body[2:] {
		switch rc {
		case 0, 1, 2, 0x80:
		default:
			return nil, New(CatInvalid, "SUBACK return code %d at index %d invalid", rc, i)
		}
	}
	return &Suback{PacketID: pid, ReturnCodes: append([]byte(nil), body[2:]...)}, nil
}

// ---------------------------------------------------------------------------
// Encode
// ---------------------------------------------------------------------------

func encodeFixed(typ, flags byte) byte { return typ<<4 | flags&0x0F }

// EncodeConnect returns the complete frame.
func (p *Connect) Encode() []byte {
	var b Buffer
	b.String(ProtocolName)
	b.Byte(ProtocolLevel)
	var f byte
	if p.CleanSession {
		f |= 0x02
	}
	if p.WillFlag {
		f |= 0x04 | (p.WillQoS << 3)
		if p.WillRetain {
			f |= 0x20
		}
	}
	if p.UsernameFlag {
		f |= 0x40
	}
	if p.PasswordFlag {
		f |= 0x80
	}
	b.Byte(f)
	b.Uint16(p.KeepAlive)
	b.String(p.ClientID)
	if p.WillFlag {
		b.String(p.WillTopic)
		b.Binary(p.WillMessage)
	}
	if p.UsernameFlag {
		b.String(p.Username)
	}
	if p.PasswordFlag {
		b.Binary(p.Password)
	}
	var out Buffer
	out.Frame(encodeFixed(TypeConnect, 0), b.B)
	return out.B
}

// EncodeConnack returns the complete frame.
func (p *Connack) Encode() []byte {
	var body Buffer
	var ack byte
	if p.SessionPresent {
		ack = 1
	}
	body.Byte(ack)
	body.Byte(p.ReturnCode)
	var out Buffer
	out.Frame(encodeFixed(TypeConnack, 0), body.B)
	return out.B
}

// EncodePublish returns the complete frame.
func (p *Publish) Encode() []byte {
	var body Buffer
	body.String(p.Topic)
	if p.QoS > 0 {
		body.Uint16(p.PacketID)
	}
	body.Bytes(p.Payload)
	var flags byte
	if p.Dup {
		flags |= 0x08
	}
	flags |= (p.QoS & 0x03) << 1
	if p.Retain {
		flags |= 0x01
	}
	var out Buffer
	out.Frame(encodeFixed(TypePublish, flags), body.B)
	return out.B
}

// EncodePuback returns the complete frame.
func (p *Puback) Encode() []byte {
	var body Buffer
	body.Uint16(p.PacketID)
	var out Buffer
	out.Frame(encodeFixed(TypePuback, 0), body.B)
	return out.B
}

// EncodeSubscribe returns the complete frame (fixed flags 0x2).
func (p *Subscribe) Encode() []byte {
	var body Buffer
	body.Uint16(p.PacketID)
	for _, f := range p.Filters {
		body.String(f.Topic)
		body.Byte(f.QoS & 0x03)
	}
	var out Buffer
	out.Frame(encodeFixed(TypeSubscribe, 0x2), body.B)
	return out.B
}

// EncodeSuback returns the complete frame.
func (p *Suback) Encode() []byte {
	var body Buffer
	body.Uint16(p.PacketID)
	body.Bytes(p.ReturnCodes)
	var out Buffer
	out.Frame(encodeFixed(TypeSuback, 0), body.B)
	return out.B
}

// EncodePingreq returns the complete frame.
func (p *Pingreq) Encode() []byte {
	var out Buffer
	out.Frame(encodeFixed(TypePingreq, 0), nil)
	return out.B
}

// EncodePingresp returns the complete frame.
func (p *Pingresp) Encode() []byte {
	var out Buffer
	out.Frame(encodeFixed(TypePingresp, 0), nil)
	return out.B
}

// EncodeDisconnect returns the complete frame.
func (p *Disconnect) Encode() []byte {
	var out Buffer
	out.Frame(encodeFixed(TypeDisconnect, 0), nil)
	return out.B
}
