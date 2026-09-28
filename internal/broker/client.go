package broker

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mqttd/internal/logx"
	"mqttd/internal/packet"
	"mqttd/internal/store"
	"mqttd/internal/topics"
)

// Close reasons identify why a connection ended (used in logs/will handling).
type closeReason string

const (
	reasonProtocol    closeReason = "protocol_violation"
	reasonUnsupported closeReason = "unsupported_packet"
	reasonClient      closeReason = "client_disconnect" // DISCONNECT packet received
	reasonAbnormal    closeReason = "abnormal_close"    // TCP closed WITHOUT DISCONNECT
	reasonKeepAlive   closeReason = "keepalive_timeout"
	reasonTakeover    closeReason = "session_takeover"
	reasonLimit       closeReason = "resource_limit"
	reasonServer      closeReason = "server_closing"
	reasonInternal    closeReason = "internal_error"
)

// outboundMsg is one serialised frame awaiting the writer.
type outboundMsg struct {
	frame []byte
	qos   byte
	// pid identifies the broker->client QoS 1 delivery; 0 for QoS 0.
	pid uint16
}

// pendingQ1 is the delivery pump's state for one unacknowledged broker->client
// QoS 1 message.
type pendingQ1 struct {
	queued      bool      // a copy currently sits in sendCh
	written     bool      // at least one copy reached the socket
	lastSent    time.Time // time of the most recent write
	retainFirst bool      // deliver the first attempt with RETAIN=1
}

// client is one live connection and its state machine.
type client struct {
	b       *Broker
	conn    net.Conn
	log     *logx.Logger
	connSeq int64

	// session fields, set after CONNECT.
	clientID    string
	clean       bool
	willTopic   string
	willPayload []byte
	willQoS     byte
	willRetain  bool
	hasWill     bool
	connected   atomic.Bool

	keepalive  time.Duration
	deadlineOn bool

	// sender: writePump is the only goroutine writing queued frames.
	sendCh chan outboundMsg
	doneCh chan struct{} // closed once when the session must stop

	// Delivery-pump signaling. The pump is the single owner of broker->client
	// QoS 1 enqueues, so initial replay, new messages and retransmits can
	// never enqueue the same packet id twice concurrently.
	kickCh    chan struct{} // a new inflight row was persisted
	writtenCh chan uint16   // writePump wrote this packet id to the socket
	ackCh     chan uint16   // a PUBACK removed this packet id's inflight row

	endOnce sync.Once

	writeMu sync.Mutex // serialize direct writes (CONNACK/SUBACK/PUBACK/PINGRESP)
}

// serveConn is the top of a connection goroutine.
func (b *Broker) serveConn(conn net.Conn) {
	seq := b.mtx.metrics.ConnectionsActive.Add(1)
	c := &client{
		b:         b,
		conn:      conn,
		log:       b.log,
		connSeq:   seq,
		sendCh:    make(chan outboundMsg, b.cfg.SendQueueDepth),
		doneCh:    make(chan struct{}),
		kickCh:    make(chan struct{}, b.cfg.SendQueueDepth),
		writtenCh: make(chan uint16, b.cfg.SendQueueDepth),
		ackCh:     make(chan uint16, b.cfg.SendQueueDepth),
	}
	defer b.mtx.metrics.ConnectionsActive.Add(-1)
	defer conn.Close()

	reason, fatal := c.runConnect()
	if fatal != nil {
		c.log.Event("connect", dispositionFor(fatal), "connection closed during CONNECT",
			"conn", c.connSeq, "err", fatal.Error())
		return
	}
	if reason != "" {
		// CONNACK rejection: packet was well-formed but policy refused it.
		return
	}
	c.serveSession()
}

// runConnect reads and processes exactly one CONNECT. It returns a non-empty
// reason when the connection must end without a registered session, or a
// fatal read/parse error.
func (c *client) runConnect() (closeReason, error) {
	b := c.b
	frame, err := packet.ReadFrame(c.conn, b.cfg.MaxPacketBytes)
	if err != nil {
		// EOF on an idle fresh socket is an ordinary client disconnect, not a
		// protocol violation; malformed/oversized first frames are counted.
		if errors.Is(err, io.EOF) {
			return reasonClient, err
		}
		pe := packet.AsError(err)
		switch {
		case pe != nil && pe.Cat == packet.CatLimit:
			b.mtx.metrics.FramesInvalid.Add(1)
			return reasonLimit, err
		case pe != nil:
			b.mtx.metrics.FramesInvalid.Add(1)
			return reasonProtocol, err
		default:
			return reasonClient, err
		}
	}
	typ, flags := packet.FrameHeader(frame[0])
	if typ != packet.TypeConnect || flags != 0 {
		b.mtx.metrics.FramesInvalid.Add(1)
		return reasonProtocol, packet.New(packet.CatInvalid,
			"first packet must be CONNECT with flags 0 (got type=%d flags=0x%x)", typ, flags)
	}
	connPkt, err := packet.DecodeConnect(frame[1+varLenLen(frame):])
	if err != nil {
		return c.handleConnectParseError(err)
	}
	c.clientID = connPkt.ClientID
	c.clean = connPkt.CleanSession
	c.keepalive = c.deriveKeepalive(connPkt.KeepAlive)
	c.deadlineOn = c.keepalive > 0
	if connPkt.WillFlag {
		// This subset implements only QoS 0/1: a QoS 2 will is refused at
		// CONNECT rather than silently downgraded (3.1.2.9 allows refusal).
		if connPkt.WillQoS == 2 {
			b.mtx.metrics.ConnectsRejected.Add(1)
			b.mtx.metrics.FramesUnsupported.Add(1)
			_ = c.writeRaw((&packet.Connack{
				ReturnCode: packet.ConnackServerUnavailable,
			}).Encode())
			c.log.Event("connect", logx.DispositionRejectUnsupported,
				"will QoS 2 not supported", "conn", c.connSeq)
			return reasonUnsupported, nil
		}
		if err := topics.ValidateName(connPkt.WillTopic); err != nil {
			b.mtx.metrics.ConnectsRejected.Add(1)
			b.mtx.metrics.FramesInvalid.Add(1)
			_ = c.writeRaw((&packet.Connack{
				ReturnCode: packet.ConnackIDRejected,
			}).Encode())
			c.log.Event("connect", logx.DispositionRejectInvalid,
				"invalid will topic", "conn", c.connSeq, "err", err.Error())
			return reasonProtocol, nil
		}
		c.hasWill = true
		c.willTopic = connPkt.WillTopic
		c.willPayload = connPkt.WillMessage
		c.willQoS = connPkt.WillQoS
		c.willRetain = connPkt.WillRetain
	}

	// Client id policy (3.1.3): empty allowed only with clean session.
	if c.clientID == "" && !c.clean {
		b.mtx.metrics.ConnectsRejected.Add(1)
		c.writeRaw((&packet.Connack{SessionPresent: false, ReturnCode: packet.ConnackIDRejected}).Encode())
		c.log.Event("connect", logx.DispositionRejectInvalid,
			"empty client id without clean session", "conn", c.connSeq)
		return reasonProtocol, nil
	}
	if c.clientID == "" {
		c.clientID = generatedID(c.connSeq)
	}
	if !b.cfg.AcceptAnonymous && !connPkt.UsernameFlag {
		b.mtx.metrics.ConnectsRejected.Add(1)
		c.writeRaw((&packet.Connack{ReturnCode: packet.ConnackNotAuthorized}).Encode())
		return reasonProtocol, nil
	}

	// Session resolution: check for an existing DURABLE persisted session and
	// for a live connection using the same id (takeover, MQTT 3.1.3.1). The
	// old connection is closed OUTSIDE the broker lock because its teardown
	// path takes the same lock. sessionPresent (CONNACK SP) reflects ONLY a
	// durable persisted session (MQTT 3.2.2.2): taking over a clean client
	// must not set SP.
	b.mtx.mu.Lock()
	var oldClient *client
	if old := b.mtx.clients[c.clientID]; old != nil {
		oldClient = old
	}
	_, sessionErr := b.st.SessionClean(c.clientID)
	durableExists := sessionErr == nil
	if c.clean {
		// A clean start discards any prior durable state before re-registering.
		_ = b.st.WipeClient(c.clientID)
		b.mtx.index.removeClient(c.clientID)
		durableExists = false
	} else if !durableExists {
		// Enforce the durable-session cap only for brand-new durable sessions.
		if nSessions := b.countDurableSessions(); nSessions >= b.cfg.MaxSessions {
			b.mtx.mu.Unlock()
			b.mtx.metrics.ConnectsRejected.Add(1)
			b.mtx.metrics.RejectedSessions.Add(1)
			c.writeRaw((&packet.Connack{ReturnCode: packet.ConnackServerUnavailable}).Encode())
			c.log.Event("connect", logx.DispositionRejectLimit,
				"session limit reached", "conn", c.connSeq, "limit", b.cfg.MaxSessions)
			return reasonLimit, nil
		}
	}
	if err := b.st.CreateSession(c.clientID, c.clean, time.Now().UnixNano()); err != nil {
		b.mtx.mu.Unlock()
		b.mtx.metrics.ConnectsRejected.Add(1)
		c.writeRaw((&packet.Connack{ReturnCode: packet.ConnackServerUnavailable}).Encode())
		c.log.Event("connect", logx.DispositionInternalError, "create session failed",
			"conn", c.connSeq, "err", err.Error())
		return reasonInternal, nil
	}
	b.mtx.clients[c.clientID] = c
	b.mtx.mu.Unlock()

	if oldClient != nil {
		b.mtx.metrics.TakenOver.Add(1)
		oldClient.shutdown(reasonTakeover)
	}

	sessionPresent := durableExists
	connack := &packet.Connack{SessionPresent: sessionPresent, ReturnCode: packet.ConnackAccepted}
	if err := c.writeRaw(connack.Encode()); err != nil {
		b.deregister(c, reasonInternal)
		return reasonInternal, err
	}
	b.mtx.metrics.ConnectsAccepted.Add(1)
	c.connected.Store(true)
	c.log.Event("connect", logx.DispositionAccept, "session established",
		"conn", c.connSeq, "client", c.clientID, "clean", c.clean,
		"session_present", sessionPresent, "keepalive_s", connPkt.KeepAlive)

	if !c.clean {
		// Rebuild in-memory trie entries for this durable client (a restarted
		// broker loaded them all at startup; this covers a wiped live index).
		if subs, err := b.st.ListSubscriptions(c.clientID); err == nil {
			for _, s := range subs {
				b.mtx.index.add(splitLevels(s.Filter), s.ClientID, s.QoS)
			}
		}
	}
	return "", nil
}

// handleConnectParseError maps a CONNECT parse failure onto the mandated
// response: CONNACK code 1 for a wrong protocol level, otherwise a silent
// protocol-violation close (a malformed CONNECT gets no CONNACK).
func (c *client) handleConnectParseError(err error) (closeReason, error) {
	if level, ok := packet.IsProtoLevel(err); ok {
		c.b.mtx.metrics.ConnectsRejected.Add(1)
		c.b.mtx.metrics.FramesUnsupported.Add(1)
		// Code 1 is the prescribed response for an unacceptable level.
		_ = c.writeRaw((&packet.Connack{ReturnCode: packet.ConnackBadProto}).Encode())
		c.log.Event("connect", logx.DispositionRejectUnsupported,
			"unsupported protocol level", "conn", c.connSeq, "level", level)
		return reasonUnsupported, nil
	}
	pe := packet.AsError(err)
	if pe != nil && pe.Cat == packet.CatUnsupported {
		// Unknown protocol name: CONNACK with code 1 per 3.1.2.
		c.b.mtx.metrics.ConnectsRejected.Add(1)
		c.b.mtx.metrics.FramesUnsupported.Add(1)
		_ = c.writeRaw((&packet.Connack{ReturnCode: packet.ConnackBadProto}).Encode())
		c.log.Event("connect", logx.DispositionRejectUnsupported, "bad protocol name",
			"conn", c.connSeq, "err", err.Error())
		return reasonUnsupported, nil
	}
	c.b.mtx.metrics.FramesInvalid.Add(1)
	c.log.Event("connect", logx.DispositionRejectInvalid, "malformed CONNECT",
		"conn", c.connSeq, "err", err.Error())
	return reasonProtocol, err
}

func (c *client) deriveKeepalive(secs uint16) time.Duration {
	if secs == 0 {
		return 0
	}
	d := time.Duration(float64(secs) * c.b.cfg.KeepAliveFactor * float64(time.Second))
	if max := c.b.cfg.KeepAliveMax; max > 0 && d > max {
		d = max
	}
	return d
}

// serveSession runs the write pump, the QoS 1 delivery pump (initial replay,
// retransmission and inflight reconciliation) and the read loop.
func (c *client) serveSession() {
	go c.writePump()
	go c.deliveryPump()

	readReason := c.readLoop()
	c.terminate(readReason)
}

func (c *client) readLoop() closeReason {
	for {
		if c.deadlineOn {
			_ = c.conn.SetReadDeadline(time.Now().Add(c.keepalive))
		}
		frame, err := packet.ReadFrame(c.conn, c.b.cfg.MaxPacketBytes)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				c.log.Event("keepalive", logx.DispositionStateConflict,
					"read timeout without PINGREQ", "client", c.clientID,
					"timeout_ms", c.keepalive.Milliseconds())
				return reasonKeepAlive
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// The socket closed without a DISCONNECT packet: an abnormal
				// network disconnect per MQTT 3.1.2.6 (will IS published).
				return reasonAbnormal
			}
			pe := packet.AsError(err)
			if pe != nil {
				switch pe.Cat {
				case packet.CatLimit:
					c.b.mtx.metrics.FramesInvalid.Add(1)
					return reasonLimit
				default:
					c.b.mtx.metrics.FramesInvalid.Add(1)
					return reasonProtocol
				}
			}
			return reasonClient
		}
		if r := c.dispatch(frame); r != "" {
			return r
		}
	}
}

// dispatch handles one post-CONNECT frame and returns "" to continue or a
// close reason to terminate.
func (c *client) dispatch(frame []byte) closeReason {
	typ, flags := packet.FrameHeader(frame[0])
	body := frame[1+varLenLen(frame):]
	switch typ {
	case packet.TypePublish:
		return c.handleIncomingPublish(flags, body)
	case packet.TypePuback:
		if flags != 0 {
			return c.protoViolate("PUBACK fixed flags must be 0, got 0x%x", flags)
		}
		return c.handlePuback(body)
	case packet.TypeSubscribe:
		return c.handleSubscribe(flags, body)
	case packet.TypePingreq:
		if flags != 0 {
			return c.protoViolate("PINGREQ fixed flags must be 0, got 0x%x", flags)
		}
		if err := c.writeRaw((&packet.Pingresp{}).Encode()); err != nil {
			return reasonInternal
		}
		return ""
	case packet.TypeDisconnect:
		if flags != 0 {
			return c.protoViolate("DISCONNECT fixed flags must be 0, got 0x%x", flags)
		}
		return reasonClient
	default:
		// Outside this subset: reserved types 0/15, the QoS 2 family
		// (5 PUBREC, 6 PUBREL, 7 PUBCOMP), UNSUBSCRIBE (10), and
		// server-only packets (2 CONNACK, 9 SUBACK, 13 PINGRESP) arriving
		// from a client are all rejected. Type 10 is a defined-but-unmodeled
		// feature; the rest are malformed/reserved for this direction.
		if typ == 10 {
			c.b.mtx.metrics.FramesUnsupported.Add(1)
			c.log.Event("decode", logx.DispositionRejectUnsupported,
				"UNSUBSCRIBE is not supported by this subset",
				"client", c.clientID)
			return reasonUnsupported
		}
		c.b.mtx.metrics.FramesInvalid.Add(1)
		c.log.Event("decode", logx.DispositionRejectInvalid,
			"packet type not allowed from client", "client", c.clientID,
			"type", typ, "flags", flags)
		return reasonProtocol
	}
}

func (c *client) protoViolate(format string, args ...any) closeReason {
	c.b.mtx.metrics.FramesInvalid.Add(1)
	c.log.Event("decode", logx.DispositionRejectInvalid,
		"protocol violation: "+fmt.Sprintf(format, args...),
		"client", c.clientID)
	return reasonProtocol
}

// handleIncomingPublish validates, acknowledges (QoS 1), retains and routes.
func (c *client) handleIncomingPublish(flags byte, body []byte) closeReason {
	pub, err := packet.DecodePublish(flags, body)
	if err != nil {
		pe := packet.AsError(err)
		if pe != nil && pe.Cat == packet.CatUnsupported {
			c.b.mtx.metrics.FramesUnsupported.Add(1)
			c.log.Event("publish", logx.DispositionRejectUnsupported,
				"QoS 2 PUBLISH rejected: subset supports only QoS 0/1",
				"client", c.clientID, "err", err.Error())
			return reasonUnsupported
		}
		c.b.mtx.metrics.FramesInvalid.Add(1)
		c.log.Event("publish", logx.DispositionRejectInvalid,
			"malformed PUBLISH", "client", c.clientID, "err", err.Error())
		return reasonProtocol
	}
	if err := topics.ValidateName(pub.Topic); err != nil {
		c.b.mtx.metrics.FramesInvalid.Add(1)
		c.log.Event("publish", logx.DispositionRejectInvalid,
			"invalid topic name", "client", c.clientID, "topic", pub.Topic)
		return reasonProtocol
	}
	if pub.QoS == 0 {
		c.b.mtx.metrics.PublishReceivedQoS0.Add(1)
	} else {
		c.b.mtx.metrics.PublishReceivedQoS1.Add(1)
	}

	// Retained handling (3.3.1.3): store/delete before fan-out so a message
	// cannot both be retained and delivered to its own matching subscribers
	// as retained (it is delivered normally, with RETAIN=0, to subscribers).
	if pub.Retain {
		if err := c.b.applyRetained(pub); err != nil {
			if pe := packet.AsError(err); pe != nil && pe.Cat == packet.CatLimit {
				c.log.Event("retain", logx.DispositionRejectLimit,
					"retained rejected: limit", "client", c.clientID, "err", err.Error())
				return reasonLimit
			}
			return reasonInternal
		}
	}

	// For QoS 1 the broker persists per-subscriber inflight rows before
	// sending PUBACK so a crash between PUBACK and fan-out loses nothing.
	// Duplicate delivery to subscribers is permitted (at-least-once); the
	// PUBLISH DUP bit is preserved end-to-end by the wire packet only.
	c.b.routePublish(pub)

	if pub.QoS == 1 {
		if err := c.writeRaw((&packet.Puback{PacketID: pub.PacketID}).Encode()); err != nil {
			return reasonInternal
		}
	}
	c.logPub(pub)
	return ""
}

func (c *client) logPub(pub *packet.Publish) {
	c.log.Event("publish", logx.DispositionAccept, "published",
		"client", c.clientID, "topic", pub.Topic, "qos", pub.QoS,
		"retain", pub.Retain, "dup", pub.Dup, "pid", pub.PacketID,
		"bytes", len(pub.Payload))
}

// handlePuback removes a subscriber-facing inflight row keyed by the inbound
// publisher? No: PUBACK here acknowledges a PUBLISH the BROKER sent to THIS
// client. It matches (this client, packet id).
func (c *client) handlePuback(body []byte) closeReason {
	p, err := packet.DecodePuback(body)
	if err != nil {
		c.b.mtx.metrics.FramesInvalid.Add(1)
		return reasonProtocol
	}
	c.b.mtx.metrics.PubackReceived.Add(1)
	res, err := c.b.st.DeleteInflight(c.clientID, p.PacketID)
	if err != nil {
		return reasonInternal
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.b.mtx.metrics.InflightCurrent.Add(-n)
		select {
		case c.ackCh <- p.PacketID:
		default:
			// The pump reconciles against the table on its next tick even if
			// this signal is coalesced.
		}
		c.log.Event("puback", logx.DispositionAccept, "inflight acknowledged",
			"client", c.clientID, "pid", p.PacketID)
	} else {
		// A PUBACK for an unknown packet id is harmless under at-least-once
		// semantics: the delivery may already have been removed (duplicate
		// PUBACK). It is logged, not treated as a protocol violation.
		c.log.Event("puback", logx.DispositionAccept,
			"PUBACK for unknown/duplicate packet id ignored",
			"client", c.clientID, "pid", p.PacketID)
	}
	return ""
}

// handleSubscribe validates flags/filters, persists each subscription and
// replies SUBACK (per-filter granted QoS or 0x80), then delivers retained
// messages matching each ACCEPTED filter.
func (c *client) handleSubscribe(flags byte, body []byte) closeReason {
	if flags != 0x2 {
		return c.protoViolate("SUBSCRIBE fixed flags must be 0b0010, got 0x%x", flags)
	}
	sub, err := packet.DecodeSubscribe(body)
	if err != nil {
		c.b.mtx.metrics.FramesInvalid.Add(1)
		c.log.Event("subscribe", logx.DispositionRejectInvalid,
			"malformed SUBSCRIBE", "client", c.clientID, "err", err.Error())
		return reasonProtocol
	}
	returnCodes := make([]byte, len(sub.Filters))
	var accepted []packet.SubFilter
	for i, f := range sub.Filters {
		if err := topics.ValidateFilter(f.Topic); err != nil {
			returnCodes[i] = packet.SubackFailure
			c.log.Event("subscribe", logx.DispositionRejectInvalid,
				"invalid filter", "client", c.clientID, "filter", f.Topic)
			continue
		}
		if f.QoS == 2 {
			// Explicit refusal of QoS 2 rather than silent downgrade: this
			// subset does not implement QoS 2, so SUBACK reports failure
			// (0x80) for that filter, per 3.8.3/3.8.4.
			returnCodes[i] = packet.SubackFailure
			c.log.Event("subscribe", logx.DispositionRejectUnsupported,
				"QoS 2 subscription refused", "client", c.clientID, "filter", f.Topic)
			continue
		}
		// Resource cap on subscriptions.
		n, _ := c.b.st.CountSubscriptions(c.clientID)
		already := c.subscriptionExists(f.Topic)
		if !already && n >= c.b.cfg.MaxSubscriptionsPerClient {
			returnCodes[i] = packet.SubackFailure
			c.b.mtx.metrics.RejectedSubLimit.Add(1)
			c.log.Event("subscribe", logx.DispositionRejectLimit,
				"subscription limit reached", "client", c.clientID,
				"filter", f.Topic, "limit", c.b.cfg.MaxSubscriptionsPerClient)
			continue
		}
		if err := c.b.st.UpsertSubscription(c.clientID, f.Topic, f.QoS); err != nil {
			returnCodes[i] = packet.SubackFailure
			c.log.Event("subscribe", logx.DispositionInternalError,
				"persist subscription failed", "client", c.clientID, "err", err.Error())
			continue
		}
		c.b.mtx.mu.Lock()
		c.b.mtx.index.add(splitLevels(f.Topic), c.clientID, f.QoS)
		if !already {
			c.b.mtx.metrics.SubscriptionsCurrent.Add(1)
		}
		c.b.mtx.mu.Unlock()
		returnCodes[i] = f.QoS
		accepted = append(accepted, f)
	}
	if err := c.writeRaw((&packet.Suback{
		PacketID:    sub.PacketID,
		ReturnCodes: returnCodes,
	}).Encode()); err != nil {
		return reasonInternal
	}
	c.log.Event("subscribe", logx.DispositionAccept, "subscribe processed",
		"client", c.clientID, "pid", sub.PacketID, "filters", len(sub.Filters))

	// Retained delivery for accepted filters (3.8.4 / 3.3.1.3): each retained
	// message matching the NEW filter is sent with RETAIN=1.
	for _, f := range accepted {
		if err := c.deliverRetained(f); err != nil {
			return reasonInternal
		}
	}
	return ""
}

func (c *client) subscriptionExists(filter string) bool {
	// The trie node membership reflects presence; look up by walking.
	c.b.mtx.mu.Lock()
	defer c.b.mtx.mu.Unlock()
	n := c.b.mtx.index.root()
	for _, lv := range splitLevels(filter) {
		switch lv {
		case "+":
			n = n.plus
		case "#":
			n = n.hash
		default:
			n = n.next[lv]
		}
		if n == nil {
			return false
		}
	}
	_, ok := n.members[c.clientID]
	return ok
}

// writeRaw serializes direct control responses (CONNACK/SUBACK/PUBACK/
// PINGRESP). These are small and rare; a mutex keeps them ordered ahead of
// queued PUBLISH frames where required.
func (c *client) writeRaw(b []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.deadlineOn {
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	}
	_, err := c.conn.Write(b)
	if c.deadlineOn {
		_ = c.conn.SetWriteDeadline(time.Time{})
	}
	return err
}

// enqueueQoS0 puts a QoS 0 PUBLISH frame on the bounded sender queue. When the
// queue is full the frame is dropped and counted (slow-subscriber policy).
func (c *client) enqueueQoS0(frame []byte) {
	select {
	case c.sendCh <- outboundMsg{frame: frame, qos: 0}:
	default:
		c.b.mtx.metrics.DroppedQueueFull.Add(1)
		c.log.Event("deliver", logx.DispositionDrop,
			"slow subscriber: QoS 0 dropped (queue full)",
			"client", c.clientID, "depth", cap(c.sendCh))
	}
}

// kick wakes the delivery pump after a new inflight row was persisted.
func (c *client) kick() {
	select {
	case c.kickCh <- struct{}{}:
	default:
	}
}

// writePump drains sendCh onto the socket. It is the only goroutine writing
// queued frames; writeRaw control frames are mutex-guarded independently. A
// write failure closes the socket, which ends the read loop.
func (c *client) writePump() {
	wt := c.b.cfg.SendWriteTimeout
	for {
		select {
		case <-c.doneCh:
			return
		case msg := <-c.sendCh:
			if wt > 0 {
				_ = c.conn.SetWriteDeadline(time.Now().Add(wt))
			}
			_, err := c.conn.Write(msg.frame)
			if wt > 0 {
				_ = c.conn.SetWriteDeadline(time.Time{})
			}
			if err != nil {
				// A write timeout (dead slow consumer holding its receive
				// window shut) or a broken socket: release the writer and
				// tear down the connection. Durable QoS 1 rows survive and
				// are replayed on reconnect with DUP=1.
				c.log.Event("deliver", logx.DispositionDrop,
					"outbound write failed; closing slow/dead connection",
					"client", c.clientID, "err", err.Error())
				_ = c.conn.Close()
				return
			}
			if msg.qos == 1 {
				c.b.mtx.metrics.DeliveredQoS1.Add(1)
				if msg.pid != 0 {
					_ = c.b.st.MarkInflightSent(c.clientID, msg.pid)
					select {
					case c.writtenCh <- msg.pid:
					default:
					}
				}
			} else {
				c.b.mtx.metrics.DeliveredQoS0.Add(1)
			}
		}
	}
}

// deliveryPump is the single owner of broker->client QoS 1 enqueues. It
// reconciles its in-memory pending map with the authoritative inflight table
// on four triggers: session start (replay), a new persisted row (kick), a
// successful write, and the retransmit timer.
//
// State machine for one packet id:
//
//	(absent) --persist--> not queued
//	not queued --enqueue ok--> queued (frame in sendCh)
//	queued --written--> inflight on the wire (written=true)
//	queued/written --PUBACK--> (absent, row deleted)
//	written --retransmit interval elapsed--> queued again, DUP=1
//
// A frame sitting in sendCh (not yet written) is NEVER duplicated; only
// frames known to have been written are retransmitted, and then with DUP=1.
func (c *client) deliveryPump() {
	interval := c.b.cfg.RetransmitInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval / 2)
	defer t.Stop()

	pending := map[uint16]*pendingQ1{}
	now := time.Now
	// initial marks the first reconciliation of this connection. MQTT
	// 3.3.1.1/4.4: when a client reconnects with a persistent session, every
	// inflight message re-delivered on the NEW connection carries DUP=1 —
	// regardless of whether an earlier copy may have reached the network on a
	// previous connection (we cannot know, so DUP=1 is mandatory). After the
	// first pass, genuinely-never-sent rows (sent=0, created on this
	// connection) go out with DUP=0.
	initial := true

	reconcile := func() {
		rows, err := c.b.st.ListInflight(c.clientID)
		if err != nil {
			return
		}
		live := make(map[uint16]store.InflightRow, len(rows))
		for _, r := range rows {
			live[r.PacketID] = r
		}
		// Drop pending entries whose inflight row was acknowledged.
		for pid := range pending {
			if _, ok := live[pid]; !ok {
				delete(pending, pid)
			}
		}
		// Re-enqueue attempts for every live row that currently has no copy
		// in the send queue.
		for _, r := range rows {
			p := pending[r.PacketID]
			if p != nil && p.queued {
				continue
			}
			if p != nil && p.written && now().Sub(p.lastSent) < interval {
				continue // not yet due for retransmission
			}
			dup := p != nil && p.written
			// On the FIRST reconciliation of a connection, every existing
			// inflight row is a session resume/replay -> DUP=1. Later, a row
			// written at least once (sent=1) re-sends with DUP=1.
			if initial || (p == nil && r.Sent) {
				dup = true
			}
			// RETAIN is carried only on the first delivery of a retained
			// image; retransmitted/replayed frames carry RETAIN=0 (3.3.1.3).
			retain := byte(0)
			firstAttempt := p == nil && !r.Sent
			if firstAttempt && r.Retain {
				retain = 1
			}
			pub := &packet.Publish{
				Dup: dup, QoS: r.QoS,
				Retain:   retain == 1,
				Topic:    r.Topic,
				PacketID: r.PacketID,
				Payload:  r.Payload,
			}
			frame := pub.Encode()
			select {
			case c.sendCh <- outboundMsg{frame: frame, qos: r.QoS, pid: r.PacketID}:
				if p == nil {
					p = &pendingQ1{retainFirst: retain == 1}
					pending[r.PacketID] = p
				}
				p.queued = true
				p.written = false
				if dup {
					c.b.mtx.metrics.Retransmits.Add(1)
					c.log.Event("retx", logx.DispositionRetry,
						"re-enqueued unacknowledged QoS 1 with DUP=1",
						"client", c.clientID, "pid", r.PacketID)
				} else if r.Sent {
					c.b.mtx.metrics.Retransmits.Add(1)
					c.log.Event("replay", logx.DispositionRetry,
						"replayed unacknowledged QoS 1 on session resume",
						"client", c.clientID, "pid", r.PacketID)
				}
			default:
				// Queue full: stay un-queued; the next kick/tick retries.
			}
		}
	}

	// Initial replay of durable inflight rows.
	reconcile()
	initial = false
	if !c.clean {
		if rows, err := c.b.st.ListInflight(c.clientID); err == nil && len(rows) > 0 {
			c.log.Event("replay", logx.DispositionRetry,
				"session resume: inflight rows pending",
				"client", c.clientID, "count", len(rows))
		}
	}

	for {
		select {
		case <-c.doneCh:
			return
		case <-c.kickCh:
			reconcile()
		case pid := <-c.writtenCh:
			if p := pending[pid]; p != nil {
				p.queued = false
				p.written = true
				p.lastSent = now()
			}
		case pid := <-c.ackCh:
			delete(pending, pid)
		case <-t.C:
			reconcile()
		}
	}
}

// shutdown is called by the broker (takeover/server stop).
func (c *client) shutdown(r closeReason) {
	c.endOnce.Do(func() {
		close(c.doneCh)
		_ = c.conn.Close()
	})
}

// terminate performs end-of-session cleanup: will publication, durable
// retention or clean wipe.
func (c *client) terminate(r closeReason) {
	b := c.b
	c.endOnce.Do(func() { close(c.doneCh) })
	c.conn.Close()

	// Will semantics (3.1.2.6/3.14.4): a will is published on an ABNORMAL
	// disconnect (network drop, keep-alive expiry, takeover, protocol
	// violation, resource limit, internal error) and suppressed only after a
	// DISCONNECT packet or a graceful server shutdown.
	publishWill := c.hasWill
	if r == reasonClient || r == reasonServer {
		publishWill = false
	}
	if publishWill {
		b.publishWill(c)
	}
	b.deregister(c, r)
	c.log.Event("disconnect", dispositionForReason(r),
		"session ended", "client", c.clientID, "reason", r,
		"clean", c.clean, "will_published", publishWill)
}

// deregister removes the live client entry and, for clean sessions, wipes all
// persisted state; durable sessions keep subscriptions and inflight rows.
func (b *Broker) deregister(c *client, r closeReason) {
	b.mtx.mu.Lock()
	defer b.mtx.mu.Unlock()
	// If this connection was taken over, the current map entry is the
	// successor; it owns the durable state and this teardown must not wipe it.
	if cur := b.mtx.clients[c.clientID]; cur != c {
		return
	}
	delete(b.mtx.clients, c.clientID)
	if c.clean {
		subs, _ := b.st.ListSubscriptions(c.clientID)
		b.mtx.index.removeClient(c.clientID)
		b.mtx.metrics.SubscriptionsCurrent.Add(-int64(len(subs)))
		n, _ := b.st.CountInflight(c.clientID)
		b.mtx.metrics.InflightCurrent.Add(-int64(n))
		_ = b.st.WipeClient(c.clientID)
	}
}

// helpers -------------------------------------------------------------------

func splitLevels(filter string) []string {
	if filter == "" {
		return nil
	}
	return strings.Split(filter, "/")
}

func generatedID(seq int64) string {
	return "auto-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatInt(seq, 36)
}

// varLenLen returns the number of remaining-length bytes present in the frame
// (1..4), assuming a well-formed frame; ReadFrame guarantees this.
func varLenLen(frame []byte) int {
	for i := 1; i <= 4 && i < len(frame); i++ {
		if frame[i]&0x80 == 0 {
			return i
		}
	}
	return 1
}

func dispositionFor(err error) string {
	pe := packet.AsError(err)
	if pe == nil {
		return logx.DispositionInternalError
	}
	switch pe.Cat {
	case packet.CatInvalid:
		return logx.DispositionRejectInvalid
	case packet.CatUnsupported:
		return logx.DispositionRejectUnsupported
	case packet.CatLimit:
		return logx.DispositionRejectLimit
	case packet.CatState:
		return logx.DispositionStateConflict
	default:
		return logx.DispositionInternalError
	}
}

func dispositionForReason(r closeReason) string {
	switch r {
	case reasonClient, reasonServer:
		return logx.DispositionAccept
	case reasonAbnormal, reasonKeepAlive, reasonTakeover:
		return logx.DispositionStateConflict
	case reasonProtocol:
		return logx.DispositionRejectInvalid
	case reasonUnsupported:
		return logx.DispositionRejectUnsupported
	case reasonLimit:
		return logx.DispositionRejectLimit
	default:
		return logx.DispositionInternalError
	}
}

// ensure store import used (InflightRow construction lives in routing.go).
var _ = store.InflightRow{}
