package vjson

import (
	stdjson "encoding/json"
	"testing"
	"time"
)

// TestIfaceSwitchOps_StructWithFallback is a regression test for the VM_SAVE_AND_RETURN ops_ptr bug.
//
// Trigger: struct with `any` field whose runtime value is a struct containing
// a FALLBACK field (e.g. time.Time → json.Marshaler). The C VM enters the
// cached interface Blueprint via SWITCH_OPS and may yield inside it.
// Before the fix, the saved PC was relative to the cached ops but ops_ptr
// still pointed to the parent Blueprint, causing a mismatch on resume.
func TestIfaceSwitchOps_StructWithFallback(t *testing.T) {
	type Inner struct {
		T time.Time `json:"t"`
	}

	type Outer struct {
		A string `json:"a"`
		V any    `json:"v"`
	}

	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	v := Outer{
		A: "hello",
		V: Inner{T: now},
	}

	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	want, _ := stdjson.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("output mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestIfaceSwitchOps_StructWithStringAndFallback tests the variant where
// the parent struct has multiple STRING fields before the `any` field.
// Before the fix, with the right PC alignment this caused infinite BUF_FULL
// (the mismatched PC landed on a STRING instruction with corrupted base).
func TestIfaceSwitchOps_StructWithStringAndFallback(t *testing.T) {
	type Inner struct {
		X string    `json:"x"`
		T time.Time `json:"t"`
	}

	type Outer struct {
		A string `json:"a"`
		B string `json:"b"`
		V any    `json:"v"`
	}

	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	v := Outer{
		A: "hello",
		B: "world",
		V: Inner{X: "test", T: now},
	}

	done := make(chan struct{})
	var got []byte
	var err error
	go func() {
		got, err = Marshal(v)
		close(done)
	}()

	select {
	case <-done:
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		want, _ := stdjson.Marshal(&v)
		if string(got) != string(want) {
			t.Errorf("output mismatch\n got: %s\nwant: %s", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HANG: timed out after 3s (regression: ops_ptr/PC mismatch)")
	}
}
