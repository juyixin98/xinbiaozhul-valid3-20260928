package broker

import (
	"mqttlocal/internal/store"
)

// Conn is the broker-side state for one attached network connection. The
// server package owns the transport; the broker enqueues encoded frames on
// SendCh and asks the server to close via RequestClose.
type Conn struct {
	id       uint64 // connection id (diagnostic)
	clientID string
	clean    bool

	send    chan []byte
	closeCh chan struct{}
	closed  bool

	// epoch is bumped on take-over; stale readers stop mutating state.
	epoch uint64

	// subs holds this connection's subscriptions. For durable sessions it
	// mirrors the SQLite rows and is the structure used during fan-out.
	subs map[string]*subscription

	// inflight holds outbound QoS1 messages awaiting PUBACK keyed by the
	// packet id used on this client's flow. For durable sessions it mirrors
	// the inflight table.
	inflight map[uint16]*store.StoredMessage

	// nextOutID allocates packet identifiers 1..65535, skipping identifiers
	// currently in flight (MQTT-2.3.1).
	nextOutID uint16

	// will is the registered Last Will.
	will *store.WillState

	keepAlive uint16

	// requestClose asks the transport layer to close with a typed reason
	// (take-over, slow subscriber).
	requestClose TerminateFunc
}

// CleanSession reports whether this is a clean (non-persistent) connection.
func (c *Conn) CleanSession() bool { return c.clean }

// KeepAlive returns the negotiated Keep Alive value (seconds).
func (c *Conn) KeepAlive() uint16 { return c.keepAlive }

// SendCh returns the outbound frame channel (closed on connection teardown).
func (c *Conn) SendCh() <-chan []byte { return c.send }

// CloseCh is closed when the broker wants the connection gone (take-over,
// slow subscriber, orderly shutdown).
func (c *Conn) CloseCh() <-chan struct{} { return c.closeCh }

// ID returns the diagnostic connection id.
func (c *Conn) ID() uint64 { return c.id }

// ClientIdentifier returns the attached MQTT client id.
func (c *Conn) ClientIdentifier() string { return c.clientID }

type subscription struct {
	filter string
	qos    byte
}

// allocPacketID returns the next non-zero identifier that is not currently
// in flight. Returns 0 if the window is exhausted. Caller holds broker mu.
func (c *Conn) allocPacketID() uint16 {
	for attempt := 0; attempt < 65535; attempt++ {
		c.nextOutID++
		if c.nextOutID == 0 {
			c.nextOutID = 1
		}
		if _, used := c.inflight[c.nextOutID]; !used {
			return c.nextOutID
		}
	}
	return 0
}

// enqueueSend places one frame on the bounded send queue. It returns false
// when the queue is full or the connection is being torn down — the caller
// treats that as a slow-subscriber condition.
func (c *Conn) enqueueSend(frame []byte) bool {
	if c.closed {
		return false
	}
	select {
	case c.send <- frame:
		return true
	default:
		return false
	}
}

// TrySend queues a frame from outside the broker (server-side PINGRESP). It
// reports whether the frame was accepted.
func (c *Conn) TrySend(frame []byte) bool { return c.enqueueSend(frame) }
