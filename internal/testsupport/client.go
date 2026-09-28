// Package testsupport provides a hand-written MQTT test client and fixtures.
// It deliberately lives outside the broker packages: it speaks the wire
// protocol over raw TCP, can inject arbitrary (including malformed) bytes,
// and exposes every flag a conformance test needs.
//
// Frames sent to the client are parsed by a LOCAL, independent parser
// (readFrame) rather than the server's inbound decoder; production encoders
// (internal/packet) are used only to build well-formed inputs. Malformed
// inputs are constructed byte-by-byte in the tests.
package testsupport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"mqttlocal/internal/packet"
)

// Client is one raw TCP MQTT test connection.
type Client struct {
	conn net.Conn
	seq  atomic.Uint64
	name string
}

// Dial opens a connection to addr.
func Dial(addr, name string) (*Client, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{conn: c, name: name}, nil
}

// Name returns the diagnostic name.
func (c *Client) Name() string { return c.name }

// Step returns a numbered protocol-step description (request/run id logging).
func (c *Client) Step(stage, format string, args ...any) string {
	n := c.seq.Add(1)
	return fmt.Sprintf("[run=%s conn=%s seq=%d] stage=%s :: %s",
		RunID, c.name, n, stage, fmt.Sprintf(format, args...))
}

// SetReadDeadline exposes the socket deadline.
func (c *Client) SetReadDeadline(d time.Duration) {
	if d <= 0 {
		_ = c.conn.SetReadDeadline(time.Time{})
		return
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(d))
}

// Send writes arbitrary bytes (raw frame injection).
func (c *Client) Send(b []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Write(b)
	return err
}

// ReadRawByte reads one byte (EOF/close detection).
func (c *Client) ReadRawByte(wait time.Duration) (byte, int, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(wait))
	one := make([]byte, 1)
	n, err := c.conn.Read(one)
	if n > 0 {
		return one[0], n, nil
	}
	return 0, n, err
}

// ExpectClose asserts that the next read shows the connection closed.
func (c *Client) ExpectClose(wait time.Duration) error {
	_ = c.conn.SetReadDeadline(time.Now().Add(wait))
	one := make([]byte, 1)
	n, err := c.conn.Read(one)
	if n > 0 {
		return fmt.Errorf("expected close, read %d byte(s): %x", n, one[:n])
	}
	if err == nil {
		return fmt.Errorf("expected close, zero-byte read without error")
	}
	return nil
}

// Close tears down the raw TCP socket (no DISCONNECT).
func (c *Client) Close() error { return c.conn.Close() }

// Frame is a parsed server-to-client packet.
type Frame struct {
	Type  packet.Type
	Flags byte
	Body  []byte
}

// readFrame reads one complete MQTT frame using the client's own parser.
func (c *Client) readFrame(wait time.Duration) (*Frame, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(wait))
	first := make([]byte, 1)
	if _, err := io.ReadFull(c.conn, first); err != nil {
		return nil, err
	}
	rem, err := c.readRemainingLength(wait)
	if err != nil {
		return nil, err
	}
	body := make([]byte, rem)
	if rem > 0 {
		if _, err := io.ReadFull(c.conn, body); err != nil {
			return nil, err
		}
	}
	return &Frame{Type: packet.Type(first[0] >> 4), Flags: first[0] & 0x0F, Body: body}, nil
}

func (c *Client) readRemainingLength(wait time.Duration) (int, error) {
	value, mult := 0, 1
	for i := 0; i < 4; i++ {
		b := make([]byte, 1)
		if _, err := io.ReadFull(c.conn, b); err != nil {
			return 0, err
		}
		value += int(b[0]&0x7F) * mult
		if b[0]&0x80 == 0 {
			return value, nil
		}
		mult *= 128
	}
	return 0, fmt.Errorf("remaining length too long")
}

// ConnAck is the parsed CONNACK reply.
type ConnAck struct {
	SessionPresent bool
	Code           byte
}

// ConnectOpts parameterizes a CONNECT.
type ConnectOpts struct {
	ClientID     string
	CleanSession bool
	KeepAlive    uint16
	WillTopic    string
	WillMessage  []byte
	WillQoS      byte
	WillRetain   bool
	HasWill      bool
	Raw          []byte // when non-nil, sent verbatim (malformed injection)
}

// BasicConnectOpts builds ConnectOpts without a Will.
func BasicConnectOpts(clientID string, clean bool, keepAlive uint16) ConnectOpts {
	return ConnectOpts{ClientID: clientID, CleanSession: clean, KeepAlive: keepAlive}
}

// WillConnectOpts builds ConnectOpts carrying a Last Will.
func WillConnectOpts(clientID string, clean bool, keepAlive uint16, willTopic string, willMessage []byte, willQoS byte, willRetain bool) ConnectOpts {
	return ConnectOpts{
		ClientID: clientID, CleanSession: clean, KeepAlive: keepAlive,
		HasWill: true, WillTopic: willTopic, WillMessage: willMessage,
		WillQoS: willQoS, WillRetain: willRetain,
	}
}

// Connect sends a CONNECT and reads one CONNACK.
func (c *Client) Connect(o ConnectOpts) (*ConnAck, error) {
	frame := o.Raw
	if frame == nil {
		pc := &packet.Connect{
			ClientID:     o.ClientID,
			CleanSession: o.CleanSession,
			KeepAlive:    o.KeepAlive,
			HasWill:      o.HasWill,
			WillTopic:    o.WillTopic,
			WillMessage:  o.WillMessage,
			WillQoS:      o.WillQoS,
			WillRetain:   o.WillRetain,
		}
		frame = packet.EncodeConnect(pc)
	}
	if err := c.Send(frame); err != nil {
		return nil, err
	}
	f, err := c.readFrame(3 * time.Second)
	if err != nil {
		return nil, err
	}
	if f.Type != packet.TypeCONNACK {
		return nil, fmt.Errorf("expected CONNACK, got %s", f.Type)
	}
	if len(f.Body) != 2 {
		return nil, fmt.Errorf("CONNACK body must be 2 bytes, got %d", len(f.Body))
	}
	return &ConnAck{SessionPresent: f.Body[0] == 1, Code: f.Body[1]}, nil
}

// PublishFrame is a parsed PUBLISH.
type PublishFrame struct {
	Topic    string
	Payload  []byte
	PacketID uint16
	QoS      byte
	Dup      bool
	Retain   bool
}

// ParsePublish decodes a PUBLISH frame body.
func (f *Frame) ParsePublish() (*PublishFrame, error) {
	if f.Type != packet.TypePUBLISH {
		return nil, fmt.Errorf("not a PUBLISH: %s", f.Type)
	}
	if len(f.Body) < 2 {
		return nil, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint16(f.Body[:2]))
	if 2+n > len(f.Body) {
		return nil, fmt.Errorf("truncated topic")
	}
	p := &PublishFrame{
		Topic:  string(f.Body[2 : 2+n]),
		Dup:    f.Flags&0x08 != 0,
		QoS:    (f.Flags >> 1) & 0x03,
		Retain: f.Flags&0x01 != 0,
	}
	pos := 2 + n
	if p.QoS > 0 {
		if pos+2 > len(f.Body) {
			return nil, fmt.Errorf("truncated packet id")
		}
		p.PacketID = binary.BigEndian.Uint16(f.Body[pos : pos+2])
		pos += 2
	}
	p.Payload = append([]byte(nil), f.Body[pos:]...)
	return p, nil
}

// PacketID extracts the id from PUBACK body.
func (f *Frame) PacketID() (uint16, error) {
	if len(f.Body) != 2 {
		return 0, fmt.Errorf("%s body must be 2 bytes, got %d", f.Type, len(f.Body))
	}
	return binary.BigEndian.Uint16(f.Body[:2]), nil
}

// ReadFrame reads one frame with an explicit timeout (exported wrapper).
func (c *Client) ReadFrame(wait time.Duration) (*Frame, error) { return c.readFrame(wait) }

// --- high-level helpers -----------------------------------------------------

// SendSubscribe sends a SUBSCRIBE.
func (c *Client) SendSubscribe(id uint16, filter string, qos byte) error {
	return c.Send(packet.EncodeSubscribe(id, []packet.Subscription{{Filter: filter, QoS: qos}}))
}

// SendPublish sends a PUBLISH.
func (c *Client) SendPublish(topic string, payload []byte, qos byte, id uint16, dup, retain bool) error {
	return c.Send(packet.EncodePublish(topic, payload, id, qos, dup, retain))
}

// SendPuback sends a PUBACK.
func (c *Client) SendPuback(id uint16) error { return c.Send(packet.EncodePuback(id)) }

// SendPing sends PINGREQ.
func (c *Client) SendPing() error { return c.Send(packet.EncodePingreq()) }

// SendDisconnect sends DISCONNECT.
func (c *Client) SendDisconnect() error { return c.Send(packet.EncodeDisconnect()) }

// ReadSuback reads a SUBACK and returns its granted codes.
func (c *Client) ReadSuback(wait time.Duration) (uint16, []byte, error) {
	f, err := c.readFrame(wait)
	if err != nil {
		return 0, nil, err
	}
	if f.Type != packet.TypeSUBACK {
		return 0, nil, fmt.Errorf("expected SUBACK, got %s", f.Type)
	}
	if len(f.Body) < 2 {
		return 0, nil, fmt.Errorf("short SUBACK")
	}
	return binary.BigEndian.Uint16(f.Body[:2]), append([]byte(nil), f.Body[2:]...), nil
}
