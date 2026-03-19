package vjson

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type wrapMapStrStr struct {
	M map[string]string `json:"m"`
}
type wrapMapStrInt struct {
	M map[string]int `json:"m"`
}
type wrapMapIntStr struct {
	M map[int]string `json:"m"`
}
type wrapMapStrInt64 struct {
	M map[string]int64 `json:"m"`
}
type wrapMapStrSlice struct {
	M map[string][]int `json:"m"`
}
type wrapMapStrMap struct {
	M map[string]map[string]int `json:"m"`
}

// marshalMapStrStr marshals val through a struct field to hit the native VM.
func marshalMapStrStr(t *testing.T, val map[string]string) []byte {
	t.Helper()
	w := wrapMapStrStr{M: val}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatalf("Marshal(%v) error: %v", val, err)
	}
	return got
}

// stdlibMapStrStr returns encoding/json's output for a map[string]string via a struct field.
func stdlibMapStrStr(t *testing.T, val map[string]string) []byte {
	t.Helper()
	w := wrapMapStrStr{M: val}
	got, _ := json.Marshal(w)
	return got
}

// marshalMapStrInt marshals map[string]int through a struct field.
func marshalMapStrInt(t *testing.T, val map[string]int) []byte {
	t.Helper()
	w := wrapMapStrInt{M: val}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatalf("Marshal(%v) error: %v", val, err)
	}
	return got
}

// stdlibMapStrInt returns encoding/json's output for map[string]int.
func stdlibMapStrInt(t *testing.T, val map[string]int) []byte {
	t.Helper()
	w := wrapMapStrInt{M: val}
	got, _ := json.Marshal(w)
	return got
}

// marshalMapStrInt64 marshals map[string]int64 through a struct field.
func marshalMapStrInt64(t *testing.T, val map[string]int64) []byte {
	t.Helper()
	w := wrapMapStrInt64{M: val}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatalf("Marshal(%v) error: %v", val, err)
	}
	return got
}

// stdlibMapStrInt64 returns encoding/json's output for map[string]int64.
func stdlibMapStrInt64(t *testing.T, val map[string]int64) []byte {
	t.Helper()
	w := wrapMapStrInt64{M: val}
	got, _ := json.Marshal(w)
	return got
}

// marshalMapIntStr marshals map[int]string through a struct field.
func marshalMapIntStr(t *testing.T, val map[int]string) []byte {
	t.Helper()
	w := wrapMapIntStr{M: val}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatalf("Marshal(%v) error: %v", val, err)
	}
	return got
}

// stdlibMapIntStr returns encoding/json's output for map[int]string.
func stdlibMapIntStr(t *testing.T, val map[int]string) []byte {
	t.Helper()
	w := wrapMapIntStr{M: val}
	got, _ := json.Marshal(w)
	return got
}

// mapsEqual checks if two JSON objects with map content are equivalent
// (keys may be in different order due to map iteration randomness).
func mapsEqual(got, want []byte) bool {
	// Parse both as generic JSON and compare with sorted keys.
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		return false
	}
	if err := json.Unmarshal(want, &wantVal); err != nil {
		return false
	}
	return equalJSON(gotVal, wantVal)
}

// equalJSON compares two JSON values for equality, handling map key order.
func equalJSON(a, b any) bool {
	switch a := a.(type) {
	case map[string]any:
		bm, ok := b.(map[string]any)
		if !ok || len(a) != len(bm) {
			return false
		}
		for k, av := range a {
			bv, ok := bm[k]
			if !ok || !equalJSON(av, bv) {
				return false
			}
		}
		return true
	case []any:
		bs, ok := b.([]any)
		if !ok || len(a) != len(bs) {
			return false
		}
		for i := range a {
			if !equalJSON(a[i], bs[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// ---------------------------------------------------------------------------
// TestNativeMapStrStr — table-driven tests for map[string]string via native VM
// ---------------------------------------------------------------------------

func TestNativeMapStrStr(t *testing.T) {

	cases := []struct {
		name string
		val  map[string]string
	}{
		// empty and nil
		{"empty", map[string]string{}},
		{"nil", nil},

		// single entry
		{"single", map[string]string{"key": "value"}},
		{"single_empty_key", map[string]string{"": "empty_key"}},
		{"single_empty_value", map[string]string{"empty_val": ""}},

		// multiple entries
		{"two_entries", map[string]string{"a": "1", "b": "2"}},
		{"three_entries", map[string]string{"x": "xx", "y": "yy", "z": "zz"}},

		// special characters in keys/values
		{"unicode_key", map[string]string{"中文": "chinese"}},
		{"unicode_value", map[string]string{"key": "日本語"}},
		{"unicode_both", map[string]string{"日本": "東京", "中国": "北京"}},
		{"emoji", map[string]string{"face": "😀", "heart": "❤️"}},
		{"escaped_chars", map[string]string{"quote": `"quoted"`, "backslash": `path\to\file`}},
		{"newline", map[string]string{"key": "line1\nline2"}},
		{"tab", map[string]string{"key": "col1\tcol2"}},

		// various key/value lengths
		{"short_keys", map[string]string{"a": "x", "b": "y", "c": "z"}},
		{"long_key", map[string]string{"this_is_a_very_long_key_name_that_exceeds_normal_length": "value"}},
		{"long_value", map[string]string{"key": "this_is_a_very_long_value_name_that_exceeds_normal_length"}},
		{"long_both", map[string]string{
			"long_key_one":   "long_value_one",
			"long_key_two":   "long_value_two",
			"long_key_three": "long_value_three",
		}},

		// numeric-looking strings
		{"numeric_strings", map[string]string{"1": "one", "2": "two", "3": "three"}},
		{"float_strings", map[string]string{"1.5": "one_point_five", "2.0": "two_point_zero"}},
		{"bool_strings", map[string]string{"true": "yes", "false": "no"}},

		// whitespace keys/values
		{"space_key", map[string]string{" ": "space_key"}},
		{"space_value", map[string]string{"key": " "}},
		{"leading_trailing_spaces", map[string]string{"  key  ": "  value  "}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalMapStrStr(t, tc.val)
			want := stdlibMapStrStr(t, tc.val)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNativeMapStrStr_LargeMap — stress test with many entries
// ---------------------------------------------------------------------------

func TestNativeMapStrStr_LargeMap(t *testing.T) {

	rng := rand.New(rand.NewSource(20260309))

	for _, size := range []int{10, 50, 100, 200, 500} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			m := make(map[string]string, size)
			for i := 0; i < size; i++ {
				key := fmt.Sprintf("key_%04d", i)
				value := fmt.Sprintf("value_%d_%s", i, randomString(rng, 10))
				m[key] = value
			}

			got := marshalMapStrStr(t, m)
			want := stdlibMapStrStr(t, m)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch for size=%d:\n  got len: %d\n  want len: %d",
					size, len(got), len(want))
			}
		})
	}
}

// randomString generates a random alphanumeric string of length n.
func randomString(rng *rand.Rand, n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// TestNativeMapStrInt_LargeMap — stress test to trigger BUF_FULL resume
// ---------------------------------------------------------------------------

func TestNativeMapStrInt_LargeMap(t *testing.T) {

	rng := rand.New(rand.NewSource(20260314))

	for _, size := range []int{10, 50, 100, 200, 500} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			m := make(map[string]int, size)
			for i := 0; i < size; i++ {
				key := fmt.Sprintf("key_%04d_%s", i, randomString(rng, 8))
				m[key] = rng.Intn(2000000) - 1000000
			}

			got := marshalMapStrInt(t, m)
			want := stdlibMapStrInt(t, m)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch for size=%d:\n  got len: %d\n  want len: %d",
					size, len(got), len(want))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNativeMapStrInt64_LargeMap — stress test to trigger BUF_FULL resume
// ---------------------------------------------------------------------------

func TestNativeMapStrInt64_LargeMap(t *testing.T) {

	rng := rand.New(rand.NewSource(20260314))

	for _, size := range []int{10, 50, 100, 200, 500} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			m := make(map[string]int64, size)
			for i := 0; i < size; i++ {
				key := fmt.Sprintf("key_%04d_%s", i, randomString(rng, 8))
				m[key] = rng.Int63n(2e18) - 1e18
			}

			got := marshalMapStrInt64(t, m)
			want := stdlibMapStrInt64(t, m)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch for size=%d:\n  got len: %d\n  want len: %d",
					size, len(got), len(want))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNativeMapStrInt — map[string]int via C-native Swiss Map iteration
// ---------------------------------------------------------------------------

func TestNativeMapStrInt(t *testing.T) {

	cases := []struct {
		name string
		val  map[string]int
	}{
		{"empty", map[string]int{}},
		{"nil", nil},
		{"single", map[string]int{"count": 42}},
		{"multiple", map[string]int{"a": 1, "b": 2, "c": 3}},
		{"negative", map[string]int{"neg": -100, "pos": 100}},
		{"zero", map[string]int{"zero": 0, "nonzero": 1}},
		{"large_values", map[string]int{"max_int": int(^uint(0) >> 1), "min_int": -int(^uint(0)>>1) - 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalMapStrInt(t, tc.val)
			want := stdlibMapStrInt(t, tc.val)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNativeMapStrInt64 — map[string]int64 via C-native Swiss Map iteration
// ---------------------------------------------------------------------------

func TestNativeMapStrInt64(t *testing.T) {

	cases := []struct {
		name string
		val  map[string]int64
	}{
		{"empty", map[string]int64{}},
		{"nil", nil},
		{"single", map[string]int64{"count": 42}},
		{"multiple", map[string]int64{"a": 1, "b": 2, "c": 3}},
		{"negative", map[string]int64{"neg": -100, "pos": 100}},
		{"zero", map[string]int64{"zero": 0, "nonzero": 1}},
		{"large_values", map[string]int64{
			"max_int64": 1<<63 - 1,
			"min_int64": -1 << 63,
		}},
		{"mixed_magnitude", map[string]int64{
			"tiny":     1,
			"small":    256,
			"medium":   1000000,
			"large":    1<<62 - 1,
			"negative": -999999999999,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalMapStrInt64(t, tc.val)
			want := stdlibMapStrInt64(t, tc.val)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNativeMapIntStr — map[int]string via native VM (non-string key)
// ---------------------------------------------------------------------------

func TestNativeMapIntStr(t *testing.T) {

	cases := []struct {
		name string
		val  map[int]string
	}{
		{"empty", map[int]string{}},
		{"nil", nil},
		{"single", map[int]string{1: "one"}},
		{"multiple", map[int]string{1: "one", 2: "two", 3: "three"}},
		{"negative_keys", map[int]string{-1: "minus_one", -100: "minus_hundred"}},
		{"zero_key", map[int]string{0: "zero"}},
		{"large_keys", map[int]string{1000000: "million", 999999999: "big"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalMapIntStr(t, tc.val)
			want := stdlibMapIntStr(t, tc.val)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNativeMap_Nested — nested maps via native VM
// ---------------------------------------------------------------------------

func TestNativeMap_Nested(t *testing.T) {

	t.Run("map_of_slice", func(t *testing.T) {
		val := map[string][]int{
			"one":   {1},
			"two":   {1, 2},
			"three": {1, 2, 3},
			"empty": {},
		}
		w := wrapMapStrSlice{M: val}
		got, err := Marshal(&w)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(w)
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
		}
	})

	t.Run("map_of_map", func(t *testing.T) {
		val := map[string]map[string]int{
			"outer1": {"inner1": 1, "inner2": 2},
			"outer2": {"inner3": 3},
			"empty":  {},
		}
		w := wrapMapStrMap{M: val}
		got, err := Marshal(&w)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(w)
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
		}
	})

	t.Run("deeply_nested", func(t *testing.T) {
		type Inner struct {
			Data map[string]string `json:"data"`
		}
		type Outer struct {
			Items map[string]Inner `json:"items"`
		}
		val := Outer{
			Items: map[string]Inner{
				"first":  {Data: map[string]string{"a": "1"}},
				"second": {Data: map[string]string{"b": "2", "c": "3"}},
			},
		}
		got, err := Marshal(&val)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(val)
		// Map iteration order is non-deterministic, so compare via
		// unmarshal round-trip instead of raw string equality.
		var gotObj, wantObj Outer
		if err := json.Unmarshal(got, &gotObj); err != nil {
			t.Fatalf("unmarshal got: %v", err)
		}
		if err := json.Unmarshal(want, &wantObj); err != nil {
			t.Fatalf("unmarshal want: %v", err)
		}
		if !reflect.DeepEqual(gotObj, wantObj) {
			t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// TestNativeMap_InStruct — maps as struct fields with various configurations
// ---------------------------------------------------------------------------

func TestNativeMap_InStruct(t *testing.T) {

	type MapStruct struct {
		Data      map[string]string `json:"data"`
		Optional  map[string]int    `json:"optional,omitempty"`
		Pointer   *map[string]int   `json:"pointer"`
		Ignored   map[string]string `json:"-"`
		Named     map[string]string `json:"named_field"`
		Counters  map[string]int64  `json:"counters"`
		OptInt64  map[string]int64  `json:"opt_int64,omitempty"`
	}

	cases := []struct {
		name string
		val  MapStruct
	}{
		{"empty", MapStruct{}},
		{"nil_pointer", MapStruct{Data: map[string]string{"a": "b"}}},
		{"with_optional", MapStruct{Data: map[string]string{"x": "y"}, Optional: map[string]int{"n": 1}}},
		{"with_counters", MapStruct{
			Counters: map[string]int64{"hits": 999999999999, "misses": -1},
		}},
		{"omitempty_int64_nil", MapStruct{
			Data:     map[string]string{"x": "y"},
			OptInt64: nil, // should be omitted
		}},
		{"omitempty_int64_nonempty", MapStruct{
			OptInt64: map[string]int64{"n": 42},
		}},
		{"all_fields", MapStruct{
			Data:     map[string]string{"key": "value"},
			Optional: map[string]int{"num": 42},
			Pointer:  &map[string]int{"ptr": 100},
			Named:    map[string]string{"name": "test"},
			Counters: map[string]int64{"c": 1<<62 - 1},
			OptInt64: map[string]int64{"big": -1 << 63},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Marshal(&tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if !mapsEqual(got, want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNativeMap_WithIndent — maps with indentation
// ---------------------------------------------------------------------------

func TestNativeMap_WithIndent(t *testing.T) {

	t.Run("string_string", func(t *testing.T) {
		val := map[string]string{
			"alpha":   "first",
			"beta":    "second",
			"gamma":   "third",
			"delta":   "fourth",
			"epsilon": "fifth",
		}
		got, err := MarshalIndent(&val, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.MarshalIndent(val, "", "  ")
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:\n%s\n  want:\n%s", got, want)
		}
	})

	t.Run("string_int", func(t *testing.T) {
		val := map[string]int{
			"alpha": 1, "beta": 2, "gamma": 3,
			"delta": -100, "epsilon": 0,
		}
		got, err := MarshalIndent(&val, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.MarshalIndent(val, "", "  ")
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:\n%s\n  want:\n%s", got, want)
		}
	})

	t.Run("string_int64", func(t *testing.T) {
		val := map[string]int64{
			"small": 42, "large": 1<<62 - 1, "negative": -999999999999,
		}
		got, err := MarshalIndent(&val, "", "\t")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.MarshalIndent(val, "", "\t")
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:\n%s\n  want:\n%s", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// TestNativeMap_EscapeHTML — maps with HTML-escaping enabled
// ---------------------------------------------------------------------------

func TestNativeMap_EscapeHTML(t *testing.T) {

	t.Run("string_string", func(t *testing.T) {
		val := map[string]string{
			"script": "<script>alert('xss')</script>",
			"link":   "<a href=\"test\">link</a>",
			"amp":    "tom & jerry",
			"quote":  `"quoted"`,
		}
		got, err := Marshal(&val, WithEscapeHTML())
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(val) // encoding/json escapes HTML by default
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
		}
	})

	t.Run("string_int", func(t *testing.T) {
		// Keys contain HTML-sensitive chars; values are ints (no escaping needed).
		val := map[string]int{
			"<b>bold</b>": 1,
			"a&b":         2,
			"normal":      3,
		}
		got, err := Marshal(&val, WithEscapeHTML())
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(val)
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
		}
	})

	t.Run("string_int64", func(t *testing.T) {
		val := map[string]int64{
			"<tag>": 999999999999,
			"a&b":   -42,
		}
		got, err := Marshal(&val, WithEscapeHTML())
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(val)
		if !mapsEqual(got, want) {
			t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// TestNativeMap_Roundtrip — verify marshal/unmarshal roundtrip
// ---------------------------------------------------------------------------

func TestNativeMap_Roundtrip(t *testing.T) {

	t.Run("string_string", func(t *testing.T) {
		original := map[string]string{
			"key1": "value1",
			"key2": "value2",
			"key3": "value with spaces",
		}
		got, err := Marshal(&original)
		if err != nil {
			t.Fatal(err)
		}

		var decoded map[string]string
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if len(decoded) != len(original) {
			t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(original))
		}
		for k, v := range original {
			if decoded[k] != v {
				t.Errorf("decoded[%q] = %q, want %q", k, decoded[k], v)
			}
		}
	})

	t.Run("string_int", func(t *testing.T) {
		original := map[string]int{"a": 1, "b": 2, "c": -100}
		got, err := Marshal(&original)
		if err != nil {
			t.Fatal(err)
		}

		var decoded map[string]int
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatal(err)
		}

		for k, v := range original {
			if decoded[k] != v {
				t.Errorf("decoded[%q] = %d, want %d", k, decoded[k], v)
			}
		}
	})

	t.Run("string_int64", func(t *testing.T) {
		original := map[string]int64{
			"small": 42, "large": 1<<62 - 1, "negative": -999999999999,
		}
		got, err := Marshal(&original)
		if err != nil {
			t.Fatal(err)
		}

		var decoded map[string]int64
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatal(err)
		}

		for k, v := range original {
			if decoded[k] != v {
				t.Errorf("decoded[%q] = %d, want %d", k, decoded[k], v)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TestNativeMap_Marshaler — map with custom MarshalJSON keys
// ---------------------------------------------------------------------------

type TextKey struct {
	Name string
	Age  int
}

func (k TextKey) MarshalText() ([]byte, error) {
	return fmt.Appendf(nil, "%s:%d", k.Name, k.Age), nil
}

func TestNativeMap_Marshaler(t *testing.T) {

	type KeyMarshalMap struct {
		M map[TextKey]string `json:"m"`
	}

	val := KeyMarshalMap{
		M: map[TextKey]string{
			{Name: "alice", Age: 30}: "engineer",
			{Name: "bob", Age: 25}:   "designer",
		},
	}

	got, err := Marshal(&val)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(val)

	if !mapsEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestNativeMap_SliceOfMap — slice of maps
// ---------------------------------------------------------------------------

func TestNativeMap_SliceOfMap(t *testing.T) {

	type SliceMap struct {
		Items []map[string]string `json:"items"`
	}

	val := SliceMap{
		Items: []map[string]string{
			{"a": "1"},
			{"b": "2", "c": "3"},
			{},
			nil,
		},
	}

	got, err := Marshal(&val)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(val)

	// Use mapsEqual since maps inside struct may have different key order
	if !mapsEqual(got, want) {
		t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestNativeMap_Random — stress test with random map contents
// ---------------------------------------------------------------------------

func TestNativeMap_Random(t *testing.T) {

	rng := rand.New(rand.NewSource(20260309))
	const N = 100

	var mismatches int
	for i := 0; i < N; i++ {
		// Generate random map
		size := rng.Intn(50) + 1
		m := make(map[string]string, size)
		for j := 0; j < size; j++ {
			key := randomString(rng, rng.Intn(20)+1)
			value := randomString(rng, rng.Intn(30)+1)
			m[key] = value
		}

		got := marshalMapStrStr(t, m)
		want := stdlibMapStrStr(t, m)

		if !mapsEqual(got, want) {
			mismatches++
			if mismatches <= 5 {
				t.Errorf("random #%d mismatch:\n  got:  %s\n  want: %s", i, got, want)
			}
		}
	}
	if mismatches > 5 {
		t.Errorf("... and %d more mismatches (total %d/%d)", mismatches-5, mismatches, N)
	}
}
