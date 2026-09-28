package e2e

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestSmugglingEmbeddedRequestNeverParsed 是请求走私的核心黑盒断言：
// 在声明长度的消息体里嵌入一个完整伪造请求，该伪造请求绝不能被服务端
// 当作第二请求处理（不能返回第二条响应，且随后连接状态由我们掌控）。
func TestSmugglingEmbeddedRequestNeverParsed(t *testing.T) {
	sv := startServer(t, defaultTestLimits())
	c := sv.dial(t)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	embedded := "GET /admin HTTP/1.1\r\nHost: evil\r\n\r\n"
	body := "PREFIX-" + embedded
	wire := "POST /records HTTP/1.1\r\nHost: x\r\nContent-Length: " +
		itoa(len(body)) + "\r\n\r\n" + body

	if _, err := c.Write([]byte(wire)); err != nil {
		t.Fatal(err)
	}
	// 第一响应：201（整个 embedded 串只是 body 的一部分）。
	br := bufio.NewReader(c)
	resp1 := mustReadOneResponse(t, br)
	if !bytes.Contains(resp1, []byte(" 201 ")) {
		t.Fatalf("first response must be 201, got:\n%s", resp1)
	}
	// 校验存储的 body 与发送的完全一致（内嵌请求被逐字节吞入记录）。
	if !bytes.Contains(resp1, []byte(`"size":`+itoa(len(body)))) {
		t.Fatalf("stored size mismatch:\n%s", resp1)
	}

	// 此时连接上不应有任何“第二响应”自发到来（伪造 GET 没被处理）。
	_ = c.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	// 但合法的第二请求仍应正常工作（边界精确，连接可复用）。
	if _, err := c.Write([]byte("GET /healthz HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	resp2 := mustReadOneResponse(t, br)
	if !bytes.Contains(resp2, []byte(" 200 ")) || !bytes.Contains(resp2, []byte("ok")) {
		t.Fatalf("legitimate second request must still work, got:\n%s", resp2)
	}
}

// TestTECLSmuggleClosesConnection 验证 TE+CL 同时出现返回 400 并关闭，
// 且紧随其后的“走私”请求永远不会被读取处理。
func TestTECLSmuggleClosesConnection(t *testing.T) {
	sv := startServer(t, defaultTestLimits())
	c := sv.dial(t)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	attack := "POST /records HTTP/1.1\r\nHost: x\r\n" +
		"Content-Length: 6\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"0\r\n\r\nGET /admin HTTP/1.1\r\nHost: x\r\n\r\n"
	if _, err := c.Write([]byte(attack)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp := mustReadOneResponse(t, br)
	if !bytes.Contains(resp, []byte(" 400 ")) {
		t.Fatalf("TE+CL conflict must be 400, got:\n%s", resp)
	}
	if !bytes.Contains(bytes.ToLower(resp), []byte("connection: close")) {
		t.Fatalf("400 must carry Connection: close:\n%s", resp)
	}
	// 连接随后必须关闭，走私的 GET 无任何响应。
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	if _, err := br.Read(buf); err == nil {
		t.Fatalf("connection should be closed after smuggling attempt")
	}
}

// TestBadRequestClosesWithoutConsumingNext 坏请求关闭连接，且错误响应里
// 不能携带/泄露后续字节。
func TestBadRequestClosesWithoutConsumingNext(t *testing.T) {
	sv := startServer(t, defaultTestLimits())
	c := sv.dial(t)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	// 非法裸 LF + 伪造后续请求。
	attack := "GET / HTTP/1.1\nHost: x\r\n\r\nGET /admin HTTP/1.1\r\nHost: x\r\n\r\n"
	if _, err := c.Write([]byte(attack)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp := mustReadOneResponse(t, br)
	if !bytes.Contains(resp, []byte(" 400 ")) {
		t.Fatalf("bare LF must be 400, got:\n%s", resp)
	}
	if bytes.Contains(resp, []byte("/admin")) {
		t.Fatalf("error response must not echo smuggle payload")
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := br.Read(make([]byte, 16)); err == nil {
		t.Fatal("connection must close after bare LF")
	}
}

// TestErrorLogCarriesByteOffset 验证非法请求的日志含 run/conn/req/kind/
// phase/offset，可按字节偏移定位。
func TestErrorLogCarriesByteOffset(t *testing.T) {
	fixtures, data, err := LoadFixtures()
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	for _, f := range fixtures {
		if f.ID == "bad-bare-lf-requestline" {
			payload = data[f.ID]
		}
	}
	if payload == nil {
		t.Fatal("fixture not found")
	}
	sv := startServer(t, defaultTestLimits())
	c := sv.dial(t)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write(payload)
	_ = c.(interface{ CloseWrite() error }).CloseWrite()
	br := bufio.NewReader(c)
	_ = mustReadOneResponse(t, br)

	// 等待日志落盘。
	deadline := time.Now().Add(2 * time.Second)
	var log string
	for time.Now().Before(deadline) {
		log = sv.logBuf.String()
		if strings.Contains(log, "offset=") && strings.Contains(log, "invalid_request") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, want := range []string{
		"run=run-e2e-0001", "conn=c", "req=", "kind=invalid_request",
		"phase=request-line", "offset=14", "bare LF",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}

// TestChunkedBodyRoundTripSHA256 用较大 chunked 体（多块+扩展+trailer）
// 验证解码内容逐字节等于原始输入（按 sha256 对账）。
func TestChunkedBodyRoundTripSHA256(t *testing.T) {
	sv := startServer(t, defaultTestLimits())
	c := sv.dial(t)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	payload := bytes.Repeat([]byte("h1parse-chunk-boundary-"), 200) // ~4400B
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])

	var wire bytes.Buffer
	wire.WriteString("POST /records HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n")
	for off := 0; off < len(payload); {
		n := 100
		if off+n > len(payload) {
			n = len(payload) - off
		}
		chunk := payload[off : off+n]
		wire.WriteString(itoaHex(n) + ";seq=" + itoa(off/n) + "\r\n")
		wire.Write(chunk)
		wire.WriteString("\r\n")
		off += n
	}
	wire.WriteString("0\r\nETag: \"e2e\"\r\n\r\n")

	if _, err := c.Write(wire.Bytes()); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp := mustReadOneResponse(t, br)
	if !bytes.Contains(resp, []byte(" 201 ")) {
		t.Fatalf("chunked POST expected 201:\n%s", resp)
	}
	if !bytes.Contains(resp, []byte(wantSHA)) {
		t.Fatalf("stored sha256 mismatch; want %s in response", wantSHA)
	}
}

// ---- helpers ----

func mustReadOneResponse(t *testing.T, br *bufio.Reader) []byte {
	t.Helper()
	bc := &bufferedConn{br: br}
	return readResponseBR(t, bc)
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

func itoaHex(n int) string {
	const h = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = h[n&0xf]
		n >>= 4
	}
	return string(b[i:])
}

var _ = io.EOF
var _ net.Conn
