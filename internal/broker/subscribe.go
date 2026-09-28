package broker

import (
	"mqttlocal/internal/packet"
	"mqttlocal/internal/store"
)

// SubscribeResult is the SUBACK decision per requested filter.
type SubscribeResult struct {
	PacketID uint16
	Granted  []byte // one byte per filter: 0x00, 0x01 or 0x80
}

// HandleSubscribe processes a SUBSCRIBE and, on success, enqueues matching
// retained messages (MQTT-3.3.1-6) after the SUBACK. The caller sends
// SubAckFrame() then; retained frames are already queued on the writer.
func (b *Broker) HandleSubscribe(cl *Conn, s *packet.Subscribe) (*SubscribeResult, *CloseError) {
	b.mu.Lock()
	defer b.mu.Unlock()

	cur := b.clients[cl.clientID]
	if cur != cl {
		return nil, newClose(ReasonStateConflict, "stale connection after take-over")
	}

	res := &SubscribeResult{PacketID: s.PacketID}
	var newlyAdded []*subscription
	for _, sub := range s.Subscriptions {
		if !packet.ValidTopicFilter(sub.Filter) {
			// Malformed filter is a protocol violation (MQTT-4.7 / SUBSCRIBE
			// constraints): close the connection rather than grant 0x80 —
			// 0x80 is reserved for policy/administrative refusal, which is
			// exactly the QoS 2 case here.
			b.log.Reject("subscribe.bad_filter", cl.id, "client_id", cl.clientID, "filter", sub.Filter)
			return nil, newClose(ReasonProtocolViolation, "invalid topic filter %q", sub.Filter)
		}
		if sub.QoS == 2 {
			b.log.Reject("subscribe.qos2", cl.id, "client_id", cl.clientID, "filter", sub.Filter,
				"rule", "subset: grant 0x80, connection continues")
			res.Granted = append(res.Granted, packet.SubAckFailure)
			continue
		}
		// Repeated filter: update granted QoS; retained delivery still
		// happens (allowed, observable behaviour for fresh subscribe).
		existing := cl.subs[sub.Filter]
		if existing == nil {
			newSub := &subscription{filter: sub.Filter, qos: sub.QoS}
			cl.subs[sub.Filter] = newSub
			newlyAdded = append(newlyAdded, newSub)
		} else {
			existing.qos = sub.QoS
		}
		if !cl.clean {
			if err := b.st.PutSubscription(cl.clientID, sub.Filter, sub.QoS); err != nil {
				b.log.Failure("subscribe.persist", cl.id, err, "filter", sub.Filter)
				return nil, newClose(ReasonNetworkError, "subscription persistence failure")
			}
		}
		res.Granted = append(res.Granted, sub.QoS)
		b.stats.Subscriptions++
	}

	b.log.Decision("subscribe.ack", cl.id, "client_id", cl.clientID,
		"packet_id", s.PacketID, "granted", res.Granted, "new_filters", len(newlyAdded))

	// SUBACK must precede the retained messages that this SUBSCRIBE
	// triggers (MQTT-3.8.4-5); both share the single ordered send channel.
	cl.enqueueSend(packet.EncodeSuback(s.PacketID, res.Granted))

	// Retained delivery for the accepted, newly added filters: one message
	// per retained topic matching any of them, QoS = min(retained qos,
	// granted qos), RETAIN flag set on the delivery (MQTT-3.3.1-6/7).
	if len(newlyAdded) > 0 {
		retained, err := b.st.AllRetained()
		if err != nil {
			b.log.Failure("subscribe.retained_load", cl.id, err)
		} else {
			delivered := map[string]bool{}
			for _, m := range retained {
				granted, ok := matchSubscription(newlyAddedSet(newlyAdded), m.Topic)
				if !ok || delivered[m.Topic] {
					continue
				}
				delivered[m.Topic] = true
				q := byte(m.QoS)
				if q > granted {
					q = granted
				}
				if q == 0 {
					frame := packet.EncodePublish(m.Topic, m.Payload, 0, 0, false, true)
					if !cl.enqueueSend(frame) {
						b.kickSlowLocked(cl, "send queue full during retained delivery")
						return res, nil
					}
					b.stats.SentQos0++
					continue
				}
				if len(cl.inflight) >= b.cfg.MaxInflightPerSession {
					b.kickSlowLocked(cl, "inflight window exhausted during retained delivery")
					return res, nil
				}
				id := cl.allocPacketID()
				if id == 0 {
					b.kickSlowLocked(cl, "no free packet identifier during retained delivery")
					return res, nil
				}
				sm := &store.StoredMessage{Topic: m.Topic, Payload: m.Payload, QoS: 1, Retain: true, PacketID: id}
				cl.inflight[id] = sm
				if !cl.clean {
					_ = b.st.PutInflight(cl.clientID, *sm)
					_ = b.st.SetNextPacketID(cl.clientID, nextAfter(id))
				}
				frame := packet.EncodePublish(m.Topic, m.Payload, id, 1, false, true)
				if !cl.enqueueSend(frame) {
					sm.Dup = true
					if !cl.clean {
						_ = b.st.PutInflight(cl.clientID, *sm)
					}
					b.kickSlowLocked(cl, "send queue full during retained QoS 1 delivery")
					return res, nil
				}
				b.stats.SentQos1++
			}
		}
	}

	return res, nil
}

func newlyAddedSet(subs []*subscription) map[string]*subscription {
	m := make(map[string]*subscription, len(subs))
	for _, s := range subs {
		m[s.filter] = s
	}
	return m
}

// HandlePuback removes one outbound QoS1 message from the inflight state.
// An unknown identifier is a protocol error (MQTT-2.3.1: PUBACK refers to a
// known flow; spec text uses "the PUBLISH that is being acknowledged").
func (b *Broker) HandlePuback(cl *Conn, id uint16) *CloseError {
	b.mu.Lock()
	defer b.mu.Unlock()

	cur := b.clients[cl.clientID]
	if cur != cl {
		return newClose(ReasonStateConflict, "stale connection after take-over")
	}
	m, ok := cl.inflight[id]
	if !ok {
		b.log.Reject("puback.unknown", cl.id, "client_id", cl.clientID, "packet_id", id,
			"rule", "PUBACK for unknown identifier is a protocol violation")
		return newClose(ReasonProtocolViolation, "PUBACK packet identifier %d does not match an unacknowledged PUBLISH", id)
	}
	delete(cl.inflight, id)
	if !cl.clean {
		if err := b.st.DeleteInflight(cl.clientID, id); err != nil {
			b.log.Failure("puback.delete_inflight", cl.id, err, "packet_id", id)
		}
	}
	b.stats.PubacksReceived++
	b.log.Event("puback.recv", cl.id, "client_id", cl.clientID, "packet_id", id, "topic", m.Topic)

	// Reconnect while online: an ACK may free window that offline-queued
	// messages are waiting for. Promote one queued message if possible.
	if !cl.clean && !cl.closed {
		b.tryPromoteOfflineLocked(cl)
	}
	return nil
}

// RestoreSession enqueues redelivery of persisted inflight messages (DUP=1,
// same identifiers) and then promotes offline-queued QoS1 messages as window
// allows. Called after CONNACK is written on a reconnect with SessionPresent.
func (b *Broker) RestoreSession(cl *Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clients[cl.clientID] != cl {
		return
	}
	if cl.clean {
		return
	}

	inflight, err := b.st.Inflight(cl.clientID)
	if err != nil {
		b.log.Failure("restore.inflight_load", cl.id, err, "client_id", cl.clientID)
		return
	}
	for i := range inflight {
		m := inflight[i]
		// Redelivery uses the SAME packet identifier and DUP=1
		// (MQTT-4.4.0-1, MQTT-3.3.1-1).
		m.Dup = true
		cl.inflight[m.PacketID] = &m
		_ = b.st.PutInflight(cl.clientID, m)
		frame := packet.EncodePublish(m.Topic, m.Payload, m.PacketID, 1, true, m.Retain)
		if !cl.enqueueSend(frame) {
			b.kickSlowLocked(cl, "send queue full during inflight redelivery")
			return
		}
		b.stats.Redeliveries++
		b.log.Decision("restore.redeliver", cl.id,
			"client_id", cl.clientID, "packet_id", m.PacketID,
			"topic", m.Topic, "dup", true)
	}
	b.tryPromoteOfflineLocked(cl)
}

// tryPromoteOfflineLocked moves queued offline QoS1 messages into inflight
// while window and send queue permit. Caller holds b.mu.
func (b *Broker) tryPromoteOfflineLocked(cl *Conn) {
	room := b.cfg.MaxInflightPerSession - len(cl.inflight)
	if room <= 0 || cl.closed {
		return
	}
	msgs, err := b.st.PopOffline(cl.clientID, room)
	if err != nil {
		b.log.Failure("offline.pop", cl.id, err, "client_id", cl.clientID)
		return
	}
	for i := range msgs {
		m := msgs[i]
		id := cl.allocPacketID()
		if id == 0 {
			// Extremely unlikely (65535 ids all in flight); stop promoting.
			break
		}
		m.PacketID = id
		stored := store.StoredMessage{Topic: m.Topic, Payload: m.Payload, QoS: 1, PacketID: id, Retain: m.Retain}
		cl.inflight[id] = &stored
		_ = b.st.PutInflight(cl.clientID, stored)
		_ = b.st.SetNextPacketID(cl.clientID, nextAfter(id))
		frame := packet.EncodePublish(m.Topic, m.Payload, id, 1, false, m.Retain)
		if !cl.enqueueSend(frame) {
			stored.Dup = true
			_ = b.st.PutInflight(cl.clientID, stored)
			b.kickSlowLocked(cl, "send queue full during offline promotion")
			return
		}
		b.stats.SentQos1++
		b.log.Decision("offline.promote", cl.id,
			"client_id", cl.clientID, "packet_id", id, "topic", m.Topic, "dup", false)
	}
}
