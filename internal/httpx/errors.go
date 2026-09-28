// Package httpx implements a strict, self-contained subset of HTTP/1.1
// request parsing: request line + headers + fixed-length and chunked
// bodies, driven by an explicit per-connection state machine.
//
// It deliberately does not use net/http: the framing rules (RFC 9112)
// are implemented from scratch so that ambiguity and request-smuggling
// shapes are rejected deterministically.
package httpx

import (
	"errors"
	"fmt"
)

// Kind classifies failures so that callers (state machine, business
// layer, tests, logs) can distinguish illegal input, protocol state
// conflicts, resource exhaustion and runtime/IO failures without
// parsing error strings.
type Kind string

const (
	// KindInvalidSyntax: bytes do not match the HTTP/1.1 grammar
	// (bad token, bare LF, bad chunk, truncated message, ...).
	KindInvalidSyntax Kind = "invalid_syntax"

	// KindProtocolConflict: syntactically present but mutually
	// contradictory framing (TE vs CL, differing CL values, ...).
	KindProtocolConflict Kind = "protocol_conflict"

	// KindStateConflict: a message is invalid because of the
	// connection state it arrived in (pipelined body overflow,
	// trailer after consumed body, ...).
	KindStateConflict Kind = "state_conflict"

	// KindResourceLimit: a configured hard limit was exceeded
	// (head section, body, chunk extension line, ...).
	KindResourceLimit Kind = "resource_limit"

	// KindUnsupported: grammar is valid but the subset does not
	// implement the feature (other HTTP versions, transfer codings,
	// absolute-form targets, ...).
	KindUnsupported Kind = "unsupported"

	// KindIOFailure: transport level failure unrelated to protocol
	// grammar (reset, deadline, broken pipe).
	KindIOFailure Kind = "io_failure"
)

// Phase names the parsing step a failure occurred in. Together with
// the byte offset it lets logs pinpoint exactly where a request died.
type Phase string

const (
	PhaseReadRequestLine Phase = "read_request_line"
	PhaseRequestLine     Phase = "parse_request_line"
	PhaseReadHeaders     Phase = "read_headers"
	PhaseHeaderField     Phase = "parse_header_field"
	PhaseFraming         Phase = "resolve_framing"
	PhaseReadFixedBody   Phase = "read_fixed_body"
	PhaseChunkSizeLine   Phase = "read_chunk_size_line"
	PhaseChunkSize       Phase = "parse_chunk_size"
	PhaseChunkData       Phase = "read_chunk_data"
	PhaseChunkEnd        Phase = "read_chunk_end_crlf"
	PhaseReadTrailer     Phase = "read_chunk_trailer"
)

// Error is the single error contract of the parser. Every non-nil
// error returned by this package is either *Error or wraps one.
type Error struct {
	Kind    Kind
	Phase   Phase
	Offset  int64  // absolute byte offset on the connection stream
	Code    int    // suggested HTTP status code; 0 = do not respond, close
	Basis   string // grammar rule / RFC clause the decision is based on
	Message string
	Cause   error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("httpx: %s at phase=%s offset=%d", e.Kind, e.Phase, e.Offset)
	if e.Code != 0 {
		s += fmt.Sprintf(" code=%d", e.Code)
	}
	if e.Basis != "" {
		s += " basis=" + e.Basis
	}
	s += ": " + e.Message
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

func (e *Error) Unwrap() error { return e.Cause }

// AsError extracts the *Error from an error chain.
func AsError(err error) (*Error, bool) {
	var pe *Error
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

func perr(kind Kind, phase Phase, offset int64, code int, basis, msg string, cause error) *Error {
	return &Error{
		Kind:    kind,
		Phase:   phase,
		Offset:  offset,
		Code:    code,
		Basis:   basis,
		Message: msg,
		Cause:   cause,
	}
}

// Sentinels.

// ErrCleanEOF means the peer closed at a request boundary with no
// partial bytes: it is normal connection teardown, not a failure.
var ErrCleanEOF = errors.New("httpx: clean eof at request boundary")

// errIncomplete is an internal sentinel produced by the reader when
// the stream ends in the middle of a token; parsers wrap it into an
// Error carrying phase and offset.
var errIncomplete = errors.New("truncated in mid-message")

// errBareLF marks a line terminated by LF not preceded by CR.
var errBareLF = errors.New("line feed not preceded by CR (bare LF)")

// bareLFError carries the absolute stream offset of the offending LF
// so logs can point at the exact byte. It wraps errBareLF.
type bareLFError struct {
	Offset int64
}

func (e *bareLFError) Error() string { return errBareLF.Error() }
func (e *bareLFError) Unwrap() error { return errBareLF }

// errLineTooLong marks a line that crossed the configured ceiling
// before its terminating CRLF arrived.
var errLineTooLong = errors.New("line exceeded configured length before CRLF")
