package venc

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/vopt"
)

// MarshalOption is an alias for vopt.Options.
type MarshalOption = vopt.Options

// encFlagsOf translates o into the flag word the escaper and the native VM
// read. Absent options stay zero, which is the no-escape fast path.
func encFlagsOf(o vopt.Options) uint32 {
	var f uint32
	if o.Enabled(vopt.FlagEscapeHTML) {
		f |= uint32(escapeHTML)
	}
	if o.Enabled(vopt.FlagEscapeLineTerms) {
		f |= uint32(escapeLineTerms)
	}
	if allow, named := o.Get(vopt.FlagAllowInvalidUTF8); named && !allow {
		f |= uint32(escapeInvalidUTF8) | EncRawUTF8Repl
	}
	if o.Enabled(vopt.FlagFloatExpAuto) {
		f |= EncFloatExpAuto
	}
	return f
}

// applyOptions resolves o onto es. acquireEncodeState has already zeroed flags
// and indentString, so only named options need writing.
func (es *encodeState) applyOptions(o vopt.Options) {
	es.flags = encFlagsOf(o)
	if prefix, step, ok := o.Indent(); ok {
		es.indentPrefix = prefix
		es.indentString = step
		es.indentDepth = 0
		es.useNativeVM = encvm.Available && isSimpleIndent(prefix, step)
	}
	es.bufSize = o.BufSize()
}

// Marshal serializes v to JSON.
//
// Pointer T: handled inline; v is 8 bytes, dereference without &v so v
// stays on the stack (zero allocs).
// Interface T holding a pointer: unwrapped to the pointer path, so passing
// a *S through `any` costs the same as passing it typed.
// Value T:   dispatches to marshalSlow in a separate function so that its
// &v does not poison the pointer path's escape analysis.
func Marshal[T any](v T, opts ...MarshalOption) ([]byte, error) {
	rt := reflect.TypeFor[T]()
	switch rt.Kind() {
	case reflect.Pointer:
		elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if elemPtr == nil {
			return []byte("null"), nil
		}
		return marshalPtr(uintptr(gort.TypePtr(rt)), rt, elemPtr, opts)
	case reflect.Interface:
		var dyn unsafe.Pointer
		if rt.NumMethod() == 0 {
			dyn = gort.EfaceRType(unsafe.Pointer(&v))
		} else {
			dyn = gort.IfaceConcreteRType(unsafe.Pointer(&v))
		}
		if dyn != nil {
			if drt := gort.TypeFromRType(dyn); drt.Kind() == reflect.Pointer {
				// Pointer-shaped dynamic types are stored directly in the data word.
				elemPtr := (*gort.GoIface)(unsafe.Pointer(&v)).Data
				if elemPtr == nil {
					return []byte("null"), nil
				}
				return marshalPtr(uintptr(dyn), drt, elemPtr, opts)
			}
		}
	}
	return marshalSlow(v, rt, opts)
}

func marshalPtr(rtp uintptr, rt reflect.Type, elemPtr unsafe.Pointer, opts []MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.applyOptions(vopt.Join(opts...))
	return es.marshalWith(encElemTypeInfoOf(rtp, rt), elemPtr)
}

func marshalSlow[T any](v T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	return marshalSlowPtr(&v, rt, opts)
}

func marshalSlowPtr[T any](v *T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.applyOptions(vopt.Join(opts...))

	ti := EncTypeInfoOf(rt)
	return es.marshalWith(ti, unsafe.Pointer(v))
}

func (es *encodeState) marshalWith(ti *EncTypeInfo, ptr unsafe.Pointer) ([]byte, error) {
	// Drain the BufSize hand-off into a local: it is a per-call directive,
	// not encode state, so it lives on es only long enough to cross from the
	// Options application to here. Zeroing it now means release needs no reset.
	bufSize := es.bufSize
	es.bufSize = 0

	hint := int(ti.AdaptiveHint.Load())
	if hint == 0 {
		hint = encodingSizeHint(ti, ptr)
		ti.AdaptiveHint.Store(int64(hint))
	}

	if bufSize > 0 {
		if cap(es.buf) < bufSize {
			es.buf = gort.MakeDirtyBytes(0, bufSize)
		}
	} else {
		es.growBuf(hint)
	}

	if err := es.encodeTop(ti, ptr); err != nil {
		return nil, err
	}

	n := len(es.buf)
	adapted := n + n/32 // +3% headroom
	if adapted > hint {
		ti.AdaptiveHint.Store(int64(adapted))
	}

	if bufSize > 0 {
		// Tight copy: allocate exactly n bytes for the caller;
		// es.buf keeps its full capacity for pool reuse.
		result := make([]byte, n)
		copy(result, es.buf)
		es.buf = es.buf[:0]
		return result, nil
	}

	result := es.buf[:n:n] // cap=n prevents caller append from corrupting pool buffer
	es.buf = es.buf[n:]
	return result, nil
}

func MarshalIndent[T any](v T, prefix, indent string, opts ...MarshalOption) ([]byte, error) {
	return Marshal(v, append(opts, vopt.Indent(prefix, indent))...)
}

func AppendMarshal[T any](dst []byte, v T, opts ...MarshalOption) ([]byte, error) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Pointer {
		return appendMarshalSlow(dst, v, rt, opts)
	}

	elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
	if elemPtr == nil {
		return append(dst, "null"...), nil
	}

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.applyOptions(vopt.Join(opts...))

	es.buf = dst
	rtp := uintptr(gort.TypePtr(rt))
	ti := encElemTypeInfoOf(rtp, rt)

	if err := es.encodeTop(ti, elemPtr); err != nil {
		es.buf = nil
		return dst, err
	}

	result := es.buf
	es.buf = nil
	return result, nil
}

func appendMarshalSlow[T any](dst []byte, v T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	return appendMarshalSlowPtr(dst, &v, rt, opts)
}

func appendMarshalSlowPtr[T any](dst []byte, v *T, rt reflect.Type, opts []MarshalOption) ([]byte, error) {
	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.applyOptions(vopt.Join(opts...))

	es.buf = dst
	ti := EncTypeInfoOf(rt)

	if err := es.encodeTop(ti, unsafe.Pointer(v)); err != nil {
		es.buf = nil
		return dst, err
	}

	result := es.buf
	es.buf = nil
	return result, nil
}
