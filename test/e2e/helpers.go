package e2e

import (
	"net"
	"testing"
	"time"

	"h1parse/internal/protocol"
)

// sendSlow 按每字节间隔发送，验证半包。
func sendSlow(t *testing.T, c net.Conn, data []byte, delay time.Duration) {
	t.Helper()
	for i := range data {
		if _, err := c.Write(data[i : i+1]); err != nil {
			t.Fatalf("slow write at %d: %v", i, err)
		}
		time.Sleep(delay)
	}
}

// defaultTestLimits 给测试用的较宽限制（个别超限夹具用例自行覆盖）。
func defaultTestLimits() protocol.Limits {
	return protocol.Limits{
		MaxHeaderBytes: 1 << 16,
		MaxBodyBytes:   1 << 20,
		MaxChunkSize:   1 << 18,
	}
}
