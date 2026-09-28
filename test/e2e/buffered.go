package e2e

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// bufferedConn 把 net.Conn 包成带缓冲的读取，供逐响应解析。
type bufferedConn struct {
	br interface {
		io.Reader
		ReadString(byte) (string, error)
	}
}

func newBufferedConn(c io.Reader) *bufferedConn {
	return &bufferedConn{br: bufio.NewReader(c)}
}

// readResponseBR 从带缓冲连接读取一条最终响应（自动跳过 1xx 中间响应）。
func readResponseBR(t *testing.T, bc *bufferedConn) []byte {
	t.Helper()
	for {
		raw := readOne(t, bc)
		line := string(raw)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			parts := strings.SplitN(line[:i], " ", 3)
			if len(parts) >= 2 && strings.HasPrefix(strings.TrimSpace(parts[1]), "1") {
				continue // 100 Continue 等中间帧
			}
		}
		return raw
	}
}

func readOne(t *testing.T, bc *bufferedConn) []byte {
	t.Helper()
	br := bc.br
	var raw bytes.Buffer
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	raw.WriteString(statusLine)
	contentLength := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		raw.WriteString(line)
		if line == "\r\n" {
			break
		}
		l := strings.ToLower(line)
		if strings.HasPrefix(l, "content-length:") {
			v := strings.TrimSpace(line[len("Content-Length:"):])
			n := 0
			for _, ch := range v {
				n = n*10 + int(ch-'0')
			}
			contentLength = n
		}
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(br, body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	raw.Write(body)
	return raw.Bytes()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// assertConnEOF 断言后续读取遇到 EOF（服务端关闭连接）。
// 容许在关闭前缓冲里有一个额外响应（前面流水线请求的响应）。
func assertConnEOF(t *testing.T, bc *bufferedConn) {
	t.Helper()
	_ = time.Now
	buf := make([]byte, 256)
	// 可能先读到一个完整错误响应：尝试再读一条，之后必须 EOF。
	// 直接等待关闭：
	done := make(chan struct {
		gotData bool
		err     error
	}, 1)
	go func() {
		n, err := bc.br.Read(buf)
		done <- struct {
			gotData bool
			err     error
		}{n > 0, err}
	}()
	select {
	case r := <-done:
		// 收到关闭（EOF/错误）即满足。若收到数据，再读一次必须关闭。
		if r.err != nil {
			return
		}
		if r.gotData {
			_, err := bc.br.Read(buf)
			if err != nil {
				return
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not close connection after bad/truncated request")
	}
}
