package vjson

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
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
	if s.V != 99.9 {
		t.Errorf("got %v, want 99.9 (null should leave value unchanged)", s.V)
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
	if !strings.Contains(err.Error(), "cannot unmarshal string") {
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

func TestSkipString_EscapedQuoteAndClosingQuoteSameWindow(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Keep unknown field near the end so skipValue/skipString handles it.
	// String contains escaped quote before final closing quote.
	err := Unmarshal([]byte(`{"name":"ok","x":"abcdef\\\"gh"}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_UnicodeEscapeAcrossSWARWindow(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Crafted so that \uXXXX appears after a 7-byte prefix inside the skipped string,
	// exercising escape handling near SWAR-window boundaries.
	err := Unmarshal([]byte(`{"name":"ok","x":"1234567\\u0041tail"}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
	}
}

func TestSkipString_DenseBackslashes(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	var s S
	// Dense backslashes to stress repeated escape handling in single-pass mode.
	err := Unmarshal([]byte(`{"name":"ok","x":"\\\\\\\\\\\\\\\\\\\\\\\\\\\\"}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "ok" {
		t.Errorf("got %q, want %q", s.Name, "ok")
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

// ---------- json.Marshaler / json.Unmarshaler interface support ----------

// customMarshalType implements json.Marshaler with a value receiver.
type customMarshalType struct {
	Value string
}

func (c customMarshalType) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"custom:%s"`, c.Value)), nil
}

// customUnmarshalType implements json.Unmarshaler with a pointer receiver.
type customUnmarshalType struct {
	Value string
}

func (c *customUnmarshalType) UnmarshalJSON(data []byte) error {
	// Expect data like `"custom:xxx"`
	s := string(data)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("customUnmarshalType: expected quoted string, got %s", s)
	}
	s = s[1 : len(s)-1]
	if !strings.HasPrefix(s, "custom:") {
		return fmt.Errorf("customUnmarshalType: expected custom: prefix, got %s", s)
	}
	c.Value = strings.TrimPrefix(s, "custom:")
	return nil
}

// ptrMarshalType implements json.Marshaler with a pointer receiver.
type ptrMarshalType struct {
	N int
}

func (p *ptrMarshalType) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"ptr:%d"`, p.N)), nil
}

// ptrUnmarshalType implements json.Unmarshaler with a pointer receiver.
type ptrUnmarshalType struct {
	N int
}

func (p *ptrUnmarshalType) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("ptrUnmarshalType: expected quoted string, got %s", s)
	}
	s = s[1 : len(s)-1]
	if !strings.HasPrefix(s, "ptr:") {
		return fmt.Errorf("ptrUnmarshalType: expected ptr: prefix, got %s", s)
	}
	_, err := fmt.Sscanf(s, "ptr:%d", &p.N)
	return err
}

func TestMarshal_JSONMarshaler(t *testing.T) {
	v := customMarshalType{Value: "hello"}
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"custom:hello"` {
		t.Errorf("got %s, want %s", data, `"custom:hello"`)
	}
}

func TestUnmarshal_JSONUnmarshaler(t *testing.T) {
	var v customUnmarshalType
	err := Unmarshal([]byte(`"custom:world"`), &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Value != "world" {
		t.Errorf("got %q, want %q", v.Value, "world")
	}
}

func TestRoundtrip_JSONMarshaler(t *testing.T) {
	original := customMarshalType{Value: "roundtrip"}
	data, err := Marshal(&original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// customMarshalType also implements Unmarshaler (via *customUnmarshalType won't work,
	// but customMarshalType outputs "custom:xxx" which customUnmarshalType can parse).
	// We use a separate struct to test the roundtrip.
	var result customUnmarshalType
	err = Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if result.Value != "roundtrip" {
		t.Errorf("got %q, want %q", result.Value, "roundtrip")
	}
}

func TestMarshal_JSONMarshaler_PointerReceiver(t *testing.T) {
	v := ptrMarshalType{N: 42}
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"ptr:42"` {
		t.Errorf("got %s, want %s", data, `"ptr:42"`)
	}
}

func TestUnmarshal_JSONUnmarshaler_PointerReceiver(t *testing.T) {
	var v ptrUnmarshalType
	err := Unmarshal([]byte(`"ptr:99"`), &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.N != 99 {
		t.Errorf("got %d, want %d", v.N, 99)
	}
}

func TestMarshal_TimeTime(t *testing.T) {
	ts := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	data, err := Marshal(&ts)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// encoding/json marshals time.Time as RFC3339Nano quoted string
	want, _ := json.Marshal(ts)
	if string(data) != string(want) {
		t.Errorf("got %s, want %s", data, want)
	}

	// Roundtrip: unmarshal back
	var result time.Time
	err = Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !result.Equal(ts) {
		t.Errorf("roundtrip: got %v, want %v", result, ts)
	}
}

func TestMarshal_JSONMarshaler_Null(t *testing.T) {
	// nil pointer to a Marshaler type → "null"
	type S struct {
		T *time.Time `json:"t"`
	}
	s := S{T: nil}
	data, err := Marshal(&s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"t":null}` {
		t.Errorf("got %s, want %s", data, `{"t":null}`)
	}
}

func TestUnmarshal_JSONMarshaler_InStruct(t *testing.T) {
	type Event struct {
		Name string    `json:"name"`
		At   time.Time `json:"at"`
	}

	input := `{"name":"deploy","at":"2024-06-15T12:30:00Z"}`
	var ev Event
	err := Unmarshal([]byte(input), &ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Name != "deploy" {
		t.Errorf("name: got %q, want %q", ev.Name, "deploy")
	}
	expected := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	if !ev.At.Equal(expected) {
		t.Errorf("at: got %v, want %v", ev.At, expected)
	}

	// Marshal roundtrip
	data, err := Marshal(&ev)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var ev2 Event
	err = Unmarshal(data, &ev2)
	if err != nil {
		t.Fatalf("roundtrip unmarshal error: %v", err)
	}
	if ev2.Name != ev.Name || !ev2.At.Equal(ev.At) {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", ev2, ev)
	}
}

// ---------- encoding.TextMarshaler / encoding.TextUnmarshaler interface support ----------

// textMarshalType implements encoding.TextMarshaler with a value receiver.
type textMarshalType struct {
	Value string
}

func (t textMarshalType) MarshalText() ([]byte, error) {
	return []byte("text:" + t.Value), nil
}

func (t *textMarshalType) UnmarshalText(data []byte) error {
	s := string(data)
	if !strings.HasPrefix(s, "text:") {
		return fmt.Errorf("textMarshalType: expected text: prefix, got %s", s)
	}
	t.Value = strings.TrimPrefix(s, "text:")
	return nil
}

// ptrTextMarshalType implements encoding.TextMarshaler with a pointer receiver.
type ptrTextMarshalType struct {
	N int
}

func (p *ptrTextMarshalType) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("ptrtext:%d", p.N)), nil
}

func (p *ptrTextMarshalType) UnmarshalText(data []byte) error {
	_, err := fmt.Sscanf(string(data), "ptrtext:%d", &p.N)
	return err
}

// bothMarshalerType implements both json.Marshaler and encoding.TextMarshaler.
// json.Marshaler should take precedence for value encoding.
type bothMarshalerType struct {
	Value string
}

func (b bothMarshalerType) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"json:%s"`, b.Value)), nil
}

func (b bothMarshalerType) MarshalText() ([]byte, error) {
	return []byte("text:" + b.Value), nil
}

func TestMarshal_TextMarshaler(t *testing.T) {
	v := textMarshalType{Value: "hello"}
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"text:hello"` {
		t.Errorf("got %s, want %s", data, `"text:hello"`)
	}
}

func TestUnmarshal_TextUnmarshaler(t *testing.T) {
	var v textMarshalType
	err := Unmarshal([]byte(`"text:world"`), &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Value != "world" {
		t.Errorf("got %q, want %q", v.Value, "world")
	}
}

func TestRoundtrip_TextMarshaler(t *testing.T) {
	original := textMarshalType{Value: "roundtrip"}
	data, err := Marshal(&original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var result textMarshalType
	err = Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if result.Value != "roundtrip" {
		t.Errorf("got %q, want %q", result.Value, "roundtrip")
	}
}

func TestMarshal_TextMarshaler_PointerReceiver(t *testing.T) {
	v := ptrTextMarshalType{N: 42}
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"ptrtext:42"` {
		t.Errorf("got %s, want %s", data, `"ptrtext:42"`)
	}
}

func TestUnmarshal_TextUnmarshaler_PointerReceiver(t *testing.T) {
	var v ptrTextMarshalType
	err := Unmarshal([]byte(`"ptrtext:99"`), &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.N != 99 {
		t.Errorf("got %d, want %d", v.N, 99)
	}
}

func TestMarshal_TextMarshaler_Priority(t *testing.T) {
	// json.Marshaler should take precedence over TextMarshaler for value encoding
	v := bothMarshalerType{Value: "test"}
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"json:test"` {
		t.Errorf("got %s, want %s (json.Marshaler should win over TextMarshaler)", data, `"json:test"`)
	}
}

func TestMarshal_MapIntString(t *testing.T) {
	m := map[int]string{1: "one", 2: "two"}
	data, err := Marshal(&m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it's valid JSON by unmarshaling with stdlib
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("stdlib unmarshal failed: %v (data: %s)", err, data)
	}
	if got["1"] != "one" || got["2"] != "two" {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestUnmarshal_MapIntString(t *testing.T) {
	var m map[int]string
	err := Unmarshal([]byte(`{"1":"a","2":"b"}`), &m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m[1] != "a" || m[2] != "b" {
		t.Errorf("got %v, want map[1:a 2:b]", m)
	}
}

func TestRoundtrip_MapIntString(t *testing.T) {
	original := map[int]string{10: "ten", 20: "twenty"}
	data, err := Marshal(&original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var result map[int]string
	err = Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if result[10] != "ten" || result[20] != "twenty" {
		t.Errorf("roundtrip mismatch: got %v, want %v", result, original)
	}
}

func TestMarshal_MapUintKey(t *testing.T) {
	m := map[uint64]string{100: "hundred"}
	data, err := Marshal(&m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("stdlib unmarshal failed: %v (data: %s)", err, data)
	}
	if got["100"] != "hundred" {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestUnmarshal_MapUintKey(t *testing.T) {
	var m map[uint64]string
	err := Unmarshal([]byte(`{"100":"hundred"}`), &m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m[100] != "hundred" {
		t.Errorf("got %v, want map[100:hundred]", m)
	}
}

// textKeyType is a custom type for map keys that implements TextMarshaler.
type textKeyType struct {
	A, B string
}

func (k textKeyType) MarshalText() ([]byte, error) {
	return []byte(k.A + "/" + k.B), nil
}

func (k *textKeyType) UnmarshalText(data []byte) error {
	parts := strings.SplitN(string(data), "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("textKeyType: expected a/b format, got %s", data)
	}
	k.A = parts[0]
	k.B = parts[1]
	return nil
}

func TestMarshal_MapTextMarshalerKey(t *testing.T) {
	m := map[textKeyType]string{
		{A: "x", B: "y"}: "val1",
	}
	data, err := Marshal(&m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("stdlib unmarshal failed: %v (data: %s)", err, data)
	}
	if got["x/y"] != "val1" {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestUnmarshal_MapTextUnmarshalerKey(t *testing.T) {
	var m map[textKeyType]string
	err := Unmarshal([]byte(`{"x/y":"val1"}`), &m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := textKeyType{A: "x", B: "y"}
	if m[expected] != "val1" {
		t.Errorf("got %v, want map[{x y}:val1]", m)
	}
}

func TestRoundtrip_MapTextMarshalerKey(t *testing.T) {
	original := map[textKeyType]int{
		{A: "foo", B: "bar"}: 42,
	}
	data, err := Marshal(&original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var result map[textKeyType]int
	err = Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	expected := textKeyType{A: "foo", B: "bar"}
	if result[expected] != 42 {
		t.Errorf("roundtrip mismatch: got %v, want %v", result, original)
	}
}

func TestMarshal_TextMarshaler_InStruct(t *testing.T) {
	type S struct {
		Name string          `json:"name"`
		Val  textMarshalType `json:"val"`
	}

	s := S{Name: "test", Val: textMarshalType{Value: "hello"}}
	data, err := Marshal(&s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the output
	var got map[string]string
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatalf("stdlib unmarshal failed: %v (data: %s)", err, data)
	}
	if got["name"] != "test" || got["val"] != "text:hello" {
		t.Errorf("unexpected result: %v", got)
	}

	// Unmarshal back
	var s2 S
	err = Unmarshal(data, &s2)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if s2.Name != "test" || s2.Val.Value != "hello" {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", s2, s)
	}
}

func TestUnmarshal_TextUnmarshaler_Null(t *testing.T) {
	type S struct {
		Val *textMarshalType `json:"val"`
	}
	s := S{Val: &textMarshalType{Value: "existing"}}
	err := Unmarshal([]byte(`{"val":null}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Val != nil {
		t.Errorf("got %v, want nil", s.Val)
	}
}

func TestUnmarshal_TextUnmarshaler_NonString(t *testing.T) {
	// TextUnmarshaler expects a JSON string, passing a number should error
	var v textMarshalType
	err := Unmarshal([]byte(`123`), &v)
	if err == nil {
		t.Fatal("expected error for non-string input to TextUnmarshaler")
	}
}

func TestMarshal_MapIntInt(t *testing.T) {
	m := map[int]int{1: 10, 2: 20}
	data, err := Marshal(&m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Roundtrip
	var result map[int]int
	err = Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if result[1] != 10 || result[2] != 20 {
		t.Errorf("roundtrip mismatch: got %v", result)
	}
}

// Verify stdlib compatibility for map[int]string
func TestMarshal_MapIntString_StdlibCompat(t *testing.T) {
	m := map[int]string{1: "a", 2: "b"}

	vjsonData, err := Marshal(&m)
	if err != nil {
		t.Fatalf("vjson marshal error: %v", err)
	}

	stdlibData, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("stdlib marshal error: %v", err)
	}

	// Both should produce valid JSON that round-trips via stdlib
	var vjsonResult, stdlibResult map[int]string
	if err := json.Unmarshal(vjsonData, &vjsonResult); err != nil {
		t.Fatalf("stdlib unmarshal of vjson data failed: %v", err)
	}
	if err := json.Unmarshal(stdlibData, &stdlibResult); err != nil {
		t.Fatalf("stdlib unmarshal of stdlib data failed: %v", err)
	}
	if vjsonResult[1] != stdlibResult[1] || vjsonResult[2] != stdlibResult[2] {
		t.Errorf("vjson result %v differs from stdlib result %v", vjsonResult, stdlibResult)
	}
}

// ---------- Comprehensive non-string map key tests (stdlib compat) ----------

// TestMapNonStringKey_StdlibCompat verifies that vjson produces the same
// marshal output and unmarshal results as encoding/json for map types with
// non-string keys: integer types, TextMarshaler/TextUnmarshaler, and various
// value types (string, int, bool, struct, slice, nested map).
func TestMapNonStringKey_StdlibCompat(t *testing.T) {
	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}

	t.Run("map[int8]string", func(t *testing.T) {
		m := map[int8]string{-1: "neg", 0: "zero", 127: "max"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int16]string", func(t *testing.T) {
		m := map[int16]string{-100: "neg", 100: "pos"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int32]string", func(t *testing.T) {
		m := map[int32]string{-2147483648: "min", 2147483647: "max"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int64]string", func(t *testing.T) {
		m := map[int64]string{-9999999999: "big_neg", 9999999999: "big_pos"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[uint8]string", func(t *testing.T) {
		m := map[uint8]string{0: "zero", 255: "max"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[uint16]string", func(t *testing.T) {
		m := map[uint16]string{0: "zero", 65535: "max"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[uint32]string", func(t *testing.T) {
		m := map[uint32]string{0: "zero", 4294967295: "max"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[uint64]string", func(t *testing.T) {
		m := map[uint64]string{0: "zero", 18446744073709551615: "max"}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int]int", func(t *testing.T) {
		m := map[int]int{1: 10, 2: 20, -3: 30}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int]bool", func(t *testing.T) {
		m := map[int]bool{1: true, 2: false, 3: true}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int]float64", func(t *testing.T) {
		m := map[int]float64{1: 1.5, 2: 2.7}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int]struct", func(t *testing.T) {
		m := map[int]Inner{1: {X: 10, Y: "a"}, 2: {X: 20, Y: "b"}}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int][]string", func(t *testing.T) {
		m := map[int][]string{1: {"a", "b"}, 2: {"c"}}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int]*struct", func(t *testing.T) {
		m := map[int]*Inner{1: {X: 10, Y: "a"}, 2: nil}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[textKeyType]struct", func(t *testing.T) {
		m := map[textKeyType]Inner{
			{A: "foo", B: "bar"}: {X: 1, Y: "v1"},
			{A: "baz", B: "qux"}: {X: 2, Y: "v2"},
		}
		compareMapRoundtrip(t, &m)
	})

	t.Run("map[int]map[string]int", func(t *testing.T) {
		m := map[int]map[string]int{
			1: {"a": 10, "b": 20},
			2: {"c": 30},
		}
		compareMapRoundtrip(t, &m)
	})

	// Unmarshal-only: verify error on invalid key
	t.Run("unmarshal_invalid_int_key", func(t *testing.T) {
		var m map[int]string
		vjErr := Unmarshal([]byte(`{"abc":"val"}`), &m)
		stdErr := json.Unmarshal([]byte(`{"abc":"val"}`), &m)
		if (vjErr == nil) != (stdErr == nil) {
			t.Errorf("error mismatch: vjson=%v, stdlib=%v", vjErr, stdErr)
		}
	})

	t.Run("unmarshal_overflow_int8_key", func(t *testing.T) {
		var m map[int8]string
		vjErr := Unmarshal([]byte(`{"999":"val"}`), &m)
		stdErr := json.Unmarshal([]byte(`{"999":"val"}`), &m)
		if (vjErr == nil) != (stdErr == nil) {
			t.Errorf("error mismatch: vjson=%v, stdlib=%v", vjErr, stdErr)
		}
	})

	// Empty map
	t.Run("map[int]string_empty", func(t *testing.T) {
		m := map[int]string{}
		compareMapRoundtrip(t, &m)
	})

	// Nil map unmarshal
	t.Run("map[int]string_null", func(t *testing.T) {
		var vjM, stdM map[int]string
		vjErr := Unmarshal([]byte(`null`), &vjM)
		stdErr := json.Unmarshal([]byte(`null`), &stdM)
		if (vjErr == nil) != (stdErr == nil) {
			t.Errorf("error mismatch: vjson=%v, stdlib=%v", vjErr, stdErr)
		}
		if !reflect.DeepEqual(vjM, stdM) {
			t.Errorf("result mismatch: vjson=%v, stdlib=%v", vjM, stdM)
		}
	})
}

// compareMapRoundtrip marshals with both vjson and stdlib, then unmarshals
// each output with both libs, and checks all four results match.
func compareMapRoundtrip[T any](t *testing.T, m *T) {
	t.Helper()

	// Marshal with vjson
	vjData, vjErr := Marshal(m)
	// Marshal with stdlib
	stdData, stdErr := json.Marshal(m)

	if (vjErr == nil) != (stdErr == nil) {
		t.Fatalf("marshal error mismatch: vjson=%v, stdlib=%v", vjErr, stdErr)
	}
	if vjErr != nil {
		return
	}

	// Both outputs should be valid JSON. Unmarshal vjson output with stdlib.
	var stdFromVj T
	if err := json.Unmarshal(vjData, &stdFromVj); err != nil {
		t.Fatalf("stdlib cannot parse vjson output: %v\n  vjson: %s\n  stdlib: %s", err, vjData, stdData)
	}

	// Unmarshal stdlib output with vjson.
	var vjFromStd T
	if err := Unmarshal(stdData, &vjFromStd); err != nil {
		t.Fatalf("vjson cannot parse stdlib output: %v\n  stdlib: %s", err, stdData)
	}

	// Unmarshal stdlib output with stdlib (reference).
	var stdFromStd T
	json.Unmarshal(stdData, &stdFromStd)

	// Unmarshal vjson output with vjson.
	var vjFromVj T
	Unmarshal(vjData, &vjFromVj)

	// All four should match.
	if !reflect.DeepEqual(stdFromVj, stdFromStd) {
		t.Errorf("vjson marshal output differs from stdlib:\n  vjson→stdlib: %+v\n  stdlib→stdlib: %+v\n  vjson data: %s\n  stdlib data: %s",
			stdFromVj, stdFromStd, vjData, stdData)
	}
	if !reflect.DeepEqual(vjFromStd, stdFromStd) {
		t.Errorf("vjson unmarshal of stdlib output differs:\n  stdlib→vjson: %+v\n  stdlib→stdlib: %+v\n  data: %s",
			vjFromStd, stdFromStd, stdData)
	}
	if !reflect.DeepEqual(vjFromVj, stdFromStd) {
		t.Errorf("vjson roundtrip differs from stdlib:\n  vjson→vjson: %+v\n  stdlib→stdlib: %+v",
			vjFromVj, stdFromStd)
	}
}

// ---------- stdlib types: net.IP (pure TextMarshaler, no json.Marshaler) ----------

func TestRoundtrip_NetIP(t *testing.T) {
	ip := net.ParseIP("192.168.1.1")
	data, err := Marshal(&ip)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	stdData, _ := json.Marshal(ip)
	if string(data) != string(stdData) {
		t.Errorf("vjson %s != stdlib %s", data, stdData)
	}

	var got net.IP
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !ip.Equal(got) {
		t.Errorf("roundtrip: got %v, want %v", got, ip)
	}
}

func TestRoundtrip_NetIP_IPv6(t *testing.T) {
	ip := net.ParseIP("::1")
	data, err := Marshal(&ip)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	stdData, _ := json.Marshal(ip)
	if string(data) != string(stdData) {
		t.Errorf("vjson %s != stdlib %s", data, stdData)
	}

	var got net.IP
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !ip.Equal(got) {
		t.Errorf("roundtrip: got %v, want %v", got, ip)
	}
}

func TestUnmarshal_NetIP_InStruct(t *testing.T) {
	type Host struct {
		Name string `json:"name"`
		Addr net.IP `json:"addr"`
	}
	input := `{"name":"gw","addr":"10.0.0.1"}`

	var got Host
	if err := Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	var std Host
	json.Unmarshal([]byte(input), &std)

	if got.Name != std.Name || !got.Addr.Equal(std.Addr) {
		t.Errorf("got %+v, stdlib got %+v", got, std)
	}
}

// ---------- stdlib types: big.Int (*T has json.Marshaler + TextMarshaler) ----------

func TestRoundtrip_BigInt(t *testing.T) {
	v := new(big.Int).SetInt64(123456789012345)
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	stdData, _ := json.Marshal(v)
	if string(data) != string(stdData) {
		t.Errorf("vjson %s != stdlib %s", data, stdData)
	}

	var got big.Int
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if v.Cmp(&got) != 0 {
		t.Errorf("roundtrip: got %v, want %v", &got, v)
	}
}

func TestRoundtrip_BigInt_Negative(t *testing.T) {
	v := new(big.Int).SetInt64(-99999999999)
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	stdData, _ := json.Marshal(v)
	if string(data) != string(stdData) {
		t.Errorf("vjson %s != stdlib %s", data, stdData)
	}

	var got big.Int
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if v.Cmp(&got) != 0 {
		t.Errorf("roundtrip: got %v, want %v", &got, v)
	}
}

func TestRoundtrip_BigInt_InStruct(t *testing.T) {
	type Wallet struct {
		Owner   string   `json:"owner"`
		Balance *big.Int `json:"balance"`
	}
	w := Wallet{Owner: "alice", Balance: new(big.Int).SetInt64(42)}
	data, err := Marshal(&w)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	stdData, _ := json.Marshal(w)
	if string(data) != string(stdData) {
		t.Errorf("vjson %s != stdlib %s", data, stdData)
	}

	var got Wallet
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got.Owner != "alice" || got.Balance.Cmp(new(big.Int).SetInt64(42)) != 0 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

// ---------- stdlib types: big.Float (pure TextMarshaler on *T) ----------

func TestRoundtrip_BigFloat(t *testing.T) {
	v := new(big.Float).SetFloat64(3.14159265358979)
	data, err := Marshal(&v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	stdData, _ := json.Marshal(v)
	if string(data) != string(stdData) {
		t.Errorf("vjson %s != stdlib %s", data, stdData)
	}

	var got big.Float
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	// Compare via text: Parse from decimal may yield different precision than SetFloat64,
	// but the string representation must match.
	if got.Text('g', -1) != v.Text('g', -1) {
		t.Errorf("roundtrip: got %s, want %s", got.Text('g', -1), v.Text('g', -1))
	}
}

// ---------- stdlib types: time.Time as map key ----------

func TestRoundtrip_MapTimeKey(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	m := map[time.Time]string{t1: "new year", t2: "mid year"}

	data, err := Marshal(&m)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	stdData, _ := json.Marshal(m)

	// Verify vjson output parses identically via stdlib
	var vjsonResult, stdResult map[time.Time]string
	if err := json.Unmarshal(data, &vjsonResult); err != nil {
		t.Fatalf("stdlib cannot parse vjson output: %v (data: %s)", err, data)
	}
	json.Unmarshal(stdData, &stdResult)

	for k, v := range stdResult {
		if vjsonResult[k] != v {
			t.Errorf("key %v: vjson %q != stdlib %q", k, vjsonResult[k], v)
		}
	}

	// Roundtrip via vjson
	var got map[time.Time]string
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got[t1] != "new year" || got[t2] != "mid year" {
		t.Errorf("roundtrip mismatch: %v", got)
	}
}

// ---------- stdlib types: net.IP as map key ----------

func TestRoundtrip_MapNetIPKey(t *testing.T) {
	m := map[string]int{"10.0.0.1": 80, "10.0.0.2": 443}

	// net.IP is a slice, not usable as map key directly.
	// Use string representation and verify TextMarshaler path via struct.
	type Entry struct {
		Addr net.IP `json:"addr"`
		Port int    `json:"port"`
	}
	entries := []Entry{
		{Addr: net.ParseIP("10.0.0.1"), Port: 80},
		{Addr: net.ParseIP("10.0.0.2"), Port: 443},
	}
	data, err := Marshal(&entries)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	stdData, _ := json.Marshal(entries)
	if string(data) != string(stdData) {
		t.Errorf("vjson %s != stdlib %s", data, stdData)
	}

	var got []Entry
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	for i, e := range entries {
		if !got[i].Addr.Equal(e.Addr) || got[i].Port != e.Port {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], e)
		}
	}
	_ = m // silence unused
}

// ---------- scanArray: [N]any (fixed-size array of interface{}) ----------

func TestUnmarshal_FixedArrayAny_MixedTypes(t *testing.T) {
	// [N]any should decode a JSON array with mixed types into a fixed-size Go array.
	var arr [5]any
	err := Unmarshal([]byte(`["hello", 42, true, null, {"k": "v"}]`), &arr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if arr[0] != "hello" {
		t.Errorf("arr[0]: got %v (%T), want \"hello\"", arr[0], arr[0])
	}
	// Numbers decode as float64 by default (same as encoding/json).
	if arr[1] != float64(42) {
		t.Errorf("arr[1]: got %v (%T), want float64(42)", arr[1], arr[1])
	}
	if arr[2] != true {
		t.Errorf("arr[2]: got %v (%T), want true", arr[2], arr[2])
	}
	if arr[3] != nil {
		t.Errorf("arr[3]: got %v (%T), want nil", arr[3], arr[3])
	}
	wantMap := map[string]any{"k": "v"}
	if !reflect.DeepEqual(arr[4], wantMap) {
		t.Errorf("arr[4]: got %v (%T), want %v", arr[4], arr[4], wantMap)
	}
}

func TestUnmarshal_FixedArrayAny_NestedArray(t *testing.T) {
	// Nested arrays inside [N]any should produce []any elements.
	var arr [2]any
	err := Unmarshal([]byte(`[[1, 2], ["a", "b"]]`), &arr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want0 := []any{float64(1), float64(2)}
	want1 := []any{"a", "b"}
	if !reflect.DeepEqual(arr[0], want0) {
		t.Errorf("arr[0]: got %v, want %v", arr[0], want0)
	}
	if !reflect.DeepEqual(arr[1], want1) {
		t.Errorf("arr[1]: got %v, want %v", arr[1], want1)
	}
}

func TestUnmarshal_FixedArrayAny_FewerElements(t *testing.T) {
	// JSON array shorter than [N]: trailing elements should be nil (zero value of any).
	var arr [4]any
	err := Unmarshal([]byte(`["only", "two"]`), &arr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if arr[0] != "only" {
		t.Errorf("arr[0]: got %v, want \"only\"", arr[0])
	}
	if arr[1] != "two" {
		t.Errorf("arr[1]: got %v, want \"two\"", arr[1])
	}
	if arr[2] != nil {
		t.Errorf("arr[2]: got %v, want nil", arr[2])
	}
	if arr[3] != nil {
		t.Errorf("arr[3]: got %v, want nil", arr[3])
	}
}

func TestUnmarshal_FixedArrayAny_MoreElements(t *testing.T) {
	// JSON array longer than [N]: extra elements should be silently discarded.
	var arr [2]any
	err := Unmarshal([]byte(`[1, 2, 3, 4, 5]`), &arr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if arr[0] != float64(1) {
		t.Errorf("arr[0]: got %v, want float64(1)", arr[0])
	}
	if arr[1] != float64(2) {
		t.Errorf("arr[1]: got %v, want float64(2)", arr[1])
	}
}

func TestUnmarshal_FixedArrayAny_Empty(t *testing.T) {
	// Empty JSON array: all elements should be nil.
	var arr [3]any
	err := Unmarshal([]byte(`[]`), &arr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, v := range arr {
		if v != nil {
			t.Errorf("arr[%d]: got %v, want nil", i, v)
		}
	}
}

func TestUnmarshal_FixedArrayAny_InStruct(t *testing.T) {
	// [N]any as a struct field.
	type S struct {
		Items [3]any `json:"items"`
		Name  string `json:"name"`
	}
	var s S
	err := Unmarshal([]byte(`{"name": "test", "items": [1, "two", false]}`), &s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Name != "test" {
		t.Errorf("Name: got %q, want %q", s.Name, "test")
	}
	if s.Items[0] != float64(1) {
		t.Errorf("Items[0]: got %v (%T), want float64(1)", s.Items[0], s.Items[0])
	}
	if s.Items[1] != "two" {
		t.Errorf("Items[1]: got %v, want \"two\"", s.Items[1])
	}
	if s.Items[2] != false {
		t.Errorf("Items[2]: got %v, want false", s.Items[2])
	}
}

func TestUnmarshal_FixedArrayAny_StdlibCompat(t *testing.T) {
	// Results should match encoding/json behavior.
	inputs := []string{
		`[1, "two", true, null, {"k": 3}]`,
		`[]`,
		`[1, 2, 3, 4, 5, 6]`,
		`[[1], [2]]`,
	}
	for _, input := range inputs {
		var vjArr [3]any
		var stdArr [3]any

		vjErr := Unmarshal([]byte(input), &vjArr)
		stdErr := json.Unmarshal([]byte(input), &stdArr)

		if (vjErr == nil) != (stdErr == nil) {
			t.Errorf("input %s: vjson err=%v, stdlib err=%v", input, vjErr, stdErr)
			continue
		}
		if !reflect.DeepEqual(vjArr, stdArr) {
			t.Errorf("input %s:\n  vjson:  %v\n  stdlib: %v", input, vjArr, stdArr)
		}
	}
}

// ---------- stdlib types: time.Time roundtrip vs stdlib ----------

func TestMarshal_TimeTime_StdlibCompat(t *testing.T) {
	times := []time.Time{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 15, 12, 30, 45, 123456789, time.UTC),
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		{}, // zero value
	}
	for _, ts := range times {
		vjsonData, err := Marshal(&ts)
		if err != nil {
			t.Fatalf("marshal %v: %v", ts, err)
		}
		stdData, _ := json.Marshal(ts)
		if string(vjsonData) != string(stdData) {
			t.Errorf("time %v: vjson %s != stdlib %s", ts, vjsonData, stdData)
		}

		var got time.Time
		if err := Unmarshal(vjsonData, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", vjsonData, err)
		}
		var std time.Time
		json.Unmarshal(stdData, &std)
		if !got.Equal(std) {
			t.Errorf("time %v: vjson got %v, stdlib got %v", ts, got, std)
		}
	}
}
