package tests

import (
	"testing"

	vjson "github.com/velox-io/json"
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

	// Second unmarshal — if the parser reuses the same arena block, it will
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

// TestCaseInsensitive_UpperKeyLowerTag tests case-insensitive field matching
// when the JSON key has uppercase letters but all struct tags are lowercase.
func TestCaseInsensitive_UpperKeyLowerTag(t *testing.T) {
	type Foo struct {
		Name  string         `json:"name"`
		Value map[int]string `json:"value"`
	}

	tests := []struct {
		name     string
		input    string
		wantName string
		wantVal  map[int]string
	}{
		{
			name:     "uppercase first letter",
			input:    `{"Name":"edf","value":{"1":"v"}}`,
			wantName: "edf",
			wantVal:  map[int]string{1: "v"},
		},
		{
			name:     "all uppercase",
			input:    `{"NAME":"edf","VALUE":{"2":"w"}}`,
			wantName: "edf",
			wantVal:  map[int]string{2: "w"},
		},
		{
			name:     "mixed case",
			input:    `{"nAmE":"edf","VaLuE":{"3":"x"}}`,
			wantName: "edf",
			wantVal:  map[int]string{3: "x"},
		},
		{
			name:     "exact match still works",
			input:    `{"name":"edf","value":{"4":"y"}}`,
			wantName: "edf",
			wantVal:  map[int]string{4: "y"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Foo
			err := vjson.Unmarshal([]byte(tt.input), &got)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name != tt.wantName {
				t.Errorf("Name: got %q, want %q", got.Name, tt.wantName)
			}
			if len(got.Value) != len(tt.wantVal) {
				t.Errorf("Value length: got %d, want %d", len(got.Value), len(tt.wantVal))
			}
			for k, v := range tt.wantVal {
				if got.Value[k] != v {
					t.Errorf("Value[%d]: got %q, want %q", k, got.Value[k], v)
				}
			}
		})
	}
}

// TestCaseInsensitive_PreExistingMapValues verifies that unmarshal into a
// struct with pre-existing map values merges correctly (matching encoding/json
// behavior) when case-insensitive field matching is needed.
func TestCaseInsensitive_PreExistingMapValues(t *testing.T) {
	type Foo struct {
		Name  string         `json:"name"`
		Value map[int]string `json:"value"`
	}

	foo := &Foo{
		Name:  "abc",
		Value: map[int]string{0: "existing"},
	}
	input := `{"Name":"edf", "value": {"123": "v"}}`
	err := vjson.Unmarshal([]byte(input), foo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if foo.Name != "edf" {
		t.Errorf("Name: got %q, want %q", foo.Name, "edf")
	}
	if foo.Value[0] != "existing" {
		t.Errorf("Value[0]: got %q, want %q", foo.Value[0], "existing")
	}
	if foo.Value[123] != "v" {
		t.Errorf("Value[123]: got %q, want %q", foo.Value[123], "v")
	}
}

// TestCaseInsensitive_NonBitmapPath verifies case-insensitive matching on the
// non-bitmap path (>8 fields), which goes through LookupFieldBytes directly.
func TestCaseInsensitive_NonBitmapPath(t *testing.T) {
	// >8 fields forces the non-bitmap lookup path
	type Big struct {
		F1 string `json:"f1"`
		F2 string `json:"f2"`
		F3 string `json:"f3"`
		F4 string `json:"f4"`
		F5 string `json:"f5"`
		F6 string `json:"f6"`
		F7 string `json:"f7"`
		F8 string `json:"f8"`
		F9 string `json:"f9"`
	}

	tests := []struct {
		name   string
		input  string
		wantF1 string
		wantF9 string
	}{
		{
			name:   "uppercase key",
			input:  `{"F1":"a","F9":"b"}`,
			wantF1: "a",
			wantF9: "b",
		},
		{
			name:   "mixed case key",
			input:  `{"f1":"a","F9":"b"}`,
			wantF1: "a",
			wantF9: "b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Big
			err := vjson.Unmarshal([]byte(tt.input), &got)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.F1 != tt.wantF1 {
				t.Errorf("F1: got %q, want %q", got.F1, tt.wantF1)
			}
			if got.F9 != tt.wantF9 {
				t.Errorf("F9: got %q, want %q", got.F9, tt.wantF9)
			}
		})
	}
}
