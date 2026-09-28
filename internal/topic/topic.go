// Package topic implements MQTT 3.1.1 topic filter semantics (§4.7).
//
// The production matcher (Match) is a level-based implementation. An
// intentionally different second implementation (ReferenceMatch in ref.go)
// exists purely as an independent oracle for differential testing: the two
// share no matching code and were written against the spec text separately.
// Tests fuzz both against each other and against a table of boundary cases.
package topic

// splitLevels returns the topic levels WITHOUT allocating substrings in a
// separate type; the production matcher works on indices.
func splitLevels(t string) []string {
	if t == "" {
		return nil
	}
	levels := []string{}
	start := 0
	for i := 0; i <= len(t); i++ {
		if i == len(t) || t[i] == '/' {
			levels = append(levels, t[start:i])
			start = i + 1
		}
	}
	return levels
}

// Match reports whether a concrete topic name matches a filter, applying
// MQTT 3.1.1 §4.7.1/4.7.2:
//
//   - '/' separates levels; a leading/trailing '/' creates an empty level
//     which is significant ("/a/" has three levels: "", "a", "").
//   - '+' matches exactly one level of any content.
//   - '#' matches the remainder (any number of levels, including zero); it
//     is legal only as the final level. The prefix must be "/": filter "a/#"
//     matches "a" (zero remaining levels), "a/x", "a/x/y" but NOT "ab".
//   - A '$'-prefixed name (the server's namespaced space) does NOT match
//     wildcard filters whose first level is '+' or '#' (MQTT §4.7, "Topics
//     beginning with $"). Literal filters can still match.
//
// This broker never publishes to '$' topics, but the rule is implemented and
// tested for explicit boundary behaviour.
func Match(filter, name string) bool {
	if !validFilterLevels(filter) || name == "" {
		return false
	}
	fLevels := splitLevels(filter)
	nLevels := splitLevels(name)

	// $ namespacing: wildcard-first filters cannot match '$' topics.
	if len(name) > 0 && name[0] == '$' && len(fLevels) > 0 &&
		(fLevels[0] == "+" || fLevels[0] == "#") {
		return false
	}

	for i, f := range fLevels {
		if f == "#" {
			// Last level guaranteed by validation. Remaining name levels
			// are unconstrained; the parent level was already matched.
			return true
		}
		if i >= len(nLevels) {
			return false
		}
		if f == "+" {
			continue // matches exactly this one level
		}
		if f != nLevels[i] {
			return false
		}
	}
	// Filter exhausted: exact level count required (no trailing '#').
	return len(fLevels) == len(nLevels)
}

// validFilterLevels checks wildcard placement structurally (same rules as
// packet.ValidTopicFilter, kept local so the topic package has no inbound
// dependency on the codec package).
func validFilterLevels(f string) bool {
	if f == "" {
		return false
	}
	start := 0
	for i := 0; i <= len(f); i++ {
		if i != len(f) && f[i] != '/' {
			continue
		}
		level := f[start:i]
		if level == "#" {
			if i != len(f) {
				return false
			}
		} else if level != "+" {
			for j := start; j < i; j++ {
				if f[j] == '+' || f[j] == '#' {
					return false
				}
			}
		}
		start = i + 1
	}
	return true
}
