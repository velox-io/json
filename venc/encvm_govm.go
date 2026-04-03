package venc

import (
	"encoding/base64"
	"fmt"
	"math"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

// goVM executes a compiled Blueprint entirely in Go.
// It interprets the same bytecode as the C VM but writes directly to m.buf
// via append — no BufCur/BufEnd, no BUF_FULL yield.
// Supports both compact and indented output modes.
func (m *marshaler) goVM(bp *Blueprint, base unsafe.Pointer) error {
	ops := bp.Ops
	opsLen := int32(len(ops))
	indent := m.indent != ""
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
				m.goVMWriteKey(hdr, first, indent)
			} else if !first {
				m.buf = append(m.buf, ',')
				if indent {
					m.appendNewlineIndent()
				}
			}
			m.buf = append(m.buf, '{')
			if indent {
				m.indentDepth++
			}
			first = true
			pc += 8

		case opObjClose:
			if indent {
				m.indentDepth--
				if !first {
					m.appendNewlineIndent()
				}
			}
			m.buf = append(m.buf, '}')
			first = false
			pc += 8

		// ---- Scalar primitives ----

		case opBool:
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			if hdr.KeyLen > 0 {
				m.goVMWriteKey(hdr, first, indent)
				first = false
			}
			if *(*bool)(fieldPtr) {
				m.buf = append(m.buf, litTrue...)
			} else {
				m.buf = append(m.buf, litFalse...)
			}
			pc += 8

		case opInt:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendInt64(int64(*(*int)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt8:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendInt64(int64(*(*int8)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt16:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendInt64(int64(*(*int16)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt32:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendInt64(int64(*(*int32)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opInt64:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendInt64(*(*int64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		case opUint:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendUint64(uint64(*(*uint)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint8:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendUint64(uint64(*(*uint8)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint16:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendUint64(uint64(*(*uint16)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint32:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendUint64(uint64(*(*uint32)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opUint64:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendUint64(*(*uint64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		case opFloat32:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			f := float64(*(*float32)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &UnsupportedValueError{Str: "NaN or Inf float value"}
			}
			m.appendJSONFloat32(f)
			pc += 8

		case opFloat64:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			f := *(*float64)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &UnsupportedValueError{Str: "NaN or Inf float value"}
			}
			m.appendJSONFloat64(f)
			pc += 8

		case opString:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			s := *(*string)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			m.encodeString(s)
			pc += 8

		// ---- Keyed scalar shortcuts ----

		case opKString:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			s := *(*string)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			m.encodeString(s)
			pc += 8

		case opKInt:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendInt64(int64(*(*int)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opKInt64:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendInt64(*(*int64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		case opKQInt:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendQuotedInt64(int64(*(*int)(unsafe.Add(base, uintptr(hdr.FieldOff)))))
			pc += 8

		case opKQInt64:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			m.appendQuotedInt64(*(*int64)(unsafe.Add(base, uintptr(hdr.FieldOff))))
			pc += 8

		// ---- Special value types ----

		case opRawMessage:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			sh := (*gort.SliceHeader)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if sh.Data == nil || sh.Len == 0 {
				m.buf = append(m.buf, litNull...)
			} else {
				raw := unsafe.Slice((*byte)(sh.Data), sh.Len)
				m.buf = append(m.buf, raw...)
			}
			pc += 8

		case opNumber:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			s := *(*string)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if s == "" {
				m.buf = append(m.buf, '0')
			} else {
				m.buf = append(m.buf, s...)
			}
			pc += 8

		case opByteSlice:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			sh := (*gort.SliceHeader)(unsafe.Add(base, uintptr(hdr.FieldOff)))
			if sh.Data == nil {
				m.buf = append(m.buf, litNull...)
			} else {
				data := unsafe.Slice((*byte)(sh.Data), sh.Len)
				m.buf = append(m.buf, '"')
				encodedLen := base64.StdEncoding.EncodedLen(sh.Len)
				start := len(m.buf)
				m.buf = append(m.buf, make([]byte, encodedLen)...)
				base64.StdEncoding.Encode(m.buf[start:], data)
				m.buf = append(m.buf, '"')
			}
			pc += 8

		case opTime:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			// Delegate to fallback — time.Time has custom MarshalJSON
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: goVM: opTime at PC=%d with no fallback info", pc)
			}
			fieldPtr := unsafe.Add(base, fb.Offset)
			if err := m.encodeValue(fb.TI, fieldPtr); err != nil {
				return err
			}
			pc += 8

		// ---- Control flow: omitempty ----

		case opSkipIfZero:
			ext := opExtAt(ops, pc)
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			skip := goVMIsZero(fieldPtr, ext.OperandB, hdr)
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
			frame := &m.vmCtx.Stack[depth]
			frame.RetBase = base
			// Store return PC and first flag in the union area
			*(*int32)(unsafe.Pointer(&frame._union[0])) = pc + 16 // return address
			*(*int32)(unsafe.Pointer(&frame._union[4])) = boolToInt32(first) // save first
			depth++

			// Switch base to the field pointed to by FieldOff
			base = unsafe.Add(base, uintptr(hdr.FieldOff))
			first = true
			pc = ext.OperandA // jump to subroutine entry (absolute offset)

		case opRet:
			if depth == 0 {
				return nil // program termination
			}
			depth--
			frame := &m.vmCtx.Stack[depth]
			base = frame.RetBase
			pc = *(*int32)(unsafe.Pointer(&frame._union[0]))       // restore return PC
			first = *(*int32)(unsafe.Pointer(&frame._union[4])) != 0 // restore first flag
			first = false // after a call, we've written something

		// ---- Control flow: pointer deref ----

		case opPtrDeref:
			ext := opExtAt(ops, pc)
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			p := *(*unsafe.Pointer)(fieldPtr)
			if p == nil {
				// Write key + null, skip deref body
				m.goVMWriteKey(hdr, first, indent)
				first = false
				m.buf = append(m.buf, litNull...)
				pc += ext.OperandA // jump past PtrEnd
			} else {
				// Push frame to save old base
				if depth >= VJ_MAX_STACK_DEPTH {
					return fmt.Errorf("venc: nesting depth exceeds limit (%d)", depth)
				}
				frame := &m.vmCtx.Stack[depth]
				frame.RetBase = base
				depth++
				base = p
				pc += 16 // enter deref body
			}

		case opPtrEnd:
			if depth > 0 {
				depth--
				base = m.vmCtx.Stack[depth].RetBase
			}
			pc += 8

		// ---- Slice iteration ----

		case opSliceBegin:
			ext := opExtAt(ops, pc)
			elemSize := uintptr(ext.OperandA)
			bodyLen := ext.OperandB
			sh := (*gort.SliceHeader)(unsafe.Add(base, uintptr(hdr.FieldOff)))

			if hdr.KeyLen > 0 {
				m.goVMWriteKey(hdr, first, indent)
				first = false
			}

			if sh.Data == nil {
				m.buf = append(m.buf, litNull...)
				// Jump past the loop body + SliceEnd
				pc += 16 + bodyLen + 16
				continue
			}

			m.buf = append(m.buf, '[')
			if indent {
				m.indentDepth++
			}

			if sh.Len == 0 {
				if indent {
					m.indentDepth--
				}
				m.buf = append(m.buf, ']')
				pc += 16 + bodyLen + 16
				continue
			}

			// Push ITER frame
			if depth >= VJ_MAX_STACK_DEPTH {
				return fmt.Errorf("venc: nesting depth exceeds limit (%d)", depth)
			}
			frame := &m.vmCtx.Stack[depth]
			frame.RetBase = base
			*(*unsafe.Pointer)(unsafe.Pointer(&frame._union[0])) = sh.Data
			*(*int64)(unsafe.Pointer(&frame._union[8])) = int64(sh.Len)
			*(*int32)(unsafe.Pointer(&frame._union[16])) = 0 // idx = 0
			frame.State = int32(elemSize) // stash elemSize in State for SliceEnd
			depth++

			base = sh.Data // point to first element
			first = true
			pc += 16 // enter loop body

		case opSliceEnd:
			ext := opExtAt(ops, pc)
			depth--
			frame := &m.vmCtx.Stack[depth]
			idx := *(*int32)(unsafe.Pointer(&frame._union[16])) + 1
			count := *(*int64)(unsafe.Pointer(&frame._union[8]))
			elemSize := uintptr(frame.State)

			if int64(idx) < count {
				// Continue iteration
				*(*int32)(unsafe.Pointer(&frame._union[16])) = idx
				depth++
				data := *(*unsafe.Pointer)(unsafe.Pointer(&frame._union[0]))
				base = unsafe.Add(data, uintptr(idx)*elemSize)
				first = false // subsequent elements get comma
				pc += ext.OperandA // jump back to body start
			} else {
				// End iteration
				base = frame.RetBase
				if indent {
					m.indentDepth--
					m.appendNewlineIndent()
				}
				m.buf = append(m.buf, ']')
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
				m.goVMWriteKey(hdr, first, indent)
				first = false
			}

			m.buf = append(m.buf, '[')

			if arrayLen == 0 {
				m.buf = append(m.buf, ']')
				pc += 16 + bodyLen + 16
				continue
			}

			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))

			if depth >= VJ_MAX_STACK_DEPTH {
				return fmt.Errorf("venc: nesting depth exceeds limit (%d)", depth)
			}
			frame := &m.vmCtx.Stack[depth]
			frame.RetBase = base
			*(*unsafe.Pointer)(unsafe.Pointer(&frame._union[0])) = fieldPtr
			*(*int64)(unsafe.Pointer(&frame._union[8])) = int64(arrayLen)
			*(*int32)(unsafe.Pointer(&frame._union[16])) = 0
			frame.State = int32(elemSize)
			depth++

			base = fieldPtr
			first = true
			pc += 16

		// ---- Sequence (single-instruction loops) ----

		case opSeqFloat64, opSeqInt, opSeqInt64, opSeqString:
			if err := m.goVMSeq(hdr, ops, pc, base, first, op); err != nil {
				return err
			}
			first = false
			pc += 16

		// ---- Map ----

		case opMap:
			// Delegate entire map to Go
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: goVM: opMap at PC=%d with no fallback info", pc)
			}
			if hdr.KeyLen > 0 {
				m.goVMWriteKey(hdr, first, indent)
				first = false
			}
			mapInfo := fb.TI.ResolveMap()
			mapPtr := unsafe.Add(base, fb.Offset)
			if err := m.encodeMap(mapInfo, mapPtr); err != nil {
				return err
			}
			pc += 8

		case opMapStrStr, opMapStrInt, opMapStrInt64:
			m.goVMWriteKey(hdr, first, indent)
			first = false
			// Delegate to the Go map encoder via the field's EncodeFn
			fb, ok := bp.Fallbacks[int(pc)]
			if ok {
				fieldPtr := unsafe.Add(base, fb.Offset)
				if err := m.encodeValue(fb.TI, fieldPtr); err != nil {
					return err
				}
			} else {
				// No fallback? Try encodeMapFallback via the map type info.
				return fmt.Errorf("venc: goVM: map opcode %d at PC=%d with no fallback", op, pc)
			}
			pc += 8

		case opMapStrIter:
			ext := opExtAt(ops, pc)
			bodyLen := ext.OperandB
			// For Go VM, we don't do Swiss map iteration — delegate the whole map
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: goVM: opMapStrIter at PC=%d with no fallback", pc)
			}
			m.goVMWriteKey(hdr, first, indent)
			first = false
			mapInfo := fb.TI.ResolveMap()
			mapPtr := unsafe.Add(base, fb.Offset)
			if err := m.encodeMap(mapInfo, mapPtr); err != nil {
				return err
			}
			// Skip past body + MapStrIterEnd
			pc += 16 + bodyLen + 16

		case opMapStrIterEnd:
			// Should not reach here if opMapStrIter skipped properly
			return fmt.Errorf("venc: goVM: unexpected opMapStrIterEnd at PC=%d", pc)

		// ---- Interface ----

		case opInterface:
			fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
			if hdr.KeyLen > 0 {
				m.goVMWriteKey(hdr, first, indent)
				first = false
			} else if !first {
				m.buf = append(m.buf, ',')
			}
			if err := m.encodeAnyIface(fieldPtr); err != nil {
				return err
			}
			first = false
			pc += 8

		// ---- Fallback (custom marshaler, ,string, etc.) ----

		case opFallback:
			fb, ok := bp.Fallbacks[int(pc)]
			if !ok {
				return fmt.Errorf("venc: goVM: opFallback at PC=%d with no fallback info", pc)
			}
			fieldPtr := unsafe.Add(base, fb.Offset)

			if fb.TI.TagFlags&EncTagFlagOmitEmpty != 0 && fb.TI.IsZeroFn != nil {
				if fb.TI.IsZeroFn(fieldPtr) {
					pc += 8
					continue
				}
			}

			if !first {
				m.buf = append(m.buf, ',')
			}
			if hdr.KeyLen > 0 {
				m.buf = append(m.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
			} else if len(fb.TI.KeyBytes) > 0 {
				m.buf = append(m.buf, fb.TI.KeyBytes...)
			}

			if fb.TI.TagFlags&EncTagFlagQuoted != 0 {
				if err := m.encodeValueQuoted(fb.TI, fieldPtr); err != nil {
					return err
				}
			} else {
				if err := m.encodeValue(fb.TI, fieldPtr); err != nil {
					return err
				}
			}
			first = false
			pc += 8

		default:
			return fmt.Errorf("venc: goVM: unknown opcode %d at PC=%d", op, pc)
		}
	}

	return nil
}

// goVMWriteKey writes comma (if not first) + indent + key from the pool + key-space.
func (m *marshaler) goVMWriteKey(hdr *VjOpHdr, first bool, indent bool) {
	if !first {
		m.buf = append(m.buf, ',')
	}
	if indent {
		m.appendNewlineIndent()
	}
	if hdr.KeyLen > 0 {
		m.buf = append(m.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
		if indent {
			m.buf = append(m.buf, ' ')
		}
	}
}

// goVMIsZero implements the zero-check for opSkipIfZero.
// tag is the ZeroCheckTag from OperandB.
func goVMIsZero(ptr unsafe.Pointer, tag int32, hdr *VjOpHdr) bool {
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
		// For complex types (slice, map, pointer), check if nil/empty
		// Using the tag as KindSlice etc.
		switch typ.ElemTypeKind(tag) {
		case typ.KindSlice:
			return (*gort.SliceHeader)(ptr).Data == nil
		case typ.KindMap:
			return *(*unsafe.Pointer)(ptr) == nil
		case typ.KindPointer:
			return *(*unsafe.Pointer)(ptr) == nil
		}
		return false
	}
}

// goVMSeq handles single-instruction sequence ops (opSeqFloat64, opSeqInt, opSeqInt64, opSeqString).
func (m *marshaler) goVMSeq(hdr *VjOpHdr, ops []byte, pc int32, base unsafe.Pointer, first bool, op uint16) error {
	ext := opExtAt(ops, pc)
	fieldPtr := unsafe.Add(base, uintptr(hdr.FieldOff))
	doIndent := m.indent != ""

	if hdr.KeyLen > 0 {
		m.goVMWriteKey(hdr, first, doIndent)
	}

	packed := uint32(ext.OperandA)
	var data unsafe.Pointer
	var count int

	if packed == 0 {
		// Slice
		sh := (*gort.SliceHeader)(fieldPtr)
		if sh.Data == nil {
			m.buf = append(m.buf, litNull...)
			return nil
		}
		data = sh.Data
		count = sh.Len
	} else {
		// Array: packed = (elemSize & 0xFFFF) | (arrayLen << 16)
		count = int(packed >> 16)
		data = fieldPtr
	}

	m.buf = append(m.buf, '[')
	if doIndent && count > 0 {
		m.indentDepth++
	}
	for i := 0; i < count; i++ {
		if i > 0 {
			m.buf = append(m.buf, ',')
		}
		if doIndent {
			m.appendNewlineIndent()
		}
		switch op {
		case opSeqFloat64:
			f := *(*float64)(unsafe.Add(data, uintptr(i)*8))
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &UnsupportedValueError{Str: "NaN or Inf float value"}
			}
			m.appendJSONFloat64(f)
		case opSeqInt:
			m.appendInt64(int64(*(*int)(unsafe.Add(data, uintptr(i)*unsafe.Sizeof(int(0))))))
		case opSeqInt64:
			m.appendInt64(*(*int64)(unsafe.Add(data, uintptr(i)*8)))
		case opSeqString:
			s := *(*string)(unsafe.Add(data, uintptr(i)*unsafe.Sizeof("")))
			m.encodeString(s)
		}
	}
	if doIndent && count > 0 {
		m.indentDepth--
		m.appendNewlineIndent()
	}
	m.buf = append(m.buf, ']')
	return nil
}

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}
