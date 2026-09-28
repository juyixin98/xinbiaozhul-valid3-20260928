// Package diag provides structured logging for request/record/run tracing.
// Every log line carries the broker run id and, when applicable, a connection
// id and MQTT packet identifier, so protocol decisions can be reconstructed
// from logs.
package diag

import (
	"io"
	"log/slog"
	"os"
	"sync/atomic"
)

// Logger wraps slog with stable key names used across the project.
type Logger struct {
	sl   *slog.Logger
	run  string
	conn atomic.Uint64
}

// New creates a Logger writing to w. runID identifies this broker run and is
// attached to every record.
func New(w io.Writer, runID string, level slog.Level) *Logger {
	if w == nil {
		w = os.Stderr
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return &Logger{sl: slog.New(h).With("run", runID), run: runID}
}

// RunID returns the broker run identifier.
func (l *Logger) RunID() string { return l.run }

// NextConnID hands out a per-run monotonic connection number.
func (l *Logger) NextConnID() uint64 { return l.conn.Add(1) }

func (l *Logger) attrs(kind, stage string, connID uint64, extra []any) []any {
	attrs := make([]any, 0, 6+len(extra))
	attrs = append(attrs, "kind", kind)
	if stage != "" {
		attrs = append(attrs, "stage", stage)
	}
	if connID != 0 {
		attrs = append(attrs, "conn", connID)
	}
	attrs = append(attrs, extra...)
	return attrs
}

// Event logs a state-machine / lifecycle event.
func (l *Logger) Event(stage string, connID uint64, kv ...any) {
	l.sl.Log(nil, slog.LevelInfo, "event", l.attrs("event", stage, connID, kv)...)
}

// Decision logs a protocol decision (accept/reject and the rule applied).
func (l *Logger) Decision(stage string, connID uint64, kv ...any) {
	l.sl.Log(nil, slog.LevelInfo, "decision", l.attrs("decision", stage, connID, kv)...)
}

// Reject logs a refused input with the governing spec rule.
func (l *Logger) Reject(stage string, connID uint64, kv ...any) {
	l.sl.Log(nil, slog.LevelWarn, "reject", l.attrs("reject", stage, connID, kv)...)
}

// Failure logs an operational failure distinct from protocol rejection.
func (l *Logger) Failure(stage string, connID uint64, err error, kv ...any) {
	args := l.attrs("failure", stage, connID, kv)
	if err != nil {
		args = append(args, "err", err.Error())
	}
	l.sl.Log(nil, slog.LevelError, "failure", args...)
}

// Discard returns a no-op logger for tests that do not inspect logs.
func Discard() *Logger {
	return New(io.Discard, "test", slog.LevelError)
}
