package packet

import (
	"encoding/binary"
)

// enc builds an outbound packet. All encoders allocate a complete frame;
// sizes here are bounded by broker resource limits, not streamed.
type enc struct {
	first byte
	body  []byte
}

func putU16(b []byte, v uint16) { binary.BigEndian.PutUint16(b, v) }

func appendU16String(dst []byte, s string) []byte {
	dst = append(dst, 0, 0)
	putU16(dst[len(dst)-2:], uint16(len(s)))
	return append(dst, s...)
}

// encodeRemainingLength appends the (minimal) variable-length encoding of v
// (MQTT 3.1.1 §2.2.3).
func encodeRemainingLength(v int) []byte {
	var out []byte
	for {
		digit := byte(v % 128)
		v /= 128
		if v > 0 {
			digit |= 0x80
		}
		out = append(out, digit)
		if v == 0 {
			return out
		}
	}
}

func frame(first byte, body []byte) []byte {
	out := make([]byte, 0, 1+4+len(body))
	out = append(out, first)
	out = append(out, encodeRemainingLength(len(body))...)
	out = append(out, body...)
	return out
}

// EncodeConnack encodes a CONNACK packet. sessionPresent may be true only
// when code is 0x00 (MQTT-3.2.2-4).
func EncodeConnack(code byte, sessionPresent bool) []byte {
	sa := byte(0)
	if sessionPresent && code == ConnAccepted {
		sa = 1
	}
	return frame((byte(TypeCONNACK)<<4)|0x00, []byte{sa, code})
}

// EncodePingresp encodes a PINGRESP packet.
func EncodePingresp() []byte {
	return frame((byte(TypePINGRESP)<<4)|0x00, nil)
}

// EncodePuback encodes a PUBACK for packet identifier id (id MUST be > 0).
func EncodePuback(id uint16) []byte {
	body := make([]byte, 2)
	putU16(body, id)
	return frame((byte(TypePUBACK)<<4)|0x00, body)
}

// EncodeSuback encodes a SUBACK. returnCodes entries are 0x00, 0x01 or 0x80.
func EncodeSuback(packetID uint16, returnCodes []byte) []byte {
	body := make([]byte, 0, 2+len(returnCodes))
	id := make([]byte, 2)
	putU16(id, packetID)
	body = append(body, id...)
	body = append(body, returnCodes...)
	return frame((byte(TypeSUBACK)<<4)|0x00, body)
}

// EncodePublish encodes a PUBLISH packet.
// dup, qos, retain map to DUP, QoS, RETAIN flag bits. id is ignored for
// QoS 0 and MUST be non-zero for QoS 1.
func EncodePublish(topic string, payload []byte, id uint16, qos byte, dup, retain bool) []byte {
	body := make([]byte, 0, 2+len(topic)+2+len(payload))
	body = appendU16String(body, topic)
	if qos > 0 {
		idb := make([]byte, 2)
		putU16(idb, id)
		body = append(body, idb...)
	}
	body = append(body, payload...)

	var flags byte
	if dup {
		flags |= 0x08
	}
	flags |= (qos & 0x03) << 1
	if retain {
		flags |= 0x01
	}
	return frame((byte(TypePUBLISH)<<4)|flags, body)
}

// EncodeConnect builds a CONNECT packet (test client helper).
func EncodeConnect(c *Connect) []byte {
	var body []byte
	body = appendU16String(body, "MQTT")
	body = append(body, 4) // level 4

	var flags byte
	if c.CleanSession {
		flags |= 0x02
	}
	if c.HasWill {
		flags |= 0x04
		flags |= (c.WillQoS & 0x03) << 3
		if c.WillRetain {
			flags |= 0x20
		}
	}
	if c.HasPassword {
		flags |= 0x40
	}
	if c.HasUsername {
		flags |= 0x80
	}
	body = append(body, flags)
	ka := make([]byte, 2)
	putU16(ka, c.KeepAlive)
	body = append(body, ka...)
	body = appendU16String(body, c.ClientID)
	if c.HasWill {
		body = appendU16String(body, c.WillTopic)
		l := make([]byte, 2)
		putU16(l, uint16(len(c.WillMessage)))
		body = append(body, l...)
		body = append(body, c.WillMessage...)
	}
	if c.HasUsername {
		body = appendU16String(body, c.Username)
	}
	if c.HasPassword {
		l := make([]byte, 2)
		putU16(l, uint16(len(c.Password)))
		body = append(body, l...)
		body = append(body, c.Password...)
	}
	return frame((byte(TypeCONNECT)<<4)|0x00, body)
}

// EncodeSubscribe builds a SUBSCRIBE packet (test client helper).
func EncodeSubscribe(id uint16, subs []Subscription) []byte {
	var body []byte
	idb := make([]byte, 2)
	putU16(idb, id)
	body = append(body, idb...)
	for _, s := range subs {
		body = appendU16String(body, s.Filter)
		body = append(body, s.QoS&0x03)
	}
	// Reserved bits 0010 are fixed for SUBSCRIBE.
	return frame((byte(TypeSUBSCRIBE)<<4)|0x02, body)
}

// EncodePingreq builds a PINGREQ packet.
func EncodePingreq() []byte { return frame((byte(TypePINGREQ)<<4)|0x00, nil) }

// EncodeDisconnect builds a DISCONNECT packet.
func EncodeDisconnect() []byte { return frame((byte(TypeDISCONNECT)<<4)|0x00, nil) }
