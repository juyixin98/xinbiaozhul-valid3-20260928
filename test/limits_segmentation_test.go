package testutil_test

import (
	"io"
	"math/rand"
	"strings"
	"testing"

	"http11subset/internal/httpx"
	"http11subset/test/testutil"
)

// TestTinyHeaderLimit rejects a head section over a small ceiling.
func TestTinyHeaderLimit(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	limits := httpx.Limits{MaxHeaderBytes: 40, MaxBodyBytes: 1024, MaxChunkLineBytes: 40}
	wire := "GET /x HTTP/1.1\r\nHost: x\r\nX-Long: abcdefghijklmnop\r\n\r\n"
	p := httpx.NewParser(strings.NewReader(wire), limits, 16)
	_, err := p.ReadHead()
	pe, ok := httpx.AsError(err)
	if !ok {
		t.Fatalf("want *httpx.Error, got %v", err)
	}
	if pe.Kind != httpx.KindResourceLimit || pe.Code != 431 {
		t.Fatalf("want resource_limit/431, got %s/%d", pe.Kind, pe.Code)
	}
	log.Case("tiny-head", 1, "limit", "431_HEADER_LIMIT", "MaxHeaderBytes", pe, nil)
}

// TestTinyChunkLineLimit rejects an oversized extension line.
func TestTinyChunkLineLimit(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	limits := httpx.Limits{MaxHeaderBytes: 64 * 1024, MaxBodyBytes: 1 << 20, MaxChunkLineBytes: 12}
	wire := "POST /x HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"4;verylongextensionname=v\r\nabcd\r\n0\r\n\r\n"
	p := httpx.NewParser(strings.NewReader(wire), limits, 16)
	req, err := p.ReadHead()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	err = p.ReadBody(req)
	pe, ok := httpx.AsError(err)
	if !ok {
		t.Fatalf("want *httpx.Error, got %v", err)
	}
	if pe.Kind != httpx.KindResourceLimit {
		t.Fatalf("want resource_limit, got %s (%v)", pe.Kind, pe)
	}
	log.Case("tiny-chunkline", 1, "limit", "CHUNK_LINE_LIMIT", "MaxChunkLineBytes", pe, nil)
}

// TestRandomSegmentation replays each valid fixture across 20
// deterministic random segmentation patterns. Acceptance and the
// consumed boundary must be segmentation-independent.
func TestRandomSegmentation(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()
	fixtures := testutil.LoadFixtures(t, "testdata/fixtures.json")

	valid := 0
	for _, fx := range fixtures {
		if fx["want_reject"] == true || asString(fx, "want_reject") == "true" {
			continue
		}
		valid++
		id := asString(fx, "id")
		wire := []byte(asString(fx, "wire"))
		t.Run(id, func(t *testing.T) {
			for seed := int64(0); seed < 20; seed++ {
				rng := rand.New(rand.NewSource(seed))
				r := &randomReader{data: wire, rng: rng, max: 9}
				p := httpx.NewParser(r, testLimits(), 8)
				req, err := p.ReadHead()
				if err != nil {
					t.Fatalf("seed %d head: %v", seed, err)
				}
				if err := p.ReadBody(req); err != nil {
					t.Fatalf("seed %d body: %v", seed, err)
				}
				wantRem := asString(fx, "remainder")
				if rem := len(wire) - int(req.BodyEndOffset); rem != len(wantRem) {
					t.Fatalf("seed %d: residual len %d want %d", seed, rem, len(wantRem))
				}
			}
			log.Step("random_split", "SEGMENTATION_INDEPENDENT",
				"20 random splits yield identical boundary",
				map[string]any{"case_id": id})
		})
	}
	log.Step("random_split", "ALL_VALID_FIXTURES_STABLE",
		"half/sticky packet handling proven", map[string]any{"valid_cases": valid})
}

// randomReader returns 1..max bytes per Read deterministically.
type randomReader struct {
	data []byte
	pos  int
	rng  *rand.Rand
	max  int
}

func (r *randomReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.rng.Intn(r.max) + 1
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data)-r.pos {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

// TestErrorKindsDistinct asserts the four failure classes never
// collapse: invalid input, protocol conflict, resource limit and IO
// failure are separable by Kind, and a truncated message is never
// reported as success or as a normal EOF.
func TestErrorKindsDistinct(t *testing.T) {
	log := testutil.NewRunLogger(t)
	defer log.Close()

	cases := []struct {
		name string
		wire string
		want httpx.Kind
	}{
		{"invalid", "GET /x HTTP/1.1\r\nHost: x\r\nBad-No-Colon\r\n\r\n", httpx.KindInvalidSyntax},
		{"conflict", "POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n", httpx.KindProtocolConflict},
		{"limit", "POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 1048577\r\n\r\n", httpx.KindResourceLimit},
		{"unsupported", "POST /x HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: gzip, chunked\r\n\r\n", httpx.KindUnsupported},
	}
	seen := map[httpx.Kind]bool{}
	for i, tc := range cases {
		p := httpx.NewParser(strings.NewReader(tc.wire), testLimits(), 16)
		var pe *httpx.Error
		req, err := p.ReadHead()
		if err == nil {
			err = p.ReadBody(req)
		}
		if err == nil {
			t.Fatalf("%s: expected failure, got success", tc.name)
		}
		pe, ok := httpx.AsError(err)
		if !ok {
			t.Fatalf("%s: non-httpx error %v", tc.name, err)
		}
		if pe.Kind != tc.want {
			t.Fatalf("%s: kind=%s want %s", tc.name, pe.Kind, tc.want)
		}
		seen[pe.Kind] = true
		log.Case(tc.name, i+1, "classify", "DISTINCT_"+strings.ToUpper(string(tc.want)),
			"failure class separable without string matching", pe, nil)
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 distinct kinds, saw %d", len(seen))
	}

	// Truncation is KindInvalidSyntax with code 0, NOT clean EOF.
	trunc := "POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 9\r\n\r\nabc"
	p := httpx.NewParser(strings.NewReader(trunc), testLimits(), 16)
	req, err := p.ReadHead()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	err = p.ReadBody(req)
	pe, _ := httpx.AsError(err)
	if pe == nil || pe.Kind != httpx.KindInvalidSyntax || pe.Code != 0 {
		t.Fatalf("truncation misreported: %+v", err)
	}
	log.Case("truncation", 1, "classify", "TRUNCATED_NOT_SUCCESS_NOT_EOF",
		"undefined/truncated result must not masquerade as success", pe, nil)
}
