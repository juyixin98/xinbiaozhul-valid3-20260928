// Command smokeclient 用裸 TCP 发送若干原始 HTTP/1.1 字节串，验证
// 服务端在真实连接上的行为。供 scripts/verify.sh 调用。
//
// 用法：smokeclient -addr 127.0.0.1:8080
package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "server address")
	flag.Parse()
	if err := run(*addr); err != nil {
		fmt.Fprintln(os.Stderr, "smokeclient:", err)
		os.Exit(1)
	}
}

func run(addr string) error {
	// 1) 持久连接 + 流水线：POST 固定体后紧跟 GET。
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	pipe := "POST /records HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nhello" +
		"GET /healthz HTTP/1.1\r\nHost: x\r\n\r\n"
	if _, err := c.Write([]byte(pipe)); err != nil {
		return err
	}
	br := bufio.NewReader(c)
	resp1 := readResp(br)
	resp2 := readResp(br)
	fmt.Println("--- pipelined response 1 (expect 201) ---")
	fmt.Print(statusLine(resp1))
	fmt.Println("--- pipelined response 2 (expect 200) ---")
	fmt.Print(statusLine(resp2))
	if !strings.Contains(statusLine(resp1), " 201 ") ||
		!strings.Contains(statusLine(resp2), " 200 ") {
		return fmt.Errorf("pipeline status mismatch")
	}

	// 2) chunked 请求（含分块扩展 + trailer）。
	c2, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer c2.Close()
	chunked := "POST /records HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n6;name=smoke\r\n world\r\n0\r\nETag: x\r\n\r\n"
	if _, err := c2.Write([]byte(chunked)); err != nil {
		return err
	}
	r := readResp(bufio.NewReader(c2))
	fmt.Println("--- chunked response (expect 201) ---")
	fmt.Print(statusLine(r))
	if !strings.Contains(statusLine(r), " 201 ") {
		return fmt.Errorf("chunked status mismatch")
	}

	// 3) TE+CL 冲突必须 400 且随后关闭连接。
	c3, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer c3.Close()
	attack := "POST /records HTTP/1.1\r\nHost: x\r\n" +
		"Content-Length: 6\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"
	if _, err := c3.Write([]byte(attack)); err != nil {
		return err
	}
	br3 := bufio.NewReader(c3)
	r3 := readResp(br3)
	fmt.Println("--- TE+CL conflict (expect 400 + close) ---")
	fmt.Print(statusLine(r3))
	if !strings.Contains(statusLine(r3), " 400 ") {
		return fmt.Errorf("expected 400")
	}
	_ = c3.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	if _, err := br3.Read(buf); err == nil {
		return fmt.Errorf("connection not closed after smuggling attempt")
	}
	fmt.Println("smokeclient: OK")
	return nil
}

func readResp(br *bufio.Reader) string {
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		sb.WriteString(line)
		if err != nil || line == "\r\n" {
			break
		}
	}
	// 读 Content-Length 主体。
	text := sb.String()
	n := 0
	for _, l := range strings.Split(text, "\r\n") {
		if strings.HasPrefix(strings.ToLower(l), "content-length:") {
			v := strings.TrimSpace(l[len("content-length:"):])
			for _, ch := range v {
				n = n*10 + int(ch-'0')
			}
		}
	}
	body := make([]byte, n)
	_, _ = readFull(br, body)
	sb.Write(body)
	return sb.String()
}

func readFull(br *bufio.Reader, p []byte) (int, error) {
	got := 0
	for got < len(p) {
		n, err := br.Read(p[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func statusLine(resp string) string {
	if i := strings.IndexByte(resp, '\n'); i >= 0 {
		return resp[:i+1]
	}
	return resp
}
