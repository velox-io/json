package venc

import (
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// TestByteSliceBufFull_KeyDuplication reproduces a bug where the native VM
// wrote the JSON key for a []byte field, then hit BUF_FULL during base64
// encoding, causing the key to be emitted twice: "data":,"data":"...".
//
// Root cause: VM_CHECK for OP_BYTE_SLICE only reserves space for the key,
// not for the base64 payload. After VM_WRITE_KEY succeeds the subsequent
// vj_encode_base64 may find insufficient space and return NULL, triggering
// VJ_EXIT_BUF_FULL. On resume the VM re-executes the same opcode, writing
// the key a second time.
func TestByteSliceBufFull_KeyDuplication(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	type S struct {
		Pad  string `json:"p"`
		Data []byte `json:"data"`
	}

	// base64(10 bytes) = ceil(10/3)*4 = 16 chars, plus quotes = 18 bytes
	// We pick a buffer just big enough for the key portion but not the
	// base64 payload, to trigger BUF_FULL between VM_WRITE_KEY and base64.
	data := make([]byte, 10)
	for i := range data {
		data[i] = 0xf9
	}

	ti := EncTypeInfoOf(reflect.TypeFor[S]())

	// Try various tight buffer sizes to hit the boundary.
	// Output: {"p":"...","data":"<18 chars>"} ~ padLen + 25 + base64
	for padLen := 0; padLen <= 30; padLen++ {
		v := S{
			Pad:  string(make([]byte, padLen)),
			Data: data,
		}

		want, _ := json.Marshal(v)
		wantLen := len(want)

		// Walk buffer capacities from just-too-small up to exact fit.
		// The bug triggers when cap is enough for key but not base64.
		lo := wantLen / 2
		if lo < 10 {
			lo = 10
		}
		for cap := lo; cap <= wantLen+4; cap++ {
			es := acquireEncodeState()
			WithBufSize(cap)(es)
			es.buf = make([]byte, 0, cap)

			got, err := es.marshalWith(ti, unsafe.Pointer(&v))
			releaseEncodeState(es)

			if err != nil {
				t.Fatalf("padLen=%d cap=%d: error: %v", padLen, cap, err)
			}

			if string(got) != string(want) {
				t.Errorf("padLen=%d cap=%d: mismatch\n  got:  %s\n  want: %s", padLen, cap, got, want)
			}
		}
	}
}
