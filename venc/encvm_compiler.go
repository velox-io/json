package venc

import (
	"math"
	"reflect"
	"strings"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// typeName keeps trace output compact for anonymous structs.
func typeName(t reflect.Type) string {
	s := t.String()
	if !strings.HasPrefix(s, "struct {") {
		return s
	}
	const maxLen = 48
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + " ...}"
}

// alignedOps gives the C VM an 8-byte-aligned instruction buffer.
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

// compileBlueprint lowers a struct into one flat VM program.
func compileBlueprint(si *EncStructInfo) *Blueprint {
	b := &irBuilder{
		visiting: make(map[reflect.Type]Label),
	}

	rootLabel := b.allocLabel()
	b.visiting[si.Type] = rootLabel
	b.defineLabel(rootLabel)

	b.emit(IRInst{
		Op:         opObjOpen,
		Annotation: typeName(si.Type),
		SourceType: si.Type,
	})

	emitStructBody(b, si, 0)

	b.emit(IRInst{Op: opObjClose})

	b.emit(IRInst{Op: opRet})

	emitPendingSubs(b)

	insts := runPasses(b.insts, b.nextLabel)

	ops, fallbacks, annotations := lower(insts)

	return &Blueprint{
		Name:        typeName(si.Type),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// emitPendingSubs drains the cycle subroutine queue.
func emitPendingSubs(b *irBuilder) {
	for len(b.pendingSubs) > 0 {
		sub := b.pendingSubs[0]
		b.pendingSubs = b.pendingSubs[1:]

		subLabel := sub.label

		b.visiting[sub.si.Type] = subLabel

		b.defineLabel(subLabel)

		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: sub.si.Type.String(),
			SourceType: sub.si.Type,
		})
		emitStructBody(b, sub.si, 0)
		b.emit(IRInst{Op: opObjClose})
		b.emit(IRInst{Op: opRet})
	}
}

// compileStandaloneSliceBlueprint handles slice types discovered at runtime.
func compileStandaloneSliceBlueprint(si *EncSliceInfo) *Blueprint {
	b := &irBuilder{
		visiting: make(map[reflect.Type]Label),
	}

	if si.ElemTI.Kind == typ.KindUint8 && si.ElemSize == 1 {
		b.emit(IRInst{Op: opByteSlice, FieldOff: 0})
	} else {
		emitSliceInner(b, 0, si)
	}
	b.emit(IRInst{Op: opRet})

	ops, fallbacks, annotations := lower(b.insts)

	return &Blueprint{
		Name:        si.SliceType.String(),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// compileStandaloneMapBlueprint handles runtime-discovered map types.
func compileStandaloneMapBlueprint(mi *EncMapInfo) *Blueprint {
	b := &irBuilder{
		visiting: make(map[reflect.Type]Label),
	}

	mapTI := EncTypeInfoOf(mi.MapType)
	emitMapInner(b, 0, mapTI, mi)
	b.emit(IRInst{Op: opRet})

	ops, fallbacks, annotations := lower(b.insts)

	return &Blueprint{
		Name:        mi.MapType.String(),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// compileStandaloneArrayBlueprint handles runtime-discovered array types.
func compileStandaloneArrayBlueprint(ai *EncArrayInfo) *Blueprint {
	b := &irBuilder{
		visiting: make(map[reflect.Type]Label),
	}

	emitArrayInner(b, 0, ai)
	b.emit(IRInst{Op: opRet})

	ops, fallbacks, annotations := lower(b.insts)

	return &Blueprint{
		Name:        ai.ArrayType.String(),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// emitStructBody emits the field program for one struct body.
func emitStructBody(b *irBuilder, si *EncStructInfo, baseOff uintptr) {
	for i := range si.Fields {
		fi := &si.Fields[i]
		fieldOff := baseOff + fi.Offset

		needsOmitempty := fi.TagFlags&EncTagFlagOmitEmpty != 0

		if fieldOff > math.MaxUint16 {
			emitYieldOverflow(b, fi, fieldOff)
			continue
		}

		if len(fi.KeyBytes) > 255 {
			emitYieldOverflow(b, fi, fieldOff)
			continue
		}

		// time.Time has native VM support; other marshal hooks yield.
		if fi.TypeFlags&(EncTypeFlagHasMarshalFn|EncTypeFlagHasTextMarshalFn) != 0 {
			if isTimeType(fi) {
				if needsOmitempty {
					emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
				}
				emitTime(b, fi, fieldOff)
				continue
			}
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitYield(b, fi, fieldOff, fallbackReasonFromFlags(fi.TypeFlags, fi.TagFlags))
			continue
		}

		// Only int/int64 `,string` stays native.
		if fi.TagFlags&EncTagFlagQuoted != 0 {
			if fi.Kind == typ.KindInt || fi.Kind == typ.KindInt64 {
				if needsOmitempty {
					emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
				}
				emitQuotedInt(b, fi, fieldOff)
				continue
			}
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitYield(b, fi, fieldOff, fbReasonQuoted)
			continue
		}

		switch fi.Kind {
		case typ.KindBool,
			typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
			typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
			typ.KindFloat32, typ.KindFloat64,
			typ.KindString,
			typ.KindNumber:
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitPrimitive(b, fi, fieldOff)

		case typ.KindStruct:
			subSI := fi.ResolveStruct()
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(fi.Kind),
				})
				emitNestedStruct(b, fi, fieldOff, subSI)
				b.defineLabel(afterLabel)
			} else {
				emitNestedStruct(b, fi, fieldOff, subSI)
			}

		case typ.KindPointer:
			pDec := fi.ResolvePointer()
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(typ.KindPointer),
				})
				emitPointer(b, fi, fieldOff, pDec)
				b.defineLabel(afterLabel)
			} else {
				emitPointer(b, fi, fieldOff, pDec)
			}

		case typ.KindSlice:
			sliceSI := fi.ResolveSlice()
			// []byte stays native via OP_BYTE_SLICE.
			if sliceSI.ElemTI.Kind == typ.KindUint8 && sliceSI.ElemSize == 1 {
				if needsOmitempty {
					emitSkipIfZero(b, fi, fieldOff, 16+8, typ.KindSlice)
				}
				emitByteSlice(b, fi, fieldOff)
				continue
			}
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(typ.KindSlice),
				})
				emitSlice(b, fi, fieldOff, sliceSI)
				b.defineLabel(afterLabel)
			} else {
				emitSlice(b, fi, fieldOff, sliceSI)
			}

		case typ.KindArray:
			ai := fi.ResolveArray()
			// [N]byte still goes through the Go base64 path.
			if ai.ElemTI.Kind == typ.KindUint8 && ai.ElemSize == 1 {
				emitYield(b, fi, fieldOff, fbReasonByteArray)
				continue
			}
			emitArray(b, fi, fieldOff, ai)

		case typ.KindMap:
			mi := fi.ResolveMap()
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(typ.KindMap),
				})
				if canSwissMapInC(mi.MapKind) {
					emitMapSwiss(b, fi, fieldOff, mi.MapKind)
				} else {
					emitMap(b, fi, fieldOff, mi)
				}
				b.defineLabel(afterLabel)
			} else if canSwissMapInC(mi.MapKind) {
				emitMapSwiss(b, fi, fieldOff, mi.MapKind)
			} else {
				emitMap(b, fi, fieldOff, mi)
			}

		case typ.KindAny:
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, typ.KindAny)
			}
			emitInterface(b, fi, fieldOff)

		case typ.KindIface:
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, typ.KindIface)
			}
			emitYield(b, fi, fieldOff, fbReasonUnknown)

		default:
			if needsOmitempty {
				emitSkipIfZero(b, fi, fieldOff, 16+8, fi.Kind)
			}
			emitYield(b, fi, fieldOff, fbReasonUnknown)
		}
	}
}

// emitPrimitive emits one primitive opcode and upgrades hot keyed cases to branchless keyed variants.
func emitPrimitive(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
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

// emitQuotedInt emits the native `,string` int/int64 opcodes.
func emitQuotedInt(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	var op uint16
	switch fi.Kind {
	case typ.KindInt:
		op = opKQInt
	case typ.KindInt64:
		op = opKQInt64
	default:
		panic("emitQuotedInt: unsupported kind")
	}
	b.emit(IRInst{
		Op:       op,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

// emitNestedStruct inlines a nested struct body unless cycles force a call.
func emitNestedStruct(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, subSI *EncStructInfo) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}

	b.emit(IRInst{
		Op:         opObjOpen,
		KeyLen:     keyLen,
		KeyOff:     keyOff,
		Annotation: typeName(subSI.Type),
		SourceType: subSI.Type,
	})

	prevLabel, wasVisiting := b.visiting[subSI.Type]
	if !wasVisiting {
		b.visiting[subSI.Type] = InvalidLabel
	}

	emitStructBody(b, subSI, fieldOff)

	if !wasVisiting {
		delete(b.visiting, subSI.Type)
	} else {
		b.visiting[subSI.Type] = prevLabel
	}

	b.emit(IRInst{Op: opObjClose})
}

// emitPointer wraps the pointee body in PTR_DEREF/PTR_END.
func emitPointer(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, pDec *EncPointerInfo) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
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

	emitDerefBody(b, elemTI)

	b.emit(IRInst{Op: opPtrEnd})

	b.defineLabel(afterLabel)
}

// emitDerefBody emits the keyless body used after pointer dereference.
func emitDerefBody(b *irBuilder, elemTI *EncTypeInfo) {
	// time.Time stays native; other marshal hooks yield.
	if elemTI.TypeFlags&(EncTypeFlagHasMarshalFn|EncTypeFlagHasTextMarshalFn) != 0 {
		if isTimeType(elemTI) {
			emitTimeInner(b, elemTI, 0)
			return
		}
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
	case typ.KindBool,
		typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
		typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
		typ.KindFloat32, typ.KindFloat64,
		typ.KindString,
		typ.KindNumber:
		b.emit(IRInst{
			Op:       kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case typ.KindStruct:
		subSI := elemTI.ResolveStruct()
		if _, visiting := b.visiting[subSI.Type]; visiting {
			emitCall(b, subSI, 0)
			return
		}
		b.visiting[subSI.Type] = InvalidLabel
		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: typeName(subSI.Type),
			SourceType: subSI.Type,
		})
		emitStructBody(b, subSI, 0)
		b.emit(IRInst{Op: opObjClose})
		delete(b.visiting, subSI.Type)

	case typ.KindSlice:
		sliceSI := elemTI.ResolveSlice()
		if sliceSI.ElemTI.Kind == typ.KindUint8 && sliceSI.ElemSize == 1 {
			b.emit(IRInst{Op: opByteSlice, FieldOff: 0})
		} else {
			emitSliceInner(b, 0, sliceSI)
		}

	case typ.KindArray:
		ai := elemTI.ResolveArray()
		emitArrayInner(b, 0, ai)

	case typ.KindMap:
		mi := elemTI.ResolveMap()
		if canSwissMapInC(mi.MapKind) {
			emitMapSwissInner(b, 0, mi.MapKind, elemTI)
		} else {
			emitMapInner(b, 0, elemTI, mi)
		}

	case typ.KindAny:
		b.emit(IRInst{
			Op:       opInterface,
			FieldOff: 0,
		})

	case typ.KindPointer:
		innerDec := elemTI.ResolvePointer()
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

// emitSlice emits a slice loop or a native sequence opcode for supported element types.
func emitSlice(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, si *EncSliceInfo) {
	if seqOp := seqOpcode(si.ElemTI); seqOp != 0 {
		keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
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

	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
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
		OperandA: int32(si.ElemSize),
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, si.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(si.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitSliceInner is the keyless slice form used by deref/element paths.
func emitSliceInner(b *irBuilder, fieldOff uintptr, si *EncSliceInfo) {
	if seqOp := seqOpcode(si.ElemTI); seqOp != 0 {
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
		OperandA: int32(si.ElemSize),
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, si.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(si.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitArray emits an array loop or a native sequence opcode for supported element types.
func emitArray(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, ai *EncArrayInfo) {
	if seqOp := seqOpcode(ai.ElemTI); seqOp != 0 {
		keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
		if !ok {
			emitYieldOverflow(b, fi, fieldOff)
			return
		}
		packed := int32(ai.ElemSize&0xFFFF) | int32(ai.ArrayLen&0xFFFF)<<16
		b.emit(IRInst{
			Op:       seqOp,
			KeyLen:   keyLen,
			KeyOff:   keyOff,
			FieldOff: uint16(fieldOff),
			OperandA: packed, // array: operand_a != 0
		})
		return
	}

	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	packed := int32(ai.ElemSize&0xFFFF) | int32(ai.ArrayLen&0xFFFF)<<16

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

	emitElementBody(b, ai.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(ai.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitArrayInner is the keyless array form used by deref/element paths.
func emitArrayInner(b *irBuilder, fieldOff uintptr, ai *EncArrayInfo) {
	if seqOp := seqOpcode(ai.ElemTI); seqOp != 0 {
		packed := int32(ai.ElemSize&0xFFFF) | int32(ai.ArrayLen&0xFFFF)<<16
		b.emit(IRInst{
			Op:       seqOp,
			FieldOff: uint16(fieldOff),
			OperandA: packed, // array: operand_a != 0
		})
		return
	}

	packed := int32(ai.ElemSize&0xFFFF) | int32(ai.ArrayLen&0xFFFF)<<16

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opArrayBegin,
		FieldOff: uint16(fieldOff),
		OperandA: packed,
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, ai.ElemTI)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(ai.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitElementBody emits the keyless body used inside slice/array/map iteration.
func emitElementBody(b *irBuilder, elemTI *EncTypeInfo) {
	if elemTI.TypeFlags&(EncTypeFlagHasMarshalFn|EncTypeFlagHasTextMarshalFn) != 0 {
		if isTimeType(elemTI) {
			emitTimeInner(b, elemTI, 0)
			return
		}
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
	case typ.KindBool,
		typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
		typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
		typ.KindFloat32, typ.KindFloat64,
		typ.KindString,
		typ.KindNumber:
		b.emit(IRInst{
			Op:       kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case typ.KindStruct:
		subSI := elemTI.ResolveStruct()
		if _, visiting := b.visiting[subSI.Type]; visiting {
			emitCall(b, subSI, 0)
			return
		}
		b.visiting[subSI.Type] = InvalidLabel
		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: typeName(subSI.Type),
			SourceType: subSI.Type,
		})
		emitStructBody(b, subSI, 0)
		b.emit(IRInst{Op: opObjClose})
		delete(b.visiting, subSI.Type)

	case typ.KindPointer:
		pDec := elemTI.ResolvePointer()
		afterLabel := b.allocLabel()
		b.emit(IRInst{
			Op:     opPtrDeref,
			Target: afterLabel,
		})
		emitDerefBody(b, pDec.ElemTI)
		b.emit(IRInst{Op: opPtrEnd})
		b.defineLabel(afterLabel)

	case typ.KindSlice:
		sliceSI := elemTI.ResolveSlice()
		if sliceSI.ElemTI.Kind == typ.KindUint8 && sliceSI.ElemSize == 1 {
			b.emit(IRInst{Op: opByteSlice, FieldOff: 0})
		} else {
			emitSliceInner(b, 0, sliceSI)
		}

	case typ.KindArray:
		ai := elemTI.ResolveArray()
		emitArrayInner(b, 0, ai)

	case typ.KindMap:
		mi := elemTI.ResolveMap()
		if canSwissMapInC(mi.MapKind) {
			emitMapSwissInner(b, 0, mi.MapKind, elemTI)
		} else {
			emitMapInner(b, 0, elemTI, mi)
		}

	case typ.KindAny:
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

// emitMap uses Swiss-map iteration when possible and otherwise yields the whole map to Go.
func emitMap(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, mi *EncMapInfo) {
	if canSwissMapIterInC(mi) {
		emitMapSwissIter(b, fi, fieldOff, mi)
		return
	}
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}

	b.emit(IRInst{
		Op:       opMap,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		Fallback: &fbInfo{
			TI:     fi,
			Offset: fieldOff,
		},
	})
}

// emitMapInner is the keyless map form used by standalone and nested paths.
func emitMapInner(b *irBuilder, fieldOff uintptr, mapTI *EncTypeInfo, mi *EncMapInfo) {
	if canSwissMapIterInC(mi) {
		emitMapSwissIterInner(b, fieldOff, mi)
		return
	}

	b.emit(IRInst{
		Op:       opMap,
		FieldOff: uint16(fieldOff),
		Fallback: &fbInfo{
			TI:     mapTI,
			Offset: fieldOff,
		},
	})
}

// seqOpcode picks the native sequence iterator for the small set of supported element types.
func seqOpcode(elemTI *EncTypeInfo) uint16 {
	if elemTI.TypeFlags&(EncTypeFlagHasMarshalFn|EncTypeFlagHasTextMarshalFn) != 0 {
		return 0
	}
	switch elemTI.Kind {
	case typ.KindFloat64:
		return opSeqFloat64
	case typ.KindInt:
		return opSeqInt
	case typ.KindInt64:
		return opSeqInt64
	case typ.KindString:
		return opSeqString
	}
	return 0
}

// canSwissMapInC reports whether the specialized Swiss-map opcode is available.
func canSwissMapInC(variant typ.MapVariant) bool {
	switch variant {
	case typ.MapVariantStrStr:
		return SwissMapLayoutOK
	case typ.MapVariantStrInt:
		return SwissMapStrIntLayoutOK
	case typ.MapVariantStrInt64:
		return SwissMapStrInt64LayoutOK
	}
	return false
}

// canSwissMapIterInC reports whether MAP_STR_ITER can drive this map.
func canSwissMapIterInC(mi *EncMapInfo) bool {
	if !mi.IsStringKey {
		return false
	}
	if mi.SlotSize == 0 {
		return false
	}
	return true
}

func swissMapSlotSize(mi *EncMapInfo) int32 {
	return int32(mi.SlotSize)
}

func swissMapOpcode(variant typ.MapVariant) uint16 {
	switch variant {
	case typ.MapVariantStrStr:
		return opMapStrStr
	case typ.MapVariantStrInt:
		return opMapStrInt
	case typ.MapVariantStrInt64:
		return opMapStrInt64
	}
	panic("unreachable")
}

func emitMapSwiss(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, variant typ.MapVariant) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
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

// emitMapSwissInner emits the keyless specialized Swiss-map opcode with a
// fallback entry so the Go VM can delegate to Go-native map encoding.
func emitMapSwissInner(b *irBuilder, fieldOff uintptr, variant typ.MapVariant, ti *EncTypeInfo) {
	b.emit(IRInst{
		Op:       swissMapOpcode(variant),
		FieldOff: uint16(fieldOff),
		Fallback: &fbInfo{
			TI:     ti,
			Offset: fieldOff,
		},
	})
}

// emitMapSwissIter emits MAP_STR_ITER around the value body for a keyed field.
func emitMapSwissIter(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, mi *EncMapInfo) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	slotSize := swissMapSlotSize(mi)

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

	emitElementBody(b, mi.ValTI)

	b.defineLabel(afterLabel)
	b.emit(IRInst{
		Op:       opMapStrIterEnd,
		LoopBack: bodyLabel,
		OperandB: slotSize,
	})
}

// emitMapSwissIterInner is the keyless MAP_STR_ITER form.
func emitMapSwissIterInner(b *irBuilder, fieldOff uintptr, mi *EncMapInfo) {
	slotSize := swissMapSlotSize(mi)

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opMapStrIter,
		FieldOff: uint16(fieldOff),
		OperandA: slotSize,
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitElementBody(b, mi.ValTI)

	b.defineLabel(afterLabel)
	b.emit(IRInst{
		Op:       opMapStrIterEnd,
		LoopBack: bodyLabel,
		OperandB: slotSize,
	})
}

// emitCall reuses or schedules the subroutine used for a cycle-participating struct.
func emitCall(b *irBuilder, si *EncStructInfo, fieldOff uintptr) {
	existingLabel := b.visiting[si.Type]

	if existingLabel != InvalidLabel {
		b.emit(IRInst{
			Op:         opCall,
			FieldOff:   uint16(fieldOff),
			Target:     existingLabel,
			Annotation: typeName(si.Type),
		})
	} else {
		// Reuse an already pending subroutine label instead of scheduling a dead duplicate.
		var targetLabel Label
		for _, p := range b.pendingSubs {
			if p.si.Type == si.Type {
				targetLabel = p.label
				break
			}
		}
		if targetLabel == InvalidLabel {
			targetLabel = b.allocLabel()
			b.pendingSubs = append(b.pendingSubs, pendingSub{si: si, label: targetLabel})
		}

		b.visiting[si.Type] = targetLabel

		b.emit(IRInst{
			Op:         opCall,
			FieldOff:   uint16(fieldOff),
			Target:     targetLabel,
			Annotation: typeName(si.Type),
		})
	}
}

func emitInterface(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
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

func fallbackReasonFromFlags(typeFlags typ.TypeFlag, tagFlags typ.TagFlag) int32 {
	if typeFlags&EncTypeFlagHasMarshalFn != 0 {
		return fbReasonMarshaler
	}
	if typeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		return fbReasonTextMarshaler
	}
	if tagFlags&EncTagFlagQuoted != 0 {
		return fbReasonQuoted
	}
	return fbReasonUnknown
}

// emitYield attaches the Go fallback metadata used at yield time.
func emitYield(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr, reason int32) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
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

// emitYieldOverflow handles fields that cannot fit native field/key encoding limits.
func emitYieldOverflow(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr) {
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

// emitByteSlice emits the native []byte base64 opcode.
func emitByteSlice(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	b.emit(IRInst{
		Op:       opByteSlice,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
	})
}

func isTimeType(fi *EncTypeInfo) bool {
	if fi.Kind != typ.KindStruct {
		return false
	}
	if fi.UT == nil {
		return false
	}
	return fi.UT.Type == timeType
}

// emitTime emits the native time.Time opcode with Go fallback metadata for complex zones.
func emitTime(b *irBuilder, fi *EncTypeInfo, fieldOff uintptr) {
	keyOff, keyLen, ok := b.addKey(fi.KeyBytes)
	if !ok {
		emitYieldOverflow(b, fi, fieldOff)
		return
	}
	b.emit(IRInst{
		Op:       opTime,
		KeyLen:   keyLen,
		KeyOff:   keyOff,
		FieldOff: uint16(fieldOff),
		Fallback: &fbInfo{
			TI:     fi,
			Offset: fieldOff,
			Reason: fbReasonMarshaler,
		},
	})
}

// emitTimeInner is the keyless time.Time form.
func emitTimeInner(b *irBuilder, elemTI *EncTypeInfo, fieldOff uintptr) {
	b.emit(IRInst{
		Op:       opTime,
		FieldOff: uint16(fieldOff),
		Fallback: &fbInfo{
			TI:     elemTI,
			Offset: fieldOff,
			Reason: fbReasonMarshaler,
		},
	})
}

// emitSkipIfZero emits a fixed-distance omitempty jump.
func emitSkipIfZero(b *irBuilder, _ *EncTypeInfo, fieldOff uintptr, skipBytes int, kind typ.ElemTypeKind) {
	b.emit(IRInst{
		Op:       opSkipIfZero,
		FieldOff: uint16(fieldOff),
		OperandA: int32(skipBytes),
		OperandB: int32(kind),
	})
}
