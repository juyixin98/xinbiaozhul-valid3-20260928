package httpx

import (
	"errors"
	"io"
)

// Parser drives one connection stream through successive HTTP/1.1
// requests. It is intentionally frame-oriented: the connection state
// machine in conn.go decides when to call ReadHead/ReadBody and what
// to do with errors; Parser only parses.
type Parser struct {
	s      *stream
	limits Limits
}

// NewParser builds a parser over r. initialBuffer is the fill window
// hint; the per-fill ceiling is derived from limits.
func NewParser(r io.Reader, limits Limits, initialBuffer int) *Parser {
	grow := limits.MaxHeaderBytes
	if int64(grow) < limits.MaxBodyBytes {
		if limits.MaxBodyBytes < 4*1024*1024 {
			grow = int(limits.MaxBodyBytes)
		} else {
			grow = 4 * 1024 * 1024
		}
	}
	return &Parser{
		s:      newStream(r, initialBuffer, grow+1024),
		limits: limits,
	}
}

// Buffered reports how many already-framed bytes (i.e. the start of
// the next pipelined request) are held.
func (p *Parser) Buffered() int { return p.s.Buffered() }

// Offset reports the absolute stream offset of the next unread byte.
func (p *Parser) Offset() int64 { return p.s.Offset() }

// ReadBody consumes the body declared by req, enforcing the configured
// limits and validating framing completely. On success every body byte
// has been consumed and req.Body/req.BodyEndOffset are populated; the
// stream cursor sits exactly at the first byte of the next message.
func (p *Parser) ReadBody(req *Request) error {
	switch req.Frame {
	case FrameNone:
		req.Body = nil
		req.BodyEndOffset = p.s.Offset()
		return nil
	case FrameFixed:
		return p.readFixedBody(req)
	case FrameChunked:
		return p.readChunkedBody(req)
	default:
		return perr(KindStateConflict, PhaseFraming, p.s.Offset(), 0,
			"internal: unknown framing", "unknown FrameMode", nil)
	}
}

func (p *Parser) readFixedBody(req *Request) error {
	if req.ContentLength < 0 {
		return perr(KindInvalidSyntax, PhaseFraming, p.s.Offset(), 400,
			"RFC9112 §6.3: Content-Length",
			"negative Content-Length", nil)
	}
	if req.ContentLength > p.limits.MaxBodyBytes {
		return perr(KindResourceLimit, PhaseReadFixedBody, p.s.Offset(), 413,
			"configured MaxBodyBytes",
			"declared Content-Length exceeds body limit", nil)
	}
	body := make([]byte, 0, int(req.ContentLength))
	body, err := p.s.ReadAppend(body, req.ContentLength)
	if err != nil {
		return p.wrapBodyRead(err, PhaseReadFixedBody)
	}
	req.Body = body
	req.BodyEndOffset = p.s.Offset()
	return nil
}

func (p *Parser) wrapBodyRead(err error, phase Phase) error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	switch {
	case errors.Is(err, errIncomplete), errors.Is(err, io.ErrUnexpectedEOF):
		return perr(KindInvalidSyntax, phase, p.s.Offset(), 0,
			"RFC9112: message body is delimited by the connection",
			"connection ended before the full body arrived", err)
	case errors.Is(err, ErrCleanEOF):
		return perr(KindInvalidSyntax, phase, p.s.Offset(), 0,
			"RFC9112: complete message required",
			"clean EOF inside body", err)
	case errors.Is(err, io.ErrShortBuffer):
		return perr(KindResourceLimit, phase, p.s.Offset(), 413,
			"configured buffer ceiling",
			"body exceeded buffered read ceiling", err)
	default:
		return perr(KindIOFailure, phase, p.s.Offset(), 0,
			"transport read failure", err.Error(), err)
	}
}

// Chunked body grammar (RFC 9112):
//
//	chunked-body   = *chunk
//	                 last-chunk
//	                 trailer-part
//	                 CRLF
//	chunk          = chunk-size [ chunk-ext ] CRLF chunk-data CRLF
//	chunk-size     = 1*HEXDIG
//	last-chunk     = 1*("0") [ chunk-ext ] CRLF
//	chunk-ext      = *( ";" chunk-ext-name [ "=" chunk-ext-val ] )
//
// chunk-data is exactly chunk-size octets — it is never scanned for
// CRLF, so bodies that contain CRLF cannot desynchronize framing.
func (p *Parser) readChunkedBody(req *Request) error {
	var body []byte
	total := int64(0)

	for {
		lineStart := p.s.Offset()
		line, _, next, err := p.s.ReadLine(p.limits.MaxChunkLineBytes)
		if err != nil {
			return p.wrapChunkLineRead(err, lineStart)
		}
		size, isLast, pe := parseChunkSizeLine(line)
		if pe != nil {
			pe.Offset = lineStart
			return pe
		}
		if isLast {
			// trailer-part: possibly zero field lines, then CRLF.
			if err := p.readTrailers(next); err != nil {
				return err
			}
			req.Body = body
			req.BodyEndOffset = p.s.Offset()
			return nil
		}
		// chunk-data is exactly `size` raw octets.
		if total+size > p.limits.MaxBodyBytes {
			return perr(KindResourceLimit, PhaseChunkData, p.s.Offset(), 413,
				"configured MaxBodyBytes",
				"decoded chunked payload would exceed body limit", nil)
		}
		body, err = p.s.ReadAppend(body, size)
		if err != nil {
			return p.wrapBodyRead(err, PhaseChunkData)
		}
		total += size
		// Mandatory CRLF directly after chunk-data.
		if err := p.readCRLF(PhaseChunkEnd); err != nil {
			return err
		}
	}
}

func (p *Parser) wrapChunkLineRead(err error, off int64) error {
	var bare *bareLFError
	if errors.As(err, &bare) {
		return perr(KindInvalidSyntax, PhaseChunkSizeLine, bare.Offset, 400,
			"RFC9112 §2.2: line termination is CRLF",
			"bare LF in chunk framing", err)
	}
	switch {
	case errors.As(err, new(*Error)):
		return err
	case errors.Is(err, ErrCleanEOF):
		return perr(KindInvalidSyntax, PhaseChunkSizeLine, off, 0,
			"RFC9112: chunked body must terminate with zero-size chunk",
			"clean EOF before last-chunk", err)
	case errors.Is(err, errIncomplete):
		return perr(KindInvalidSyntax, PhaseChunkSizeLine, off, 0,
			"RFC9112: chunk-size line CRLF",
			"truncated chunk-size line", err)
	case errors.Is(err, errLineTooLong):
		return perr(KindResourceLimit, PhaseChunkSizeLine, off, 400,
			"configured MaxChunkLineBytes",
			"chunk-size line (with extensions) too long", err)
	default:
		return p.wrapBodyRead(err, PhaseChunkSizeLine)
	}
}

// readCRLF consumes the exactly-two CRLF bytes following chunk data.
// Using an exact 2-byte check rejects peers that send CR or LF alone
// and rejects embedded bytes that a line-based scan would misframe.
func (p *Parser) readCRLF(phase Phase) error {
	b, err := p.s.ensure(2)
	if err != nil {
		return p.wrapBodyRead(err, phase)
	}
	if b[0] != '\r' || b[1] != '\n' {
		return perr(KindInvalidSyntax, phase, p.s.Offset(), 400,
			"RFC9112: chunk-data is followed by CRLF",
			"expected CRLF after chunk data", nil)
	}
	p.s.advance(2)
	return nil
}

func (p *Parser) readTrailers(headBudgetStart int64) error {
	for {
		lineStart := p.s.Offset()
		line, _, next, err := p.s.ReadLine(p.limits.MaxHeaderBytes)
		if err != nil {
			return p.wrapHeadRead(err, PhaseReadTrailer, lineStart)
		}
		if next-headBudgetStart > int64(p.limits.MaxHeaderBytes) {
			return perr(KindResourceLimit, PhaseReadTrailer, next, 400,
				"configured MaxHeaderBytes",
				"trailer section too large", nil)
		}
		if len(line) == 0 {
			return nil // terminating CRLF of trailer-part
		}
		name, value, pe := parseFieldLine(line)
		if pe != nil {
			pe.Offset = lineStart
			pe.Phase = PhaseReadTrailer
			return pe
		}
		// Trailers are validated but not exposed: a trailer that
		// tries to redefine framing is rejected outright.
		switch {
		case equalFoldStr(name, "Content-Length"), equalFoldStr(name, "Transfer-Encoding"):
			return perr(KindProtocolConflict, PhaseReadTrailer, lineStart, 400,
				"RFC9112: framing fields forbidden in trailers",
				"framing header in trailer", nil)
		case equalFoldStr(name, "Host"):
			return perr(KindProtocolConflict, PhaseReadTrailer, lineStart, 400,
				"RFC9110: Host is a routing field, forbidden in trailers",
				"Host in trailer", nil)
		}
		_ = value
	}
}

// parseChunkSizeLine parses "1*HEXDIG [ chunk-ext ]" (the trailing
// CRLF has already been removed). Hex is parsed manually so a value
// that overflows int64 is rejected rather than wrapped.
func parseChunkSizeLine(line []byte) (size int64, last bool, e *Error) {
	semi := -1
	for i, c := range line {
		if c == ';' {
			semi = i
			break
		}
		if c == ' ' || c == '\t' {
			// Tolerate no whitespace before extensions; BWS before
			// ';' is allowed by some clients, accept only if it is
			// immediately followed by ';'.
			for j := i; j < len(line); j++ {
				if line[j] == ';' {
					semi = j
					break
				}
				if line[j] != ' ' && line[j] != '\t' {
					return 0, false, perr(KindInvalidSyntax, PhaseChunkSize, 0, 400,
						"RFC9112: chunk-size [ chunk-ext ]",
						"stray whitespace in chunk-size line", nil)
				}
			}
			break
		}
	}
	hex := line
	if semi >= 0 {
		hex = line[:semi]
		if err := validateChunkExt(line[semi+1:]); err != nil {
			return 0, false, perr(KindInvalidSyntax, PhaseChunkSize, 0, 400,
				"RFC9112: chunk-ext syntax", err.Error(), err)
		}
	}
	if len(hex) == 0 {
		return 0, false, perr(KindInvalidSyntax, PhaseChunkSize, 0, 400,
			"RFC9112: chunk-size = 1*HEXDIG",
			"missing chunk size", nil)
	}
	var v int64
	for _, c := range hex {
		var d int64
		switch {
		case isDigit(c):
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int64(c-'A') + 10
		default:
			return 0, false, perr(KindInvalidSyntax, PhaseChunkSize, 0, 400,
				"RFC9112: chunk-size = 1*HEXDIG",
				"non-hex digit in chunk size", nil)
		}
		if v > (1<<63-1-d)/16 {
			return 0, false, perr(KindResourceLimit, PhaseChunkSize, 0, 400,
				"int64 chunk size",
				"chunk size overflow", nil)
		}
		v = v*16 + d
	}
	if v == 0 {
		return 0, true, nil
	}
	return v, false, nil
}

// validateChunkExt performs a grammar check on "chunk-ext-name [ '='
// chunk-ext-val ] *( ';' ... )" so illegal extensions are rejected
// (their content is otherwise ignored).
func validateChunkExt(ext []byte) error {
	for len(ext) > 0 {
		// name
		i := 0
		for i < len(ext) && ext[i] != '=' && ext[i] != ';' {
			if !isTchar(ext[i]) {
				return errors.New("illegal chunk-ext-name byte")
			}
			i++
		}
		if i == 0 {
			return errors.New("empty chunk-ext-name")
		}
		ext = ext[i:]
		if len(ext) > 0 && ext[0] == '=' {
			ext = ext[1:]
			if len(ext) > 0 && ext[0] == '"' {
				// quoted-string: walk to closing quote, rejecting
				// bare CR/LF; backslash escapes are checked.
				j := 1
				for j < len(ext) {
					c := ext[j]
					if c == '\\' && j+1 < len(ext) {
						j += 2
						continue
					}
					if c == '"' {
						break
					}
					if c == '\r' || c == '\n' {
						return errors.New("CR/LF in quoted chunk-ext-val")
					}
					j++
				}
				if j >= len(ext) || ext[j] != '"' {
					return errors.New("unterminated quoted chunk-ext-val")
				}
				ext = ext[j+1:]
			} else {
				k := 0
				for k < len(ext) && ext[k] != ';' {
					if !isTchar(ext[k]) {
						return errors.New("illegal chunk-ext-val byte")
					}
					k++
				}
				ext = ext[k:]
			}
		}
		if len(ext) == 0 {
			return nil
		}
		if ext[0] != ';' {
			return errors.New("malformed chunk-ext list")
		}
		ext = ext[1:]
	}
	return nil
}
