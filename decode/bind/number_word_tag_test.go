package bind

import (
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// A number's value word is an arbitrary 8 bytes. Nothing constrains its high bits,
// so it can hold any pattern at all, including one whose high byte equals the seam
// tag. Any walk that inspects a value word as if it were a tag word will therefore
// misread some legal input.
//
// It did. The tape-bind number path stepped onto the value word with the same
// macro used to step between entries, and that macro follows seams. For a double
// whose bits are 0x4A00000000000000 (a real value: 2.923003274661806e+48) the high
// byte is 'J', so the word read as a seam, its distance field read as zero, and
// the walk advanced by nothing. Not a wrong answer, a hang: measured at rc=124
// under a 90s timeout, and confirmed present before the seam encoding changed, so
// it was reachable for as long as the merged tape has existed.
//
// The invariant the fix restores is that a seam appears only where a tag word may
// appear. tape_value_end already respected it, stepping over an l/u/d value word
// with a bare p++; TAP_READ_NUMBER brings the cursor macros in line. Note what
// that buys beyond this one pattern: if a value word is never examined, no tag
// encoding can collide with one, so the tag's width stops being load-bearing.
//
// These cases need the merged tape, because that is the only tape carrying seams:
// a contiguous one has none and the misread has nothing to latch onto.

type numAliasHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type numAliasCase struct {
	D    float64 `json:"d"`
	I    int64   `json:"i"`
	Next int     `json:"next"`
}

func init() {
	vbind.DefineVariantCases[numAliasHost, struct {
		_ numAliasCase `case:"c1"`
	}]()
}

// Values whose bit patterns collide with the seam tag, alongside controls that do
// not. The test asserts the decode is correct; a regression does not fail it, it
// hangs, which the go test timeout reports.
func TestNumberValueWordNotReadAsTag(t *testing.T) {
	cases := []struct {
		name string
		json string
		want float64
	}{
		// 0x4A00000000000000: high byte 'J', distance field zero. The exact word
		// that hung, so it stays even though the fix is not pattern-specific.
		{"seam_tag_zero_distance", `2.923003274661806e+48`, 2.923003274661806e+48},
		// 0x4A00000000000005: high byte 'J' with a nonzero distance, which advanced
		// by five words instead of spinning. A silently wrong decode rather than a
		// hang, and the more dangerous of the two.
		{"seam_tag_nonzero_distance", `2.923003274661809e+48`, 2.923003274661809e+48},
		// Negative doubles have the top bit set. Harmless against an 8-bit tag, and
		// the reason a 1-bit tag cannot be adopted until this fix is in place: it
		// would turn a 1-in-256 collision into 1-in-2.
		{"negative", `-1.5`, -1.5},
		{"negative_large", `-1.7976931348623157e+308`, -1.7976931348623157e+308},
		{"control_small", `1.5`, 1.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := `{"kind":"c1","d":` + c.json + `,"next":7,"unknown":1}`
			var h numAliasHost
			if err := Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			cc, ok := h.Case.(numAliasCase)
			if !ok {
				t.Fatalf("Case = %T, want numAliasCase", h.Case)
			}
			if cc.D != c.want {
				t.Errorf("D = %v, want %v", cc.D, c.want)
			}
			// Next is the assertion that catches a misread which did not hang: the
			// walk must arrive at the following entry, not somewhere past it.
			if cc.Next != 7 {
				t.Errorf("Next = %d, want 7 (the walk lost its place after the number)", cc.Next)
			}
			// And the other view must still see its own entry.
			if got := h.Rest.String(); got != `{"unknown":1}` {
				t.Errorf("Rest = %s, want {\"unknown\":1}", got)
			}
		})
	}
}

// The same collision through an integer value word, which is stored with a
// different tag and read by the same arm. int64 has no unused bits at all, so any
// 64-bit pattern is a legal value.
func TestIntegerValueWordNotReadAsTag(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int64
	}{
		// 0x4A00000000000000 as an integer: high byte 'J', distance zero.
		{"seam_tag_zero_distance", `5332261958806667264`, 5332261958806667264},
		{"seam_tag_nonzero_distance", `5332261958806667269`, 5332261958806667269},
		// Top bit set, i.e. every negative integer.
		{"negative_one", `-1`, -1},
		{"min_int64", `-9223372036854775808`, -9223372036854775808},
		{"control", `42`, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := `{"kind":"c1","i":` + c.json + `,"next":7,"unknown":1}`
			var h numAliasHost
			if err := Unmarshal([]byte(src), &h); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			cc, ok := h.Case.(numAliasCase)
			if !ok {
				t.Fatalf("Case = %T, want numAliasCase", h.Case)
			}
			if cc.I != c.want {
				t.Errorf("I = %d, want %d", cc.I, c.want)
			}
			if cc.Next != 7 {
				t.Errorf("Next = %d, want 7 (the walk lost its place after the number)", cc.Next)
			}
		})
	}
}

// The colliding word inside a value.Value, which is walked by UnmarshalValue
// rather than by the field arms above. A separate path over the same words, so a
// fix applied to only one of them shows up here.
func TestNumberValueWordThroughUnmarshalValue(t *testing.T) {
	var h numAliasHost
	src := `{"kind":"c1","next":7,"u":2.923003274661806e+48,"v":9}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	var out struct {
		U float64 `json:"u"`
		V int     `json:"v"`
	}
	if err := UnmarshalValue(h.Rest, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if out.U != 2.923003274661806e+48 {
		t.Errorf("U = %v, want 2.923003274661806e+48", out.U)
	}
	if out.V != 9 {
		t.Errorf("V = %d, want 9 (the walk lost its place after the number)", out.V)
	}
}
