package packet

import "testing"

func TestUTF8Validation(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"a/b/c", true},
		{"café/☕", true},
		{"a\x00b", false},
		{"a\x01b", false},
		{"a\x1fb", false},
		{"a\x20b", true}, // space allowed
		{"a\x7fb", false},
		{"a\x80b", false},       // continuation without start
		{"a\xc0\x80", false},    // overlong NUL
		{"\xed\xa0\x80", false}, // UTF-16 surrogate half
		{"a\x9fb", false},
		{"a\xc2\xa0b", true}, // U+00A0 (NO-BREAK SPACE) allowed
	}
	for _, tc := range cases {
		if got := ValidMQTTUTF8([]byte(tc.in)); got != tc.want {
			t.Errorf("ValidMQTTUTF8(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestTopicNamesAndFilters(t *testing.T) {
	goodNames := []string{"a", "a/b", "a//b", "/a", "a/", "sport/tennis/player1", "中文/主题"}
	for _, n := range goodNames {
		if !ValidTopicName(n) {
			t.Errorf("topic name should be valid: %q", n)
		}
	}
	badNames := []string{"", "a/+", "a/#", "a\x00b"}
	for _, n := range badNames {
		if ValidTopicName(n) {
			t.Errorf("topic name should be invalid: %q", n)
		}
	}
	goodFilters := []string{"a", "a/+", "#", "a/#", "+/b", "a/+/c", "$SYS/#", "/"}
	for _, f := range goodFilters {
		if !ValidTopicFilter(f) {
			t.Errorf("filter should be valid: %q", f)
		}
	}
	badFilters := []string{"", "a/#/b", "a/+x", "a/x#", "#/a"}
	for _, f := range badFilters {
		if ValidTopicFilter(f) {
			t.Errorf("filter should be invalid: %q", f)
		}
	}
}
