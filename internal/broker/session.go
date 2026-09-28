package broker

import (
	"time"

	"mqttlocal/internal/packet"
	"mqttlocal/internal/store"
)

// ConnectResult is the broker's verdict on a CONNECT. When Accepted is false
// ConnAckCode carries the code the server must send before closing.
type ConnectResult struct {
	Accepted       bool
	ConnAckCode    byte
	SessionPresent bool
	ClientID       string
	Reason         string
}

// TerminateFunc is supplied by the server when a connection attaches. It
// closes the network so the reader unblocks, and is invoked when the broker
// must drop a connection asynchronously (take-over, slow subscriber).
type TerminateFunc func(reason *CloseError)

// HandleConnect applies a decoded CONNECT to broker state. On success the
// returned *Conn is attached; the caller sends CONNACK per the result.
func (b *Broker) HandleConnect(connID uint64, c *packet.Connect, tr TerminateFunc) (*Conn, *ConnectResult) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "broker shutting down"}
	}
	res := &ConnectResult{ClientID: c.ClientID}

	// Will QoS 2: subset policy is explicit refusal per the task contract —
	// CONNACK 0x03 Server Unavailable, then close.
	if c.HasWill && c.WillQoS == 2 {
		b.stats.RejectedConnects++
		b.log.Reject("connect.will_qos2", connID, "client_id", c.ClientID, "rule", "subset: no QoS2 -> connack 0x03")
		return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "Will QoS 2 is not supported by this subset"}
	}

	if len(b.clients) >= b.cfg.MaxConnections {
		b.stats.RejectedConnects++
		b.log.Reject("connect.connection_cap", connID, "client_id", c.ClientID, "cap", b.cfg.MaxConnections)
		return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "connection limit reached"}
	}

	clientID := c.ClientID
	generated := false
	if clientID == "" {
		// Valid only with CleanSession=1 (decoder-enforced). Server-assigned
		// ids are namespaced so they cannot collide with a client id.
		clientID = autoClientID(connID)
		generated = true
	}

	// Take-over: an existing connection with the same id is closed and its
	// Will is NOT published (MQTT-3.1.4-2).
	if old := b.clients[clientID]; old != nil {
		b.stats.TakenOver++
		b.log.Event("takeover.begin", connID, "client_id", clientID, "old_conn", old.id)
		old.closed = true

		old.epoch++
		// The writer/reader goroutines unwind through the channel close.
		close(old.closeCh)
		if old.requestClose != nil {
			fn := old.requestClose
			old.requestClose = nil
			go fn(newClose(ReasonTakenOver, "client id taken over by new connection"))
		}
	}

	sessionPresent := false
	cl := &Conn{
		id:           connID,
		clientID:     clientID,
		clean:        c.CleanSession,
		send:         make(chan []byte, b.cfg.SendQueueDepth),
		closeCh:      make(chan struct{}),
		subs:         map[string]*subscription{},
		inflight:     map[uint16]*store.StoredMessage{},
		keepAlive:    c.KeepAlive,
		requestClose: tr,
	}

	if !c.CleanSession {
		exists, err := b.st.SessionExists(clientID)
		if err != nil {
			b.log.Failure("connect.session_lookup", connID, err, "client_id", clientID)
			return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "storage failure"}
		}
		if exists {
			sessionPresent = true
			row, err := b.st.GetSession(clientID)
			if err != nil {
				b.log.Failure("connect.session_load", connID, err, "client_id", clientID)
				return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "storage failure"}
			}
			cl.nextOutID = row.NextPacketID
			// The Will belongs to a NETWORK connection: a new CONNECT
			// re-establishes it below. Do NOT carry the stored Will over —
			// a reconnect with Will Flag=0 has no Will (and the stored
			// one is cleared below).
			subs, err := b.st.Subscriptions(clientID)
			if err != nil {
				b.log.Failure("connect.subs_load", connID, err, "client_id", clientID)
				return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "storage failure"}
			}
			for f, q := range subs {
				cl.subs[f] = &subscription{filter: f, qos: q}
			}
		} else {
			if err := b.st.SaveSession(clientID, 1); err != nil {
				b.log.Failure("connect.session_save", connID, err, "client_id", clientID)
				return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "storage failure"}
			}
			cl.nextOutID = 0
		}
	} else {
		// CleanSession=1 against a pre-existing durable session: the stored
		// session (subscriptions, inflight, offline, will) MUST be discarded
		// (MQTT-3.1.4-4, MQTT-3.1.2-6). A same-id connection cannot have
		// reached here without having been taken over above.
		if exists, err := b.st.SessionExists(clientID); err == nil && exists {
			if err := b.st.DeleteSession(clientID); err != nil {
				b.log.Failure("connect.session_purge", connID, err, "client_id", clientID)
				return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "storage failure"}
			}
			b.log.Event("connect.purge_durable", connID,
				"client_id", clientID, "rule", "MQTT-3.1.4-4 clean start discards stored session")
		}
	}

	// Register/refresh the Will. A new CONNECT without a Will flag
	// replaces (removes) any Will from the previous connection.
	if c.HasWill {
		w := &store.WillState{Topic: c.WillTopic, Payload: c.WillMessage, QoS: c.WillQoS, Retain: c.WillRetain}
		cl.will = w
		if !c.CleanSession {
			if err := b.st.SetWill(clientID, w); err != nil {
				b.log.Failure("connect.set_will", connID, err, "client_id", clientID)
				return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "storage failure"}
			}
		}
	} else if !c.CleanSession {
		// Durable reconnect without a Will: discard any stored one.
		if err := b.st.SetWill(clientID, nil); err != nil {
			b.log.Failure("connect.clear_will", connID, err, "client_id", clientID)
			return nil, &ConnectResult{ConnAckCode: packet.ConnServerUnavailable, Reason: "storage failure"}
		}
	}

	b.clients[clientID] = cl
	b.stats.AcceptedConnects++
	b.log.Decision("connect.accept", connID,
		"client_id", clientID, "clean", c.CleanSession,
		"session_present", sessionPresent, "generated_id", generated,
		"keepalive", c.KeepAlive, "will", c.HasWill)

	res.Accepted = true
	res.SessionPresent = sessionPresent
	res.ClientID = clientID
	return cl, res
}

func autoClientID(connID uint64) string {
	// Namespaced with '#' which can never appear in a client-supplied id?
	// No — '#' is not forbidden in Client IDs. Use a fixed prefix instead;
	// collision is astronomically unlikely and, per spec, a conflicting
	// take-over would close the auto session anyway.
	return "mqttlocal-auto-" + time.Now().Format("20060102T150405.000000") + "-" + itoa(connID)
}

func itoa(u uint64) string {
	if u == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}
