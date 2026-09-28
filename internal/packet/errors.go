package packet

import "fmt"

// ConnectRejectError carries a CONNACK return code for a CONNECT that was
// structurally readable but cannot be accepted. The broker MUST send the
// CONNACK and then close the network connection (MQTT-3.1.4-5 / §3.2).
type ConnectRejectError struct {
	Code   byte
	Reason string
}

func (e *ConnectRejectError) Error() string {
	return fmt.Sprintf("mqtt: CONNECT rejected with code 0x%02x: %s", e.Code, e.Reason)
}
