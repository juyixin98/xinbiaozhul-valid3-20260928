package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"mqttlocal/internal/broker"
	"mqttlocal/internal/config"
	"mqttlocal/internal/diag"
	"mqttlocal/internal/packet"
)

// connState runs one connection's state machine.
type connState struct {
	cfg config.Config
	br  *broker.Broker
	log *diag.Logger
	id  uint64

	conn net.Conn
	rd   *packet.Reader

	attached *broker.Conn // nil until a CONNECT is accepted

	finishOnce sync.Once
	closeOnce  sync.Once
	closeReq   chan *broker.CloseError
}

func newConnState(cfg config.Config, br *broker.Broker, log *diag.Logger, c net.Conn, id uint64) *connState {
	cs := &connState{
		cfg:      cfg,
		br:       br,
		log:      log,
		id:       id,
		conn:     c,
		closeReq: make(chan *broker.CloseError, 2),
	}
	rd := packet.NewReader(c)
	rd.MaxPayload = cfg.MaxPacketBytes
	cs.rd = rd
	return cs
}

// run executes the lifecycle and guarantees exactly one teardown.
func (cs *connState) run(ctx context.Context) {
	cs.log.Event("conn.open", cs.id, "remote", cs.conn.RemoteAddr().String())

	writerDone := make(chan struct{})
	ctxConn, cancel := context.WithCancel(ctx)
	defer cancel()

	var finalReason *broker.CloseError

	// Phase 1: a single CONNECT, or nothing.
	connect, cr := cs.readConnect()
	if cr != nil {
		finalReason = cr
	} else {
		var res *broker.ConnectResult
		cs.attached, res = cs.br.HandleConnect(cs.id, connect, cs.requestAsyncClose)
		if !res.Accepted {
			// Policy rejection: send CONNACK then close (MQTT-3.1.4-5).
			_ = cs.writeRaw(packet.EncodeConnack(res.ConnAckCode, false))
			cs.log.Reject("connect.rejected", cs.id, "code", res.ConnAckCode, "reason", res.Reason)
			finalReason = broker.NewCloseError(broker.ReasonRejectedConnect, res.Reason)
		} else {
			// Accept: send CONNACK then (durable reconnect) restore flows.
			_ = cs.writeRaw(packet.EncodeConnack(packet.ConnAccepted, res.SessionPresent))
			go func() { // writer
				defer close(writerDone)
				cs.writerLoop(ctxConn)
			}()
			cs.armKeepaliveDeadline(cs.attached.KeepAlive())
			if res.SessionPresent {
				cs.br.RestoreSession(cs.attached)
			}
			finalReason = cs.loop(ctxConn, writerDone)
		}
	}

	cs.finish(finalReason)
}

// readConnect enforces the NEW-state gate: the first packet must be a
// structurally valid CONNECT.
func (cs *connState) readConnect() (*packet.Connect, *broker.CloseError) {
	// CONNECT itself does not honor keep-alive; give the client a bounded
	// time to start the handshake.
	_ = cs.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	pk, err := cs.rd.ReadPacket()
	if err != nil {
		return nil, cs.classifyPreConnect(err)
	}
	if pk.Type != packet.TypeCONNECT {
		cs.log.Reject("connect.first_packet", cs.id, "got", pk.Type.String())
		return nil, broker.NewCloseError(broker.ReasonStateConflict,
			"first packet must be CONNECT, got %s", pk.Type)
	}
	return pk.Connect, nil
}

func (cs *connState) classifyPreConnect(err error) *broker.CloseError {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return broker.NewCloseError(broker.ReasonClientClosed, "closed before CONNECT")
	case errors.Is(err, packet.ErrTooLarge):
		return broker.NewCloseError(broker.ReasonResourceExhaustion, "CONNECT too large: %v", err)
	default:
		var rej *packet.ConnectRejectError
		if errors.As(err, &rej) {
			// The rejection carries a CONNACK code: send it during finish.
			// We cannot return through the accepted path; stash the code.
			_ = cs.writeRaw(packet.EncodeConnack(rej.Code, false))
			cs.log.Reject("connect.connack_code", cs.id, "code", rej.Code, "reason", rej.Reason)
			return broker.NewCloseError(broker.ReasonRejectedConnect, rej.Reason)
		}
		cs.log.Reject("connect.malformed", cs.id, "err", err.Error())
		return broker.NewCloseError(broker.ReasonProtocolViolation, "malformed CONNECT: %v", err)
	}
}

// loop processes packets in READY state until the connection ends.
func (cs *connState) loop(ctx context.Context, writerDone <-chan struct{}) *broker.CloseError {
	// Clear the connect-phase deadline and let keep-alive govern.
	_ = cs.conn.SetReadDeadline(time.Time{})

	for {
		select {
		case <-ctx.Done():
			return broker.NewCloseError(broker.ReasonServerShutdown, "broker shutting down")
		case r := <-cs.closeReq:
			return r
		default:
		}

		pk, err := cs.rd.ReadPacket()
		if err != nil {
			// Prefer an externally requested reason (e.g. take-over raced).
			select {
			case r := <-cs.closeReq:
				return r
			default:
			}
			return cs.classifyRuntime(err)
		}

		cs.armKeepaliveDeadline(cs.attached.KeepAlive())

		if r := cs.dispatch(pk); r != nil {
			return r
		}
	}
}

func (cs *connState) dispatch(pk *packet.Packet) *broker.CloseError {
	switch pk.Type {
	case packet.TypePINGREQ:
		cs.log.Event("ping", cs.id, "client_id", cs.attached.ClientIdentifier())
		if !cs.attached.TrySend(packet.EncodePingresp()) {
			return broker.NewCloseError(broker.ReasonResourceExhaustion, "send queue full on PINGRESP")
		}
		return nil

	case packet.TypePUBLISH:
		// QoS 2 is well-formed MQTT 3.1.1 but explicitly out of this subset.
		if pk.Publish.QoS == 2 {
			cs.log.Reject("publish.qos2", cs.id, "rule", "subset: close on inbound QoS2")
			return broker.NewCloseError(broker.ReasonProtocolViolation,
				"PUBLISH QoS 2 is not supported by this subset")
		}
		_, closeErr := cs.br.HandlePublish(cs.attached, pk.Publish)
		return closeErr // PUBACK (if any) was already enqueued by the broker

	case packet.TypePUBACK:
		return cs.br.HandlePuback(cs.attached, pk.PubAck.PacketID)

	case packet.TypeSUBSCRIBE:
		_, closeErr := cs.br.HandleSubscribe(cs.attached, pk.Subscribe)
		return closeErr // SUBACK (+ retained) already queued

	case packet.TypeDISCONNECT:
		cs.br.MarkCleanDisconnect(cs.attached)
		return broker.NewCloseError(broker.ReasonClientClosed, "client DISCONNECT")

	case packet.TypeCONNECT:
		return broker.NewCloseError(broker.ReasonStateConflict, "second CONNECT on one transport (MQTT-3.1.0-2)")

	case packet.TypePUBREC, packet.TypePUBREL, packet.TypePUBCOMP,
		packet.TypeUNSUBSCRIBE, packet.TypeUNSUBACK:
		cs.log.Reject("packet.unsupported", cs.id, "type", pk.Type.String())
		return broker.NewCloseError(broker.ReasonProtocolViolation,
			"%s is not supported by this subset", pk.Type)

	case packet.TypeCONNACK, packet.TypeSUBACK, packet.TypePINGRESP:
		return broker.NewCloseError(broker.ReasonProtocolViolation,
			"%s may only be sent by a server", pk.Type)

	default:
		return broker.NewCloseError(broker.ReasonProtocolViolation,
			"reserved packet type %d", byte(pk.Type))
	}
}

func (cs *connState) classifyRuntime(err error) *broker.CloseError {
	var ne net.Error
	switch {
	case errors.Is(err, io.EOF):
		// READY-state EOF without a DISCONNECT packet is an abnormal
		// disconnection: the Last Will MUST be published (MQTT-3.14.4).
		// A clean network close is only produced by an explicit DISCONNECT,
		// which is handled in dispatch().
		return broker.NewCloseError(broker.ReasonNetworkError, "transport closed without DISCONNECT")
	case errors.Is(err, io.ErrUnexpectedEOF):
		// Abrupt truncation: abnormal close (Will published).
		return broker.NewCloseError(broker.ReasonNetworkError, "truncated packet: %v", err)
	case errors.As(err, &ne) && ne.Timeout():
		return broker.NewCloseError(broker.ReasonKeepAliveTimeout, "no packet within keep-alive window")
	case errors.Is(err, packet.ErrTooLarge):
		return broker.NewCloseError(broker.ReasonResourceExhaustion, "packet size cap: %v", err)
	case errors.Is(err, packet.ErrMalformed), errors.Is(err, packet.ErrUnsupported):
		return broker.NewCloseError(broker.ReasonProtocolViolation, "%v", err)
	default:
		return broker.NewCloseError(broker.ReasonNetworkError, "read failure: %v", err)
	}
}

// armKeepaliveDeadline applies the 1.5x keep-alive deadline (MQTT-3.1.2-24).
// Keep Alive 0 disables the server-side timer.
func (cs *connState) armKeepaliveDeadline(keepAlive uint16) {
	if keepAlive == 0 {
		_ = cs.conn.SetReadDeadline(time.Time{})
		return
	}
	mult := cs.cfg.KeepAliveMultiplier
	if mult < 1.0 {
		mult = 1.5
	}
	d := time.Duration(float64(keepAlive)*mult*float64(time.Second)) + time.Second
	_ = cs.conn.SetReadDeadline(time.Now().Add(d))
}

// writerLoop serializes all post-CONNECT frames. It exits when the send
// channel is closed (broker-side teardown), the connection's CloseCh fires
// (take-over / slow subscriber) or the context ends.
func (cs *connState) writerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-cs.attached.CloseCh():
			return
		case frame, ok := <-cs.attached.SendCh():
			if !ok {
				return
			}
			_ = cs.conn.SetWriteDeadline(time.Now().Add(cs.cfg.WriteTimeout))
			if _, err := cs.conn.Write(frame); err != nil {
				// Reader classifies the resulting closed connection.
				cs.requestAsyncClose(broker.NewCloseError(broker.ReasonNetworkError, "write failure: %v", err))
				return
			}
			cs.log.Event("write", cs.id, "client_id", cs.attached.ClientIdentifier(), "bytes", len(frame))
		}
	}
}

// requestAsyncClose is the callback handed to the broker; it closes the
// transport so the reader unblocks. It may run from any goroutine and is
// idempotent.
func (cs *connState) requestAsyncClose(r *broker.CloseError) {
	select {
	case cs.closeReq <- r:
	default:
	}
	cs.closeOnce.Do(func() { _ = cs.conn.Close() })
}

func (cs *connState) writeRaw(frame []byte) error {
	_ = cs.conn.SetWriteDeadline(time.Now().Add(cs.cfg.WriteTimeout))
	_, err := cs.conn.Write(frame)
	return err
}

// finish performs the single, final teardown.
func (cs *connState) finish(r *broker.CloseError) {
	cs.finishOnce.Do(func() {
		if r == nil {
			r = broker.NewCloseError(broker.ReasonNetworkError, "unknown termination")
		}
		// Stop pending read/write immediately (idempotent).
		cs.closeOnce.Do(func() { _ = cs.conn.Close() })

		if cs.attached != nil {
			cs.br.End(cs.attached, r)
		}
		cs.log.Event("conn.finish", cs.id,
			"reason", r.Reason, "detail", r.Detail,
			"client_id", clientIDOf(cs.attached))
	})
}

func clientIDOf(c *broker.Conn) string {
	if c == nil {
		return ""
	}
	return c.ClientIdentifier()
}
