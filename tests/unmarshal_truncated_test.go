package tests

import (
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// TestTruncatedValueAfterColon verifies that the parser returns an error
// (instead of reading out-of-bounds memory) when the JSON input is truncated
// immediately after the colon of a known struct field.
//
// Regression: vj_parser.go inline struct hot-path calls sliceAt(src, idx)
// without first checking idx >= len(src), causing an unsafe OOB read when
// the input ends right after "key": (with no value following).
func TestTruncatedValueAfterColon(t *testing.T) {
	type Simple struct {
		Name  string  `json:"name"`
		Value int     `json:"value"`
		Score float64 `json:"score"`
	}

	truncatedInputs := []struct {
		label string
		json  string
	}{
		{"string field, no ws", `{"name":`},
		{"string field, trailing space", `{"name": `},
		{"string field, trailing spaces", `{"name":   `},
		{"int field, no ws", `{"value":`},
		{"int field, trailing space", `{"value": `},
		{"float field, no ws", `{"score":`},
		{"float field, trailing tab", `{"score":` + "\t"},
		{"second field truncated", `{"name":"hello","value":`},
		{"second field truncated ws", `{"name":"hello","value": `},
	}

	for _, tc := range truncatedInputs {
		t.Run(tc.label, func(t *testing.T) {
			var s Simple
			err := vjson.Unmarshal([]byte(tc.json), &s)
			if err == nil {
				t.Fatalf("expected error for truncated input %q, got nil (s=%+v)", tc.json, s)
			}
			errMsg := err.Error()
			if !strings.Contains(errMsg, "EOF") &&
				!strings.Contains(errMsg, "unexpected end") &&
				!strings.Contains(errMsg, "syntax error") {
				t.Logf("got error (acceptable): %v", err)
			}
		})
	}
}

// TestTruncatedValueAfterColon_UnknownField ensures the unknown-field path
// (fi == nil → skipValue) also handles truncation correctly.
func TestTruncatedValueAfterColon_UnknownField(t *testing.T) {
	type OnlyName struct {
		Name string `json:"name"`
	}

	inputs := []struct {
		label string
		json  string
	}{
		{"unknown field truncated", `{"unknown":`},
		{"unknown field truncated ws", `{"unknown": `},
	}

	for _, tc := range inputs {
		t.Run(tc.label, func(t *testing.T) {
			var s OnlyName
			err := vjson.Unmarshal([]byte(tc.json), &s)
			if err == nil {
				t.Fatalf("expected error for truncated input %q, got nil", tc.json)
			}
		})
	}
}

// TestTruncatedMapValueAfterColon verifies truncation handling in map paths.
// These call sites delegate to scanString/scanValue/scanValueAny which have
// their own entry bounds checks — these tests verify the callee contract.
func TestTruncatedMapValueAfterColon(t *testing.T) {
	t.Run("map[string]string", func(t *testing.T) {
		type M struct {
			Tags map[string]string `json:"tags"`
		}
		cases := []string{
			`{"tags":{"a":`,
			`{"tags":{"a": `,
			`{"tags":{"a":"b","c":`,
		}
		for _, input := range cases {
			var m M
			err := vjson.Unmarshal([]byte(input), &m)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", input)
			}
		}
	})

	t.Run("map[string]any", func(t *testing.T) {
		type M struct {
			Data map[string]any `json:"data"`
		}
		cases := []string{
			`{"data":{"x":`,
			`{"data":{"x": `,
			`{"data":{"x":1,"y":`,
		}
		for _, input := range cases {
			var m M
			err := vjson.Unmarshal([]byte(input), &m)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", input)
			}
		}
	})

	t.Run("map[string]int", func(t *testing.T) {
		type M struct {
			Counts map[string]int `json:"counts"`
		}
		cases := []string{
			`{"counts":{"k":`,
			`{"counts":{"k": `,
			`{"counts":{"k":1,"j":`,
		}
		for _, input := range cases {
			var m M
			err := vjson.Unmarshal([]byte(input), &m)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", input)
			}
		}
	})
}
