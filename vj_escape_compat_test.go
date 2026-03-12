package vjson

import (
	"encoding/json"
	"testing"
)

func TestEscape_StdCompat(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		{"ascii", "hello world"},
		{"chinese", "中文"},
		{"emoji", "\U0001F600"},
		{"html_lt", "<script>"},
		{"html_gt", "foo>bar"},
		{"html_amp", "foo&bar"},
		{"U+2028", "a\u2028b"},
		{"U+2029", "a\u2029b"},
		{"invalid_utf8_0xFF", "abc\xffdef"},
		{"invalid_utf8_truncated", "abc\xc0def"},
		{"lone_surrogate", "abc\xed\xa0\x80def"},
		{"null_byte", "abc\x00def"},
		{"mixed", "hello\n\t\"world\"\x00<>&\u2028"},
		{"all_control", "\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f"},
		{"surrogate_pair_bytes", "\xed\xa0\xbd\xed\xb8\x80"},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdOut, err := json.Marshal(tc.s)
			if err != nil {
				t.Fatalf("stdlib error: %v", err)
			}
			got := appendEscapedString(nil, tc.s, escapeStdCompat)
			if string(got) != string(stdOut) {
				t.Errorf("mismatch:\n  input:  %q\n  stdlib: %s\n  velox:  %s", tc.s, stdOut, got)
			}
		})
	}
}

func TestEscape_Default(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"ascii", "hello", `"hello"`},
		{"chinese", "中文", `"中文"`},
		{"html_passthrough", "<>&", `"<>&"`},
		{"U+2028", "a\u2028b", `"a\u2028b"`},
		{"U+2029", "a\u2029b", `"a\u2029b"`},
		{"invalid_utf8", "a\xffb", `"a\ufffdb"`},
		{"lone_surrogate", "\xed\xa0\x80", `"\ufffd\ufffd\ufffd"`},
		{"null", "\x00", `"\u0000"`},
		{"backslash", `a\b`, `"a\\b"`},
		{"quote", `a"b`, `"a\"b"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(appendEscapedString(nil, tc.s, escapeDefault))
			if got != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %s\n  got:   %s", tc.s, tc.want, got)
			}
		})
	}
}

func TestEscape_NoFlags(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"ascii", "hello", `"hello"`},
		{"html_passthrough", "<>&", `"<>&"`},
		{"U+2028_passthrough", "a\u2028b", "\"a\u2028b\""},
		{"invalid_utf8_passthrough", "a\xffb", "\"a\xffb\""},
		{"null", "\x00", `"\u0000"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(appendEscapedString(nil, tc.s, 0))
			if got != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %q\n  got:   %q", tc.s, tc.want, got)
			}
		})
	}
}

func TestEscape_HTMLOnly(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"lt", "<", `"\u003c"`},
		{"gt", ">", `"\u003e"`},
		{"amp", "&", `"\u0026"`},
		{"mixed", "a<b>c&d", `"a\u003cb\u003ec\u0026d"`},
		{"no_html", "hello", `"hello"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(appendEscapedString(nil, tc.s, escapeHTML))
			if got != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %s\n  got:   %s", tc.s, tc.want, got)
			}
		})
	}
}

func TestMarshal_WithStdCompat(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	s := S{V: "<a>&\u2028\xffend"}

	got, err := Marshal(&s, WithStdCompat())
	if err != nil {
		t.Fatal(err)
	}

	stdOut, _ := json.Marshal(s)
	if string(got) != string(stdOut) {
		t.Errorf("mismatch:\n  stdlib: %s\n  velox:  %s", stdOut, got)
	}
}

func TestMarshal_DefaultEscapesSafe(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	s := S{V: "abc\xffdef"}

	got, err := Marshal(&s)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"v":"abc\ufffddef"}`
	if string(got) != want {
		t.Errorf("want: %s\n got: %s", want, got)
	}
}
