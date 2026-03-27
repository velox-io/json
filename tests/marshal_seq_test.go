package tests

import (
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
)

// TestMarshal_SeqFloat64 — []float64 via C-native sequence iterator

func TestMarshal_SeqFloat64(t *testing.T) {
	type S struct {
		Vals []float64 `json:"vals"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"nil", S{Vals: nil}},
		{"empty", S{Vals: []float64{}}},
		{"single", S{Vals: []float64{3.14}}},
		{"multiple", S{Vals: []float64{1.0, 2.5, -3.14, 0}}},
		{"large_values", S{Vals: []float64{1e15, -1e15, 1.23456789}}},
		{"integers_as_float", S{Vals: []float64{1, 2, 3, 100, -42}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestMarshal_SeqInt — []int via C-native sequence iterator

func TestMarshal_SeqInt(t *testing.T) {
	type S struct {
		Vals []int `json:"vals"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"nil", S{Vals: nil}},
		{"empty", S{Vals: []int{}}},
		{"single", S{Vals: []int{42}}},
		{"multiple", S{Vals: []int{1, -2, 3, 0, 100}}},
		{"large_values", S{Vals: []int{int(^uint(0) >> 1), -int(^uint(0)>>1) - 1}}},
		{"zeros", S{Vals: []int{0, 0, 0}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestMarshal_SeqInt64 — []int64 via C-native sequence iterator

func TestMarshal_SeqInt64(t *testing.T) {
	type S struct {
		Vals []int64 `json:"vals"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"nil", S{Vals: nil}},
		{"empty", S{Vals: []int64{}}},
		{"single", S{Vals: []int64{42}}},
		{"multiple", S{Vals: []int64{1, -2, 3, 0, 1000000}}},
		{"extremes", S{Vals: []int64{1<<63 - 1, -1 << 63}}},
		{"mixed_magnitude", S{Vals: []int64{0, 1, -1, 256, -999999999999, 1 << 62}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestMarshal_SeqString — []string via C-native sequence iterator

func TestMarshal_SeqString(t *testing.T) {
	type S struct {
		Vals []string `json:"vals"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"nil", S{Vals: nil}},
		{"empty", S{Vals: []string{}}},
		{"single", S{Vals: []string{"hello"}}},
		{"multiple", S{Vals: []string{"a", "b", "c"}}},
		{"empty_strings", S{Vals: []string{"", "", ""}}},
		{"unicode", S{Vals: []string{"中文", "日本語", "한국어"}}},
		{"escaped", S{Vals: []string{`"quoted"`, "line\nnew", "tab\there"}}},
		{"mixed", S{Vals: []string{"normal", "", "with spaces", "emoji 😀"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestMarshal_SeqOmitempty — sequence fields with omitempty tag

func TestMarshal_SeqOmitempty(t *testing.T) {
	type S struct {
		Label   string    `json:"label"`
		Floats  []float64 `json:"floats,omitempty"`
		Ints    []int     `json:"ints,omitempty"`
		Int64s  []int64   `json:"int64s,omitempty"`
		Strings []string  `json:"strings,omitempty"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"all_nil", S{Label: "test"}},
		{"all_empty", S{
			Label:   "test",
			Floats:  []float64{},
			Ints:    []int{},
			Int64s:  []int64{},
			Strings: []string{},
		}},
		{"some_present", S{
			Label:  "test",
			Ints:   []int{1, 2},
			Int64s: nil,
		}},
		{"all_present", S{
			Label:   "test",
			Floats:  []float64{1.5},
			Ints:    []int{42},
			Int64s:  []int64{100},
			Strings: []string{"ok"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestMarshal_SeqPointer — pointer to sequence fields

func TestMarshal_SeqPointer(t *testing.T) {
	type S struct {
		Floats  *[]float64 `json:"floats"`
		Ints    *[]int     `json:"ints"`
		Int64s  *[]int64   `json:"int64s"`
		Strings *[]string  `json:"strings"`
	}

	f := []float64{1.5, 2.5}
	i := []int{1, 2, 3}
	i64 := []int64{100, 200}
	s := []string{"a", "b"}

	cases := []struct {
		name string
		val  S
	}{
		{"all_nil", S{}},
		{"all_present", S{Floats: &f, Ints: &i, Int64s: &i64, Strings: &s}},
		{"mixed", S{Floats: &f, Ints: nil, Int64s: &i64, Strings: nil}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestMarshal_SeqMultiField — struct with multiple seq fields of different types

func TestMarshal_SeqMultiField(t *testing.T) {
	type S struct {
		Name    string    `json:"name"`
		Floats  []float64 `json:"floats"`
		Ints    []int     `json:"ints"`
		Int64s  []int64   `json:"int64s"`
		Strings []string  `json:"strings"`
		Extra   int       `json:"extra"`
	}

	cases := []struct {
		name string
		val  S
	}{
		{"all_nil", S{Name: "test", Extra: 1}},
		{"all_empty", S{
			Name: "test", Floats: []float64{}, Ints: []int{},
			Int64s: []int64{}, Strings: []string{}, Extra: 2,
		}},
		{"all_populated", S{
			Name:    "test",
			Floats:  []float64{1.1, 2.2},
			Ints:    []int{10, 20, 30},
			Int64s:  []int64{1 << 40, -(1 << 40)},
			Strings: []string{"hello", "world"},
			Extra:   99,
		}},
		{"mixed_nil_and_values", S{
			Name:    "mixed",
			Floats:  nil,
			Ints:    []int{1},
			Int64s:  nil,
			Strings: []string{"only"},
			Extra:   0,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vjson.Marshal(tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}

// TestMarshal_SeqIndent — sequence fields with indentation

func TestMarshal_SeqIndent(t *testing.T) {
	type S struct {
		Floats  []float64 `json:"floats"`
		Ints    []int     `json:"ints"`
		Int64s  []int64   `json:"int64s"`
		Strings []string  `json:"strings"`
	}

	val := S{
		Floats:  []float64{1.5, -2.5, 0},
		Ints:    []int{1, 2, 3},
		Int64s:  []int64{100, -200},
		Strings: []string{"a", "b", "c"},
	}

	got, err := vjson.MarshalIndent(val, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.MarshalIndent(val, "", "  ")
	if string(got) != string(want) {
		t.Errorf("mismatch:\n  got:\n%s\n  want:\n%s", got, want)
	}
}

// TestMarshal_SeqEscapeHTML — sequence string fields with HTML escaping

func TestMarshal_SeqEscapeHTML(t *testing.T) {
	type S struct {
		Vals []string `json:"vals"`
	}

	val := S{Vals: []string{
		"<script>alert('xss')</script>",
		"a & b",
		`"quoted"`,
		"normal",
	}}

	got, err := vjson.Marshal(val, vjson.WithEscapeHTML())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(val) // encoding/json escapes HTML by default
	if string(got) != string(want) {
		t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}
