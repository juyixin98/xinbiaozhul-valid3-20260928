// Package refparse is an INDEPENDENT reference decoder for the
// HTTP/1.1 subset. It exists only for tests: legal byte fixtures are
// decoded both by the production parser (internal/httpx) and by this
// file, and the two independent decodings must agree.
//
// It is deliberately written in a different style from the streaming
// parser: it operates on a fully buffered byte slice, splits lines
// with bytes.Split and collects fields into a map. It shares no code
// with internal/httpx, so an agreement failure indicates a real
// divergence rather than a duplicated expectation.
package refparse

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Field is one header field in wire order.
type Field struct{ Name, Value string }

// Message is one independently decoded request.
type Message struct {
	Method    string
	Target    string
	Version   string
	Fields    []Field
	Chunked   bool
	HasLength bool
	Length    int64
	Body      []byte
	Consumed  int // total bytes consumed for this message
	HeadEnd   int // offset just past the terminating empty line
}

// Parse decodes exactly one request from data and returns it together
// with the unconsumed remainder (the start of the next pipelined
// request). Any illegal input is an error.
func Parse(data []byte, maxHead int, maxBody int64, maxChunkLine int) (*Message, []byte, error) {
	m := &Message{}

	// Locate the end of the head section as the first CRLF CRLF,
	// scanning with a local helper (no shared code).
	headEnd, err := findHeadEnd(data, maxHead)
	if err != nil {
		return nil, nil, err
	}
	m.HeadEnd = headEnd
	// head spans the request line and header fields only: drop the
	// terminating empty line, i.e. the trailing CRLF CRLF.
	head := data[:headEnd-4]
	lines := bytes.Split(head, []byte("\r\n"))

	// Request line.
	rline := strings.Split(string(lines[0]), " ")
	if len(rline) != 3 {
		return nil, nil, errors.New("ref: request line must be method SP target SP version")
	}
	for _, c := range []byte(rline[0]) {
		if !tokChar(c) {
			return nil, nil, errors.New("ref: bad method token")
		}
	}
	m.Method = rline[0]
	m.Target = rline[1]
	m.Version = rline[2]
	if m.Version != "HTTP/1.1" && m.Version != "HTTP/1.0" {
		return nil, nil, fmt.Errorf("ref: unsupported version %q", m.Version)
	}
	if len(m.Target) == 0 {
		return nil, nil, errors.New("ref: empty target")
	}
	if m.Target[0] != '/' && m.Target != "*" {
		return nil, nil, errors.New("ref: only origin-form / asterisk supported")
	}

	// Header fields into both an ordered list and a multi-value map.
	index := map[string][]string{}
	for _, ln := range lines[1:] {
		colon := bytes.IndexByte(ln, ':')
		if colon <= 0 {
			return nil, nil, errors.New("ref: header field without name/colon")
		}
		name := string(ln[:colon])
		for _, c := range []byte(name) {
			if !tokChar(c) {
				return nil, nil, errors.New("ref: bad field name")
			}
		}
		val := strings.TrimSpace(string(ln[colon+1:]))
		for _, c := range []byte(val) {
			if c < 0x20 && c != '\t' {
				return nil, nil, errors.New("ref: control byte in field value")
			}
		}
		m.Fields = append(m.Fields, Field{name, val})
		lname := strings.ToLower(name)
		index[lname] = append(index[lname], val)
	}

	te, hasTE := index["transfer-encoding"]
	cls, hasCL := index["content-length"]
	if hasTE && hasCL {
		return nil, nil, errors.New("ref: TE/CL conflict")
	}

	rest := data[headEnd:]

	switch {
	case hasTE:
		if len(te) != 1 || !strings.EqualFold(strings.TrimSpace(te[0]), "chunked") {
			return nil, nil, errors.New("ref: only sole chunked TE supported")
		}
		m.Chunked = true
		body, used, cerr := decodeChunks(rest, maxBody, maxChunkLine)
		if cerr != nil {
			return nil, nil, cerr
		}
		m.Body = body
		m.Consumed = headEnd + used
	case hasCL:
		vals := []string{}
		for _, raw := range cls {
			for _, part := range strings.Split(raw, ",") {
				vals = append(vals, strings.TrimSpace(part))
			}
		}
		var first int64 = -1
		for _, v := range vals {
			if v == "" {
				return nil, nil, errors.New("ref: empty CL member")
			}
			for _, c := range []byte(v) {
				if c < '0' || c > '9' {
					return nil, nil, errors.New("ref: non-digit CL")
				}
			}
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				return nil, nil, perr
			}
			if first == -1 {
				first = n
			} else if n != first {
				return nil, nil, errors.New("ref: conflicting CL values")
			}
		}
		m.HasLength = true
		m.Length = first
		if first > maxBody {
			return nil, nil, errors.New("ref: body over limit")
		}
		if int64(len(rest)) < first {
			return nil, nil, errors.New("ref: truncated fixed body")
		}
		m.Body = append([]byte(nil), rest[:first]...)
		m.Consumed = headEnd + int(first)
	default:
		m.Consumed = headEnd
	}

	return m, data[m.Consumed:], nil
}

// findHeadEnd returns the offset immediately after "CRLF CRLF" that
// closes the head, rejecting bare LF anywhere before it.
func findHeadEnd(data []byte, maxHead int) (int, error) {
	if len(data) == 0 {
		return 0, errors.New("ref: empty input")
	}
	limit := maxHead
	if len(data) < limit {
		limit = len(data)
	}
	for i := 0; i < limit; i++ {
		if data[i] == '\n' && (i == 0 || data[i-1] != '\r') {
			return 0, errors.New("ref: bare LF in head")
		}
		if i+3 < len(data) && data[i] == '\r' && data[i+1] == '\n' &&
			data[i+2] == '\r' && data[i+3] == '\n' {
			return i + 4, nil
		}
	}
	if len(data) > maxHead {
		return 0, errors.New("ref: head over limit")
	}
	return 0, errors.New("ref: truncated head")
}

// decodeChunks independently walks chunked framing.
func decodeChunks(data []byte, maxBody int64, maxChunkLine int) (body []byte, used int, err error) {
	pos := 0
	for {
		eol := bytes.Index(data[pos:], []byte("\r\n"))
		if eol < 0 {
			return nil, 0, errors.New("ref: unterminated chunk line")
		}
		if eol > maxChunkLine {
			return nil, 0, errors.New("ref: chunk line too long")
		}
		line := string(data[pos : pos+eol])
		pos += eol + 2
		sizeTok := line
		if sc := strings.IndexByte(line, ';'); sc >= 0 {
			sizeTok = strings.TrimRight(line[:sc], " \t")
		}
		if sizeTok == "" {
			return nil, 0, errors.New("ref: empty chunk size")
		}
		size, perr := strconv.ParseUint(sizeTok, 16, 64)
		if perr != nil {
			return nil, 0, fmt.Errorf("ref: bad chunk hex: %w", perr)
		}
		if size == 0 {
			// trailers until empty line
			for {
				end := bytes.Index(data[pos:], []byte("\r\n"))
				if end < 0 {
					return nil, 0, errors.New("ref: truncated trailer")
				}
				line := data[pos : pos+end]
				pos += end + 2
				if len(line) == 0 {
					return body, pos, nil
				}
				ln := string(line)
				if strings.Contains(ln, "\r") || strings.Contains(ln, "\n") {
					return nil, 0, errors.New("ref: bad trailer")
				}
			}
		}
		if int64(len(body))+int64(size) > maxBody {
			return nil, 0, errors.New("ref: body over limit")
		}
		if pos+int(size)+2 > len(data) {
			return nil, 0, errors.New("ref: truncated chunk data")
		}
		body = append(body, data[pos:pos+int(size)]...)
		pos += int(size)
		if data[pos] != '\r' || data[pos+1] != '\n' {
			return nil, 0, errors.New("ref: missing CRLF after chunk data")
		}
		pos += 2
	}
}

func tokChar(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.',
		'^', '_', '`', '|', '~':
		return true
	}
	return false
}
