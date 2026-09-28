package packet

import (
	"errors"
	"fmt"
)

// Category classifies a failure so the broker and tests can distinguish
// malformed protocol input, unsupported protocol features, resource limits,
// state conflicts and internal/runtime failures.
type Category string

const (
	CatInvalid     Category = "invalid_input"  // bytes violate MQTT 3.1.1
	CatUnsupported Category = "unsupported"    // well-formed feature outside this subset
	CatLimit       Category = "resource_limit" // configured bound exceeded
	CatState       Category = "state_conflict" // packet not allowed in current state
	CatInternal    Category = "internal"       // runtime/database failure
)

// Error is the explicit error contract of the packet/broker layers: every
// failure carries a stable category and a reason string.
type Error struct {
	Cat    Category
	Reason string
}

func (e *Error) Error() string { return string(e.Cat) + ": " + e.Reason }

// New builds an *Error.
func New(cat Category, format string, args ...any) *Error {
	return &Error{Cat: cat, Reason: fmt.Sprintf(format, args...)}
}

// AsError extracts an *Error from err, or returns nil.
func AsError(err error) *Error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}

// Sentinel-worthy helpers.
var (
	ErrTruncated = New(CatInvalid, "packet truncated")
)
