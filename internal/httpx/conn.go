package httpx

import (
	"errors"
	"net"
	"strconv"
)

// Logger is the minimal structured logging sink used by the connection
// machine. Keys are structured; byte offsets are always present on
// framing events.
type Logger interface {
	ParseError(connID string, seq int, e *Error)
	ConnEvent(connID, event string, fields map[string]any)
	RequestEvent(connID string, seq int, event string, fields map[string]any)
}

// Handler is the business entry point. It receives a fully framed
// request (body already bounded and consumed) and writes one complete
// response via ResponseWriter. A handler error becomes a 500 and is
// logged; framing itself is never the handler's responsibility.
type Handler interface {
	ServeHTTP(w ResponseWriter, r *Request)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ResponseWriter, *Request)

// ServeHTTP implements Handler.
func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) { f(w, r) }

// ResponseWriter assembles one HTTP/1.1 response. The status code and
// headers must be set before Write is called; Write flushes the head
// once and then streams the body.
type ResponseWriter interface {
	Header() *ResponseHeader
	WriteHeader(status int)
	Write([]byte) (int, error)
}

// ResponseHeader is the response-side header set (order preserving).
type ResponseHeader struct {
	order  []string
	values map[string][]string
}

// NewResponseHeader constructs an empty response header set.
func NewResponseHeader() *ResponseHeader {
	return &ResponseHeader{values: map[string][]string{}}
}

// Set replaces all values for a name.
func (h *ResponseHeader) Set(name, value string) {
	if _, ok := h.values[name]; !ok {
		h.order = append(h.order, name)
	}
	h.values[name] = []string{value}
}

// Add appends a value for a name.
func (h *ResponseHeader) Add(name, value string) {
	if _, ok := h.values[name]; !ok {
		h.order = append(h.order, name)
	}
	h.values[name] = append(h.values[name], value)
}

// Get returns the first value.
func (h *ResponseHeader) Get(name string) (string, bool) {
	v, ok := h.values[name]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

// ServerConfig configures a connection (timeouts are applied by the
// TCP listener layer; the machine itself only parses).
type ServerConfig struct {
	Limits            Limits
	Logger            Logger
	Handler           Handler
	InitialBuffer     int
	Enable100Continue bool
}

// Conn drives the per-connection state machine. One Conn serves every
// pipelined request on one net.Conn, strictly in order.
type Conn struct {
	id      string
	c       net.Conn
	p       *Parser
	cfg     ServerConfig
	w       *responseWriter
	seq     int
	closing bool
}

// NewConn wraps a connection.
func NewConn(id string, c net.Conn, cfg ServerConfig) *Conn {
	if cfg.InitialBuffer == 0 {
		cfg.InitialBuffer = 8 * 1024
	}
	return &Conn{
		id:  id,
		c:   c,
		p:   NewParser(c, cfg.Limits, cfg.InitialBuffer),
		cfg: cfg,
		w:   newResponseWriter(c),
	}
}

// Serve runs the request loop until the peer closes cleanly, a framing
// error forces shutdown, the handler signals Connection: close, or a
// transport write fails. It always closes the underlying connection.
func (cn *Conn) Serve() {
	defer cn.c.Close()
	cn.cfg.Logger.ConnEvent(cn.id, "open", map[string]any{
		"remote": cn.c.RemoteAddr().String(),
	})
	for {
		cn.seq++
		keepAlive, err := cn.serveOne(cn.seq)
		if err != nil {
			// Framing/IO errors are terminal: the connection is
			// closed without trusting any residual bytes as a new
			// request. When an HTTP status is defined for the failure
			// we best-effort emit one error response first.
			cn.shutdown(err)
			return
		}
		if !keepAlive || cn.closing {
			cn.cfg.Logger.ConnEvent(cn.id, "close_requested", map[string]any{
				"requests": cn.seq,
			})
			return
		}
	}
}

// serveOne parses one complete request and dispatches it. It returns
// keepAlive=false when the request asked for the connection to close.
func (cn *Conn) serveOne(seq int) (bool, error) {
	req, err := cn.p.ReadHead()
	if err != nil {
		if errors.Is(err, ErrCleanEOF) {
			if seq == 1 {
				cn.cfg.Logger.ConnEvent(cn.id, "clean_eof_idle", nil)
			} else {
				cn.cfg.Logger.ConnEvent(cn.id, "clean_eof", map[string]any{
					"requests": seq - 1,
				})
			}
			return false, nil
		}
		return false, err
	}

	cn.cfg.Logger.RequestEvent(cn.id, seq, "head", map[string]any{
		"method":  req.Method,
		"target":  req.Target,
		"version": req.Version,
		"frame":   frameName(req.Frame),
		"offset":  req.HeadOffset,
	})

	// Expect: 100-continue is answered once, before body reads, when
	// enabled; the body is then read regardless (it must be consumed
	// to preserve the framing boundary). Unknown Expect values get a
	// 417 and close the connection.
	if v, ok := req.Header.Get("Expect"); ok {
		if !equalFoldStr(v, "100-continue") {
			return false, perr(KindUnsupported, PhaseFraming,
				cn.p.Offset(), 417, "RFC9110: Expect extension not implemented",
				"unsupported Expect value: "+v, nil)
		}
		if cn.cfg.Enable100Continue {
			if err := cn.w.writeContinue(); err != nil {
				return false, err
			}
		}
	}

	if err := cn.p.ReadBody(req); err != nil {
		return false, err
	}

	cn.cfg.Logger.RequestEvent(cn.id, seq, "body_complete", map[string]any{
		"body_bytes":      len(req.Body),
		"next_req_offset": req.BodyEndOffset,
	})

	// Dispatch to business logic.
	cn.w.reset(req)
	cn.cfg.Handler.ServeHTTP(cn.w, req)
	if err := cn.w.flush(); err != nil {
		return false, err
	}

	keep := req.KeepAlive()
	if cn.w.wantsClose() {
		keep = false
	}
	return keep, nil
}

// shutdown is the terminal path. When the failure carries an HTTP
// status the machine best-effort writes a single error response; the
// connection is then closed so no residual byte can be interpreted as
// a second request. Truncation (Code 0) and IO failures are closed
// silently, since no safe response boundary exists.
func (cn *Conn) shutdown(err error) {
	if pe, ok := AsError(err); ok {
		cn.cfg.Logger.ParseError(cn.id, cn.seq, pe)
		if pe.Code != 0 {
			writeSimpleError(cn.c, pe.Code, statusReason(pe.Code))
		}
		return
	}
	cn.cfg.Logger.ConnEvent(cn.id, "io_error", map[string]any{
		"error": err.Error(),
	})
}

func frameName(f FrameMode) string {
	switch f {
	case FrameFixed:
		return "content-length"
	case FrameChunked:
		return "chunked"
	default:
		return "none"
	}
}

func statusReason(code int) string {
	switch code {
	case 100:
		return "Continue"
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 400:
		return "Bad Request"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 413:
		return "Content Too Large"
	case 415:
		return "Unsupported Media Type"
	case 417:
		return "Expectation Failed"
	case 431:
		return "Request Header Fields Too Large"
	case 500:
		return "Internal Server Error"
	case 501:
		return "Not Implemented"
	case 505:
		return "HTTP Version Not Supported"
	default:
		return "Error"
	}
}

// itoa is a tiny local int-to-string used on hot paths.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
