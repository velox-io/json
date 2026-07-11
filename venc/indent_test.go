package venc

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

type customMarshalerVal struct{}

func (customMarshalerVal) MarshalJSON() ([]byte, error) { return []byte(`99`), nil }

type indentResetYielder struct {
	Name string
	Val  customMarshalerVal // forces yieldFallback at depth 1 every encode
}

type indentResetSimple struct {
	A int
	B int
}

// runIndentDepthReset verifies that the indent entry point (setup) resets
// es.indentDepth to 0 before encoding. A stale indentDepth would leak into
// the encode and emit an extra indent level. Tag-independent: the reset
// property must hold whether or not the native VM is available, so the
// stale state is poisoned directly instead of relying on a VM yield.
func runIndentDepthReset(t *testing.T, setup func(*encodeState)) {
	t.Helper()
	es := acquireEncodeState()
	defer releaseEncodeState(es)

	// Simulate stale indentDepth left by a prior encode.
	es.indentDepth = 5

	setup(es)
	if es.indentDepth != 0 {
		t.Fatalf("entry point did not reset indentDepth: got %d, want 0", es.indentDepth)
	}

	inner := indentResetSimple{A: 1, B: 2}
	ti := EncTypeInfoOf(reflect.TypeOf(indentResetSimple{}))
	if err := es.encodeTop(ti, unsafe.Pointer(&inner)); err != nil {
		t.Fatalf("encode: %v", err)
	}

	want := "\n\u00a0\"A\""
	if !strings.Contains(string(es.buf), want) {
		t.Fatalf("stale indentDepth leaked into output.\nwant field prefix %q\ngot: %s",
			want, string(es.buf))
	}
}

// TestIndentDepthReset_MarshalPath exercises the real withIndent option
// (MarshalIndent path). withIndent must reset es.indentDepth = 0.
func TestIndentDepthReset_MarshalPath(t *testing.T) {
	runIndentDepthReset(t, withIndent("", "\u00a0"))
}

// TestIndentDepthReset_EncoderPath mirrors Encoder.encodePtr's indent setup
// (encoder.go:163-169). encodePtr must reset es.indentDepth = 0 in its indent
// branch. NOTE: setup is a hand mirror; keep it in sync with encodePtr.
func TestIndentDepthReset_EncoderPath(t *testing.T) {
	encoderSetup := func(prefix, indent string) func(*encodeState) {
		return func(es *encodeState) {
			es.indentString = indent
			if indent != "" {
				es.indentDepth = 0
				es.indentPrefix = prefix
				es.useNativeVM = encvm.Available && isSimpleIndent(prefix, indent)
			}
		}
	}
	runIndentDepthReset(t, encoderSetup("", "\u00a0"))
}

// TestIndentYieldLeavesStaleDepth guards the VM yield path: a yieldFallback at
// depth > 0 must leave es.indentDepth stale (synced from ctx.IndentDepth in
// vm_exec.go) so the next encode's entry point has something to reset. This
// stale-state leakage is a VM-yield phenomenon; under vj_noencvm there is no
// yield, so the scenario cannot arise.
func TestIndentYieldLeavesStaleDepth(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	es := acquireEncodeState()
	defer releaseEncodeState(es)

	withIndent("", "  ")(es)
	outer := indentResetYielder{Name: "x", Val: customMarshalerVal{}}
	ti := EncTypeInfoOf(reflect.TypeOf(indentResetYielder{}))
	if err := es.encodeTop(ti, unsafe.Pointer(&outer)); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if es.indentDepth == 0 {
		t.Fatalf("yield did not leave stale indentDepth; got 0, want > 0")
	}
	t.Logf("after yield: es.indentDepth=%d", es.indentDepth)
}

// TestCompactModeUsesNativeVM guards compact (no-indent) routing: exec() sends
// compact encodes to the native VM iff es.useNativeVM is true. compact
// Marshal/Encoder (indentString=="") never goes through withIndent or
// encodePtr's indent branch, so es.useNativeVM must default to encvm.Available
// in acquireEncodeState; otherwise compact struct/slice/map encodes silently
// fall back to the interpreter.
func TestCompactModeUsesNativeVM(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	if !es.useNativeVM {
		t.Errorf("compact mode broken: encvm.Available=true but es.useNativeVM=false after acquire; compact encodes will use interp instead of native VM")
	}
}
