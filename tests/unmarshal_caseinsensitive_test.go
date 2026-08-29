//go:build vdec || vj_nondec || !((darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64))

package tests

import (
	"testing"

	vjson "github.com/velox-io/json"
)

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
