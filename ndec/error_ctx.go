// lazy error rendering with field path construction.
//
// TYPE_MISMATCH and UNKNOWN_FIELD errors capture a snapshot of
// ctx.Frames at the error site and lazily render the field path
// string (stdlib-compatible) when .Error() or errors.As is called.
// Hot paths are unaffected: all allocation and formatting is deferred
// to the error path.

package ndec

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

// ndecMaxDepth must match C-side NDEC_MAX_DEPTH.
const errCtxMaxDepth = ndecMaxDepth

// errCtx holds the raw materials for rendering a field-path error.
// It is populated once at yield time and read at most once (by the
// lazy render).  No heap allocation for frames: the snapshot lives
// inline.
//
// errCtx is lazy. The driver returns driverState to the pool at the end
// of Unmarshal; pool buffers (d.scratch, d.kvBuf) are overwritten on the
// next call. Any pointer into a driver-private buffer is stale by the time
// err.Error() runs. Therefore:
//  1. rawPtr/rawLen point into the user's input slice (which the driver
//     KeepAlives only during runUnmarshal). The lazy render does not hold
//     input; the "value description" is pre-computed into valueDesc at
//     capture time and rawPtr is not read later.
//  2. Unknown-field Field names also come from rawPtr and are deep-copied
//     into rawCopy at capture time.
//  3. MAP-frame KV slot key strings reference d.kvBuf / d.scratch and
//     likewise go stale. stdlib does not render map keys in
//     UnmarshalTypeError.Field, so ndec follows suit, sidestepping the
//     driver buffer lifetime issue.
type errCtx struct {
	frames [errCtxMaxDepth]ndecFrame // snapshot of ctx.Frames[0..depth-1]
	depth  int                       // active frame count

	offset int64 // byte offset in input (C-side CUR_OFFSET)

	// valueDesc is the result of describeJSONValue(rawPtr, rawLen),
	// pre-computed at capture time. rawPtr is not retained in errCtx
	// to avoid crossing the input's lifetime.
	valueDesc string
	// rawCopy is a deep copy of the raw token bytes, only used by lazy
	// unknown-field errors that need to render the field name. Regular
	// type errors do not read this field.
	rawCopy []byte

	// Leaf field metadata (only populated for STRUCT frames):
	leafFieldIdx int32 // BindPendingFieldIdx at error time; -1 if N/A

	rootBT *typeInfo // root type info (for field name resolution)
}

// lazyTypeError implements error for TYPE_MISMATCH with lazy path rendering.
type lazyTypeError struct {
	ctx      *errCtx
	once     sync.Once
	rendered *UnmarshalTypeError
}

func (e *lazyTypeError) Error() string {
	e.once.Do(e.render)
	if e.rendered == nil {
		return "ndec: type mismatch"
	}
	return e.rendered.Error()
}

func (e *lazyTypeError) Unwrap() error {
	e.once.Do(e.render)
	if e.rendered == nil {
		return nil
	}
	return e.rendered
}

// As supports errors.As for json.UnmarshalTypeError compat and
// *UnmarshalTypeError lookup.
func (e *lazyTypeError) As(target any) bool {
	e.once.Do(e.render)
	if e.rendered == nil {
		return false
	}
	switch t := target.(type) {
	case **json.UnmarshalTypeError:
		*t = &json.UnmarshalTypeError{
			Value:  e.rendered.Value,
			Type:   e.rendered.Type,
			Offset: e.rendered.Offset,
			Struct: e.rendered.Struct,
			Field:  e.rendered.Field,
		}
		return true
	case *json.UnmarshalTypeError:
		*t = json.UnmarshalTypeError{
			Value:  e.rendered.Value,
			Type:   e.rendered.Type,
			Offset: e.rendered.Offset,
			Struct: e.rendered.Struct,
			Field:  e.rendered.Field,
		}
		return true
	case **UnmarshalTypeError:
		*t = e.rendered
		return true
	case *UnmarshalTypeError:
		*t = *e.rendered
		return true
	}
	return false
}

// render populates the UnmarshalTypeError from the captured context.
func (e *lazyTypeError) render() {
	ec := e.ctx
	ute := &UnmarshalTypeError{
		Value:  ec.valueDesc,
		Offset: ec.offset,
		Type:   ec.leafType(),
	}
	ute.Struct, ute.Field = ec.renderFieldPath()
	e.rendered = ute
}

// lazyUnknownFieldError implements error for UNKNOWN_FIELD with lazy rendering.
type lazyUnknownFieldError struct {
	ctx      *errCtx
	once     sync.Once
	rendered *UnknownFieldError
}

func (e *lazyUnknownFieldError) Error() string {
	e.once.Do(e.render)
	if e.rendered == nil {
		return "ndec: unknown field"
	}
	return e.rendered.Error()
}

func (e *lazyUnknownFieldError) Unwrap() error {
	e.once.Do(e.render)
	if e.rendered == nil {
		return nil
	}
	return e.rendered
}

func (e *lazyUnknownFieldError) render() {
	ec := e.ctx
	ufe := &UnknownFieldError{
		Offset: ec.offset,
	}
	ufe.Struct, ufe.Field = ec.renderFieldPath()
	// For unknown field, the unknown field name might be in the raw token.
	// rawCopy is a deep copy from capture time and no longer goes stale
	// when the driver is reused.
	if len(ec.rawCopy) > 0 {
		ufe.Field = strings.TrimSuffix(strings.TrimPrefix(string(ec.rawCopy), `"`), `"`)
	}
	e.rendered = ufe
}

func describeJSONValue(raw []byte) string {
	if len(raw) == 0 {
		return "null"
	}
	switch raw[0] {
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	case '{':
		return "object"
	case '[':
		return "array"
	default:
		return "number"
	}
}

// describeTokenKind translates the reactor's yield_flags token-kind number
// (NDEC_YF_TOKEN_*) into a stdlib-compatible Value string. 0 means the
// reactor did not set a token kind.
func describeTokenKind(flag uint8) string {
	switch flag {
	case yfTokenNull:
		return "null"
	case yfTokenBool:
		return "bool"
	case yfTokenNumber:
		return "number"
	case yfTokenString:
		return "string"
	case yfTokenObject:
		return "object"
	case yfTokenArray:
		return "array"
	}
	return ""
}

func (ec *errCtx) leafType() reflect.Type {
	if ec.depth == 0 {
		return nil
	}
	leaf := &ec.frames[ec.depth-1]
	leafTI := (*typeInfo)(leaf.BindType)

	switch bindKind(leaf.BindContainerKind) {
	case bkStruct:
		// Leaf is a STRUCT frame. The error is on a field value.
		// Use the leaf frame's own typeInfo to resolve the field.
		if leafTI == nil || ec.leafFieldIdx < 0 {
			return nil
		}
		fields := leafTI.structFields()
		idx := int(ec.leafFieldIdx)
		if idx < 0 || idx >= len(fields) {
			return nil
		}
		fi := fields[idx]
		if fi.Type != nil {
			return (*typeInfo)(fi.Type).rt()
		}
		return reflectTypeForBindKind(bindKind(fi.Kind))

	case bkSlice, bkFixedArray:
		if leafTI != nil {
			return leafTI.rt()
		}

	case bkMap:
		if leafTI != nil {
			if et := leafTI.elemTypeInfo(); et != nil {
				return et.rt()
			}
		}
	}

	return nil
}

func reflectTypeForBindKind(k bindKind) reflect.Type {
	switch k {
	case bkBool:
		return reflect.TypeFor[bool]()
	case bkInt:
		return reflect.TypeFor[int]()
	case bkInt8:
		return reflect.TypeFor[int8]()
	case bkInt16:
		return reflect.TypeFor[int16]()
	case bkInt32:
		return reflect.TypeFor[int32]()
	case bkInt64:
		return reflect.TypeFor[int64]()
	case bkUint:
		return reflect.TypeFor[uint]()
	case bkUint8:
		return reflect.TypeFor[uint8]()
	case bkUint16:
		return reflect.TypeFor[uint16]()
	case bkUint32:
		return reflect.TypeFor[uint32]()
	case bkUint64:
		return reflect.TypeFor[uint64]()
	case bkFloat32:
		return reflect.TypeFor[float32]()
	case bkFloat64:
		return reflect.TypeFor[float64]()
	case bkString:
		return reflect.TypeFor[string]()
	default:
		return nil
	}
}

// renderParentFieldSegment renders the intermediate field name for a
// SLICE/MAP frame based on its parent_field_idx.
func (ec *errCtx) renderParentFieldSegment(b *strings.Builder, frames []ndecFrame, frameIdx int) {
	frame := &frames[frameIdx]
	if frame.ParentFieldIdx < 0 || frameIdx == 0 {
		return
	}
	parent := &frames[frameIdx-1]
	if parent.BindContainerKind != uint8(bkStruct) {
		return
	}
	parentTI := (*typeInfo)(parent.BindType)
	if parentTI == nil || parentTI.kind() != bkStruct {
		return
	}
	origPaths := parentTI.structFieldOriginalPaths()
	idx := int(frame.ParentFieldIdx)
	if idx >= 0 && idx < len(origPaths) && len(origPaths[idx]) > 0 {
		for _, seg := range origPaths[idx] {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(seg)
		}
		return
	}
	fieldNames := parentTI.structFieldNames()
	if idx >= 0 && idx < len(fieldNames) {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(fieldNames[idx])
	}
}

func (ec *errCtx) renderFieldPath() (structName, fieldPath string) {
	frames := ec.frames[:ec.depth]
	if len(frames) == 0 {
		return "", ""
	}

	// stdlib sets UnmarshalTypeError.Struct to the innermost STRUCT type
	// name that contains the leaf field (empty string for anonymous structs).
	// Walk backwards to find the innermost STRUCT frame.
	for i := len(frames) - 1; i >= 0; i-- {
		if bindKind(frames[i].BindContainerKind) == bkStruct {
			ti := (*typeInfo)(frames[i].BindType)
			if ti != nil && ti.rt() != nil {
				structName = ti.rt().Name()
			}
			break
		}
	}

	var b strings.Builder
	lastIdx := len(frames) - 1
	for i, f := range frames {
		switch bindKind(f.BindContainerKind) {
		case bkStruct:
			idx := f.BindPendingFieldIdx
			if i == lastIdx && ec.leafFieldIdx >= 0 {
				idx = ec.leafFieldIdx
			}
			if idx >= 0 {
				ti := (*typeInfo)(f.BindType)
				if ti != nil && ti.kind() == bkStruct {
					// stdlib renders the full Go embedding path in
					// UnmarshalTypeError.Field (e.g. "Inner.Y" not just
					// "Y" for Outer{Inner; Y}). structFieldOriginalPaths
					// stores the Go-level path from build time so we can
					// reconstruct it. Fall back to fieldNames for plain
					// fields with no embedding.
					origPaths := ti.structFieldOriginalPaths()
					if int(idx) < len(origPaths) && len(origPaths[idx]) > 0 {
						for _, seg := range origPaths[idx] {
							if b.Len() > 0 {
								b.WriteByte('.')
							}
							b.WriteString(seg)
						}
					} else {
						fieldNames := ti.structFieldNames()
						if int(idx) < len(fieldNames) {
							if b.Len() > 0 {
								b.WriteByte('.')
							}
							b.WriteString(fieldNames[idx])
						}
					}
				}
			}

		case bkSlice, bkFixedArray:
			// stdlib does not render array indices in Field
			// (e.g. "Items.Name" not "Items[1].Name"). We still
			// render the parent STRUCT field name that triggered
			// this SLICE push (e.g. "Items"). begin_array captured
			// ParentFieldIdx on the child frame; we read it here
			// to produce the stdlib-style intermediate segment.
			ec.renderParentFieldSegment(&b, frames, i)

		case bkMap:
			// stdlib sets only Value for map value type mismatches
			// (e.g. "string" into int), never renders [key] in Field.
			// We follow the same behavior. However stdlib still
			// carries the parent STRUCT field name (e.g. "Settings"
			// for a map[string]int field "Settings") via the
			// ParentFieldIdx captured during begin_map.
			ec.renderParentFieldSegment(&b, frames, i)
		}
	}
	return structName, b.String()
}

func (d *driverState) makeTypeError() error {
	ec := &errCtx{
		depth:  int(d.ctx.Depth),
		offset: int64(d.ctx.ErrorPos),
		rootBT: d.rootBT,
	}

	if v := describeTokenKind(d.userData.YieldFlags); v != "" {
		ec.valueDesc = v
	} else {
		rawSlice := rawTokenSlice(d.userData.RawPtr, d.userData.RawLen)
		ec.valueDesc = describeJSONValue(rawSlice)
	}

	// Snapshot frames.
	copy(ec.frames[:ec.depth], d.ctx.Frames[:ec.depth])

	// Extract leaf field info from the innermost frame.
	if ec.depth > 0 {
		leaf := &ec.frames[ec.depth-1]
		switch bindKind(leaf.BindContainerKind) {
		case bkStruct:
			ec.leafFieldIdx = leaf.BindPendingFieldIdx
		default:
			ec.leafFieldIdx = -1
		}
	}

	return &lazyTypeError{ctx: ec}
}

// makeUnknownFieldError captures error context for unknown field errors.
func (d *driverState) makeUnknownFieldError() error {
	ec := &errCtx{
		depth:  int(d.ctx.Depth),
		offset: int64(d.ctx.ErrorPos),
		rootBT: d.rootBT,
	}

	// unknown field name comes from raw token. Deep-copy to avoid
	// referencing input/scratch that may be reused.
	if d.userData.RawLen > 0 && d.userData.RawPtr != nil {
		raw := unsafe.Slice((*byte)(d.userData.RawPtr), d.userData.RawLen)
		ec.rawCopy = append([]byte(nil), raw...)
	}

	// Snapshot frames.
	copy(ec.frames[:ec.depth], d.ctx.Frames[:ec.depth])

	return &lazyUnknownFieldError{ctx: ec}
}

// rawTokenSlice wraps unsafe.Slice with nil/len guard. Only called during
// capture (while the driver holds the input reference); the returned slice
// must not outlive the capture.
func rawTokenSlice(p unsafe.Pointer, n uint32) []byte {
	if p == nil || n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(p), n)
}
