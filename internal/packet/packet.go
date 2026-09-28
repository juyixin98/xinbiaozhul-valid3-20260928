// Package packet implements MQTT 3.1.1 (protocol level 4) wire encoding for the
// subset supported by this broker:
//
//	CONNECT, CONNACK, SUBSCRIBE, SUBACK,
//	PUBLISH (QoS 0/1), PUBACK,
//	PINGREQ, PINGRESP, DISCONNECT.
//
// This package is deliberately transport-agnostic: it converts between byte
// slices and typed structs and performs ONLY protocol-level validation
// (wire well-formedness). Broker policy (session state, authorization,
// resource limits) lives in the broker package.
package packet

// Packet type codes, MQTT 3.1.1 section 2.2.1.
const (
	TypeConnect    byte = 1
	TypeConnack    byte = 2
	TypePublish    byte = 3
	TypePuback     byte = 4
	TypeSubscribe  byte = 8
	TypeSuback     byte = 9
	TypePingreq    byte = 12
	TypePingresp   byte = 13
	TypeDisconnect byte = 14
)

// CONNACK return codes, MQTT 3.1.1 section 3.2.2.3.
const (
	ConnackAccepted          byte = 0
	ConnackBadProto          byte = 1
	ConnackIDRejected        byte = 2
	ConnackServerUnavailable byte = 3
	ConnackNotAuthorized     byte = 5
)

// SUBACK return code for a failed subscription, MQTT 3.1.1 section 3.8.3.
const SubackFailure byte = 0x80

// Protocol level values required/supported.
const (
	ProtocolName       = "MQTT"
	ProtocolLevel byte = 4
)

// FixedHeaderFlags returns the 4-bit fixed-header flag value (DUP/QoS/RETAIN
// for PUBLISH) that each non-PUBLISH packet type MUST carry. ok is false for
// unknown packet types. MQTT 3.1.1 table 2.2.
func FixedHeaderFlags(t byte) (flags byte, ok bool) {
	switch t {
	case TypeConnect, TypeConnack, TypePuback,
		TypeSuback, TypePingreq, TypePingresp, TypeDisconnect:
		return 0x0, true
	case TypeSubscribe:
		return 0x2, true
	case TypePublish:
		return 0x0, true // caller must inspect actual flags
	default:
		return 0, false
	}
}
