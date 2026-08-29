package venc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// Regression tests for VM resume safety in indent mode.
//
// Background: when the write window fills mid-opcode, the VM exits and Go
// re-enters it at the SAME opcode, re-executing it from the start. Any state
// mutation performed before the op's space check is therefore applied again
// on resume. The container-close paths (OBJ_CLOSE, SLICE_END, the map/swiss
// done states) used to decrement indent_depth BEFORE their check; every
// close that was interrupted by a window-full exit lost one depth level.
// With enough output the depth went negative, VM_WRITE_INDENT computed a
// negative byte count, and its memcpy crashed on the wrapped size_t
// (repro: benchmark MarshalIndent workload on the deep jsonbench
// GolangSource corpus once earlier datasets had shaped the pooled buffer).
// The close ops now run their check before the decrement (VM_CHECK_CLOSE),
// and SLICE_END commits iter_idx only after its check passes (an early
// commit would skip an element on resume).
//
// The window-size sweep below is the deterministic net: starting the encode
// with a k-byte window makes the first window-full exit land at byte k, so
// sweeping k walks the interruption point through every op boundary of the
// output, including the close ops' checks.

type resumeNode struct {
	Name string       `json:"name"`
	Kids []resumeNode `json:"kids,omitempty"`
	Tags []string     `json:"tags,omitempty"`
}

func buildResumeTree(depth, breadth int) resumeNode {
	if depth == 0 {
		return resumeNode{Name: strings.Repeat("leaf-", 4) + "x"}
	}
	kids := make([]resumeNode, breadth)
	for i := range kids {
		kids[i] = buildResumeTree(depth-1, breadth)
	}
	return resumeNode{Name: fmt.Sprintf("level-%d-node", depth), Kids: kids, Tags: []string{"a", "bb", "ccc"}}
}

// sweepIndentWindow encodes v with every starting window size from 8 bytes
// up to len(want)+slack and requires byte-identical output each time.
func sweepIndentWindow(t *testing.T, v any, ptr unsafe.Pointer, want string) {
	t.Helper()
	ti := EncTypeInfoOf(reflect.TypeOf(v))
	for k := 8; k <= len(want)+64; k++ {
		es := acquireEncodeState()
		withIndent("", "  ")(es)
		es.flags = uint32(escapeStdCompat)
		es.buf = make([]byte, 0, k)
		err := es.encodeTop(ti, ptr)
		got := string(es.buf)
		releaseEncodeState(es)
		if err != nil {
			t.Fatalf("window=%d: encode: %v", k, err)
		}
		if got != want {
			t.Fatalf("window=%d: output mismatch:\n got:  %q\n want: %q", k, got, want)
		}
	}
}

func TestVMResumeIndent_WindowSweep(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}
	v := buildResumeTree(4, 3)
	want, err := json.MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}
	sweepIndentWindow(t, v, unsafe.Pointer(&v), string(want))
}

// TestVMResumeIndent_MapCloseSweep covers the map/swiss done states. A
// single-key map keeps the output deterministic (Go map iteration order is
// randomized for multi-key maps), and the nested value keeps the map close
// far enough into the output to be crossed by the sweep.
func TestVMResumeIndent_MapCloseSweep(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}
	type doc struct {
		ID   int            `json:"id"`
		Data map[string]int `json:"data"`
		Tail []string       `json:"tail"`
	}
	v := doc{
		ID:   7,
		Data: map[string]int{"the-only-key": 42},
		Tail: []string{"x", "yy", "zzz"},
	}
	want, err := json.MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}
	sweepIndentWindow(t, v, unsafe.Pointer(&v), string(want))
}

// TestMarshalIndent_FallbackFieldPaths pins the everyday fallback paths under
// indent: nested structs, interface fields and Marshaler fields must render
// with full stdlib-compatible indentation. The Go-side fallback key protocol
// (interp opFallback) used to drop the newline+indent decoration entirely.
type indentCheckInner struct {
	Y int `json:"y"`
}

type indentCheckDoc struct {
	A int                  `json:"a"`
	B string               `json:"b"`
	C indentCheckInner     `json:"c"`
	D any                  `json:"d"`
	E indentCheckMarshaler `json:"e"`
	F []indentCheckInner   `json:"f"`
}

type indentCheckMarshaler struct {
	Ref string
}

func (m indentCheckMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"` + m.Ref + `"`), nil }

func TestMarshalIndent_FallbackFieldPaths(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}
	v := indentCheckDoc{
		A: 1,
		B: "x",
		C: indentCheckInner{Y: 2},
		D: map[string]any{"k": []any{1, 2, map[string]int{"z": 3}}},
		E: indentCheckMarshaler{Ref: "2026-08-20T12:00:00Z"},
		F: []indentCheckInner{{Y: 10}, {Y: 20}},
	}
	got, err := MarshalIndent(&v, "", "  ", WithStdCompat())
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	want, err := json.MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("fallback field indent mismatch:\n got:  %q\n want: %q", got, want)
	}
}
