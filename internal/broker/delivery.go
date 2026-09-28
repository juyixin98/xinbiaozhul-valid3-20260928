package broker

import (
	"mqttlocal/internal/packet"
	"mqttlocal/internal/store"
	"mqttlocal/internal/topic"
)

// HandlePublish applies a client PUBLISH to broker state: retained store
// mutation and fan-out delivery. Returned ackID is the packet identifier to
// PUBACK for QoS 1 input (0 for QoS 0). A non-nil CloseError mandates
// connection termination.
func (b *Broker) HandlePublish(cl *Conn, p *packet.Publish) (ackID uint16, closeErr *CloseError) {
	b.mu.Lock()
	defer b.mu.Unlock()

	cur := b.clients[cl.clientID]
	if cur != cl {
		return 0, newClose(ReasonStateConflict, "stale connection after take-over")
	}

	// Explicit refusal of out-of-subset input: an inbound QoS 2 PUBLISH is
	// structurally legal MQTT 3.1.1 but unsupported here.
	if p.QoS == 2 {
		b.log.Reject("publish.qos2", cl.id, "client_id", cl.clientID, "rule", "subset: QoS2 inbound refused")
		return 0, newClose(ReasonProtocolViolation, "PUBLISH QoS 2 is not supported by this subset")
	}

	if p.QoS == 1 {
		b.stats.ReceivedQos1++
	} else {
		b.stats.ReceivedQos0++
	}

	// Retained message handling (MQTT-3.3.1-3..5): RETAIN=1 stores the
	// message (empty payload deletes); retained messages do NOT fan out.
	if p.Retain {
		if err := b.st.SetRetained(p.Topic, p.Payload, p.QoS); err != nil {
			b.log.Failure("publish.retained_store", cl.id, err, "topic", p.Topic)
			return 0, newClose(ReasonNetworkError, "retained store failure")
		}
		b.log.Decision("retained.set", cl.id, "client_id", cl.clientID, "topic", p.Topic, "deleted", len(p.Payload) == 0)
	} else {
		b.fanoutLocked(p.Topic, p.Payload, p.QoS)
	}

	if p.QoS == 1 {
		// Duplicates (DUP=1 with a reused id) are legal QoS 1 input; the
		// broker's ACK contract is idempotent. The identifier is validated
		// structurally (non-zero) by the decoder.
		b.log.Event("publish.recv_qos1", cl.id,
			"client_id", cl.clientID, "packet_id", p.PacketID,
			"dup", p.Dup, "topic", p.Topic)
		cl.enqueueSend(packet.EncodePuback(p.PacketID))
		return p.PacketID, nil
	}
	return 0, nil
}

// fanoutLocked delivers a normal PUBLISH to every matching online and
// durable-offline subscriber. QoS is downgraded to each subscription's
// granted level (MQTT-3.8.4 / §4.3 note: granted QoS is the delivery cap).
// Caller holds b.mu.
func (b *Broker) fanoutLocked(topicName string, payload []byte, pubQoS byte) {
	deliveredQos0 := 0
	deliveredQos1 := 0
	queuedOffline := 0

	// Online subscribers.
	for _, cl := range b.clients {
		granted, ok := matchSubscription(cl.subs, topicName)
		if !ok {
			continue
		}
		q := pubQoS
		if q > granted {
			q = granted
		}
		if q == 0 {
			frame := packet.EncodePublish(topicName, payload, 0, 0, false, false)
			if cl.enqueueSend(frame) {
				b.stats.SentQos0++
				deliveredQos0++
			} else {
				b.kickSlowLocked(cl, "send queue full on QoS 0 delivery")
			}
			continue
		}
		// QoS 1.
		if len(cl.inflight) >= b.cfg.MaxInflightPerSession {
			b.kickSlowLocked(cl, "inflight window exhausted")
			continue
		}
		id := cl.allocPacketID()
		if id == 0 {
			b.kickSlowLocked(cl, "no free packet identifier")
			continue
		}
		m := &store.StoredMessage{Topic: topicName, Payload: append([]byte(nil), payload...), QoS: 1, PacketID: id}
		cl.inflight[id] = m
		if !cl.clean {
			if err := b.st.PutInflight(cl.clientID, *m); err != nil {
				b.log.Failure("fanout.persist_inflight", cl.id, err, "client_id", cl.clientID)
			}
			if err := b.st.SetNextPacketID(cl.clientID, nextAfter(id)); err != nil {
				b.log.Failure("fanout.persist_hint", cl.id, err, "client_id", cl.clientID)
			}
		}
		// Enqueue after persistence: if the queue is full we still redeliver
		// on reconnect (the inflight record exists with DUP promoted then).
		dup := false
		frame := packet.EncodePublish(topicName, m.Payload, id, 1, dup, false)
		if !cl.enqueueSend(frame) {
			// Never sent: mark DUP for the next (reconnect) attempt and
			// close the slow subscriber now.
			m.Dup = true
			if !cl.clean {
				_ = b.st.PutInflight(cl.clientID, *m)
			}
			b.kickSlowLocked(cl, "send queue full on QoS 1 delivery")
			continue
		}
		b.stats.SentQos1++
		deliveredQos1++
	}

	// Durable offline subscribers.
	online := make(map[string]bool, len(b.clients))
	for id := range b.clients {
		online[id] = true
	}
	offline, err := b.st.OfflineSubscriptions(online)
	if err != nil {
		b.log.Failure("fanout.offline_lookup", 0, err)
		return
	}
	seen := map[string]bool{}
	for _, s := range offline {
		if seen[s.ClientID] {
			continue
		}
		// A session with multiple matching filters queues the message once.
		if !topic.Match(s.Filter, topicName) {
			continue
		}
		seen[s.ClientID] = true
		q := pubQoS
		if q > s.QoS {
			q = s.QoS
		}
		if q == 0 {
			// QoS downgrade to 0: nothing survives while offline
			// (MQTT-4.1.0-2-style session message storage is QoS>=1 only).
			continue
		}
		m := store.StoredMessage{Topic: topicName, Payload: append([]byte(nil), payload...), QoS: 1}
		if err := b.st.EnqueueOffline(s.ClientID, m); err != nil {
			b.log.Failure("fanout.enqueue_offline", 0, err, "client_id", s.ClientID)
			continue
		}
		dropped, err := b.st.TrimOfflineOldest(s.ClientID, b.cfg.MaxOfflinePerSession)
		if err != nil {
			b.log.Failure("fanout.trim_offline", 0, err, "client_id", s.ClientID)
		}
		if dropped > 0 {
			b.stats.DroppedOffline += int64(dropped)
			b.log.Reject("offline.overflow", 0, "client_id", s.ClientID, "dropped_oldest", dropped, "cap", b.cfg.MaxOfflinePerSession)
		}
		queuedOffline++
	}

	b.log.Event("fanout", 0, "topic", topicName, "qos", pubQoS,
		"qos0", deliveredQos0, "qos1", deliveredQos1, "offline", queuedOffline)
}

// matchSubscription returns the granted QoS of the highest-QoS matching
// filter in a session's subscription set. MQTT delivery for overlapping
// filters of one session is defined as "MUST receive" per filter, but this
// subset delivers once using the maximum granted QoS (documented decision,
// README §5).
func matchSubscription(subs map[string]*subscription, name string) (byte, bool) {
	best := byte(0)
	matched := false
	for _, s := range subs {
		if topic.Match(s.filter, name) {
			if !matched || s.qos > best {
				best = s.qos
			}
			matched = true
		}
	}
	return best, matched
}

// kickSlowLocked closes an online subscriber that cannot keep up. Its
// inflight records remain persisted and are redelivered with DUP=1 after
// reconnect; offline messages continue to queue. Caller holds b.mu.
func (b *Broker) kickSlowLocked(cl *Conn, detail string) {
	if cl.closed {
		return
	}
	b.stats.ClosedResource++
	b.log.Reject("slow_subscriber.close", cl.id,
		"client_id", cl.clientID, "detail", detail,
		"inflight", len(cl.inflight), "policy", "close-and-redeliver")
	cl.closed = true
	cl.epoch++
	close(cl.closeCh)
	fn := cl.requestClose
	cl.requestClose = nil
	if fn != nil {
		go fn(newClose(ReasonResourceExhaustion, detail))
	}
}

func nextAfter(id uint16) uint16 {
	if id == 65535 {
		return 1
	}
	return id + 1
}
