// Package logx provides the broker's structured diagnostic logging. Every line
// carries a run identifier, a monotonic sequence number, a stage and a
// disposition so that test output can be correlated with protocol decisions:
//
//	2026-09-28T10:00:00Z run=9f31 seq=000007 stage=connect conn=c-3 result=accept ...
//
// Dispositions use a controlled vocabulary:
//
//	accept | reject_invalid | reject_unsupported | reject_limit |
//	state_conflict | retry | drop | internal_error
//
// "Not converged" / uncertain outcomes are never logged as accept.
package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Version is reported on every broker startup line.
const Version = "mqttd-0.1.0-mqtt311-subset"

// Standard dispositions.
const (
	DispositionAccept            = "accept"
	DispositionRejectInvalid     = "reject_invalid"
	DispositionRejectUnsupported = "reject_unsupported"
	DispositionRejectLimit       = "reject_limit"
	DispositionStateConflict     = "state_conflict"
	DispositionRetry             = "retry"
	DispositionDrop              = "drop"
	DispositionInternalError     = "internal_error"
)

// Logger emits structured lines.
type Logger struct {
	mu    sync.Mutex
	w     io.Writer
	runID string
	seq   uint64
	now   func() time.Time
}

// New creates a logger bound to a run id.
func New(w io.Writer, runID string) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{w: w, runID: runID, now: time.Now}
}

// Default returns a stderr logger.
func Default(runID string) *Logger { return New(os.Stderr, runID) }

// Event writes one line. stage identifies the protocol phase; disposition is
// one of the Disposition* constants; kvs are additional key/value pairs.
func (l *Logger) Event(stage, disposition, msg string, kvs ...any) {
	seq := atomic.AddUint64(&l.seq, 1)
	var line []byte
	line = append(line, l.now().UTC().Format(time.RFC3339Nano)...)
	line = append(line, " run="...)
	line = append(line, l.runID...)
	line = fmt.Appendf(line, " seq=%06d", seq)
	line = append(line, " stage="...)
	line = append(line, stage...)
	if disposition != "" {
		line = append(line, " result="...)
		line = append(line, disposition...)
	}
	line = append(line, " msg=\""...)
	line = append(line, msg...)
	line = append(line, '"')
	for i := 0; i+1 < len(kvs); i += 2 {
		line = fmt.Appendf(line, " %v=%v", kvs[i], kvs[i+1])
	}
	line = append(line, '\n')
	l.mu.Lock()
	_, _ = l.w.Write(line)
	l.mu.Unlock()
}

// RunID returns the run identifier carried by every line.
func (l *Logger) RunID() string { return l.runID }
