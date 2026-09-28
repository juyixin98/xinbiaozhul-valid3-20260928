package handler_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"testing"

	"h1parse/internal/handler"
	"h1parse/internal/logging"
	"h1parse/internal/protocol"
	"h1parse/internal/storage"
)

func newTestHandler(t *testing.T) *handler.Handler {
	t.Helper()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &handler.Handler{
		Store:  store,
		Log:    logging.Discard(),
		MaxBuf: 1 << 20,
	}
}

func serve(h *handler.Handler, req *protocol.Request) (string, protocol.ResponseWriter) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	rw := protocol.NewResponseWriter(bw, req.Method == "HEAD", false, 1<<20)
	h.Serve(context.Background(), rw, req)
	_ = rw.Flush()
	return buf.String(), rw
}

func TestHandlerRoutes(t *testing.T) {
	h := newTestHandler(t)
	cases := []struct {
		method, target string
		status         int
		bodyContains   string
	}{
		{"GET", "/healthz", 200, `"status":"ok"`},
		{"HEAD", "/healthz", 200, ""},
		{"GET", "/", 200, "h1parse"},
		{"GET", "/nope", 404, "no route"},
		{"DELETE", "/records", 405, "not allowed"},
		{"GET", "/records/zz", 404, "not found"},
	}
	for _, tc := range cases {
		req := &protocol.Request{Method: tc.method, Target: tc.target, ContentLength: -1,
			Body: emptyFrame{}}
		out, rw := serve(h, req)
		if rw.Status() != tc.status {
			t.Fatalf("%s %s: status=%d want %d; out=%s", tc.method, tc.target,
				rw.Status(), tc.status, out)
		}
		if tc.bodyContains != "" && !bytes.Contains([]byte(out), []byte(tc.bodyContains)) {
			t.Fatalf("%s %s: body missing %q: %s", tc.method, tc.target, tc.bodyContains, out)
		}
	}
}

func TestHandlerHEADHasNoBody(t *testing.T) {
	h := newTestHandler(t)
	req := &protocol.Request{Method: "HEAD", Target: "/healthz", ContentLength: -1,
		Body: emptyFrame{}}
	out, rw := serve(h, req)
	if rw.Status() != 200 {
		t.Fatalf("status %d", rw.Status())
	}
	// HEAD：有 Content-Length 头，但空行后无主体。
	if !bytes.Contains([]byte(out), []byte("Content-Length:")) {
		t.Fatalf("HEAD should carry Content-Length: %s", out)
	}
	idx := bytes.Index([]byte(out), []byte("\r\n\r\n"))
	if idx < 0 || idx+4 != len(out) {
		t.Fatalf("HEAD must have no body bytes after blank line: %q", out)
	}
}

func TestHandlerPOSTFixedThenGet(t *testing.T) {
	h := newTestHandler(t)
	req := &protocol.Request{
		Method: "POST", Target: "/records",
		HasLength: true, ContentLength: 5,
		Body:   &bytesFrame{data: []byte("hello")},
		Fields: []protocol.Field{{Name: "Content-Type", Value: "text/plain"}},
	}
	out, rw := serve(h, req)
	if rw.Status() != 201 {
		t.Fatalf("POST status=%d body=%s", rw.Status(), out)
	}
	if !bytes.Contains([]byte(out), []byte(`"size":5`)) {
		t.Fatalf("record size missing: %s", out)
	}
	// 取 id 再 GET（粗解析）。
	idField := "Location"
	_ = idField
	line := ""
	for _, l := range bytes.Split([]byte(out), []byte("\r\n")) {
		if bytes.HasPrefix(l, []byte("Location: /records/")) {
			line = string(bytes.TrimPrefix(l, []byte("Location: /records/")))
		}
	}
	if len(line) != 16 {
		t.Fatalf("bad location id %q from %s", line, out)
	}
	getReq := &protocol.Request{Method: "GET", Target: "/records/" + line,
		ContentLength: -1, Body: emptyFrame{}}
	out2, rw2 := serve(h, getReq)
	// "hello" 的 base64 为 "aGVsbG8="（[]byte 经 JSON 编码）。
	if rw2.Status() != 200 || !bytes.Contains([]byte(out2), []byte("aGVsbG8=")) {
		t.Fatalf("GET record failed: %s", out2)
	}
}

func TestHandlerPOSTRequiresLength(t *testing.T) {
	h := newTestHandler(t)
	req := &protocol.Request{Method: "POST", Target: "/records", ContentLength: -1,
		Body: emptyFrame{}}
	_, rw := serve(h, req)
	if rw.Status() != 411 {
		t.Fatalf("want 411, got %d", rw.Status())
	}
}

func TestHandlerBodyErrorForcesClose(t *testing.T) {
	h := newTestHandler(t)
	req := &protocol.Request{
		Method: "POST", Target: "/records",
		Chunked: true, Body: &errorFrame{},
	}
	_, rw := serve(h, req)
	if rw.Status() != 400 {
		t.Fatalf("want 400, got %d", rw.Status())
	}
	if !rw.CloseAfter() {
		t.Fatal("body protocol error must force close")
	}
}

// ---- 帧双 ----

type emptyFrame struct{}

func (emptyFrame) Read([]byte) (int, error) { return 0, io.EOF }
func (emptyFrame) Close() error             { return nil }
func (emptyFrame) Consumed() bool           { return true }
func (emptyFrame) FrameOffset() int64       { return 0 }

type bytesFrame struct{ data []byte }

func (f *bytesFrame) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	return n, nil
}
func (f *bytesFrame) Close() error       { return nil }
func (f *bytesFrame) Consumed() bool     { return len(f.data) == 0 }
func (f *bytesFrame) FrameOffset() int64 { return 0 }

type errorFrame struct{}

func (errorFrame) Read([]byte) (int, error) {
	return 0, &protocol.ProtoError{
		Kind: protocol.KindInvalid, Phase: protocol.PhaseChunkData,
		Offset: 3, Msg: "synthetic bad chunk",
	}
}
func (errorFrame) Close() error       { return nil }
func (errorFrame) Consumed() bool     { return false }
func (errorFrame) FrameOffset() int64 { return 0 }
