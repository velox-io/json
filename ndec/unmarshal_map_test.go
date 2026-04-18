package ndec

import (
	"fmt"
	"strings"
	"testing"
)

type mapStringStringBox struct {
	M map[string]string `json:"m"`
}

type mapTwoBox struct {
	A map[string]string `json:"a"`
	B map[string]string `json:"b"`
}

func TestMapStringString_Empty(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, `{"m":{}}`, got, want)
}

func TestMapStringString_Null(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, `{"m":null}`, got, want)
}

func TestMapStringString_Single(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, `{"m":{"k":"v"}}`, got, want)
}

func TestMapStringString_Many(t *testing.T) {
	for _, n := range []int{1, 5, 15, 16, 17, 31, 32, 33, 50, 64, 65, 100} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"k%d":"v%d"`, i, i)
			}
			sb.WriteString(`}}`)
			got := &mapStringStringBox{}
			want := &mapStringStringBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

func TestMapStringString_EscapeKey(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	// Escaped keys must survive the scratch-based unescape path before map lookup.
	runParity(t, `{"m":{"a\nb":"v","cé":"w"}}`, got, want)
}

func TestMapStringString_EscapeValue(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, `{"m":{"k1":"v\nx","k2":"aéb"}}`, got, want)
}

func TestMapStringString_TwoFields(t *testing.T) {
	got := &mapTwoBox{}
	want := &mapTwoBox{}
	runParity(t, `{"a":{"k":"v"},"b":{"x":"y","z":"w"}}`, got, want)
}

// Crossing the first flush boundary must reset the staged KV buffer so the
// next pair reuses slot 0 instead of corrupting the previous batch.
func TestMapStringString_FlushBoundary(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	var sb strings.Builder
	sb.WriteString(`{"m":{`)
	for i := range mapKVBufCount + 1 {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"k%02d":"v%02d"`, i, i)
	}
	sb.WriteString(`}}`)
	runParity(t, sb.String(), got, want)
}

type mapStringIntBox struct {
	M map[string]int `json:"m"`
}
type mapStringInt64Box struct {
	M map[string]int64 `json:"m"`
}
type mapStringUint32Box struct {
	M map[string]uint32 `json:"m"`
}
type mapStringFloat64Box struct {
	M map[string]float64 `json:"m"`
}
type mapStringBoolBox struct {
	M map[string]bool `json:"m"`
}

func TestMapStringInt(t *testing.T) {
	got := &mapStringIntBox{}
	want := &mapStringIntBox{}
	runParity(t, `{"m":{"a":1,"b":-2,"c":1234567}}`, got, want)
}

func TestMapStringInt64_Boundary(t *testing.T) {
	for _, n := range []int{1, 16, 17, 32, 33, 50} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := 0; i < n; i++ {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"k%d":%d`, i, int64(i)*1000+int64(i))
			}
			sb.WriteString(`}}`)
			got := &mapStringInt64Box{}
			want := &mapStringInt64Box{}
			runParity(t, sb.String(), got, want)
		})
	}
}

func TestMapStringUint32(t *testing.T) {
	got := &mapStringUint32Box{}
	want := &mapStringUint32Box{}
	runParity(t, `{"m":{"a":0,"b":255,"c":4294967295}}`, got, want)
}

func TestMapStringFloat64(t *testing.T) {
	got := &mapStringFloat64Box{}
	want := &mapStringFloat64Box{}
	runParity(t, `{"m":{"a":1.5,"b":-2.25,"c":1e10}}`, got, want)
}

func TestMapStringBool(t *testing.T) {
	got := &mapStringBoolBox{}
	want := &mapStringBoolBox{}
	runParity(t, `{"m":{"t":true,"f":false}}`, got, want)
}

// Null map values for scalar element types collapse to the zero value in the same way as stdlib.
func TestMapStringInt_NullValue(t *testing.T) {
	got := &mapStringIntBox{}
	want := &mapStringIntBox{}
	runParity(t, `{"m":{"a":1,"b":null,"c":3}}`, got, want)
}

func TestMapStringString_NullValue(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, `{"m":{"a":"x","b":null,"c":"z"}}`, got, want)
}

type mapInnerVal struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type mapStringStructBox struct {
	M map[string]mapInnerVal `json:"m"`
}

func TestMapStringStruct_Single(t *testing.T) {
	got := &mapStringStructBox{}
	want := &mapStringStructBox{}
	runParity(t, `{"m":{"u1":{"name":"Alice","age":30}}}`, got, want)
}

func TestMapStringStruct_Many(t *testing.T) {
	for _, n := range []int{1, 16, 17, 32, 33, 50} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"u%d":{"name":"name_%d","age":%d}`, i, i, i*10)
			}
			sb.WriteString(`}}`)
			got := &mapStringStructBox{}
			want := &mapStringStructBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

// Reused struct slots must be zeroed before each value so missing fields do
// not leak data from the previous map element.
func TestMapStringStruct_PartialFields(t *testing.T) {
	got := &mapStringStructBox{}
	want := &mapStringStructBox{}
	runParity(t, `{"m":{"a":{"age":1,"name":"x"},"b":{"name":"y"},"c":{"age":3}}}`, got, want)
}

// Keep one case where escaped map keys and escaped nested fields are both decoded.
func TestMapStringStruct_Escape(t *testing.T) {
	got := &mapStringStructBox{}
	want := &mapStringStructBox{}
	runParity(t, `{"m":{"a\nb":{"name":"x\ny","age":7}}}`, got, want)
}

// Null for a struct-valued map entry should decode to that struct's zero value.
func TestMapStringStruct_NullValue(t *testing.T) {
	got := &mapStringStructBox{}
	want := &mapStringStructBox{}
	runParity(t, `{"m":{"a":{"name":"x","age":1},"b":null}}`, got, want)
}

type mapStringPtrIntBox struct {
	M map[string]*int `json:"m"`
}

type mapStringPtrStringBox struct {
	M map[string]*string `json:"m"`
}

type mapStringPtrBoolBox struct {
	M map[string]*bool `json:"m"`
}

type mapPtrInnerVal struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type mapStringPtrStructBox struct {
	M map[string]*mapPtrInnerVal `json:"m"`
}

func TestMapStringPtrInt_Basic(t *testing.T) {
	cases := []string{
		`{"m":{}}`,
		`{"m":null}`,
		`{"m":{"a":1}}`,
		`{"m":{"a":1,"b":null,"c":-2}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &mapStringPtrIntBox{}
			want := &mapStringPtrIntBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestMapStringPtrInt_FlushBoundary(t *testing.T) {
	for _, n := range []int{1, 16, 17, 32, 33} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"k%d":%d`, i, i*7-3)
			}
			sb.WriteString(`}}`)
			got := &mapStringPtrIntBox{}
			want := &mapStringPtrIntBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

func TestMapStringPtrString_EscapeAndNull(t *testing.T) {
	got := &mapStringPtrStringBox{}
	want := &mapStringPtrStringBox{}
	runParity(t, `{"m":{"a\nb":"x\ny","empty":"","nil":null,"utf8":"中文"}}`, got, want)
}

func TestMapStringPtrBool(t *testing.T) {
	got := &mapStringPtrBoolBox{}
	want := &mapStringPtrBoolBox{}
	runParity(t, `{"m":{"t":true,"f":false,"n":null}}`, got, want)
}

func TestMapStringPtrStruct_Basic(t *testing.T) {
	cases := []string{
		`{"m":{}}`,
		`{"m":null}`,
		`{"m":{"a":{"name":"x","age":1}}}`,
		`{"m":{"a":{"name":"x","age":1},"b":null}}`,
		`{"m":{"a":{"age":1},"b":{"name":"y"}}}`,
		`{"m":{"a\nb":{"name":"x\ny","age":7}}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &mapStringPtrStructBox{}
			want := &mapStringPtrStructBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestMapStringPtrStruct_Many(t *testing.T) {
	for _, n := range []int{1, 16, 17, 32, 33} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"u%d":{"name":"name_%d","age":%d}`, i, i, i*10)
			}
			sb.WriteString(`}}`)
			got := &mapStringPtrStructBox{}
			want := &mapStringPtrStructBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

type mapStringMapIntBox struct {
	M map[string]map[string]int `json:"m"`
}

type mapStringMapStringBox struct {
	M map[string]map[string]string `json:"m"`
}

type mapStringMapStructBox struct {
	M map[string]map[string]mapInnerVal `json:"m"`
}

type mapStringMapPtrIntBox struct {
	M map[string]map[string]*int `json:"m"`
}

func TestMapStringMapInt_Basic(t *testing.T) {
	cases := []string{
		`{"m":{}}`,
		`{"m":{"a":{},"b":null,"c":{"x":1,"y":-2}}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &mapStringMapIntBox{}
			want := &mapStringMapIntBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestMapStringMapInt_InnerFlushBoundary(t *testing.T) {
	for _, n := range []int{1, 16, 17, 32, 33} {
		t.Run(fmt.Sprintf("inner=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{"outer":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"k%d":%d`, i, i*11-5)
			}
			sb.WriteString(`}}}`)
			got := &mapStringMapIntBox{}
			want := &mapStringMapIntBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

func TestMapStringMapInt_OuterFlushBoundary(t *testing.T) {
	for _, n := range []int{16, 17} {
		t.Run(fmt.Sprintf("outer=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"outer%d":{"k":%d}`, i, i)
			}
			sb.WriteString(`}}`)
			got := &mapStringMapIntBox{}
			want := &mapStringMapIntBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

func TestMapStringMapString_Escape(t *testing.T) {
	got := &mapStringMapStringBox{}
	want := &mapStringMapStringBox{}
	runParity(t, `{"m":{"a\nb":{"x\ny":"v\nz","utf8":"中文"}}}`, got, want)
}

func TestMapStringMapStruct(t *testing.T) {
	got := &mapStringMapStructBox{}
	want := &mapStringMapStructBox{}
	runParity(t, `{"m":{"g1":{"u1":{"name":"x","age":1},"u2":{"name":"y"}},"g2":{"u3":{"age":3}}}}`, got, want)
}

func TestMapStringMapPtrInt(t *testing.T) {
	got := &mapStringMapPtrIntBox{}
	want := &mapStringMapPtrIntBox{}
	runParity(t, `{"m":{"a":{"x":1,"y":null},"b":{"z":2}}}`, got, want)
}

// Lazy map allocation is deferred until the first flush. These tests cover the
// three ways that first flush can happen: closing-only, buffer-full, and an
// inner map flushing before its outer map has allocated.
func TestMapLazyAlloc_ClosingFlushOnly(t *testing.T) {
	entries := mapKVBufCount / 2
	var sb strings.Builder
	sb.WriteString(`{"m":{`)
	for i := range entries {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"k%d":"v%d"`, i, i)
	}
	sb.WriteString(`}}`)
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, sb.String(), got, want)
}

// A large map should allocate on the first buffer-full flush, reuse that map
// across later flushes, and keep parent-slot rebasing intact.
func TestMapLazyAlloc_BufferFullFlushFirst(t *testing.T) {
	entries := mapKVBufCount*3 + 1
	var sb strings.Builder
	sb.WriteString(`{"m":{`)
	for i := range entries {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"k%02d":"v%02d"`, i, i)
	}
	sb.WriteString(`}}`)
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, sb.String(), got, want)
}

// Nested maps exercise the handoff where an inner map flushes before the outer
// map has allocated and later flushes continue through the already-rebased parent slot.
func TestMapLazyAlloc_NestedFirstFlush(t *testing.T) {
	innerLarge := mapKVBufCount + 3
	outerEntries := mapKVBufCount + 2
	var sb strings.Builder
	sb.WriteString(`{"m":{`)
	for o := range outerEntries {
		if o > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"o%02d":{`, o)
		n := 2
		if o == 0 {
			n = innerLarge
		}
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `"k%02d":%d`, i, i)
		}
		sb.WriteString(`}`)
	}
	sb.WriteString(`}}`)
	got := &mapStringMapIntBox{}
	want := &mapStringMapIntBox{}
	runParity(t, sb.String(), got, want)
}

// Even an empty object must allocate a non-nil empty map on closing flush when
// the destination map has not been created yet.
func TestMapLazyAlloc_Empty(t *testing.T) {
	got := &mapStringStringBox{}
	want := &mapStringStringBox{}
	runParity(t, `{"m":{}}`, got, want)
}

func TestMapStringStructWithInnerMap_FirstCallRebase(t *testing.T) {
	type inner struct {
		Sub map[string]int `json:"sub"`
	}
	type box struct {
		M map[string]inner `json:"m"`
	}
	got := &box{}
	want := &box{}
	runParity(t, `{"m":{"a":{"sub":{"x":1}}}}`, got, want)
}

type ptrMapStringStringBox struct {
	M *map[string]string `json:"m"`
}

type ptrMapStringIntBox struct {
	M *map[string]int `json:"m"`
}

type ptrMapStringStructBox struct {
	M *map[string]mapInnerVal `json:"m"`
}

func TestPtrMapStringString_Basic(t *testing.T) {
	cases := []string{
		`{"m":{}}`,
		`{"m":null}`,
		`{}`,
		`{"m":{"k":"v"}}`,
		`{"m":{"a":"x","b":"y","c":"z"}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &ptrMapStringStringBox{}
			want := &ptrMapStringStringBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestPtrMapStringInt_FlushBoundary(t *testing.T) {
	for _, n := range []int{1, 16, 17, 32, 33, 50} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"k%d":%d`, i, i*7-3)
			}
			sb.WriteString(`}}`)
			got := &ptrMapStringIntBox{}
			want := &ptrMapStringIntBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

func TestPtrMapStringStruct(t *testing.T) {
	cases := []string{
		`{"m":{"u1":{"name":"Alice","age":30}}}`,
		`{"m":{"a":{"name":"x"},"b":{"age":2}}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &ptrMapStringStructBox{}
			want := &ptrMapStringStructBox{}
			runParity(t, in, got, want)
		})
	}
}

// Non-string map keys rely on the stdlib rule that JSON object keys are parsed
// from strings into the destination integer type before insertion.

type intMapStringBox struct {
	M map[int]string `json:"m"`
}

type int8MapStringBox struct {
	M map[int8]string `json:"m"`
}

type int64MapIntBox struct {
	M map[int64]int `json:"m"`
}

type uint32MapStringBox struct {
	M map[uint32]string `json:"m"`
}

type intMapStructBox struct {
	M map[int]mapInnerVal `json:"m"`
}

type intMapPtrIntBox struct {
	M map[int]*int `json:"m"`
}

func TestMapIntKey_Basic(t *testing.T) {
	cases := []string{
		`{"m":{}}`,
		`{"m":null}`,
		`{}`,
		`{"m":{"1":"a"}}`,
		`{"m":{"1":"a","2":"b","3":"c"}}`,
		`{"m":{"-1":"neg","0":"zero","42":"life"}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &intMapStringBox{}
			want := &intMapStringBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestMapInt8Key_Range(t *testing.T) {
	got := &int8MapStringBox{}
	want := &int8MapStringBox{}
	runParity(t, `{"m":{"-128":"min","127":"max","0":"zero"}}`, got, want)
}

func TestMapInt64Key_Boundary(t *testing.T) {
	for _, n := range []int{1, 16, 17, 32, 33, 50} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"m":{`)
			for i := range n {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `"%d":%d`, i*7-3, i*11)
			}
			sb.WriteString(`}}`)
			got := &int64MapIntBox{}
			want := &int64MapIntBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}

func TestMapUint32Key(t *testing.T) {
	got := &uint32MapStringBox{}
	want := &uint32MapStringBox{}
	runParity(t, `{"m":{"0":"a","4294967295":"max","42":"life"}}`, got, want)
}

func TestMapIntKey_StructValue(t *testing.T) {
	got := &intMapStructBox{}
	want := &intMapStructBox{}
	runParity(t, `{"m":{"1":{"name":"x","age":1},"2":{"name":"y","age":2}}}`, got, want)
}

func TestMapIntKey_PtrIntValue(t *testing.T) {
	got := &intMapPtrIntBox{}
	want := &intMapPtrIntBox{}
	runParity(t, `{"m":{"1":10,"2":null,"3":-30}}`, got, want)
}
