package venc

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/velox-io/json/internal/jsonfmt"
	"github.com/velox-io/json/typ"
)

// formatEncodeFn returns the encoder for a field of type ti whose `format`
// tag option is f. The format applies f.Ptrs dereferences below the field;
// a nil pointer on the way encodes as null.
//
// The pointee types are resolved per call rather than here: a recursive
// field type may still be under construction while its struct is built.
func formatEncodeFn(f *jsonfmt.Format, ti *EncTypeInfo) EncodeFn {
	return func(es *encodeState, ptr unsafe.Pointer) error {
		vt := ti
		for range f.Ptrs {
			if ptr = *(*unsafe.Pointer)(ptr); ptr == nil {
				es.buf = append(es.buf, litNull...)
				return nil
			}
			vt = vt.ResolvePointer().ElemType
		}
		return es.encodeFormatted(f, vt, ptr)
	}
}

// encodeFormatted writes the value of type ti at ptr in the representation f
// selects.
func (es *encodeState) encodeFormatted(f *jsonfmt.Format, ti *EncTypeInfo, ptr unsafe.Pointer) error {
	switch f.Kind {
	case jsonfmt.NilAsNull, jsonfmt.NilAsEmpty:
		if *(*unsafe.Pointer)(ptr) != nil { // slice data or map header
			return ti.Encode(es, ptr)
		}
		switch {
		case f.Kind == jsonfmt.NilAsNull:
			es.buf = append(es.buf, litNull...)
		case ti.Kind == typ.KindMap:
			es.buf = append(es.buf, litEmpty...)
		default:
			es.buf = append(es.buf, litArr...)
		}
		return nil

	case jsonfmt.Base64, jsonfmt.Base64URL, jsonfmt.Base32, jsonfmt.Base32Hex, jsonfmt.Base16:
		b, ok := formattedBytes(ti, ptr)
		if !ok {
			es.buf = append(es.buf, litNull...)
			return nil
		}
		es.buf = append(es.buf, '"')
		es.buf = f.AppendBytes(es.buf, b)
		es.buf = append(es.buf, '"')
		return nil

	case jsonfmt.ByteArray:
		b, ok := formattedBytes(ti, ptr)
		if !ok {
			es.buf = append(es.buf, litNull...)
			return nil
		}
		return es.encodeByteElems(ti, b)

	case jsonfmt.NonFinite:
		var v float64
		if ti.Kind == typ.KindFloat32 {
			v = float64(*(*float32)(ptr))
		} else {
			v = *(*float64)(ptr)
		}
		if name := jsonfmt.NonFiniteName(v); name != "" {
			es.buf = append(es.buf, '"')
			es.buf = append(es.buf, name...)
			es.buf = append(es.buf, '"')
			return nil
		}
		es.quoteIf(f.Quoted)
		if ti.Kind == typ.KindFloat32 {
			es.appendJSONFloat32(v)
		} else {
			es.appendJSONFloat64(v)
		}
		es.quoteIf(f.Quoted)
		return nil

	case jsonfmt.TimeLayout:
		// A layout can hold any text, so its output is escaped like any string.
		var scratch [64]byte
		text, _ := f.AppendTime(scratch[:0], *(*time.Time)(ptr))
		es.encodeString(unsafeString(text))
		return nil

	case jsonfmt.TimeRFC3339, jsonfmt.TimeUnix:
		// These representations, like the duration ones below, are ASCII
		// digits, signs, and separators (and "µs"), which no escaping mode
		// changes.
		quoted := f.InString()
		es.quoteIf(quoted)
		t := *(*time.Time)(ptr)
		buf, err := f.AppendTime(es.buf, t)
		if err != nil {
			return &UnsupportedValueError{Str: fmt.Sprintf("time %v has no RFC 3339 form: %v", t, err)}
		}
		es.buf = buf
		es.quoteIf(quoted)
		return nil

	case jsonfmt.DurationUnits, jsonfmt.DurationISO8601, jsonfmt.DurationNumber:
		quoted := f.InString()
		es.quoteIf(quoted)
		es.buf = f.AppendDuration(es.buf, *(*time.Duration)(ptr))
		es.quoteIf(quoted)
		return nil
	}
	return fmt.Errorf("venc: format kind %d has no encoder", f.Kind)
}

func (es *encodeState) quoteIf(quoted bool) {
	if quoted {
		es.buf = append(es.buf, '"')
	}
}

// formattedBytes returns the bytes of a byte slice or array of type ti at ptr,
// or false for a nil slice, which encodes as null.
func formattedBytes(ti *EncTypeInfo, ptr unsafe.Pointer) ([]byte, bool) {
	if ti.Kind == typ.KindArray {
		return unsafe.Slice((*byte)(ptr), ti.ResolveArray().ArrayLen), true
	}
	sh := (*SliceHeader)(ptr)
	if sh.Data == nil {
		return nil, false
	}
	return unsafe.Slice((*byte)(sh.Data), sh.Len), true
}

// encodeByteElems writes b as a JSON array whose elements encode as the byte
// slice or array type ti's element type does: a plain byte as a number, a
// named byte type through its own methods if it has them.
func (es *encodeState) encodeByteElems(ti *EncTypeInfo, b []byte) error {
	var elemTI *EncTypeInfo
	if ti.Kind == typ.KindArray {
		elemTI = ti.ResolveArray().ElemType
	} else {
		elemTI = ti.ResolveSlice().ElemType
	}
	if len(b) == 0 {
		es.buf = append(es.buf, litArr...)
		return nil
	}
	indent := es.indentString != ""
	es.buf = append(es.buf, '[')
	if indent {
		es.indentDepth++
	}
	for i := range b {
		if i > 0 {
			es.buf = append(es.buf, ',')
		}
		if indent {
			es.appendNewlineIndent()
		}
		if err := elemTI.Encode(es, unsafe.Pointer(&b[i])); err != nil {
			return err
		}
	}
	if indent {
		es.indentDepth--
		es.appendNewlineIndent()
	}
	es.buf = append(es.buf, ']')
	return nil
}
