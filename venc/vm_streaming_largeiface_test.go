package venc

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// TestStreamingBufFull_LargeStringIface reproduces the second deadlock class:
// a large interface value (e.g. a 60KB string) whose atomic reservation never
// fits the streaming workBuf.
//
// OP_INTERFACE speculatively writes the key, then delegates to
// encode_primitive_value. For OP_STRING the C encoder reserves
// 2 + len*6 bytes (worst-case escaping) and returns VJ_EXIT_BUF_FULL having
// written zero bytes when that won't fit (eface.c OP_STRING branch). Two
// problems follow:
//
//  1. vj_op_interface's VJ_IFACE_BUF_FULL case does NOT roll back the
//     speculatively-written key (unlike the YIELD / CACHE_MISS / NAN_INF
//     cases), so on re-entry the key is written again, duplicating it.
//
//  2. Because the key write makes written > 0, the streaming BUF_FULL path
//     only flushes and never grows; the string's atomic reservation
//     (360002 bytes here) is larger than any reasonable workBuf, so the loop
//     spins forever flushing just the key. The written==0 guard added for the
//     empty-write storm does not catch this, since written equals the key
//     length.
//
// The cap on writes bounds the run so the test fails fast instead of hanging.
func TestStreamingBufFull_LargeStringIface(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	type S struct {
		Data any `json:"data"`
	}
	big := strings.Repeat("x", 60000)
	v := S{Data: big}

	// Warm the ifaceCache so string values resolve to OP_STRING in
	// encode_primitive_value instead of the first-encode CACHE_MISS yield.
	if _, err := Marshal(v); err != nil {
		t.Fatalf("warmup Marshal: %v", err)
	}

	ti := EncTypeInfoOf(reflect.TypeFor[S]())

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.flags = uint32(escapeStringFlags)
	es.nativeIndent = true
	// Small workBuf: large enough for OP_INTERFACE's key+330 reservation but
	// far smaller than the 60KB string's atomic reservation (360002 bytes).
	es.buf = make([]byte, 0, 4096)

	var out []byte
	var writes int
	const maxWrites = 5000
	errStorm := errors.New("large-string deadlock: key flushed without progress")
	es.flushFn = func(p []byte) (int, error) {
		writes++
		if writes > maxWrites {
			return 0, errStorm
		}
		out = append(out, p...)
		return len(p), nil
	}

	err := es.encodeTop(ti, unsafe.Pointer(&v))
	out = append(out, es.buf...)
	es.buf = es.buf[:0]

	switch {
	case err == errStorm:
		t.Fatalf("deadlock: flushed the key %d times without completing the value", writes)
	case err != nil:
		t.Fatalf("encodeTop: %v", err)
	}

	if c := strings.Count(string(out), `"data"`); c != 1 {
		head := string(out)
		if len(head) > 80 {
			head = head[:80] + "..."
		}
		t.Errorf("key \"data\" emitted %d times, want 1 (output head=%q)", c, head)
	}

	want := `{"data":"` + big + `"}`
	if string(out) != want {
		got := string(out)
		if len(got) > 80 {
			got = got[:80] + "..."
		}
		t.Errorf("mismatch\n  got  len=%d head=%q\n  want len=%d", len(out), got, len(want))
	}
}
