package vjson

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// testUnescape is a test helper that wraps unescapeSinglePass.
// It appends a closing '"' to the input and calls unescapeSinglePass,
// returning the decoded string and any error.
func testUnescape(input string) (string, int, error) {
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
		return "", 0, err
	}
	return string(result), len(result), nil
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

		// Long strings
		{"long no escapes", makeLongString(100), makeLongString(100), 100},
		{"long with escapes", makeLongStringWithEscapes(50), makeExpectedLongWithEscapes(50), 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotLen, err := testUnescape(tt.input)
			if err != nil {
				t.Errorf("unescape(%q) unexpected error: %v", tt.input, err)
				return
			}

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

	// Test that invalid escapes are rejected per RFC 8259
	errorTests := []struct {
		name  string
		input string
	}{
		{"unknown escape", `hello\Xworld`},
		{"unknown escape x", `\x41`},
		{"incomplete unicode", `\u041`},
		{"invalid unicode hex", `\uXXXX`},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := testUnescape(tt.input)
			if err == nil {
				t.Errorf("unescape(%q) expected error, got nil", tt.input)
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

// TestUnescapeSequenceDirect tests unescapeSequence directly for paths
// not reached through unescapeSinglePass (which inlines common escapes).
func TestUnescapeSequenceDirect(t *testing.T) {
	t.Run("trailing backslash", func(t *testing.T) {
		// data has backslash at end, i+1 >= n
		data := []byte(`\`)
		dst := make([]byte, 10)
		_, _, err := unescapeSequence(data, len(data), 0, dst, 0)
		if err == nil {
			t.Fatal("expected error for trailing backslash")
		}
		if err != errUnexpectedEOF {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("common escape via unescapeSequence", func(t *testing.T) {
		// Feed a non-'u' escape directly to unescapeSequence (covers escapeTable lookup)
		data := []byte(`\n`)
		dst := make([]byte, 10)
		newI, newPos, err := unescapeSequence(data, len(data), 0, dst, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if newI != 2 {
			t.Errorf("newI = %d, want 2", newI)
		}
		if newPos != 1 || dst[0] != '\n' {
			t.Errorf("got dst[0]=%q pos=%d, want '\\n' pos=1", dst[0], newPos)
		}
	})

	t.Run("unknown escape via unescapeSequence", func(t *testing.T) {
		// Unknown escape character 'X' should return error
		data := []byte(`\X`)
		dst := make([]byte, 10)
		_, _, err := unescapeSequence(data, len(data), 0, dst, 0)
		if err == nil {
			t.Fatal("expected error for unknown escape")
		}
		if !strings.Contains(err.Error(), "invalid escape") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestUnescapeSWAR8ByteCopy tests the SWAR fast path where 8 consecutive
// bytes have no special characters (combined == 0).
func TestUnescapeSWAR8ByteCopy(t *testing.T) {
	// Build a string longer than 8 bytes with an escape early so that
	// firstEscIdx is set, then after the escape put 8+ plain bytes to
	// trigger the SWAR 8-byte direct copy path.
	// Input: \n + 16 plain bytes => after processing \n, the loop sees 16 plain bytes.
	plain := "ABCDEFGHIJKLMNOP" // 16 bytes, all > 0x20, no quote/backslash
	input := `\n` + plain
	got, gotLen, err := testUnescape(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "\n" + plain
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if gotLen != len(want) {
		t.Errorf("gotLen = %d, want %d", gotLen, len(want))
	}
}

// TestUnescapeControlCharInSWARLoop tests control character detection in the
// SWAR loop (the fast path for strings >= 8 bytes remaining).
func TestUnescapeControlCharInSWARLoop(t *testing.T) {
	// We need the escape to come first so firstEscIdx < len(input),
	// then after the escape a control char appears within an 8-byte window.
	// Build: \n + padding so total remaining >= 8, with a control char embedded.
	// 5 plain bytes + control char + 2 more plain bytes = 8 bytes after \n
	input := `\n` + "ABCDE" + "\x01" + "GH"
	_, _, err := testUnescape(input)
	if err == nil {
		t.Fatal("expected error for control character in SWAR loop")
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestUnescapeControlCharInTailLoop tests control character detection in the
// byte-by-byte tail loop (< 8 bytes remaining).
func TestUnescapeControlCharInTailLoop(t *testing.T) {
	// To hit the tail loop (i+8 > n), we need < 8 bytes remaining after the
	// SWAR loop has consumed earlier bytes. Use unescapeSinglePass directly
	// with a carefully sized input.
	// Build src so firstEscIdx = 0 and after processing the escape,
	// there are < 8 bytes left including a control char.
	// src: \n + "AB" + \x01 + "C" + '"' = 2+2+1+1+1 = 7 raw bytes after firstEsc
	// SWAR loop needs i+8 <= n, first iteration: i=0, n=7 => 0+8=8 > 7, skip SWAR.
	// Goes directly to tail loop which processes byte-by-byte.
	sc := &Parser{}
	src := []byte("\\nAB\x01C\"")
	_, _, err := sc.unescapeSinglePass(src, 0, 0)
	if err == nil {
		t.Fatal("expected error for control character in tail loop")
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestUnescapeTrailingBackslashInSWARLoop tests trailing backslash detection
// within the SWAR fast-path loop.
func TestUnescapeTrailingBackslashInSWARLoop(t *testing.T) {
	// We need the SWAR loop to encounter a backslash at the very end of src
	// (i+1 >= n). Build a string where the backslash is the last byte.
	// Use unescapeSinglePass directly: src has no closing quote, backslash at end.
	// We need >= 8 bytes remaining so the SWAR loop runs, and the backslash at the end.
	// src = "XXXXXXX\" (8 bytes), no closing quote.
	// firstEscIdx = 7 (the backslash position), start = 0
	src := []byte("XXXXXXX\\") // 8 bytes
	sc := &Parser{}
	_, _, err := sc.unescapeSinglePass(src, 0, 7)
	if err == nil {
		t.Fatal("expected error for trailing backslash in SWAR loop")
	}
	if !strings.Contains(err.Error(), "unexpected end of input") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestUnescapePrefixExceedsBuffer tests the path where the prefix before the
// first escape exceeds the buffer size, triggering the initial grow.
func TestUnescapePrefixExceedsBuffer(t *testing.T) {
	// Use a Parser with no arena (arenaRemaining < scratchBufSize),
	// so buf = sc.buf[:] which is scratchBufSize (2048).
	// Then make the prefix > 2048 bytes.
	sc := &Parser{} // no arenaData => arenaRemaining = 0, uses sc.buf
	prefixLen := scratchBufSize + 100
	prefix := strings.Repeat("A", prefixLen)
	// src = prefix + `\n` + closing quote
	src := []byte(prefix + `\n` + `"`)
	_, result, err := sc.unescapeSinglePass(src, 0, prefixLen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := prefix + "\n"
	if string(result) != want {
		t.Errorf("result length = %d, want %d", len(result), len(want))
	}
}

// TestUnescapeGrowInSWARLoop tests the grow() path when the buffer is nearly
// full during the SWAR loop (pos+8 > len(buf)).
func TestUnescapeGrowInSWARLoop(t *testing.T) {
	// Use a Parser with no arena so it uses the scratch buffer (2048 bytes).
	// Fill almost the entire buffer via a long prefix, then have plain bytes
	// after an escape so the SWAR loop tries to write 8 bytes but pos+8 > len(buf).
	sc := &Parser{}
	prefixLen := scratchBufSize - 4 // leave only 4 bytes free
	prefix := strings.Repeat("B", prefixLen)
	// After the prefix, an escape \n followed by enough plain bytes to trigger SWAR 8-byte copy
	src := []byte(prefix + `\n` + "CDCDCDCD" + `"`)
	_, result, err := sc.unescapeSinglePass(src, 0, prefixLen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := prefix + "\n" + "CDCDCDCD"
	if string(result) != want {
		t.Errorf("result length = %d, want %d", len(result), len(want))
	}
}

// TestUnescapeDoneLargeResultFromScratch tests the finalization path where the
// result is decoded in the scratch buffer, hasn't overflowed, but exceeds
// arenaInlineMax, requiring a heap allocation.
func TestUnescapeDoneLargeResultFromScratch(t *testing.T) {
	// Use a Parser with no arena (uses scratch buf), and produce a result
	// larger than arenaInlineMax (1024) but not overflowing scratch (2048).
	sc := &Parser{}
	contentLen := arenaInlineMax + 100 // 1124 bytes, fits in scratch (2048)
	content := strings.Repeat("X", contentLen)
	// No escapes, firstEscIdx = contentLen (at the closing quote position)
	src := []byte(content + `"`)
	_, result, err := sc.unescapeSinglePass(src, 0, contentLen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != content {
		t.Errorf("result = %q, want %q", string(result), content)
	}
	if len(result) != contentLen {
		t.Errorf("result len = %d, want %d", len(result), contentLen)
	}
}

// TestUnescapeDoneOverflowedHeapBuffer tests the finalization path where the
// buffer has overflowed (grew via heap allocation) and the result is used directly.
func TestUnescapeDoneOverflowedHeapBuffer(t *testing.T) {
	// Use a Parser with no arena so it uses scratch buf (2048 bytes).
	// Create input that overflows the scratch buffer: prefix > scratchBufSize.
	sc := &Parser{}
	contentLen := scratchBufSize + 500 // 2548, exceeds scratch
	content := strings.Repeat("Z", contentLen)
	src := []byte(content + `"`)
	_, result, err := sc.unescapeSinglePass(src, 0, contentLen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != content {
		t.Errorf("result length = %d, want %d", len(result), contentLen)
	}
}

// TestProcessEscapedStringKinds tests processEscapedString for different TypeInfo kinds.
func TestProcessEscapedStringKinds(t *testing.T) {
	sc := &Parser{}
	src := []byte(`hello\nworld"`)

	t.Run("KindString", func(t *testing.T) {
		ti := &TypeInfo{Kind: KindString}
		var s string
		endIdx, err := sc.processEscapedString(src, 0, 5, ti, unsafe.Pointer(&s))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s != "hello\nworld" {
			t.Errorf("got %q, want %q", s, "hello\nworld")
		}
		_ = endIdx
	})

	t.Run("KindAny", func(t *testing.T) {
		sc.arenaOff = 0
		ti := &TypeInfo{Kind: KindAny}
		var a any
		endIdx, err := sc.processEscapedString(src, 0, 5, ti, unsafe.Pointer(&a))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		s, ok := a.(string)
		if !ok {
			t.Fatalf("expected string, got %T", a)
		}
		if s != "hello\nworld" {
			t.Errorf("got %q, want %q", s, "hello\nworld")
		}
		_ = endIdx
	})

	t.Run("KindInt returns error", func(t *testing.T) {
		sc.arenaOff = 0
		ti := &TypeInfo{Kind: KindInt, Ext: &TypeInfoExt{Type: reflect.TypeOf(0)}}
		var dummy int
		_, err := sc.processEscapedString(src, 0, 5, ti, unsafe.Pointer(&dummy))
		if err == nil {
			t.Fatal("expected error for KindInt")
		}
		if !strings.Contains(err.Error(), "cannot unmarshal string") {
			t.Errorf("unexpected error: %v", err)
		}
	})
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
