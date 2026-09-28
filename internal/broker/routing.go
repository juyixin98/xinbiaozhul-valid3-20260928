package broker

import (
	"time"

	"mqttd/internal/logx"
	"mqttd/internal/packet"
	"mqttd/internal/store"
	"mqttd/internal/topics"
)

// routePublish fans one validated PUBLISH out to matching subscribers.
//
// Delivery semantics:
//   - Granted QoS = min(published QoS, subscription QoS) (3.8.4/4.3).
//   - QoS 0 fan-out is enqueued directly (dropped if the slow-consumer queue
//     is full; counted).
//   - QoS 1 fan-out persists an inflight row BEFORE the frame is queued, so a
//     crash after a successful send (or after the publisher's PUBACK) never
//     loses the message. Delivery is at-least-once: replays set DUP=1.
//   - One message is delivered at most once per client even if several of the
//     client's filters match; the highest matching granted QoS wins.
//   - Frames fanned out from a normal PUBLISH always carry RETAIN=0 (3.3.1.3);
//     only the retained-image delivery (see deliverRetained) sets RETAIN=1.
func (b *Broker) routePublish(pub *packet.Publish) {
	b.mtx.mu.Lock()
	levels := splitLevels(pub.Topic)
	dollar := len(pub.Topic) > 0 && pub.Topic[0] == '$'
	matches := b.mtx.index.match(levels, dollar)
	targets := dedupGrants(matches)
	clients := make(map[string]*client, len(targets))
	for cid := range targets {
		clients[cid] = b.mtx.clients[cid] // nil if offline
	}
	b.mtx.mu.Unlock()

	for cid, grantedQoS := range targets {
		effective := pub.QoS
		if grantedQoS < effective {
			effective = grantedQoS
		}
		c := clients[cid]
		if effective == 0 {
			if c == nil {
				// Offline durable client with only QoS 0 matching filters:
				// QoS 0 messages are not queued (MQTT 4.1 session state).
				continue
			}
			out := &packet.Publish{
				QoS: 0, Retain: false, Topic: pub.Topic, Payload: pub.Payload,
			}
			c.enqueueQoS0(out.Encode())
			continue
		}
		// QoS 1 path: persist first (online or offline durable client).
		pid, err := b.allocatePacketID(cid)
		if err != nil {
			b.log.Event("deliver", logx.DispositionRejectLimit,
				"no packet id available", "client", cid, "err", err.Error())
			continue
		}
		row := store.InflightRow{
			ClientID: cid,
			PacketID: pid,
			Topic:    pub.Topic,
			Payload:  pub.Payload,
			QoS:      1,
			Retain:   false,
			Sent:     false,
		}
		if err := b.st.InsertInflight(row, time.Now().UnixNano()); err != nil {
			b.log.Event("deliver", logx.DispositionInternalError,
				"persist inflight failed", "client", cid, "pid", pid, "err", err.Error())
			continue
		}
		b.mtx.metrics.InflightCurrent.Add(1)
		if c == nil {
			// Durable offline client: the row waits for reconnect replay.
			b.log.Event("deliver", logx.DispositionAccept,
				"queued QoS 1 for offline durable client",
				"client", cid, "pid", pid, "topic", pub.Topic)
			continue
		}
		// The client's single delivery pump performs the actual enqueue,
		// guaranteeing initial send vs. retransmit are never duplicated.
		c.kick()
	}
}

// dedupGrants collapses multiple matching filters per client to the highest
// granted QoS for that client.
func dedupGrants(ms []subMember) map[string]byte {
	out := make(map[string]byte, len(ms))
	for _, m := range ms {
		if q, ok := out[m.clientID]; !ok || m.qos > q {
			out[m.clientID] = m.qos
		}
	}
	return out
}

// allocatePacketID returns a currently-unused 1..65535 identifier for one
// subscriber-facing QoS 1 delivery. Identifiers are scoped per client (the
// direction broker->client has its own space, MQTT 2.3.1) and chosen by a
// scan of the inflight table; this is linear in window size but the window is
// bounded by MaxInflightPerClient, and it makes persistence the single source
// of truth across reconnects and broker restarts.
func (b *Broker) allocatePacketID(clientID string) (uint16, error) {
	rows, err := b.st.ListInflight(clientID)
	if err != nil {
		return 0, err
	}
	used := make(map[uint16]struct{}, len(rows))
	for _, r := range rows {
		used[r.PacketID] = struct{}{}
	}
	if len(used) >= b.cfg.MaxInflightPerClient {
		b.mtx.metrics.RejectedInflight.Add(1)
		return 0, packet.New(packet.CatLimit,
			"client %s has %d unacknowledged QoS 1 deliveries (limit %d)",
			clientID, len(used), b.cfg.MaxInflightPerClient)
	}
	for id := uint16(1); id != 0; id++ {
		if _, taken := used[id]; !taken {
			return id, nil
		}
	}
	// Mathematically unreachable given the limit above; never wrap to 0
	// (packet id 0 is illegal).
	return 0, packet.New(packet.CatLimit, "packet identifier space exhausted")
}

// applyRetained stores or deletes the retained image for a topic.
func (b *Broker) applyRetained(pub *packet.Publish) error {
	if len(pub.Payload) == 0 {
		// Empty payload with RETAIN=1 deletes the retained message (3.3.1.3).
		if err := b.st.SetRetained(pub.Topic, nil, pub.QoS, time.Now().UnixNano()); err != nil {
			return err
		}
		// RowsAffected is ignored: deleting a non-existent retained topic is
		// idempotent; the metric is reconciled from the authoritative count.
		if n, err := b.st.CountRetained(); err == nil {
			b.mtx.metrics.RetainedCurrent.Store(int64(n))
		}
		b.log.Event("retain", logx.DispositionAccept,
			"retained message cleared", "topic", pub.Topic)
		return nil
	}
	n, err := b.st.CountRetained()
	if err != nil {
		return err
	}
	// Overwriting an existing topic must not trip the cap; only reject a new
	// topic once the bound is reached.
	if n >= b.cfg.MaxRetained && !b.retainedExists(pub.Topic) {
		b.log.Event("retain", logx.DispositionRejectLimit,
			"retained message limit reached", "topic", pub.Topic,
			"limit", b.cfg.MaxRetained)
		return packet.New(packet.CatLimit, "retained message limit reached")
	}
	if err := b.st.SetRetained(pub.Topic, pub.Payload, pub.QoS, time.Now().UnixNano()); err != nil {
		return err
	}
	if n2, err := b.st.CountRetained(); err == nil {
		b.mtx.metrics.RetainedCurrent.Store(int64(n2))
	}
	b.log.Event("retain", logx.DispositionAccept,
		"retained message stored/replaced", "topic", pub.Topic,
		"qos", pub.QoS, "bytes", len(pub.Payload))
	return nil
}

func (b *Broker) retainedExists(topic string) bool {
	rows, err := b.st.RetainedMatching(func(_, t string) bool { return t == topic }, topic)
	if err != nil {
		return false
	}
	return len(rows) > 0
}

// deliverRetained sends the retained image for one newly accepted filter.
// Retained delivery downgrades QoS as with live routing and sets RETAIN=1.
func (c *client) deliverRetained(f packet.SubFilter) error {
	rows, err := c.b.st.RetainedMatching(topics.Match, f.Topic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		qos := r.QoS
		if f.QoS < qos {
			qos = f.QoS
		}
		out := &packet.Publish{
			QoS: qos, Retain: true, Topic: r.Topic, Payload: r.Payload,
		}
		if qos == 0 {
			c.enqueueQoS0(out.Encode())
			continue
		}
		// QoS 1 retained images also use the durable inflight machinery:
		// persist with a fresh packet id, then let the delivery pump enqueue.
		pid, err := c.b.allocatePacketID(c.clientID)
		if err != nil {
			// Per-client inflight cap: skip this retained image; the client
			// can re-subscribe later. Logged distinctly as a limit.
			c.b.log.Event("retain", logx.DispositionRejectLimit,
				"cannot deliver retained image: inflight full",
				"client", c.clientID, "topic", r.Topic)
			continue
		}
		row := store.InflightRow{
			ClientID: c.clientID, PacketID: pid, Topic: r.Topic,
			Payload: r.Payload, QoS: 1, Retain: true, Sent: false,
		}
		if err := c.b.st.InsertInflight(row, time.Now().UnixNano()); err != nil {
			return err
		}
		c.b.mtx.metrics.InflightCurrent.Add(1)
		c.kick()
	}
	return nil
}

// publishWill fans out a client's will message as a normal PUBLISH (and stores
// it when willRetain is set).
func (b *Broker) publishWill(c *client) {
	pub := &packet.Publish{
		QoS: c.willQoS, Retain: c.willRetain,
		Topic: c.willTopic, Payload: c.willPayload,
	}
	b.mtx.metrics.WillPublished.Add(1)
	b.log.Event("will", logx.DispositionAccept, "publishing will message",
		"client", c.clientID, "topic", c.willTopic, "qos", c.willQoS,
		"retain", c.willRetain)
	if c.willRetain {
		if err := b.applyRetained(pub); err != nil {
			b.log.Event("will", logx.DispositionInternalError,
				"will retain failed", "err", err.Error())
		}
	}
	b.routePublish(pub)
}

// countDurableSessions must be called under mtx.mu.
func (b *Broker) countDurableSessions() int {
	n, err := b.st.CountDurableSessions()
	if err != nil {
		return 0
	}
	return n
}
