//go:build !windows

package tests

import (
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
