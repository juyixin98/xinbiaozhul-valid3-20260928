package broker

// End finalizes an attached connection. It must be invoked exactly once per
// successful CONNECT, by the server's connection goroutine or by an
// asynchronous take-over. The Will semantics:
//
//   - ReasonClientClosed (clean DISCONNECT): Will deleted, never published
//     (MQTT-3.14.4-3).
//   - ReasonTakenOver: suppressed by the take-over path (terminate nilled).
//   - ReasonServerShutdown: suppressed (orderly shutdown, state retained).
//   - any other abnormal close (protocol error, keep-alive, resource/network
//     failure): Will published if registered (MQTT-3.1.2-8).
//
// Durable sessions remain stored (subscriptions/inflight/offline); clean
// sessions are deleted entirely.
func (b *Broker) End(cl *Conn, reason *CloseError) {
	b.mu.Lock()
	defer b.mu.Unlock()

	cur := b.clients[cl.clientID]
	if cur != cl {
		// Already taken over by a newer connection: nothing to do.
		return
	}
	delete(b.clients, cl.clientID)
	cl.closed = true
	cl.epoch++

	publishWill := false
	removeWill := false

	switch reason.Reason {
	case ReasonClientClosed:
		removeWill = true
	case ReasonServerShutdown, ReasonTakenOver:
		// Suppressed.
	default:
		publishWill = true
		if cl.clean {
			// Clean sessions publish the Will but persist nothing.
		}
	}

	will := cl.will
	if removeWill {
		will = nil
		if !cl.clean {
			_ = b.st.SetWill(cl.clientID, nil)
		}
	}
	if !cl.clean && will == nil && cl.will != nil {
		_ = b.st.SetWill(cl.clientID, nil)
	}

	b.log.Event("conn.end", cl.id,
		"client_id", cl.clientID, "reason", reason.Reason, "detail", reason.Detail,
		"will_published", publishWill && will != nil, "clean", cl.clean)

	switch reason.Reason {
	case ReasonProtocolViolation:
		b.stats.ClosedProtocol++
	case ReasonResourceExhaustion:
		b.stats.ClosedResource++
	case ReasonKeepAliveTimeout:
		b.stats.ClosedKeepAlive++
	case ReasonNetworkError:
		b.stats.ClosedNetwork++
	}

	if cl.clean {
		// Memory-only state simply disappears. No inflight persistence, no
		// offline queue, no session row.
	} else {
		// In-memory inflight stays mirrored in SQLite (nothing to delete);
		// the session reconnects and gets them with DUP=1.
	}

	// Will fan-out is performed while holding mu; payloads are small and
	// the fanout never blocks on I/O (channel enqueue only).
	if publishWill && will != nil {
		b.fanoutLocked(will.Topic, append([]byte(nil), will.Payload...), will.QoS)
		// Retained Will: published AND stored (MQTT-3.1.2-10).
		if will.Retain {
			if err := b.st.SetRetained(will.Topic, append([]byte(nil), will.Payload...), will.QoS); err != nil {
				b.log.Failure("will.retained_store", cl.id, err, "topic", will.Topic)
			}
		}
		b.log.Decision("will.publish", cl.id, "client_id", cl.clientID,
			"topic", will.Topic, "qos", will.QoS, "retain", will.Retain)
	}
}

// MarkCleanDisconnect is called when the client sent a well-formed DISCONNECT.
// Will suppression itself happens in End() via ReasonClientClosed; this only
// cancels any pending asynchronous transport close.
func (b *Broker) MarkCleanDisconnect(cl *Conn) {
	b.mu.Lock()
	cl.requestClose = nil
	b.log.Event("disconnect.recv", cl.id, "client_id", cl.clientID, "rule", "MQTT-3.14.4-3 no will")
	b.mu.Unlock()
}

// RecoverDurableWills handles broker restart: every durable session row with
// a stored Will represents a client that did not disconnect cleanly before
// the crash, so its Will is published once and cleared (MQTT-3.1.2-7).
func (b *Broker) RecoverDurableWills() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	ids, err := b.st.AllDurableClients()
	if err != nil {
		return err
	}
	for _, id := range ids {
		row, err := b.st.GetSession(id)
		if err != nil {
			return err
		}
		if row.Will == nil {
			continue
		}
		b.log.Event("startup.recover_will", 0, "client_id", id, "topic", row.Will.Topic)
		b.fanoutLocked(row.Will.Topic, append([]byte(nil), row.Will.Payload...), row.Will.QoS)
		if row.Will.Retain {
			_ = b.st.SetRetained(row.Will.Topic, append([]byte(nil), row.Will.Payload...), row.Will.QoS)
		}
		if err := b.st.SetWill(id, nil); err != nil {
			return err
		}
	}
	return nil
}

// CloseAll detaches all live connections without publishing Wills (orderly
// shutdown). It returns the encoded frame the server may try to write first
// (none — MQTT has no server DISCONNECT in 3.1.1) and the count.
func (b *Broker) CloseAll() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, cl := range b.clients {
		if cl.closed {
			continue
		}
		cl.closed = true
		cl.requestClose = nil
		cl.epoch++
		close(cl.closeCh)
		n++
	}
	return n
}
