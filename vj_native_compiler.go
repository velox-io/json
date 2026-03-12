package vjson

import (
	"reflect"
	"unsafe"
)

// ================================================================
// Blueprint Compiler — Native VM
//
// Compiles a StructCodec (type metadata) into a flat, linear
// instruction stream (Blueprint). The compiler recursively walks
// the type tree and emits instructions into a single []VjOpStep.
//
// Key invariants:
//   - One Blueprint per top-level struct type, cached in StructCodec
//   - All nested structs, pointers, slices are inlined
//   - No pointer-chasing at runtime (SubOps eliminated)
//   - KeyPool is a contiguous []byte holding all pre-encoded keys
// ================================================================

// blueprintBuilder accumulates instructions and key data during compilation.
type blueprintBuilder struct {
	ops       []VjOpStep
	keyPool   []byte
	fallbacks map[int]*fbInfo // PC index → fallback info

	// visiting tracks struct types currently being compiled along the
	// recursive call chain (through pointers). This prevents infinite
	// recursion when two struct types form a cycle via pointer fields
	// (e.g. type A struct { B *B }; type B struct { A *A }).
	// When a cycle is detected, an OP_FALLBACK is emitted instead of
	// inlining the struct body, deferring to the Go encoder.
	visiting map[reflect.Type]bool
}

// emit appends an instruction and returns its index.
func (b *blueprintBuilder) emit(op VjOpStep) int {
	idx := len(b.ops)
	b.ops = append(b.ops, op)
	return idx
}

// addKey appends key bytes to the KeyPool and returns a pointer+length.
// The returned pointer is only valid after the final KeyPool is allocated;
// we use the index approach and fix up pointers at the end.
func (b *blueprintBuilder) addKey(keyBytes []byte) (poolOffset int, keyLen uint16) {
	if len(keyBytes) == 0 {
		return 0, 0
	}
	offset := len(b.keyPool)
	b.keyPool = append(b.keyPool, keyBytes...)
	return offset, uint16(len(keyBytes))
}

// pc returns the current program counter (next instruction index).
func (b *blueprintBuilder) pc() int {
	return len(b.ops)
}

// keyOffsets tracks which ops need key pointer fixup and their pool offsets.
type keyFixup struct {
	opIdx      int // index in b.ops
	poolOffset int // offset in b.keyPool
}

// compileBlueprint compiles a StructCodec into a Blueprint.
// The resulting Blueprint contains a single flat instruction stream
// for the entire type tree, with all nested types inlined.
func compileBlueprint(dec *StructCodec) *Blueprint {
	var b blueprintBuilder
	b.fallbacks = make(map[int]*fbInfo)
	b.visiting = make(map[reflect.Type]bool)
	var fixups []keyFixup

	// Mark top-level struct as visiting to detect cycles.
	b.visiting[dec.Typ] = true

	// Emit top-level struct as STRUCT_BEGIN + body + STRUCT_END.
	beginIdx := b.emit(VjOpStep{
		OpType:   opStructBegin,
		FieldOff: 0,
	})

	emitStructBody(&b, &fixups, dec, 0)

	b.emit(VjOpStep{
		OpType: opStructEnd,
	})

	b.ops[beginIdx].OperandA = int32(b.pc() - beginIdx - 1)

	// Terminate
	b.emit(VjOpStep{OpType: opEnd})

	// Fix up key pointers
	applyKeyFixups(&b, fixups)

	return &Blueprint{
		Ops:       b.ops,
		KeyPool:   b.keyPool,
		Fallbacks: b.fallbacks,
	}
}

// applyKeyFixups resolves key pool offsets into real pointers.
func applyKeyFixups(b *blueprintBuilder, fixups []keyFixup) {
	if len(b.keyPool) > 0 {
		poolBase := unsafe.Pointer(&b.keyPool[0])
		for _, f := range fixups {
			b.ops[f.opIdx].KeyPtr = unsafe.Add(poolBase, f.poolOffset)
		}
	}
}

// compileStandaloneSliceBlueprint builds a Blueprint for encoding a slice
// whose type was discovered at runtime (e.g. inside an interface{}).
// The ops encode: SLICE_BEGIN + element body + SLICE_END + END.
// The VM's base register must point to the GoSlice header on entry.
func compileStandaloneSliceBlueprint(dec *SliceCodec) *Blueprint {
	var b blueprintBuilder
	b.fallbacks = make(map[int]*fbInfo)
	b.visiting = make(map[reflect.Type]bool)
	var fixups []keyFixup

	emitSliceInner(&b, &fixups, 0, dec)
	b.emit(VjOpStep{OpType: opEnd})

	applyKeyFixups(&b, fixups)
	return &Blueprint{
		Ops:       b.ops,
		KeyPool:   b.keyPool,
		Fallbacks: b.fallbacks,
	}
}

// emitStructBody emits instructions for all fields in a struct.
// baseOff is the struct's offset within its parent (0 for top-level).
func emitStructBody(b *blueprintBuilder, fixups *[]keyFixup, dec *StructCodec, baseOff uintptr) {
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		fieldOff := baseOff + fi.Offset

		// Determine if this field needs omitempty.
		needsOmitempty := fi.Flags&tiFlagOmitEmpty != 0

		// Fields with custom marshalers or ,string tag → yield to Go.
		if fi.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn|tiFlagQuoted) != 0 {
			if needsOmitempty {
				emitSkipIfZero(b, fixups, fi, fieldOff, 1, fi.Kind)
			}
			emitYield(b, fixups, fi, fieldOff, i)
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
				emitSkipIfZero(b, fixups, fi, fieldOff, 1, fi.Kind)
			}
			emitPrimitive(b, fixups, fi, fieldOff)

		case KindStruct:
			subDec := fi.resolveCodec().(*StructCodec)
			if needsOmitempty {
				// Calculate the span of the nested struct instructions.
				// We emit a placeholder OP_SKIP_IF_ZERO, then emit the
				// struct, then patch the skip count.
				skipIdx := emitSkipIfZeroPlaceholder(b, fixups, fi, fieldOff, fi.Kind)
				emitNestedStruct(b, fixups, fi, fieldOff, subDec)
				// Patch: skip count = number of instructions emitted for the struct
				b.ops[skipIdx].OperandA = int32(b.pc() - skipIdx - 1)
			} else {
				emitNestedStruct(b, fixups, fi, fieldOff, subDec)
			}

		case KindPointer:
			pDec := fi.resolveCodec().(*PointerCodec)
			if needsOmitempty {
				// For pointers, omitempty checks nil (same as ptr_deref).
				// The ptr_deref instruction already handles nil→null+jump,
				// so omitempty just means: skip the key entirely if nil.
				skipIdx := emitSkipIfZeroPlaceholder(b, fixups, fi, fieldOff, KindPointer)
				emitPointer(b, fixups, fi, fieldOff, pDec)
				b.ops[skipIdx].OperandA = int32(b.pc() - skipIdx - 1)
			} else {
				emitPointer(b, fixups, fi, fieldOff, pDec)
			}

		case KindSlice:
			sliceDec := fi.resolveCodec().(*SliceCodec)
			// []byte needs base64 encoding — yield to Go.
			if sliceDec.ElemTI.Kind == KindUint8 && sliceDec.ElemSize == 1 {
				if needsOmitempty {
					emitSkipIfZero(b, fixups, fi, fieldOff, 1, KindSlice)
				}
				emitYield(b, fixups, fi, fieldOff, i)
				continue
			}
			if needsOmitempty {
				skipIdx := emitSkipIfZeroPlaceholder(b, fixups, fi, fieldOff, KindSlice)
				emitSlice(b, fixups, fi, fieldOff, sliceDec)
				b.ops[skipIdx].OperandA = int32(b.pc() - skipIdx - 1)
			} else {
				emitSlice(b, fixups, fi, fieldOff, sliceDec)
			}

		case KindMap:
			mapDec := fi.resolveCodec().(*MapCodec)
			if needsOmitempty {
				// Map omitempty needs Go-side len check (C only checks nil).
				// Emit as fallback so Go handles omitempty + full map encoding.
				emitYield(b, fixups, fi, fieldOff, i)
			} else {
				emitMap(b, fixups, fi, fieldOff, mapDec)
			}

		case KindAny:
			if needsOmitempty {
				emitSkipIfZero(b, fixups, fi, fieldOff, 1, KindAny)
			}
			emitInterface(b, fixups, fi, fieldOff)

		default:
			// Unknown kind → yield to Go.
			if needsOmitempty {
				emitSkipIfZero(b, fixups, fi, fieldOff, 1, fi.Kind)
			}
			emitYield(b, fixups, fi, fieldOff, i)
		}
	}
}

// emitPrimitive emits a single primitive encoding instruction.
func emitPrimitive(b *blueprintBuilder, fixups *[]keyFixup, fi *TypeInfo, fieldOff uintptr) {
	poolOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	idx := b.emit(VjOpStep{
		OpType:   kindToOpcode(fi.Kind),
		KeyLen:   keyLen,
		FieldOff: uint32(fieldOff),
	})
	if keyLen > 0 {
		*fixups = append(*fixups, keyFixup{opIdx: idx, poolOffset: poolOff})
	}
}

// emitNestedStruct emits OBJ_OPEN + body + OBJ_CLOSE for a nested struct.
// Uses frameless flat encoding: child field offsets are computed at compile
// time (baseOff = parent field offset), so the VM doesn't need to push a
// stack frame or switch the base register.
func emitNestedStruct(b *blueprintBuilder, fixups *[]keyFixup, fi *TypeInfo, fieldOff uintptr, subDec *StructCodec) {
	poolOff, keyLen := b.addKey(fi.Ext.KeyBytes)

	// OBJ_OPEN: lightweight '{' with key, no frame push
	openIdx := b.emit(VjOpStep{
		OpType: opObjOpen,
		KeyLen: keyLen,
		// FieldOff unused by OBJ_OPEN (no base switch)
	})
	if keyLen > 0 {
		*fixups = append(*fixups, keyFixup{opIdx: openIdx, poolOffset: poolOff})
	}

	// Mark this struct type as visiting to detect cycles through pointers.
	// For example: type A struct { B B }; type B struct { Back *B }
	// Without this mark, *B → B → *B → B → ... would recurse forever.
	wasVisiting := b.visiting[subDec.Typ]
	b.visiting[subDec.Typ] = true

	// Emit child fields with accumulated offset (baseOff = fieldOff).
	// Since OBJ_OPEN doesn't switch base, child field offsets must be
	// absolute from the top-level struct.
	emitStructBody(b, fixups, subDec, fieldOff)

	// Restore previous visiting state (don't unconditionally delete —
	// the type might have been visiting from an outer scope).
	if !wasVisiting {
		delete(b.visiting, subDec.Typ)
	}

	// OBJ_CLOSE: lightweight '}'
	b.emit(VjOpStep{
		OpType: opObjClose,
	})
}

// emitPointer emits PTR_DEREF + the dereferenced type's instructions.
func emitPointer(b *blueprintBuilder, fixups *[]keyFixup, fi *TypeInfo, fieldOff uintptr, pDec *PointerCodec) {
	poolOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	elemTI := pDec.ElemTI

	// PTR_DEREF: operand_a = number of instructions to skip on nil (patched below)
	derefIdx := b.emit(VjOpStep{
		OpType:   opPtrDeref,
		KeyLen:   keyLen,
		FieldOff: uint32(fieldOff),
	})
	if keyLen > 0 {
		*fixups = append(*fixups, keyFixup{opIdx: derefIdx, poolOffset: poolOff})
	}

	// Emit the dereferenced type's instructions.
	// After PTR_DEREF, base is set to the dereferenced pointer value.
	emitDerefBody(b, fixups, elemTI)

	// Emit PTR_END to pop the deref frame and restore parent base.
	b.emit(VjOpStep{
		OpType: opPtrEnd,
	})

	// Patch: skip count = instructions emitted for deref body + PTR_END
	b.ops[derefIdx].OperandA = int32(b.pc() - derefIdx - 1)
}

// emitDerefBody emits the body for a dereferenced pointer target.
// The offset is 0 because base has been switched to the deref'd address.
func emitDerefBody(b *blueprintBuilder, fixups *[]keyFixup, elemTI *TypeInfo) {
	// Custom marshalers → yield
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		idx := b.emit(VjOpStep{
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
		// Primitive: emit a keyless primitive instruction (off=0, no key)
		b.emit(VjOpStep{
			OpType:   kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case KindStruct:
		subDec := elemTI.resolveCodec().(*StructCodec)
		// Cycle detection: if this struct type is already being compiled
		// along the current call chain, emit a fallback to avoid infinite
		// recursion. The Go encoder will handle the recursive type at runtime.
		if b.visiting[subDec.Typ] {
			idx := b.emit(VjOpStep{
				OpType: opFallback,
			})
			b.fallbacks[idx] = &fbInfo{
				TI:     elemTI,
				Offset: 0,
			}
			return
		}
		b.visiting[subDec.Typ] = true
		// Inline the struct with STRUCT_BEGIN/END (keyless, off=0)
		beginIdx := b.emit(VjOpStep{
			OpType:   opStructBegin,
			FieldOff: 0,
		})
		emitStructBody(b, fixups, subDec, 0)
		b.emit(VjOpStep{
			OpType: opStructEnd,
		})
		b.ops[beginIdx].OperandA = int32(b.pc() - beginIdx - 1)
		delete(b.visiting, subDec.Typ)

	case KindSlice:
		sliceDec := elemTI.resolveCodec().(*SliceCodec)
		emitSliceInner(b, fixups, 0, sliceDec)

	case KindMap:
		mapDec := elemTI.resolveCodec().(*MapCodec)
		emitMapInner(b, fixups, 0, elemTI, mapDec)

	case KindAny:
		b.emit(VjOpStep{
			OpType:   opInterface,
			FieldOff: 0,
		})

	case KindPointer:
		// Pointer to pointer — recurse
		innerDec := elemTI.resolveCodec().(*PointerCodec)
		derefIdx := b.emit(VjOpStep{
			OpType:   opPtrDeref,
			FieldOff: 0,
		})
		emitDerefBody(b, fixups, innerDec.ElemTI)
		b.emit(VjOpStep{
			OpType: opPtrEnd,
		})
		b.ops[derefIdx].OperandA = int32(b.pc() - derefIdx - 1)

	default:
		// Fallback
		idx := b.emit(VjOpStep{
			OpType: opFallback,
		})
		b.fallbacks[idx] = &fbInfo{
			TI:     elemTI,
			Offset: 0, // base is already the deref'd pointer
		}
	}
}

// emitSlice emits SLICE_BEGIN + element body + SLICE_END for a slice field.
func emitSlice(b *blueprintBuilder, fixups *[]keyFixup, fi *TypeInfo, fieldOff uintptr, sliceDec *SliceCodec) {
	poolOff, keyLen := b.addKey(fi.Ext.KeyBytes)

	beginIdx := b.emit(VjOpStep{
		OpType:   opSliceBegin,
		KeyLen:   keyLen,
		FieldOff: uint32(fieldOff),
		OperandA: int32(sliceDec.ElemSize), // elem_size
		// OperandB will be patched to body_len
	})
	if keyLen > 0 {
		*fixups = append(*fixups, keyFixup{opIdx: beginIdx, poolOffset: poolOff})
	}

	bodyStart := b.pc()

	// Emit element body (offset=0, base points to elem[i])
	emitElementBody(b, fixups, sliceDec.ElemTI)

	// SLICE_END
	b.emit(VjOpStep{
		OpType: opSliceEnd,
	})

	// Patch: OperandB = body length (from instruction after SLICE_BEGIN to before SLICE_END)
	bodyLen := b.pc() - bodyStart - 1 // -1 to exclude SLICE_END itself
	b.ops[beginIdx].OperandB = int32(bodyLen)
}

// emitSliceInner is like emitSlice but without key bytes (for deref'd pointers).
func emitSliceInner(b *blueprintBuilder, fixups *[]keyFixup, fieldOff uintptr, sliceDec *SliceCodec) {
	beginIdx := b.emit(VjOpStep{
		OpType:   opSliceBegin,
		FieldOff: uint32(fieldOff),
		OperandA: int32(sliceDec.ElemSize),
	})

	bodyStart := b.pc()
	emitElementBody(b, fixups, sliceDec.ElemTI)
	b.emit(VjOpStep{OpType: opSliceEnd})

	bodyLen := b.pc() - bodyStart - 1
	b.ops[beginIdx].OperandB = int32(bodyLen)
}

// emitElementBody emits the instructions for encoding a single element
// (used in slice loops). base points to the element.
func emitElementBody(b *blueprintBuilder, fixups *[]keyFixup, elemTI *TypeInfo) {
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		idx := b.emit(VjOpStep{OpType: opFallback})
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
		b.emit(VjOpStep{
			OpType:   kindToOpcode(elemTI.Kind),
			FieldOff: 0,
		})

	case KindStruct:
		subDec := elemTI.resolveCodec().(*StructCodec)
		// Cycle detection (same as emitDerefBody).
		if b.visiting[subDec.Typ] {
			idx := b.emit(VjOpStep{OpType: opFallback})
			b.fallbacks[idx] = &fbInfo{
				TI:     elemTI,
				Offset: 0,
			}
			return
		}
		b.visiting[subDec.Typ] = true
		beginIdx := b.emit(VjOpStep{
			OpType:   opStructBegin,
			FieldOff: 0,
		})
		emitStructBody(b, fixups, subDec, 0)
		b.emit(VjOpStep{OpType: opStructEnd})
		b.ops[beginIdx].OperandA = int32(b.pc() - beginIdx - 1)
		delete(b.visiting, subDec.Typ)

	case KindPointer:
		pDec := elemTI.resolveCodec().(*PointerCodec)
		derefIdx := b.emit(VjOpStep{
			OpType:   opPtrDeref,
			FieldOff: 0,
		})
		emitDerefBody(b, fixups, pDec.ElemTI)
		b.emit(VjOpStep{
			OpType: opPtrEnd,
		})
		b.ops[derefIdx].OperandA = int32(b.pc() - derefIdx - 1)

	case KindSlice:
		sliceDec := elemTI.resolveCodec().(*SliceCodec)
		emitSliceInner(b, fixups, 0, sliceDec)

	case KindMap:
		mapDec := elemTI.resolveCodec().(*MapCodec)
		emitMapInner(b, fixups, 0, elemTI, mapDec)

	case KindAny:
		b.emit(VjOpStep{
			OpType:   opInterface,
			FieldOff: 0,
		})

	default:
		idx := b.emit(VjOpStep{OpType: opFallback})
		b.fallbacks[idx] = &fbInfo{
			TI:     elemTI,
			Offset: 0,
		}
	}
}

// emitMap emits MAP_BEGIN + value body + MAP_END for a map field.
// Map iteration is Go-driven: the VM yields for each k/v pair.
func emitMap(b *blueprintBuilder, fixups *[]keyFixup, fi *TypeInfo, fieldOff uintptr, mapDec *MapCodec) {
	poolOff, keyLen := b.addKey(fi.Ext.KeyBytes)

	beginIdx := b.emit(VjOpStep{
		OpType:   opMapBegin,
		KeyLen:   keyLen,
		FieldOff: uint32(fieldOff),
	})
	if keyLen > 0 {
		*fixups = append(*fixups, keyFixup{opIdx: beginIdx, poolOffset: poolOff})
	}

	// Register MAP_BEGIN in fallback table so Go can find the MapCodec.
	b.fallbacks[beginIdx] = &fbInfo{
		TI:     fi,
		Offset: fieldOff,
	}

	// Emit value encoding instructions (base will be set to value ptr by Go)
	emitElementBody(b, fixups, mapDec.ValTI)

	endIdx := b.emit(VjOpStep{
		OpType: opMapEnd,
	})

	// Patch: operand_a of MAP_BEGIN = distance to MAP_END
	b.ops[beginIdx].OperandA = int32(endIdx - beginIdx)
}

// emitMapInner is like emitMap but without key bytes.
func emitMapInner(b *blueprintBuilder, fixups *[]keyFixup, fieldOff uintptr, elemTI *TypeInfo, mapDec *MapCodec) {
	beginIdx := b.emit(VjOpStep{
		OpType:   opMapBegin,
		FieldOff: uint32(fieldOff),
	})

	// Register MAP_BEGIN in fallback table so Go can find the MapCodec.
	b.fallbacks[beginIdx] = &fbInfo{
		TI:     elemTI,
		Offset: fieldOff,
	}

	emitElementBody(b, fixups, mapDec.ValTI)

	endIdx := b.emit(VjOpStep{OpType: opMapEnd})
	b.ops[beginIdx].OperandA = int32(endIdx - beginIdx)
}

// emitInterface emits a single OP_INTERFACE instruction.
func emitInterface(b *blueprintBuilder, fixups *[]keyFixup, fi *TypeInfo, fieldOff uintptr) {
	poolOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	idx := b.emit(VjOpStep{
		OpType:   opInterface,
		KeyLen:   keyLen,
		FieldOff: uint32(fieldOff),
	})
	if keyLen > 0 {
		*fixups = append(*fixups, keyFixup{opIdx: idx, poolOffset: poolOff})
	}
}

// emitYield emits a single OP_YIELD instruction for Go fallback.
func emitYield(b *blueprintBuilder, fixups *[]keyFixup, fi *TypeInfo, fieldOff uintptr, fieldIdx int) {
	poolOff, keyLen := b.addKey(fi.Ext.KeyBytes)
	idx := b.emit(VjOpStep{
		OpType:   opFallback,
		KeyLen:   keyLen,
		FieldOff: uint32(fieldOff),
		OperandA: int32(fieldIdx), // Go needs this to find the field's TypeInfo
	})
	if keyLen > 0 {
		*fixups = append(*fixups, keyFixup{opIdx: idx, poolOffset: poolOff})
	}
	// Record fallback info so Go can encode this field.
	b.fallbacks[idx] = &fbInfo{
		TI:     fi,
		Offset: fieldOff,
	}
}

// emitSkipIfZero emits a OP_SKIP_IF_ZERO instruction with a known skip count.
func emitSkipIfZero(b *blueprintBuilder, _ *[]keyFixup, _ *TypeInfo, fieldOff uintptr, skipCount int, kind ElemTypeKind) {
	b.emit(VjOpStep{
		OpType:   opSkipIfZero | (uint16(kind) << 8), // high byte = ZeroCheckTag (matches ElemTypeKind)
		FieldOff: uint32(fieldOff),
		OperandA: int32(skipCount),
	})
}

// emitSkipIfZeroPlaceholder emits a OP_SKIP_IF_ZERO with OperandA=0 (to be patched).
// Returns the index of the emitted instruction.
func emitSkipIfZeroPlaceholder(b *blueprintBuilder, _ *[]keyFixup, _ *TypeInfo, fieldOff uintptr, kind ElemTypeKind) int {
	return b.emit(VjOpStep{
		OpType:   opSkipIfZero | (uint16(kind) << 8),
		FieldOff: uint32(fieldOff),
		OperandA: 0, // placeholder, patched by caller
	})
}

// ================================================================
// Blueprint cache integration with StructCodec
// ================================================================

// getBlueprint returns the compiled Blueprint for this StructCodec.
// Results are cached after the first call (thread-safe).
func (dec *StructCodec) getBlueprint() *Blueprint {
	cache := dec.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileBlueprint(dec)
	})
	return cache.blueprint
}
