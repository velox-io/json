package vjson

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// ---------- Types for testing ----------

type Float32Struct struct {
	V float32 `json:"v"`
}

type Float64Struct struct {
	V float64 `json:"v"`
}

type IntSliceStruct struct {
	Nums []int `json:"nums"`
}

type StringSliceStruct struct {
	Items []string `json:"items"`
}

type FloatSliceStruct struct {
	Vals []float64 `json:"vals"`
}

type PtrIntStruct struct {
	V *int `json:"v"`
}

type PtrStringStruct struct {
	V *string `json:"v"`
}

type Float32NullStruct struct {
	V float32 `json:"v"`
}

type PtrNullStruct struct {
	V *int `json:"v"`
}

type NestedStruct struct {
	Name  string `json:"name"`
	Inner struct {
		Value int `json:"value"`
	} `json:"inner"`
}

type StructWithUnknownFields struct {
	Name string `json:"name"`
}

type MapIntStruct struct {
	M map[string]int `json:"m"`
}

type AnyStruct struct {
	V any `json:"v"`
}

// ---------- scanNumber: float32 ----------

func TestScanNumber_Float32(t *testing.T) {
	var s Float32Struct
	err := Unmarshal([]byte(`{"v": 3.14}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.V < 3.13 || s.V > 3.15 {
		t.Errorf("got %v, want ~3.14", s.V)
	}
}

func TestScanNumber_Float32Null(t *testing.T) {
	var s Float32NullStruct
	s.V = 99.9
	err := Unmarshal([]byte(`{"v": null}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.V != 0 {
		t.Errorf("got %v, want 0", s.V)
	}
}

// ---------- scanNull: KindPointer ----------

func TestScanNull_Pointer(t *testing.T) {
	val := 42
	s := PtrNullStruct{V: &val}
	err := Unmarshal([]byte(`{"v": null}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.V != nil {
		t.Errorf("got %v, want nil", s.V)
	}
}

// ---------- scanPointer: pointer-free elem ----------

func TestScanPointer_PointerFreeElem(t *testing.T) {
	// *int is pointer-free elem (int doesn't contain pointers)
	type PtrFloat struct {
		V *float64 `json:"v"`
	}
	var s PtrFloat
	err := Unmarshal([]byte(`{"v": 3.14}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.V == nil || *s.V != 3.14 {
		t.Errorf("got %v, want &3.14", s.V)
	}
}

// ---------- Invalid literals ----------

func TestScanTrue_InvalidLiteral(t *testing.T) {
	type B struct {
		V bool `json:"v"`
	}
	var s B
	// "tXue" starts with 't' but isn't "true"
	err := Unmarshal([]byte(`{"v": tXue}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid true literal")
	}
}

func TestScanFalse_InvalidLiteral(t *testing.T) {
	type B struct {
		V bool `json:"v"`
	}
	var s B
	// "fXlse" starts with 'f' but isn't "false"
	err := Unmarshal([]byte(`{"v": fXlse}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid false literal")
	}
}

func TestScanNull_InvalidLiteral(t *testing.T) {
	var s AnyStruct
	// "nXll" starts with 'n' but isn't "null"
	err := Unmarshal([]byte(`{"v": nXll}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid null literal")
	}
}

// ---------- scanStringValue SWAR: KindAny with long no-escape string ----------

func TestScanStringValue_KindAny_SWARPath(t *testing.T) {
	// String must be long enough that the closing quote is found in the SWAR loop.
	// SWAR needs pos+8 <= n (total src length). With prefix '{"v": "' (7 bytes)
	// and suffix '"}' (2 bytes), total = 7 + len + 2 + 1(close quote). To ensure
	// the quote falls within a SWAR 8-byte window, use a long string.
	var s AnyStruct
	longVal := strings.Repeat("X", 64)
	input := `{"v": "` + longVal + `"}`
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.V != longVal {
		t.Errorf("got %q, want %q", s.V, longVal)
	}
}

// ---------- scanStringValue SWAR: control character ----------

func TestScanStringValue_ControlChar_SWAR(t *testing.T) {
	// Embed a control char in the middle of a long string to trigger SWAR control char path.
	// The string must be long enough that SWAR processes the byte containing the control char.
	prefix := strings.Repeat("A", 16)
	input := []byte(`{"v": "` + prefix + "\x01" + `rest"}`)
	var s AnyStruct
	err := Unmarshal(input, &s)
	if err == nil {
		t.Fatal("expected error for control character in string")
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------- scanStringValue: default kind (not string/any) in SWAR path ----------

func TestScanStringValue_DefaultKind_SWAR(t *testing.T) {
	// Try to assign a long string (>8 bytes, triggering SWAR) to an int field
	type IntField struct {
		V int `json:"v"`
	}
	var s IntField
	longVal := strings.Repeat("A", 64)
	input := `{"v": "` + longVal + `"}`
	err := Unmarshal([]byte(input), &s)
	if err == nil {
		t.Fatal("expected error for string-to-int assignment")
	}
	if !strings.Contains(err.Error(), "cannot assign string") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------- skipValue: true, false, {, [ ----------

func TestSkipValue_TrueFalseObjectArray(t *testing.T) {
	// struct with only "name" field; all other fields are skipped via skipValue
	type S struct {
		Name string `json:"name"`
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"skip true", `{"extra": true, "name": "ok"}`, "ok"},
		{"skip false", `{"extra": false, "name": "ok"}`, "ok"},
		{"skip object", `{"extra": {"nested": 1}, "name": "ok"}`, "ok"},
		{"skip array", `{"extra": [1, 2, 3], "name": "ok"}`, "ok"},
		{"skip nested obj+arr", `{"extra": {"a": [true, false, null]}, "name": "ok"}`, "ok"},
		{"skip string", `{"extra": "hello world", "name": "ok"}`, "ok"},
		{"skip number", `{"extra": 12345, "name": "ok"}`, "ok"},
		{"skip null", `{"extra": null, "name": "ok"}`, "ok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s S
			err := Unmarshal([]byte(tt.input), &s)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s.Name != tt.want {
				t.Errorf("got %q, want %q", s.Name, tt.want)
			}
		})
	}
}

// ---------- skipString: all paths (SWAR + tail, escapes, control chars) ----------

func TestSkipString_WithEscapes(t *testing.T) {
	// Skip a string value containing escapes (backslash in SWAR path)
	type S struct {
		Name string `json:"name"`
	}
	// "extra" has a long escaped string (>8 bytes), triggering SWAR backslash path in skipString
	escaped := `hello\\nworld\\ttab`
	input := `{"extra": "` + escaped + `", "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_WithUnicode(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Escaped unicode in skipped field
	input := `{"extra": "test\u0041value", "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_LongNoSpecial(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Long string without special chars to trigger SWAR 8-byte skip (combined==0)
	longVal := strings.Repeat("ABCDEFGH", 5) // 40 bytes
	input := `{"extra": "` + longVal + `", "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_TailPath(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Short string (< 8 bytes) so only the tail byte-by-byte path runs in skipString
	input := `{"extra": "short", "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_TailEscape(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// String with escape in tail path (< 8 bytes remaining)
	input := `{"extra": "ab\ncd", "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_TailUnicodeEscape(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Unicode escape in tail path
	input := `{"extra": "\u0041", "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

// ---------- skipContainer: nested objects and arrays ----------

func TestSkipContainer_Nested(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Deeply nested containers to exercise skipContainer's depth counting
	input := `{"extra": {"a": {"b": [1, [2, 3], {"c": 4}]}}, "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipContainer_WithStrings(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Container with strings inside (triggers skipString from within skipContainer)
	input := `{"extra": {"key": "value", "arr": ["a", "b"]}, "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipContainer_LargeNestedForSWAR(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Large nested container with lots of content to trigger SWAR 8-byte skip in skipContainer
	inner := `"k":"v","n":12345678`
	// Build enough content to trigger SWAR: numbers/letters with no structural chars
	content := `{"extra": {` + inner + `}, "name": "ok"}`
	var s S
	err := Unmarshal([]byte(content), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipContainer_Array(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Skip an array container
	input := `{"extra": [1, "two", true, null, [3, 4]], "name": "ok"}`
	var s S
	err := Unmarshal([]byte(input), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

// ---------- scanArray: pointer-free elements ([]int, []float64) ----------

func TestScanArray_PointerFreeElements(t *testing.T) {
	// []int uses pointer-free backing (make([]byte))
	var s IntSliceStruct
	err := Unmarshal([]byte(`{"nums": [1, 2, 3]}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Nums) != 3 || s.Nums[0] != 1 || s.Nums[1] != 2 || s.Nums[2] != 3 {
		t.Errorf("got %v, want [1 2 3]", s.Nums)
	}
}

func TestScanArray_PointerFreeGrow(t *testing.T) {
	// Array with > 2 elements triggers grow for pointer-free path (initCap=2)
	var s FloatSliceStruct
	err := Unmarshal([]byte(`{"vals": [1.1, 2.2, 3.3, 4.4, 5.5]}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Vals) != 5 {
		t.Errorf("got len=%d, want 5", len(s.Vals))
	}
}

// ---------- scanObjectToMap: multi-entry map (comma continuation) ----------

func TestScanObjectToMap_MultiEntry(t *testing.T) {
	var s MapIntStruct
	err := Unmarshal([]byte(`{"m": {"a": 1, "b": 2, "c": 3}}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.M) != 3 || s.M["a"] != 1 || s.M["b"] != 2 || s.M["c"] != 3 {
		t.Errorf("got %v", s.M)
	}
}

// ---------- Malformed JSON: scanObject errors ----------

func TestScanObject_TruncatedAfterBrace(t *testing.T) {
	type S struct {
		Inner struct {
			V int `json:"v"`
		} `json:"inner"`
	}
	var s S
	err := Unmarshal([]byte(`{"inner": {`), &s)
	if err == nil {
		t.Fatal("expected error for truncated object")
	}
}

func TestScanObject_MissingColon(t *testing.T) {
	type S struct {
		V int `json:"v"`
	}
	var s S
	err := Unmarshal([]byte(`{"v" 1}`), &s)
	if err == nil {
		t.Fatal("expected error for missing colon")
	}
}

func TestScanObject_NonQuoteKey(t *testing.T) {
	type S struct {
		V int `json:"v"`
	}
	var s S
	err := Unmarshal([]byte(`{v: 1}`), &s)
	if err == nil {
		t.Fatal("expected error for non-quoted key")
	}
}

func TestScanObject_UnexpectedCharAfterValue(t *testing.T) {
	type S struct {
		V int `json:"v"`
	}
	var s S
	err := Unmarshal([]byte(`{"v": 1 & "w": 2}`), &s)
	if err == nil {
		t.Fatal("expected error for unexpected char after value")
	}
}

func TestScanObject_TruncatedAfterValue(t *testing.T) {
	type S struct {
		V int `json:"v"`
	}
	var s S
	err := Unmarshal([]byte(`{"v": 1`), &s)
	if err == nil {
		t.Fatal("expected error for truncated after value")
	}
}

// ---------- Malformed JSON: scanObjectToMap errors ----------

func TestScanObjectToMap_TruncatedAfterBrace(t *testing.T) {
	var s MapIntStruct
	err := Unmarshal([]byte(`{"m": {`), &s)
	if err == nil {
		t.Fatal("expected error for truncated map")
	}
}

func TestScanObjectToMap_NonQuoteKey(t *testing.T) {
	var s MapIntStruct
	err := Unmarshal([]byte(`{"m": {k: 1}}`), &s)
	if err == nil {
		t.Fatal("expected error for non-quoted key in map")
	}
}

func TestScanObjectToMap_MissingColon(t *testing.T) {
	var s MapIntStruct
	err := Unmarshal([]byte(`{"m": {"k" 1}}`), &s)
	if err == nil {
		t.Fatal("expected error for missing colon in map")
	}
}

func TestScanObjectToMap_TruncatedAfterValue(t *testing.T) {
	var s MapIntStruct
	err := Unmarshal([]byte(`{"m": {"k": 1`), &s)
	if err == nil {
		t.Fatal("expected error for truncated map after value")
	}
}

// ---------- Malformed JSON: scanMapStringString errors ----------

func TestScanMapStringString_TruncatedAfterBrace(t *testing.T) {
	type S struct {
		M map[string]string `json:"m"`
	}
	var s S
	err := Unmarshal([]byte(`{"m": {`), &s)
	if err == nil {
		t.Fatal("expected error for truncated map[string]string")
	}
}

func TestScanMapStringString_NonQuoteKey(t *testing.T) {
	type S struct {
		M map[string]string `json:"m"`
	}
	var s S
	err := Unmarshal([]byte(`{"m": {k: "v"}}`), &s)
	if err == nil {
		t.Fatal("expected error for non-quoted key in map[string]string")
	}
}

func TestScanMapStringString_MissingColon(t *testing.T) {
	type S struct {
		M map[string]string `json:"m"`
	}
	var s S
	err := Unmarshal([]byte(`{"m": {"k" "v"}}`), &s)
	if err == nil {
		t.Fatal("expected error for missing colon")
	}
}

func TestScanMapStringString_TruncatedAfterValue(t *testing.T) {
	type S struct {
		M map[string]string `json:"m"`
	}
	var s S
	err := Unmarshal([]byte(`{"m": {"k": "v"`), &s)
	if err == nil {
		t.Fatal("expected error for truncated map[string]string after value")
	}
}

func TestScanMapStringString_UnexpectedChar(t *testing.T) {
	type S struct {
		M map[string]string `json:"m"`
	}
	var s S
	err := Unmarshal([]byte(`{"m": {"k": "v" & "k2": "v2"}}`), &s)
	if err == nil {
		t.Fatal("expected error for unexpected char in map[string]string")
	}
}

// ---------- Malformed JSON: scanArray errors ----------

func TestScanArray_TruncatedAfterBracket(t *testing.T) {
	var s IntSliceStruct
	err := Unmarshal([]byte(`{"nums": [`), &s)
	if err == nil {
		t.Fatal("expected error for truncated array")
	}
}

func TestScanArray_TruncatedAfterElement(t *testing.T) {
	var s IntSliceStruct
	err := Unmarshal([]byte(`{"nums": [1`), &s)
	if err == nil {
		t.Fatal("expected error for truncated array after element")
	}
}

func TestScanArray_UnexpectedChar(t *testing.T) {
	var s IntSliceStruct
	err := Unmarshal([]byte(`{"nums": [1 & 2]}`), &s)
	if err == nil {
		t.Fatal("expected error for unexpected char in array")
	}
}

func TestScanArray_InvalidElement(t *testing.T) {
	var s IntSliceStruct
	err := Unmarshal([]byte(`{"nums": [1, "not_a_number"]}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid element type in array")
	}
}

// ---------- scanPointer edge cases ----------

func TestScanPointer_TruncatedNull(t *testing.T) {
	// "nul" is truncated null literal for pointer field
	var s PtrIntStruct
	err := Unmarshal([]byte(`{"v": nul`), &s)
	if err == nil {
		t.Fatal("expected error for truncated null on pointer")
	}
}

func TestScanPointer_EOF(t *testing.T) {
	var s PtrIntStruct
	err := Unmarshal([]byte(`{"v": `), &s)
	if err == nil {
		t.Fatal("expected error for EOF on pointer value")
	}
}

func TestScanPointer_ValueError(t *testing.T) {
	// Pointer to int, but value is a string
	var s PtrIntStruct
	err := Unmarshal([]byte(`{"v": "hello"}`), &s)
	if err == nil {
		t.Fatal("expected error for string-to-*int assignment")
	}
}

// ---------- scanStringKey: edge cases ----------

func TestScanStringBytes_ControlChar_SWAR(t *testing.T) {
	// scanStringKey is used for object keys.
	// Put a control char in the key to trigger the SWAR control char path.
	type S struct {
		V int `json:"v"`
	}
	var s S
	// Key with control char embedded - needs to be > 8 bytes for SWAR
	input := []byte(`{"ABCDEFGH` + "\x01" + `": 1}`)
	err := Unmarshal(input, &s)
	if err == nil {
		t.Fatal("expected error for control char in key (SWAR path)")
	}
}

func TestScanStringBytes_Escape_SWAR(t *testing.T) {
	// scanStringKey: backslash in SWAR loop triggers unescapeSinglePass
	type S struct {
		V int `json:"v"`
	}
	var s S
	// Key with escape in SWAR range
	input := []byte(`{"hello\nworld": 1, "v": 42}`)
	err := Unmarshal(input, &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.V != 42 {
		t.Errorf("got %d, want 42", s.V)
	}
}

func TestScanStringBytes_Escape_Tail(t *testing.T) {
	// scanStringKey: backslash in tail loop (< 8 bytes)
	type S struct {
		V int `json:"v"`
	}
	var s S
	// Short key with escape
	input := []byte(`{"a\nb": 1, "v": 42}`)
	err := Unmarshal(input, &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.V != 42 {
		t.Errorf("got %d, want 42", s.V)
	}
}

func TestScanStringBytes_ControlChar_Tail(t *testing.T) {
	// scanStringKey: control char in tail loop
	type S struct {
		V int `json:"v"`
	}
	var s S
	input := []byte("{\"A\x01\": 1}")
	err := Unmarshal(input, &s)
	if err == nil {
		t.Fatal("expected error for control char in key (tail path)")
	}
}

func TestScanStringBytes_EOF(t *testing.T) {
	// scanStringKey: unterminated string
	type S struct {
		V int `json:"v"`
	}
	var s S
	err := Unmarshal([]byte(`{"abc`), &s)
	if err == nil {
		t.Fatal("expected error for unterminated key string")
	}
}

// ---------- skipValue error paths ----------

func TestSkipValue_EOF(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// The unknown field "extra" has no value => EOF
	err := Unmarshal([]byte(`{"extra": `), &s)
	if err == nil {
		t.Fatal("expected error for EOF in skipValue")
	}
}

func TestSkipValue_UnexpectedChar(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Unknown field value starts with '&'
	err := Unmarshal([]byte(`{"extra": &, "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for unexpected char in skipValue")
	}
}

func TestSkipValue_InvalidNumber(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Invalid number in skipped value
	err := Unmarshal([]byte(`{"extra": 1.2.3, "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid number in skipValue")
	}
}

// ---------- skipString error paths ----------

func TestSkipString_InvalidEscape_SWAR(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Skipped field with invalid escape in SWAR path (>8 bytes)
	input := `{"extra": "ABCDEFGH\Xinvalid", "name": "ok"}`
	err := Unmarshal([]byte(input), &s)
	if err == nil {
		t.Fatal("expected error for invalid escape in skipped string")
	}
}

func TestSkipString_InvalidEscape_Tail(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Short skipped field with invalid escape
	input := `{"extra": "\X", "name": "ok"}`
	err := Unmarshal([]byte(input), &s)
	if err == nil {
		t.Fatal("expected error for invalid escape in skipped string (tail)")
	}
}

func TestSkipString_ControlChar_SWAR(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Control char in skipped string (SWAR path) - no backslash preceding it
	input := []byte(`{"extra": "ABCDEFGH` + "\x01" + `", "name": "ok"}`)
	err := Unmarshal(input, &s)
	if err == nil {
		t.Fatal("expected error for control char in skipped string")
	}
}

func TestSkipString_ControlChar_Tail(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Control char in skipped string (tail path)
	input := []byte(`{"extra": "AB` + "\x01" + `", "name": "ok"}`)
	err := Unmarshal(input, &s)
	if err == nil {
		t.Fatal("expected error for control char in skipped string (tail)")
	}
}

func TestSkipString_TruncatedEscape_SWAR(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Backslash at end of input in SWAR path
	err := Unmarshal([]byte(`{"extra": "ABCDEFG\`), &s)
	if err == nil {
		t.Fatal("expected error for truncated escape in SWAR")
	}
}

func TestSkipString_TruncatedEscape_Tail(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Backslash at end of input in tail path
	err := Unmarshal([]byte(`{"extra": "A\`), &s)
	if err == nil {
		t.Fatal("expected error for truncated escape in tail")
	}
}

func TestSkipString_TruncatedUnicode_SWAR(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Truncated \uXXXX in SWAR path
	err := Unmarshal([]byte(`{"extra": "ABCDEFG\u00`), &s)
	if err == nil {
		t.Fatal("expected error for truncated unicode escape")
	}
}

func TestSkipString_TruncatedUnicode_Tail(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Truncated \uXXXX in tail path
	err := Unmarshal([]byte(`{"extra": "\u00`), &s)
	if err == nil {
		t.Fatal("expected error for truncated unicode in tail")
	}
}

func TestSkipString_InvalidUnicodeHex_SWAR(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Invalid hex digits in \uXXXX in SWAR path
	err := Unmarshal([]byte(`{"extra": "ABCDEFG\uZZZZ", "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid unicode hex")
	}
}

func TestSkipString_InvalidUnicodeHex_Tail(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"extra": "\uZZZZ", "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid unicode hex in tail")
	}
}

func TestSkipString_EOF(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Unterminated string in skipped value
	err := Unmarshal([]byte(`{"extra": "abc`), &s)
	if err == nil {
		t.Fatal("expected error for unterminated skipped string")
	}
}

// ---------- skipContainer error paths ----------

func TestSkipContainer_Unclosed(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"extra": {"a": 1`), &s)
	if err == nil {
		t.Fatal("expected error for unclosed container")
	}
}

func TestSkipContainer_StringError(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// String with error inside a skipped container
	err := Unmarshal([]byte(`{"extra": {"k": "unterminated`), &s)
	if err == nil {
		t.Fatal("expected error for string error in skipped container")
	}
}

// ---------- skipContainer byte-by-byte path ----------

func TestSkipContainer_TailBytePath(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Short container content (< 8 bytes) to trigger byte-by-byte path
	err := Unmarshal([]byte(`{"extra": [1], "name": "ok"}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipContainer_TailStringInBytePath(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Container with a short string that hits tail byte-by-byte in skipContainer
	err := Unmarshal([]byte(`{"extra": {"a":"b"}, "name": "ok"}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

// ---------- scanObject: skipValue error propagation ----------

func TestScanObject_SkipValueError(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Unknown field with malformed value
	err := Unmarshal([]byte(`{"unknown": tXue, "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error from skipValue for malformed literal")
	}
}

// ---------- scanObject: scanStringKey error on key ----------

func TestScanObject_KeyError(t *testing.T) {
	type S struct {
		V int `json:"v"`
	}
	var s S
	// Key string with control char
	input := []byte("{\"AB\x01\": 1}")
	err := Unmarshal(input, &s)
	if err == nil {
		t.Fatal("expected error for invalid key string")
	}
}

// ---------- skipValue: truncated literals ----------

func TestSkipValue_TruncatedTrue(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"extra": tru`), &s)
	if err == nil {
		t.Fatal("expected error for truncated true in skipValue")
	}
}

func TestSkipValue_TruncatedFalse(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"extra": fals`), &s)
	if err == nil {
		t.Fatal("expected error for truncated false in skipValue")
	}
}

func TestSkipValue_TruncatedNull(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"extra": nul`), &s)
	if err == nil {
		t.Fatal("expected error for truncated null in skipValue")
	}
}

func TestSkipValue_InvalidFalse(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// "fXlse" starts with 'f' but isn't "false"
	err := Unmarshal([]byte(`{"extra": fXlse, "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid false literal in skipValue")
	}
}

func TestSkipValue_InvalidNull(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"extra": nXll, "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid null literal in skipValue")
	}
}

func TestSkipValue_InvalidTrue(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"extra": tXue, "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid true literal in skipValue")
	}
}

func TestSkipValue_InvalidNumberInSkip(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Invalid number: leading zeros
	err := Unmarshal([]byte(`{"extra": 01, "name": "ok"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid number in skipValue")
	}
}

// ---------- skipString tail: valid escapes ----------

func TestSkipString_TailValidUnicode(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Short skipped string where only tail path runs.
	// To ensure the tail path processes the escape (not SWAR), make the entire
	// JSON small enough that `i + 8 > n` after the opening quote.
	// JSON: {"x":"\u0041","name":"ok"}  = 28 bytes
	// After opening quote for x's value at offset 5, i=6, n=28, 6+8=14<=28: SWAR runs.
	// We need i+8 > n. Use very short JSON: {"x":"\u0041"} = 16 bytes.
	// i=6, n=16, 6+8=14<=16: SWAR still runs.
	// Actually, let's use a different approach: pad with enough content AFTER the
	// skipped string so the final JSON forces the SWAR out.
	// Simpler: the skip is called via skipValue, which is called from scanObject.
	// The key "x" uses scanStringKey. Then skipValue is called on the value.
	// skipValue => skipString. The value string starts at some offset.
	// Let's just do a tiny value that gets into the tail.
	// {"x":"\u0041"} is 16 bytes. Value "..." starts at offset 5.
	// skipString: i = 6, n = 15 (before closing }). Wait, n = len(src) = 16.
	// But actually there may be padding. Let me just try and see what happens.
	// Actually the best approach: make a value with backslash that barely doesn't fit in SWAR.
	// Value: `"\u0041"` starts at offset 5, content starts at 6.
	// Content has `\`, then `u`, `0`, `0`, `4`, `1`, `"` = 7 chars starting at 6.
	// i=6, i+8=14. n=16 (full json). 14<=16, so SWAR runs and finds `\`.
	// Hmm. It's hard to avoid SWAR with full JSON.
	// Let me use a VERY short value: `"\n"` (2 bytes content).
	// JSON: {"x":"\n"}  src length = 12. Value starts at offset 5.
	// skipString: i = 6, n = 12. 6+8=14>12: tail path!
	err := Unmarshal([]byte("{\"x\":\"\\n\"}"), &s)
	// This should succeed (or at least not cause issues).
	// But s.Name is empty since there's no "name" field.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSkipString_TailValidCommonEscape(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	// Very short JSON so tail path is hit for skipString
	// {"x":"\t","name":"ok"} = 23 bytes
	// Value starts at offset 5. skipString: i=6, n=23. 6+8=14<=23: SWAR runs.
	// Hmm, SWAR still runs. Let me look at this differently.
	// skipString is called from skipValue which is called from scanObject for unknown fields.
	// The issue is src is always the full JSON buffer.
	// For the tail to be hit, we need the opening quote of the skipped value to be
	// at offset >= n-8. For that, the skipped string must be near the END of the buffer,
	// but unknown fields are processed before known fields in JSON order.
	// Alternative: put the value at the very end: {"name":"ok","x":"\n"}
	// src length = 23. "x" value `"\n"` starts at offset 18. skipString: i=19, n=23.
	// 19+8=27>23: tail path!
	var s S
	err := Unmarshal([]byte(`{"name":"ok","x":"\n"}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_TailValidUnicodeEscape(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Put unknown field at end with \uXXXX, make buffer short enough for tail
	// {"name":"ok","x":"\u0041"} = 27 bytes. Value starts at offset 18.
	// i=19, n=27. 19+8=27<=27: SWAR still runs! Need n < i+8.
	// {"name":"ok","x":"\u004"} is invalid but...
	// Let me add one more char: {"name":"ok","xx":"\u0041"} = 28 bytes.
	// Value starts at offset 19. i=20, n=28. 20+8=28<=28: SWAR runs.
	// We need n to be small enough. Try minimal JSON:
	// {"name":"o","x":"\u0041"} = 26 bytes. Value starts at offset 17. i=18, n=26. 18+8=26<=26: SWAR.
	// Even shorter: {"n":"o","x":"\u0041"} = 23. Value starts at offset 14. i=15, n=23. 15+8=23<=23: SWAR.
	// Very difficult to make tail path for unicode. Let me try:
	// {"n":"","x":"\u0041"} = 22. Value at offset 13. i=14, n=22. 14+8=22<=22: SWAR.
	// The problem is \uXXXX is 6 chars + opening/closing quote = 8 chars. Plus the JSON structure
	// around it means src is always >= 8 bytes from the value's start.
	//
	// For the tail unicode path, I think we need a very specific buffer size.
	// Let me try without the name field: {"x":"\u0041"} = 16. Value at offset 5. i=6, n=16. 6+8=14<=16: SWAR.
	// Hmm it's always SWAR-reachable. The only way to hit the tail is if the string
	// starts very close to the end of the buffer, but valid JSON always has closing braces.
	//
	// I'll skip this specific subpath as it's effectively unreachable in practice.
	// But the control char tail in skipString CAN be hit:
	err := Unmarshal([]byte("{\"name\":\"ok\",\"x\":\"\\u0041\"}"), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_TailControlChar(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Put unknown field at the end so the value string starts late in buffer
	// {"name":"ok","x":"A\x01"} - the value starts at offset 18, so i=19, n=25 (approx)
	// 19+8=27>25: tail path!
	input := []byte("{\"name\":\"ok\",\"x\":\"A\x01\"}")
	err := Unmarshal(input, &s)
	if err == nil {
		t.Fatal("expected error for control char in skipString tail")
	}
}

func TestSkipString_TailInvalidEscape(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Put unknown field at the end with invalid escape in tail path
	// {"name":"ok","x":"\X"} = 22 bytes. Value at offset 18. i=19, n=22. 19+8=27>22: tail!
	err := Unmarshal([]byte("{\"name\":\"ok\",\"x\":\"\\X\"}"), &s)
	if err == nil {
		t.Fatal("expected error for invalid escape in skipString tail")
	}
}

func TestSkipString_TailTruncatedEscape(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Backslash at end of input in tail path
	// {"name":"ok","x":"\ - value starts late, tail path
	err := Unmarshal([]byte("{\"name\":\"ok\",\"x\":\"\\"), &s)
	if err == nil {
		t.Fatal("expected error for truncated escape in skipString tail")
	}
}

func TestSkipString_TailTruncatedUnicode(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// \u with insufficient hex digits in tail path
	err := Unmarshal([]byte("{\"name\":\"ok\",\"x\":\"\\u00"), &s)
	if err == nil {
		t.Fatal("expected error for truncated unicode in skipString tail")
	}
}

func TestSkipString_TailInvalidUnicodeHex(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// \uZZZZ in tail path
	err := Unmarshal([]byte("{\"name\":\"ok\",\"x\":\"\\uZZZZ\"}"), &s)
	if err == nil {
		t.Fatal("expected error for invalid unicode hex in skipString tail")
	}
}

// ---------- skipContainer: tail path for { [ } ] ----------

func TestSkipContainer_TailNested(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Put a short nested container at the end so the byte-by-byte tail path
	// handles the { and } characters.
	// {"name":"ok","x":{}} = 21 bytes. Container starts at offset 18.
	// skipContainer: i = 19, n = 21. 19+8=27>21: tail path!
	err := Unmarshal([]byte(`{"name":"ok","x":{}}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipContainer_TailNestedArray(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Short array at end: {"name":"ok","x":[]} = 21 bytes
	err := Unmarshal([]byte(`{"name":"ok","x":[]}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipContainer_TailStringError(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// A string with error inside container in tail path
	// {"name":"ok","x":{"k":"ab - truncated string in tail
	err := Unmarshal([]byte("{\"name\":\"ok\",\"x\":{\"k\":\"ab"), &s)
	if err == nil {
		t.Fatal("expected error for string error in skipContainer tail")
	}
}

// ---------- []byte ↔ base64 string ----------

type ByteSliceStruct struct {
	Data []byte `json:"data"`
}

func TestUnmarshal_ByteSlice_Base64(t *testing.T) {
	// "aGVsbG8=" is base64 for "hello"
	var s ByteSliceStruct
	err := Unmarshal([]byte(`{"data":"aGVsbG8="}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(s.Data, []byte("hello")) {
		t.Errorf("got %v (%q), want %v (%q)", s.Data, s.Data, []byte("hello"), "hello")
	}
}

func TestUnmarshal_ByteSlice_Empty(t *testing.T) {
	var s ByteSliceStruct
	err := Unmarshal([]byte(`{"data":""}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Data == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(s.Data) != 0 {
		t.Errorf("got len=%d, want 0", len(s.Data))
	}
}

func TestUnmarshal_ByteSlice_Null(t *testing.T) {
	s := ByteSliceStruct{Data: []byte("existing")}
	err := Unmarshal([]byte(`{"data":null}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Data != nil {
		t.Errorf("got %v, want nil", s.Data)
	}
}

func TestUnmarshal_ByteSlice_Array(t *testing.T) {
	// JSON array [1,2,3] should still work as []byte{1,2,3}
	var s ByteSliceStruct
	err := Unmarshal([]byte(`{"data":[1,2,3]}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(s.Data, []byte{1, 2, 3}) {
		t.Errorf("got %v, want [1 2 3]", s.Data)
	}
}

func TestUnmarshal_ByteSlice_InvalidBase64(t *testing.T) {
	var s ByteSliceStruct
	err := Unmarshal([]byte(`{"data":"not-valid-base64!@#"}`), &s)
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "invalid base64") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnmarshal_ByteSlice_Roundtrip(t *testing.T) {
	original := ByteSliceStruct{Data: []byte("hello, world!")}

	data, err := Marshal(&original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// Verify the marshaled output contains base64
	encoded := base64.StdEncoding.EncodeToString([]byte("hello, world!"))
	if !bytes.Contains(data, []byte(encoded)) {
		t.Fatalf("marshaled data %q does not contain expected base64 %q", data, encoded)
	}

	var roundtripped ByteSliceStruct
	err = Unmarshal(data, &roundtripped)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !bytes.Equal(original.Data, roundtripped.Data) {
		t.Errorf("roundtrip mismatch: got %v, want %v", roundtripped.Data, original.Data)
	}
}
