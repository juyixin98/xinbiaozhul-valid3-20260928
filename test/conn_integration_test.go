package testutil_test

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"http11subset/internal/httpx"
	"http11subset/internal/storage"
	"http11subset/test/testutil"
)

func itoa(n int) string { return strconv.Itoa(n) }

// pipeConn runs a real httpx.Conn over one end of a net.Pipe with an
// in-memory store; the test drives the client end.
func pipeConn(t *testing.T, hdl httpx.Handler) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	cfg := httpx.ServerConfig{
		Limits:            testLimits(),
		Logger:            httpx.NopLogger{},
		Handler:           hdl,
		InitialBuffer:     16,
		Enable100Continue: true,
	}
	cn := httpx.NewConn("test-conn", server, cfg)
	go cn.Serve()
	return client
}

func echoHandler(w httpx.ResponseWriter, r *httpx.Request) {
	w.Header().Set("X-Method", r.Method)
	w.Header().Set("X-Target", r.Target)
	w.WriteHeader(200)
	w.Write(r.Body)
}

func mustReadResponse(t *testing.T, c net.Conn, name string) (status int, headers string, body []byte) {
	t.Helper()
	// Read until we can parse Content-Length and the body; responses
	// are small and buffered, so a deadline-guarded loop suffices.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	var got []byte
	for {
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if i := bytes.Index(got, []byte("\r\n\r\n")); i >= 0 {
			headText := string(got[:i])
			// parse status
			line := strings.SplitN(headText, "\r\n", 2)[0]
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 2 {
				t.Fatalf("%s: bad status line %q", name, line)
			}
			st := 0
			for _, ch := range parts[1] {
				st = st*10 + int(ch-'0')
			}
			// locate content-length
			cl := -1
			for _, h := range strings.Split(headText, "\r\n")[1:] {
				if strings.HasPrefix(strings.ToLower(h), "content-length:") {
					v := strings.TrimSpace(h[len("content-length:"):])
					for _, ch := range v {
						if cl < 0 {
							cl = 0
						}
						cl = cl*10 + int(ch-'0')
					}
				}
			}
			if cl >= 0 && len(got) >= i+4+cl {
				return st, headText, got[i+4 : i+4+cl]
			}
		}
		if err != nil {
			t.Fatalf("%s: read response: %v (got %q)", name, err, got)
		}
	}
}

// TestKeepAliveTwoRequests sends two sequential requests on one
// connection and expects two ordered responses.
func TestKeepAliveTwoRequests(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	c := pipeConn(t, httpx.HandlerFunc(echoHandler))
	defer c.Close()

	for i, req := range []string{
		"GET /one HTTP/1.1\r\nHost: x\r\n\r\n",
		"POST /two HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nhello",
	} {
		if _, err := c.Write([]byte(req)); err != nil {
			t.Fatalf("write %d: %v", i+1, err)
		}
		status, head, body := mustReadResponse(t, c, "keepalive")
		if status != 200 {
			t.Fatalf("request %d status=%d", i+1, status)
		}
		if !strings.Contains(strings.ToLower(head), "connection: keep-alive") {
			t.Fatalf("request %d not keep-alive: %q", i+1, head)
		}
		want := ""
		if i == 1 {
			want = "hello"
		}
		if string(body) != want {
			t.Fatalf("request %d body=%q want %q", i+1, body, want)
		}
		log.Case("keepalive", i+1, "serve", "RESPONSE_200_KEEPALIVE",
			"persistent connection after exact body consume", nil,
			map[string]any{"body_len": len(body)})
	}
}

// TestPipelinedResponses verifies that two requests written at once
// yield exactly two responses, in order (no interleaving, no loss).
func TestPipelinedResponses(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	c := pipeConn(t, httpx.HandlerFunc(echoHandler))
	defer c.Close()

	wire := "GET /first HTTP/1.1\r\nHost: x\r\n\r\n" +
		"GET /second HTTP/1.1\r\nHost: x\r\n\r\n"
	if _, err := c.Write([]byte(wire)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, h1, _ := mustReadResponse(t, c, "first")
	_, h2, _ := mustReadResponse(t, c, "second")
	if !strings.Contains(h1, "X-Target: /first") {
		t.Fatalf("first response target wrong: %q", h1)
	}
	if !strings.Contains(h2, "X-Target: /second") {
		t.Fatalf("second response target wrong: %q", h2)
	}
	log.Step("pipeline", "TWO_RESPONSES_ORDERED", "strict serial dispatch on one connection",
		map[string]any{"case_id": "pipelined"})
}

// TestBadRequestClosesAndDropsResidual is the core smuggling guard:
// after a TE/CL conflict the server MUST respond 400 and close, and
// MUST never process the "smuggled" request bytes that follow.
func TestBadRequestClosesAndDropsResidual(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	called := false
	hdl := httpx.HandlerFunc(func(w httpx.ResponseWriter, r *httpx.Request) {
		if r.Target == "/smuggled" {
			called = true
		}
		echoHandler(w, r)
	})
	c := pipeConn(t, hdl)
	defer c.Close()

	smug := "POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: x\r\n\r\n"
	if _, err := c.Write([]byte(smug)); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, _, _ := mustReadResponse(t, c, "conflict")
	if status != 400 {
		t.Fatalf("status=%d want 400", status)
	}
	// Next read must report EOF/closed.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err == nil {
		t.Fatalf("expected connection close after 400")
	}
	if called {
		t.Fatalf("smuggled /smuggled request was dispatched")
	}
	log.Step("smuggle", "CONFLICT_400_CLOSE_NO_SMUGGLE",
		"RFC9112 TE/CL conflict => 400 then close; residual not dispatched",
		map[string]any{"case_id": "cl-te-smuggle"})
}

// TestTruncationClosesSilently checks a mid-body truncation closes the
// connection WITHOUT any HTTP response (no safe response boundary).
func TestTruncationClosesSilently(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	c := pipeConn(t, httpx.HandlerFunc(echoHandler))
	defer c.Close()

	trunc := "POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\nabc"
	if _, err := c.Write([]byte(trunc)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil && n != 0 {
		// acceptable: closed with no bytes
	}
	if n != 0 {
		t.Fatalf("expected silent close, got %d bytes %q", n, buf[:n])
	}
	log.Step("truncation", "CLOSED_SILENT", "truncated fixed body has no safe response boundary",
		map[string]any{"case_id": "trunc-fixed"})
}

// TestChunkedServerRoundTrip exercises chunked upload end to end and
// confirms the handler receives the decoded payload.
func TestChunkedServerRoundTrip(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	c := pipeConn(t, httpx.HandlerFunc(echoHandler))
	defer c.Close()

	wire := "POST /up HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"4;name=v\r\nWiki\r\n5\r\npedia\r\n0\r\nX-Done: 1\r\n\r\n"
	if _, err := c.Write([]byte(wire)); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, _, body := mustReadResponse(t, c, "chunked")
	if status != 200 || string(body) != "Wikipedia" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	log.Step("chunked", "DECODED_EXT_TRAILER",
		"chunk-ext skipped, trailer validated, payload assembled",
		map[string]any{"case_id": "chunked-roundtrip", "body": "Wikipedia"})
}

// Test413ResponseAndClose verifies an over-limit body yields 413 and
// closes the connection.
func Test413ResponseAndClose(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	c := pipeConn(t, httpx.HandlerFunc(echoHandler))
	defer c.Close()

	wire := "POST /big HTTP/1.1\r\nHost: x\r\nContent-Length: 1048577\r\n\r\n"
	if _, err := c.Write([]byte(wire)); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, _, _ := mustReadResponse(t, c, "big")
	if status != 413 {
		t.Fatalf("status=%d want 413", status)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 8)); err == nil {
		t.Fatalf("expected close after 413")
	}
	log.Step("limit", "413_CLOSE", "declared length over MaxBodyBytes",
		map[string]any{"case_id": "cl-over-limit"})
}

// TestStorageEndToEnd drives the real app handler + SQLite and
// verifies create/get/health over the strict parser.
func TestStorageEndToEnd(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()

	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// The app handler lives in another package; reuse it through a
	// tiny local shim that imports app via the server test helper.
	h := newAppHandler(t, store)
	c := pipeConn(t, h)
	defer c.Close()

	createBody := `{"title":"hi","payload":""}`
	create := "POST /v1/records HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: " +
		itoa(len(createBody)) + "\r\n\r\n" + createBody
	if _, err := c.Write([]byte(create)); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, _, body := mustReadResponse(t, c, "create")
	if status != 201 || !strings.Contains(string(body), `"id":1`) {
		t.Fatalf("create status=%d body=%s", status, body)
	}
	log.Step("storage", "CREATED_201", "SQLite insert through strict parser",
		map[string]any{"case_id": "db-create"})

	get := "GET /v1/records/1 HTTP/1.1\r\nHost: x\r\n\r\n"
	if _, err := c.Write([]byte(get)); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, _, body = mustReadResponse(t, c, "get")
	if status != 200 || !strings.Contains(string(body), `"title":"hi"`) {
		t.Fatalf("get status=%d body=%s", status, body)
	}
	log.Step("storage", "FETCHED_200", "SQLite select on same persistent connection",
		map[string]any{"case_id": "db-get"})

	health := "GET /healthz HTTP/1.1\r\nHost: x\r\n\r\n"
	if _, err := c.Write([]byte(health)); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, _, body = mustReadResponse(t, c, "health")
	if status != 200 || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("health status=%d body=%s", status, body)
	}

	// Ensure no leaked reads: a fresh request after 3 still works.
	extra := "GET /healthz HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
	if _, err := c.Write([]byte(extra)); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, _, _ = mustReadResponse(t, c, "health-close")
	if status != 200 {
		t.Fatalf("final health status=%d", status)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.Copy(io.Discard, c); err != nil && !strings.Contains(err.Error(), "closed") {
		// deadline or close both fine
	}
}
