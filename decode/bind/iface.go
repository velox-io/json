package bind

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/jerr"
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
// those slots are copied into runtime maps. Source-backed spans slice the
// current source; scratch-backed spans slice the feed driver's raw scratch,
// the materialized copy of a value that crossed a window edge.
func drainDeferredRecords(p *Parser, m *ndec.BindMachine) error {
	used := m.Alloc.DeferredDrainUsed
	if used == 0 {
		return nil
	}
	src, raw := p.curSrc(), p.feedRaw()
	buf := unsafe.Slice(m.Alloc.DeferredDrain, used)
	for off := uint32(0); off < used; off += ndec.UnmarshalRecordSize {
		rec := (*ndec.UnmarshalRecord)(unsafe.Pointer(&buf[off]))
		switch vbind.Kind(rec.Kind) {
		case vbind.KindUnmarshaler, vbind.KindRawMessage:
			span := src
			if rec.Backing == ndec.BindRecordBackingScratch {
				span = raw
			}
			data := trimTrailingWS(span[rec.Arg0:rec.Arg1])
			if rec.Kind == uint8(vbind.KindUnmarshaler) {
				hooks := p.tt.UnmarshalHooks[rec.TypeIdx]
				if hooks == nil {
					return errors.New("bind: unmarshal hook missing for type")
				}
				if err := hooks.UnmarshalFn(unsafe.Pointer(rec.Target), data); err != nil {
					return err
				}
			} else {
				// json.RawMessage is []byte. Appending into the destination in
				// place keeps the slice header off the heap and reuses any capacity
				// already there, which is also what RawMessage.UnmarshalJSON does.
				// Under the zero-copy opt a source-backed span aliases the caller
				// buffer, with cap clamped so appends stay off the caller's memory.
				if p.optFlags&ndec.BindOptZeroCopyStr != 0 && rec.Backing == ndec.BindRecordBackingSource {
					dst := (*[]byte)(unsafe.Pointer(rec.Target))
					*dst = data[:len(data):len(data)]
				} else {
					dst := (*[]byte)(unsafe.Pointer(rec.Target))
					*dst = append((*dst)[:0], data...)
				}
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
		case vbind.KindIface:
			span := src
			if rec.Backing == ndec.BindRecordBackingScratch {
				span = raw
			}
			data := trimTrailingWS(span[rec.Arg0:rec.Arg1])
			if err := bindIfaceRecord(p, rec, data); err != nil {
				return err
			}
		case vbind.KindSlice:
			// A []byte target staged from a JSON string: decode base64 from
			// the interned string bytes, like encoding/json. An empty string
			// yields a non-nil empty slice. The record carries a str-arena
			// span, not a document position, so the syntax error claims none.
			strBase := unsafe.Pointer(m.Alloc.StrArena)
			s := unsafe.Slice((*byte)(unsafe.Add(strBase, uintptr(rec.Arg0))), rec.Arg1)
			dbuf := make([]byte, base64.StdEncoding.DecodedLen(len(s)))
			n, err := base64.StdEncoding.Decode(dbuf, s)
			if err != nil {
				return jerr.NewSyntaxErrorWrap(
					fmt.Sprintf("vjson: invalid base64 in []byte field: %v", err), 0, err)
			}
			*(*[]byte)(unsafe.Pointer(rec.Target)) = dbuf[:n]
		default:
			return errors.New("bind: unknown unmarshal record kind")
		}
	}
	m.Alloc.DeferredDrainUsed = 0
	return nil
}

// recordDocOffset translates a record's span offset into a document offset.
// Source-backed records drain before their window relocates, so the live
// window base rebases them; scratch-backed spans name the raw scratch, whose
// bytes have no document position.
func recordDocOffset(p *Parser, rec *ndec.UnmarshalRecord) int64 {
	if rec.Backing == ndec.BindRecordBackingScratch {
		return 0
	}
	if p.feed != nil {
		return int64(rec.Arg0) + int64(p.feed.base)
	}
	return int64(rec.Arg0)
}

// bindIfaceRecord applies the encoding/json strategy over a staged non-empty
// interface slot. A null literal clears the slot without consulting the
// dynamic value. For any other value the dynamic value decides: a pointer
// implementing json.Unmarshaler receives the raw value, a pointer implementing
// only encoding.TextUnmarshaler accepts a string, any other pointer gets an
// in-place decode into its pointee, and a nil interface or a non-pointer
// dynamic value is a type error. Container elements always stage a zero slot,
// so they fail for every non-null value, matching encoding/json, which decodes
// map and slice elements into fresh zero values.
func bindIfaceRecord(p *Parser, rec *ndec.UnmarshalRecord, data []byte) error {
	slot := (*[2]unsafe.Pointer)(unsafe.Pointer(rec.Target))
	if len(data) == 0 || data[0] == 'n' {
		*slot = [2]unsafe.Pointer{}
		return nil
	}
	ifaceType := p.tt.ReflectTypes[rec.TypeIdx]
	typeErr := func() error {
		return &UnmarshalTypeError{Value: jsonValueName(data, 0, 0), Type: ifaceType, Offset: recordDocOffset(p, rec)}
	}
	if slot[0] == nil {
		return typeErr()
	}
	dv := reflect.NewAt(ifaceType, unsafe.Pointer(rec.Target)).Elem().Elem()
	if dv.Kind() != reflect.Pointer {
		return typeErr()
	}
	if u, ok := dv.Interface().(json.Unmarshaler); ok {
		return u.UnmarshalJSON(data)
	}
	if tu, ok := dv.Interface().(encoding.TextUnmarshaler); ok {
		if data[0] != '"' {
			return typeErr()
		}
		var s string
		if err := unmarshalRawInto(p, data, reflect.TypeFor[string](), unsafe.Pointer(&s)); err != nil {
			return err
		}
		return tu.UnmarshalText([]byte(s))
	}
	return unmarshalRawInto(p, data, dv.Elem().Type(), unsafe.Pointer(dv.Pointer()))
}

// unmarshalRawInto decodes one complete JSON value into ptr through a fresh
// parser over the type's cached shape. It backs interface-slot sub-decodes
// (pointee recovery and TextUnmarshaler string extraction), which run inside
// the parent drain, so it must never borrow the parent machine.
func unmarshalRawInto(p *Parser, data []byte, rt reflect.Type, ptr unsafe.Pointer) error {
	sh, err := shapeFor(rt)
	if err != nil {
		return err
	}
	sp := getParser(sh)
	defer putParser(sh, sp)
	// The sub-parse copies its input through a reusable pad buffer, so the
	// zero-copy alias must not propagate.
	sp.optFlags = p.optFlags &^ ndec.BindOptZeroCopyStr
	return sp.unmarshal(data, ptr)
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
