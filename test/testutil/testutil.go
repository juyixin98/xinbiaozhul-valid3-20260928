// Package testutil holds shared, test-only helpers: a structured run
// logger, a deterministic "drip" reader that proves half-packet /
// sticky-packet handling, and fixture loading.
package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"http11subset/internal/httpx"
)

// RunID uniquely identifies a test process invocation; it is stamped
// into every assertion line and the JSONL trace so a run can be
// correlated in CI logs.
var RunID = fmt.Sprintf("run-%d-%d", time.Now().UTC().UnixMicro(), os.Getpid())

// Version stamps logs with the parser version under test.
const Version = httpx.ImplementationVersion

// Step is the structured record emitted for every test stage.
type Step struct {
	RunID      string         `json:"run_id"`
	Version    string         `json:"version"`
	Test       string         `json:"test"`
	CaseID     string         `json:"case_id,omitempty"`
	Seq        int            `json:"seq,omitempty"`
	Stage      string         `json:"stage"`
	Step       int            `json:"step,omitempty"`
	Decision   string         `json:"decision"`
	Basis      string         `json:"basis,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Phase      string         `json:"phase,omitempty"`
	Offset     int64          `json:"offset,omitempty"`
	WantOffset int64          `json:"want_offset,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
	Err        string         `json:"err,omitempty"`
}

// RunLogger writes a human-readable line to test output and an
// append-only JSONL trace file (test/out/runlog.jsonl). It records the
// request/case, run identifier, version, stage, sequence number,
// calculation step and the basis for each decision.
type RunLogger struct {
	t     *testing.T
	mu    sync.Mutex
	f     *os.File
	seq   atomic.Int64
	steps int
}

var truncOnce sync.Once

// NewRunLogger opens test/out/runlog.jsonl, truncating it once per
// process; subsequent test functions append their steps.
func NewRunLogger(t *testing.T) *RunLogger {
	t.Helper()
	dir := filepath.Join("..", "test", "out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir out: %v", err)
	}
	path := filepath.Join(dir, "runlog.jsonl")
	truncOnce.Do(func() {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("truncate runlog: %v", err)
		}
	})
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open runlog: %v", err)
	}
	l := &RunLogger{t: t, f: f}
	l.Step("harness", "run_start", "test run begins", map[string]any{
		"run_id":  RunID,
		"version": Version,
	})
	return l
}

// Step records one decision step.
func (l *RunLogger) Step(stage, decision, basis string, detail map[string]any) *Step {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps++
	s := Step{
		RunID:    RunID,
		Version:  Version,
		Test:     l.t.Name(),
		Stage:    stage,
		Step:     l.steps,
		Decision: decision,
		Basis:    basis,
		Detail:   detail,
		Seq:      int(l.seq.Add(1)),
	}
	if detail != nil {
		if v, ok := detail["case_id"].(string); ok {
			s.CaseID = v
		}
		if v, ok := detail["kind"].(string); ok {
			s.Kind = v
		}
		if v, ok := detail["phase"].(string); ok {
			s.Phase = v
		}
		if v, ok := detail["offset"].(int64); ok {
			s.Offset = v
		}
		if v, ok := detail["want_offset"].(int64); ok {
			s.WantOffset = v
		}
		if v, ok := detail["err"].(string); ok {
			s.Err = v
		}
	}
	b, _ := json.Marshal(s)
	if l.f != nil {
		l.f.Write(append(b, '\n'))
	}
	l.t.Logf("[%s %s] %s step=%d %s %s", RunID, Version, stage, l.steps, decision, basis)
	return &s
}

// Case records a fixture-level decision.
func (l *RunLogger) Case(caseID string, seq int, stage, decision, basis string, pe *httpx.Error, extra map[string]any) {
	d := map[string]any{"case_id": caseID, "seq": seq}
	if pe != nil {
		d["kind"] = string(pe.Kind)
		d["phase"] = string(pe.Phase)
		d["offset"] = pe.Offset
		d["code"] = pe.Code
		d["err"] = pe.Error()
	}
	for k, v := range extra {
		d[k] = v
	}
	l.Step(stage, decision, basis, d)
}

// Close finalizes the trace.
func (l *RunLogger) Close() {
	if l.f != nil {
		l.f.Close()
	}
}

// DripReader feeds bytes in a deterministic split pattern so tests
// exercise half packets (one byte at a time), fixed chunks and random
// boundaries. It never coalesces beyond what the pattern dictates; the
// parser must therefore succeed regardless of TCP segmentation.
type DripReader struct {
	data []byte
	pos  int
	// mode: "byte" => 1 byte per Read; "chunk:N" => N bytes;
	// "pattern" => 1,2,3,1,2,3...; "all" => whole slice.
	mode   string
	n      int
	cyc    int
	readID int
}

// NewDripReader constructs a reader over data.
func NewDripReader(data []byte, mode string) *DripReader {
	d := &DripReader{data: data, mode: mode}
	if n := scanN(mode); n > 0 {
		d.n = n
		d.mode = "chunk"
	}
	return d
}

func scanN(mode string) int {
	var n int
	if _, err := fmt.Sscanf(mode, "chunk:%d", &n); err == nil {
		return n
	}
	return 0
}

// Read implements io.Reader.
func (d *DripReader) Read(p []byte) (int, error) {
	if d.pos >= len(d.data) {
		return 0, io.EOF
	}
	d.readID++
	var take int
	switch d.mode {
	case "byte":
		take = 1
	case "chunk":
		take = d.n
	case "all":
		take = len(d.data) - d.pos
	default: // pattern
		take = d.cyc%3 + 1
		d.cyc++
	}
	if take <= 0 {
		take = 1
	}
	if take > len(p) {
		take = len(p)
	}
	if take > len(d.data)-d.pos {
		take = len(d.data) - d.pos
	}
	copy(p, d.data[d.pos:d.pos+take])
	d.pos += take
	return take, nil
}

// LoadFixtures reads a generated JSON fixture file into a generic
// structure.
func LoadFixtures(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixtures %s: %v", path, err)
	}
	var doc struct {
		Fixtures []map[string]any `json:"fixtures"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	return doc.Fixtures
}
