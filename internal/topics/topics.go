// Package topics implements MQTT 3.1.1 topic name / topic filter validation
// and wildcard matching (section 4.7).
//
// Rules implemented:
//   - Levels are separated by '/'; a trailing '/' creates a final empty level.
//   - '+' matches exactly one level (which may be empty, e.g. "a/" vs "a/+").
//   - '#' matches zero or more levels and may only be the last level, and it
//     occupies that whole level ("sport#" is invalid; "sport/#" is valid).
//   - Topic names (used in PUBLISH) must contain no wildcards.
//   - Topics beginning with '$' ("$SYS/...") are a server namespace: wildcard
//     filters never match them (section 4.7.2). An exact filter may.
package topics

import "strings"

// MaxTopicLen is the MQTT wire ceiling for a topic string (2-byte length).
const MaxTopicLen = 65535

// ValidateName checks a PUBLISH topic name: non-empty, valid UTF-8, no
// wildcards, wire-length bound, no embedded NUL.
func ValidateName(name string) error {
	if name == "" {
		return errf("topic name must not be empty")
	}
	if len(name) > MaxTopicLen {
		return errf("topic name longer than %d bytes", MaxTopicLen)
	}
	if strings.IndexByte(name, 0) >= 0 {
		return errf("topic name must not contain NUL")
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '+' || name[i] == '#' {
			return errf("topic name must not contain wildcard %q", name[i])
		}
	}
	return nil
}

// ValidateFilter checks a subscription topic filter.
func ValidateFilter(filter string) error {
	if filter == "" {
		return errf("topic filter must not be empty")
	}
	if len(filter) > MaxTopicLen {
		return errf("topic filter longer than %d bytes", MaxTopicLen)
	}
	if strings.IndexByte(filter, 0) >= 0 {
		return errf("topic filter must not contain NUL")
	}
	for i := 0; i < len(filter); i++ {
		ch := filter[i]
		switch ch {
		case '#':
			// '#' must be the final character and occupy a full level.
			if i != len(filter)-1 {
				return errf("'#' must be the final level")
			}
			if i > 0 && filter[i-1] != '/' {
				return errf("'#' must occupy a whole level")
			}
		case '+':
			// '+' must occupy a whole level.
			if i > 0 && filter[i-1] != '/' {
				return errf("'+' must occupy a whole level")
			}
			if i < len(filter)-1 && filter[i+1] != '/' {
				return errf("'+' must occupy a whole level")
			}
		}
	}
	return nil
}

// Match reports whether a validated subscription filter matches a validated
// concrete topic name. This is the production implementation: it splits both
// strings into level slices and performs an iterative level comparison.
func Match(filter, topic string) bool {
	// Wildcard filters do not match the '$' server namespace (4.7.2).
	if (filter[0] == '+' || filter[0] == '#') && topic[0] == '$' {
		return false
	}
	f := strings.Split(filter, "/")
	t := strings.Split(topic, "/")
	i := 0
	for i < len(f) {
		if f[i] == "#" {
			// '#' is validated to be last; it absorbs the remaining levels.
			return true
		}
		if i >= len(t) {
			return false // filter has more levels than the topic
		}
		if f[i] != "+" && f[i] != t[i] {
			return false
		}
		i++
	}
	return i == len(t)
}
