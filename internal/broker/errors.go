package broker

import "fmt"

// CloseReason is the typed reason a connection terminated. Tests and logs use
// it to distinguish the four failure classes required by the task:
//
//   - invalid input (protocol violations)
//   - state conflict
//   - resource exhaustion
//   - operational failure
//
// Normal closes are separate.
type CloseReason string

const (
	ReasonClientClosed       CloseReason = "client_disconnect"   // clean DISCONNECT / EOF
	ReasonProtocolViolation  CloseReason = "protocol_violation"  // invalid input
	ReasonRejectedConnect    CloseReason = "connect_rejected"    // CONNACK sent, then closed
	ReasonStateConflict      CloseReason = "state_conflict"      // e.g. packet before CONNECT
	ReasonResourceExhaustion CloseReason = "resource_exhaustion" // limits exceeded
	ReasonTakenOver          CloseReason = "taken_over"          // same ClientID reconnected
	ReasonKeepAliveTimeout   CloseReason = "keepalive_timeout"   // client silent too long
	ReasonNetworkError       CloseReason = "network_error"       // operational I/O failure
	ReasonServerShutdown     CloseReason = "server_shutdown"     // graceful broker stop
)

// CloseError carries the reason and a human explanation (with the spec rule
// where applicable).
type CloseError struct {
	Reason CloseReason
	Detail string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("connection closed: %s: %s", e.Reason, e.Detail)
}

func newClose(r CloseReason, format string, args ...any) *CloseError {
	return &CloseError{Reason: r, Detail: fmt.Sprintf(format, args...)}
}

// NewCloseError is the exported constructor used by the server layer.
func NewCloseError(r CloseReason, format string, args ...any) *CloseError {
	return newClose(r, format, args...)
}
