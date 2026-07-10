package venc

import (
	"errors"
	"reflect"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// TestStreamingBufFull_AnyValueDeadlock reproduces the production CPU-100%
// infinite loop in the streaming Encoder path.
//
// A pooled encodeState whose buf cap was nibbled below ~331 bytes (Marshal
// reuses the tail via es.buf = es.buf[n:], shrinking cap) is reused for a
// streaming Encode of a value containing an `any` field. OP_INTERFACE
// speculatively VM_CHECKs 330+ bytes up front regardless of the real value
// size; with a smaller workBuf it returns BUF_FULL having written 0 bytes.
// The streaming BUF_FULL handler only flushed (0 bytes) and never grew, so
// execVMLoop spun forever emitting empty Write calls.
//
// The grow-on-zero-progress guard in execVMLoop breaks the deadlock: with it,
// the buffer grows until the VM's next atomic write fits and encoding
// completes in a handful of writes. Without it this test hangs and is failed
// by the per-encode write cap below.
func TestStreamingBufFull_AnyValueDeadlock(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	v := map[string]any{
		"status":      "ok",
		"service":     "dboss-platform",
		"instance_id": "dboss-dbossserver-0",
	}
	// velox iterates map keys in Swiss-map order (JSON object key order is
	// unspecified), so compare against velox's own Marshal, not stdlib.
	want, _ := Marshal(v)

	ti := EncTypeInfoOf(reflect.TypeFor[map[string]any]())

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.flags = uint32(escapeStringFlags)
	es.nativeIndent = true
	// Simulate a nibbled pooled buffer: cap below OP_INTERFACE's ~331-byte
	// reservation. The victim's size hint (~304) is smaller than this, so
	// growBuf would not restore it, leaving the streaming Encoder stuck.
	es.buf = make([]byte, 0, 64)

	var out []byte
	var writes int
	const maxWrites = 1024
	errStorm := errors.New("BUF_FULL storm: deadlock not broken")
	es.flushFn = func(p []byte) (int, error) {
		writes++
		if writes > maxWrites {
			return 0, errStorm
		}
		out = append(out, p...)
		return len(p), nil
	}

	if err := es.encodeTop(ti, unsafe.Pointer(&v)); err != nil {
		t.Fatalf("encodeTop failed after %d writes: %v", writes, err)
	}
	out = append(out, es.buf...)
	es.buf = es.buf[:0]

	if writes > maxWrites {
		t.Fatalf("BUF_FULL storm: %d writes, deadlock not broken", writes)
	}
	if string(out) != string(want) {
		t.Errorf("mismatch\n  got:  %s\n  want: %s", out, want)
	}
}
