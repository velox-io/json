package vjson

import (
	"encoding/json"
	"testing"
)

// TestNativeEscape_StdCompat verifies that the native encoder (via Marshal)
// produces identical output to encoding/json for all flag combinations that
// encoding/json uses: HTML escaping + line terminator escaping + UTF-8 correction.
func TestNativeEscape_StdCompat(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		// --- ASCII ---
		{"ascii_plain", "hello world"},
		{"ascii_quote", `say "hello"`},
		{"ascii_backslash", `path\to\file`},
		{"ascii_control_chars", "\x00\x01\x02\x03\x04\x05\x06\x07"},
		{"ascii_short_escapes", "\b\t\n\f\r"},
		// 16-byte control chars: exercises SIMD simd_tail path with remaining==16.
		// Previously this was a known divergence (0x1F threshold bug in SIMD masks).
		{"ctrl_16_all", "\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f"},
		{"ctrl_only_0x1f", "\x1f"},                                  // single 0x1F
		{"ctrl_0x1f_in_simd_tail", "abcdefghij\x1f"},                // 0x1F in simd tail
		{"ctrl_0x1f_boundary", "abcdefghijklmno\x1f"},               // 16 bytes, 0x1F at pos 15
		// Surrogate byte sequences: each byte replaced individually (matching stdlib).
		{"surrogate_3byte", "\xed\xa0\x80"},       // U+D800 encoding → 3× \ufffd
		{"surrogate_high", "\xed\xb0\x80"},        // U+DC00 encoding → 3× \ufffd
		{"surrogate_in_text", "a\xed\xa0\x80b"},   // surrounded by ASCII

		// --- HTML ---
		{"html_lt", "<script>alert(1)</script>"},
		{"html_gt", "a>b"},
		{"html_amp", "a&b"},
		{"html_mixed", `<a href="x">&</a>`},

		// --- Line terminators (U+2028, U+2029) ---
		{"line_sep", "a\u2028b"},
		{"para_sep", "a\u2029b"},
		{"both_seps", "\u2028\u2029"},
		{"sep_at_start", "\u2028hello"},
		{"sep_at_end", "hello\u2029"},
		{"sep_consecutive", "\u2028\u2028\u2028"},

		// --- Non-ASCII / UTF-8 ---
		{"chinese", "中文测试"},
		{"japanese", "日本語テスト"},
		{"korean", "한국어"},
		{"emoji", "\U0001F600\U0001F4A9"},
		{"mixed_cjk", "hello中文world日本語"},

		// --- Invalid UTF-8 ---
		{"invalid_0xff", "abc\xffdef"},
		{"invalid_truncated_2byte", "abc\xc0def"},
		{"invalid_truncated_3byte", "abc\xe0\x80def"},
		{"invalid_truncated_4byte", "abc\xf0\x80\x80def"},
		{"invalid_continuation", "abc\x80def"},
		{"invalid_overlong_2byte", "\xc0\xaf"},

		// --- Mixed ---
		{"mixed_all", "hello\n\t\"world\"\x00<>&\u2028\xff中文\U0001F600"},
		{"empty", ""},

		// --- Long strings (exercise SIMD paths) ---
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

			got, err := Marshal(&s, WithStdCompat())
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

// TestNativeEscape_Default verifies the default Marshal behavior (escapeLineTerms only).
// HTML chars pass through, invalid UTF-8 passes through, but U+2028/2029 are escaped.
func TestNativeEscape_Default(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want string
	}{
		{"ascii", "hello", `{"v":"hello"}`},
		{"html_passthrough", "<>&", `{"v":"<>&"}`},
		{"line_sep", "a\u2028b", `{"v":"a\u2028b"}`},
		{"para_sep", "a\u2029b", `{"v":"a\u2029b"}`},
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
			got, err := Marshal(&S{V: tc.s})
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %s\n  got:   %s", tc.s, tc.want, got)
			}
		})
	}
}

// TestNativeEscape_FastPath verifies WithFastEscape (flags=0, VMExecFast path).
// Only mandatory JSON escapes: control chars, '"', '\\'. Everything else passes through.
func TestNativeEscape_FastPath(t *testing.T) {
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type S struct {
				V string `json:"v"`
			}
			got, err := Marshal(&S{V: tc.s}, WithFastEscape())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %q\n  got:   %q", tc.s, tc.want, got)
			}
		})
	}
}

// TestNativeEscape_UTF8Only verifies UTF-8 correction without HTML or line term escaping.
func TestNativeEscape_UTF8Only(t *testing.T) {
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
			got, err := Marshal(&S{V: tc.s}, WithFastEscape(), WithUTF8Correction())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %q\n  got:   %q", tc.s, tc.want, got)
			}
		})
	}
}

// TestNativeEscape_HTMLOnly verifies HTML escaping without UTF-8 correction or line terms.
func TestNativeEscape_HTMLOnly(t *testing.T) {
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
			got, err := Marshal(&S{V: tc.s}, WithFastEscape(), WithEscapeHTML())
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("mismatch:\n  input: %q\n  want:  %q\n  got:   %q", tc.s, tc.want, got)
			}
		})
	}
}

// TestNativeEscape_LongStrings exercises SIMD paths with longer payloads
// across different flag combinations.
func TestNativeEscape_LongStrings(t *testing.T) {
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
		got, err := Marshal(&S{V: cjkWithSep}, WithStdCompat())
		if err != nil {
			t.Fatal(err)
		}
		stdOut, _ := json.Marshal(S{V: cjkWithSep})
		if string(got) != string(stdOut) {
			t.Errorf("mismatch with stdlib for long CJK+sep")
		}
	})

	t.Run("StdCompat_long_ascii_html", func(t *testing.T) {
		got, err := Marshal(&S{V: asciiHTML}, WithStdCompat())
		if err != nil {
			t.Fatal(err)
		}
		stdOut, _ := json.Marshal(S{V: asciiHTML})
		if string(got) != string(stdOut) {
			t.Errorf("mismatch with stdlib for long ASCII+HTML")
		}
	})

	t.Run("Default_long_cjk_with_sep", func(t *testing.T) {
		got, err := Marshal(&S{V: cjkWithSep})
		if err != nil {
			t.Fatal(err)
		}
		// Default: line terms escaped, but no HTML, no UTF-8 correction
		if !containsBytes(got, []byte(`\u2028`)) {
			t.Errorf("expected \\u2028 in output: %s", got)
		}
		// HTML chars should NOT be escaped
		if containsBytes(got, []byte(`\u003c`)) {
			t.Errorf("unexpected HTML escaping in default mode")
		}
	})

	t.Run("FastEscape_long_passthrough", func(t *testing.T) {
		got, err := Marshal(&S{V: cjkWithSep}, WithFastEscape())
		if err != nil {
			t.Fatal(err)
		}
		// Line seps should NOT be escaped in fast mode
		if containsBytes(got, []byte(`\u2028`)) {
			t.Errorf("unexpected line term escaping in fast mode: %s", got)
		}
	})
}

// --- helpers ---

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
