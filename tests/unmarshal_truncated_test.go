package tests

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	vjson "github.com/velox-io/json"
)

// allocAtPageEnd places data at the very end of a page, followed immediately
// by an unmapped guard page. Any out-of-bounds read past data will SIGSEGV.
// Returns the guarded slice and a cleanup function.
func allocAtPageEnd(data []byte) (guarded []byte, cleanup func()) {
	pageSize := syscall.Getpagesize()
	// Allocate 2 pages: one for data, one guard page.
	size := 2 * pageSize
	mem, err := syscall.Mmap(-1, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		panic("mmap failed: " + err.Error())
	}
	// Make the second page inaccessible (guard page).
	if err := syscall.Mprotect(mem[pageSize:], syscall.PROT_NONE); err != nil {
		syscall.Munmap(mem)
		panic("mprotect failed: " + err.Error())
	}
	// Place data right-aligned to the end of the first page.
	n := len(data)
	offset := pageSize - n
	copy(mem[offset:pageSize], data)
	guarded = mem[offset:pageSize:pageSize]

	cleanup = func() { syscall.Munmap(mem) }
	return
}

// guardedUnmarshal places JSON data on a guard page and unmarshals into v.
// Any out-of-bounds read past the data will SIGSEGV.
func guardedUnmarshal[T any](t *testing.T, jsonStr string, v *T) error {
	t.Helper()
	data := []byte(jsonStr)
	guarded, cleanup := allocAtPageEnd(data)
	defer cleanup()
	_ = unsafe.Pointer(&guarded[0]) // keep alive
	return vjson.Unmarshal(guarded, v)
}

// TestTruncatedValueAfterColon_GuardPage uses mmap guard pages to detect
// the out-of-bounds read in the inline struct hot-path (vj_parser.go) where
// sliceAt(src, idx) is called directly after skipWS without a bounds check.
//
// This is the only call site where the caller itself does a raw sliceAt
// rather than delegating to a callee (scanValue, scanString, etc.) that
// already validates idx at its entry. The guard page turns the silent
// memory-safety bug into a hard SIGSEGV.
func TestTruncatedValueAfterColon_GuardPage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mmap guard-page test not supported on Windows")
	}

	type Simple struct {
		Name  string  `json:"name"`
		Value int     `json:"value"`
		Score float64 `json:"score"`
	}

	// Ensure type metadata is pre-compiled before the critical test.
	var warmup Simple
	_ = vjson.Unmarshal([]byte(`{"name":"x","value":1,"score":1.0}`), &warmup)

	truncatedInputs := []struct {
		label string
		json  string
	}{
		{"string field, no ws", `{"name":`},
		{"string field, trailing space", `{"name": `},
		{"int field, no ws", `{"value":`},
		{"int field, trailing space", `{"value": `},
		{"float field, no ws", `{"score":`},
		{"second field truncated", `{"name":"hello","value":`},
		{"second field truncated ws", `{"name":"hello","value": `},
	}

	for _, tc := range truncatedInputs {
		t.Run(tc.label, func(t *testing.T) {
			var s Simple
			err := guardedUnmarshal(t, tc.json, &s)
			if err == nil {
				t.Fatalf("expected error for truncated input %q, got nil (s=%+v)", tc.json, s)
			}
		})
	}
}

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
