package topic

import "testing"

// TestWildcardBoundary is the explicit §4.7 boundary table. Both the
// production matcher and the independent reference are asserted.
func TestWildcardBoundary(t *testing.T) {
	cases := []struct {
		filter string
		name   string
		want   bool
		note   string
	}{
		{"a", "a", true, "exact"},
		{"a", "b", false, "exact mismatch"},
		{"a/b", "a/b", true, "two levels"},
		{"a/b", "a", false, "missing level"},
		{"a/+", "a/b", true, "+ matches one level"},
		{"a/+", "a", false, "+ requires a level"},
		{"a/+", "a/b/c", false, "+ matches exactly one"},
		{"+", "a", true, "single + matches single level"},
		{"+", "a/b", false, "single + not two"},
		{"a/#", "a", true, "# matches zero remaining levels"},
		{"a/#", "a/", true, "# matches one empty level"},
		{"a/#", "a/b", true, "# matches one level"},
		{"a/#", "a/b/c/d", true, "# matches many"},
		{"a/#", "ab", false, "prefix must reach a level boundary"},
		{"#", "a", true, "# alone matches any topic"},
		{"#", "a/b/c", true, "# alone multi-level"},
		{"#", "", false, "empty name never matches"},
		{"/", "/", true, "single separator creates two empty levels"},
		{"//", "//", true, "double separator"},
		{"/a", "/a", true, "leading slash empty first level"},
		{"+/a", "/a", true, "+ matches empty first level"},
		{"a/+/", "a/b/", true, "trailing slash empty last level"},
		{"a/+", "a/b/", false, "level count differs"},
		{"sport/tennis/player1", "sport/tennis/player1", true, "spec example exact"},
		{"sport/tennis/+", "sport/tennis/player2", true, "spec example +"},
		{"sport/tennis/+", "sport/tennis/player1/ranking", false, "spec + only one"},
		{"sport/+", "sport/tennis", true, "spec +"},
		{"sport/+", "sport", false, "spec + requires level"},
		{"sport/#", "sport", true, "spec # zero levels"},
		{"sport/#", "sport/tennis/player1", true, "spec # multi"},
		{"#", "$SYS/x", false, "$ topics not matched by #"},
		{"+", "$SYS", false, "$ topics not matched by +"},
		{"$SYS/#", "$SYS/x", true, "literal $ filter can match"},
		{"a/b#c", "a/b#c", false, "embedded hash is literal non-match invalid filter"},
		{"a/+b", "a/xb", false, "embedded plus invalid filter"},
	}
	for _, tc := range cases {
		t.Run(tc.filter+"|"+tc.name, func(t *testing.T) {
			got := Match(tc.filter, tc.name)
			if got != tc.want {
				t.Fatalf("Match(%q,%q)=%v want %v (%s)", tc.filter, tc.name, got, tc.want, tc.note)
			}
			ref := ReferenceMatch(tc.filter, tc.name)
			if ref != got {
				t.Fatalf("reference disagrees: Match=%v ReferenceMatch=%v (%s)", got, ref, tc.note)
			}
		})
	}
}

// TestInvalidFilters documents rejection of malformed filters.
func TestInvalidFilters(t *testing.T) {
	for _, f := range []string{"", "a/#/b", "a/#/c", "a/b+/c", "a/+x", "a/x#", "#/a", "a/#b"} {
		if Match(f, "a/b") {
			t.Fatalf("invalid filter %q matched", f)
		}
		if ReferenceMatch(f, "a/b") {
			t.Fatalf("invalid filter %q matched (reference)", f)
		}
	}
}

// TestDifferentialFuzz drives a structured random generator over the
// 6-symbol alphabet that covers all boundary positions (leading/trailing
// separators, wildcards as whole or partial levels, $). Any disagreement
// between the two implementations fails the test — this is the independent
// reference path required by the task; expected values are never produced by
// the code under test.
func TestDifferentialFuzz(t *testing.T) {
	const iterations = 20000
	// Filters use the full §4.7 symbol set; topic NAMES are generated from
	// legal-name symbols (letters and '/', optionally '$'-prefixed) so both
	// implementations are compared only on inputs the protocol can produce.
	filterAlpha := []byte{'a', '/', '+', '#'}
	nameAlpha := []byte{'a', 'b', '/'}
	seed := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}
	gen := func(alpha []byte, allowDollar bool) string {
		n := int(next()%9) + 1
		b := make([]byte, n)
		for i := range b {
			b[i] = alpha[int(next()%uint64(len(alpha)))]
		}
		if allowDollar && next()%3 == 0 {
			b = append([]byte{'$'}, b...)
		}
		return string(b)
	}
	for i := 0; i < iterations; i++ {
		f := gen(filterAlpha, false)
		name := gen(nameAlpha, true)
		got := Match(f, name)
		ref := ReferenceMatch(f, name)
		if got != ref {
			t.Fatalf("iter %d disagreement filter=%q name=%q production=%v reference=%v",
				i, f, name, got, ref)
		}
	}
}
