package packet

import (
	"unicode/utf8"
)

// ValidMQTTUTF8 validates an MQTT "UTF-8 Encoded String" body per
// MQTT 3.1.1 §1.5.3: valid UTF-8 (non-shortest forms rejected by
// unicode/utf8), with code points U+0000..U+001F and U+007F..U+009F
// forbidden. The 65535-byte length cap is enforced structurally by the
// 2-byte prefix; callers decode through mqttString so this checks content.
func ValidMQTTUTF8(b []byte) bool {
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if r <= 0x1F || (r >= 0x7F && r <= 0x9F) {
			return false
		}
		b = b[size:]
	}
	return true
}

// ValidTopicName validates a PUBLISH topic name (MQTT 3.1.1 §4.7.3):
//   - non-empty,
//   - no NUL / wildcard characters ('+' '#'),
//   - '/' is a legal level separator at any position,
//   - content must be valid MQTT UTF-8.
func ValidTopicName(name string) bool {
	if name == "" {
		return false
	}
	if !ValidMQTTUTF8([]byte(name)) {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '+' || c == '#' || c == 0 {
			return false
		}
	}
	return true
}

// ValidTopicFilter validates a SUBSCRIBE filter (MQTT 3.1.1 §4.7.1):
//   - non-empty,
//   - '+' occupies a whole level,
//   - '#' is either alone or the final character after '/', occupying the
//     final level,
//   - content is valid MQTT UTF-8.
func ValidTopicFilter(f string) bool {
	if f == "" {
		return false
	}
	if !ValidMQTTUTF8([]byte(f)) {
		return false
	}
	levelStart := 0
	for i := 0; i <= len(f); i++ {
		if i != len(f) && f[i] != '/' {
			continue
		}
		level := f[levelStart:i]
		switch {
		case level == "#":
			if i != len(f) {
				return false // '#' must be the last level
			}
		case level == "+":
			// whole-level wildcard: fine.
		default:
			for j := levelStart; j < i; j++ {
				if f[j] == '+' || f[j] == '#' || f[j] == 0 {
					return false // wildcard embedded in a level
				}
			}
		}
		levelStart = i + 1
	}
	return true
}
