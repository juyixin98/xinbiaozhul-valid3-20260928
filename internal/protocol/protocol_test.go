package protocol_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"h1parse/internal/protocol"
)

// runID/版本注入：测试日志带这些标识。
const testRun = "run-unit-0001"

func mustReq(t *testing.T, in string) (*protocol.Request, *protocol.Parser) {
	t.Helper()
	p := protocol.NewParser(strings.NewReader(in), protocol.DefaultLimits())
	req, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	return req, p
}

func readBody(t *testing.T, req *protocol.Request) string {
	t.Helper()
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("body read: %v", err)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatalf("body close: %v", err)
	}
	return string(b)
}

func TestLegalFixedBody(t *testing.T) {
	req, _ := mustReq(t, "POST /x HTTP/1.1\r\nContent-Length: 5\r\n\r\nhello")
	if req.Method != "POST" || req.Target != "/x" {
		t.Fatalf("request line: %s %s", req.Method, req.Target)
	}
	if !req.HasLength || req.ContentLength != 5 {
		t.Fatalf("framing: has=%v len=%d", req.HasLength, req.ContentLength)
	}
	if got := readBody(t, req); got != "hello" {
		t.Fatalf("body = %q", got)
	}
}

func TestLegalChunkedWithExtAndTrailer(t *testing.T) {
	in := "POST /x HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n" +
		"6;name=\"v\";x=1\r\n world\r\n" +
		"0\r\nETag: abc\r\n\r\n"
	req, _ := mustReq(t, in)
	if !req.Chunked {
		t.Fatal("expected chunked")
	}
	if got := readBody(t, req); got != "hello world" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestLegalDuplicateCLSameValueLeadingZero(t *testing.T) {
	in := "POST /x HTTP/1.1\r\nContent-Length: 5\r\nContent-Length: 05\r\n\r\nhello"
	req, _ := mustReq(t, in)
	if req.ContentLength != 5 {
		t.Fatalf("len=%d", req.ContentLength)
	}
}

// TestRejectCases 覆盖每一条拒绝条件。
func TestRejectCases(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		kind   protocol.Kind
		phase  string
		status int
		offGE  int64 // 断言偏移下界；-1 不检查
	}{
		{
			"te-cl-both",
			"POST / HTTP/1.1\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseFraming, 400, 0,
		},
		{
			"duplicate-cl-differ",
			"POST / HTTP/1.1\r\nContent-Length: 5\r\nContent-Length: 6\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseFraming, 400, 0,
		},
		{
			"cl-comma-list",
			"POST / HTTP/1.1\r\nContent-Length: 1, 2\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseFraming, 400, 0,
		},
		{
			"cl-overflow",
			"POST / HTTP/1.1\r\nContent-Length: 99999999999999999999\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseFraming, 400, 0,
		},
		{
			"cl-nonnumeric",
			"POST / HTTP/1.1\r\nContent-Length: 5x\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseFraming, 400, 0,
		},
		{
			"bare-lf",
			"GET / HTTP/1.1\nHost: x\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseRequestLine, 400, 0,
		},
		{
			"stray-cr",
			"GET / HTTP/1.1\r\nX: a\rb\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseRequestLine, 400, 0,
		},
		{
			"folded-header",
			"GET / HTTP/1.1\r\nBad Header: x\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseHeader, 400, 0,
		},
		{
			"no-colon",
			"GET / HTTP/1.1\r\nX-No-Colon\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseHeader, 400, 0,
		},
		{
			"extra-sp-line",
			"GET  /  HTTP/1.1\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseRequestLine, 400, 0,
		},
		{
			"absolute-form",
			"GET http://h/ HTTP/1.1\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseRequestLine, 400, 0,
		},
		{
			"authority-form",
			"CONNECT h:443 HTTP/1.1\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseRequestLine, 400, 0,
		},
		{
			"version-10",
			"GET / HTTP/1.0\r\n\r\n",
			protocol.KindVersionUnsupported, protocol.PhaseRequestLine, 505, 0,
		},
		{
			"te-identity",
			"POST / HTTP/1.1\r\nTransfer-Encoding: identity\r\n\r\n",
			protocol.KindUnsupportedCoding, protocol.PhaseFraming, 501, 0,
		},
		{
			"chunked-not-last",
			"POST / HTTP/1.1\r\nTransfer-Encoding: chunked, gzip\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseFraming, 400, 0,
		},
		{
			"connection-upgrade",
			"GET / HTTP/1.1\r\nConnection: upgrade\r\nUpgrade: websocket\r\n\r\n",
			protocol.KindUpgradeUnsupported, protocol.PhaseFraming, 501, 0,
		},
		{
			"expect-other",
			"POST / HTTP/1.1\r\nContent-Length: 1\r\nExpect: 200-ok\r\n\r\nx",
			protocol.KindExpectationFailed, protocol.PhaseFraming, 417, 0,
		},
		{
			"chunk-size-bad-hex",
			"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5g\r\nabcde\r\n0\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseChunkSize, 400, 0,
		},
		{
			"chunk-size-overflow",
			"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n10000000000000000\r\n",
			protocol.KindInvalid, protocol.PhaseChunkSize, 400, 0,
		},
		{
			"chunk-data-bad-crlf",
			"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nabcdeX\r\n0\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseChunkData, 400, 0,
		},
		{
			"trailer-bad-field",
			"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nno-colon-here\r\n\r\n",
			protocol.KindInvalid, protocol.PhaseHeader, 400, 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := protocol.NewParser(strings.NewReader(tc.input), protocol.DefaultLimits())
			req, err := p.Next()
			if err == nil {
				// 体阶段错误（chunk-*）需要读体才暴露。
				if req != nil && req.Body != nil {
					_, err = io.ReadAll(req.Body)
				}
			}
			pe := protocol.AsProtoError(err)
			if pe == nil {
				t.Fatalf("expected %s error, got accept", tc.kind)
			}
			if pe.Kind != tc.kind {
				t.Fatalf("kind=%s want %s; err=%v", pe.Kind, tc.kind, pe)
			}
			if pe.Phase != tc.phase {
				t.Fatalf("phase=%s want %s", pe.Phase, tc.phase)
			}
			if got := pe.HTTPStatus(); got != tc.status {
				t.Fatalf("status=%d want %d", got, tc.status)
			}
			if tc.offGE >= 0 && pe.Offset < tc.offGE {
				t.Fatalf("offset=%d want >= %d", pe.Offset, tc.offGE)
			}
			if pe.CloseAfterError() != true {
				t.Fatal("protocol errors must close connection")
			}
		})
	}
}

func TestLimitsHeaderTooLarge(t *testing.T) {
	lim := protocol.DefaultLimits()
	var sb strings.Builder
	sb.WriteString("GET / HTTP/1.1\r\n")
	for int64(sb.Len()) < lim.MaxHeaderBytes {
		sb.WriteString("X-Pad: 0000000000000000\r\n")
	}
	sb.WriteString("\r\n")
	p := protocol.NewParser(strings.NewReader(sb.String()), lim)
	_, err := p.Next()
	pe := protocol.AsProtoError(err)
	if pe == nil || pe.Kind != protocol.KindHeaderTooLarge || pe.HTTPStatus() != 431 {
		t.Fatalf("want 431 header_too_large, got %v", err)
	}
}

func TestLimitsBodyAndChunk(t *testing.T) {
	lim := protocol.Limits{MaxHeaderBytes: 1024, MaxBodyBytes: 10, MaxChunkSize: 8}
	t.Run("declared CL too large", func(t *testing.T) {
		p := protocol.NewParser(strings.NewReader("POST / HTTP/1.1\r\nContent-Length: 11\r\n\r\n"), lim)
		_, err := p.Next()
		pe := protocol.AsProtoError(err)
		if pe == nil || pe.Kind != protocol.KindPayloadTooLarge || pe.HTTPStatus() != 413 {
			t.Fatalf("want 413, got %v", err)
		}
	})
	t.Run("single chunk too large", func(t *testing.T) {
		in := "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n9\r\n123456789\r\n0\r\n\r\n"
		p := protocol.NewParser(strings.NewReader(in), lim)
		req, err := p.Next()
		if err != nil {
			t.Fatalf("headers should parse: %v", err)
		}
		_, rerr := io.ReadAll(req.Body)
		pe := protocol.AsProtoError(rerr)
		if pe == nil || pe.Kind != protocol.KindPayloadTooLarge {
			t.Fatalf("want chunk 413, got %v", rerr)
		}
	})
	t.Run("cumulative chunks too large", func(t *testing.T) {
		in := "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" +
			"5\r\n12345\r\n5\r\n12345\r\n0\r\n\r\n"
		lim2 := protocol.Limits{MaxHeaderBytes: 1024, MaxBodyBytes: 9, MaxChunkSize: 8192}
		p := protocol.NewParser(strings.NewReader(in), lim2)
		req, _ := p.Next()
		_, err := io.ReadAll(req.Body)
		pe := protocol.AsProtoError(err)
		if pe == nil || pe.Kind != protocol.KindPayloadTooLarge {
			t.Fatalf("want cumulative 413, got %v", err)
		}
	})
}

// TestHalfAndStickyPackets 用逐字节/分块喂入验证半包透明，以及
// 流水线粘包后第二请求边界精确。
func TestHalfAndStickyPackets(t *testing.T) {
	msg := []byte("POST /records HTTP/1.1\r\nContent-Length: 3\r\n\r\nabc" +
		"GET /healthz HTTP/1.1\r\n\r\n")
	for _, chunk := range []int{1, 2, 3, 5, 7, 64} {
		name := "chunk-" + itoa(chunk)
		t.Run(name, func(t *testing.T) {
			feed := newScriptedReader(msg, chunk)
			p := protocol.NewParser(feed, protocol.DefaultLimits())
			req1, err := p.Next()
			if err != nil {
				t.Fatalf("req1 headers: %v", err)
			}
			b1, _ := io.ReadAll(req1.Body)
			if string(b1) != "abc" {
				t.Fatalf("req1 body=%q", b1)
			}
			if err := req1.Body.Close(); err != nil {
				t.Fatal(err)
			}
			req2, err := p.Next()
			if err != nil {
				t.Fatalf("req2 (pipelined) must parse, got %v", err)
			}
			if req2.Method != "GET" || req2.Target != "/healthz" {
				t.Fatalf("req2=%s %s", req2.Method, req2.Target)
			}
			if !req2.Body.Consumed() {
				t.Fatal("GET body frame not at boundary")
			}
			if _, err := p.Next(); err != io.EOF {
				t.Fatalf("want clean EOF, got %v", err)
			}
		})
	}
}

// TestTruncations 验证各类截断被识别为 KindIncomplete，而不是 400。
func TestTruncations(t *testing.T) {
	cases := map[string]string{
		"header-mid-line": "GET / HTTP/1.1\r\nHost: x",
		"fixed-body":      "POST / HTTP/1.1\r\nContent-Length: 5\r\n\r\nab",
		"chunk-data":      "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nab",
		"chunk-crlf":      "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nabcde",
		"trailer":         "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nX: y",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			p := protocol.NewParser(strings.NewReader(in), protocol.DefaultLimits())
			req, err := p.Next()
			if err == nil {
				_, err = io.ReadAll(req.Body)
			}
			pe := protocol.AsProtoError(err)
			if pe == nil || pe.Kind != protocol.KindIncomplete {
				t.Fatalf("want incomplete, got %v", err)
			}
			if pe.HTTPStatus() != 0 {
				t.Fatalf("incomplete must map to status 0 (no response), got %d", pe.HTTPStatus())
			}
		})
	}
}

// TestResidualBodyNotNextRequest 是走私核心断言：消息体内的请求行绝不被解析。
func TestResidualBodyNotNextRequest(t *testing.T) {
	embedded := []byte("12345GET / HTTP/1.1\r\nHost: y\r\n\r\nX") // 33 字节
	if len(embedded) != 33 {
		t.Fatalf("fixture math wrong: %d", len(embedded))
	}
	in := append([]byte("POST /records HTTP/1.1\r\nContent-Length: 33\r\n\r\n"), embedded...)
	p := protocol.NewParser(bytes.NewReader(in), protocol.DefaultLimits())
	req, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if !bytes.Equal(body, embedded) {
		t.Fatalf("body mismatch: %q", body)
	}
	if _, err := p.Next(); err != io.EOF {
		t.Fatalf("embedded request must NOT be parsed as request 2; got %v", err)
	}
}

// TestOffsetAbsoluteAcrossPipelinedRequests 偏移在持久连接上累计正确。
func TestOffsetAbsoluteAcrossPipelinedRequests(t *testing.T) {
	first := "POST /x HTTP/1.1\r\nContent-Length: 2\r\n\r\nab"
	secondBad := "GARBAGE\r\n\r\n" // 无 SP 的非法请求行
	combined := first + secondBad
	p := protocol.NewParser(strings.NewReader(combined), protocol.DefaultLimits())
	req1, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(req1.Body); err != nil {
		t.Fatal(err)
	}
	_, err = p.Next()
	pe := protocol.AsProtoError(err)
	if pe == nil || pe.Kind != protocol.KindInvalid {
		t.Fatalf("want invalid second request, got %v", err)
	}
	// 第二请求行行首偏移必须恰好等于第一请求的线长（绝对偏移累计正确）。
	if pe.Offset != int64(len(first)) {
		t.Fatalf("offset=%d want %d (must be absolute on connection)",
			pe.Offset, len(first))
	}
}

// ---- 测试辅助 ----

type scriptedReader struct {
	data []byte
	pos  int
	n    int
}

func newScriptedReader(data []byte, n int) *scriptedReader { return &scriptedReader{data: data, n: n} }

func (s *scriptedReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		return 0, io.EOF
	}
	want := s.n
	if want > len(p) {
		want = len(p)
	}
	if want > len(s.data)-s.pos {
		want = len(s.data) - s.pos
	}
	copy(p, s.data[s.pos:s.pos+want])
	s.pos += want
	return want, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
