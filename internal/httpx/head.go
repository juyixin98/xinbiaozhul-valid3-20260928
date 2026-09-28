package httpx

import (
	"errors"
	"strconv"
)

// ReadHead parses exactly one request head (request line + header
// section + terminating empty line) from the stream. It consumes
// precisely the head bytes; body framing is left for ReadBody.
//
// Contract:
//   - A clean peer close with zero pending bytes returns ErrCleanEOF.
//   - Every other failure is *Error carrying phase, absolute offset
//     and a suggested status code; the caller MUST close.
//   - On success the request describes its framing (Frame/CL) but has
//     no body yet.
func (p *Parser) ReadHead() (*Request, error) {
	s := p.s
	headStart := s.Offset()

	line, _, lineEnd, err := s.ReadLine(p.limits.MaxHeaderBytes)
	if err != nil {
		return nil, p.wrapHeadRead(err, PhaseReadRequestLine, headStart)
	}
	req := &Request{HeadOffset: headStart}
	headBytes := lineEnd - headStart

	if err := p.parseRequestLine(line, req); err != nil {
		return nil, err // already *Error with offset/phase
	}

	// Header fields.
	var raw [][2]string
	for {
		lineStart := s.Offset()
		line, _, next, rerr := s.ReadLine(p.limits.MaxHeaderBytes)
		if rerr != nil {
			// A clean EOF between header lines is a truncated head
			// section (the terminating empty line never arrived),
			// not an idle keep-alive close: only the very first read
			// of a connection may legitimately see clean EOF.
			if errors.Is(rerr, ErrCleanEOF) {
				return nil, perr(KindInvalidSyntax, PhaseReadHeaders, lineStart, 0,
					"RFC9112: head section ends with an empty line (CRLF CRLF)",
					"connection ended before the head section terminator", rerr)
			}
			return nil, p.wrapHeadRead(rerr, PhaseReadHeaders, lineStart)
		}
		headBytes += next - lineStart
		if headBytes > int64(p.limits.MaxHeaderBytes) {
			return nil, perr(KindResourceLimit, PhaseReadHeaders, next, 431,
				"RFC9112: head section size limit",
				"head section exceeded MaxHeaderBytes", nil)
		}
		if len(line) == 0 {
			break // final empty line of the head
		}
		name, value, pe := parseFieldLine(line)
		if pe != nil {
			pe.Offset = lineStart
			// A line that swallowed an illegal bare newline (the
			// next CRLF was used as the terminator) is reported at
			// the line-reading phase: the failure is an illegal line
			// terminator, not a field grammar nuance.
			if containsBareNewline(line) {
				pe.Phase = PhaseReadHeaders
				pe.Basis = "RFC9112 §2.2: line termination is CRLF"
				pe.Message = "illegal bare LF in header section"
			}
			return nil, pe
		}
		raw = append(raw, [2]string{name, value})
	}

	for _, f := range raw {
		req.Header.add(f[0], f[1])
	}

	// Host requirements.
	if req.Version == "HTTP/1.1" {
		if hv := req.Header.Values("Host"); len(hv) == 0 {
			return nil, perr(KindInvalidSyntax, PhaseHeaderField, lineEnd, 400,
				"RFC9110: HTTP/1.1 requests must carry exactly one Host",
				"missing Host header field", nil)
		} else if len(hv) > 1 {
			return nil, perr(KindInvalidSyntax, PhaseHeaderField, lineEnd, 400,
				"RFC9110: duplicate Host field",
				"multiple Host header fields", nil)
		}
	}

	frame, cl, ferr := resolveFraming(&req.Header, lineEnd)
	if ferr != nil {
		return nil, ferr
	}
	req.Frame = frame
	req.ContentLength = cl
	return req, nil
}

// wrapHeadRead converts reader sentinels/IO errors into *Error.
func (p *Parser) wrapHeadRead(err error, phase Phase, off int64) error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	var bare *bareLFError
	if errors.As(err, &bare) {
		return perr(KindInvalidSyntax, phase, bare.Offset, 400,
			"RFC9112 §2.2: line termination is CRLF",
			"bare LF is not a legal line terminator", err)
	}
	switch {
	case errors.Is(err, ErrCleanEOF):
		return ErrCleanEOF
	case errors.Is(err, errIncomplete):
		return perr(KindInvalidSyntax, phase, off, 0,
			"RFC9112: CRLF-terminated head",
			"connection ended in the middle of the head section", err)
	case errors.Is(err, errLineTooLong):
		return perr(KindResourceLimit, phase, off, 431,
			"RFC9112: head line length limit",
			"header line exceeded configured limit before CRLF", err)
	default:
		return perr(KindIOFailure, phase, off, 0,
			"transport read failure", err.Error(), err)
	}
}

// parseRequestLine enforces:
//
//	method SP request-target SP HTTP-version CRLF
//
// with exactly two SP separators (no HTAB, no extra whitespace).
func (p *Parser) parseRequestLine(line []byte, req *Request) error {
	off := req.HeadOffset
	if len(line) == 0 {
		return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
			"RFC9112: request line starts with CRLF",
			"empty request line", nil)
	}
	first := -1
	second := -1
	for i, c := range line {
		if c == ' ' {
			if first == -1 {
				first = i
			} else {
				second = i
				break
			}
		} else if c == '\t' {
			return perr(KindInvalidSyntax, PhaseRequestLine, off+int64(i), 400,
				"RFC9112 §2.1: request-line separators are single SP",
				"HTAB is not permitted in the request line", nil)
		}
	}
	if first <= 0 || second <= 0 || second == len(line)-1 {
		return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
			"RFC9112 §2.1: method SP request-target SP HTTP-version",
			"malformed request line (need exactly two SP separators)", nil)
	}
	method := line[:first]
	target := line[first+1 : second]
	version := line[second+1:]

	if !isToken(method) {
		return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
			"RFC9110: method = token",
			"method is not a valid token", nil)
	}
	if err := validateRequestTarget(target, off+int64(first)+1); err != nil {
		return err
	}
	if err := validateHTTPVersion(version, off+int64(second)+1); err != nil {
		return err
	}
	req.Method = string(method)
	req.Target = string(target)
	req.Version = string(version)
	return nil
}

// validateRequestTarget accepts only origin-form ("/" path [ "?"
// query ]) in this subset; asterisk-form ("OPTIONS *") is also
// accepted at grammar level. Absolute-form / authority-form would
// imply proxy semantics and are refused (no proxy forwarding).
func validateRequestTarget(t []byte, off int64) error {
	if len(t) == 0 {
		return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
			"RFC9110: request-target is required",
			"empty request-target", nil)
	}
	for _, c := range t {
		switch {
		case c == 0x09:
			// HTAB only allowed within quoted strings elsewhere;
			// never in a request-target.
			return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
				"RFC9110: origin-form = absolute-path [ '?' query ]",
				"HTAB in request-target", nil)
		case c <= 0x1f || c == 0x7f:
			return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
				"RFC9110: control characters in request-target",
				"control byte in request-target", nil)
		}
	}
	switch {
	case string(t) == "*":
		return nil // asterisk-form
	case t[0] == '/':
		// Reject "//authority..." absolute-form smuggled into
		// origin-form and fragment separators.
		for _, c := range t {
			if c == '#' {
				return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
					"RFC9110: fragment is not sent in request-target",
					"'#' is not permitted in request-target", nil)
			}
		}
		return nil
	default:
		if hasSchemePrefix(t) {
			return perr(KindUnsupported, PhaseRequestLine, off, 400,
				"subset: no proxy forwarding",
				"absolute-form request-target is not supported", nil)
		}
		return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
			"RFC9110: only origin-form request-target is supported",
			"request-target must begin with '/'", nil)
	}
}

func hasSchemePrefix(t []byte) bool {
	for i := 0; i < len(t) && i < 16; i++ {
		c := t[i]
		if c == ':' {
			return i > 1 && i+2 < len(t) && t[i+1] == '/' && t[i+2] == '/'
		}
		if !isAlpha(c) && !isDigit(c) && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return false
}

func validateHTTPVersion(v []byte, off int64) error {
	switch string(v) {
	case "HTTP/1.1":
		return nil
	case "HTTP/1.0":
		return nil
	case "HTTP/0.9", "HTTP/2.0", "HTTP/2":
		return perr(KindUnsupported, PhaseRequestLine, off, 505,
			"subset: HTTP/1.0 and HTTP/1.1 only",
			"unsupported HTTP version: "+string(v), nil)
	default:
		return perr(KindInvalidSyntax, PhaseRequestLine, off, 400,
			"RFC9110: HTTP-name DQUOTE HTTP-version DQUOTE",
			"malformed HTTP version: "+string(v), nil)
	}
}

// parseFieldLine parses one "field-name OWS ':' OWS field-value OWS"
// line. Whitespace before the colon is explicitly rejected (RFC 9112
// removed that allowance to close a smuggling gap), and obsolete
// folding is rejected (no SP/HTAB leading bytes arrive here as a
// continuation at all).
func parseFieldLine(line []byte) (string, string, *Error) {
	ci := -1
	for i, c := range line {
		if c == ':' {
			ci = i
			break
		}
	}
	if ci <= 0 {
		return "", "", perr(KindInvalidSyntax, PhaseHeaderField, 0, 400,
			"RFC9110: fields line = field-name ':' OWS field-value OWS",
			"header field missing colon or name", nil)
	}
	name := line[:ci]
	for _, c := range name {
		if !isTchar(c) {
			return "", "", perr(KindInvalidSyntax, PhaseHeaderField, 0, 400,
				"RFC9110: field-name = token",
				"illegal byte in header field name", nil)
		}
	}
	value := trimOWS(line[ci+1:])
	if !validFieldValue(value) {
		return "", "", perr(KindInvalidSyntax, PhaseHeaderField, 0, 400,
			"RFC9110 §5.5 / RFC9112: field-value has no bare CR/LF and no obsolete folding",
			"illegal byte in header field value", nil)
	}
	return string(name), string(value), nil
}

// resolveFraming determines body framing from Content-Length and
// Transfer-Encoding, applying the strict RFC 9112 rules:
//
//   - CL present + TE present            -> 400 (conflict)
//   - multiple CL with differing decoded  -> 400 (ambiguous)
//     values
//   - malformed CL value                 -> 400
//   - TE names a coding other than       -> 501
//     chunked (identity excluded)
//   - single "chunked" TE                -> chunked framing
//   - neither                            -> no body
func resolveFraming(h *Header, off int64) (FrameMode, int64, error) {
	clVals := h.Values("Content-Length")
	teVals := h.Values("Transfer-Encoding")

	if len(teVals) > 0 && len(clVals) > 0 {
		return 0, 0, perr(KindProtocolConflict, PhaseFraming, off, 400,
			"RFC9112 §6.1: TE and CL must not be combined",
			"Transfer-Encoding and Content-Length both present", nil)
	}

	if len(teVals) > 0 {
		// This subset implements precisely one framing: a sole
		// "chunked" coding, no parameters and no chained codings.
		// Anything else (gzip, deflate, compress, "identity",
		// "chunked, chunked", parameters) is refused rather than
		// interpreted, so no coding-name obfuscation can change
		// framing decisions.
		flat := flattenCodings(teVals)
		if len(flat) != 1 || !equalFoldStr(flat[0], "chunked") {
			for _, cd := range flat {
				if equalFoldStr(cd, "identity") {
					return 0, 0, perr(KindProtocolConflict, PhaseFraming, off, 400,
						"RFC9112: identity is not a body transfer coding",
						"Transfer-Encoding: identity is not a body framing", nil)
				}
			}
			return 0, 0, perr(KindUnsupported, PhaseFraming, off, 501,
				"RFC9112: only a sole chunked transfer coding is implemented",
				"unsupported or ambiguous Transfer-Encoding: "+joinComma(flat), nil)
		}
		return FrameChunked, -1, nil
	}

	if len(clVals) > 0 {
		cl, err := parseContentLength(clVals, off)
		if err != nil {
			return 0, 0, err
		}
		return FrameFixed, cl, nil
	}
	return FrameNone, 0, nil
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

func flattenCodings(vals []string) []string {
	var out []string
	for _, v := range vals {
		out = append(out, splitComma(v)...)
	}
	return out
}

// containsBareNewline reports whether a field line carries an LF not
// part of a CRLF (it could only get here by hiding behind a later
// CRLF that ReadLine treated as the terminator).
func containsBareNewline(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' && (i == 0 || b[i-1] != '\r') {
			return true
		}
	}
	return false
}

// parseContentLength validates every Content-Length field line.
// Per RFC 9112 §6.3 a comma-separated list is accepted only when all
// members decode to the same value; multiple fields must agree too.
// Any malformed member makes the message invalid.
func parseContentLength(vals []string, off int64) (int64, error) {
	var first int64 = -1
	for _, raw := range vals {
		members := splitComma(raw)
		if len(members) == 0 {
			return 0, perr(KindInvalidSyntax, PhaseFraming, off, 400,
				"RFC9112 §6.3: Content-Length value",
				"empty Content-Length field", nil)
		}
		for _, m := range members {
			v, err := parseSingleCL(m)
			if err != nil {
				return 0, perr(KindInvalidSyntax, PhaseFraming, off, 400,
					"RFC9112 §6.3: Content-Length = 1*DIGIT",
					err.Error(), err)
			}
			if first == -1 {
				first = v
			} else if v != first {
				return 0, perr(KindProtocolConflict, PhaseFraming, off, 400,
					"RFC9112 §6.3: inconsistent Content-Length values",
					"differing Content-Length values in comma list or repeated fields", nil)
			}
		}
	}
	if first < 0 {
		return 0, perr(KindInvalidSyntax, PhaseFraming, off, 400,
			"RFC9112 §6.3: Content-Length value",
			"missing Content-Length value", nil)
	}
	return first, nil
}

func parseSingleCL(m string) (int64, error) {
	if len(m) == 0 {
		return 0, strconv.ErrSyntax
	}
	for i := 0; i < len(m); i++ {
		c := m[i]
		if !isDigit(c) {
			// Leading zeros are legal; spaces/signs/hex are not.
			return 0, strconv.ErrSyntax
		}
	}
	v, err := strconv.ParseInt(m, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}
