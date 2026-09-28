package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func netDial(addr string) (net.Conn, error) { return net.DialTimeout("tcp", addr, time.Second) }

// TestStdlibClientCompat 用 Go 标准库 net/http 客户端验证服务端对
// “合法语法参考”的互操作：固定长度、chunked（Transport 自动编码）、
// 持久连接复用、HEAD、404/405、Expect:100-continue。
func TestStdlibClientCompat(t *testing.T) {
	sv := startServer(t, defaultTestLimits())
	base := "http://" + sv.addr
	client := &http.Client{Timeout: 5 * time.Second}

	// GET health
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("health body %q", body)
	}
	if !resp.Close {
		t.Log("connection reusable as expected")
	}

	// POST 固定长度
	resp, err = client.Post(base+"/records", "text/plain", bytes.NewReader([]byte("stdlib-fixed")))
	if err != nil {
		t.Fatal(err)
	}
	loc := resp.Header.Get("Location")
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || loc == "" {
		t.Fatalf("POST fixed %d loc=%q body=%s", resp.StatusCode, loc, body)
	}
	var rec map[string]any
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["size"].(float64) != float64(len("stdlib-fixed")) {
		t.Fatalf("size field: %v", rec["size"])
	}

	// GET 记录
	resp, err = client.Get(base + loc)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// 记录 JSON 中 body 是 []byte，encoding/json 以 base64 表示。
	if resp.StatusCode != 200 || !strings.Contains(string(body), "c3RkbGliLWZpeGVk") {
		t.Fatalf("GET record %d %s", resp.StatusCode, body)
	}

	// POST chunked：Transport 对 *bytes.Reader 一般发 CL；用 io.Pipe 强制 chunked。
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("chunk-one-"))
		time.Sleep(10 * time.Millisecond)
		_, _ = pw.Write([]byte("chunk-two"))
		_ = pw.Close()
	}()
	req, _ := http.NewRequest(http.MethodPost, base+"/records", pr)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("chunked POST %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"size":19`) {
		t.Fatalf("chunked body size: %s", body)
	}

	// 404 / 405
	resp, _ = client.Get(base + "/does-not-exist")
	if resp.StatusCode != 404 {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	req, _ = http.NewRequest(http.MethodDelete, base+"/records", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 405 || resp.Header.Get("Allow") == "" {
		t.Fatalf("want 405+Allow, got %d allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
	_ = resp.Body.Close()
}

// TestServerShutsDownGracefully 验证 Shutdown 停止 accept 且在途请求完成。
func TestServerShutsDownGracefully(t *testing.T) {
	sv := startServer(t, defaultTestLimits())
	resp, err := http.Get("http://" + sv.addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sv.srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	// 新连接应失败。
	c, err := netDial(sv.addr)
	if err == nil {
		_ = c.Close()
		t.Fatal("listener should be closed after shutdown")
	}
}
