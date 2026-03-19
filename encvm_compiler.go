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

// compileBlueprint compiles a StructCodec into a Blueprint.
// The resulting Blueprint contains a single flat instruction stream
// for the entire type tree, with all nested types inlined.
func compileBlueprint(dec *StructCodec) *Blueprint {
	b := &irBuilder{
		visiting: make(map[reflect.Type]Label),
	}

	// Mark top-level struct as visiting with a label at position 0.
	rootLabel := b.allocLabel()
	b.visiting[dec.Typ] = rootLabel
	b.defineLabel(rootLabel)

	// Emit top-level struct as OBJ_OPEN + body + OBJ_CLOSE.
	b.emit(IRInst{
		Op:         opObjOpen,
		Annotation: dec.Typ.String(),
		SourceType: dec.Typ,
	})

	emitStructBody(b, dec, 0)

	b.emit(IRInst{Op: opObjClose})

	// Terminate the main instruction stream.
	b.emit(IRInst{Op: opRet})

	// Emit subroutines for cycle-participating struct types.
	emitPendingSubs(b)

	// Run optimization passes.
	insts := runPasses(b.insts, b.nextLabel)

	// Lower IR to byte stream.
	ops, fallbacks, annotations := lower(insts)

	return &Blueprint{
		Name:        dec.Typ.String(),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// emitPendingSubs emits subroutines for all cycle-participating struct types.
//
// Each subroutine is: label + OBJ_OPEN + struct body + OBJ_CLOSE + OP_RET.
//
// pendingSubs is drained iteratively because emitting a subroutine body may
// discover additional cycles (e.g. mutual recursion A↔B), appending new
// entries to pendingSubs.
func emitPendingSubs(b *irBuilder) {
	for len(b.pendingSubs) > 0 {
		// Pop one pending subroutine.
		sub := b.pendingSubs[0]
		b.pendingSubs = b.pendingSubs[1:]

		// Use the label stored in the pendingSub (not from visiting map,
		// which may have been deleted by emitDerefBody/emitElementBody).
		subLabel := sub.label

		// Re-establish visiting entry so nested emitCall can find it.
		b.visiting[sub.dec.Typ] = subLabel

		// Define the label at the current position.
		b.defineLabel(subLabel)

		// Emit subroutine body: OBJ_OPEN + fields + OBJ_CLOSE + OP_RET.
		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: sub.dec.Typ.String(),
			SourceType: sub.dec.Typ,
		})
		emitStructBody(b, sub.dec, 0)
		b.emit(IRInst{Op: opObjClose})
		b.emit(IRInst{Op: opRet})
	}
}

// compileStandaloneSliceBlueprint builds a Blueprint for encoding a slice
// whose type was discovered at runtime (e.g. inside an interface{}).
func compileStandaloneSliceBlueprint(dec *SliceCodec) *Blueprint {
	b := &irBuilder{
		visiting: make(map[reflect.Type]Label),
	}

	emitSliceInner(b, 0, dec)
	b.emit(IRInst{Op: opRet})

	ops, fallbacks, annotations := lower(b.insts)

	return &Blueprint{
		Name:        dec.SliceType.String(),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// compileStandaloneArrayBlueprint builds a Blueprint for encoding a fixed-size array
// whose type was discovered at runtime (e.g. inside an interface{}).
func compileStandaloneArrayBlueprint(dec *ArrayCodec) *Blueprint {
	b := &irBuilder{
		visiting: make(map[reflect.Type]Label),
	}

	emitArrayInner(b, 0, dec)
	b.emit(IRInst{Op: opRet})

	ops, fallbacks, annotations := lower(b.insts)

	return &Blueprint{
		Name:        dec.ArrayType.String(),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// emitStructBody emits instructions for all fields in a struct.
// baseOff is the struct's offset within its parent (0 for top-level).
func emitStructBody(b *irBuilder, dec *StructCodec, baseOff uintptr) {
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		fieldOff := baseOff + fi.Offset

		// Determine if this field needs omitempty.
		needsOmitempty := fi.Flags&tiFlagOmitEmpty != 0

		// Check field_off overflow: if > uint16 max, emit as Go fallback.
		if fieldOff > math.MaxUint16 {
			emitYieldOverflow(b, fi, fieldOff)
			continue
		}

		// Check key_len overflow: if > 255, emit as Go fallback.
		if fi.Ext != nil && len(fi.Ext.KeyBytes) > 255 {
			emitYieldOverflow(b, fi, fieldOff)
			continue
		}

		// Fields with custom marshalers or ,string tag → yield to Go.
		if fi.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn|tiFlagQuoted) != 0 {
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitYield(b, fi, fieldOff, fallbackReasonFromFlags(fi.Flags))
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
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitPrimitive(b, fi, fieldOff)

		case KindStruct:
			subDec := fi.resolveCodec().(*StructCodec)
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(fi.Kind),
				})
				emitNestedStruct(b, fi, fieldOff, subDec)
				b.defineLabel(afterLabel)
			} else {
				emitNestedStruct(b, fi, fieldOff, subDec)
			}

		case KindPointer:
			pDec := fi.resolveCodec().(*PointerCodec)
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(KindPointer),
				})
				emitPointer(b, fi, fieldOff, pDec)
				b.defineLabel(afterLabel)
			} else {
				emitPointer(b, fi, fieldOff, pDec)
			}

		case KindSlice:
			sliceDec := fi.resolveCodec().(*SliceCodec)
			// []byte needs base64 encoding — yield to Go.
			if sliceDec.ElemTI.Kind == KindUint8 && sliceDec.ElemSize == 1 {
				if needsOmitempty {
					emitSkipIfZero(b, fi, fieldOff, 16+8, KindSlice)
				}
				emitYield(b, fi, fieldOff, fbReasonByteSlice)
				continue
			}
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(KindSlice),
				})
				emitSlice(b, fi, fieldOff, sliceDec)
				b.defineLabel(afterLabel)
			} else {
				emitSlice(b, fi, fieldOff, sliceDec)
			}

		case KindArray:
			aDec := fi.resolveCodec().(*ArrayCodec)
			// [N]byte needs base64 encoding — yield to Go.
			if aDec.ElemTI.Kind == KindUint8 && aDec.ElemSize == 1 {
				emitYield(b, fi, fieldOff, fbReasonByteArray)
				continue
			}
			// Arrays can't be nil; omitempty is not meaningful.
			emitArray(b, fi, fieldOff, aDec)

		case KindMap:
			mapDec := fi.resolveCodec().(*MapCodec)
			if needsOmitempty {
				emitYield(b, fi, fieldOff, fbReasonMapOmitempty)
			} else if canSwissMapInC(mapDec.MapKind) {
				emitMapSwiss(b, fi, fieldOff, mapDec.MapKind)
			} else {
				emitMap(b, fi, fieldOff, mapDec)
			}

		case KindAny:
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, KindAny)
			}
			emitInterface(b, fi, fieldOff)

		default:
			// Unknown kind → yield to Go.
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitYield(b, fi, fieldOff, fbReasonUnknown)
		}
	}
}

// emitPrimitive emits a single primitive encoding instruction (bool/int/float/string/etc).
// For struct fields (keyLen > 0), STRING/INT/INT64 use keyed variants that
// skip the key_len branch in the VM for better branch prediction.
func emitPrimitive(b *irBuilder, fi *TypeInfo, fieldOff uintptr) {
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	op := kindToOpcode(fi.Kind)
	if keyLen > 0 {
		switch op {
		case opString:
			op = opKString
		case opInt:
			op = opKInt
		case opInt64:
			op = opKInt64
		}
	}
	b.emit(IRInst{
		Op:       op,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

// emitNestedStruct emits OBJ_OPEN + body + OBJ_CLOSE for a nested struct.
func emitNestedStruct(b *irBuilder, fi *TypeInfo, fieldOff uintptr, subDec *StructCodec) {
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}

	b.emit(IRInst{
		Op:         opObjOpen,
		KeyLen:     keyLen,
		KeyOff:     keyOff,
		Annotation: subDec.Typ.String(),
		SourceType: subDec.Typ,
	})

	// Mark type as visiting to detect cycles through pointers.
	prevLabel, wasVisiting := b.visiting[subDec.Typ]
	if !wasVisiting {
		b.visiting[subDec.Typ] = InvalidLabel
	}

	// Emit child fields with accumulated offset (no base switch).
	emitStructBody(b, subDec, fieldOff)

	// Restore previous visiting state.
	if !wasVisiting {
		delete(b.visiting, subDec.Typ)
	} else {
		b.visiting[subDec.Typ] = prevLabel
	}

	b.emit(IRInst{Op: opObjClose})
}

// emitPointer emits PTR_DEREF + the dereferenced type's instructions + PTR_END.
func emitPointer(b *irBuilder, fi *TypeInfo, fieldOff uintptr, pDec *PointerCodec) {
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	elemTI := pDec.ElemTI

	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opPtrDeref,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		Target:   afterLabel,
	})

	// Emit the dereferenced type's instructions.
	emitDerefBody(b, elemTI)

	// PTR_END pops the deref frame and restores parent base.
	b.emit(IRInst{Op: opPtrEnd})

	b.defineLabel(afterLabel)
}

// emitDerefBody emits the body for a dereferenced pointer target.
func emitDerefBody(b *irBuilder, elemTI *TypeInfo) {
	// Custom marshalers → yield
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		b.emit(IRInst{
			Op: opFallback,
			Fallback: &fbInfo{
				TI:     elemTI,
				Offset: 0,
			},
		})
		return
	}

	switch elemTI.Kind {
	case KindBool,
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
		KindFloat32, KindFloat64,
		KindString,
		KindRawMessage, KindNumber:
		b.emit(IRInst{
			Op:       kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case KindStruct:
		subDec := elemTI.resolveCodec().(*StructCodec)
		if _, visiting := b.visiting[subDec.Typ]; visiting {
			emitCall(b, subDec, 0)
			return
		}
		b.visiting[subDec.Typ] = InvalidLabel
		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: subDec.Typ.String(),
			SourceType: subDec.Typ,
		})
		emitStructBody(b, subDec, 0)
		b.emit(IRInst{Op: opObjClose})
		delete(b.visiting, subDec.Typ)

	case KindSlice:
		sliceDec := elemTI.resolveCodec().(*SliceCodec)
		if sliceDec.ElemTI.Kind == KindUint8 && sliceDec.ElemSize == 1 {
			b.emit(IRInst{Op: opByteSlice, FieldOff: 0})
		} else {
			emitSliceInner(b, 0, sliceDec)
		}

	case KindArray:
		aDec := elemTI.resolveCodec().(*ArrayCodec)
		emitArrayInner(b, 0, aDec)

	case KindMap:
		mapDec := elemTI.resolveCodec().(*MapCodec)
		if canSwissMapInC(mapDec.MapKind) {
			emitMapSwissInner(b, 0, mapDec.MapKind)
		} else {
			emitMapInner(b, 0, elemTI, mapDec)
		}

	case KindAny:
		b.emit(IRInst{
			Op:       opInterface,
			FieldOff: 0,
		})

	case KindPointer:
		innerDec := elemTI.resolveCodec().(*PointerCodec)
		afterLabel := b.allocLabel()
		b.emit(IRInst{
			Op:     opPtrDeref,
			Target: afterLabel,
		})
		emitDerefBody(b, innerDec.ElemTI)
		b.emit(IRInst{Op: opPtrEnd})
		b.defineLabel(afterLabel)

	default:
		b.emit(IRInst{
			Op: opFallback,
			Fallback: &fbInfo{
				TI:     elemTI,
				Offset: 0,
			},
		})
	}
}

// emitSlice emits SLICE_BEGIN + element body + SLICE_END for a slice field.
// For primitive element types, emits a single OP_SEQ_xxx instruction instead.
func emitSlice(b *irBuilder, fi *TypeInfo, fieldOff uintptr, sliceDec *SliceCodec) {
	if seqOp := seqOpcode(sliceDec.ElemTI); seqOp != 0 {
		keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
		if !ok {
			emitYieldOverflow(b, fi, fieldOff)
			return
		}
		b.emit(IRInst{
			Op:       seqOp,
			KeyLen:   keyLen,
			KeyOff:   keyOff,
			FieldOff: uint16(fieldOff),
			OperandA: 0, // slice: operand_a == 0
		})
		return
	}

	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opSliceBegin,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		OperandA: int32(sliceDec.ElemSize),
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, sliceDec.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(sliceDec.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitSliceInner is like emitSlice but without key bytes (for deref'd pointers).
func emitSliceInner(b *irBuilder, fieldOff uintptr, sliceDec *SliceCodec) {
	if seqOp := seqOpcode(sliceDec.ElemTI); seqOp != 0 {
		b.emit(IRInst{
			Op:       seqOp,
			FieldOff: uint16(fieldOff),
			OperandA: 0, // slice: operand_a == 0
		})
		return
	}

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opSliceBegin,
		FieldOff: uint16(fieldOff),
		OperandA: int32(sliceDec.ElemSize),
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, sliceDec.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(sliceDec.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitArray emits ARRAY_BEGIN + element body + SLICE_END for an array field.
// For primitive element types, emits a single OP_SEQ_xxx instruction instead.
func emitArray(b *irBuilder, fi *TypeInfo, fieldOff uintptr, aDec *ArrayCodec) {
	if seqOp := seqOpcode(aDec.ElemTI); seqOp != 0 {
		keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
		if !ok {
			emitYieldOverflow(b, fi, fieldOff)
			return
		}
		packed := int32(aDec.ElemSize&0xFFFF) | int32(aDec.ArrayLen&0xFFFF)<<16
		b.emit(IRInst{
			Op:       seqOp,
			KeyLen:   keyLen,
			KeyOff:   keyOff,
			FieldOff: uint16(fieldOff),
			OperandA: packed, // array: operand_a != 0
		})
		return
	}

	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	packed := int32(aDec.ElemSize&0xFFFF) | int32(aDec.ArrayLen&0xFFFF)<<16

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opArrayBegin,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		OperandA: packed,
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, aDec.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(aDec.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitArrayInner is like emitArray but without key bytes.
func emitArrayInner(b *irBuilder, fieldOff uintptr, aDec *ArrayCodec) {
	if seqOp := seqOpcode(aDec.ElemTI); seqOp != 0 {
		packed := int32(aDec.ElemSize&0xFFFF) | int32(aDec.ArrayLen&0xFFFF)<<16
		b.emit(IRInst{
			Op:       seqOp,
			FieldOff: uint16(fieldOff),
			OperandA: packed, // array: operand_a != 0
		})
		return
	}

	packed := int32(aDec.ElemSize&0xFFFF) | int32(aDec.ArrayLen&0xFFFF)<<16

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opArrayBegin,
		FieldOff: uint16(fieldOff),
		OperandA: packed,
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, aDec.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(aDec.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitElementBody emits the instructions for encoding a single element
// (used in slice/array loops). base points to the element.
func emitElementBody(b *irBuilder, elemTI *TypeInfo) {
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		b.emit(IRInst{
			Op: opFallback,
			Fallback: &fbInfo{
				TI:     elemTI,
				Offset: 0,
			},
		})
		return
	}

	switch elemTI.Kind {
	case KindBool,
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
		KindFloat32, KindFloat64,
		KindString,
		KindRawMessage, KindNumber:
		b.emit(IRInst{
			Op:       kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case KindStruct:
		subDec := elemTI.resolveCodec().(*StructCodec)
		if _, visiting := b.visiting[subDec.Typ]; visiting {
			emitCall(b, subDec, 0)
			return
		}
		b.visiting[subDec.Typ] = InvalidLabel
		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: subDec.Typ.String(),
			SourceType: subDec.Typ,
		})
		emitStructBody(b, subDec, 0)
		b.emit(IRInst{Op: opObjClose})
		delete(b.visiting, subDec.Typ)

	case KindPointer:
		pDec := elemTI.resolveCodec().(*PointerCodec)
		afterLabel := b.allocLabel()
		b.emit(IRInst{
			Op:     opPtrDeref,
			Target: afterLabel,
		})
		emitDerefBody(b, pDec.ElemTI)
		b.emit(IRInst{Op: opPtrEnd})
		b.defineLabel(afterLabel)

	case KindSlice:
		sliceDec := elemTI.resolveCodec().(*SliceCodec)
		if sliceDec.ElemTI.Kind == KindUint8 && sliceDec.ElemSize == 1 {
			b.emit(IRInst{Op: opByteSlice, FieldOff: 0})
		} else {
			emitSliceInner(b, 0, sliceDec)
		}

	case KindArray:
		aDec := elemTI.resolveCodec().(*ArrayCodec)
		emitArrayInner(b, 0, aDec)

	case KindMap:
		mapDec := elemTI.resolveCodec().(*MapCodec)
		if canSwissMapInC(mapDec.MapKind) {
			emitMapSwissInner(b, 0, mapDec.MapKind)
		} else {
			emitMapInner(b, 0, elemTI, mapDec)
		}

	case KindAny:
		b.emit(IRInst{
			Op:       opInterface,
			FieldOff: 0,
		})

	default:
		b.emit(IRInst{
			Op: opFallback,
			Fallback: &fbInfo{
				TI:     elemTI,
				Offset: 0,
			},
		})
	}
}

// emitMap emits MAP_BEGIN + value body + MAP_END for a map field.
// If the map supports Swiss Map key iteration, emits MAP_STR_ITER + body + MAP_STR_ITER_END instead.
func emitMap(b *irBuilder, fi *TypeInfo, fieldOff uintptr, mapDec *MapCodec) {
	if canSwissMapIterInC(mapDec) {
		emitMapSwissIter(b, fi, fieldOff, mapDec)
		return
	}
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	endLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opMapBegin,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		Target:   endLabel,
		Fallback: &fbInfo{
			TI:     fi,
			Offset: fieldOff,
		},
	})

	emitElementBody(b, mapDec.ValTI)

	b.defineLabel(endLabel)
	b.emit(IRInst{Op: opMapEnd})
}

// emitMapInner is like emitMap but without key bytes.
func emitMapInner(b *irBuilder, fieldOff uintptr, elemTI *TypeInfo, mapDec *MapCodec) {
	if canSwissMapIterInC(mapDec) {
		emitMapSwissIterInner(b, fieldOff, mapDec)
		return
	}
	endLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opMapBegin,
		FieldOff: uint16(fieldOff),
		Target:   endLabel,
		Fallback: &fbInfo{
			TI:     elemTI,
			Offset: fieldOff,
		},
	})

	emitElementBody(b, mapDec.ValTI)

	b.defineLabel(endLabel)
	b.emit(IRInst{Op: opMapEnd})
}

// seqOpcode returns the sequence iterator opcode for a given element TypeInfo,
// or 0 if the element type is not supported by a C-native sequence iterator.
// Supported: float64, int, int64, string (without custom marshalers).
func seqOpcode(elemTI *TypeInfo) uint16 {
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		return 0
	}
	switch elemTI.Kind {
	case KindFloat64:
		return opSeqFloat64
	case KindInt:
		return opSeqInt
	case KindInt64:
		return opSeqInt64
	case KindString:
		return opSeqString
	}
	return 0
}

// canSwissMapInC returns true if the given map variant can use C-native Swiss Map iteration.
func canSwissMapInC(variant MapVariant) bool {
	switch variant {
	case MapVariantStrStr:
		return SwissMapLayoutOK
	case MapVariantStrInt:
		return SwissMapStrIntLayoutOK
	case MapVariantStrInt64:
		return SwissMapStrInt64LayoutOK
	}
	return false
}

// canSwissMapIterInC returns true if the map can use the generic Swiss Map key
// iterator (MAP_STR_ITER/MAP_STR_ITER_END) for C-native key iteration with
// VM-dispatched value body instructions.
func canSwissMapIterInC(mapDec *MapCodec) bool {
	if !mapDec.isStringKey {
		return false
	}
	if mapDec.SlotSize == 0 {
		return false
	}
	return true
}

// swissMapSlotSize returns the slot size for the map, or 0 if unknown.
func swissMapSlotSize(mapDec *MapCodec) int32 {
	return int32(mapDec.SlotSize)
}

// swissMapOpcode returns the VM opcode for the given Swiss Map variant.
func swissMapOpcode(variant MapVariant) uint16 {
	switch variant {
	case MapVariantStrStr:
		return opMapStrStr
	case MapVariantStrInt:
		return opMapStrInt
	case MapVariantStrInt64:
		return opMapStrInt64
	}
	panic("unreachable")
}

func emitMapSwiss(b *irBuilder, fi *TypeInfo, fieldOff uintptr, variant MapVariant) {
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	b.emit(IRInst{
		Op:       swissMapOpcode(variant),
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

// emitMapSwissInner emits a keyless Swiss Map op (for deref/element body contexts).
func emitMapSwissInner(b *irBuilder, fieldOff uintptr, variant MapVariant) {
	b.emit(IRInst{
		Op:       swissMapOpcode(variant),
		FieldOff: uint16(fieldOff),
	})
}

// emitMapSwissIter emits MAP_STR_ITER + value body + MAP_STR_ITER_END for a struct field map.
func emitMapSwissIter(b *irBuilder, fi *TypeInfo, fieldOff uintptr, mapDec *MapCodec) {
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	slotSize := swissMapSlotSize(mapDec)

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opMapStrIter,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		OperandA: slotSize,
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, mapDec.ValTI)

	b.defineLabel(afterLabel)
	b.emit(IRInst{
		Op:       opMapStrIterEnd,
		LoopBack: bodyLabel,
		OperandB: slotSize,
	})
}

// emitMapSwissIterInner emits MAP_STR_ITER + value body + MAP_STR_ITER_END without key bytes.
func emitMapSwissIterInner(b *irBuilder, fieldOff uintptr, mapDec *MapCodec) {
	slotSize := swissMapSlotSize(mapDec)

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opMapStrIter,
		FieldOff: uint16(fieldOff),
		OperandA: slotSize,
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, mapDec.ValTI)

	b.defineLabel(afterLabel)
	b.emit(IRInst{
		Op:       opMapStrIterEnd,
		LoopBack: bodyLabel,
		OperandB: slotSize,
	})
}

// emitCall emits an OP_CALL instruction for a cycle-participating struct.
func emitCall(b *irBuilder, dec *StructCodec, fieldOff uintptr) {
	existingLabel := b.visiting[dec.Typ]

	if existingLabel != InvalidLabel {
		// Subroutine label already allocated — emit CALL directly.
		b.emit(IRInst{
			Op:         opCall,
			FieldOff:   uint16(fieldOff),
			Target:     existingLabel,
			Annotation: dec.Typ.String(),
		})
	} else {
		// Check if a subroutine is already pending for this type.
		// This happens when the type was previously encountered in a different
		// inline context, which scheduled a pendingSub but then cleaned up
		// b.visiting. Reuse the existing label instead of allocating a new one
		// that would never be defined.
		var targetLabel Label
		for _, p := range b.pendingSubs {
			if p.dec.Typ == dec.Typ {
				targetLabel = p.label
				break
			}
		}
		if targetLabel == InvalidLabel {
			// Not yet scheduled — allocate label and schedule.
			targetLabel = b.allocLabel()
			b.pendingSubs = append(b.pendingSubs, pendingSub{dec: dec, label: targetLabel})
		}

		b.visiting[dec.Typ] = targetLabel

		b.emit(IRInst{
			Op:         opCall,
			FieldOff:   uint16(fieldOff),
			Target:     targetLabel,
			Annotation: dec.Typ.String(),
		})
	}
}

func emitInterface(b *irBuilder, fi *TypeInfo, fieldOff uintptr) {
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	b.emit(IRInst{
		Op:       opInterface,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

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

// emitYield emits a single OP_FALLBACK instruction for Go fallback.
func emitYield(b *irBuilder, fi *TypeInfo, fieldOff uintptr, reason int32) {
	keyOff, keyLen, ok := b.addKey(fi.Ext.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	b.emit(IRInst{
		Op:       opFallback,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		Fallback: &fbInfo{
			TI:     fi,
			Offset: fieldOff,
			Reason: reason,
		},
	})
}

// emitYieldOverflow emits an OP_FALLBACK for a field whose field_off or key_len
// exceeds the uint16/uint8 range, or when the global key pool is full.
// The key bytes are not stored in the pool; instead Go reads them from
// fbInfo.TI.Ext.KeyBytes at yield time.
func emitYieldOverflow(b *irBuilder, fi *TypeInfo, fieldOff uintptr) {
	b.emit(IRInst{
		Op:       opFallback,
		FieldOff: 0,
		Fallback: &fbInfo{
			TI:     fi,
			Offset: fieldOff,
			Reason: fbReasonKeyPoolFull,
		},
	})
}

// emitSkipIfZero emits an OP_SKIP_IF_ZERO with a fixed skip distance.
// skipBytes is the total size of the instructions to skip (e.g. 16+8 = SKIP_IF_ZERO itself + one 8-byte instruction).
func emitSkipIfZero(b *irBuilder, _ *TypeInfo, fieldOff uintptr, skipBytes int, kind ElemTypeKind) {
	b.emit(IRInst{
		Op:       opSkipIfZero,
		FieldOff: uint16(fieldOff),
		OperandA: int32(skipBytes),
		OperandB: int32(kind),
	})
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
