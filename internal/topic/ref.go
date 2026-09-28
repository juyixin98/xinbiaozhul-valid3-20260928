package topic

// ReferenceMatch is an INDEPENDENT second implementation of MQTT 3.1.1
// §4.7 filter matching, used only as a test oracle. It deliberately uses a
// different technique than Match: a single left-to-right byte walk, no level
// slicing and no shared helpers.
//
// Semantics (derived independently from the spec):
//
//   - '/' separates levels; leading/trailing separators create empty levels.
//   - '+' matches exactly one level of any content.
//   - '#' is legal only as the final level and matches any number of
//     remaining levels INCLUDING ZERO, so "a/#" matches "a" and "+/#"
//     matches "b" (the '+' consumed the only level, '#' matches nothing).
//   - '$'-prefixed names are not matched by filters beginning with '+'/'#'.
func ReferenceMatch(filter, name string) bool {
	if len(filter) == 0 || len(name) == 0 {
		return false
	}
	if !refFilterOK(filter) {
		return false
	}
	if name[0] == '$' && (filter[0] == '+' || filter[0] == '#') {
		return false
	}

	fi, ni := 0, 0
	for fi < len(filter) {
		switch filter[fi] {
		case '#':
			// Only reached as a whole level (validated); accepts any
			// suffix including empty.
			return true
		case '+':
			for ni < len(name) && name[ni] != '/' {
				ni++
			}
			fi++
			if fi == len(filter) {
				return ni == len(name)
			}
			if filter[fi] != '/' {
				return false
			}
			if ni == len(name) {
				// Name ends here; valid only if filter remainder is "/#".
				return fi == len(filter)-2 && filter[fi+1] == '#'
			}
			if name[ni] != '/' {
				return false
			}
			fi++
			ni++
		default:
			// Literal level: compare byte-for-byte to the boundary.
			for fi < len(filter) && !refIsLevelChar(filter[fi]) {
				if ni >= len(name) || name[ni] != filter[fi] {
					return false
				}
				fi++
				ni++
			}
			var fCh, nCh byte
			if fi < len(filter) {
				fCh = filter[fi]
			}
			if ni < len(name) {
				nCh = name[ni]
			}
			fEnd := fi == len(filter) || fCh == '/' || fCh == '+' || fCh == '#'
			nEnd := ni == len(name) || nCh == '/'
			if !fEnd || !nEnd {
				return false
			}
			if fCh == '+' || fCh == '#' {
				continue // next iteration handles the wildcard token
			}
			if fi == len(filter) {
				return ni == len(name)
			}
			// fCh == '/': filter continues.
			if ni == len(name) {
				// Name ends; match only when the rest of the filter is "/#".
				return fi == len(filter)-2 && filter[fi+1] == '#'
			}
			fi++
			ni++
		}
	}
	return ni == len(name)
}

func refIsLevelChar(b byte) bool { return b == '/' || b == '+' || b == '#' }

// refFilterOK: structural wildcard placement, independently phrased as a
// per-character adjacency check.
func refFilterOK(f string) bool {
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case '#':
			if i > 0 && f[i-1] != '/' {
				return false
			}
			if i != len(f)-1 {
				return false
			}
		case '+':
			if i > 0 && f[i-1] != '/' {
				return false
			}
			if i != len(f)-1 && f[i+1] != '/' {
				return false
			}
		}
	}
	return true
}
