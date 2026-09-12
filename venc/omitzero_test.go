package venc

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// The opcode number, yield reason, and ZCT tags are an ABI contract with the
// native VM (native/encvm/impl/types.h): a mismatch dispatches out of bounds
// or yields with an unknown reason.
func TestOmitZeroOpcodeABI(t *testing.T) {
	if opSkipIfZeroGo != 50 {
		t.Fatalf("OP_SKIP_IF_ZERO_GO = %d, want 50", opSkipIfZeroGo)
	}
	if yieldOmitZero != 4 {
		t.Fatalf("VJ_YIELD_OMIT_ZERO = %d, want 4", yieldOmitZero)
	}
	if !isLongOp(opSkipIfZeroGo) {
		t.Fatal("OP_SKIP_IF_ZERO_GO must be a long (16-byte) op")
	}
	if zctOZSlice != 28 || zctOZMap != 29 || zctOZRaw != 30 {
		t.Fatalf("oz tags = %d/%d/%d, want 28/29/30", zctOZSlice, zctOZMap, zctOZRaw)
	}
}

func TestInterpIsZeroOmitZeroTags(t *testing.T) {
	var s []int
	if !interpIsZero(unsafe.Pointer(&s), zctOZSlice) {
		t.Error("nil slice must be zero")
	}
	s = []int{}
	if interpIsZero(unsafe.Pointer(&s), zctOZSlice) {
		t.Error("empty non-nil slice must not be zero")
	}

	var m map[string]int
	if !interpIsZero(unsafe.Pointer(&m), zctOZMap) {
		t.Error("nil map must be zero")
	}
	m = map[string]int{}
	if interpIsZero(unsafe.Pointer(&m), zctOZMap) {
		t.Error("empty non-nil map must not be zero")
	}

	var r json.RawMessage
	if !interpIsZero(unsafe.Pointer(&r), zctOZRaw) {
		t.Error("nil RawMessage must be zero")
	}
	r = json.RawMessage{}
	if interpIsZero(unsafe.Pointer(&r), zctOZRaw) {
		t.Error("empty non-nil RawMessage must not be zero")
	}
}

// Every method-mode field costs one yield round-trip per encode; the
// resumes must land byte-identical to encoding/json.
func TestOmitZeroNativeYieldResume(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	type S struct {
		A time.Time    `json:"a,omitzero"`
		M int          `json:"m"`
		B time.Time    `json:"b,omitzero"`
		C *time.Time   `json:"c,omitzero"`
		D ozTestStruct `json:"d,omitzero"`
	}
	want, err := json.Marshal(S{M: 5, B: time.Unix(2, 0), C: new(time.Time)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(S{M: 5, B: time.Unix(2, 0), C: new(time.Time)})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("yield resume:\n got:  %s\n want: %s", got, want)
	}
}

type ozTestStruct struct {
	X int
	Y string
}

// A buffer that fills mid-emission (after the omitzero check advanced past
// the skip-go op) must resume without re-running the check or duplicating the
// key: the skip-go op is never re-executed because its PC advanced in Go.
func TestOmitZeroNativeBufFullMidField(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	type S struct {
		Pad  string    `json:"p"`
		When time.Time `json:"when,omitzero"`
		Tail string    `json:"t"`
	}
	v := S{Pad: string(make([]byte, 8)), When: time.Unix(1, 0), Tail: "end"}

	want, _ := json.Marshal(v)
	wantLen := len(want)

	ti := EncTypeInfoOf(reflect.TypeFor[S]())
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
			t.Fatalf("cap=%d: error: %v", cap, err)
		}
		if string(got) != string(want) {
			t.Fatalf("cap=%d: mismatch\n  got:  %s\n  want: %s", cap, got, want)
		}
	}
}
