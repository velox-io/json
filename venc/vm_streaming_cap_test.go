package venc

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// TestStreamingBuffer_CapBackAfterOversized confirms that es.streamBuf is
// bounded across encodes: an encode whose atomic write grows the buffer past
// streamBufCapMax (here a ~60KB string reserving 360KB+ under worst-case
// escaping) is tolerated for the in-flight encode, but the next acquire caps
// it back to encBufInitSize so a single oversized value cannot ratchet the
// pooled buffer toward unbounded memory.
func TestStreamingBuffer_CapBackAfterOversized(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	const bufFloor = encBufInitSize // 32 KiB
	const bufMax = streamBufCapMax  // 128 KiB

	type S struct {
		Data any `json:"data"`
	}
	big := strings.Repeat("x", 60000)
	v := S{Data: big}
	// Warm the ifaceCache so the string resolves to OP_STRING inline.
	if _, err := Marshal(v); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	ti := EncTypeInfoOf(reflect.TypeFor[S]())

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.flags = uint32(escapeStringFlags)
	es.nativeIndent = true

	// Drive the streaming path (swap onto streamBuf) with a large string,
	// which grows es.buf (== streamBuf mid-encode) well past bufMax.
	var writes int
	const maxWrites = 5000
	es.flushFn = func(p []byte) (int, error) {
		writes++
		if writes > maxWrites {
			return 0, errStr("overflow")
		}
		return len(p), nil
	}
	marshalBuf := es.buf
	es.buf = es.acquireStreamBuf()
	if err := es.encodeTop(ti, unsafe.Pointer(&v)); err != nil {
		t.Fatalf("oversized encodeTop: %v", err)
	}
	es.streamBuf = es.buf
	es.buf = marshalBuf

	if cap(es.streamBuf) <= bufMax {
		t.Fatalf("post-oversize cap=%d, want grown past %d", cap(es.streamBuf), bufMax)
	}

	// Verify the bound on the same object (sync.Pool does not guarantee that
	// acquireEncodeState returns the object we just released, so we cannot
	// rely on a release/re-acquire round-trip). releaseEncodeState caps
	// es.streamBuf back to the floor when it exceeds streamBufCapMax.
	releaseEncodeState(es)
	switch c := cap(es.streamBuf); {
	case c > bufMax:
		t.Fatalf("after release streamBuf cap=%d, want <= %d (not capped back)", c, bufMax)
	case c < bufFloor:
		t.Fatalf("after release streamBuf cap=%d, want >= %d (floor)", c, bufFloor)
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
