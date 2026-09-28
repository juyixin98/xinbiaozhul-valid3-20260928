// Command genfixtures regenerates the byte-level HTTP/1.1 test
// fixtures from hand-authored wire strings.
//
// The INPUT bytes are assembled from explicit wire fragments (CRLF is
// always written as the two-character escape "\r\n") so every byte is
// auditable. The EXPECTED outcomes (method, body, reject kind/phase,
// byte offset, legal-grammar reference) are authored by hand against
// RFC 9110/9112; they are never produced by the parser under test.
// Tests additionally decode every "valid" fixture with the separate
// internal/refparse reference decoder and require both decoders to
// agree independently.
//
// Usage:
//
//	go run ./test/genfixtures -out test/testdata/fixtures.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Fixture is one byte-level case.
type Fixture struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Category    string `json:"category"` // legal | conflict | syntax | truncation | smuggling | limit | state
	Wire        string `json:"wire"`     // exact bytes on the connection
	// Expectations for legal messages:
	WantValid bool   `json:"want_valid"`
	Method    string `json:"method,omitempty"`
	Target    string `json:"target,omitempty"`
	Version   string `json:"version,omitempty"`
	Frame     string `json:"frame,omitempty"` // none | fixed | chunked
	Length    int64  `json:"length,omitempty"`
	Body      string `json:"body,omitempty"`
	// Bytes that must remain after consuming this message (start of
	// the next pipelined request). Empty means the boundary is EOF.
	Remainder string `json:"remainder,omitempty"`
	// Expectations for rejected messages:
	WantReject bool   `json:"want_reject,omitempty"`
	Kind       string `json:"kind,omitempty"` // invalid_syntax | protocol_conflict | resource_limit | unsupported
	Phase      string `json:"phase,omitempty"`
	// wantOffset, when >=0, asserts the absolute byte offset reported.
	WantOffset int64 `json:"want_offset"`
	// wantHTTPStatus is the response status the server emits before
	// closing; 0 means the connection is closed silently.
	WantHTTPStatus int `json:"want_http_status,omitempty"`
	// ParseCount is the number of successive messages to parse from
	// the same stream before the asserted outcome (default 1). Used
	// for pipelined-then-truncated cases.
	ParseCount int    `json:"parse_count,omitempty"`
	LegalRef   string `json:"legal_ref,omitempty"` // RFC clause for the accepted shape
}

func main() {
	out := flag.String("out", "test/testdata/fixtures.json", "output path")
	flag.Parse()

	cases := build()
	b, err := json.MarshalIndent(struct {
		Version  string    `json:"version"`
		Spec     string    `json:"spec"`
		Note     string    `json:"note"`
		Fixtures []Fixture `json:"fixtures"`
	}{
		Version:  "fixtures-2026-09-28",
		Spec:     "RFC 9110 (HTTP semantics) + RFC 9112 (HTTP/1.1 messaging)",
		Note:     "Wire strings contain literal CRLF via \\r\\n escapes. Expected values are hand-authored; valid cases are additionally cross-checked by internal/refparse.",
		Fixtures: cases,
	}, "", "  ")
	if err != nil {
		fatal(err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %d fixtures to %s\n", len(cases), *out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// Wire fragment helpers.
const CRLF = "\r\n"

func reqLine(method, target, version string) string {
	return method + " " + target + " " + version + CRLF
}

func hdr(k, v string) string { return k + ": " + v + CRLF }

func build() []Fixture {
	var f []Fixture
	add := func(x Fixture) {
		if x.WantOffset == 0 {
			x.WantOffset = -1
		}
		f = append(f, x)
	}

	// ---------- LEGAL REFERENCE GRAMMAR ----------

	getNoBody := reqLine("GET", "/healthz", "HTTP/1.1") +
		hdr("Host", "localhost") + hdr("User-Agent", "fixture") + CRLF
	add(Fixture{
		ID: "valid-get-no-body", Description: "minimal GET, no body, persistent connection",
		Category: "legal", Wire: getNoBody, WantValid: true,
		Method: "GET", Target: "/healthz", Version: "HTTP/1.1",
		Frame: "none", LegalRef: "RFC9112 §6.3 absence of framing fields => no body",
	})

	postFixed := reqLine("POST", "/v1/records", "HTTP/1.1") +
		hdr("Host", "localhost") + hdr("Content-Length", "13") +
		hdr("Content-Type", "application/json") + CRLF +
		`{"title":"a"}`
	add(Fixture{
		ID: "valid-post-fixed", Description: "fixed-length 13 byte JSON body",
		Category: "legal", Wire: postFixed, WantValid: true,
		Method: "POST", Target: "/v1/records", Version: "HTTP/1.1",
		Frame: "fixed", Length: 13, Body: `{"title":"a"}`,
		LegalRef: "RFC9112 §6.3 Content-Length framing",
	})

	// Two pipelined requests on one connection (粘包/back-to-back).
	first := reqLine("GET", "/a", "HTTP/1.1") + hdr("Host", "x") + CRLF
	second := reqLine("GET", "/b", "HTTP/1.1") + hdr("Host", "x") + CRLF
	add(Fixture{
		ID: "valid-pipeline-two", Description: "two pipelined requests, exact boundary must be found",
		Category: "legal", Wire: first + second, WantValid: true,
		Method: "GET", Target: "/a", Version: "HTTP/1.1", Frame: "none",
		Remainder: second,
		LegalRef:  "RFC9112 §9.3.2 pipelining; first message must leave second intact",
	})

	// Fixed body followed by a pipelined request (residual test).
	fixedThenReq := reqLine("POST", "/p", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "5") + CRLF + "12345"
	nextReq := reqLine("GET", "/after", "HTTP/1.1") + hdr("Host", "x") + CRLF
	add(Fixture{
		ID: "valid-fixed-then-pipeline", Description: "fixed body then a pipelined request; no residual body leak",
		Category: "legal", Wire: fixedThenReq + nextReq, WantValid: true,
		Method: "POST", Target: "/p", Frame: "fixed", Length: 5, Body: "12345",
		Remainder: nextReq,
		LegalRef:  "RFC9112 §6.3 exactly Content-Length octets then next message",
	})

	// Chunked with extensions, trailers and CRLF inside data.
	chunkWire := reqLine("POST", "/c", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"4;name=val\r\nWiki\r\n" +
		"5;foo=\"a;b\"\r\npedia\r\n" +
		"0\r\nTrailer-X: yes\r\n\r\n"
	add(Fixture{
		ID: "valid-chunked-ext-trailers", Description: "chunked with chunk-ext, quoted ext value, trailer; data contains no split",
		Category: "legal", Wire: chunkWire, WantValid: true,
		Method: "POST", Target: "/c", Version: "HTTP/1.1",
		Frame: "chunked", Body: "Wikipedia",
		LegalRef: "RFC9112 §7.1 chunk, chunk-ext, last-chunk and trailer-part",
	})

	// Chunked where the data itself contains CRLF (must NOT desync).
	crlfBody := "ab\r\ncd"
	chunkCRLF := reqLine("POST", "/raw", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		fmt.Sprintf("%x%s%s%s", len(crlfBody), CRLF, crlfBody, CRLF) +
		"0\r\n\r\n"
	add(Fixture{
		ID: "valid-chunked-body-with-crlf", Description: "chunk payload literally contains CRLF; size, not scanning, delimits",
		Category: "legal", Wire: chunkCRLF, WantValid: true,
		Method: "POST", Target: "/raw", Frame: "chunked", Body: crlfBody,
		LegalRef: "RFC9112 §7.1.2 chunk-data = 1*OCTET sized by chunk-size",
	})

	// Empty chunked body.
	emptyChunk := reqLine("POST", "/e", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF + "0\r\n\r\n"
	add(Fixture{
		ID: "valid-chunked-empty", Description: "immediate last-chunk, empty body",
		Category: "legal", Wire: emptyChunk, WantValid: true,
		Method: "POST", Target: "/e", Frame: "chunked", Body: "",
		LegalRef: "RFC9112 §7.1 last-chunk terminates",
	})

	// Content-Length: 0.
	clZero := reqLine("POST", "/z", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "0") + CRLF
	add(Fixture{
		ID: "valid-cl-zero", Description: "explicit zero length body",
		Category: "legal", Wire: clZero, WantValid: true,
		Method: "POST", Target: "/z", Frame: "fixed", Length: 0, Body: "",
		LegalRef: "RFC9112 §6.3 Content-Length: 0",
	})

	// Multiple Content-Length with identical values (comma list).
	clDupOK := reqLine("POST", "/dup", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "7, 7") + CRLF + "abcdefg"
	add(Fixture{
		ID: "valid-cl-consistent-list", Description: "comma list of identical CL values is consistent",
		Category: "legal", Wire: clDupOK, WantValid: true,
		Method: "POST", Target: "/dup", Frame: "fixed", Length: 7, Body: "abcdefg",
		LegalRef: "RFC9112 §6.3 comma list allowed when values agree",
	})

	// HTTP/1.0 with keep-alive.
	ka10 := reqLine("GET", "/old", "HTTP/1.0") +
		hdr("Host", "x") + hdr("Connection", "keep-alive") + CRLF
	add(Fixture{
		ID: "valid-http10-keepalive", Description: "HTTP/1.0 opt-in keep-alive",
		Category: "legal", Wire: ka10, WantValid: true,
		Method: "GET", Target: "/old", Version: "HTTP/1.0", Frame: "none",
		LegalRef: "RFC9110 Connection: keep-alive on HTTP/1.0",
	})

	// Case-insensitive header names; obs-text value.
	obsVal := "xÿ"
	obs := reqLine("GET", "/obs", "HTTP/1.1") +
		hdr("host", "x") + "X-Mixed: " + obsVal + CRLF + CRLF
	add(Fixture{
		ID: "valid-obs-text-value", Description: "lowercase field name accepted; obs-text byte 0xFF in value",
		Category: "legal", Wire: obs, WantValid: true,
		Method: "GET", Target: "/obs", Frame: "none",
		LegalRef: "RFC9110 field-name case-insensitive; field-value allows obs-text",
	})

	// OPTIONS asterisk-form.
	star := reqLine("OPTIONS", "*", "HTTP/1.1") + hdr("Host", "x") + CRLF
	add(Fixture{
		ID: "valid-options-asterisk", Description: "asterisk-form target",
		Category: "legal", Wire: star, WantValid: true,
		Method: "OPTIONS", Target: "*", Frame: "none",
		LegalRef: "RFC9110 asterisk-form for OPTIONS",
	})

	// Chunked followed by a pipelined request (boundary proof).
	chunkThen := reqLine("POST", "/c2", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"3\r\nabc\r\n0\r\n\r\n"
	afterChunk := reqLine("GET", "/tail", "HTTP/1.1") + hdr("Host", "x") + CRLF
	add(Fixture{
		ID: "valid-chunked-then-pipeline", Description: "chunked request followed by pipelined request",
		Category: "legal", Wire: chunkThen + afterChunk, WantValid: true,
		Method: "POST", Target: "/c2", Frame: "chunked", Body: "abc",
		Remainder: afterChunk,
		LegalRef:  "RFC9112 §9.3.2 + §7.1 exact chunked boundary",
	})

	// ---------- TE / CL CONFLICTS ----------

	teAndCL := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") +
		hdr("Content-Length", "8") + CRLF + "0\r\n\r\n"
	add(Fixture{
		ID: "reject-te-and-cl", Description: "Transfer-Encoding and Content-Length both present",
		Category: "conflict", Wire: teAndCL, WantReject: true,
		Kind: "protocol_conflict", Phase: "resolve_framing",
		WantHTTPStatus: 400, WantOffset: -1,
	})

	clAndTE2 := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "8") +
		hdr("Transfer-Encoding", "chunked") + CRLF
	add(Fixture{
		ID: "reject-cl-and-te-reversed", Description: "CL then TE present (order independent)",
		Category: "conflict", Wire: clAndTE2, WantReject: true,
		Kind: "protocol_conflict", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	clDifferFields := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "8") +
		hdr("Content-Length", "9") + CRLF
	add(Fixture{
		ID: "reject-cl-differing-fields", Description: "two CL fields with different lengths",
		Category: "conflict", Wire: clDifferFields, WantReject: true,
		Kind: "protocol_conflict", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	clDifferList := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "8, 9") + CRLF
	add(Fixture{
		ID: "reject-cl-differing-list", Description: "comma list with disagreeing lengths",
		Category: "conflict", Wire: clDifferList, WantReject: true,
		Kind: "protocol_conflict", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	clFoldSmuggle := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + "Content-Length: 8\r\nContent-Length:09\r\n" + CRLF
	add(Fixture{
		ID: "reject-cl-numeric-different-encoding", Description: "8 and 09 decode to 8 and 9 and disagree",
		Category: "smuggling", Wire: clFoldSmuggle, WantReject: true,
		Kind: "protocol_conflict", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	// ---------- MALFORMED SYNTAX / ILLEGAL NEWLINES ----------

	bareLFReq := "GET /x HTTP/1.1\nHost: x\n\n"
	add(Fixture{
		ID: "reject-bare-lf-request-line", Description: "LF without CR on request line",
		Category: "syntax", Wire: bareLFReq, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_request_line", WantHTTPStatus: 400, WantOffset: 15,
	})

	bareLFHeader := reqLine("GET", "/x", "HTTP/1.1") + "Host: x\n" + CRLF
	add(Fixture{
		ID: "reject-bare-lf-header", Description: "header line terminated by bare LF",
		Category: "syntax", Wire: bareLFHeader, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_headers", WantHTTPStatus: 400,
	})

	obsoleteFold := reqLine("GET", "/x", "HTTP/1.1") +
		hdr("Host", "x") + "X-Long: part1\r\n\tpart2\r\n" + CRLF
	add(Fixture{
		ID: "reject-obsolete-line-folding", Description: "obs-fold (leading HTAB continuation) rejected as non-token field name",
		Category: "syntax", Wire: obsoleteFold, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_header_field", WantHTTPStatus: 400,
	})

	badMethod := reqLine("GE T", "/x", "HTTP/1.1") + hdr("Host", "x") + CRLF
	add(Fixture{
		ID: "reject-method-space", Description: "method contains space",
		Category: "syntax", Wire: badMethod, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_request_line", WantHTTPStatus: 400,
	})

	noSeparators := "GET/x HTTP/1.1\r\n\r\n"
	add(Fixture{
		ID: "reject-no-sp", Description: "request line missing SP separators",
		Category: "syntax", Wire: noSeparators, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_request_line", WantHTTPStatus: 400,
	})

	tabSep := "GET\t/x\tHTTP/1.1\r\nHost: x\r\n\r\n"
	add(Fixture{
		ID: "reject-tab-separators", Description: "HTAB used where SP required in request line",
		Category: "syntax", Wire: tabSep, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_request_line", WantHTTPStatus: 400,
	})

	missingHost := reqLine("GET", "/x", "HTTP/1.1") + CRLF
	add(Fixture{
		ID: "reject-missing-host", Description: "HTTP/1.1 requires Host",
		Category: "syntax", Wire: missingHost, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_header_field", WantHTTPStatus: 400,
	})

	duplicateHost := reqLine("GET", "/x", "HTTP/1.1") +
		hdr("Host", "a") + hdr("Host", "b") + CRLF
	add(Fixture{
		ID: "reject-duplicate-host", Description: "two Host fields",
		Category: "syntax", Wire: duplicateHost, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_header_field", WantHTTPStatus: 400,
	})

	emptyFieldName := reqLine("GET", "/x", "HTTP/1.1") +
		hdr("Host", "x") + ": value\r\n\r\n"
	add(Fixture{
		ID: "reject-empty-field-name", Description: "header line starts with colon",
		Category: "syntax", Wire: emptyFieldName, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_header_field", WantHTTPStatus: 400,
	})

	spaceBeforeColon := reqLine("GET", "/x", "HTTP/1.1") +
		hdr("Host", "x") + "X-Bad : v\r\n\r\n"
	add(Fixture{
		ID: "reject-space-before-colon", Description: "whitespace between field name and colon (smuggling gap)",
		Category: "smuggling", Wire: spaceBeforeColon, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_header_field", WantHTTPStatus: 400,
	})

	nulInValue := reqLine("GET", "/x", "HTTP/1.1") +
		hdr("Host", "x") + "X-N: ab\x00cd\r\n\r\n"
	add(Fixture{
		ID: "reject-nul-in-value", Description: "NUL byte in field value",
		Category: "syntax", Wire: nulInValue, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_header_field", WantHTTPStatus: 400,
	})

	clNotNumber := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "12x") + CRLF
	add(Fixture{
		ID: "reject-cl-not-number", Description: "non-digit Content-Length",
		Category: "syntax", Wire: clNotNumber, WantReject: true,
		Kind: "invalid_syntax", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	clNegative := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "-1") + CRLF
	add(Fixture{
		ID: "reject-cl-negative", Description: "minus sign in Content-Length",
		Category: "syntax", Wire: clNegative, WantReject: true,
		Kind: "invalid_syntax", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	clHex := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "0x10") + CRLF
	add(Fixture{
		ID: "reject-cl-hex", Description: "hex Content-Length rejected",
		Category: "syntax", Wire: clHex, WantReject: true,
		Kind: "invalid_syntax", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	badVersion := reqLine("GET", "/x", "HTTP/2.0") + hdr("Host", "x") + CRLF
	add(Fixture{
		ID: "reject-http2-version", Description: "HTTP/2 prior-knowledge over this port unsupported",
		Category: "syntax", Wire: badVersion, WantReject: true,
		Kind: "unsupported", Phase: "parse_request_line", WantHTTPStatus: 505,
	})

	absoluteTarget := reqLine("GET", "http://evil.example.com/x", "HTTP/1.1") +
		hdr("Host", "x") + CRLF
	add(Fixture{
		ID: "reject-absolute-form", Description: "absolute-form implies proxy and is not implemented",
		Category: "syntax", Wire: absoluteTarget, WantReject: true,
		Kind: "unsupported", Phase: "parse_request_line", WantHTTPStatus: 400,
	})

	// ---------- CHUNKED ERRORS ----------

	badChunkHex := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"xyz\r\nabc\r\n0\r\n\r\n"
	add(Fixture{
		ID: "reject-chunk-bad-hex", Description: "non-hex chunk size",
		Category: "syntax", Wire: badChunkHex, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_chunk_size", WantHTTPStatus: 400,
	})

	chunkMissingCRLF := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"3\r\nabcXX0\r\n\r\n"
	add(Fixture{
		ID: "reject-chunk-missing-end-crlf", Description: "chunk-data not followed by CRLF",
		Category: "syntax", Wire: chunkMissingCRLF, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_chunk_end_crlf", WantHTTPStatus: 400,
	})

	chunkBareLF := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"3\nabc\n0\n\n"
	add(Fixture{
		ID: "reject-chunk-bare-lf", Description: "chunk framing uses bare LF",
		Category: "syntax", Wire: chunkBareLF, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_chunk_size_line", WantHTTPStatus: 400,
	})

	chunkBadExt := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"3;bad name=v\r\nabc\r\n0\r\n\r\n"
	add(Fixture{
		ID: "reject-chunk-bad-ext", Description: "chunk-ext-name contains space",
		Category: "syntax", Wire: chunkBadExt, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_chunk_size", WantHTTPStatus: 400,
	})

	teGzip := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "gzip, chunked") + CRLF
	add(Fixture{
		ID: "reject-te-gzip-chained", Description: "chained coding gzip, chunked unsupported",
		Category: "conflict", Wire: teGzip, WantReject: true,
		Kind: "unsupported", Phase: "resolve_framing", WantHTTPStatus: 501,
	})

	teChunkedTwice := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked, chunked") + CRLF
	add(Fixture{
		ID: "reject-te-chunked-twice", Description: "double chunked is ambiguous for this subset",
		Category: "conflict", Wire: teChunkedTwice, WantReject: true,
		Kind: "unsupported", Phase: "resolve_framing", WantHTTPStatus: 501,
	})

	framingInTrailer := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"0\r\nContent-Length: 5\r\n\r\n"
	add(Fixture{
		ID: "reject-framing-in-trailer", Description: "Content-Length smuggled in trailer",
		Category: "smuggling", Wire: framingInTrailer, WantReject: true,
		Kind: "protocol_conflict", Phase: "read_chunk_trailer", WantHTTPStatus: 400,
	})

	// ---------- TRUNCATION ----------

	truncatedHead := reqLine("GET", "/x", "HTTP/1.1") + hdr("Host", "x")
	add(Fixture{
		ID: "reject-truncated-head", Description: "EOF in the middle of header section",
		Category: "truncation", Wire: truncatedHead, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_headers", WantOffset: -1, WantHTTPStatus: 0,
	})

	truncatedFixedBody := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "10") + CRLF + "abc"
	add(Fixture{
		ID: "reject-truncated-fixed-body", Description: "3 of 10 body bytes then EOF; must not be treated as next request",
		Category: "truncation", Wire: truncatedFixedBody, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_fixed_body", WantHTTPStatus: 0,
	})

	truncatedChunkData := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"5\r\nabc"
	add(Fixture{
		ID: "reject-truncated-chunk-data", Description: "declared 5 octets, only 3 then EOF",
		Category: "truncation", Wire: truncatedChunkData, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_chunk_data", WantHTTPStatus: 0,
	})

	truncatedChunkLine := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF + "4\r\nabcd\r\n1"
	add(Fixture{
		ID: "reject-truncated-chunk-size-line", Description: "next chunk-size line cut off",
		Category: "truncation", Wire: truncatedChunkLine, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_chunk_size_line", WantHTTPStatus: 0,
	})

	truncatedTrailer := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"0\r\nX-T: unfinished"
	add(Fixture{
		ID: "reject-truncated-trailer", Description: "trailer field line cut off",
		Category: "truncation", Wire: truncatedTrailer, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_chunk_trailer", WantHTTPStatus: 0,
	})

	// A partial pipelined second request after a complete first must
	// be an error, never an accepted second request.
	goodThenPartial := reqLine("GET", "/ok", "HTTP/1.1") + hdr("Host", "x") + CRLF +
		"GET /partial HTTP/1.1\r\nHost: x"
	add(Fixture{
		ID: "reject-partial-pipelined-second", Description: "complete request then truncated next request",
		Category: "truncation", Wire: goodThenPartial, WantReject: true,
		Kind: "invalid_syntax", Phase: "read_headers", WantHTTPStatus: 0,
		ParseCount: 2,
	})

	// ---------- RESOURCE LIMITS ----------

	bigCL := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "1048577") + CRLF
	add(Fixture{
		ID: "reject-cl-over-limit", Description: "declared length exceeds 1MiB body limit",
		Category: "limit", Wire: bigCL, WantReject: true,
		Kind: "resource_limit", Phase: "read_fixed_body", WantHTTPStatus: 413,
	})

	bigChunk := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") + CRLF +
		"100001\r\n"
	add(Fixture{
		ID: "reject-chunk-size-over-limit", Description: "single chunk (0x100001 = 1048577 bytes) larger than 1MiB body limit",
		Category: "limit", Wire: bigChunk, WantReject: true,
		Kind: "resource_limit", Phase: "read_chunk_data", WantHTTPStatus: 413,
	})

	// ---------- REQUEST SMUGGLING AMBIGUITY ----------

	// CL.TE classic: CL present first, TE present second.
	clTEsmug := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Content-Length", "4") +
		hdr("Transfer-Encoding", "chunked") + CRLF +
		"0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: x\r\n\r\n"
	add(Fixture{
		ID: "reject-smuggle-cl-te", Description: "CL.TE: both present; must reject, never process smuggled request",
		Category: "smuggling", Wire: clTEsmug, WantReject: true,
		Kind: "protocol_conflict", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	// TE.CL classic.
	teCLsmug := reqLine("POST", "/x", "HTTP/1.1") +
		hdr("Host", "x") + hdr("Transfer-Encoding", "chunked") +
		hdr("Content-Length", "4") + CRLF + "0\r\n\r\n"
	add(Fixture{
		ID: "reject-smuggle-te-cl", Description: "TE.CL: both present; reject",
		Category: "smuggling", Wire: teCLsmug, WantReject: true,
		Kind: "protocol_conflict", Phase: "resolve_framing", WantHTTPStatus: 400,
	})

	// Header line that hides a second request via embedded CRLF is
	// already caught by field-value checks; assert phase.
	embeddedCRLF := reqLine("GET", "/x", "HTTP/1.1") +
		hdr("Host", "x") + "X: a\r\nGET /evil HTTP/1.1\r\n\r\n"
	add(Fixture{
		ID: "reject-embedded-request-in-field", Description: "an embedded request line is then parsed as another field and fails (no colon)",
		Category: "smuggling", Wire: embeddedCRLF, WantReject: true,
		Kind: "invalid_syntax", Phase: "parse_header_field", WantHTTPStatus: 400,
	})

	return f
}
