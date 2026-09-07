package bind

import (
	"io"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/value"
)

// The str-arena provenance table lives in the pooled noscan machine block, so
// a dropped entry must have its base nil'd while the retired backing is still
// retained (strprov.go documents the discipline). Lowering the count alone
// leaves the base behind, and the next append's pointer store then hands the
// write barrier an old value whose span a later GC cycle has freed, tripping
// the runtime's bad-pointer check.
//
// Decoder.Decode's live-window branch is the path that made this reachable: it
// runs the cursor positioning loop, which can serve an input yield and record a
// retired generation, before feedBegin resets the table for the new value.

// strProvEntryBase reads one provenance entry's base directly, bypassing the
// count so a leaked base is visible above the live height.
func strProvEntryBase(m *ndec.BindMachine, i int) unsafe.Pointer {
	return (*ndec.BindStrProvEntry)(unsafe.Add(unsafe.Pointer(m),
		ndec.BindMachineStrProvOffset+uintptr(i)*unsafe.Sizeof(ndec.BindStrProvEntry{}))).Base
}

// TestFeedBeginClearsStrProvBases drives enough ndjson values through a window
// small enough to force repeated str-arena growth inside the positioning loop,
// then asserts no entry at or above the live count kept a base.
func TestFeedBeginClearsStrProvBases(t *testing.T) {
	type elem struct {
		ID   string      `json:"id"`
		N    int64       `json:"n"`
		Tags []string    `json:"tags"`
		V    value.Value `json:"v"`
	}

	src := newNdjsonReader(8 << 20)
	// A small window makes each value's arena sizing tight, so mounts during
	// the positioning loop grow the arena and retire generations often.
	d := NewDecoder(src, WithBufferSize(1<<12))

	var count int64
	for {
		var e elem
		err := d.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Decode at value %d: %v", count, err)
		}
		count++

		m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(d.parser.machine)))
		for i := int(m.StrProvCount()); i < ndec.BindStrProvMax; i++ {
			if base := strProvEntryBase(m, i); base != nil {
				t.Fatalf("value %d: provenance entry %d above count %d kept base %p",
					count, i, m.StrProvCount(), base)
			}
		}
	}
	if count == 0 {
		t.Fatal("decoded no values")
	}
	t.Logf("values=%d", count)
}
