package vjson

import (
	"math"
	"reflect"
	"unsafe"
)

// alignedOps returns a copy of data backed by 8-byte aligned memory.
// Uses a []uint64 as the backing store to guarantee alignment, since
// Go's allocator naturally aligns uint64 on 8-byte boundaries.
// This ensures the C VM can safely cast ops pointers to VjOpHdr*/VjOpExt*.
func alignedOps(data []byte) []byte {
	n := len(data)
	if n == 0 {
		return data
	}
	buf := make([]uint64, (n+7)/8)
	dst := unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), len(buf)*8)[:n]
	copy(dst, data)
	return dst
}

// Blueprint Compiler — compiles a StructCodec into a flat, linear
// instruction stream (Blueprint) with all nested types inlined.
// Keys are stored in the global key pool (globalKeyPool).

// blueprintBuilder accumulates instructions during compilation.
// Instructions are emitted as a byte stream ([]byte), with each instruction
// being either 8 bytes (short) or 16 bytes (long/extended).
type blueprintBuilder struct {
	ops       []byte          // byte stream (variable-length instructions)
	fallbacks map[int]*fbInfo // byte offset → fallback info

	// visiting tracks struct types currently on the compilation call chain.
	// Value semantics:
	//   present && value == -1: visiting, no subroutine allocated yet
	//   present && value >= 0:  subroutine already emitted at that byte offset
	visiting map[reflect.Type]int

	// pendingSubs records struct types that need subroutine emission.
	// Populated when a cycle is first detected; drained by emitPendingSubs.
	pendingSubs []*StructCodec

	// recurseFixups records OP_CALL instructions whose operand_a (target byte offset)
	// must be patched after the subroutine for the target type is emitted.
	recurseFixups []recurseFixup
}

// emitShort appends an 8-byte instruction and returns its byte offset.
func (b *blueprintBuilder) emitShort(hdr VjOpHdr) int {
	idx := len(b.ops)
	b.ops = append(b.ops, (*[8]byte)(unsafe.Pointer(&hdr))[:]...)
	return idx
}

// emitLong appends a 16-byte instruction (header + extension) and returns its byte offset.
func (b *blueprintBuilder) emitLong(hdr VjOpHdr, ext VjOpExt) int {
	idx := len(b.ops)
	b.ops = append(b.ops, (*[8]byte)(unsafe.Pointer(&hdr))[:]...)
	b.ops = append(b.ops, (*[8]byte)(unsafe.Pointer(&ext))[:]...)
	return idx
}

// patchExt patches the VjOpExt at the given byte offset (byteOff + 8).
// Only valid for extended (16-byte) instructions.
func (b *blueprintBuilder) patchExt(byteOff int, ext VjOpExt) {
	copy(b.ops[byteOff+8:byteOff+16], (*[8]byte)(unsafe.Pointer(&ext))[:])
}

// addKey inserts key bytes into the global key pool (with deduplication).
// Returns the pool offset and length. Thread-safe.
func (b *blueprintBuilder) addKey(keyBytes []byte) (keyOff uint16, keyLen uint8) {
	if len(keyBytes) == 0 {
		return 0, 0
	}
	return globalKeyPoolInsert(keyBytes)
}

// pc returns the current byte offset (next instruction position).
func (b *blueprintBuilder) pc() int {
	return len(b.ops)
}

// recurseFixup records an OP_CALL instruction for a recursive struct that
// needs its operand_a patched to the subroutine's start byte offset once emitted.
type recurseFixup struct {
	opByteOff int          // byte offset of the OP_CALL in b.ops
	targetTy  reflect.Type // the struct type whose subroutine we jump to
}

// compileBlueprint compiles a StructCodec into a Blueprint.
// The resulting Blueprint contains a single flat instruction stream
// for the entire type tree, with all nested types inlined.
func compileBlueprint(dec *StructCodec) *Blueprint {
	var b blueprintBuilder
	b.fallbacks = make(map[int]*fbInfo)
	b.visiting = make(map[reflect.Type]int)

	// Mark top-level struct as visiting to detect cycles.
	b.visiting[dec.Typ] = -1

	// Emit top-level struct as OBJ_OPEN + body + OBJ_CLOSE.
	b.emitShort(VjOpHdr{
		OpType: opObjOpen,
	})

	emitStructBody(&b, dec, 0)

	b.emitShort(VjOpHdr{
		OpType: opObjClose,
	})

	// Terminate the main instruction stream. OP_RET at depth=0 returns
	// to Go; at depth>0 (SWITCH_OPS) it pops the CALL frame.
	// Subroutines placed after this are only reachable via OP_CALL frames.
	b.emitShort(VjOpHdr{OpType: opRet})

	// Emit subroutines for cycle-participating struct types.
	emitPendingSubs(&b)

	return &Blueprint{
		Name:      dec.Typ.String(),
		Ops:       alignedOps(b.ops),
		Fallbacks: b.fallbacks,
	}
}

// emitPendingSubs emits subroutines for all cycle-participating struct types
// and patches the OP_CALL instructions that reference them.
//
// Each subroutine is: OBJ_OPEN + struct body + OBJ_CLOSE + OP_RET.
// The OP_RET pops the CALL frame, restoring ops/pc/base from the caller's
// stack frame.
//
// pendingSubs is drained iteratively because emitting a subroutine body may
// discover additional cycles (e.g. mutual recursion A↔B), appending new
// entries to pendingSubs.
func emitPendingSubs(b *blueprintBuilder) {
	for len(b.pendingSubs) > 0 {
		// Pop one pending subroutine.
		sub := b.pendingSubs[0]
		b.pendingSubs = b.pendingSubs[1:]

		// Record subroutine start PC and update visiting map.
		subPC := b.pc()
		b.visiting[sub.Typ] = subPC

		// Emit subroutine body: OBJ_OPEN + fields + OBJ_CLOSE + OP_RET.
		b.emitShort(VjOpHdr{OpType: opObjOpen})
		emitStructBody(b, sub, 0)
		b.emitShort(VjOpHdr{OpType: opObjClose})
		b.emitShort(VjOpHdr{OpType: opRet})
	}

	// Patch all OP_CALL instructions with resolved subroutine byte offsets.
	for _, fix := range b.recurseFixups {
		pc, ok := b.visiting[fix.targetTy]
		if !ok || pc < 0 {
			panic("vjson: compileBlueprint: unresolved recurse fixup for " + fix.targetTy.String())
		}
		b.patchExt(fix.opByteOff, VjOpExt{OperandA: int32(pc)})
	}
}

// compileStandaloneSliceBlueprint builds a Blueprint for encoding a slice
// whose type was discovered at runtime (e.g. inside an interface{}).
// The ops encode: SLICE_BEGIN + element body + SLICE_END + RET.
// Entered via IFACE_SWITCH_OPS (which pushes a CALL frame); RET pops it.
// The VM's base register must point to the GoSlice header on entry.
func compileStandaloneSliceBlueprint(dec *SliceCodec) *Blueprint {
	var b blueprintBuilder
	b.fallbacks = make(map[int]*fbInfo)
	b.visiting = make(map[reflect.Type]int)

	emitSliceInner(&b, 0, dec)
	b.emitShort(VjOpHdr{OpType: opRet})

	return &Blueprint{
		Name:      dec.SliceType.String(),
		Ops:       alignedOps(b.ops),
		Fallbacks: b.fallbacks,
	}
}

// compileStandaloneArrayBlueprint builds a Blueprint for encoding a fixed-size array
// whose type was discovered at runtime (e.g. inside an interface{}).
// Entered via IFACE_SWITCH_OPS (which pushes a CALL frame); RET pops it.
func compileStandaloneArrayBlueprint(dec *ArrayCodec) *Blueprint {
	var b blueprintBuilder
	b.fallbacks = make(map[int]*fbInfo)
	b.visiting = make(map[reflect.Type]int)

	emitArrayInner(&b, 0, dec)
	b.emitShort(VjOpHdr{OpType: opRet})

	return &Blueprint{
		Name:      dec.ArrayType.String(),
		Ops:       alignedOps(b.ops),
		Fallbacks: b.fallbacks,
	}
}

// emitStructBody emits instructions for all fields in a struct.
// baseOff is the struct's offset within its parent (0 for top-level).
func emitStructBody(b *blueprintBuilder, dec *StructCodec, baseOff uintptr) {
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		fieldOff := baseOff + fi.Offset

		// Determine if this field needs omitempty.
		needsOmitempty := fi.Flags&tiFlagOmitEmpty != 0

		// Check field_off overflow: if > uint16 max, emit as Go fallback.
		// The Go fallback handler uses fbInfo.Offset (uintptr), not op.FieldOff.
		// We also cannot emit OP_SKIP_IF_ZERO for oversized offsets.
		if fieldOff > math.MaxUint16 {
			emitYieldOverflow(b, fi, fieldOff, i)
			continue
		}

		// Check key_len overflow: if > 255, emit as Go fallback.
		if fi.Ext != nil && len(fi.Ext.KeyBytes) > 255 {
			emitYieldOverflow(b, fi, fieldOff, i)
			continue
		}

		// Fields with custom marshalers or ,string tag → yield to Go.
		if fi.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn|tiFlagQuoted) != 0 {
			if needsOmitempty {
				// Skip 16 (SKIP_IF_ZERO) + 8 (FALLBACK) = 24 bytes
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitYield(b, fi, fieldOff, i, fallbackReasonFromFlags(fi.Flags))
			continue
		}

		switch fi.Kind {
		case KindBool,
			KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
			KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
			KindFloat32, KindFloat64,
			KindString,
			KindRawMessage, KindNumber:
			if needsOmitempty {
				// Skip 16 (SKIP_IF_ZERO) + 8 (primitive) = 24 bytes
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitPrimitive(b, fi, fieldOff)

		case KindStruct:
			subDec := fi.resolveCodec().(*StructCodec)
			if needsOmitempty {
				// Emit placeholder OP_SKIP_IF_ZERO, then nested struct, then patch.
				skipIdx := emitSkipIfZeroPlaceholder(b, fi, fieldOff, fi.Kind)
				emitNestedStruct(b, fi, fieldOff, subDec)
				// Patch: skip byte distance = from SKIP_IF_ZERO start to current position
				b.patchExt(skipIdx, VjOpExt{OperandA: int32(b.pc() - skipIdx), OperandB: int32(fi.Kind)})
			} else {
				emitNestedStruct(b, fi, fieldOff, subDec)
			}

		case KindPointer:
			pDec := fi.resolveCodec().(*PointerCodec)
			if needsOmitempty {
				skipIdx := emitSkipIfZeroPlaceholder(b, fi, fieldOff, KindPointer)
				emitPointer(b, fi, fieldOff, pDec)
				b.patchExt(skipIdx, VjOpExt{OperandA: int32(b.pc() - skipIdx), OperandB: int32(KindPointer)})
			} else {
				emitPointer(b, fi, fieldOff, pDec)
			}

		case KindSlice:
			sliceDec := fi.resolveCodec().(*SliceCodec)
			// []byte needs base64 encoding — yield to Go.
			if sliceDec.ElemTI.Kind == KindUint8 && sliceDec.ElemSize == 1 {
				if needsOmitempty {
					// Skip 16 (SKIP_IF_ZERO) + 8 (FALLBACK) = 24 bytes
					emitSkipIfZero(b, fi, fieldOff, 16+8, KindSlice)
				}
				emitYield(b, fi, fieldOff, i, fbReasonByteSlice)
				continue
			}
			if needsOmitempty {
				skipIdx := emitSkipIfZeroPlaceholder(b, fi, fieldOff, KindSlice)
				emitSlice(b, fi, fieldOff, sliceDec)
				b.patchExt(skipIdx, VjOpExt{OperandA: int32(b.pc() - skipIdx), OperandB: int32(KindSlice)})
			} else {
				emitSlice(b, fi, fieldOff, sliceDec)
			}

		case KindArray:
			aDec := fi.resolveCodec().(*ArrayCodec)
			// [N]byte needs base64 encoding — yield to Go.
			if aDec.ElemTI.Kind == KindUint8 && aDec.ElemSize == 1 {
				emitYield(b, fi, fieldOff, i, fbReasonByteArray)
				continue
			}
			// Arrays can't be nil; omitempty is not meaningful.
			emitArray(b, fi, fieldOff, aDec)

		case KindMap:
			mapDec := fi.resolveCodec().(*MapCodec)
			if needsOmitempty {
				// Map omitempty needs Go-side len check (C only checks nil).
				// Emit as fallback so Go handles omitempty + full map encoding.
				emitYield(b, fi, fieldOff, i, fbReasonMapOmitempty)
			} else if mapDec.ValIsString && mapDec.KeyType.Kind() == reflect.String {
				// map[string]string: single C opcode with native Swiss Map iteration.
				emitMapStrStr(b, fi, fieldOff)
			} else {
				emitMap(b, fi, fieldOff, mapDec)
			}

		case KindAny:
			if needsOmitempty {
				// Skip 16 (SKIP_IF_ZERO) + 8 (INTERFACE) = 24 bytes
				emitSkipIfZero(b, fi, fieldOff, 16+8, KindAny)
			}
			emitInterface(b, fi, fieldOff)

		default:
			// Unknown kind → yield to Go.
			if needsOmitempty {
				// Skip 16 (SKIP_IF_ZERO) + 8 (FALLBACK) = 24 bytes
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitYield(b, fi, fieldOff, i, fbReasonUnknown)
		}
	}
}

// emitPrimitive emits a single 8-byte primitive encoding instruction.
func emitPrimitive(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	b.emitShort(VjOpHdr{
		OpType:   kindToOpcode(fi.Kind),
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

// emitNestedStruct emits OBJ_OPEN + body + OBJ_CLOSE for a nested struct.
// Uses frameless flat encoding: child field offsets are computed at compile
// time (baseOff = parent field offset), so the VM doesn't need to push a
// stack frame or switch the base register.
func emitNestedStruct(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr, subDec *StructCodec) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)

	// OBJ_OPEN: lightweight '{' with key, no frame push
	b.emitShort(VjOpHdr{
		OpType: opObjOpen,
		KeyLen: keyLen,
		KeyOff: keyOff,
	})

	// Mark type as visiting to detect cycles through pointers.
	_, wasVisiting := b.visiting[subDec.Typ]
	b.visiting[subDec.Typ] = -1

	// Emit child fields with accumulated offset (no base switch).
	emitStructBody(b, subDec, fieldOff)

	// Restore previous visiting state.
	if !wasVisiting {
		delete(b.visiting, subDec.Typ)
	}

	// OBJ_CLOSE: lightweight '}'
	b.emitShort(VjOpHdr{
		OpType: opObjClose,
	})
}

// emitPointer emits PTR_DEREF + the dereferenced type's instructions.
func emitPointer(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr, pDec *PointerCodec) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	elemTI := pDec.ElemTI

	// PTR_DEREF (16 bytes): operand_a = byte distance to skip on nil (patched below)
	derefByteOff := b.emitLong(
		VjOpHdr{
			OpType:   opPtrDeref,
			KeyLen:   keyLen,
			KeyOff:   keyOff,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{OperandA: 0}, // placeholder
	)

	// Emit the dereferenced type's instructions.
	// After PTR_DEREF, base is set to the dereferenced pointer value.
	emitDerefBody(b, elemTI)

	// Emit PTR_END (8 bytes) to pop the deref frame and restore parent base.
	b.emitShort(VjOpHdr{
		OpType: opPtrEnd,
	})

	// Patch: byte distance from PTR_DEREF start to instruction after PTR_END
	b.patchExt(derefByteOff, VjOpExt{OperandA: int32(b.pc() - derefByteOff)})
}

// emitDerefBody emits the body for a dereferenced pointer target.
// The offset is 0 because base has been switched to the deref'd address.
func emitDerefBody(b *blueprintBuilder, elemTI *TypeInfo) {
	// Custom marshalers → yield
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		idx := b.emitShort(VjOpHdr{
			OpType: opFallback,
		})
		b.fallbacks[idx] = &fbInfo{
			TI:     elemTI,
			Offset: 0, // base is already the deref'd pointer
		}
		return
	}

	switch elemTI.Kind {
	case KindBool,
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
		KindFloat32, KindFloat64,
		KindString,
		KindRawMessage, KindNumber:
		// Primitive: emit a keyless 8-byte instruction (off=0, no key)
		b.emitShort(VjOpHdr{
			OpType:   kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case KindStruct:
		subDec := elemTI.resolveCodec().(*StructCodec)
		// Cycle detection: if this struct type is already being compiled
		// along the current call chain, emit OP_CALL to call the
		// subroutine that will be emitted at the end of the Blueprint.
		if _, visiting := b.visiting[subDec.Typ]; visiting {
			emitCall(b, subDec, 0)
			return
		}
		b.visiting[subDec.Typ] = -1
		// Inline the struct with OBJ_OPEN/CLOSE (keyless, off=0)
		b.emitShort(VjOpHdr{
			OpType: opObjOpen,
		})
		emitStructBody(b, subDec, 0)
		b.emitShort(VjOpHdr{
			OpType: opObjClose,
		})
		delete(b.visiting, subDec.Typ)

	case KindSlice:
		sliceDec := elemTI.resolveCodec().(*SliceCodec)
		emitSliceInner(b, 0, sliceDec)

	case KindArray:
		aDec := elemTI.resolveCodec().(*ArrayCodec)
		emitArrayInner(b, 0, aDec)

	case KindMap:
		mapDec := elemTI.resolveCodec().(*MapCodec)
		if mapDec.ValIsString && mapDec.KeyType.Kind() == reflect.String {
			emitMapStrStrInner(b, 0)
		} else {
			emitMapInner(b, 0, elemTI, mapDec)
		}

	case KindAny:
		b.emitShort(VjOpHdr{
			OpType:   opInterface,
			FieldOff: 0,
		})

	case KindPointer:
		// Pointer to pointer — recurse
		innerDec := elemTI.resolveCodec().(*PointerCodec)
		derefByteOff := b.emitLong(
			VjOpHdr{OpType: opPtrDeref, FieldOff: 0},
			VjOpExt{OperandA: 0},
		)
		emitDerefBody(b, innerDec.ElemTI)
		b.emitShort(VjOpHdr{
			OpType: opPtrEnd,
		})
		b.patchExt(derefByteOff, VjOpExt{OperandA: int32(b.pc() - derefByteOff)})

	default:
		// Fallback
		idx := b.emitShort(VjOpHdr{
			OpType: opFallback,
		})
		b.fallbacks[idx] = &fbInfo{
			TI:     elemTI,
			Offset: 0, // base is already the deref'd pointer
		}
	}
}

// emitSlice emits SLICE_BEGIN + element body + SLICE_END for a slice field.
func emitSlice(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr, sliceDec *SliceCodec) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)

	beginByteOff := b.emitLong(
		VjOpHdr{
			OpType:   opSliceBegin,
			KeyLen:   keyLen,
			KeyOff:   keyOff,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{
			OperandA: int32(sliceDec.ElemSize), // elem_size
			OperandB: 0,                        // body byte length, patched below
		},
	)

	bodyStartByteOff := b.pc()

	// Emit element body (offset=0, base points to elem[i])
	emitElementBody(b, sliceDec.ElemTI)

	// SLICE_END (16 bytes): OperandA = body start byte offset, OperandB = elem_size
	b.emitLong(
		VjOpHdr{OpType: opSliceEnd},
		VjOpExt{
			OperandA: int32(bodyStartByteOff),  // absolute byte offset of body start
			OperandB: int32(sliceDec.ElemSize), // elem_size
		},
	)

	// Patch SLICE_BEGIN's operand_b with body byte length (excluding SLICE_END)
	bodyByteLen := b.pc() - bodyStartByteOff - 16 // -16 to exclude SLICE_END
	b.patchExt(beginByteOff, VjOpExt{
		OperandA: int32(sliceDec.ElemSize),
		OperandB: int32(bodyByteLen),
	})
}

// emitSliceInner is like emitSlice but without key bytes (for deref'd pointers).
func emitSliceInner(b *blueprintBuilder, fieldOff uintptr, sliceDec *SliceCodec) {
	beginByteOff := b.emitLong(
		VjOpHdr{
			OpType:   opSliceBegin,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{
			OperandA: int32(sliceDec.ElemSize),
			OperandB: 0,
		},
	)

	bodyStartByteOff := b.pc()
	emitElementBody(b, sliceDec.ElemTI)
	b.emitLong(
		VjOpHdr{OpType: opSliceEnd},
		VjOpExt{
			OperandA: int32(bodyStartByteOff),
			OperandB: int32(sliceDec.ElemSize),
		},
	)

	bodyByteLen := b.pc() - bodyStartByteOff - 16
	b.patchExt(beginByteOff, VjOpExt{
		OperandA: int32(sliceDec.ElemSize),
		OperandB: int32(bodyByteLen),
	})
}

// emitArray emits ARRAY_BEGIN + element body + SLICE_END for an array field.
func emitArray(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr, aDec *ArrayCodec) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)

	// Pack elem_size (low 16) | array_len (high 16) into operand_a.
	packed := int32(aDec.ElemSize&0xFFFF) | int32(aDec.ArrayLen&0xFFFF)<<16

	beginByteOff := b.emitLong(
		VjOpHdr{
			OpType:   opArrayBegin,
			KeyLen:   keyLen,
			KeyOff:   keyOff,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{
			OperandA: packed,
			OperandB: 0, // body byte length, patched below
		},
	)

	bodyStartByteOff := b.pc()

	// Emit element body (offset=0, base points to elem[i])
	emitElementBody(b, aDec.ElemTI)

	// Reuse SLICE_END for loop back-edge: OperandA = body byte offset, OperandB = elem_size
	b.emitLong(
		VjOpHdr{OpType: opSliceEnd},
		VjOpExt{
			OperandA: int32(bodyStartByteOff),
			OperandB: int32(aDec.ElemSize),
		},
	)

	bodyByteLen := b.pc() - bodyStartByteOff - 16
	b.patchExt(beginByteOff, VjOpExt{
		OperandA: packed,
		OperandB: int32(bodyByteLen),
	})
}

// emitArrayInner is like emitArray but without key bytes (for deref'd pointers / top-level).
func emitArrayInner(b *blueprintBuilder, fieldOff uintptr, aDec *ArrayCodec) {
	packed := int32(aDec.ElemSize&0xFFFF) | int32(aDec.ArrayLen&0xFFFF)<<16

	beginByteOff := b.emitLong(
		VjOpHdr{
			OpType:   opArrayBegin,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{
			OperandA: packed,
			OperandB: 0,
		},
	)

	bodyStartByteOff := b.pc()
	emitElementBody(b, aDec.ElemTI)
	b.emitLong(
		VjOpHdr{OpType: opSliceEnd},
		VjOpExt{
			OperandA: int32(bodyStartByteOff),
			OperandB: int32(aDec.ElemSize),
		},
	)

	bodyByteLen := b.pc() - bodyStartByteOff - 16
	b.patchExt(beginByteOff, VjOpExt{
		OperandA: packed,
		OperandB: int32(bodyByteLen),
	})
}

// emitElementBody emits the instructions for encoding a single element
// (used in slice loops). base points to the element.
func emitElementBody(b *blueprintBuilder, elemTI *TypeInfo) {
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		idx := b.emitShort(VjOpHdr{
			OpType: opFallback,
		})
		b.fallbacks[idx] = &fbInfo{
			TI:     elemTI,
			Offset: 0, // base is the element pointer
		}
		return
	}

	switch elemTI.Kind {
	case KindBool,
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
		KindFloat32, KindFloat64,
		KindString,
		KindRawMessage, KindNumber:
		b.emitShort(VjOpHdr{
			OpType:   kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case KindStruct:
		subDec := elemTI.resolveCodec().(*StructCodec)
		// Cycle detection (same as emitDerefBody).
		if _, visiting := b.visiting[subDec.Typ]; visiting {
			emitCall(b, subDec, 0)
			return
		}
		b.visiting[subDec.Typ] = -1
		b.emitShort(VjOpHdr{
			OpType: opObjOpen,
		})
		emitStructBody(b, subDec, 0)
		b.emitShort(VjOpHdr{OpType: opObjClose})
		delete(b.visiting, subDec.Typ)

	case KindPointer:
		pDec := elemTI.resolveCodec().(*PointerCodec)
		derefByteOff := b.emitLong(
			VjOpHdr{OpType: opPtrDeref, FieldOff: 0},
			VjOpExt{OperandA: 0},
		)
		emitDerefBody(b, pDec.ElemTI)
		b.emitShort(VjOpHdr{
			OpType: opPtrEnd,
		})
		b.patchExt(derefByteOff, VjOpExt{OperandA: int32(b.pc() - derefByteOff)})

	case KindSlice:
		sliceDec := elemTI.resolveCodec().(*SliceCodec)
		emitSliceInner(b, 0, sliceDec)

	case KindArray:
		aDec := elemTI.resolveCodec().(*ArrayCodec)
		emitArrayInner(b, 0, aDec)

	case KindMap:
		mapDec := elemTI.resolveCodec().(*MapCodec)
		if mapDec.ValIsString && mapDec.KeyType.Kind() == reflect.String {
			emitMapStrStrInner(b, 0)
		} else {
			emitMapInner(b, 0, elemTI, mapDec)
		}

	case KindAny:
		b.emitShort(VjOpHdr{
			OpType:   opInterface,
			FieldOff: 0,
		})

	default:
		idx := b.emitShort(VjOpHdr{
			OpType: opFallback,
		})
		b.fallbacks[idx] = &fbInfo{
			TI:     elemTI,
			Offset: 0,
		}
	}
}

// emitMap emits MAP_BEGIN + value body + MAP_END for a map field.
// Map iteration is Go-driven: the VM yields for each k/v pair.
func emitMap(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr, mapDec *MapCodec) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)

	beginByteOff := b.emitLong(
		VjOpHdr{
			OpType:   opMapBegin,
			KeyLen:   keyLen,
			KeyOff:   keyOff,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{OperandA: 0}, // distance to MAP_END, patched below
	)

	// Register MAP_BEGIN in fallback table so Go can find the MapCodec.
	b.fallbacks[beginByteOff] = &fbInfo{
		TI:     fi,
		Offset: fieldOff,
	}

	// Emit value encoding instructions (base will be set to value ptr by Go)
	emitElementBody(b, mapDec.ValTI)

	endByteOff := b.emitShort(VjOpHdr{
		OpType: opMapEnd,
	})

	// Patch: operand_a of MAP_BEGIN = byte distance to MAP_END
	b.patchExt(beginByteOff, VjOpExt{OperandA: int32(endByteOff - beginByteOff)})
}

// emitMapInner is like emitMap but without key bytes.
func emitMapInner(b *blueprintBuilder, fieldOff uintptr, elemTI *TypeInfo, mapDec *MapCodec) {
	beginByteOff := b.emitLong(
		VjOpHdr{
			OpType:   opMapBegin,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{OperandA: 0},
	)

	// Register MAP_BEGIN in fallback table so Go can find the MapCodec.
	b.fallbacks[beginByteOff] = &fbInfo{
		TI:     elemTI,
		Offset: fieldOff,
	}

	emitElementBody(b, mapDec.ValTI)

	endByteOff := b.emitShort(VjOpHdr{OpType: opMapEnd})
	b.patchExt(beginByteOff, VjOpExt{OperandA: int32(endByteOff - beginByteOff)})
}

// emitMapStrStr emits a single OP_MAP_STR_STR instruction for map[string]string.
// C handles the entire iteration natively — no MAP_END, no fallback entry needed.
func emitMapStrStr(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	b.emitShort(VjOpHdr{
		OpType:   opMapStrStr,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

// emitMapStrStrInner emits a keyless OP_MAP_STR_STR (for deref/element body contexts).
func emitMapStrStrInner(b *blueprintBuilder, fieldOff uintptr) {
	b.emitShort(VjOpHdr{
		OpType:   opMapStrStr,
		FieldOff: uint16(fieldOff),
	})
}

// emitCall emits an OP_CALL instruction for a cycle-participating struct.
// If the subroutine for dec has already been emitted (visiting[dec.Typ] >= 0),
// the target PC is set directly. Otherwise, a fixup is recorded and a pending
// subroutine is registered for later emission.
func emitCall(b *blueprintBuilder, dec *StructCodec, fieldOff uintptr) {
	byteOff := b.emitLong(
		VjOpHdr{
			OpType:   opCall,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{OperandA: 0}, // target byte offset, patched below or via fixup
	)

	if pc := b.visiting[dec.Typ]; pc >= 0 {
		// Subroutine already emitted — set target directly.
		b.patchExt(byteOff, VjOpExt{OperandA: int32(pc)})
	} else {
		// Subroutine not yet emitted — record fixup and schedule emission.
		b.recurseFixups = append(b.recurseFixups, recurseFixup{
			opByteOff: byteOff,
			targetTy:  dec.Typ,
		})
		// Only append to pendingSubs if not already pending.
		alreadyPending := false
		for _, p := range b.pendingSubs {
			if p.Typ == dec.Typ {
				alreadyPending = true
				break
			}
		}
		if !alreadyPending {
			b.pendingSubs = append(b.pendingSubs, dec)
		}
	}
}

// emitInterface emits a single OP_INTERFACE instruction.
func emitInterface(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	b.emitShort(VjOpHdr{
		OpType:   opInterface,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

// fallbackReasonFromFlags returns the FallbackReason for a field based on its TypeInfo flags.
// Priority: Marshaler > TextMarshaler > Quoted > Unknown.
func fallbackReasonFromFlags(flags tiFlag) int32 {
	if flags&tiFlagHasMarshalFn != 0 {
		return fbReasonMarshaler
	}
	if flags&tiFlagHasTextMarshalFn != 0 {
		return fbReasonTextMarshaler
	}
	if flags&tiFlagQuoted != 0 {
		return fbReasonQuoted
	}
	return fbReasonUnknown
}

// emitYield emits a single 8-byte OP_FALLBACK instruction for Go fallback.
// With variable-length instructions, FALLBACK is short (no operands).
// The fallback reason and field info are tracked via Blueprint.Fallbacks[byteOff].
func emitYield(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr, fieldIdx int, reason int32) {
	keyOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	idx := b.emitShort(VjOpHdr{
		OpType:   opFallback,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
	// Record fallback info so Go can encode this field.
	b.fallbacks[idx] = &fbInfo{
		TI:     fi,
		Offset: fieldOff,
	}
}

// emitYieldOverflow emits an OP_FALLBACK for a field whose field_off or key_len
// exceeds the uint16/uint8 range. The Go fallback handler uses fbInfo.Offset
// (uintptr, no size limit), not op.FieldOff. We set FieldOff=0 since it's
// unused by the Go handler.
func emitYieldOverflow(b *blueprintBuilder, fi *TypeInfo, fieldOff uintptr, fieldIdx int) {
	idx := b.emitShort(VjOpHdr{
		OpType:   opFallback,
		FieldOff: 0, // unused — Go uses fbInfo.Offset
	})
	b.fallbacks[idx] = &fbInfo{
		TI:     fi,
		Offset: fieldOff,
	}
}

// emitSkipIfZero emits a 16-byte OP_SKIP_IF_ZERO instruction with a known byte skip distance.
// skipBytes is the total byte distance from the SKIP_IF_ZERO start to the instruction after the skipped range.
func emitSkipIfZero(b *blueprintBuilder, _ *TypeInfo, fieldOff uintptr, skipBytes int, kind ElemTypeKind) {
	b.emitLong(
		VjOpHdr{
			OpType:   opSkipIfZero,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{
			OperandA: int32(skipBytes),
			OperandB: int32(kind), // ZeroCheckTag (matches ElemTypeKind)
		},
	)
}

// emitSkipIfZeroPlaceholder emits a 16-byte OP_SKIP_IF_ZERO with OperandA=0 (to be patched).
// Returns the byte offset of the emitted instruction.
func emitSkipIfZeroPlaceholder(b *blueprintBuilder, _ *TypeInfo, fieldOff uintptr, kind ElemTypeKind) int {
	return b.emitLong(
		VjOpHdr{
			OpType:   opSkipIfZero,
			FieldOff: uint16(fieldOff),
		},
		VjOpExt{
			OperandA: 0,           // placeholder, patched by caller
			OperandB: int32(kind), // ZeroCheckTag (matches ElemTypeKind)
		},
	)
}

// getBlueprint returns the compiled Blueprint for this StructCodec.
// Results are cached after the first call (thread-safe).
func (dec *StructCodec) getBlueprint() *Blueprint {
	cache := dec.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileBlueprint(dec)
	})
	return cache.blueprint
}

func (d *SliceCodec) getBlueprint() *Blueprint {
	cache := d.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileStandaloneSliceBlueprint(d)
	})
	return cache.blueprint
}

func (d *ArrayCodec) getBlueprint() *Blueprint {
	cache := d.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileStandaloneArrayBlueprint(d)
	})
	return cache.blueprint
}
