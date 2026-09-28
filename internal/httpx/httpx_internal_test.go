package httpx

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// byteReader returns data one byte per Read to force maximum
// fragmentation (worst-case 半包).
type byteReader struct{ data []byte }

func (b *byteReader) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	p[0] = b.data[0]
	b.data = b.data[1:]
	return 1, nil
}

// TestStreamLineByteWise proves CRLF framing is independent of Read
// boundaries, including a CR/LF split across two reads.
func TestStreamLineByteWise(t *testing.T) {
	lines := []struct {
		wire string
		want string
	}{
		{"a\r\n", "a"},
		{"\r\n", ""},
		{"X\r\ntwo\r\n", "X"},
		{"split\r\nrest\r\n", "split"}, // CR and LF delivered in separate reads
	}
	for _, tc := range lines {
		r := &byteReader{data: []byte(tc.wire)}
		s := newStream(r, 4, 64)
		line, _, _, err := s.ReadLine(100)
		if err != nil {
			t.Fatalf("%q: %v", tc.wire, err)
		}
		if string(line) != tc.want {
			t.Fatalf("line=%q want %q", line, tc.want)
		}
	}
}

// TestStreamBareLFDetected verifies a bare LF that terminates a line
// (with no later CRLF in the buffer) is rejected with its exact
// offset. A bare LF followed later by a valid CRLF is instead read as
// field content and rejected by the field grammar; that path is
// covered by the byte fixtures.
func TestStreamBareLFDetected(t *testing.T) {
	s := newStream(&byteReader{data: []byte("abc\ndef")}, 4, 64)
	_, _, _, err := s.ReadLine(100)
	var bare *bareLFError
	if !errors.As(err, &bare) {
		t.Fatalf("want bareLFError, got %v", err)
	}
	if bare.Offset != 3 {
		t.Fatalf("offset=%d want 3", bare.Offset)
	}
}

// TestStreamAppendExact consumes an exact byte count and leaves the
// pipelined suffix intact.
func TestStreamAppendExact(t *testing.T) {
	s := newStream(strings.NewReader("12345NEXT"), 4, 64)
	var dst []byte
	dst, err := s.ReadAppend(dst, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(dst) != "12345" {
		t.Fatalf("dst=%q", dst)
	}
	rest, err := s.Peek()
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "NEXT" {
		t.Fatalf("residual=%q want NEXT", rest)
	}
}

// TestParseContentLength covers agreement, disagreement, malformed
// and overflowing values.
func TestParseContentLength(t *testing.T) {
	ok := func(vals []string, want int64) {
		t.Helper()
		got, err := parseContentLength(vals, 0)
		if err != nil {
			t.Fatalf("%v: unexpected error %v", vals, err)
		}
		if got != want {
			t.Fatalf("%v => %d want %d", vals, got, want)
		}
	}
	bad := func(vals []string, kind Kind) {
		t.Helper()
		_, err := parseContentLength(vals, 0)
		pe, isPE := AsError(err)
		if !isPE || pe.Kind != kind {
			t.Fatalf("%v => %v, want %s", vals, err, kind)
		}
	}
	ok([]string{"0"}, 0)
	ok([]string{"007"}, 7)
	ok([]string{"42, 42"}, 42)
	ok([]string{"42", "42"}, 42)
	bad([]string{"1, 2"}, KindProtocolConflict)
	bad([]string{"1", "2"}, KindProtocolConflict)
	bad([]string{""}, KindInvalidSyntax)
	bad([]string{"-1"}, KindInvalidSyntax)
	bad([]string{"12a"}, KindInvalidSyntax)
	bad([]string{"99999999999999999999999"}, KindInvalidSyntax)
}

// TestResolveFramingTable is a focused decision table for the TE/CL
// conflict rules.
func TestResolveFramingTable(t *testing.T) {
	h := func(kv ...string) *Header {
		hd := &Header{}
		for i := 0; i < len(kv); i += 2 {
			hd.add(kv[i], kv[i+1])
		}
		return hd
	}
	type want struct {
		mode FrameMode
		kind Kind
	}
	check := func(name string, hd *Header, w want) {
		t.Helper()
		mode, _, err := resolveFraming(hd, 0)
		if w.kind != "" {
			pe, ok := AsError(err)
			if !ok || pe.Kind != w.kind {
				t.Fatalf("%s: got %v want %s", name, err, w.kind)
			}
			return
		}
		if err != nil {
			t.Fatalf("%s: unexpected %v", name, err)
		}
		if mode != w.mode {
			t.Fatalf("%s: mode=%v want %v", name, mode, w.mode)
		}
	}
	check("none", h("Host", "x"), want{mode: FrameNone})
	check("fixed", h("Content-Length", "5"), want{mode: FrameFixed})
	check("chunked", h("Transfer-Encoding", "chunked"), want{mode: FrameChunked})
	check("te-cl", h("Content-Length", "5", "Transfer-Encoding", "chunked"), want{kind: KindProtocolConflict})
	check("gzip", h("Transfer-Encoding", "gzip, chunked"), want{kind: KindUnsupported})
	check("double-chunk", h("Transfer-Encoding", "chunked, chunked"), want{kind: KindUnsupported})
	check("cl-disagree", h("Content-Length", "1", "Content-Length", "2"), want{kind: KindProtocolConflict})
}

// TestChunkSizeLineHex checks hex parsing incl. uppercase, extensions
// and overflow rejection.
func TestChunkSizeLineHex(t *testing.T) {
	ok := func(line string, wantSize int64, wantLast bool) {
		t.Helper()
		sz, last, err := parseChunkSizeLine([]byte(line))
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if sz != wantSize || last != wantLast {
			t.Fatalf("%q => %d/%v want %d/%v", line, sz, last, wantSize, wantLast)
		}
	}
	bad := func(line string) {
		t.Helper()
		if _, _, err := parseChunkSizeLine([]byte(line)); err == nil {
			t.Fatalf("%q unexpectedly accepted", line)
		}
	}
	ok("a", 10, false)
	ok("A;foo=bar", 10, false)
	ok("0;ext=\"a;b\"", 0, true)
	ok("7fffffffffffffff", 1<<63-1, false)
	bad("")
	bad("xyz")
	bad("a;bad name=x")
	bad("8000000000000000")
}
