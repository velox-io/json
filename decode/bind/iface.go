package bind

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// deferredErrs holds the first failures of a drive's hook calls and map-key
// conversions. Drains run wherever buffer capacity, slice growth, or
// windowing demands, so they only record failures; walk errors take
// precedence, and the report points (drive end, stream batch handoff)
// surface what was recorded. A hook failure outranks a key failure, which
// keeps the verdict independent of the order the drains ran in.
type deferredErrs struct{ hook, key error }

func (e *deferredErrs) noteHook(err error) {
	if e.hook == nil {
		e.hook = err
	}
}

func (e *deferredErrs) noteKey(err error) {
	if e.key == nil {
		e.key = err
	}
}

func (e *deferredErrs) err() error {
	if e.hook != nil {
		return e.hook
	}
	return e.key
}

// settleStaged is a report point: it drains the staged hook records, then
// the map slots their writes feed, and returns the drive's first recorded
// failure.
func (p *Parser) settleStaged(m *ndec.BindMachine) error {
	if m.Alloc.DeferredDrainUsed > 0 {
		drainDeferredRecords(p, m)
	}
	if m.Alloc.MapBufUsed > 0 {
		drainAllMapSlots(p, m)
	}
	return p.failed.err()
}

// drainDeferredRecords invokes deferred unmarshaling hooks over their captured
// spans. It runs before map drain so hook writes reach intermediate slots before
// those slots are copied into runtime maps. Source-backed spans slice the
// current source; scratch-backed spans slice the feed driver's raw scratch,
// the materialized copy of a value that crossed a window edge. Every record
// runs; failures go to p.failed.
func drainDeferredRecords(p *Parser, m *ndec.BindMachine) {
	used := m.Alloc.DeferredDrainUsed
	if used == 0 {
		return
	}
	src, raw := p.curSrc(), p.feedRaw()
	// A zero-copy drive over an internal copy publishes spans that alias the
	// caller-owned original, so the pooled pad buffer's next reuse cannot
	// corrupt them.
	if p.optFlags&ndec.BindOptZeroCopyStr != 0 && p.aliasSrc != nil {
		src = p.aliasSrc
	}
	buf := unsafe.Slice(m.Alloc.DeferredDrain, used)
	for off := uint32(0); off < used; off += ndec.UnmarshalRecordSize {
		rec := (*ndec.UnmarshalRecord)(unsafe.Pointer(&buf[off]))
		kind := vbind.Kind(rec.Kind)
		var data []byte
		switch kind {
		case vbind.KindTextUnmarshaler, vbind.KindSlice:
			data = recordSpanBytes(m, rec, src, raw)
		default:
			span := src
			if rec.Backing == ndec.BindRecordBackingScratch {
				span = raw
			}
			data = gdec.TrimTrailingWS(span[rec.Arg0:rec.Arg1])
		}
		borrowed := p.optFlags&ndec.BindOptZeroCopyStr != 0 && rec.Backing == ndec.BindRecordBackingSource
		if err := applyDeferred(p, kind, uint16(rec.TypeIdx), unsafe.Pointer(rec.Target), data, borrowed,
			recordDocOffset(p, rec)); err != nil {
			p.failed.noteHook(err)
		}
	}
	m.Alloc.DeferredDrainUsed = 0
}

// applyDeferred runs one deferred hook. data is the trimmed raw JSON span
// for Unmarshaler, RawMessage, and non-empty interface targets, and the
// decoded string body for TextUnmarshaler and base64 []byte targets.
// borrowed reports that data aliases caller-owned input under the zero-copy
// opt. docOff is the span's document offset, zero when it has none.
func applyDeferred(p *Parser, kind vbind.Kind, typeIdx uint16, target unsafe.Pointer, data []byte,
	borrowed bool, docOff int64) error {
	switch kind {
	case vbind.KindUnmarshaler:
		hooks := p.tt.UnmarshalHooks[typeIdx]
		if hooks == nil {
			return errors.New("bind: unmarshal hook missing for type")
		}
		return hooks.UnmarshalFn(target, data)
	case vbind.KindRawMessage:
		// json.RawMessage is []byte. Appending into the destination in place
		// keeps the slice header off the heap and reuses any capacity already
		// there, which is also what RawMessage.UnmarshalJSON does. A borrowed
		// span aliases the caller buffer, with cap clamped so appends stay off
		// the caller's memory.
		dst := (*[]byte)(target)
		if borrowed {
			*dst = data[:len(data):len(data)]
		} else {
			*dst = append((*dst)[:0], data...)
		}
		return nil
	case vbind.KindTextUnmarshaler:
		hooks := p.tt.UnmarshalHooks[typeIdx]
		if hooks == nil {
			return errors.New("bind: unmarshal hook missing for type")
		}
		return hooks.TextUnmarshalFn(target, data)
	case vbind.KindIface:
		return bindIfaceRecord(p, typeIdx, target, data, docOff)
	case vbind.KindSlice:
		// A []byte target staged from a JSON string: decode base64 from the
		// string bytes, like encoding/json. An empty string yields a non-nil
		// empty slice. The span carries no document position, so the syntax
		// error claims none.
		dbuf := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
		n, err := base64.StdEncoding.Decode(dbuf, data)
		if err != nil {
			return jerr.NewSyntaxErrorWrap(
				fmt.Sprintf("vjson: invalid base64 in []byte field: %v", err), 0, err)
		}
		*(*[]byte)(target) = dbuf[:n]
		return nil
	}
	return errors.New("bind: unknown unmarshal record kind")
}

// recordDocOffset translates a record's span offset into a document offset.
// Source-backed records drain before their window relocates, so the live
// window base rebases them; scratch-backed and str-arena-backed spans carry
// bytes with no document position.
func recordDocOffset(p *Parser, rec *ndec.UnmarshalRecord) int64 {
	if rec.Backing != ndec.BindRecordBackingSource {
		return 0
	}
	if p.feed != nil {
		return int64(rec.Arg0) + int64(p.feed.base)
	}
	return int64(rec.Arg0)
}

// recordSpanBytes returns the string body a TextUnmarshaler or base64 record
// references: a source span borrowed under the zero-copy opt, or the interned
// str-arena bytes.
func recordSpanBytes(m *ndec.BindMachine, rec *ndec.UnmarshalRecord, src, raw []byte) []byte {
	if rec.Backing == ndec.BindRecordBackingStrArena {
		strBase := unsafe.Pointer(m.Alloc.StrArena)
		return unsafe.Slice((*byte)(unsafe.Add(strBase, uintptr(rec.Arg0))), rec.Arg1)
	}
	span := src
	if rec.Backing == ndec.BindRecordBackingScratch {
		span = raw
	}
	return span[rec.Arg0:rec.Arg1]
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
func bindIfaceRecord(p *Parser, typeIdx uint16, target unsafe.Pointer, data []byte, docOff int64) error {
	slot := (*[2]unsafe.Pointer)(target)
	if len(data) == 0 || data[0] == 'n' {
		*slot = [2]unsafe.Pointer{}
		return nil
	}
	ifaceType := p.tt.ReflectTypes[typeIdx]
	typeErr := func() error {
		return &UnmarshalTypeError{Value: jsonValueName(data, 0, 0), Type: ifaceType, Offset: docOff}
	}
	if slot[0] == nil {
		return typeErr()
	}
	dv := reflect.NewAt(ifaceType, target).Elem().Elem()
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
			return p.rebaseSubErr(err, docOff)
		}
		return tu.UnmarshalText([]byte(s))
	}
	return p.rebaseSubErr(unmarshalRawInto(p, data, dv.Elem().Type(), unsafe.Pointer(dv.Pointer())), docOff)
}

// rebaseSubErr moves an error from a sub-decode of the value at document
// offset docOff into the document's coordinates: offsets shift by docOff,
// and a type error's field path gains the path to the value.
func (p *Parser) rebaseSubErr(err error, docOff int64) error {
	var ute *UnmarshalTypeError
	var se *SyntaxError
	switch {
	case errors.As(err, &ute):
		ute.Offset += docOff
		if prefix, ok := p.pathTo(docOff); ok && prefix != "" {
			if ute.Field != "" {
				ute.Field = prefix + "." + ute.Field
			} else {
				ute.Field = prefix
			}
			if p.tt.Root < uint32(len(p.tt.ReflectTypes)) {
				ute.Struct = p.tt.ReflectTypes[p.tt.Root].Name()
			}
		}
	case errors.As(err, &se):
		se.Offset += docOff
	}
	return err
}

// pathTo returns the dotted JSON path of the value at document offset off,
// when the drive's source still holds the document from its root.
func (p *Parser) pathTo(off int64) (string, bool) {
	if p.pathSrc == nil || off < int64(p.pathBase) {
		return "", false
	}
	tokens, _, ok := walkJSONPath(p.pathSrc, int(off-int64(p.pathBase)))
	if !ok {
		return "", false
	}
	return strings.Join(tokens, "."), true
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
	// The sub-parse owns its aliasSrc for this span, so its zero-copy aliases
	// rebase into the same caller-owned backing the parent published from.
	sp.optFlags = p.optFlags
	return sp.unmarshal(data, ptr)
}
