package testutil_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"http11subset/internal/httpx"
	"http11subset/internal/refparse"
	"http11subset/test/testutil"
)

// Default limits mirrored from httpx.DefaultLimits (tests use the
// production defaults, plus a tiny-limit case elsewhere).
func testLimits() httpx.Limits {
	return httpx.Limits{
		MaxHeaderBytes:    64 * 1024,
		MaxBodyBytes:      1 << 20,
		MaxChunkLineBytes: 8 * 1024,
	}
}

var deliveryModes = []string{"all", "byte", "pattern", "chunk:7"}

// asString pulls a string field from the generic fixture map.
func asString(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func asInt64(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// runOneParse drives the production parser over one wire input in the
// given delivery mode and returns the request, the terminal error and
// the absolute body-end offset. parseCount > 1 fully consumes that
// many preceding messages before asserting the outcome of the final
// one (used for pipelined-then-truncated input).
func runOneParse(t *testing.T, wire, mode string, parseCount int) (*httpx.Request, *httpx.Error, int64) {
	t.Helper()
	r := testutil.NewDripReader([]byte(wire), mode)
	p := httpx.NewParser(r, testLimits(), 64)
	var req *httpx.Request
	for i := 0; i < parseCount; i++ {
		var err error
		req, err = p.ReadHead()
		if err != nil {
			if pe, ok := httpx.AsError(err); ok {
				return req, pe, p.Offset()
			}
			t.Fatalf("non-httpx error from ReadHead (msg %d): %v", i+1, err)
		}
		if berr := p.ReadBody(req); berr != nil {
			if pe, ok := httpx.AsError(berr); ok {
				return req, pe, p.Offset()
			}
			t.Fatalf("non-httpx error from ReadBody (msg %d): %v", i+1, berr)
		}
	}
	return req, nil, req.BodyEndOffset
}

// TestFixtureDrivenParser runs every byte-level fixture under every
// delivery pattern. Half packets ("半包") and coalesced pipelined
// bytes ("粘包") must not change the decision, kind, phase or consumed
// boundary.
func TestFixtureDrivenParser(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	fixtures := testutil.LoadFixtures(t, "testdata/fixtures.json")

	for _, fx := range fixtures {
		id := asString(fx, "id")
		wire := asString(fx, "wire")
		t.Run(id, func(t *testing.T) {
			for _, mode := range deliveryModes {
				t.Run(mode, func(t *testing.T) {
					count := int(asInt64(fx, "parse_count"))
					if count == 0 {
						count = 1
					}
					seq := 0
					req, pe, endOff := runOneParse(t, wire, mode, count)
					seq++

					if asString(fx, "want_reject") == "true" || fx["want_reject"] == true {
						if pe == nil {
							log.Case(id, seq, "parse", "UNEXPECTED_SUCCESS", asString(fx, "category"), nil,
								map[string]any{"mode": mode})
							t.Fatalf("fixture %s (%s): expected rejection %s/%s but parser accepted",
								id, mode, asString(fx, "kind"), asString(fx, "phase"))
						}
						assertReject(t, log, id, seq, mode, fx, pe)
						return
					}

					// Accepted path.
					if pe != nil {
						log.Case(id, seq, "parse", "UNEXPECTED_REJECT", asString(fx, "legal_ref"), pe,
							map[string]any{"mode": mode})
						t.Fatalf("fixture %s (%s): expected valid request, got %v", id, mode, pe)
					}
					assertAccepted(t, log, id, seq, mode, fx, req, endOff)

					// Independent reference cross-check (single full
					// decode; reference is segmentation independent).
					if mode == "all" {
						crossCheckReference(t, log, id, wire, req, endOff)
					}
				})
			}
		})
	}
}

func assertReject(t *testing.T, log *testutil.RunLogger, id string, seq int, mode string, fx map[string]any, pe *httpx.Error) {
	t.Helper()
	wantKind := asString(fx, "kind")
	wantPhase := asString(fx, "phase")
	wantStatus := asInt64(fx, "want_http_status")
	wantOffset := asInt64(fx, "want_offset")

	if string(pe.Kind) != wantKind {
		log.Case(id, seq, "classify", "KIND_MISMATCH", "fixture expects "+wantKind, pe,
			map[string]any{"mode": mode, "want": wantKind})
		t.Fatalf("%s (%s): kind=%s want %s", id, mode, pe.Kind, wantKind)
	}
	if string(pe.Phase) != wantPhase {
		log.Case(id, seq, "locate", "PHASE_MISMATCH", "fixture expects phase "+wantPhase, pe,
			map[string]any{"mode": mode, "want_phase": wantPhase})
		t.Fatalf("%s (%s): phase=%s want %s", id, mode, pe.Phase, wantPhase)
	}
	if int64(pe.Code) != wantStatus {
		log.Case(id, seq, "respond", "STATUS_MISMATCH", "fixture expects status", pe,
			map[string]any{"mode": mode, "want_status": wantStatus})
		t.Fatalf("%s (%s): status=%d want %d", id, mode, pe.Code, wantStatus)
	}
	if wantOffset >= 0 && pe.Offset != wantOffset {
		log.Case(id, seq, "locate", "OFFSET_MISMATCH", "fixture pins exact byte", pe,
			map[string]any{"mode": mode, "want_offset": wantOffset})
		t.Fatalf("%s (%s): offset=%d want %d", id, mode, pe.Offset, wantOffset)
	}
	log.Case(id, seq, "reject", "REJECTED_AS_EXPECTED",
		"kind/phase/status"+offsetBasis(wantOffset), pe,
		map[string]any{"mode": mode, "want": wantKind, "want_status": wantStatus})
}

func offsetBasis(want int64) string {
	if want >= 0 {
		return " and exact byte offset " + strconv.FormatInt(want, 10)
	}
	return ""
}

func assertAccepted(t *testing.T, log *testutil.RunLogger, id string, seq int, mode string, fx map[string]any, req *httpx.Request, endOff int64) {
	t.Helper()
	wire := asString(fx, "wire")
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"method", req.Method, asString(fx, "method")},
		{"target", req.Target, asString(fx, "target")},
		{"version", req.Version, defaultStr(asString(fx, "version"), "HTTP/1.1")},
	}
	for _, c := range checks {
		if c.want != "" && c.got != c.want {
			t.Fatalf("%s (%s): %s=%q want %q", id, mode, c.name, c.got, c.want)
		}
	}
	switch asString(fx, "frame") {
	case "none":
		if req.Frame != httpx.FrameNone {
			t.Fatalf("%s (%s): frame=%v want none", id, mode, req.Frame)
		}
	case "fixed":
		if req.Frame != httpx.FrameFixed || req.ContentLength != asInt64(fx, "length") {
			t.Fatalf("%s (%s): frame=%v cl=%d want fixed %d", id, mode, req.Frame, req.ContentLength, asInt64(fx, "length"))
		}
	case "chunked":
		if req.Frame != httpx.FrameChunked {
			t.Fatalf("%s (%s): frame=%v want chunked", id, mode, req.Frame)
		}
	}
	if wantBody := asString(fx, "body"); fx["body"] != nil && string(req.Body) != wantBody {
		t.Fatalf("%s (%s): body=%q want %q", id, mode, string(req.Body), wantBody)
	}
	// Exact consumption: residual bytes must equal the expected
	// remainder — proving no body byte leaks into the next request.
	remainder := wire[endOff:]
	if wantRem := asString(fx, "remainder"); remainder != wantRem {
		t.Fatalf("%s (%s): remainder=%q want %q (boundary off by %d)",
			id, mode, remainder, wantRem, len(remainder)-len(wantRem))
	}
	log.Case(id, seq, "accept", "ACCEPTED_AS_EXPECTED", asString(fx, "legal_ref"), nil, map[string]any{
		"mode":          mode,
		"consumed":      endOff,
		"remainder_len": len(remainder),
		"frame":         asString(fx, "frame"),
	})
}

// crossCheckReference decodes the same bytes with the independent
// reference implementation and requires it to agree on method, target,
// version, body and the exact number of consumed bytes. The reference
// is a second implementation, not expected values copied from the
// parser under test.
func crossCheckReference(t *testing.T, log *testutil.RunLogger, id, wire string, req *httpx.Request, endOff int64) {
	t.Helper()
	l := testLimits()
	ref, rem, err := refparse.Parse([]byte(wire), l.MaxHeaderBytes, l.MaxBodyBytes, l.MaxChunkLineBytes)
	if err != nil {
		t.Fatalf("%s: reference decoder rejected a fixture the parser accepted: %v", id, err)
	}
	if ref.Method != req.Method || ref.Target != req.Target || ref.Version != req.Version {
		t.Fatalf("%s: ref request line %s %s %s != parser %s %s %s",
			id, ref.Method, ref.Target, ref.Version, req.Method, req.Target, req.Version)
	}
	if !bytes.Equal(ref.Body, req.Body) {
		t.Fatalf("%s: ref body %q != parser body %q", id, ref.Body, req.Body)
	}
	if ref.Consumed != int(endOff) {
		t.Fatalf("%s: ref consumed %d != parser consumed %d", id, ref.Consumed, endOff)
	}
	if len(rem) != len(wire)-int(endOff) {
		t.Fatalf("%s: ref remainder len %d != parser residual %d", id, len(rem), len(wire)-int(endOff))
	}
	log.Step("cross_check", "REFERENCE_AGREES", "independent refparse decoder matches method/body/consumed",
		map[string]any{"case_id": id, "consumed": endOff})
}

func defaultStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// TestPipelineTwoParses proves the parser instance itself advances
// from one complete request straight into the next pipelined one.
func TestPipelineTwoParses(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	wire := "GET /a HTTP/1.1\r\nHost: x\r\n\r\n" +
		"POST /b HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\n12345" +
		"GET /c HTTP/1.1\r\nHost: x\r\n\r\n"
	p := httpx.NewParser(strings.NewReader(wire), testLimits(), 32)

	want := []struct {
		method, target string
		body           string
	}{
		{"GET", "/a", ""},
		{"POST", "/b", "12345"},
		{"GET", "/c", ""},
	}
	for i, w := range want {
		req, err := p.ReadHead()
		if err != nil {
			t.Fatalf("request %d head: %v", i+1, err)
		}
		if err := p.ReadBody(req); err != nil {
			t.Fatalf("request %d body: %v", i+1, err)
		}
		if req.Method != w.method || req.Target != w.target || string(req.Body) != w.body {
			t.Fatalf("request %d = %s %s body=%q", i+1, req.Method, req.Target, req.Body)
		}
		log.Case("pipeline", i+1, "sequence", "MESSAGE_BOUNDARY_OK",
			"stream cursor at next message after ReadBody", nil,
			map[string]any{"target": req.Target, "offset": req.BodyEndOffset})
	}
}
