package vjson

import (
	"testing"
)

// testUnescape is a test helper that wraps unescapeSinglePass.
// It appends a closing '"' to the input and calls unescapeSinglePass,
// returning the decoded string.
func testUnescape(input string) (string, int) {
	// Wrap with closing quote so unescapeSinglePass can find it
	src := []byte(input + `"`)
	sc := &Parser{}
	// Find first backslash
	firstEsc := len(input) // default: no backslash (prefix is entire input)
	for i := 0; i < len(input); i++ {
		if input[i] == '\\' {
			firstEsc = i
			break
		}
	}
	_, result, err := sc.unescapeSinglePass(src, 0, firstEsc)
	if err != nil {
		return "", 0
	}
	return string(result), len(result)
}

// testUnescapeRange is a test helper for range-based unescape tests.
func testUnescapeRange(src string, start, end int) (string, int) {
	// Insert a closing quote at the end position
	b := []byte(src[:end])
	b = append(b, '"')
	b = append(b, src[end:]...)
	sc := &Parser{}
	// Find first backslash in [start, end)
	firstEsc := end
	for i := start; i < end; i++ {
		if b[i] == '\\' {
			firstEsc = i
			break
		}
	}
	_, result, err := sc.unescapeSinglePass(b, start, firstEsc)
	if err != nil {
		return "", 0
	}
	return string(result), len(result)
}

func TestUnescape(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantLen int
	}{
		// Basic escapes
		{"empty", "", "", 0},
		{"no escapes", "hello world", "hello world", 11},
		{"escaped quote", `hello \"world\"`, `hello "world"`, 12},
		{"escaped backslash", `path\\to\\file`, `path\to\file`, 12},
		{"escaped slash", `\/path\/to`, `/path/to`, 8},

		// Control character escapes
		{"newline", `hello\nworld`, "hello\nworld", 11},
		{"carriage return", `hello\rworld`, "hello\rworld", 11},
		{"tab", `hello\tworld`, "hello\tworld", 11},
		{"backspace", `hello\bworld`, "hello\bworld", 11},
		{"form feed", `hello\fworld`, "hello\fworld", 11},

		// Unicode escapes
		{"unicode basic", `\u0041`, "A", 1},
		{"unicode chinese", `\u4e2d\u6587`, "中文", 6},
		{"unicode in string", `hello\u0020world`, "hello world", 11},
		{"unicode null", `\u0000`, "\x00", 1},

		// Mixed escapes
		{"mixed escapes", `line1\nline2\t\"quoted\"`, "line1\nline2\t\"quoted\"", 18},
		{"multiple backslashes", `a\\b\\c`, `a\b\c`, 5},
		{"adjacent escapes", `\n\t\r`, "\n\t\r", 3},

		// Edge cases
		{"double backslash at end", `hello\\`, `hello\`, 6},
		{"incomplete unicode", `\u041`, `\u041`, 5},    // not enough hex digits
		{"invalid unicode hex", `\uXXXX`, `\uXXXX`, 6}, // non-hex chars preserved

		// Unknown escapes (should preserve)
		{"unknown escape", `hello\Xworld`, `hello\Xworld`, 12},
		{"unknown escape x", `\x41`, `\x41`, 4},

		// Long strings
		{"long no escapes", makeLongString(100), makeLongString(100), 100},
		{"long with escapes", makeLongStringWithEscapes(50), makeExpectedLongWithEscapes(50), 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotLen := testUnescape(tt.input)

			if got != tt.want {
				t.Errorf("unescape(%q) = %q (len=%d), want %q (len=%d)",
					tt.input, got, gotLen, tt.want, len(tt.want))
			}
			if gotLen != len(tt.want) {
				t.Errorf("unescape(%q) returned length %d, want %d",
					tt.input, gotLen, len(tt.want))
			}
		})
	}
}

func TestUnescapeWithRange(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		start int
		end   int
		want  string
	}{
		// Note: end index is exclusive, consistent with Go slice semantics
		{"full string", `hello\"world\"`, 0, 14, `hello"world"`},
		{"partial string", `prefix\"content\"suffix`, 7, 17, `"content"`},
		// "xxx\"yyy\"zzz" -> indices: x=0,1,2, \"=3,4, y=5,6,7, \"=8,9, z=10,11,12
		// Range [3,10) = \"yyy\" -> unescaped: "yyy"
		{"middle only", `xxx\"yyy\"zzz`, 3, 10, `"yyy"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := testUnescapeRange(tt.src, tt.start, tt.end)

			if got != tt.want {
				t.Errorf("unescape(%q, %d, %d) = %q, want %q",
					tt.src, tt.start, tt.end, got, tt.want)
			}
		})
	}
}

func TestEscapeTable(t *testing.T) {
	// Verify escape table correctness
	expected := map[byte]byte{
		'"':  '"',
		'\\': '\\',
		'/':  '/',
		'b':  '\b',
		'f':  '\f',
		'n':  '\n',
		'r':  '\r',
		't':  '\t',
	}

	for ch, want := range expected {
		if got := escapeTable[ch]; got != want {
			t.Errorf("escapeTable[%q] = %q, want %q", ch, got, want)
		}
	}

	// Verify unknown escapes return 0
	unknownChars := []byte{'a', 'z', '0', '9', ' ', 'X'}
	for _, ch := range unknownChars {
		if got := escapeTable[ch]; got != 0 {
			t.Errorf("escapeTable[%q] = %q, want 0", ch, got)
		}
	}
}

// Benchmark
func BenchmarkUnescape(b *testing.B) {
	inputs := []struct {
		name  string
		input string
	}{
		{"no escapes", "hello world this is a test string"},
		{"few escapes", `hello\nworld\ttest`},
		{"many escapes", `line1\nline2\tline3\rline4\"quoted\"\n\\slash\\`},
		{"unicode heavy", `\u4e2d\u6587\u6d4b\u8bd5\u6587\u672c`},
	}

	for _, inp := range inputs {
		b.Run(inp.name, func(b *testing.B) {
			// Wrap with closing quote for unescapeSinglePass
			src := []byte(inp.input + `"`)
			firstEsc := len(inp.input)
			for i := 0; i < len(inp.input); i++ {
				if inp.input[i] == '\\' {
					firstEsc = i
					break
				}
			}
			b.SetBytes(int64(len(inp.input)))
			sc := &Parser{}
			for b.Loop() {
				sc.arenaOff = 0 // reset arena between iterations
				_, _, _ = sc.unescapeSinglePass(src, 0, firstEsc)
			}
		})
	}
}

// Helper functions
func makeLongString(n int) string {
	result := make([]byte, n)
	for i := 0; i < n; i++ {
		result[i] = 'a' + byte(i%26)
	}
	return string(result)
}

func makeLongStringWithEscapes(n int) string {
	result := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i%5 == 0 {
			result = append(result, '\\', 'n')
		} else {
			result = append(result, 'a'+byte(i%26))
		}
	}
	return string(result)
}

func makeExpectedLongWithEscapes(n int) string {
	result := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		if i%5 == 0 {
			result = append(result, '\n')
		} else {
			result = append(result, 'a'+byte(i%26))
		}
	}
	return string(result)
}
