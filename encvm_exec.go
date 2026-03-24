package vjson

import (
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// native VM — Go-side execution loop.
// execVM calls C (encvm.VMExec) in a loop, handling buffer growth,
// yield events (fallback/interface/map/slice), and interface cache misses.

// vmWriteIndent appends newline + indent whitespace for the current VM
// indent depth. No-op in compact mode (IndentStep == 0).
func (m *marshaler) vmWriteIndent(ctx *VjExecCtx) {
	if ctx.IndentStep == 0 {
		return
	}
	n := 1 + int(ctx.IndentPrefixLen) + int(ctx.IndentDepth)*int(ctx.IndentStep)
	tpl := m.indentTpl
	m.buf = append(m.buf, tpl[:n]...)
}

// vmWriteKeySpace appends a space after the key colon in indent mode.
func (m *marshaler) vmWriteKeySpace(ctx *VjExecCtx) {
	if ctx.IndentStep != 0 {
		m.buf = append(m.buf, ' ')
	}
}

// encodeStructNative is the native VM entry point for struct encoding.
// It compiles a Blueprint (if not cached), then runs the VM.
func (m *marshaler) encodeStructNative(dec *StructCodec, base unsafe.Pointer) error {
	bp := dec.getBlueprint()
	if bp == nil || len(bp.Ops) == 0 {
		// No blueprint → pure Go fallback
		return m.encodeStructGo(dec, base)
	}
	return m.execVM(bp, base)
}

// encodeSliceNative is the native VM entry point for top-level slice encoding.
func (m *marshaler) encodeSliceNative(dec *SliceCodec, base unsafe.Pointer) error {
	bp := dec.getBlueprint()
	if bp == nil || len(bp.Ops) == 0 {
		return m.encodeSliceGo(dec, base)
	}
	return m.execVM(bp, base)
}

// encodeArrayNative is the native VM entry point for top-level array encoding.
func (m *marshaler) encodeArrayNative(dec *ArrayCodec, base unsafe.Pointer) error {
	bp := dec.getBlueprint()
	if bp == nil || len(bp.Ops) == 0 {
		return m.encodeArrayGo(dec, base)
	}
	return m.execVM(bp, base)
}

// encodeMapNative is the native VM entry point for top-level map encoding.
// Only used for string-keyed maps where MAP_STR_ITER can iterate in C.
func (m *marshaler) encodeMapNative(dec *MapCodec, ptr unsafe.Pointer) error {
	bp := dec.getBlueprint()
	if bp == nil || len(bp.Ops) == 0 {
		return m.encodeMapFallback(dec, ptr)
	}
	return m.execVM(bp, ptr)
}

// execVM runs the C VM engine with the given Blueprint and data base pointer.
// It manages the Go↔C interaction loop including buffer growth, yield handling,
// and interface cache management.
//
// Uses the reusable m.vmCtx to avoid per-call stack zeroing of the
// 2152-byte VjExecCtx. IfaceCache is already set by getMarshaler.
func (m *marshaler) execVM(bp *Blueprint, base unsafe.Pointer) error {
	// WARNING: execVM must NOT be called re-entrantly. m.vmCtx is a single
	// shared context; a nested execVM call would corrupt its state (PC, stack,
	// depth, etc.). Callers (e.g. encodeStruct) must check m.inVM and fall
	// back to the Go path when it is true.
	m.inVM = true

	if vjTraceEnabled {
		m.traceRecordBlueprint(bp)
		defer m.traceFlushBlueprints()
	}

	ctx := &m.vmCtx
	ctx.OpsPtr = unsafe.Pointer(&bp.Ops[0])
	ctx.PC = 0
	// SAFETY: base must point to heap memory. If it pointed to the goroutine
	// stack, a stack growth during yield-to-Go would relocate the stack but
	// NOT update this heap-stored pointer, leaving CurBase dangling.
	// Currently guaranteed because Marshal's escape analysis marks v as
	// heap-escaping (via indirect EncodeFn call). See Marshal doc comment.
	ctx.CurBase = base

	// Build initial vmstate: first=1, flags from m.flags,
	// depth=0, exit=0, yield=0.
	ctx.VMState = vmstateBuildInitial(m.flags)

	// Load interface cache snapshot (deferred from getMarshaler for types
	// that don't use the VM, e.g. primitives encoded via EncodeFn).
	initPrimitiveIfaceCacheOnce.Do(initPrimitiveIfaceCache)
	snap := loadIfaceCacheSnapshot()
	if len(snap.entries) > 0 {
		ctx.IfaceCachePtr = unsafe.Pointer(&snap.entries[0])
		ctx.IfaceCacheCount = int32(len(snap.entries))
	}

	// Set key pool base pointer for C VM.
	kpSnap := loadKeyPoolSnapshot()
	if kpSnap != nil && len(kpSnap.data) > 0 {
		ctx.KeyPoolBase = unsafe.Pointer(&kpSnap.data[0])
	} else {
		ctx.KeyPoolBase = nil
	}

	// Select VM mode: three-way dispatch based on indent and escape flags.
	//
	// indent + any escape  → VMExec       (default: indent + flags at runtime)
	// compact + escape flags → VMExecCompact (compact: no indent, has escape checks)
	// compact + no escape  → VMExecFast   (fast: no indent, no escape checks)
	var vmExec func(unsafe.Pointer)
	if step := isSimpleIndent(m.prefix, m.indent); step > 0 {
		// Indent mode: use default VM (with indent support).
		// Default mode handles both indent and escape flags at runtime.
		m.buildIndentTpl(m.prefix, m.indent)
		ctx.IndentTpl = unsafe.Pointer(&m.indentTpl[0])
		ctx.IndentStep = uint8(step)
		ctx.IndentPrefixLen = uint8(len(m.prefix))
		ctx.IndentDepth = 0
		vmExec = encvm.VMExec
	} else {
		// Compact mode: indent fields are already zero from pool recycling.
		if m.flags&uint32(escapeStringFlags) != 0 {
			vmExec = encvm.VMExecCompact
		} else {
			vmExec = encvm.VMExecFast
		}
	}

	err := m.execVMLoop(ctx, bp, vmExec)
	m.inVM = false
	return err
}

// execVMLoop is the inner VM execution loop, factored out of execVM so that
// the inVM flag can be cleared without defer overhead on the hot path.
func (m *marshaler) execVMLoop(ctx *VjExecCtx, bp *Blueprint, vmExec func(unsafe.Pointer)) error {
	for {
		// In streaming mode, flush accumulated data from Go-side yield
		// handlers before re-entering C. This keeps memory bounded and
		// ensures Go-written data (fallback fields, maps, interfaces)
		// reaches the writer promptly.
		if m.flushFn != nil && len(m.buf) > 0 {
			if err := m.flush(); err != nil {
				return err
			}
		}

		// Ensure sufficient buffer capacity
		avail := cap(m.buf) - len(m.buf)
		if avail < 64 {
			newCap := max(cap(m.buf)*2, 4096)
			newBuf := make([]byte, len(m.buf), newCap)
			copy(newBuf, m.buf)
			m.buf = newBuf
		}

		workBuf := m.buf[len(m.buf):cap(m.buf)]
		bufStart := unsafe.Pointer(&workBuf[0])
		ctx.BufCur = bufStart
		ctx.BufEnd = uintptr(bufStart) + uintptr(len(workBuf))

		// Enter C VM
		vmExec(unsafe.Pointer(ctx))

		// Flush trace output (no-op unless vjdebug build tag).
		m.flushVMTrace()

		written := int(uintptr(ctx.BufCur) - uintptr(bufStart))

		switch vmstateGetExit(ctx.VMState) {
		case vjExitOK:
			m.buf = m.buf[:len(m.buf)+written]
			return nil

		case vjExitBufFull:
			m.buf = m.buf[:len(m.buf)+written]

			if m.flushFn != nil {
				// Streaming mode: flush buffered data to writer and reuse
				// the buffer. Memory stays bounded at O(bufCap).
				if err := m.flush(); err != nil {
					return err
				}
			} else {
				newCap := max(cap(m.buf)*2, len(m.buf)+4096)
				newBuf := make([]byte, len(m.buf), newCap)
				copy(newBuf, m.buf)
				m.buf = newBuf
			}

		case vjExitYieldToGo:
			m.buf = m.buf[:len(m.buf)+written]

			// Sync Go-side indent depth from C VM state so that Go-driven
			// encoding (maps, any-typed values) uses the correct depth.
			if ctx.IndentStep > 0 {
				m.indentDepth = int(ctx.IndentDepth)
			}

			switch vmstateGetYield(ctx.VMState) {
			case yieldIfaceMiss:
				if err := m.handleIfaceCacheMiss(ctx); err != nil {
					return err
				}
				snap := loadIfaceCacheSnapshot()
				if len(snap.entries) > 0 {
					ctx.IfaceCachePtr = unsafe.Pointer(&snap.entries[0])
					ctx.IfaceCacheCount = int32(len(snap.entries))
				}
				// Reload key pool: the newly compiled Blueprint may have
				// inserted keys that weren't in the snapshot loaded at VM entry.
				kpSnap := loadKeyPoolSnapshot()
				if kpSnap != nil && len(kpSnap.data) > 0 {
					ctx.KeyPoolBase = unsafe.Pointer(&kpSnap.data[0])
				}
				// ctx.VMState already has correct state; PC unchanged for retry

			case yieldFallback:
				// Resolve the active Blueprint. Usually this is the root bp
				// (hot path: single pointer compare), but after SWITCH_OPS
				// the VM may be executing a child Blueprint's ops.
				activeBP := activeBlueprint(ctx, bp)
				m.traceRecordBlueprint(activeBP)

				if opHdrAt(activeBP.Ops, ctx.PC).OpType == opInterface {
					// Hot path: OP_INTERFACE yield.
					if err := m.handleInterfaceYield(ctx, activeBP); err == errVMContinue {
						continue // batch slice takeover: PC already past SLICE_END
					} else if err != nil {
						return err
					}
				} else {
					// Cold path: custom marshalers, ,string, etc.
					if err := m.handleYieldFallback(ctx, activeBP); err != nil {
						return err
					}
				}

			case yieldMapHandoff:
				activeBP := activeBlueprint(ctx, bp)
				m.traceRecordBlueprint(activeBP)
				if err := m.handleMapIteration(ctx, activeBP); err != nil {
					return err
				}

			default:
				return fmt.Errorf("vjson: unknown yield reason %d", vmstateGetYield(ctx.VMState))
			}

		case vjExitStackOvfl:
			return fmt.Errorf("vjson: nesting depth exceeds limit (depth=%d/%d)",
				vmstateGetStackDepth(ctx.VMState), VJ_MAX_STACK_DEPTH)

		case vjExitNanInf:
			return &UnsupportedValueError{Str: "NaN or Inf float value"}

		default:
			return fmt.Errorf("vjson: native encoder exit code %d", vmstateGetExit(ctx.VMState))
		}
	}
}

// handleIfaceCacheMiss compiles a Blueprint for the interface's concrete type
// and inserts it into the global cache.
func (m *marshaler) handleIfaceCacheMiss(ctx *VjExecCtx) error {
	typePtr := ctx.YieldTypePtr
	if typePtr == nil {
		return fmt.Errorf("vjson: interface cache miss with nil type pointer")
	}

	// Reconstruct reflect.Type from the raw *abi.Type pointer.
	rtype := typeFromRTypePtr(typePtr)

	// Get or create the TypeInfo for this type.
	ti := getCodec(rtype)

	// Determine tag for primitives or compile Blueprint for complex types.
	// Tag = opcode directly (all primitive opcodes >= 1, so tag=0 means "no tag").
	var tag uint8
	var bp *Blueprint
	var flags uint8

	switch {
	case ti.Kind <= KindString:
		tag = uint8(kindToOpcode(ti.Kind))
	default:
		switch ti.Kind {
		case KindStruct:
			dec := ti.resolveCodec().(*StructCodec)
			bp = dec.getBlueprint()
		case KindSlice:
			sliceDec := ti.resolveCodec().(*SliceCodec)
			bp = compileStandaloneSliceBlueprint(sliceDec)
		case KindArray:
			aDec := ti.resolveCodec().(*ArrayCodec)
			bp = compileStandaloneArrayBlueprint(aDec)
		case KindMap:
			mapDec := ti.resolveCodec().(*MapCodec)
			bp = compileStandaloneMapBlueprint(mapDec)
			// Maps are reference types — eface.data IS the map pointer.
			// INDIRECT flag tells C to use &eface.data as base, so
			// MAP_STR_ITER correctly dereferences base+0 → map pointer.
			flags = ifaceFlagIndirect
		default:
			// Insert with nil ops — C will yield on next encounter.
		}
	}

	insertIfaceCache(typePtr, bp, tag, flags)
	if bp != nil {
		m.traceRecordBlueprint(bp)
	}
	return nil
}

// encodeAnyIface encodes an interface{} value from a pointer to the eface.
// Delegates to encodeAny which covers all concrete JSON value types.
func (m *marshaler) encodeAnyIface(ifacePtr unsafe.Pointer) error {
	return m.encodeAny(*(*any)(ifacePtr))
}

// handleYieldFallback handles a yield due to custom marshaler, ,string, or
// unsupported type. Go encodes the field, then returns.
func (m *marshaler) handleYieldFallback(ctx *VjExecCtx, bp *Blueprint) error {
	// Extract the 'first' flag from vmstate.
	isFirst := vmstateGetFirst(ctx.VMState)

	// Look up fallback info by byte offset PC.
	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("vjson: native VM yield at PC=%d with no fallback info", ctx.PC)
	}

	// Compute field pointer from current base + offset.
	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)

	// Handle omitempty: skip if zero-valued.
	if fb.TI.Flags&tiFlagOmitEmpty != 0 && fb.TI.Ext != nil && fb.TI.Ext.IsZeroFn != nil {
		if fb.TI.Ext.IsZeroFn(fieldPtr) {
			// Skip: advance PC past the 8-byte FALLBACK instruction.
			ctx.PC += 8
			// vmstate already has correct first flag; no change needed.
			return nil
		}
	}

	// Write comma if not the first field.
	if !isFirst {
		m.buf = append(m.buf, ',')
		// Write indent after comma.
		m.vmWriteIndent(ctx)
	}

	// Write key.
	if fb.TI.Ext != nil && len(fb.TI.Ext.KeyBytes) > 0 {
		m.buf = append(m.buf, fb.TI.Ext.KeyBytes...)
		m.vmWriteKeySpace(ctx)
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

	// Advance PC past the 8-byte fallback instruction.
	ctx.PC += 8

	// A field was written, so clear first flag in vmstate.
	ctx.VMState &^= vjStFirstBit

	return nil
}

// handleMapIteration handles OP_MAP yield: Go encodes the entire map,
// then advances PC past this instruction.
//
// map[string]any uses encodeAnyMap with inline fast-paths for common
// value types; other map types go through encodeMap.
func (m *marshaler) handleMapIteration(ctx *VjExecCtx, bp *Blueprint) error {
	hdr := opHdrAt(bp.Ops, ctx.PC)
	opCodeVal := hdr.OpType

	// Extract the 'first' flag from vmstate (set by VM before yielding).
	isFirst := vmstateGetFirst(ctx.VMState)

	// Find the associated field info to get the MapCodec.
	// The map instruction's field_off tells us where the map is in the struct.
	mapPtr := unsafe.Add(ctx.CurBase, uintptr(hdr.FieldOff))

	// Look up the MapCodec from the fallback table or Blueprint.
	// Maps always have a fallback entry so Go can encode them.
	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("vjson: native VM map at PC=%d (op=%d) with no fallback info", ctx.PC, opCodeVal)
	}

	mapDec := fb.TI.resolveCodec().(*MapCodec)

	// Write comma if not the first field.
	if !isFirst {
		m.buf = append(m.buf, ',')
		// Write indent after comma.
		m.vmWriteIndent(ctx)
	}

	// Write key from the instruction's key data.
	if hdr.KeyLen > 0 {
		m.buf = append(m.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
		m.vmWriteKeySpace(ctx)
	}

	// Fast path for map[string]any: use encodeAnyMap which has inline type
	// dispatch for common JSON value types, avoiding reflect overhead.
	if mapDec.ValTI.Kind == KindAny && mapDec.KeyType.Kind() == reflect.String {
		mp := *(*map[string]any)(mapPtr)
		if err := m.encodeAnyMap(mp); err != nil {
			return err
		}
	} else {
		// Generic path for other map types (e.g. map[string]int).
		if err := m.encodeMap(mapDec, mapPtr); err != nil {
			return err
		}
	}

	// Skip past this instruction.
	ctx.PC += 8

	// A field was written, so clear first flag in vmstate.
	ctx.VMState &^= vjStFirstBit

	return nil
}
