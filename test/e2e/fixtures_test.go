package e2e

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestFixturesDriven 逐夹具驱动真实服务。
func TestFixturesDriven(t *testing.T) {
	fixtures, data, err := LoadFixtures()
	if err != nil {
		t.Fatal(err)
	}
	sv := startServer(t, defaultTestLimits())
	// 低限额服务：验证 413（单 chunk/体超限）。
	lowLimits := defaultTestLimits()
	lowLimits.MaxChunkSize = 128
	lowLimits.MaxBodyBytes = 1024
	svLow := startServer(t, lowLimits)

	for _, f := range fixtures {
		f := f
		t.Run(f.ID, func(t *testing.T) {
			server := sv
			if f.Group == "limits" {
				server = svLow
			}
			payload := data[f.ID]
			c := server.dial(t)
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))

			if _, err := c.Write(payload); err != nil {
				t.Fatalf("write: %v", err)
			}
			if f.Expected != "accept" {
				_ = c.(interface{ CloseWrite() error }).CloseWrite()
			}
			runFixtureExpectations(t, c, f)
		})
	}
}

// TestByteByByteDelivery 对代表性夹具逐字节喂入，结论必须与整块发送一致
// （半包透明）。
func TestByteByByteDelivery(t *testing.T) {
	fixtures, data, err := LoadFixtures()
	if err != nil {
		t.Fatal(err)
	}
	sv := startServer(t, defaultTestLimits())

	for _, f := range fixtures {
		f := f
		if !slowEligible(f.ID) {
			continue
		}
		t.Run("slow/"+f.ID, func(t *testing.T) {
			c := sv.dial(t)
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(15 * time.Second))
			// 拒绝类：服务端一看到违规字节就可能回 4xx 并关闭，导致后续
			// 慢速写入 broken pipe——这是正确行为，容忍；随后仍半关写端，
			// 读取错误响应并确认连接关闭。
			sendSlowAllowClose(t, c, data[f.ID], 300*time.Microsecond)
			if f.Expected != "accept" {
				_ = c.(interface{ CloseWrite() error }).CloseWrite()
			}
			runFixtureExpectations(t, c, f)
		})
	}
}

// sendSlowAllowClose 逐字节发送，忽略服务端提前关闭产生的写错误。
func sendSlowAllowClose(t *testing.T, c net.Conn, data []byte, delay time.Duration) {
	t.Helper()
	for i := range data {
		if _, err := c.Write(data[i : i+1]); err != nil {
			return // 服务端已关闭；后续断言验证错误响应与连接状态
		}
		time.Sleep(delay)
	}
}

func slowEligible(id string) bool {
	switch id {
	case "legal-get-basic", "legal-post-fixed", "legal-post-chunked-ext",
		"legal-pipeline-2", "legal-cl-body-swallow",
		"smuggle-te-cl-both", "smuggle-duplicate-cl-differ",
		"bad-bare-lf-requestline", "bad-chunk-bad-crlf",
		"trunc-fixed-body", "trunc-chunk-data", "bad-chunk-ext-empty-name",
		"bad-chunk-size-leading-space", "bad-trailing-cr-only":
		return true
	}
	return false
}

// runFixtureExpectations 根据 manifest 的 expected/accepts/status 断言。
func runFixtureExpectations(t *testing.T, c net.Conn, f Fixture) {
	t.Helper()
	br := newBufferedConn(c)
	switch f.Expected {
	case "accept":
		want := f.Accepts
		if want == 0 {
			want = 1
		}
		for i := 0; i < want; i++ {
			raw := readResponseBR(t, br)
			if i == 0 {
				if f.Status != 0 {
					assertStatus(t, raw, f.Status)
				} else {
					assertStatus2xxOrExpected(t, raw)
				}
			} else {
				// 流水线后续请求必须是 2xx，且顺序不乱。
				assertStatus2xxOrExpected(t, raw)
			}
		}
	case "reject":
		raw := readResponseBR(t, br)
		if f.Status != 0 {
			assertStatus(t, raw, f.Status)
		}
		assertConnectionHeader(t, raw, "close")
		// 之后连接应关闭。
		assertConnEOF(t, br)
	case "truncated":
		// 截断：服务端直接关闭（可能无响应，或先有前面请求的响应）。
		assertConnEOF(t, br)
	}
}

func assertStatus(t *testing.T, raw []byte, want int) {
	t.Helper()
	line := firstLine(raw)
	if !strings.Contains(line, " "+strconv.Itoa(want)+" ") {
		t.Fatalf("status: %s want %d\nraw: %s", line, want, raw)
	}
}

func assertStatus2xxOrExpected(t *testing.T, raw []byte) {
	t.Helper()
	line := firstLine(raw)
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		t.Fatalf("bad status line: %q", line)
	}
	code, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || code < 200 || code > 299 {
		t.Fatalf("pipelined request not 2xx: %q", line)
	}
}

func assertConnectionHeader(t *testing.T, raw []byte, want string) {
	t.Helper()
	lower := bytes.ToLower(raw)
	if !bytes.Contains(lower, []byte("connection: "+want)) {
		t.Fatalf("missing Connection: %s in\n%s", want, raw)
	}
}

func firstLine(raw []byte) string {
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		return string(bytes.TrimRight(raw[:i], "\r"))
	}
	return string(raw)
}
