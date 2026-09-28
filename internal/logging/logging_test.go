package logging_test

import (
	"strings"
	"testing"

	"h1parse/internal/logging"
)

// TestFailureCategoriesDistinct 验证四类失败在输出上可区分：
// 非法输入/状态冲突用 warn，资源超限也用 warn 但 kind 不同；
// 运行失败用 error。run/stage 标识齐全。
func TestFailureCategoriesDistinct(t *testing.T) {
	var sb strings.Builder
	log := logging.New(&sb, "run-cat-1", false)

	log.Warn("framing", "rejected", logging.Fields{"kind": "invalid_request"})
	log.Warn("framing", "rejected", logging.Fields{"kind": "header_too_large"})
	log.Warn("framing", "rejected", logging.Fields{"kind": "payload_too_large"})
	log.Error("storage", "failure", logging.Fields{"error": "disk full"})

	out := sb.String()
	for _, want := range []string{
		" warn ", " error ", "run=run-cat-1",
		"kind=invalid_request", "kind=header_too_large", "kind=payload_too_large",
		"error=disk full", "stage=framing", "stage=storage",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output missing %q:\n%s", want, out)
		}
	}
}

// TestJSONMode 验证 JSON Lines 模式字段可解析。
func TestJSONMode(t *testing.T) {
	var sb strings.Builder
	log := logging.New(&sb, "run-json-1", true)
	log.Info("accept", "listening", logging.Fields{"addr": "127.0.0.1:0"})
	line := strings.TrimSpace(sb.String())
	if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"run":"run-json-1"`) {
		t.Fatalf("not json line: %s", line)
	}
}
