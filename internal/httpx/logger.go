package httpx

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// JSONLogger emits one JSON object per line, suitable for both local
// debugging and machine filtering. Parse failures always carry kind,
// phase and the absolute byte offset, so a bad request can be located
// precisely in a captured byte stream.
type JSONLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLogger constructs a logger writing to w (os.Stderr in the
// server).
func NewJSONLogger(w io.Writer) *JSONLogger {
	if w == nil {
		w = os.Stderr
	}
	return &JSONLogger{w: w}
}

func (l *JSONLogger) emit(level string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["level"] = level
	b, _ := json.Marshal(fields)
	b = append(b, '\n')
	l.w.Write(b)
}

// ParseError logs a parser failure with full byte context.
func (l *JSONLogger) ParseError(connID string, seq int, e *Error) {
	l.emit("error", map[string]any{
		"component": "httpx",
		"event":     "parse_error",
		"conn":      connID,
		"seq":       seq,
		"kind":      string(e.Kind),
		"phase":     string(e.Phase),
		"offset":    e.Offset,
		"code":      e.Code,
		"basis":     e.Basis,
		"message":   e.Message,
		"cause":     causeString(e.Cause),
	})
}

// ConnEvent logs connection lifecycle events.
func (l *JSONLogger) ConnEvent(connID, event string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["component"] = "httpx"
	fields["event"] = event
	fields["conn"] = connID
	level := "info"
	if event == "io_error" {
		level = "error"
	}
	l.emit(level, fields)
}

// RequestEvent logs per-request milestones.
func (l *JSONLogger) RequestEvent(connID string, seq int, event string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["component"] = "httpx"
	fields["event"] = event
	fields["conn"] = connID
	fields["seq"] = seq
	l.emit("info", fields)
}

func causeString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// NopLogger discards everything (used by benchmarks / focused tests).
type NopLogger struct{}

// ParseError implements Logger.
func (NopLogger) ParseError(string, int, *Error) {}

// ConnEvent implements Logger.
func (NopLogger) ConnEvent(string, string, map[string]any) {}

// RequestEvent implements Logger.
func (NopLogger) RequestEvent(string, int, string, map[string]any) {}
