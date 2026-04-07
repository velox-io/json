package venc

import (
	"encoding/base64"
	"fmt"
	"math"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

// interp interprets a compiled Blueprint entirely in Go.
// It executes the same bytecode as the C VM but writes directly to es.buf
// via append — no BufCur/BufEnd, no BUF_FULL yield, no mid-encode flush.
// The entire output is buffered; the caller (Encoder.encodePtr / Marshal)
// handles flushing or detaching the result after interp returns.
func (es *encodeState) interp(bp *Blueprint, base unsafe.Pointer) error {
	ops := bp.Ops
	opsLen := int32(len(ops))
	indent := es.indentString != ""
	var (
		pc    int32
		first = true // tracks whether to write comma before next value
		depth int32  // stack depth
	)

	for pc < opsLen {
		hdr := opHdrAt(ops, pc)
		op := hdr.OpType

		switch op {

		// ---- Struct delimiters ----

		case opObjOpen:
			if hdr.KeyLen > 0 {
				es.interpWriteKey(hdr, first, indent)
			} else if !first {
				es.buf = append(es.buf, ',')
				if indent {
					es.appendNewlineIndent()
				}
			}
			es.buf = append(es.buf, '{')
			if indent {
				es.indentDepth++
			}
			first = true
			pc += 8

		case opObjClose:
			if indent {
				es.indentDepth--
				if !first {
					es.appendNewlineIndent()
				}
			}
			es.buf = append(es.buf, '}')
			first = false
			pc += 8

		// ---- Scalar primitives ----

		case opBool:
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			if hdr.KeyLen > 0 {
				es.interpWriteKey(hdr, first, indent)
				first = false
			}
			if *(*bool)(fieldPtr) {
				es.buf = append(es.buf, litTrue...)
			} else {
				es.buf = append(es.buf, litFalse...)
			}
			pc += 8

		case opInt:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendInt64(int64(*(*int)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt8:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendInt64(int64(*(*int8)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt16:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendInt64(int64(*(*int16)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt32:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendInt64(int64(*(*int32)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt64:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendInt64(*(*int64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		case opUint:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendUint64(uint64(*(*uint)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint8:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendUint64(uint64(*(*uint8)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint16:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendUint64(uint64(*(*uint16)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint32:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendUint64(uint64(*(*uint32)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint64:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendUint64(*(*uint64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		case opFloat32:
			es.interpWriteKey(hdr, first, indent)
			first = false
			f := float64(*(*float32)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &UnsupportedValueError{Str: "NaN or Inf float value"}
			}
			es.appendJSONFloat32(f)
			pc += 8

		case opFloat64:
			es.interpWriteKey(hdr, first, indent)
			first = false
			f := *(*float64)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &UnsupportedValueError{Str: "NaN or Inf float value"}
			}
			es.appendJSONFloat64(f)
			pc += 8

		case opString:
			es.interpWriteKey(hdr, first, indent)
			first = false
			s := *(*string)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			es.encodeString(s)
			pc += 8

		// ---- Keyed scalar shortcuts ----

		case opKString:
			es.interpWriteKey(hdr, first, indent)
			first = false
			s := *(*string)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			es.encodeString(s)
			pc += 8

		case opKInt:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendInt64(int64(*(*int)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opKInt64:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendInt64(*(*int64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		case opKQInt:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendQuotedInt64(int64(*(*int)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opKQInt64:
			es.interpWriteKey(hdr, first, indent)
			first = false
			es.appendQuotedInt64(*(*int64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		// ---- Special value types ----

		case opRawMessage:
			es.interpWriteKey(hdr, first, indent)
			first = false
			sh := (*gort.SliceHeader)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if sh.Data == nil || sh.Len == 0 {
				es.buf = append(es.buf, litNull...)
			} else {
				raw := unsafe.Slice((*byte)(sh.Data), sh.Len)
				es.buf = append(es.buf, raw...)
			}
			pc += 8

		case opNumber:
			es.interpWriteKey(hdr, first, indent)
			first = false
			s := *(*string)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if s == "" {
				es.buf = append(es.buf, '0')
			} else {
				es.buf = append(es.buf, s...)
			}
			pc += 8

		case opByteSlice:
			es.interpWriteKey(hdr, first, indent)
			first = false
			sh := (*gort.SliceHeader)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if sh.Data == nil {
				es.buf = append(es.buf, litNull...)
			} else {
				data := unsafe.Slice((*byte)(sh.Data), sh.Len)
				es.buf = append(es.buf, '"')
				encodedLen := base64.StdEncoding.EncodedLen(sh.Len)
				start := len(es.buf)
				es.buf = append(es.buf, make([]byte, encodedLen)...)
				base64.StdEncoding.Encode(es.buf[start:], data)
				es.buf = append(es.buf, '"')
			}
			pc += 8

		case opTime:
			es.interpWriteKey(hdr, first, indent)
			first = false
			// Delegate to fallback — time.Time has custom MarshalJSON
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: interp: opTime at PC=%d with no fallback info", pc)
			}
			fieldPtr := unsafe.Add(base, fb.Offset)
			if err := fb.TI.Encode(es, fieldPtr); err != nil {
				return err
			}
			pc += 8

		// ---- Control flow: omitempty ----

		case opSkipIfZero:
			ext := opExtAt(ops, pc)
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			skip := interpIsZero(fieldPtr, ext.OperandB)
			if skip {
				pc += ext.OperandA // jump forward
			} else {
				pc += 16
			}

		// ---- Control flow: subroutine call/return ----

		case opCall:
			ext := opExtAt(ops, pc)
			if depth >= VJ_MAX_STACK_DEPTH {
				return fmt.Errorf("venc: nesting depth exceeds limit (%d)", depth)
			}
			// Push stack frame: save current base, PC, first flag
			frame := &es.vmCtx.Stack[depth]
			frame.RetBase = base
			// Store return PC and first flag in the union area
			*(*int32)(unsafe.Pointer(&frame.Payload[0])) = pc + 16            // return address
			*(*int32)(unsafe.Pointer(&frame.Payload[4])) = boolToInt32(first) // save first
			depth++

			// Switch base to the field pointed to by FieldOff
			base = unsafe.Add(base, uintptr(hdr.FieldOff))
			// Only reset first=true for struct fields (KeyLen > 0 means it's a field with a key).
			// For direct array/slice elements (KeyLen == 0), preserve the current first flag.
			if hdr.KeyLen > 0 {
				first = true
			}
			pc = ext.OperandA // jump to subroutine entry (absolute offset)

		case opRet:
			if depth == 0 {
				return nil // program termination
			}
			depth--
			frame := &es.vmCtx.Stack[depth]
			base = frame.RetBase
			frame.RetBase = nil                               // clear so pooled encodeState has no dangling ptr
			pc = *(*int32)(unsafe.Pointer(&frame.Payload[0])) // restore return PC
			first = false                                     // after a call, we've written something

		// ---- Control flow: pointer deref ----

		case opPtrDeref:
			ext := opExtAt(ops, pc)
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			p := *(*unsafe.Pointer)(fieldPtr)
			if p == nil {
				// Write key + null, skip deref body
				es.interpWriteKey(hdr, first, indent)
				first = false
				es.buf = append(es.buf, litNull...)
				pc += ext.OperandA // jump past PtrEnd
			} else {
				// Write key before entering deref body (for nested fields)
				// The body will write the value starting with opObjOpen, opSliceBegin, etc.
				if hdr.KeyLen > 0 {
					es.interpWriteKey(hdr, first, indent)
					// After writing key, set first=true so the value starts without comma
					first = true
				}
				// Push frame to save old base
				if depth >= VJ_MAX_STACK_DEPTH {
					return fmt.Errorf("venc: nesting depth exceeds limit (%d)", depth)
				}
				frame := &es.vmCtx.Stack[depth]
				frame.RetBase = base
				// Store original first flag (will be restored after return)
				*(*int32)(unsafe.Pointer(&frame.Payload[4])) = boolToInt32(first)
				depth++
				base = p
				pc += 16 // enter deref body
			}

		case opPtrEnd:
			if depth > 0 {
				depth--
				base = es.vmCtx.Stack[depth].RetBase
				es.vmCtx.Stack[depth].RetBase = nil
				// After exiting pointer deref body, mark that we've written content
				first = false
			}
			pc += 8

		// ---- Slice iteration ----

		case opSliceBegin:
			ext := opExtAt(ops, pc)
			elemSize := uintptr(ext.OperandA)
			bodyLen := ext.OperandB
			sh := (*gort.SliceHeader)(unsafe.Add(base, uintptr(hdr.FieldOff)))

			if hdr.KeyLen > 0 {
				es.interpWriteKey(hdr, first, indent)
				first = false
			}

			if sh.Data == nil {
				es.buf = append(es.buf, litNull...)
				// Jump past the loop body + SliceEnd
				pc += 16 + bodyLen + 16
				continue
			}

			es.buf = append(es.buf, '[')
			if indent {
				es.indentDepth++
			}

			if sh.Len == 0 {
				if indent {
					es.indentDepth--
				}
				es.buf = append(es.buf, ']')
				pc += 16 + bodyLen + 16
				continue
			}

			// Push ITER frame
			if depth >= VJ_MAX_STACK_DEPTH {
				return fmt.Errorf("venc: nesting depth exceeds limit (%d)", depth)
			}
			frame := &es.vmCtx.Stack[depth]
			frame.RetBase = base
			*(*unsafe.Pointer)(unsafe.Pointer(&frame.Payload[0])) = sh.Data
			*(*int64)(unsafe.Pointer(&frame.Payload[8])) = int64(sh.Len)
			*(*int32)(unsafe.Pointer(&frame.Payload[16])) = 0 // idx = 0
			frame.State = int32(elemSize)                     // stash elemSize in State for SliceEnd
			depth++

			base = sh.Data // point to first element
			first = true
			pc += 16 // enter loop body

		case opSliceEnd:
			ext := opExtAt(ops, pc)
			depth--
			frame := &es.vmCtx.Stack[depth]
			idx := *(*int32)(unsafe.Pointer(&frame.Payload[16])) + 1
			count := *(*int64)(unsafe.Pointer(&frame.Payload[8]))
			elemSize := uintptr(frame.State)

			if int64(idx) < count {
				// Continue iteration
				*(*int32)(unsafe.Pointer(&frame.Payload[16])) = idx
				depth++
				data := *(*unsafe.Pointer)(unsafe.Pointer(&frame.Payload[0]))
				base = unsafe.Add(data, uintptr(idx)*elemSize)
				first = false      // subsequent elements get comma
				pc += ext.OperandA // jump back to body start
			} else {
				// End iteration
				base = frame.RetBase
				frame.RetBase = nil
				if indent {
					es.indentDepth--
					es.appendNewlineIndent()
				}
				es.buf = append(es.buf, ']')
				first = false
				pc += 16
			}

		// ---- Array iteration ----

		case opArrayBegin:
			ext := opExtAt(ops, pc)
			packed := uint32(ext.OperandA)
			elemSize := uintptr(packed & 0xFFFF)
			arrayLen := int32(packed >> 16)
			bodyLen := ext.OperandB

			if hdr.KeyLen > 0 {
				es.interpWriteKey(hdr, first, indent)
				first = false
			}

			es.buf = append(es.buf, '[')

			if arrayLen == 0 {
				es.buf = append(es.buf, ']')
				pc += 16 + bodyLen + 16
				continue
			}

			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))

			if depth >= VJ_MAX_STACK_DEPTH {
				return fmt.Errorf("venc: nesting depth exceeds limit (%d)", depth)
			}
			frame := &es.vmCtx.Stack[depth]
			frame.RetBase = base
			*(*unsafe.Pointer)(unsafe.Pointer(&frame.Payload[0])) = fieldPtr
			*(*int64)(unsafe.Pointer(&frame.Payload[8])) = int64(arrayLen)
			*(*int32)(unsafe.Pointer(&frame.Payload[16])) = 0
			frame.State = int32(elemSize)
			depth++

			base = fieldPtr
			first = true
			pc += 16

		// ---- Sequence (single-instruction loops) ----

		case opSeqFloat64, opSeqInt, opSeqInt64, opSeqString:
			if err := es.interpSeq(hdr, ops, pc, base, first, op); err != nil {
				return err
			}
			first = false
			pc += 16

		// ---- Map ----

		case opMap:
			// Delegate entire map to Go
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: interp: opMap at PC=%d with no fallback info", pc)
			}
			if hdr.KeyLen > 0 {
				es.interpWriteKey(hdr, first, indent)
				first = false
			}
			mapPtr := unsafe.Add(base, fb.Offset)
			if err := fb.TI.Encode(es, mapPtr); err != nil {
				return err
			}
			pc += 8

		case opMapStrStr, opMapStrInt, opMapStrInt64:
			es.interpWriteKey(hdr, first, indent)
			first = false
			mapPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			// Try fallback entry first (available when compiled as struct field).
			if fb, ok := bp.Fallbacks[int(pc)]; ok {
				if err := fb.TI.Encode(es, unsafe.Add(base, fb.Offset)); err != nil {
					return err
				}
			} else {
				// Standalone map op: read map pointer and encode via type-specific fast path.
				mp := *(*unsafe.Pointer)(mapPtr)
				if mp == nil {
					es.buf = append(es.buf, litNull...)
				} else {
					switch op {
					case opMapStrStr:
						if err := es.encodeMapStringString(mapPtr); err != nil {
							return err
						}
					default:
						// opMapStrInt, opMapStrInt64: delegate to generic map encoder.
						// The EncTypeInfo isn't available here, but encodeAnyMap handles
						// map[string]any. For typed maps, fall back to reflect.
						return fmt.Errorf("venc: interp: map opcode %d at PC=%d requires fallback entry", op, pc)
					}
				}
			}
			pc += 8

		case opMapStrIter:
			ext := opExtAt(ops, pc)
			bodyLen := ext.OperandB
			// For Go VM, we don't do Swiss map iteration — delegate the whole map
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: interp: opMapStrIter at PC=%d with no fallback", pc)
			}
			es.interpWriteKey(hdr, first, indent)
			first = false
			mapPtr := unsafe.Add(base, fb.Offset)
			if err := fb.TI.Encode(es, mapPtr); err != nil {
				return err
			}
			// Skip past body + MapStrIterEnd
			pc += 16 + bodyLen + 16

		case opMapStrIterEnd:
			// Should not reach here if opMapStrIter skipped properly
			return fmt.Errorf("venc: interp: unexpected opMapStrIterEnd at PC=%d", pc)

		// ---- Interface ----

		case opInterface:
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			if hdr.KeyLen > 0 {
				es.interpWriteKey(hdr, first, indent)
			} else if !first {
				es.buf = append(es.buf, ',')
			}
			if err := es.encodeAnyIface(fieldPtr); err != nil {
				return err
			}
			first = false
			pc += 8

		// ---- Fallback (custom marshaler, ,string, etc.) ----

		case opFallback:
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: interp: opFallback at PC=%d with no fallback info", pc)
			}
			fieldPtr := unsafe.Add(base, fb.Offset)

			if fb.TagFlags&EncTagFlagOmitEmpty != 0 && fb.IsZeroFn != nil {
				if fb.IsZeroFn(fieldPtr) {
					pc += 8
					continue
				}
			}

			if !first {
				es.buf = append(es.buf, ',')
			}
			if hdr.KeyLen > 0 {
				es.buf = append(es.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
			} else if len(fb.KeyBytes) > 0 {
				es.buf = append(es.buf, fb.KeyBytes...)
			}

			if fb.TagFlags&EncTagFlagQuoted != 0 {
				if err := es.encodeValueQuoted(fb.TI, fieldPtr); err != nil {
					return err
				}
			} else {
				if err := fb.TI.Encode(es, fieldPtr); err != nil {
					return err
				}
			}
			first = false
			pc += 8

		default:
			return fmt.Errorf("venc: interp: unknown opcode %d at PC=%d", op, pc)
		}
	}

	return nil
}

// interpWriteKey writes comma (if not first) + indent + key from the pool + key-space.
func (es *encodeState) interpWriteKey(hdr *VjOpHdr, first bool, indent bool) {
	if !first {
		es.buf = append(es.buf, ',')
	}
	if indent {
		es.appendNewlineIndent()
	}
	if hdr.KeyLen > 0 {
		es.buf = append(es.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
		if indent {
			es.buf = append(es.buf, ' ')
		}
	}
}

// interpIsZero implements the zero-check for opSkipIfZero.
// tag is the ZeroCheckTag from OperandB.
func interpIsZero(ptr unsafe.Pointer, tag int32) bool {
	switch uint16(tag) {
	case opBool:
		return !*(*bool)(ptr)
	case opInt:
		return *(*int)(ptr) == 0
	case opInt8:
		return *(*int8)(ptr) == 0
	case opInt16:
		return *(*int16)(ptr) == 0
	case opInt32:
		return *(*int32)(ptr) == 0
	case opInt64:
		return *(*int64)(ptr) == 0
	case opUint:
		return *(*uint)(ptr) == 0
	case opUint8:
		return *(*uint8)(ptr) == 0
	case opUint16:
		return *(*uint16)(ptr) == 0
	case opUint32:
		return *(*uint32)(ptr) == 0
	case opUint64:
		return *(*uint64)(ptr) == 0
	case opFloat32:
		return *(*float32)(ptr) == 0
	case opFloat64:
		return *(*float64)(ptr) == 0
	case opString:
		return *(*string)(ptr) == ""
	default:
		// For complex types, use the tag as ElemTypeKind.
		// Matches C VM's vj_is_zero semantics.
		switch typ.ElemTypeKind(tag) {
		case typ.KindSlice:
			// Empty for omitempty when len == 0 (not just nil).
			return (*gort.SliceHeader)(ptr).Len == 0
		case typ.KindMap:
			mp := *(*unsafe.Pointer)(ptr)
			if mp == nil {
				return true
			}
			return gort.MapLen(mp) == 0
		case typ.KindPointer:
			return *(*unsafe.Pointer)(ptr) == nil
		case typ.KindAny, typ.KindIface:
			// nil interface = zero value (eface/iface type ptr == NULL).
			return *(*unsafe.Pointer)(ptr) == nil
		case typ.KindNumber:
			// json.Number is a string — zero means empty string.
			return *(*string)(ptr) == ""
		case typ.KindRawMessage:
			// json.RawMessage is []byte — zero means nil or len==0.
			return (*gort.SliceHeader)(ptr).Len == 0
		}
		return false
	}
}

// interpSeq handles single-instruction sequence ops (opSeqFloat64, opSeqInt, opSeqInt64, opSeqString).
func (es *encodeState) interpSeq(hdr *VjOpHdr, ops []byte, pc int32, base unsafe.Pointer, first bool, op uint16) error {
	ext := opExtAt(ops, pc)
	fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
	doIndent := es.indentString != ""

	if hdr.KeyLen > 0 {
		es.interpWriteKey(hdr, first, doIndent)
	}

	packed := uint32(ext.OperandA)
	var data unsafe.Pointer
	var count int

	if packed == 0 {
		// Slice
		sh := (*gort.SliceHeader)(fieldPtr)
		if sh.Data == nil {
			es.buf = append(es.buf, litNull...)
			return nil
		}
		data = sh.Data
		count = sh.Len
	} else {
		// Array: packed = (elemSize & 0xFFFF) | (arrayLen << 16)
		count = int(packed >> 16)
		data = fieldPtr
	}

	es.buf = append(es.buf, '[')
	if doIndent && count > 0 {
		es.indentDepth++
	}
	for i := 0; i < count; i++ {
		if i > 0 {
			es.buf = append(es.buf, ',')
		}
		if doIndent {
			es.appendNewlineIndent()
		}
		switch op {
		case opSeqFloat64:
			f := *(*float64)(unsafe.Add(data, uintptr(i)*8))
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &UnsupportedValueError{Str: "NaN or Inf float value"}
			}
			es.appendJSONFloat64(f)
		case opSeqInt:
			es.appendInt64(int64(*(*int)(unsafe.Add(data, uintptr(i)*unsafe.Sizeof(int(0))))))
		case opSeqInt64:
			es.appendInt64(*(*int64)(unsafe.Add(data, uintptr(i)*8)))
		case opSeqString:
			s := *(*string)(unsafe.Add(data, uintptr(i)*unsafe.Sizeof("")))
			es.encodeString(s)
		}
	}
	if doIndent && count > 0 {
		es.indentDepth--
		es.appendNewlineIndent()
	}
	es.buf = append(es.buf, ']')
	return nil
}

func boolToInt32(b bool) int32 {
	return int32(*(*uint8)(unsafe.Pointer(&b)))
}
