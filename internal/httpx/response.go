package httpx

import (
	"bufio"
	"net"
	"strconv"
	"time"
)

// responseWriter buffers the entire response body so the head can
// always carry an exact Content-Length (the subset never emits
// chunked responses). The business endpoints in this project produce
// small JSON documents, so full buffering is safe and keeps the
// response framing trivially exact.
type responseWriter struct {
	conn   net.Conn
	bw     *bufio.Writer
	req    *Request
	hdr    *ResponseHeader
	status int
	body   []byte
	sent   bool
	close  bool
}

func newResponseWriter(c net.Conn) *responseWriter {
	return &responseWriter{
		conn: c,
		bw:   bufio.NewWriterSize(c, 4096),
		hdr:  NewResponseHeader(),
	}
}

// rawConn exposes the connection for direct error responses.
func (w *responseWriter) rawConn() net.Conn { return w.conn }

func (w *responseWriter) reset(req *Request) {
	w.req = req
	w.hdr = NewResponseHeader()
	w.status = 0
	w.body = w.body[:0]
	w.sent = false
	w.close = false
	w.bw.Reset(w.conn)
}

// Header returns the mutable response header set.
func (w *responseWriter) Header() *ResponseHeader { return w.hdr }

// WriteHeader records the status (first call wins).
func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

// Write buffers body bytes and lazily defaults the status to 200.
func (w *responseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	w.body = append(w.body, p...)
	return len(p), nil
}

// flush serializes and sends exactly one response.
func (w *responseWriter) flush() error {
	if w.sent {
		return nil
	}
	if w.status == 0 {
		w.status = 200
	}
	if err := w.writeHead(); err != nil {
		return err
	}
	if len(w.body) > 0 {
		if _, err := w.bw.Write(w.body); err != nil {
			return err
		}
	}
	w.sent = true
	return w.bw.Flush()
}

func (w *responseWriter) writeHead() error {
	if _, err := w.bw.WriteString("HTTP/1.1 "); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(strconv.Itoa(w.status)); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(" " + statusReason(w.status) + "\r\n"); err != nil {
		return err
	}

	// RFC 9110: an origin server SHOULD send Date. Supplied as a
	// header frame like any other field (no special framing meaning).
	if _, ok := w.hdr.Get("Date"); !ok {
		w.hdr.Set("Date", time.Now().UTC().Format(time.RFC1123))
	}
	if _, ok := w.hdr.Get("Content-Length"); !ok {
		w.hdr.Set("Content-Length", strconv.Itoa(len(w.body)))
	}
	if _, ok := w.hdr.Get("Connection"); ok {
		// honor explicit header
	} else if w.close || (w.req != nil && !w.req.KeepAlive()) {
		w.hdr.Set("Connection", "close")
	} else {
		w.hdr.Set("Connection", "keep-alive")
	}
	for _, k := range w.hdr.order {
		for _, v := range w.hdr.values[k] {
			if _, err := w.bw.WriteString(k + ": " + v + "\r\n"); err != nil {
				return err
			}
		}
	}
	_, err := w.bw.WriteString("\r\n")
	return err
}

// writeContinue emits the interim 100 response before the body read.
func (w *responseWriter) writeContinue() error {
	_, err := w.conn.Write([]byte("HTTP/1.1 100 Continue\r\n\r\n"))
	return err
}

func (w *responseWriter) wantsClose() bool { return w.close }

// SetClose marks that the connection must close after this response.
func (w *responseWriter) SetClose() { w.close = true }

// writeSimpleError writes one plain-text error response directly and
// always marks Connection: close.
func writeSimpleError(c net.Conn, status int, reason string) {
	body := []byte(reason + "\n")
	bw := bufio.NewWriter(c)
	bw.WriteString("HTTP/1.1 " + strconv.Itoa(status) + " " + reason + "\r\n")
	bw.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	bw.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	bw.WriteString("Connection: close\r\n\r\n")
	bw.Write(body)
	bw.Flush()
}
