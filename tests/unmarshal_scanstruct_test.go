package tests

import (
	"testing"

	vjson "github.com/velox-io/json"
)

// TestScanStructViaScanValueSpecial verifies that struct fields tagged with
// `omitempty` now take the inlined fast path (not scanStruct) during unmarshal.
// Previously, omitempty set tiFlagOmitEmpty which made Flags != 0, causing
// scanValue to dispatch through scanValueSpecial → scanStruct (slower).
// After splitting Flags into Flags (marshal) and UFlags (unmarshal), omitempty
// no longer affects the unmarshal dispatch and the inlined path is used.
func TestScanStructViaScanValueSpecial(t *testing.T) {
	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner Inner  `json:"inner,omitempty"`
	}

	input := []byte(`{"name":"hello","inner":{"x":42,"y":"world"}}`)
	var out Outer
	if err := vjson.Unmarshal(input, &out); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if out.Name != "hello" {
		t.Errorf("Name = %q, want %q", out.Name, "hello")
	}
	if out.Inner.X != 42 {
		t.Errorf("Inner.X = %d, want 42", out.Inner.X)
	}
	if out.Inner.Y != "world" {
		t.Errorf("Inner.Y = %q, want %q", out.Inner.Y, "world")
	}

	// Also test empty object — hits the '}' early-return in scanStruct.
	input2 := []byte(`{"name":"test","inner":{}}`)
	var out2 Outer
	if err := vjson.Unmarshal(input2, &out2); err != nil {
		t.Fatalf("Unmarshal empty inner failed: %v", err)
	}
	if out2.Name != "test" {
		t.Errorf("Name = %q, want %q", out2.Name, "test")
	}
	if out2.Inner.X != 0 || out2.Inner.Y != "" {
		t.Errorf("Inner should be zero value, got %+v", out2.Inner)
	}
}
