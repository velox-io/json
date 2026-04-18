// Fixed arrays diverge from slices in a few ways that are easy to regress:
// begin_array writes directly into the inline storage, extra JSON elements are
// ignored, missing elements stay zeroed, and null leaves the existing array
// untouched to match encoding/json.

package ndec

import (
	"fmt"
	"strings"
	"testing"
)

type fixedIntBox struct {
	A [3]int `json:"a"`
}

type fixedStringBox struct {
	A [3]string `json:"a"`
}

type fixedBoolBox struct {
	A [4]bool `json:"a"`
}

type fixedFloatBox struct {
	A [2]float64 `json:"a"`
}

func TestFixedArrayInt_Basic(t *testing.T) {
	cases := []string{
		`{"a":[1,2,3]}`,
		`{"a":[1,2]}`,
		`{"a":[1,2,3,4,5]}`,
		`{"a":[]}`,
		`{"a":[0,0,0]}`,
		`{"a":[-1,-2,-3]}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &fixedIntBox{}
			want := &fixedIntBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestFixedArrayInt_NullAndMissing(t *testing.T) {
	// encoding/json leaves an existing fixed array untouched when the field is
	// null or missing, so this test pins the same behavior.
	cases := []string{
		`{"a":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &fixedIntBox{A: [3]int{99, 99, 99}}
			want := &fixedIntBox{A: [3]int{99, 99, 99}}
			runParity(t, in, got, want)
		})
	}
}

// begin_array zeroes the full fixed array before replaying elements, so a
// shorter JSON array must clear any stale tail values left by previous data.
func TestFixedArrayInt_PartialZeroesRest(t *testing.T) {
	cases := []struct {
		in   string
		want fixedIntBox
	}{
		{`{"a":[1]}`, fixedIntBox{A: [3]int{1, 0, 0}}},
		{`{"a":[1,2]}`, fixedIntBox{A: [3]int{1, 2, 0}}},
		{`{"a":[1,2,3]}`, fixedIntBox{A: [3]int{1, 2, 3}}},
		{`{"a":[]}`, fixedIntBox{A: [3]int{0, 0, 0}}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := &fixedIntBox{A: [3]int{99, 99, 99}}
			want := &fixedIntBox{A: [3]int{99, 99, 99}}
			runParity(t, tc.in, got, want)
		})
	}
}

func TestFixedArrayString(t *testing.T) {
	cases := []string{
		`{"a":["x","y","z"]}`,
		`{"a":["only"]}`,
		`{"a":["a\nb","中文","c"]}`,
		`{"a":["a","b","c","d"]}`, // overflow
		`{"a":[]}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &fixedStringBox{}
			want := &fixedStringBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestFixedArrayBool(t *testing.T) {
	cases := []string{
		`{"a":[true,false,true,false]}`,
		`{"a":[true]}`,
		`{"a":[]}`,
		`{"a":[true,false,true,false,true,false]}`, // overflow
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &fixedBoolBox{}
			want := &fixedBoolBox{}
			runParity(t, in, got, want)
		})
	}
}

func TestFixedArrayFloat(t *testing.T) {
	cases := []string{
		`{"a":[1.5,-2.25]}`,
		`{"a":[3.14]}`,
		`{"a":[]}`,
		`{"a":[1,2,3,4]}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got := &fixedFloatBox{}
			want := &fixedFloatBox{}
			runParity(t, in, got, want)
		})
	}
}

// Keep a large array case to ensure the zero-fill and overflow-ignore paths do
// not regress only at bigger element counts.
func TestFixedArrayLarge(t *testing.T) {
	type largeBox struct {
		A [100]int32 `json:"a"`
	}
	for _, fill := range []int{50, 99, 100, 101, 500} {
		t.Run(fmt.Sprintf("fill=%d", fill), func(t *testing.T) {
			var sb strings.Builder
			sb.WriteString(`{"a":[`)
			for i := range fill {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, "%d", i)
			}
			sb.WriteString(`]}`)
			got := &largeBox{}
			want := &largeBox{}
			runParity(t, sb.String(), got, want)
		})
	}
}
