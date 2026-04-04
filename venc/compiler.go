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

// fieldContext carries the struct field placement info needed by type emitters.
// Zero value means standalone/keyless context (top-level or nested element).
type fieldContext struct {
	FieldOff uintptr // field offset within struct
	KeyOff   uint16  // key position in global pool
	KeyLen   uint8   // key length; 0 = no key
}

// fieldFBInfo constructs a full field-level fbInfo.
func fieldFBInfo(fi *EncFieldInfo, fc fieldContext, reason int32) *fbInfo {
	return &fbInfo{
		TI:       fi.Type,
		Offset:   fc.FieldOff,
		Reason:   reason,
		TagFlags: fi.TagFlags,
		KeyBytes: fi.KeyBytes,
		IsZeroFn: fi.IsZeroFn,
	}
}

// typeFBInfo constructs a minimal type-level fbInfo (no field metadata).
func typeFBInfo(ti *EncTypeInfo, offset uintptr, reason int32) *fbInfo {
	return &fbInfo{
		TI:     ti,
		Offset: offset,
		Reason: reason,
	}
}

// ── Blueprint compiler ──────────────────────────────────────────────

// compileBlueprint compiles an EncTypeInfo into a flat VM program.
func compileBlueprint(et *EncTypeInfo) *Blueprint {
	b := &irBuilder{
		visiting: make(map[*EncTypeInfo]Label),
	}

	// Emit the type-specific body.
	switch et.Kind {
	case typ.KindStruct:
		rootLabel := b.allocLabel()
		b.visiting[et] = rootLabel
		b.defineLabel(rootLabel)
		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: typeName(et.Type),
			SourceType: et.Type,
		})
		emitStructBody(b, et.ResolveStruct(), 0)
		b.emit(IRInst{Op: opObjClose})
	case typ.KindSlice:
		si := et.ResolveSlice()
		if si.ElemType.Kind == typ.KindUint8 && si.ElemSize == 1 {
			b.emit(IRInst{Op: opByteSlice, FieldOff: 0})
		} else {
			emitSlice(b, et, fieldContext{})
		}
	case typ.KindArray:
		emitArray(b, et, fieldContext{})
	case typ.KindMap:
		emitMap(b, et, fieldContext{})
	}

	b.emit(IRInst{Op: opRet})

	// Drain cycle subroutines (only struct produces these).
	drainCycleSubs(b)

	insts := runPasses(b.insts, b.nextLabel)
	ops, fallbacks, annotations := lower(insts)

	return &Blueprint{
		Name:        et.Type.String(),
		Ops:         alignedOps(ops),
		Fallbacks:   fallbacks,
		Annotations: annotations,
	}
}

// drainCycleSubs emits subroutines for cycle-participating structs.
func drainCycleSubs(b *irBuilder) {
	for len(b.cycleSubs) > 0 {
		sub := b.cycleSubs[0]
		b.cycleSubs = b.cycleSubs[1:]

		b.visiting[sub.ti] = sub.label
		b.defineLabel(sub.label)

		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: typeName(sub.ti.Type),
			SourceType: sub.ti.Type,
		})
		emitStructBody(b, sub.ti.ResolveStruct(), 0)
		b.emit(IRInst{Op: opObjClose})
		b.emit(IRInst{Op: opRet})
	}
}

// ── Struct field dispatch ───────────────────────────────────────────

// emitStructBody emits the field program for one struct body.
func emitStructBody(b *irBuilder, si *EncStructInfo, baseOff uintptr) {
	for i := range si.Fields {
		fi := &si.Fields[i]
		fieldOff := baseOff + fi.Offset

		needsOmitempty := fi.TagFlags&EncTagFlagOmitEmpty != 0

		// Early bail: field offset or key length exceeds native limits.
		if fieldOff > math.MaxUint16 || len(fi.KeyBytes) > 255 {
			emitFieldFallbackOverflow(b, fi, fieldOff)
			continue
		}

		// Reserve the key in the global pool. All paths below need it.
		fc, ok := addKeyForField(b, fi, fieldOff)
		if !ok {
			emitFieldFallbackOverflow(b, fi, fieldOff)
			continue
		}

		// time.Time has native VM support; other marshal hooks → fallback.
		if fi.Type.TypeFlags&(EncTypeFlagHasMarshalFn|EncTypeFlagHasTextMarshalFn) != 0 {
			if isTimeType(fi.Type) {
				if needsOmitempty {
					emitSkipIfZero(b, fieldOff, 16+8, fi.Type.Kind)
				}
				emitTime(b, fi.Type, fc, fieldFBInfo(fi, fc, fbReasonMarshaler))
				continue
			}
			if needsOmitempty {
				emitSkipIfZero(b, fieldOff, 16+8, fi.Type.Kind)
			}
			reason := fallbackReasonFromFlags(fi.Type.TypeFlags, fi.TagFlags)
			emitFieldFallback(b, fc, fieldFBInfo(fi, fc, reason))
			continue
		}

		// `,string` tag: only int/int64 stays native.
		if fi.TagFlags&EncTagFlagQuoted != 0 {
			if fi.Type.Kind == typ.KindInt || fi.Type.Kind == typ.KindInt64 {
				if needsOmitempty {
					emitSkipIfZero(b, fieldOff, 16+8, fi.Type.Kind)
				}
				emitQuotedInt(b, fi.Type.Kind, fc)
				continue
			}
			if needsOmitempty {
				emitSkipIfZero(b, fieldOff, 16+8, fi.Type.Kind)
			}
			emitFieldFallback(b, fc, fieldFBInfo(fi, fc, fbReasonQuoted))
			continue
		}

		switch fi.Type.Kind {
		case typ.KindBool,
			typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
			typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
			typ.KindFloat32, typ.KindFloat64,
			typ.KindString,
			typ.KindNumber:
			if needsOmitempty {
				emitSkipIfZero(b, fieldOff, 16+8, fi.Type.Kind)
			}
			emitPrimitive(b, fi.Type.Kind, fc)

		case typ.KindStruct:
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(fi.Type.Kind),
				})
				emitNestedStruct(b, fi.Type, fc)
				b.defineLabel(afterLabel)
			} else {
				emitNestedStruct(b, fi.Type, fc)
			}

		case typ.KindPointer:
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(typ.KindPointer),
				})
				emitPointer(b, fi.Type, fc)
				b.defineLabel(afterLabel)
			} else {
				emitPointer(b, fi.Type, fc)
			}

		case typ.KindSlice:
			si := fi.Type.ResolveSlice()
			// []byte stays native via OP_BYTE_SLICE.
			if si.ElemType.Kind == typ.KindUint8 && si.ElemSize == 1 {
				if needsOmitempty {
					emitSkipIfZero(b, fieldOff, 16+8, typ.KindSlice)
				}
				emitByteSlice(b, fc)
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
				emitSlice(b, fi.Type, fc)
				b.defineLabel(afterLabel)
			} else {
				emitSlice(b, fi.Type, fc)
			}

		case typ.KindArray:
			ai := fi.Type.ResolveArray()
			// [N]byte still goes through the Go base64 path.
			if ai.ElemType.Kind == typ.KindUint8 && ai.ElemSize == 1 {
				emitFieldFallback(b, fc, fieldFBInfo(fi, fc, fbReasonByteArray))
				continue
			}
			emitArray(b, fi.Type, fc)

		case typ.KindMap:
			mi := fi.Type.ResolveMap()
			if needsOmitempty {
				afterLabel := b.allocLabel()
				b.emit(IRInst{
					Op:       opSkipIfZero,
					FieldOff: uint16(fieldOff),
					Target:   afterLabel,
					OperandB: int32(typ.KindMap),
				})
				if canSwissMapInC(mi.MapKind) {
					emitMapSwiss(b, fi.Type, fc)
				} else {
					emitMap(b, fi.Type, fc)
				}
				b.defineLabel(afterLabel)
			} else if canSwissMapInC(mi.MapKind) {
				emitMapSwiss(b, fi.Type, fc)
			} else {
				emitMap(b, fi.Type, fc)
			}

		case typ.KindAny:
			if needsOmitempty {
				emitSkipIfZero(b, fieldOff, 16+8, typ.KindAny)
			}
			emitInterface(b, fc)

		case typ.KindIface:
			if needsOmitempty {
				emitSkipIfZero(b, fieldOff, 16+8, typ.KindIface)
			}
			emitFieldFallback(b, fc, fieldFBInfo(fi, fc, fbReasonIface))

		default:
			if needsOmitempty {
				emitSkipIfZero(b, fieldOff, 16+8, fi.Type.Kind)
			}
			emitFieldFallback(b, fc, fieldFBInfo(fi, fc, fbReasonUnknown))
		}
	}
}

// addKeyForField reserves key bytes and builds a fieldContext.
func addKeyForField(b *irBuilder, fi *EncFieldInfo, fieldOff uintptr) (fieldContext, bool) {
	off, klen, ok := b.addKey(fi.KeyBytes)
	return fieldContext{FieldOff: fieldOff, KeyOff: off, KeyLen: klen}, ok
}

// ── Field-level fallback helpers (need field metadata) ──────────────

// emitFieldFallback emits an opFallback with pre-resolved key and field metadata.
func emitFieldFallback(b *irBuilder, fc fieldContext, fb *fbInfo) {
	b.emit(IRInst{
		Op:       opFallback,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
		Fallback: fb,
	})
}

// emitFieldFallbackOverflow emits a fallback for fields exceeding native encoding limits.
// Called before addKey (fieldOff too large or keyBytes too long).
func emitFieldFallbackOverflow(b *irBuilder, fi *EncFieldInfo, fieldOff uintptr) {
	b.emit(IRInst{
		Op:       opFallback,
		FieldOff: 0,
		Fallback: fieldFBInfo(fi, fieldContext{FieldOff: fieldOff}, fbReasonOverflow),
	})
}

// ── Type-level emitters (no field knowledge) ────────────────────────

// emitPrimitive emits one primitive opcode, upgrading hot cases to keyed variants.
func emitPrimitive(b *irBuilder, kind typ.ElemTypeKind, fc fieldContext) {
	op := kindToOpcode(kind)
	if fc.KeyLen > 0 {
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
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
	})
}

// emitQuotedInt emits the native `,string` int/int64 opcodes.
func emitQuotedInt(b *irBuilder, kind typ.ElemTypeKind, fc fieldContext) {
	var op uint16
	switch kind {
	case typ.KindInt:
		op = opKQInt
	case typ.KindInt64:
		op = opKQInt64
	default:
		panic("emitQuotedInt: unsupported kind")
	}
	b.emit(IRInst{
		Op:       op,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
	})
}

// emitNestedStruct inlines a nested struct body unless cycles force a call.
func emitNestedStruct(b *irBuilder, ti *EncTypeInfo, fc fieldContext) {
	b.emit(IRInst{
		Op:         opObjOpen,
		KeyLen:     fc.KeyLen,
		KeyOff:     fc.KeyOff,
		Annotation: typeName(ti.Type),
		SourceType: ti.Type,
	})

	prevLabel, wasVisiting := b.visiting[ti]
	if !wasVisiting {
		b.visiting[ti] = InvalidLabel
	}

	emitStructBody(b, ti.ResolveStruct(), fc.FieldOff)

	if !wasVisiting {
		delete(b.visiting, ti)
	} else {
		b.visiting[ti] = prevLabel
	}

	b.emit(IRInst{Op: opObjClose})
}

// emitPointer wraps the pointee body in PTR_DEREF/PTR_END.
func emitPointer(b *irBuilder, ti *EncTypeInfo, fc fieldContext) {
	elemTI := ti.ResolvePointer().ElemType
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opPtrDeref,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
		Target:   afterLabel,
	})

	emitTypeBody(b, elemTI)

	b.emit(IRInst{Op: opPtrEnd})

	b.defineLabel(afterLabel)
}

// emitSlice emits a slice loop or a native sequence opcode.
func emitSlice(b *irBuilder, ti *EncTypeInfo, fc fieldContext) {
	si := ti.ResolveSlice()
	if seqOp := seqOpcode(si.ElemType); seqOp != 0 {
		b.emit(IRInst{
			Op:       seqOp,
			KeyLen:   fc.KeyLen,
			KeyOff:   fc.KeyOff,
			FieldOff: uint16(fc.FieldOff),
			OperandA: 0, // slice: operand_a == 0
		})
		return
	}

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opSliceBegin,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
		OperandA: int32(si.ElemSize),
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitTypeBody(b, si.ElemType)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(si.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitArray emits an array loop or a native sequence opcode.
func emitArray(b *irBuilder, ti *EncTypeInfo, fc fieldContext) {
	ai := ti.ResolveArray()
	if seqOp := seqOpcode(ai.ElemType); seqOp != 0 {
		packed := int32(ai.ElemSize&0xFFFF) | int32(ai.ArrayLen&0xFFFF)<<16
		b.emit(IRInst{
			Op:       seqOp,
			KeyLen:   fc.KeyLen,
			KeyOff:   fc.KeyOff,
			FieldOff: uint16(fc.FieldOff),
			OperandA: packed, // array: operand_a != 0
		})
		return
	}

	packed := int32(ai.ElemSize&0xFFFF) | int32(ai.ArrayLen&0xFFFF)<<16

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opArrayBegin,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
		OperandA: packed,
		Target:   afterLabel,
	})

	b.defineLabel(bodyLabel)

	emitTypeBody(b, ai.ElemType)

	b.emit(IRInst{
		Op:       opSliceEnd,
		LoopBack: bodyLabel,
		OperandB: int32(ai.ElemSize),
	})

	b.defineLabel(afterLabel)
}

// emitMap dispatches map encoding: Swiss-map iter, Swiss-map opcode, or Go fallback.
func emitMap(b *irBuilder, mapTI *EncTypeInfo, fc fieldContext) {
	mi := mapTI.ResolveMap()
	if canSwissMapIterInC(mi) {
		emitMapSwissIter(b, mapTI, fc)
		return
	}

	b.emit(IRInst{
		Op:       opMap,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
		Fallback: &fbInfo{
			TI:     mapTI,
			Offset: fc.FieldOff,
		},
	})
}

// emitMapSwiss emits the specialized Swiss-map opcode (map[string]string, etc.).
// In keyless context, attaches a fallback for Go VM delegation.
func emitMapSwiss(b *irBuilder, ti *EncTypeInfo, fc fieldContext) {
	inst := IRInst{
		Op:       swissMapOpcode(ti.ResolveMap().MapKind),
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
	}
	inst.Fallback = &fbInfo{TI: ti, Offset: fc.FieldOff}
	b.emit(inst)
}

// emitMapSwissIter emits MAP_STR_ITER around the value body.
func emitMapSwissIter(b *irBuilder, ti *EncTypeInfo, fc fieldContext) {
	mi := ti.ResolveMap()
	slotSize := swissMapSlotSize(mi)

	bodyLabel := b.allocLabel()
	afterLabel := b.allocLabel()

	b.emit(IRInst{
		Op:       opMapStrIter,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
		OperandA: slotSize,
		Target:   afterLabel,
		Fallback: &fbInfo{TI: ti, Offset: fc.FieldOff},
	})

	b.defineLabel(bodyLabel)

	emitTypeBody(b, mi.ValType)

	b.defineLabel(afterLabel)
	b.emit(IRInst{
		Op:       opMapStrIterEnd,
		LoopBack: bodyLabel,
		OperandB: slotSize,
	})
}

// emitInterface emits the opInterface instruction.
func emitInterface(b *irBuilder, fc fieldContext) {
	b.emit(IRInst{
		Op:       opInterface,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
	})
}

// emitByteSlice emits the native []byte base64 opcode.
func emitByteSlice(b *irBuilder, fc fieldContext) {
	b.emit(IRInst{
		Op:       opByteSlice,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
	})
}

// emitTime emits the native time.Time opcode with fallback metadata.
func emitTime(b *irBuilder, ti *EncTypeInfo, fc fieldContext, fb *fbInfo) {
	b.emit(IRInst{
		Op:       opTime,
		KeyLen:   fc.KeyLen,
		KeyOff:   fc.KeyOff,
		FieldOff: uint16(fc.FieldOff),
		Fallback: fb,
	})
}

// emitSkipIfZero emits a fixed-distance omitempty jump.
func emitSkipIfZero(b *irBuilder, fieldOff uintptr, skipBytes int, kind typ.ElemTypeKind) {
	b.emit(IRInst{
		Op:       opSkipIfZero,
		FieldOff: uint16(fieldOff),
		OperandA: int32(skipBytes),
		OperandB: int32(kind),
	})
}

// ── Type body dispatcher (keyless context) ──────────────────────────

// emitTypeBody emits the keyless body for a given type, used after pointer
// dereference, inside slice/array/map iteration, and other non-field contexts.
func emitTypeBody(b *irBuilder, elemTI *EncTypeInfo) {
	// time.Time stays native; other marshal hooks → fallback.
	if elemTI.TypeFlags&(EncTypeFlagHasMarshalFn|EncTypeFlagHasTextMarshalFn) != 0 {
		if isTimeType(elemTI) {
			emitTime(b, elemTI, fieldContext{}, typeFBInfo(elemTI, 0, fbReasonMarshaler))
			return
		}
		b.emit(IRInst{
			Op:       opFallback,
			Fallback: typeFBInfo(elemTI, 0, fbReasonUnknown),
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
		if _, visiting := b.visiting[elemTI]; visiting {
			emitCall(b, elemTI, 0)
			return
		}
		b.visiting[elemTI] = InvalidLabel
		b.emit(IRInst{
			Op:         opObjOpen,
			Annotation: typeName(elemTI.Type),
			SourceType: elemTI.Type,
		})
		emitStructBody(b, elemTI.ResolveStruct(), 0)
		b.emit(IRInst{Op: opObjClose})
		delete(b.visiting, elemTI)

	case typ.KindSlice:
		sliceSI := elemTI.ResolveSlice()
		if sliceSI.ElemType.Kind == typ.KindUint8 && sliceSI.ElemSize == 1 {
			b.emit(IRInst{Op: opByteSlice, FieldOff: 0})
		} else {
			emitSlice(b, elemTI, fieldContext{})
		}

	case typ.KindArray:
		emitArray(b, elemTI, fieldContext{})

	case typ.KindMap:
		mi := elemTI.ResolveMap()
		if canSwissMapInC(mi.MapKind) {
			emitMapSwiss(b, elemTI, fieldContext{})
		} else {
			emitMap(b, elemTI, fieldContext{})
		}

	case typ.KindAny:
		b.emit(IRInst{
			Op:       opInterface,
			FieldOff: 0,
		})

	case typ.KindPointer:
		afterLabel := b.allocLabel()
		b.emit(IRInst{
			Op:     opPtrDeref,
			Target: afterLabel,
		})
		emitTypeBody(b, elemTI.ResolvePointer().ElemType)
		b.emit(IRInst{Op: opPtrEnd})
		b.defineLabel(afterLabel)

	default:
		b.emit(IRInst{
			Op:       opFallback,
			Fallback: typeFBInfo(elemTI, 0, fbReasonUnknown),
		})
	}
}

// ── Cycle detection ─────────────────────────────────────────────────

// emitCall reuses or schedules the subroutine for a cycle-participating struct.
func emitCall(b *irBuilder, ti *EncTypeInfo, fieldOff uintptr) {
	existingLabel := b.visiting[ti]

	if existingLabel != InvalidLabel {
		b.emit(IRInst{
			Op:         opCall,
			FieldOff:   uint16(fieldOff),
			Target:     existingLabel,
			Annotation: typeName(ti.Type),
		})
	} else {
		// Reuse an already pending subroutine label instead of scheduling a dead duplicate.
		var targetLabel Label
		for _, p := range b.cycleSubs {
			if p.ti == ti {
				targetLabel = p.label
				break
			}
		}
		if targetLabel == InvalidLabel {
			targetLabel = b.allocLabel()
			b.cycleSubs = append(b.cycleSubs, cycleSub{ti: ti, label: targetLabel})
		}

		b.visiting[ti] = targetLabel

		b.emit(IRInst{
			Op:         opCall,
			FieldOff:   uint16(fieldOff),
			Target:     targetLabel,
			Annotation: typeName(ti.Type),
		})
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

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

func isTimeType(ti *EncTypeInfo) bool {
	if ti.UniType == nil {
		return false
	}
	if ti.Kind != typ.KindStruct {
		return false
	}
	return ti.Type == timeType
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
	return mi.IsStringKey && mi.SlotSize != 0
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
