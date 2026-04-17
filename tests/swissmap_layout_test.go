package tests

import (
	"encoding/json"
	"fmt"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/gort"
)

// TestSwissMapLayoutFlags prints the runtime layout detection results.
// All should be true for both default and mapsplitgroup modes.
func TestSwissMapLayoutFlags(t *testing.T) {
	t.Logf("SwissMapLayoutOK        = %v", gort.SwissMapLayoutOK)
	t.Logf("SwissMapStrIntLayoutOK  = %v", gort.SwissMapStrIntLayoutOK)
	t.Logf("SwissMapStrInt64LayoutOK= %v", gort.SwissMapStrInt64LayoutOK)
	t.Logf("SwissMapSplitGroup      = %v", gort.SwissMapSplitGroup)
}

// TestSwissMapSplitGroupCorrectness verifies that map marshal produces
// correct JSON regardless of which code path is taken (native or fallback).
func TestSwissMapSplitGroupCorrectness(t *testing.T) {
	t.Logf("SwissMapLayoutOK = %v (false means Go fallback path)", gort.SwissMapLayoutOK)

	t.Run("map[string]string", func(t *testing.T) {
		m := map[string]string{
			"alpha": "one",
			"beta":  "two",
			"gamma": "three",
			"delta": "four",
		}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rt map[string]string
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		for k, v := range m {
			if rt[k] != v {
				t.Errorf("key %q: want %q, got %q", k, v, rt[k])
			}
		}
		if len(rt) != len(m) {
			t.Errorf("length mismatch: want %d, got %d", len(m), len(rt))
		}
	})

	t.Run("map[string]int", func(t *testing.T) {
		m := map[string]int{
			"x": 100, "y": 200, "z": -300,
		}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rt map[string]int
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		for k, v := range m {
			if rt[k] != v {
				t.Errorf("key %q: want %d, got %d", k, v, rt[k])
			}
		}
	})

	t.Run("map[string]int64", func(t *testing.T) {
		m := map[string]int64{
			"big": 1 << 50, "neg": -9999999,
		}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rt map[string]int64
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		for k, v := range m {
			if rt[k] != v {
				t.Errorf("key %q: want %d, got %d", k, v, rt[k])
			}
		}
	})

	t.Run("map[string]any", func(t *testing.T) {
		m := map[string]any{
			"str":    "hello",
			"num":    float64(42),
			"bool":   true,
			"null":   nil,
			"nested": map[string]any{"inner": "value"},
		}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rt map[string]any
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		if fmt.Sprint(rt) != fmt.Sprint(m) {
			// Deep compare via re-marshal with std lib
			want, _ := json.Marshal(m)
			got2, _ := json.Marshal(rt)
			if string(want) != string(got2) {
				t.Errorf("map[string]any mismatch:\n  want: %s\n  got:  %s", want, got2)
			}
		}
	})

	t.Run("struct_with_map_field", func(t *testing.T) {
		type S struct {
			Name string            `json:"name"`
			Tags map[string]string `json:"tags"`
			Nums map[string]int    `json:"nums"`
		}
		v := S{
			Name: "test",
			Tags: map[string]string{"env": "prod", "region": "us"},
			Nums: map[string]int{"a": 1, "b": 2, "c": 3},
		}
		got, err := vjson.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var rt S
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		if rt.Name != v.Name {
			t.Errorf("Name: want %q, got %q", v.Name, rt.Name)
		}
		for k, want := range v.Tags {
			if rt.Tags[k] != want {
				t.Errorf("Tags[%q]: want %q, got %q", k, want, rt.Tags[k])
			}
		}
		for k, want := range v.Nums {
			if rt.Nums[k] != want {
				t.Errorf("Nums[%q]: want %d, got %d", k, want, rt.Nums[k])
			}
		}
	})

	// Large map: forces multi-group (directory) layout
	t.Run("large_map", func(t *testing.T) {
		m := make(map[string]string, 200)
		for i := range 200 {
			m[fmt.Sprintf("key_%03d", i)] = fmt.Sprintf("val_%03d", i)
		}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rt map[string]string
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		if len(rt) != 200 {
			t.Errorf("large map length: want 200, got %d", len(rt))
		}
		for k, v := range m {
			if rt[k] != v {
				t.Errorf("key %q: want %q, got %q", k, v, rt[k])
			}
		}
	})

	// map[int32]struct{}: non-string key, zero-size value.
	// mapsplitgroup changes GroupSize from 72 to 48 for this type.
	t.Run("map[int32]struct{}", func(t *testing.T) {
		m := map[int32]struct{}{1: {}, -2: {}, 100: {}, 0: {}, 2147483647: {}}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		// std lib round-trip
		want, _ := json.Marshal(m)
		var rtGot map[int32]struct{}
		if err := json.Unmarshal(got, &rtGot); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		var rtWant map[int32]struct{}
		_ = json.Unmarshal(want, &rtWant)
		if len(rtGot) != len(m) {
			t.Errorf("length mismatch: want %d, got %d", len(m), len(rtGot))
		}
		for k := range m {
			if _, ok := rtGot[k]; !ok {
				t.Errorf("missing key %d", k)
			}
		}
	})

	// map[int64]struct{}: non-string key, zero-size value.
	// mapsplitgroup changes GroupSize from 136 to 80.
	t.Run("map[int64]struct{}", func(t *testing.T) {
		m := map[int64]struct{}{-999: {}, 42: {}, 1 << 40: {}, 0: {}}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rtGot map[int64]struct{}
		if err := json.Unmarshal(got, &rtGot); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		if len(rtGot) != len(m) {
			t.Errorf("length mismatch: want %d, got %d", len(m), len(rtGot))
		}
		for k := range m {
			if _, ok := rtGot[k]; !ok {
				t.Errorf("missing key %d", k)
			}
		}
	})

	// Large map[int32]struct{}: forces multi-group layout with zero-size values.
	t.Run("large_map[int32]struct{}", func(t *testing.T) {
		m := make(map[int32]struct{}, 200)
		for i := range 200 {
			m[int32(i)] = struct{}{}
		}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rtGot map[int32]struct{}
		if err := json.Unmarshal(got, &rtGot); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		if len(rtGot) != 200 {
			t.Errorf("length mismatch: want 200, got %d", len(rtGot))
		}
		for k := range m {
			if _, ok := rtGot[k]; !ok {
				t.Errorf("missing key %d", k)
			}
		}
	})

	// map[string]struct{}: string key, zero-size value (ElemStride=0 in split mode).
	// This type is a key optimization target of mapsplitgroup (GroupSize 200 -> 144).
	t.Run("map[string]struct{}", func(t *testing.T) {
		m := map[string]struct{}{"a": {}, "bb": {}, "ccc": {}, "": {}}
		got, err := vjson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var rtGot map[string]struct{}
		if err := json.Unmarshal(got, &rtGot); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v\nJSON: %s", err, got)
		}
		if len(rtGot) != len(m) {
			t.Errorf("length mismatch: want %d, got %d", len(m), len(rtGot))
		}
		for k := range m {
			if _, ok := rtGot[k]; !ok {
				t.Errorf("missing key %q", k)
			}
		}
	})
}
