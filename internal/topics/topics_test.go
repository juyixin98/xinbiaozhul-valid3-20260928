package topics_test

import (
	"testing"

	"mqttd/internal/reference/matchref"
	"mqttd/internal/topics"
)

// cases is the wildcard-boundary matrix exercised by BOTH the production
// matcher and the independent reference matcher.
var cases = []struct {
	filter string
	topic  string
	want   bool
}{
	// Exact.
	{"a/b", "a/b", true},
	{"a/b", "a/c", false},
	{"a/b", "a/b/c", false},
	{"a/b/c", "a/b", false},

	// Single level.
	{"a/+", "a/b", true},
	{"a/+", "a/", true}, // empty trailing level
	{"a/+/c", "a/b/c", true},
	{"a/+/c", "a/b/x", false},
	{"+/b", "a/b", true},
	{"+", "a", true},
	{"+", "a/b", false},
	{"a/+", "a", false},     // missing level, even though '+' matches empty
	{"a/+/c", "a//c", true}, // empty middle level
	{"a/b/+", "a/b/", true},
	{"a/b/+", "a/b", false},

	// Multi level.
	{"a/#", "a/b", true},
	{"a/#", "a/b/c/d", true},
	{"a/#", "a", true}, // '#' absorbs zero levels
	{"a/#", "a/", true},
	{"#", "anything/at/all", true},
	{"#", "a", true},
	{"a/b/#", "a/b", true},
	{"a/b/#", "a/b/c", true},
	{"a/b/#", "a/c", false},

	// Mixed.
	{"a/+/c/#", "a/b/c/d/e", true},
	{"a/+/c/#", "a/b/c", true},
	{"a/+/c/#", "a/x/x", false},

	// '$' namespace: wildcard first level never matches.
	{"#", "$SYS/x", false},
	{"+/x", "$SYS/x", false},
	{"+", "$SYS", false},
	{"$SYS/#", "$SYS/x", true}, // explicit $ filter does match
	{"$SYS/+", "$SYS/x", true},
	{"a/#", "$SYS/x", false},

	// Empty-string level edge cases.
	{"/a", "/a", true},
	{"/+", "/a", true},
	{"//", "//", true},
	{"#", "//", true},
}

func TestProductionMatcher(t *testing.T) {
	for _, tc := range cases {
		got := topics.Match(tc.filter, tc.topic)
		if got != tc.want {
			t.Errorf("Match(%q,%q)=%v want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}

// TestReferenceDifferential compares the production iterative matcher with
// the independent recursive byte-scan reference over the boundary matrix AND
// an exhaustive generated grid. A disagreement fails regardless of which side
// is "right"; expected values come from the table (hand-derived from the
// spec), not from either implementation.
func TestReferenceDifferential(t *testing.T) {
	for _, tc := range cases {
		g := topics.Match(tc.filter, tc.topic)
		r := matchref.Match(tc.filter, tc.topic)
		if g != r {
			t.Errorf("implementations differ Match(%q,%q): prod=%v ref=%v table=%v",
				tc.filter, tc.topic, g, r, tc.want)
		}
		if r != tc.want {
			t.Errorf("ref Match(%q,%q)=%v want %v", tc.filter, tc.topic, r, tc.want)
		}
	}
}

// TestExhaustiveGridDifferential builds every filter/topic over a small level
// alphabet and asserts both implementations agree. The alphabet includes the
// empty level and '$' start. This catches disagreements the curated matrix
// could miss; neither implementation is used to compute the oracle, agreement
// plus the hand-derived table coverage is the oracle.
func TestExhaustiveGridDifferential(t *testing.T) {
	atoms := []string{"", "a", "b", "+", "#", "$SYS"}
	join := func(prefix string, lv string) string {
		if prefix == "" {
			return lv
		}
		return prefix + "/" + lv
	}
	// Generate structurally valid filters of depth 1..3.
	validFilters := map[string]bool{}
	var gen func(prefix string, depth int)
	gen = func(prefix string, depth int) {
		if depth > 3 {
			return
		}
		for _, at := range atoms {
			f := join(prefix, at)
			if err := topics.ValidateFilter(f); err == nil {
				validFilters[f] = true
				if at != "#" {
					gen(f, depth+1)
				}
			}
		}
	}
	gen("", 1)

	// Concrete topics: same alphabet without wildcards, depth 1..3.
	topicSet := map[string]bool{}
	var genT func(prefix string, depth int)
	genT = func(prefix string, depth int) {
		if depth > 3 {
			return
		}
		for _, at := range []string{"", "a", "b", "$SYS"} {
			tp := join(prefix, at)
			if err := topics.ValidateName(tp); err == nil {
				topicSet[tp] = true
				genT(tp, depth+1)
			}
		}
	}
	genT("", 1)

	nf, nt := 0, 0
	for f := range validFilters {
		nf++
		for tp := range topicSet {
			nt++
			g := topics.Match(f, tp)
			r := matchref.Match(f, tp)
			if g != r {
				t.Fatalf("differential mismatch filter=%q topic=%q prod=%v ref=%v", f, tp, g, r)
			}
		}
	}
	t.Logf("differential grid: %d valid filters x %d topics evaluated", nf, len(topicSet))
	_ = nt
}

func TestValidation(t *testing.T) {
	badFilters := []string{
		"",
		"a#",
		"a/#/b",
		"a+b",
		"+a",
		"a+",
		"a/##",
	}
	for _, f := range badFilters {
		if err := topics.ValidateFilter(f); err == nil {
			t.Errorf("filter %q should be invalid", f)
		}
		if matchref.ValidFilter(f) {
			t.Errorf("ref: filter %q should be invalid", f)
		}
	}
	goodFilters := []string{"#", "a/#", "+", "a/+", "a/+/b/#", "/+", "$SYS/#", "a//b"}
	for _, f := range goodFilters {
		if err := topics.ValidateFilter(f); err != nil {
			t.Errorf("filter %q should be valid: %v", f, err)
		}
		if !matchref.ValidFilter(f) {
			t.Errorf("ref: filter %q should be valid", f)
		}
	}
	badNames := []string{"", "a/+", "a/#", "x#", "x+"}
	for _, n := range badNames {
		if err := topics.ValidateName(n); err == nil {
			t.Errorf("name %q should be invalid", n)
		}
	}
}
