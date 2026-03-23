package vjson

import (
	"encoding/json"
	"testing"
)

// Low-level: appendEscapedString tests

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
		{"null_byte_before_digits", "0\x00 00000"},
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
		{"html_passthrough", "<>&", `"\u003c\u003e\u0026"`},
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
			got := string(appendEscapedString(nil, tc.s, escapeStdCompat))
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

// Marshal-level escape tests

// TestMarshal_EscapeStdCompat verifies that Marshal with WithStdCompat()
// produces identical output to encoding/json for all flag combinations.
func TestMarshal_EscapeStdCompat(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		// ASCII
		{"ascii_plain", "hello world"},
		{"ascii_quote", `say "hello"`},
		{"ascii_backslash", `path\to\file`},
		{"ascii_control_chars", "\x00\x01\x02\x03\x04\x05\x06\x07"},
		{"ascii_short_escapes", "\b\t\n\f\r"},
		// 16-byte control chars: exercises SIMD simd_tail path with remaining==16.
		{"ctrl_16_all", "\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f"},
		{"ctrl_only_0x1f", "\x1f"},
		{"ctrl_0x1f_in_simd_tail", "abcdefghij\x1f"},
		{"ctrl_0x1f_boundary", "abcdefghijklmno\x1f"},
		// Null byte followed by space + digits: \u0000 must not swallow subsequent chars.
		{"null_byte_before_digits", "0\x00 00000"},
		{"null_byte_before_hex", "\x00a"},
		{"null_byte_alone", "\x00"},
		{"null_byte_between_zeros", "0\x000"},
		// Length boundary tests
		{"null_byte_len7", "0\x00 0000"},
		{"null_byte_len8", "0\x00 00000"},
		{"null_byte_len17", "0\x00 00000000000000"},

		// Surrogate byte sequences
		{"surrogate_3byte", "\xed\xa0\x80"},
		{"surrogate_high", "\xed\xb0\x80"},
		{"surrogate_in_text", "a\xed\xa0\x80b"},

		// HTML
		{"html_lt", "<script>alert(1)</script>"},
		{"html_gt", "a>b"},
		{"html_amp", "a&b"},
		{"html_mixed", `<a href="x">&</a>`},

		// Line terminators (U+2028, U+2029)
		{"line_sep", "a\u2028b"},
		{"para_sep", "a\u2029b"},
		{"both_seps", "\u2028\u2029"},
		{"sep_at_start", "\u2028hello"},
		{"sep_at_end", "hello\u2029"},
		{"sep_consecutive", "\u2028\u2028\u2028"},

		// Non-ASCII / UTF-8
		{"chinese", "中文测试"},
		{"japanese", "日本語テスト"},
		{"korean", "한국어"},
		{"emoji", "\U0001F600\U0001F4A9"},
		{"mixed_cjk", "hello中文world日本語"},

		// Invalid UTF-8
		{"invalid_0xff", "abc\xffdef"},
		{"invalid_truncated_2byte", "abc\xc0def"},
		{"invalid_truncated_3byte", "abc\xe0\x80def"},
		{"invalid_truncated_4byte", "abc\xf0\x80\x80def"},
		{"invalid_continuation", "abc\x80def"},
		{"invalid_overlong_2byte", "\xc0\xaf"},

		// Mixed
		{"mixed_all", "hello\n\t\"world\"\x00<>&\u2028\xff中文\U0001F600"},
		{"empty", ""},

		// Long strings (exercise SIMD paths)
		{"long_ascii", longString('a', 256)},
		{"long_chinese", longUTF8String("中文", 128)},
		{"long_with_sep", longStringWithSep(128)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}
			s := S{V: tc.s}

			got, err := Marshal(s, WithStdCompat())
			if err != nil {
				t.Fatalf("velox error: %v", err)
			}

			stdOut, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("stdlib error: %v", err)
			}

			if string(got) != string(stdOut) {
				t.Errorf("mismatch with stdlib:\n  input:  %q\n  stdlib: %s\n  velox:  %s", tc.s, stdOut, got)
			}
		})
	}
}

func TestMarshal_DefaultEscapesSafe(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	s := S{V: "abc\xffdef"}

	got, err := Marshal(s, WithStdCompat())
	if err != nil {
		t.Fatal(err)
	}

	want := `{"v":"abc\ufffddef"}`
	if string(got) != want {
		t.Errorf("want: %s\n got: %s", want, got)
	}
}

// TestMarshal_EscapeDefault verifies the default Marshal behavior (fast mode, flags=0).
// Only mandatory JSON escapes (control chars, '"', '\\'). HTML, line terminators,
// and invalid UTF-8 all pass through.
func TestMarshal_EscapeDefault(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"ascii", "hello", `{"v":"hello"}`},
		{"html_passthrough", "<>&", `{"v":"<>&"}`},
		{"line_sep_passthrough", "a\u2028b", "{\"v\":\"a\xe2\x80\xa8b\"}"},
		{"para_sep_passthrough", "a\u2029b", "{\"v\":\"a\xe2\x80\xa9b\"}"},
		{"invalid_utf8_passthrough", "a\xffb", "{\"v\":\"a\xffb\"}"},
		{"chinese", "中文", `{"v":"中文"}`},
		{"quote", `a"b`, `{"v":"a\"b"}`},
		{"control", "\n\t", `{"v":"\n\t"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}
			got, err := Marshal(S{V: tc.s})
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %s\n  got:   %s", tc.s, tc.want, got)
			}
		})
	}
}

// TestMarshal_EscapeLineTerms verifies WithEscapeLineTerms escapes U+2028/U+2029
// while leaving HTML and invalid UTF-8 untouched.
func TestMarshal_EscapeLineTerms(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"ascii", "hello", `{"v":"hello"}`},
		{"html_passthrough", "<>&", `{"v":"<>&"}`},
		{"line_sep", "a\u2028b", `{"v":"a\u2028b"}`},
		{"para_sep", "a\u2029b", `{"v":"a\u2029b"}`},
		{"both_seps", "\u2028\u2029", `{"v":"\u2028\u2029"}`},
		{"invalid_utf8_passthrough", "a\xffb", "{\"v\":\"a\xffb\"}"},
		{"chinese", "中文", `{"v":"中文"}`},
		{"quote", `a"b`, `{"v":"a\"b"}`},
		{"control", "\n\t", `{"v":"\n\t"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}
			got, err := Marshal(S{V: tc.s}, WithEscapeLineTerms())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %s\n  got:   %s", tc.s, tc.want, got)
			}
		})
	}
}

// TestMarshal_EscapeFastPath verifies WithFastEscape (flags=0, VMExecFast path).
// Only mandatory JSON escapes: control chars, '"', '\\'. Everything else passes through.
func TestMarshal_EscapeFastPath(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"ascii", "hello", `{"v":"hello"}`},
		{"html_passthrough", "<>&", `{"v":"<>&"}`},
		{"line_sep_passthrough", "a\u2028b", "{\"v\":\"a\xe2\x80\xa8b\"}"},
		{"invalid_utf8_passthrough", "a\xffb", "{\"v\":\"a\xffb\"}"},
		{"chinese", "中文", `{"v":"中文"}`},
		{"quote", `a"b`, `{"v":"a\"b"}`},
		{"backslash", `a\b`, `{"v":"a\\b"}`},
		{"control", "\n\t\x00", `{"v":"\n\t\u0000"}`},
		// Null byte followed by space + digits: regression for \u0000 swallowing chars.
		{"null_byte_before_digits", "0\x00 00000", "{\"v\":\"0\\u0000 00000\"}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}
			got, err := Marshal(S{V: tc.s}, WithFastEscape())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %q\n  got:   %q", tc.s, tc.want, got)
			}
		})
	}
}

// TestMarshal_EscapeUTF8Only verifies UTF-8 correction without HTML or line term escaping.
func TestMarshal_EscapeUTF8Only(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"valid_ascii", "hello", `{"v":"hello"}`},
		{"valid_chinese", "中文", `{"v":"中文"}`},
		{"html_passthrough", "<>&", `{"v":"<>&"}`},
		{"line_sep_passthrough", "a\u2028b", "{\"v\":\"a\xe2\x80\xa8b\"}"},
		{"invalid_0xff", "a\xffb", `{"v":"a\ufffdb"}`},
		// Surrogate bytes: each byte replaced individually (matching stdlib).
		{"lone_surrogate", "\xed\xa0\x80", `{"v":"\ufffd\ufffd\ufffd"}`},
		{"truncated_2byte", "\xc0x", `{"v":"\ufffdx"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}
			got, err := Marshal(S{V: tc.s}, WithFastEscape(), WithUTF8Correction())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %q\n  got:   %q", tc.s, tc.want, got)
			}
		})
	}
}

// TestMarshal_EscapeHTMLOnly verifies HTML escaping without UTF-8 correction or line terms.
func TestMarshal_EscapeHTMLOnly(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"lt", "<", `{"v":"\u003c"}`},
		{"gt", ">", `{"v":"\u003e"}`},
		{"amp", "&", `{"v":"\u0026"}`},
		{"no_html", "hello", `{"v":"hello"}`},
		{"invalid_utf8_passthrough", "a\xffb", "{\"v\":\"a\xffb\"}"},
		{"line_sep_passthrough", "a\u2028b", "{\"v\":\"a\xe2\x80\xa8b\"}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}
			got, err := Marshal(S{V: tc.s}, WithFastEscape(), WithEscapeHTML())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %q\n  got:   %q", tc.s, tc.want, got)
			}
		})
	}
}

// TestMarshal_EscapeLongStrings exercises SIMD paths with longer payloads
// across different flag combinations.
func TestMarshal_EscapeLongStrings(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}

	// Long CJK string with a line separator buried in the middle.
	cjk := longUTF8String("你好世界", 100)
	cjkWithSep := cjk[:150] + "\u2028" + cjk[150:]

	// Long ASCII with HTML chars sprinkled in.
	ascii := longString('x', 200)
	asciiHTML := ascii[:50] + "<" + ascii[50:100] + ">" + ascii[100:150] + "&" + ascii[150:]

	t.Run("StdCompat_long_cjk_with_sep", func(t *testing.T) {
		got, err := Marshal(S{V: cjkWithSep}, WithStdCompat())
		if err != nil {
			t.Fatal(err)
		}
		stdOut, _ := json.Marshal(S{V: cjkWithSep})
		if string(got) != string(stdOut) {
			t.Errorf("mismatch with stdlib for long CJK+sep")
		}
	})

	t.Run("StdCompat_long_ascii_html", func(t *testing.T) {
		got, err := Marshal(S{V: asciiHTML}, WithStdCompat())
		if err != nil {
			t.Fatal(err)
		}
		stdOut, _ := json.Marshal(S{V: asciiHTML})
		if string(got) != string(stdOut) {
			t.Errorf("mismatch with stdlib for long ASCII+HTML")
		}
	})

	t.Run("Default_long_cjk_with_sep", func(t *testing.T) {
		got, err := Marshal(S{V: cjkWithSep})
		if err != nil {
			t.Fatal(err)
		}
		// Default is fast mode: line terms should NOT be escaped
		if containsBytes(got, []byte(`\u2028`)) {
			t.Errorf("unexpected line term escaping in default mode: %s", got)
		}
		// HTML chars should NOT be escaped
		if containsBytes(got, []byte(`\u003c`)) {
			t.Errorf("unexpected HTML escaping in default mode")
		}
	})

	t.Run("FastEscape_long_passthrough", func(t *testing.T) {
		got, err := Marshal(S{V: cjkWithSep}, WithFastEscape())
		if err != nil {
			t.Fatal(err)
		}
		// Line seps should NOT be escaped in fast mode
		if containsBytes(got, []byte(`\u2028`)) {
			t.Errorf("unexpected line term escaping in fast mode: %s", got)
		}
	})
}

// TestMarshal_EscapeLineTermTrailingBoundary verifies that U+2028/U+2029 at the
// very end of a string is correctly escaped.
func TestMarshal_EscapeLineTermTrailingBoundary(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		// Pure line terminator only
		{"only_2028", "\u2028"},
		{"only_2029", "\u2029"},

		// ASCII prefix + trailing line terminator (scalar tail path)
		{"ascii3_trailing_2028", "abc\u2028"},
		{"ascii3_trailing_2029", "xyz\u2029"},

		// Non-ASCII prefix + trailing line terminator
		{"chinese_trailing_2028", "中\u2028"},
		{"chinese_trailing_2029", "日\u2029"},

		// Exactly 16 bytes of ASCII before trailing U+2028 (SIMD processes 16, tail=3)
		{"simd16_trailing_2028", "0123456789abcdef\u2028"},
		{"simd16_trailing_2029", "0123456789abcdef\u2029"},

		// 15 bytes ASCII + U+2028 = 18 bytes total (SIMD=16, tail=2+3=5, but U+2028 at byte 15)
		{"simd15_trailing_2028", "0123456789abcde\u2028"},

		// 13 bytes ASCII + U+2028 = 16 bytes (fits in one SIMD load, 0xE2 at pos 13)
		{"simd13_trailing_2028", "0123456789abc\u2028"},

		// Edge case: two consecutive line terminators at end
		{"trailing_both", "test\u2028\u2029"},

		// Long string ending with line terminator (multiple SIMD iterations)
		{"long_trailing_2028", longString('a', 100) + "\u2028"},
		{"long_trailing_2029", longString('b', 100) + "\u2029"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}

			// Test with WithEscapeLineTerms (line terms escaped, HTML/UTF8 passthrough)
			got, err := Marshal(S{V: tc.input}, WithEscapeLineTerms())
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}

			// Verify: output must NOT contain raw U+2028 (E2 80 A8) or U+2029 (E2 80 A9)
			if containsBytes(got, []byte{0xE2, 0x80, 0xA8}) {
				t.Errorf("U+2028 not escaped (raw bytes found):\n  input: %q\n  got:   %s", tc.input, got)
			}
			if containsBytes(got, []byte{0xE2, 0x80, 0xA9}) {
				t.Errorf("U+2029 not escaped (raw bytes found):\n  input: %q\n  got:   %s", tc.input, got)
			}

			// Verify: output must contain the escaped form \u2028 or \u2029
			has2028 := containsBytes([]byte(tc.input), []byte{0xE2, 0x80, 0xA8})
			has2029 := containsBytes([]byte(tc.input), []byte{0xE2, 0x80, 0xA9})
			if has2028 && !containsBytes(got, []byte(`\u2028`)) {
				t.Errorf("U+2028 escape sequence missing:\n  input: %q\n  got:   %s", tc.input, got)
			}
			if has2029 && !containsBytes(got, []byte(`\u2029`)) {
				t.Errorf("U+2029 escape sequence missing:\n  input: %q\n  got:   %s", tc.input, got)
			}
		})
	}
}

// Helpers

func longString(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func longUTF8String(unit string, repeats int) string {
	b := make([]byte, 0, len(unit)*repeats)
	for range repeats {
		b = append(b, unit...)
	}
	return string(b)
}

func longStringWithSep(n int) string {
	b := make([]byte, 0, n*4)
	for i := range n {
		b = append(b, "abc"...)
		if i%10 == 5 {
			b = append(b, '\xe2', '\x80', '\xa8') // U+2028
		}
	}
	return string(b)
}

func containsBytes(haystack, needle []byte) bool {
	for i := range len(haystack) - len(needle) + 1 {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
