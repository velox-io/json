package venc

import (
	"unsafe"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
)

// The tape walk re-serializes a value.Value (tape-backed parsed JSON) directly
// into es.buf. It mirrors value's own serializer but is encodeState-aware:
// strings use the mandatory escape set only (a Value is pre-parsed JSON whose
// MarshalJSON output is never re-escaped by stdlib, so optional modes like
// HTML escaping must not apply), and doubles go through the encoder's float
// policy so a Value number and a float64 field render identically.

// appendTapeValue writes the JSON form of the element at the Value's cursor.
// A zero Value writes null.
func (es *encodeState) appendTapeValue(v *value.Value) error {
	desc := valueabi.Load(unsafe.Pointer(v))
	if !desc.HasTape() {
		es.buf = append(es.buf, litNull...)
		return nil
	}
	root, _ := desc.Extent()
	es.appendTapeElem(&desc, root)
	return nil
}

// appendTapeSpread writes the members of the object at the Value's cursor
// inline: keys and values with separators, but no enclosing braces. The
// caller owns the comma state: the first emitted member consumes *first, and
// an empty or zero Value leaves it untouched. A non-object Value is a misuse
// of a decode-side construct and reports an error.
func (es *encodeState) appendTapeSpread(v *value.Value, first *bool) error {
	desc := valueabi.Load(unsafe.Pointer(v))
	if !desc.HasTape() {
		return nil
	}
	root, _ := desc.Extent()
	if desc.TagAt(root) != valueabi.TagObjBeg {
		return &UnsupportedValueError{Str: "spread of a non-object value.Value"}
	}
	end := desc.ContainerEnd(root)
	cur := desc.SkipSeams(root + 1)
	for cur < end {
		if !*first {
			es.buf = append(es.buf, ',')
		}
		*first = false
		if es.indentString != "" {
			es.appendNewlineIndent()
		}
		es.appendTapeString(&desc, cur)
		if es.indentString != "" {
			es.buf = append(es.buf, ':', ' ')
		} else {
			es.buf = append(es.buf, ':')
		}
		es.appendTapeElem(&desc, cur+1)
		cur = desc.Skip(cur + 1)
	}
	return nil
}

func (es *encodeState) appendTapeElem(desc *valueabi.Descriptor, idx int) {
	switch desc.TagAt(idx) {
	case valueabi.TagNull:
		es.buf = append(es.buf, litNull...)
	case valueabi.TagTrue:
		es.buf = append(es.buf, litTrue...)
	case valueabi.TagFalse:
		es.buf = append(es.buf, litFalse...)
	case valueabi.TagInt64:
		es.appendInt64(desc.Int64At(idx))
	case valueabi.TagUint64:
		es.appendUint64(desc.Uint64At(idx))
	case valueabi.TagDouble:
		es.appendJSONFloat64(desc.DoubleAt(idx))
	case valueabi.TagNumRaw:
		// Source text verbatim: no binary form is faithful, re-deriving digits
		// from one is the precision loss the tag exists to avoid.
		es.buf = append(es.buf, desc.NumRawAt(idx)...)
	case valueabi.TagString, valueabi.TagStrRaw, valueabi.TagStrFree:
		es.appendTapeString(desc, idx)
	case valueabi.TagArrBeg:
		es.buf = append(es.buf, '[')
		end := desc.ContainerEnd(idx)
		cur := desc.SkipSeams(idx + 1)
		first := true
		if es.indentString != "" {
			es.indentDepth++
		}
		for cur < end {
			if !first {
				es.buf = append(es.buf, ',')
			}
			first = false
			if es.indentString != "" {
				es.appendNewlineIndent()
			}
			es.appendTapeElem(desc, cur)
			cur = desc.Skip(cur)
		}
		if es.indentString != "" {
			es.indentDepth--
			if !first {
				es.appendNewlineIndent()
			}
		}
		es.buf = append(es.buf, ']')
	case valueabi.TagObjBeg:
		es.buf = append(es.buf, '{')
		end := desc.ContainerEnd(idx)
		cur := desc.SkipSeams(idx + 1)
		first := true
		if es.indentString != "" {
			es.indentDepth++
		}
		for cur < end {
			if !first {
				es.buf = append(es.buf, ',')
			}
			first = false
			if es.indentString != "" {
				es.appendNewlineIndent()
			}
			es.appendTapeString(desc, cur)
			if es.indentString != "" {
				es.buf = append(es.buf, ':', ' ')
			} else {
				es.buf = append(es.buf, ':')
			}
			es.appendTapeElem(desc, cur+1)
			cur = desc.Skip(cur + 1)
		}
		if es.indentString != "" {
			es.indentDepth--
			if !first {
				es.appendNewlineIndent()
			}
		}
		es.buf = append(es.buf, '}')
	}
}

// appendTapeString writes the string at idx. A TagStrRaw body spans src and a
// TagStrFree body is its arena copy; both are backslash-free source text
// (producers only use those tags for escape-free strings), so they round-trip
// verbatim. A TagString body is decoded content and re-escapes with the
// mandatory set.
func (es *encodeState) appendTapeString(desc *valueabi.Descriptor, idx int) {
	if t := desc.TagAt(idx); t == valueabi.TagStrRaw || t == valueabi.TagStrFree {
		es.buf = append(es.buf, '"')
		es.buf = append(es.buf, desc.ScalarStringAt(idx)...)
		es.buf = append(es.buf, '"')
		return
	}
	es.buf = appendEscapedString(es.buf, unsafeString(desc.StringAt(idx)), 0)
}
