// Package matchref contains an INDEPENDENT reference implementation of the
// MQTT 3.1.1 wildcard matching rules.
//
// It exists so tests can cross-check the production matcher
// (internal/topics, iterative level-slice comparison) against a second
// implementation written in a different style (recursive descent over
// pre-split level arrays). Production code must never import this package;
// expected values in tests are generated here / hand-derived from the spec,
// never by the code under test.
//
// Reference: OASIS MQTT v3.1.1, section 4.7 ("Topic matching").
package matchref

import "strings"

// Match returns whether filter matches topic according to the reference
// algorithm. Both inputs must be pre-validated; the function performs only
// the structural wildcard walk.
func Match(filter, topic string) bool {
	// 4.7.2: a wildcard first level never matches a '$' server topic.
	if filter != "" && (filter[0] == '+' || filter[0] == '#') &&
		topic != "" && topic[0] == '$' {
		return false
	}
	f := strings.Split(filter, "/")
	t := strings.Split(topic, "/")
	return descend(f, 0, t, 0)
}

// descend is the recursive core. It compares filter levels fi.. against topic
// levels ti.. one level at a time. Using Split means an empty trailing level
// ("a/" splits to ["a",""]) is a first-class level that '+' matches.
func descend(f []string, fi int, t []string, ti int) bool {
	// Filter exhausted: a match requires the topic exhausted at the same
	// level boundary.
	if fi == len(f) {
		return ti == len(t)
	}
	switch {
	case f[fi] == "#":
		// Validated to be the last level; absorbs zero or more remaining
		// topic levels (including an empty trailing one).
		return true
	case f[fi] == "+" || (ti < len(t) && f[fi] == t[ti]):
		// '+' matches exactly one level, even an empty one; an exact level
		// matches itself. Consume one level from each side.
		if fi+1 == len(f) {
			return ti+1 == len(t)
		}
		if ti+1 == len(t) {
			// Topic exhausted but the filter continues: only a trailing
			// multi-level wildcard can absorb the missing levels, e.g.
			// filter "a/#" matches topic "a".
			return fi+2 == len(f) && f[fi+1] == "#"
		}
		return descend(f, fi+1, t, ti+1)
	default:
		return false
	}
}

// ValidFilter is a reference re-statement of filter well-formedness used only
// by tests.
func ValidFilter(filter string) bool {
	if filter == "" || len(filter) > 65535 || strings.IndexByte(filter, 0) >= 0 {
		return false
	}
	for i := 0; i < len(filter); i++ {
		switch filter[i] {
		case '#':
			if i != len(filter)-1 {
				return false
			}
			if i > 0 && filter[i-1] != '/' {
				return false
			}
		case '+':
			if i > 0 && filter[i-1] != '/' {
				return false
			}
			if i < len(filter)-1 && filter[i+1] != '/' {
				return false
			}
		}
	}
	return true
}
