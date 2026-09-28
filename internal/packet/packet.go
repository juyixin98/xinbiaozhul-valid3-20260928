// Package packet implements MQTT 3.1.1 (OASIS standard) wire encoding for the
// subset supported by this broker:
//
//	inbound : CONNECT, SUBSCRIBE, PUBLISH (QoS 0/1), PUBACK, PINGREQ, DISCONNECT
//	outbound: CONNACK, SUBACK,   PUBLISH (QoS 0/1), PUBACK, PINGRESP
//
// The decoder is intentionally strict: every check that the spec phrases as
// "MUST" and that can be decided from a single packet (fixed-header reserved
// bits, remaining-length bounds and minimal encoding, packet identifier rules,
// string bounds and UTF-8 validity) fails with a wrapped sentinel error:
//
//   - ErrMalformed   – protocol violation; the receiver MUST close the
//     transport (MQTT-4.8.0-3). For CONNECT-time structural violations the
//     connection is closed without CONNACK.
//   - ErrUnsupported – a well-formed packet type this subset does not
//     implement (e.g. UNSUBSCRIBE, QoS 2 flow packets).
//   - ErrTooLarge    – remaining length exceeds the negotiated/resource cap.
//
// Policy rejections that keep a defined MQTT response (CONNACK codes,
// SUBACK 0x80, unsupported Will QoS 2) are NOT decided here; the broker layer
// maps them. CONNECT parse-time policy results use ConnectRejectError.
package packet

import "errors"

// Sentinel errors. Callers should classify with errors.Is.
var (
	ErrMalformed   = errors.New("mqtt: malformed packet")
	ErrUnsupported = errors.New("mqtt: unsupported packet type")
	ErrTooLarge    = errors.New("mqtt: packet exceeds size limit")
)

// Type is the 4-bit MQTT control packet type.
type Type byte

const (
	TypeRESERVED1   Type = 0
	TypeCONNECT     Type = 1
	TypeCONNACK     Type = 2
	TypePUBLISH     Type = 3
	TypePUBACK      Type = 4
	TypePUBREC      Type = 5
	TypePUBREL      Type = 6
	TypePUBCOMP     Type = 7
	TypeSUBSCRIBE   Type = 8
	TypeSUBACK      Type = 9
	TypeUNSUBSCRIBE Type = 10
	TypeUNSUBACK    Type = 11
	TypePINGREQ     Type = 12
	TypePINGRESP    Type = 13
	TypeDISCONNECT  Type = 14
	TypeRESERVED15  Type = 15
)

// String returns the spec name of a packet type.
func (t Type) String() string {
	switch t {
	case TypeCONNECT:
		return "CONNECT"
	case TypeCONNACK:
		return "CONNACK"
	case TypePUBLISH:
		return "PUBLISH"
	case TypePUBACK:
		return "PUBACK"
	case TypePUBREC:
		return "PUBREC"
	case TypePUBREL:
		return "PUBREL"
	case TypePUBCOMP:
		return "PUBCOMP"
	case TypeSUBSCRIBE:
		return "SUBSCRIBE"
	case TypeSUBACK:
		return "SUBACK"
	case TypeUNSUBSCRIBE:
		return "UNSUBSCRIBE"
	case TypeUNSUBACK:
		return "UNSUBACK"
	case TypePINGREQ:
		return "PINGREQ"
	case TypePINGRESP:
		return "PINGRESP"
	case TypeDISCONNECT:
		return "DISCONNECT"
	default:
		return "RESERVED"
	}
}

// CONNACK return codes (MQTT 3.1.1 §3.2.2.3).
const (
	ConnAccepted              byte = 0x00
	ConnBadProtocolVersion    byte = 0x01
	ConnIdentifierRejected    byte = 0x02
	ConnServerUnavailable     byte = 0x03
	ConnBadUsernameOrPassword byte = 0x04
	ConnNotAuthorized         byte = 0x05
)

// SUBACK per-topic failure code (MQTT 3.1.1 §3.8.3.1).
const SubAckFailure byte = 0x80

// Connect is a parsed CONNECT packet (MQTT 3.1.1 §3.1).
type Connect struct {
	ClientID      string
	CleanSession  bool
	KeepAlive     uint16
	Username      string
	Password      []byte
	HasUsername   bool
	HasPassword   bool
	WillTopic     string
	WillMessage   []byte
	WillQoS       byte
	WillRetain    bool
	HasWill       bool
	ProtocolLevel byte
}

// Publish is a parsed PUBLISH packet (MQTT 3.1.1 §3.3).
type Publish struct {
	Topic    string
	Payload  []byte
	PacketID uint16 // 0 for QoS 0
	QoS      byte
	Retain   bool
	Dup      bool
}

// PubAck is a parsed PUBACK packet (MQTT 3.1.1 §3.4).
type PubAck struct {
	PacketID uint16
}

// Subscription is one topic filter / maximum QoS pair inside SUBSCRIBE.
type Subscription struct {
	Filter string
	QoS    byte
}

// Subscribe is a parsed SUBSCRIBE packet (MQTT 3.1.1 §3.8).
type Subscribe struct {
	PacketID      uint16
	Subscriptions []Subscription
}

// Packet is a decoded inbound packet; exactly one optional payload field is
// populated according to Type.
type Packet struct {
	Type  Type
	Flags byte

	Connect    *Connect
	Publish    *Publish
	PubAck     *PubAck
	Subscribe  *Subscribe
	PingReq    bool
	Disconnect bool
}
