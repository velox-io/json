package tests

import (
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
)

// TestArenaReuse_EscapedStringCorruption verifies that strings decoded via the
// arena allocator are NOT corrupted when the Parser is reused from the pool.
//
// The arena stores unescaped string data (e.g. strings containing \n, \", \uXXXX).
// parserPool.Put resets arenaOff to 0 without releasing the underlying arenaData,
// so the next Unmarshal call may overwrite memory still referenced by the previous
// result's string fields.
func TestArenaReuse_EscapedStringCorruption(t *testing.T) {
	type Item struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	}

	// Both inputs use escaped characters to force the unescape path through
	// the arena (plain strings without escapes are zero-copy from src and
	// don't touch the arena).
	input1 := []byte(`{"name":"hello\nworld","desc":"foo\tbar"}`)
	input2 := []byte(`{"name":"AAAA\nBBBB","desc":"CCCC\tDDDD"}`)

	var item1 Item
	if err := vjson.Unmarshal(input1, &item1); err != nil {
		t.Fatal(err)
	}

	// Snapshot the values before the second Unmarshal reuses the parser.
	want1Name := "hello\nworld"
	want1Desc := "foo\tbar"

	if item1.Name != want1Name {
		t.Fatalf("item1.Name = %q, want %q", item1.Name, want1Name)
	}
	if item1.Desc != want1Desc {
		t.Fatalf("item1.Desc = %q, want %q", item1.Desc, want1Desc)
	}

	// Second unmarshal: if the parser reuses the same arena block, it will
	// overwrite the memory backing item1's strings.
	var item2 Item
	if err := vjson.Unmarshal(input2, &item2); err != nil {
		t.Fatal(err)
	}

	// Verify item2 is correct.
	want2Name := "AAAA\nBBBB"
	want2Desc := "CCCC\tDDDD"
	if item2.Name != want2Name {
		t.Fatalf("item2.Name = %q, want %q", item2.Name, want2Name)
	}
	if item2.Desc != want2Desc {
		t.Fatalf("item2.Desc = %q, want %q", item2.Desc, want2Desc)
	}

	// THE CRITICAL CHECK: item1's strings must still be intact.
	// If the arena was reused, item1.Name and item1.Desc will now contain
	// data from input2 (e.g. "AAAA\nBBBB" instead of "hello\nworld").
	if item1.Name != want1Name {
		t.Errorf("CORRUPTION: item1.Name changed from %q to %q after second Unmarshal",
			want1Name, item1.Name)
	}
	if item1.Desc != want1Desc {
		t.Errorf("CORRUPTION: item1.Desc changed from %q to %q after second Unmarshal",
			want1Desc, item1.Desc)
	}
}

func TestEscapes(t *testing.T) {
	type S struct{}

	var inputs = [][]byte{
		[]byte(`{"":"\0` + strings.Repeat("p", 28) + `"}`), // \0
		[]byte(`{"":"\1` + strings.Repeat("p", 28) + `"}`), // \1
		[]byte(`{"":"\v` + strings.Repeat("p", 28) + `"}`), // \v
	}
	for _, input := range inputs {
		var v S
		err := vjson.Unmarshal(input, &v)
		if err == nil {
			t.Errorf("accepted invalid json string: %+v", input)
		}
	}
}

// An object key carrying escapes decodes through str_arena before the field
// lookup, so its bytes can include characters a raw key never holds, among
// them a literal '"'. A decoded key must match a field name exactly: the
// WINDOW lookup tier once read the embedded quote as the terminator of a
// shorter stored key and bound the prefix ("age\"" against field "age").
func TestEscapedObjectKeyLookup(t *testing.T) {
	type small struct {
		Name   string  `json:"name"`
		Age    int64   `json:"age"`
		Score  float64 `json:"score"`
		Active bool    `json:"active"`
	}
	for _, tc := range []struct {
		input   string
		wantAge int64
	}{
		{`{"age":1}`, 1},           // exact raw key binds
		{`{"\u0061ge":1}`, 1},      // escaped key decoding to "age" binds
		{`{"age\"":1}`, 0},         // decodes to age": must miss
		{`{"age\u0022":1}`, 0},     // same quote via \u escape
		{`{"age\n":1}`, 0},         // age plus newline: must miss
		{`{"age\\":1}`, 0},         // age plus backslash: must miss
		{`{"age":1,"age\"":2}`, 1}, // the real key binds, the impostor does not overwrite it
	} {
		var v small
		if err := vjson.Unmarshal([]byte(tc.input), &v); err != nil {
			t.Errorf("%s: unmarshal: %v", tc.input, err)
			continue
		}
		if v.Age != tc.wantAge {
			t.Errorf("%s: Age = %d, want %d", tc.input, v.Age, tc.wantAge)
		}
	}

	// A key set large enough to select a perfect-hash tier beyond WINDOW.
	type wide struct {
		F01 int64 `json:"field_001"`
		F02 int64 `json:"field_002"`
		F03 int64 `json:"field_003"`
		F04 int64 `json:"field_004"`
		F05 int64 `json:"field_005"`
		F06 int64 `json:"field_006"`
		F07 int64 `json:"field_007"`
		F08 int64 `json:"field_008"`
		F09 int64 `json:"field_009"`
		F10 int64 `json:"field_010"`
		F11 int64 `json:"field_011"`
		F12 int64 `json:"field_012"`
		F13 int64 `json:"field_013"`
		F14 int64 `json:"field_014"`
		F15 int64 `json:"field_015"`
		F16 int64 `json:"field_016"`
		F17 int64 `json:"field_017"`
		F18 int64 `json:"field_018"`
		F19 int64 `json:"field_019"`
		F20 int64 `json:"field_020"`
		F21 int64 `json:"field_021"`
		F22 int64 `json:"field_022"`
		F23 int64 `json:"field_023"`
		F24 int64 `json:"field_024"`
		F25 int64 `json:"field_025"`
	}
	for _, tc := range []struct {
		input      string
		wantField3 int64
	}{
		{`{"field_003":7}`, 7},
		{`{"\u0066ield_003":7}`, 7},
		{`{"field_003\"":7}`, 0},
		{`{"field_003\u0022":7}`, 0},
	} {
		var v wide
		if err := vjson.Unmarshal([]byte(tc.input), &v); err != nil {
			t.Errorf("%s: unmarshal: %v", tc.input, err)
			continue
		}
		if v.F03 != tc.wantField3 {
			t.Errorf("%s: F03 = %d, want %d", tc.input, v.F03, tc.wantField3)
		}
	}
}

// The reserve-unknown carrier classifies merged-tape entries in phase2 with
// the same lookup, so an escaped key must miss the declared field there too
// and land in the reserved Value instead.
func TestEscapedObjectKeyLookupReserveUnknown(t *testing.T) {
	type host struct {
		Name string      `json:"name"`
		Age  int64       `json:"age"`
		Rest value.Value `json:",embed"`
	}
	var v host
	if err := vjson.Unmarshal([]byte(`{"age\"":1}`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.Age != 0 {
		t.Errorf(`Age = %d, want 0: key "age\"" must not match field "age"`, v.Age)
	}
	got := v.Rest.Get(`age"`)
	if !got.Exists() {
		t.Fatal(`Rest is missing the reserved key "age\""`)
	}
	if n, ok := got.Int(); !ok || n != 1 {
		t.Errorf(`Rest["age\""] = %d, ok=%v; want 1, true`, n, ok)
	}
}
