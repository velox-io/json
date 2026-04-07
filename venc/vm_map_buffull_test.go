package venc

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

func TestMapBufFull_EntryFirstResume(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	type mapBufFullPad struct {
		Pad string            `json:"p"`
		M   map[string]string `json:"m"`
	}

	padLen := 5
	v := mapBufFullPad{
		Pad: strings.Repeat("A", padLen),
		M:   map[string]string{"k": "v"},
	}
	// output: {"p":"AAAAA","m":{"k":"v"}} = 27 bytes

	ti := EncTypeInfoOf(reflect.TypeFor[mapBufFullPad]())

	es := acquireEncodeState()
	WithBufSize(20)(es)
	defer releaseEncodeState(es)

	es.buf = make([]byte, 0, 20)
	got, err := es.marshalWith(ti, unsafe.Pointer(&v))

	if err != nil {
		t.Fatalf("error: %v", err)
	}

	want, _ := json.Marshal(v)
	t.Logf("output_len=%d got=%s", len(got), got)
	if string(got) != string(want) {
		t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}
