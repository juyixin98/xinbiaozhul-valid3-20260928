// Package mqttclient is a small hand-rolled MQTT 3.1.1 test client. It uses
// only the Go standard library and this repository's own packet codec — no
// third-party MQTT library. It is deliberately low-level so protocol tests
// can:
//
//   - decide exactly when (or whether) to acknowledge an inbound QoS 1 PUBLISH
//     (acknowledgement-loss / retransmission tests),
//   - reuse outbound packet identifiers with and without DUP,
//   - subscribe and publish at QoS 0/1 and inspect every control packet,
//   - inject arbitrary raw bytes for rejection-condition tests,
//   - leave the socket closed abruptly (takeover / will tests).
package mqttclient

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"mqttd/internal/packet"
)

// EventKind enumerates the things a Client surfaces on its event channel.
type EventKind int

const (
	EvConnack  EventKind = iota // broker answered CONNECT
	EvSuback                    // broker answered SUBSCRIBE
	EvPublish                   // inbound PUBLISH
	EvPingresp                  // broker answered PINGREQ
	EvClosed                    // the read loop ended
)

// Event is one decoded inbound control packet or a closure notification.
type Event struct {
	Kind EventKind
	Conn struct {
		SessionPresent bool
		ReturnCode     byte
	}
	Sub *packet.Suback
	Pub *packet.Publish
	// Err is set on EvClosed.
	Err error
}

// Will carries the client's last-wish fields.
type Will struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
}

// Options configures Dial.
type Options struct {
	ClientID     string
	CleanSession bool
	KeepAlive    uint16
	Username     string
	Password     []byte
	Will         *Will
	// DialTimeout bounds the TCP+CONNECT handshake.
	DialTimeout time.Duration
}

// Client is one MQTT connection.
type Client struct {
	conn net.Conn

	evCh    chan Event
	closeCh chan struct{}
	closed  bool
	mu      sync.Mutex

	// When pauseRead is signalled, the read loop stops consuming from the
	// socket. That makes the TCP receive window (and then the broker's
	// bounded outbound queue) fill — the slow-subscriber condition.
	pauseCh chan struct{}
	paused  bool

	stashMu sync.Mutex
	stashed []Event

	nextPIDMu sync.Mutex
	nextPID   uint16

	// PUBACK completion registry (for outbound QoS 1 publishes).
	pubackCh map[uint16]chan struct{}
	pubackMu sync.Mutex
}

// PauseRead stops the read loop from draining the socket. Used by
// slow-subscriber tests to create real TCP back-pressure deterministically.
func (c *Client) PauseRead() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.paused {
		c.paused = true
		close(c.pauseCh)
	}
}

// DialRaw opens a TCP connection without sending anything; protocol tests
// drive the wire themselves.
func DialRaw(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, timeout)
}

// Dial connects and completes the CONNECT/CONNACK handshake. A non-zero
// CONNACK return code is returned as an error carrying the code.
func Dial(addr string, opt Options) (*Client, *Event, error) {
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 5 * time.Second
	}
	nc, err := DialRaw(addr, opt.DialTimeout)
	if err != nil {
		return nil, nil, err
	}
	c := &Client{
		conn:     nc,
		evCh:     make(chan Event, 128),
		closeCh:  make(chan struct{}),
		pauseCh:  make(chan struct{}),
		nextPID:  1,
		pubackCh: map[uint16]chan struct{}{},
	}
	cp := &packet.Connect{
		CleanSession: opt.CleanSession,
		KeepAlive:    opt.KeepAlive,
		ClientID:     opt.ClientID,
	}
	if opt.Will != nil {
		cp.WillFlag = true
		cp.WillTopic = opt.Will.Topic
		cp.WillMessage = opt.Will.Payload
		cp.WillQoS = opt.Will.QoS
		cp.WillRetain = opt.Will.Retain
	}
	if opt.Username != "" {
		cp.UsernameFlag = true
		cp.Username = opt.Username
	}
	if opt.Password != nil {
		cp.PasswordFlag = true
		cp.Password = opt.Password
	}
	if err := c.write(cp.Encode()); err != nil {
		nc.Close()
		return nil, nil, err
	}
	go c.readLoop()

	select {
	case ev := <-c.evCh:
		if ev.Kind != EvConnack {
			return c, nil, fmt.Errorf("expected CONNACK, got event %d", ev.Kind)
		}
		if ev.Conn.ReturnCode != packet.ConnackAccepted {
			return c, &ev, &ConnackError{Code: ev.Conn.ReturnCode}
		}
		return c, &ev, nil
	case <-time.After(opt.DialTimeout):
		nc.Close()
		return nil, nil, errors.New("timeout waiting for CONNACK")
	case <-c.closeCh:
		return nil, nil, errors.New("connection closed before CONNACK")
	}
}

// ConnackError reports a non-zero CONNACK return code.
type ConnackError struct{ Code byte }

func (e *ConnackError) Error() string {
	return fmt.Sprintf("CONNACK rejected with return code %d", e.Code)
}

// Events exposes inbound events.
func (c *Client) Events() <-chan Event { return c.evCh }

// RemoteAddr reports the peer address.
func (c *Client) RemoteAddr() string { return c.conn.RemoteAddr().String() }

func (c *Client) readLoop() {
	defer func() {
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.closeCh)
		}
		c.mu.Unlock()
	}()
	for {
		// Slow-subscriber gate: when reading is paused, block before touching
		// the socket so the kernel receive window fills and the broker
		// experiences real back-pressure.
		select {
		case <-c.pauseCh:
			<-c.closeCh
			return
		default:
		}
		frame, err := packet.ReadFrame(c.conn, 4*1024*1024)
		if err != nil {
			c.emit(Event{Kind: EvClosed, Err: err})
			return
		}
		typ, flags := packet.FrameHeader(frame[0])
		body := frame[1+frameVarLen(frame):]
		switch typ {
		case packet.TypeConnack:
			p, err := packet.DecodeConnack(body)
			if err != nil {
				c.emit(Event{Kind: EvClosed, Err: err})
				return
			}
			ev := Event{Kind: EvConnack}
			ev.Conn.SessionPresent = p.SessionPresent
			ev.Conn.ReturnCode = p.ReturnCode
			c.emit(ev)
		case packet.TypeSuback:
			p, err := packet.DecodeSuback(body)
			if err != nil {
				c.emit(Event{Kind: EvClosed, Err: err})
				return
			}
			c.emit(Event{Kind: EvSuback, Sub: p})
		case packet.TypePublish:
			p, err := packet.DecodePublish(flags, body)
			if err != nil {
				c.emit(Event{Kind: EvClosed, Err: err})
				return
			}
			c.emit(Event{Kind: EvPublish, Pub: p})
		case packet.TypePuback:
			p, err := packet.DecodePuback(body)
			if err != nil {
				c.emit(Event{Kind: EvClosed, Err: err})
				return
			}
			c.pubackMu.Lock()
			ch := c.pubackCh[p.PacketID]
			c.pubackMu.Unlock()
			if ch != nil {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		case packet.TypePingresp:
			c.emit(Event{Kind: EvPingresp})
		default:
			c.emit(Event{
				Kind: EvClosed,
				Err:  fmt.Errorf("unexpected packet type %d from broker", typ),
			})
			return
		}
	}
}

func (c *Client) emit(ev Event) {
	select {
	case c.evCh <- ev:
	default:
		// Tests drain events; a full channel means a test defect. Drop is
		// avoided by buffering generously above; reaching here blocks briefly.
		c.evCh <- ev
	}
}

// AllocPID returns the next 1..65535 packet identifier without reuse checks;
// tests are free to ignore it and send crafted identifiers.
func (c *Client) AllocPID() uint16 {
	c.nextPIDMu.Lock()
	defer c.nextPIDMu.Unlock()
	id := c.nextPID
	c.nextPID++
	if c.nextPID == 0 {
		c.nextPID = 1
	}
	return id
}

// Subscribe sends SUBSCRIBE and blocks until the matching SUBACK arrives.
// Events of other kinds observed in the meantime are stashed and can be
// retrieved with NextEvent; they are not lost.
func (c *Client) Subscribe(filters []packet.SubFilter, timeout time.Duration) (*packet.Suback, error) {
	pid := c.AllocPID()
	sub := &packet.Subscribe{PacketID: pid, Filters: filters}
	if err := c.write(sub.Encode()); err != nil {
		return nil, err
	}
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		ev, err := c.NextEvent(time.Until(deadline))
		if err != nil {
			return nil, err
		}
		if ev.Kind == EvSuback && ev.Sub.PacketID == pid {
			return ev.Sub, nil
		}
		c.stash(ev)
	}
}

// NextEvent returns the next inbound event (preferring stashed events) or an
// error on timeout/closure.
func (c *Client) NextEvent(timeout time.Duration) (Event, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	c.stashMu.Lock()
	if len(c.stashed) > 0 {
		ev := c.stashed[0]
		c.stashed = c.stashed[1:]
		c.stashMu.Unlock()
		return ev, nil
	}
	c.stashMu.Unlock()
	select {
	case ev := <-c.evCh:
		if ev.Kind == EvClosed {
			return ev, fmt.Errorf("connection closed: %w", ev.Err)
		}
		return ev, nil
	case <-time.After(timeout):
		return Event{}, errors.New("timeout waiting for event")
	case <-c.closeCh:
		return Event{Kind: EvClosed}, errors.New("connection closed")
	}
}

// stash holds events consumed while a helper waited for a specific reply.
func (c *Client) stash(ev Event) {
	c.stashMu.Lock()
	c.stashed = append(c.stashed, ev)
	c.stashMu.Unlock()
}

// Publish0 sends a QoS 0 PUBLISH.
func (c *Client) Publish0(topic string, payload []byte, retain bool) error {
	p := &packet.Publish{QoS: 0, Retain: retain, Topic: topic, Payload: payload}
	return c.write(p.Encode())
}

// Publish1 sends a QoS 1 PUBLISH with the given packet identifier and waits
// up to timeout for the broker PUBACK. The same pid may be passed repeatedly
// to test identifier reuse; set dup to mark repeats.
func (c *Client) Publish1(pid uint16, topic string, payload []byte, retain, dup bool, timeout time.Duration) error {
	if pid == 0 {
		return errors.New("packet identifier must be non-zero")
	}
	p := &packet.Publish{QoS: 1, PacketID: pid, Topic: topic, Payload: payload, Retain: retain, Dup: dup}
	ch := make(chan struct{}, 1)
	c.pubackMu.Lock()
	c.pubackCh[pid] = ch
	c.pubackMu.Unlock()
	defer func() {
		c.pubackMu.Lock()
		delete(c.pubackCh, pid)
		c.pubackMu.Unlock()
	}()
	if err := c.write(p.Encode()); err != nil {
		return err
	}
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	select {
	case <-ch:
		return nil
	case <-time.After(timeout):
		return errors.New("timeout waiting for PUBACK")
	case <-c.closeCh:
		return errors.New("connection closed waiting for PUBACK")
	}
}

// Puback acknowledges an inbound QoS 1 PUBLISH. Tests call this explicitly to
// simulate acknowledgement loss (by simply never calling it) and delayed
// acknowledgement.
func (c *Client) Puback(pid uint16) error {
	if pid == 0 {
		return errors.New("packet identifier must be non-zero")
	}
	return c.write((&packet.Puback{PacketID: pid}).Encode())
}

// Ping sends PINGREQ without waiting.
func (c *Client) Ping() error { return c.write((&packet.Pingreq{}).Encode()) }

// Disconnect sends DISCONNECT and closes the socket.
func (c *Client) Disconnect() error {
	if err := c.write((&packet.Disconnect{}).Encode()); err != nil {
		return err
	}
	return c.Close()
}

// SendRaw writes arbitrary bytes (rejection-condition tests).
func (c *Client) SendRaw(b []byte) error { return c.write(b) }

// Close aborts the TCP connection (no DISCONNECT), simulating a network drop.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.closeCh)
	c.mu.Unlock()
	return c.conn.Close()
}

// Done is closed when the read loop has ended.
func (c *Client) Done() <-chan struct{} { return c.closeCh }

func (c *Client) write(b []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer c.conn.SetWriteDeadline(time.Time{})
	_, err := c.conn.Write(b)
	return err
}

// frameVarLen returns the count of variable-length bytes in a validated frame
// (1..4). It mirrors the broker's parser; ReadFrame guarantees the frame is
// well formed.
func frameVarLen(frame []byte) int {
	for i := 1; i <= 4 && i < len(frame); i++ {
		if frame[i]&0x80 == 0 {
			return i
		}
	}
	return 1
}
