package e2e

import (
	"bytes"
	"strings"
	"sync"
)

// lineBuffer 是线程安全的内存日志汇，供断言“日志含偏移/阶段/run/conn/req”。
type lineBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLineBuffer() *lineBuffer { return &lineBuffer{} }

func (l *lineBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lineBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// containsAll 报告日志是否包含全部子串。
func (l *lineBuffer) containsAll(wants ...string) []string {
	got := l.String()
	var missing []string
	for _, w := range wants {
		if !strings.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	return missing
}
