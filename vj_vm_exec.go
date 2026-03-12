package vjson

import (
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/native/encoder"
)

// ================================================================
// native VM — Go-side execution loop
//
// The execVM function drives the C VM engine in a loop:
//   1. Set up ExecCtx with buffer, blueprint ops, base pointer
//   2. Call into C (encoder.VMExec)
//   3. Inspect error code:
//      - vjOK: done
//      - vjErrBufFull: grow buffer, re-enter
//      - vjErrYield: handle yield reason, re-enter
//      - vjErrNanInf: report error
//
// Interface cache miss: Go compiles the type's Blueprint, inserts
// it into the global COW cache, updates the ExecCtx snapshot,
// then re-enters C (PC unchanged — C retries OP_INTERFACE).
// ================================================================

// encodeStructNative is the native VM entry point for struct encoding.
// It compiles a Blueprint (if not cached), then runs the VM.
func (m *Marshaler) encodeStructNative(dec *StructCodec, base unsafe.Pointer) error {
	bp := dec.getBlueprint()
	if bp == nil || len(bp.Ops) == 0 {
		// No blueprint → pure Go fallback
		return m.encodeStructGo(dec, base)
	}
	return m.execVM(bp, base)
}

// execVM runs the C VM engine with the given Blueprint and data base pointer.
// It manages the Go↔C interaction loop including buffer growth, yield handling,
// and interface cache management.
//
// Uses the reusable m.vmCtx to avoid per-call stack zeroing of the
// 1248-byte VjExecCtx. IfaceCache is already set by getMarshaler.
func (m *Marshaler) execVM(bp *Blueprint, base unsafe.Pointer) error {
	if !encoder.Available {
		return fmt.Errorf("vjson: native encoder not available")
	}

	// Guard against re-entrant VM calls. This can happen when a cycle-
	// detected struct falls back to Go encoding which then calls
	// encodeStruct → encodeStructNative → execVM. Since m.vmCtx is
	// a single shared context, re-entrant calls would corrupt state.
	if m.inVM {
		panic("vjson: re-entrant execVM call (likely circular type fallback bug)")
	}
	m.inVM = true
	defer func() { m.inVM = false }()

	ctx := &m.vmCtx
	ctx.OpsPtr = unsafe.Pointer(&bp.Ops[0])
	ctx.PC = 0
	ctx.CurBase = base
	ctx.EncFlags = uint32(m.flags)
	ctx.Depth = 0

	for {
		// Ensure sufficient buffer capacity
		avail := cap(m.buf) - len(m.buf)
		if avail < 64 {
			newCap := max(cap(m.buf)*2, 4096)
			newBuf := make([]byte, len(m.buf), newCap)
			copy(newBuf, m.buf)
			m.buf = newBuf
		}

		workBuf := m.buf[len(m.buf):cap(m.buf)]
		if len(workBuf) == 0 {
			newCap := max(cap(m.buf)*2, len(m.buf)+4096)
			newBuf := make([]byte, len(m.buf), newCap)
			copy(newBuf, m.buf)
			m.buf = newBuf
			continue
		}

		bufStart := unsafe.Pointer(&workBuf[0])
		ctx.BufCur = bufStart
		ctx.BufEnd = uintptr(bufStart) + uintptr(len(workBuf))
		ctx.ErrCode = 0

		// Enter C VM
		encoder.VMExec(unsafe.Pointer(ctx))

		written := int(uintptr(ctx.BufCur) - uintptr(bufStart))

		switch ctx.ErrCode {
		case vjOK:
			m.buf = m.buf[:len(m.buf)+written]
			return nil

		case vjErrBufFull:
			// Absorb partial output and grow buffer.
			// C already saved PC/depth/stack and packed first into enc_flags.
			// On re-entry, C reads VJ_ENC_RESUME from enc_flags to restore first.
			m.buf = m.buf[:len(m.buf)+written]
			newCap := max(cap(m.buf)*2, len(m.buf)+4096)
			newBuf := make([]byte, len(m.buf), newCap)
			copy(newBuf, m.buf)
			m.buf = newBuf
			// ctx.EncFlags already has VJ_ENC_RESUME set by C's SAVE_AND_RETURN

		case vjErrYield:
			m.buf = m.buf[:len(m.buf)+written]

			switch ctx.YieldInfo {
			case yieldIfaceMiss:
				if err := m.handleIfaceCacheMiss(ctx); err != nil {
					return err
				}
				snap := loadIfaceCacheSnapshot()
				if len(snap.entries) > 0 {
					ctx.IfaceCachePtr = unsafe.Pointer(&snap.entries[0])
					ctx.IfaceCacheCount = int32(len(snap.entries))
				}
				// ctx.EncFlags already has VJ_ENC_RESUME; PC unchanged for retry

			case yieldFallback:
				// Hot path: OP_INTERFACE yield (vast majority of fallbacks).
				// Inline the interface handling to avoid map lookup + function
				// call overhead in handleYieldFallback.
				op := bp.Ops[ctx.PC]
				if op.OpType&opTypeMask == opInterface {
					isFirst := (uint32(ctx.YieldFieldIdx) & escOpFirstBit) != 0
					ifacePtr := unsafe.Add(ctx.CurBase, uintptr(op.FieldOff))

					if !isFirst {
						m.buf = append(m.buf, ',')
					}
					if op.KeyLen > 0 {
						keyBytes := unsafe.Slice((*byte)(op.KeyPtr), op.KeyLen)
						m.buf = append(m.buf, keyBytes...)
					}

				// Encode the current interface{} element.
				if err := m.encodeAnyIface(ifacePtr); err != nil {
						return err
					}

					// Batch slice takeover: if this OP_INTERFACE is inside a
					// []interface{} slice loop, encode the remaining elements
					// in Go instead of returning to C for each one. This saves
					// N-1 C↔Go round-trips per slice.
					//
					// Guard: only activate when we can verify the parent frame
					// is a SLICE in the same ops stream (bp.Ops). When the VM
					// is inside an IFACE-switched ops stream, ctx.PC is relative
					// to a different Blueprint, so bp.Ops[ctx.PC-1] would be wrong.
					// The SLICE_BEGIN check catches this safely.
					const frameSlice = int32(1) // VJ_FRAME_SLICE
					if ctx.Depth > 0 && ctx.PC > 0 {
						frame := &ctx.Stack[ctx.Depth-1]
						if frame.FrameType == frameSlice &&
							int(ctx.PC-1) < len(bp.Ops) &&
							bp.Ops[ctx.PC-1].OpType&opTypeMask == opSliceBegin {
							// Encode remaining slice elements in Go.
							elemSize := uintptr(frame.ElemSize)
							count := frame.IterCount
							for idx := frame.IterIdx + 1; idx < count; idx++ {
								m.buf = append(m.buf, ',')
								elemPtr := unsafe.Add(frame.IterData, uintptr(idx)*elemSize)
								if err := m.encodeAnyIface(elemPtr); err != nil {
									return err
								}
							}
							// Close the array, pop the slice frame.
							m.buf = append(m.buf, ']')
							ctx.Depth--
							ctx.CurBase = frame.RetBase
							// PC past SLICE_END: SLICE_BEGIN is at PC-1, body_len
							// is in its OperandB. SLICE_END = PC + body_len,
							// past SLICE_END = PC + body_len + 1.
							bodyLen := bp.Ops[ctx.PC-1].OperandB
							ctx.PC = ctx.PC + bodyLen + 1
							ctx.EncFlags = uint32(m.flags) | vjEncResume
							continue
						}
					}

					ctx.PC++
					ctx.EncFlags = uint32(m.flags) | vjEncResume
				} else {
					// Cold path: table-based fallback (custom marshalers, ,string, etc.)
					if err := m.handleYieldFallback(ctx, bp); err != nil {
						return err
					}
				}

			case yieldMapNext:
				if err := m.handleMapIteration(ctx, bp); err != nil {
					return err
				}

			default:
				return fmt.Errorf("vjson: unknown yield reason %d", ctx.YieldInfo)
			}

		case vjErrStackOvfl:
			return fmt.Errorf("vjson: struct nesting depth exceeds %d levels", maxStackDepth)

		case vjErrNanInf:
			return &UnsupportedValueError{Str: "NaN or Inf float value"}

		default:
			return fmt.Errorf("vjson: native encoder error %d", ctx.ErrCode)
		}
	}
}

// handleIfaceCacheMiss compiles a Blueprint for the interface's concrete type
// and inserts it into the global cache.
func (m *Marshaler) handleIfaceCacheMiss(ctx *VjExecCtx) error {
	typePtr := ctx.YieldTypePtr
	if typePtr == nil {
		return fmt.Errorf("vjson: interface cache miss with nil type pointer")
	}

	// Reconstruct reflect.Type from the raw *abi.Type pointer.
	rtype := typeFromRTypePtr(typePtr)

	// Get or create the TypeInfo for this type.
	ti := GetCodec(rtype)

	// Determine tag for primitives or compile Blueprint for complex types.
	// Tag stored as (opcode + 1) so that tag=0 is unambiguous for "no tag"
	// (needed because OP_BOOL == 0).
	var tag uint8
	var bp *Blueprint

	switch ti.Kind {
	case KindBool:
		tag = uint8(opBool) + 1
	case KindInt:
		tag = uint8(opInt) + 1
	case KindInt8:
		tag = uint8(opInt8) + 1
	case KindInt16:
		tag = uint8(opInt16) + 1
	case KindInt32:
		tag = uint8(opInt32) + 1
	case KindInt64:
		tag = uint8(opInt64) + 1
	case KindUint:
		tag = uint8(opUint) + 1
	case KindUint8:
		tag = uint8(opUint8) + 1
	case KindUint16:
		tag = uint8(opUint16) + 1
	case KindUint32:
		tag = uint8(opUint32) + 1
	case KindUint64:
		tag = uint8(opUint64) + 1
	case KindFloat32:
		tag = uint8(opFloat32) + 1
	case KindFloat64:
		tag = uint8(opFloat64) + 1
	case KindString:
		tag = uint8(opString) + 1
	case KindStruct:
		dec := ti.Codec.(*StructCodec)
		bp = dec.getBlueprint()
	case KindSlice:
		sliceDec := ti.Codec.(*SliceCodec)
		bp = compileStandaloneSliceBlueprint(sliceDec)
	case KindMap:
		// Maps are Go-driven — insert with nil ops.
	default:
		// Insert with nil ops — C will yield on next encounter.
	}

	insertIfaceCache(typePtr, bp, tag)
	return nil
}

// encodeAnyIface encodes an interface{} value from a pointer to the eface.
// Uses inline fast-path dispatch for the most common JSON types.
func (m *Marshaler) encodeAnyIface(ifacePtr unsafe.Pointer) error {
	v := *(*any)(ifacePtr)
	switch val := v.(type) {
	case map[string]any:
		return m.encodeAnyMap(val)
	case []any:
		return m.encodeAnySlice(val)
	case nil:
		m.buf = append(m.buf, litNull...)
		return nil
	default:
		return m.encodeAny(ifacePtr)
	}
}

// handleYieldFallback handles a yield due to custom marshaler, ,string, or
// unsupported type. Go encodes the field, then returns.
func (m *Marshaler) handleYieldFallback(ctx *VjExecCtx, bp *Blueprint) error {
	// Extract the 'first' flag from yield_field_idx (bit 31).
	isFirst := (uint32(ctx.YieldFieldIdx) & escOpFirstBit) != 0

	// Look up fallback info by PC.
	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("vjson: native VM yield at PC=%d with no fallback info", ctx.PC)
	}

	// Compute field pointer from current base + offset.
	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)

	// Handle omitempty: skip if zero-valued.
	if fb.TI.Flags&tiFlagOmitEmpty != 0 && fb.TI.Ext != nil && fb.TI.Ext.IsZeroFn != nil {
		if fb.TI.Ext.IsZeroFn(fieldPtr) {
			// Skip: advance PC, preserve first flag as-is.
			ctx.PC++
			// Set resume flags: first stays the same.
			ctx.EncFlags = uint32(m.flags) | vjEncResume
			if isFirst {
				ctx.EncFlags |= vjEncResumeFirst
			}
			return nil
		}
	}

	// Write comma if not the first field.
	if !isFirst {
		m.buf = append(m.buf, ',')
	}

	// Write key.
	if fb.TI.Ext != nil && len(fb.TI.Ext.KeyBytes) > 0 {
		m.buf = append(m.buf, fb.TI.Ext.KeyBytes...)
	}

	// Encode value.
	if fb.TI.Flags&tiFlagQuoted != 0 {
		if err := m.encodeValueQuoted(fb.TI, fieldPtr); err != nil {
			return err
		}
	} else {
		if err := m.encodeValue(fb.TI, fieldPtr); err != nil {
			return err
		}
	}

	// Advance PC past the fallback instruction.
	ctx.PC++

	// Set resume flags: a field was written, so first=false.
	ctx.EncFlags = uint32(m.flags) | vjEncResume

	return nil
}

// handleMapIteration handles the Go-driven map iteration protocol.
// When C yields at OP_MAP_BEGIN, Go takes over the entire map encoding,
// then advances PC past OP_MAP_END.
//
// For map[string]any, we use encodeAnyMap which has inline fast-paths for
// common JSON value types (string, float64, bool, nil, []any, map[string]any),
// avoiding the reflect-based encodeMapGeneric path.
func (m *Marshaler) handleMapIteration(ctx *VjExecCtx, bp *Blueprint) error {
	op := bp.Ops[ctx.PC]
	opCode := op.OpType & opTypeMask

	// Extract the 'first' flag from enc_flags (set by VM_SAVE_AND_RETURN).
	// MAP_BEGIN doesn't set yield_field_idx, so we read first from enc_flags.
	isFirst := (ctx.EncFlags & vjEncResumeFirst) != 0

	// Find the associated field info to get the MapCodec.
	// The map instruction's field_off tells us where the map is in the struct.
	mapPtr := unsafe.Add(ctx.CurBase, uintptr(op.FieldOff))

	// Write comma if not the first field.
	if !isFirst {
		m.buf = append(m.buf, ',')
	}

	// Write key from the instruction's key data.
	if op.KeyLen > 0 {
		keyBytes := unsafe.Slice((*byte)(op.KeyPtr), op.KeyLen)
		m.buf = append(m.buf, keyBytes...)
	}

	// Look up the MapCodec from the fallback table or Blueprint.
	// Maps always have a fallback entry so Go can encode them.
	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("vjson: native VM map at PC=%d (op=%d) with no fallback info", ctx.PC, opCode)
	}

	mapDec := fb.TI.Codec.(*MapCodec)

	// Fast path for map[string]any: use encodeAnyMap which has inline type
	// dispatch for common JSON value types, avoiding reflect overhead.
	if mapDec.ValTI.Kind == KindAny && mapDec.KeyType.Kind() == reflect.String {
		mp := *(*map[string]any)(mapPtr)
		if err := m.encodeAnyMap(mp); err != nil {
			return err
		}
	} else {
		// Generic path for other map types (e.g. map[string]int, map[string]string).
		if err := m.encodeMap(mapDec, mapPtr); err != nil {
			return err
		}
	}

	// Advance PC past MAP_END: skip MAP_BEGIN + operand_a (distance to MAP_END) + 1 (past MAP_END)
	ctx.PC += op.OperandA + 1

	// Set resume flags: a field was written, so first=false.
	ctx.EncFlags = uint32(m.flags) | vjEncResume

	return nil
}

// typeFromRTypePtr reconstructs a reflect.Type from a raw *abi.Type pointer.
// This is the reverse of rtypePtr().
func typeFromRTypePtr(p unsafe.Pointer) reflect.Type {
	// reflect.Type is an interface{itab, *rtype}.
	// *rtype is the same as *abi.Type.
	// We construct a reflect.Type by creating an eface with the known itab.
	//
	// Use the same trick as rtypePtr but in reverse: create a dummy
	// reflect.Type and overwrite its data word.
	var dummy reflect.Type
	eface := (*[2]unsafe.Pointer)(unsafe.Pointer(&dummy))
	// eface[0] is the itab for reflect.Type — keep it from the dummy
	// We need a real reflect.Type to get the itab. Use int as donor.
	donor := reflect.TypeOf(0)
	donorEface := (*[2]unsafe.Pointer)(unsafe.Pointer(&donor))
	eface[0] = donorEface[0] // copy itab
	eface[1] = p             // set *rtype data word
	return dummy
}
