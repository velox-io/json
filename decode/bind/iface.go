package bind

import (
	"errors"
	"unsafe"

	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// syncDeferredDrain mirrors the allocator's deferred-value buffer into the ABI
// and resets its bump cursor.
func syncDeferredDrain(alloc *vbind.Allocator, allocABI *ndec.BindAllocator) {
	allocABI.DeferredDrain = (*byte)(unsafe.Pointer(unsafe.SliceData(alloc.DeferredDrain)))
	allocABI.DeferredDrainCap = uint32(cap(alloc.DeferredDrain))
	allocABI.DeferredDrainUsed = 0
}

// drainDeferredRecords invokes deferred unmarshaling hooks over their captured
// spans. It runs before map drain so hook writes reach intermediate slots before
// those slots are copied into runtime maps.
func drainDeferredRecords(p *Parser, m *ndec.BindMachine, src []byte) error {
	used := m.Alloc.DeferredDrainUsed
	if used == 0 {
		return nil
	}
	buf := unsafe.Slice(m.Alloc.DeferredDrain, used)
	for off := uint32(0); off < used; off += ndec.UnmarshalRecordSize {
		rec := (*ndec.UnmarshalRecord)(unsafe.Pointer(&buf[off]))
		switch vbind.Kind(rec.Kind) {
		case vbind.KindUnmarshaler:
			hooks := p.tt.UnmarshalHooks[rec.TypeIdx]
			if hooks == nil {
				return errors.New("bind: unmarshal hook missing for type")
			}
			data := trimTrailingWS(src[rec.Arg0:rec.Arg1])
			if err := hooks.UnmarshalFn(unsafe.Pointer(rec.Target), data); err != nil {
				return err
			}
		case vbind.KindTextUnmarshaler:
			hooks := p.tt.UnmarshalHooks[rec.TypeIdx]
			if hooks == nil {
				return errors.New("bind: unmarshal hook missing for type")
			}
			strBase := unsafe.Pointer(m.Alloc.StrArena)
			data := unsafe.Slice((*byte)(unsafe.Add(strBase, uintptr(rec.Arg0))), rec.Arg1)
			if err := hooks.TextUnmarshalFn(unsafe.Pointer(rec.Target), data); err != nil {
				return err
			}
		case vbind.KindRawMessage:
			// json.RawMessage is []byte. Appending into the destination in
			// place keeps the slice header off the heap and reuses any capacity
			// already there, which is also what RawMessage.UnmarshalJSON does.
			data := trimTrailingWS(src[rec.Arg0:rec.Arg1])
			dst := (*[]byte)(unsafe.Pointer(rec.Target))
			*dst = append((*dst)[:0], data...)
		default:
			return errors.New("bind: unknown unmarshal record kind")
		}
	}
	m.Alloc.DeferredDrainUsed = 0
	return nil
}

// trimTrailingWS trims whitespace from the end of a JSON byte span to match
// encoding/json's exact-byte semantics for UnmarshalJSON / RawMessage inputs.
// The structural index span can include trailing whitespace between the value
// and the next structural character.
func trimTrailingWS(data []byte) []byte {
	for len(data) > 0 {
		c := data[len(data)-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			data = data[:len(data)-1]
		} else {
			break
		}
	}
	return data
}
