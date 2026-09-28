package httpx

// This file contains the ABNF-style character predicates used by the
// parser. References use the rule names of RFC 5234 and RFC 9110/9112.
//
//	HEXDIG = DIGIT / "A" / "B" / "C" / "D" / "E" / "F"
//	         / "a" / "b" / "c" / "d" / "e" / "f"
//	DIGIT  = %x30-39
//	SP     = %x20   (the only accepted request-line whitespace)
//	HTAB   = %x09
//	VCHAR  = %x21-7E
//	obs-text = %x80-FF
//	tchar  = "!" / "#" / "$" / "%" / "&" / "'" / "*" / "+" / "-" /
//	         "." / "^" / "_" / "`" / "|" / "~" / DIGIT / ALPHA
//	field-vchar = VCHAR / obs-text
//	field-content = field-vchar [ 1*( SP / HTAB ) field-vchar ]

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isHexDig(c byte) bool {
	return isDigit(c) ||
		(c >= 'a' && c <= 'f') ||
		(c >= 'A' && c <= 'F')
}

// isTchar reports whether c is a token character (method, field name
// subset, transfer-coding name characters).
func isTchar(c byte) bool {
	switch {
	case isAlpha(c), isDigit(c):
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.',
		'^', '_', '`', '|', '~':
		return true
	}
	return false
}

// isFieldVchar allows visible ASCII plus obs-text (0x80-0xFF).
func isFieldVchar(c byte) bool {
	return (c >= 0x21 && c <= 0x7e) || c >= 0x80
}

// isToken reports whether b is a non-empty 1*tchar sequence.
func isToken(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if !isTchar(c) {
			return false
		}
	}
	return true
}

// trimOWS strips optional whitespace (SP/HTAB) from both ends.
func trimOWS(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c != ' ' && c != '\t' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

// validFieldValue implements the strict field-value shape used here:
// field-content = field-vchar [ 1*( SP / HTAB ) field-vchar ]
// with NO obsolete line folding. The caller passes an OWS-trimmed
// value, so runs of SP/HTAB in the middle are legal but CR, LF, NUL
// and other control bytes are always rejected.
func validFieldValue(v []byte) bool {
	for _, c := range v {
		switch {
		case isFieldVchar(c), c == ' ', c == '\t':
		default:
			return false
		}
	}
	return true
}

// asciiEqualFold compares ASCII bytes case-insensitively.
func asciiEqualFold(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x >= 'a' && x <= 'z' {
			x -= 'a' - 'A'
		}
		if y >= 'a' && y <= 'z' {
			y -= 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
