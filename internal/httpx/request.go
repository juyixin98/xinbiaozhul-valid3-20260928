package httpx

// Method is the request method token (e.g. "GET", "POST").
type Method string

// Header is an ordered, case-insensitive multi-value header set.
// Original on-the-wire names are preserved; lookups are
// case-insensitive (RFC 9110 field names are case-insensitive).
type Header struct {
	Keys []string
	Vals [][]string
}

// Get returns the first value for the (case-insensitive) name and
// whether it was present.
func (h *Header) Get(name string) (string, bool) {
	for i, k := range h.Keys {
		if equalFoldStr(k, name) {
			if len(h.Vals[i]) > 0 {
				return h.Vals[i][0], true
			}
			return "", true
		}
	}
	return "", false
}

// Values returns every value for a name (one per header field line).
func (h *Header) Values(name string) []string {
	for i, k := range h.Keys {
		if equalFoldStr(k, name) {
			return h.Vals[i]
		}
	}
	return nil
}

func equalFoldStr(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'a' && x <= 'z' {
			x -= 'a' - 'A'
		}
		if y >= 'a' && y <= 'z' {
			y -= 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// add appends a parsed field line.
func (h *Header) add(name, value string) {
	for i, k := range h.Keys {
		if equalFoldStr(k, name) {
			h.Vals[i] = append(h.Vals[i], value)
			return
		}
	}
	h.Keys = append(h.Keys, name)
	h.Vals = append(h.Vals, []string{value})
}

// FrameMode identifies the message body transfer coding.
type FrameMode int

const (
	// FrameNone: no body is present (Content-Length absent, no TE).
	FrameNone FrameMode = iota
	// FrameFixed: exactly ContentLength octets follow.
	FrameFixed
	// FrameChunked: chunked transfer coding per RFC 9112.
	FrameChunked
)

// Request is a fully framed HTTP/1.1 request. Body is fully buffered
// (bounded by Limits.MaxBodyBytes) so the state machine can prove
// exact message consumption before dispatching the next pipeline
// request.
type Request struct {
	Method        string
	Target        string // origin-form request-target (path?query)
	Version       string // "HTTP/1.0" | "HTTP/1.1"
	Header        Header
	Frame         FrameMode
	ContentLength int64 // valid when Frame == FrameFixed
	Body          []byte

	// HeadOffset is the absolute stream offset of the request line.
	HeadOffset int64
	// BodyEndOffset is the absolute offset of the first byte after
	// the fully consumed message (start of the next pipelined
	// request, or end of stream).
	BodyEndOffset int64
}

// KeepAlive reports whether the connection should stay open based on
// version defaults and the Connection header.
func (r *Request) KeepAlive() bool {
	vals := r.Header.Values("Connection")
	hasClose := false
	hasKeepAlive := false
	for _, v := range vals {
		for _, tok := range splitComma(v) {
			switch {
			case equalFoldStr(tok, "close"):
				hasClose = true
			case equalFoldStr(tok, "keep-alive"):
				hasKeepAlive = true
			}
		}
	}
	if r.Version == "HTTP/1.1" {
		return !hasClose
	}
	return hasKeepAlive // HTTP/1.0 default is close
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			tok := trimOWSString(s[start:i])
			if tok != "" {
				out = append(out, tok)
			}
			start = i + 1
		}
	}
	return out
}

func trimOWSString(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
