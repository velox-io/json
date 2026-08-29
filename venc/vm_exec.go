package venc

import (
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/typ"
	"github.com/velox-io/json/value"
)

func (es *encodeState) writeIndent(ctx *VjExecCtx) {
	if ctx.IndentStep == 0 {
		return
	}
	n := 1 + int(ctx.IndentPrefixLen) + int(ctx.IndentDepth)*int(ctx.IndentStep)
	tpl := es.indentTpl
	es.buf = append(es.buf, tpl[:n]...)
}

func (es *encodeState) writeKeySpace(ctx *VjExecCtx) {
	if ctx.IndentStep != 0 {
		es.buf = append(es.buf, ' ')
	}
}

func (es *encodeState) execVM(bp *Blueprint, base unsafe.Pointer) error {
	es.inVM = true

	if vjTraceEnabled {
		es.traceRecordBlueprint(bp)
		defer es.traceFlushBlueprints()
	}

	ctx := &es.vmCtx
	ctx.OpsPtr = unsafe.Pointer(&bp.Ops[0])
	ctx.PC = 0
	// CurBase lives in heap state, so it must never point at stack memory.
	ctx.CurBase = base

	ctx.VMState = vmstateBuildInitial(es.flags | swissMapGlobalFlags)

	snap := loadIfaceCacheSnapshot()
	if len(snap.entries) > 0 {
		ctx.IfaceCachePtr = unsafe.Pointer(&snap.entries[0])
		ctx.IfaceCacheCount = int32(len(snap.entries))
	}

	kpSnap := loadKeyPoolSnapshot()
	if kpSnap != nil && len(kpSnap.data) > 0 {
		ctx.KeyPoolBase = unsafe.Pointer(&kpSnap.data[0])
	} else {
		ctx.KeyPoolBase = nil
	}

	// Indent mode uses the full VM; compact mode selects compact vs fast escaping.
	var vmExec func(unsafe.Pointer)
	if es.indentString != "" {
		es.buildIndentTpl(es.indentPrefix, es.indentString)
		ctx.IndentTpl = unsafe.Pointer(&es.indentTpl[0])
		ctx.IndentStep = uint8(len(es.indentString))
		ctx.IndentPrefixLen = uint8(len(es.indentPrefix))
		ctx.IndentDepth = 0
		defer func() {
			ctx.IndentTpl = nil
			ctx.IndentStep = 0
			ctx.IndentPrefixLen = 0
			ctx.IndentDepth = 0
		}()

		vmExec = encvm.VMExec
	} else {
		if es.flags&uint32(escapeStringFlags) != 0 {
			vmExec = encvm.VMExecCompact
		} else {
			vmExec = encvm.VMExecFast
		}
	}

	err := es.execVMLoop(ctx, bp, vmExec)

	es.inVM = false
	ctx.OpsPtr = nil
	ctx.CurBase = nil
	return err
}

func (es *encodeState) execVMLoop(ctx *VjExecCtx, bp *Blueprint, vmExec func(unsafe.Pointer)) error {
	bufFull := false
	produced := 0
	for {
		// The write window is es.buf[len:cap]. The previous iteration's
		// bottom-of-loop reclaim guarantees it is non-empty, so reclaim is not
		// polled here on the hot path. Only an already-full buffer at entry
		// (AppendMarshal into a full dst) needs a pre-run reclaim.
		if len(es.buf) == cap(es.buf) {
			if err := es.reclaim(bufFull, produced); err != nil {
				return err
			}
		}

		workBuf := es.buf[len(es.buf):cap(es.buf)]
		bufStart := uintptr(unsafe.Pointer(&workBuf[0]))
		ctx.BufCur = bufStart
		ctx.BufEnd = bufStart + uintptr(len(workBuf))

		es.callvm(vmExec, ctx)

		es.flushVMTrace()

		produced = int(ctx.BufCur - bufStart)
		// Single commit point for VM output, regardless of exit reason.
		es.buf = es.buf[:len(es.buf)+produced]

		switch vmstateGetExit(ctx.VMState) {
		case vjExitOK:
			return nil

		case vjExitBufFull:
			// Window exhausted; the bottom-of-loop reclaim reopens it. produced
			// distinguishes partial progress (grow only the tail) from a
			// reservation larger than the whole window (produced == 0).
			bufFull = true

		case vjExitYieldToGo:
			bufFull = false

			// Go-side fallback paths must see the VM's current indent depth.
			if ctx.IndentStep > 0 {
				es.indentDepth = int(ctx.IndentDepth)
			}

			switch vmstateGetYield(ctx.VMState) {
			case yieldIfaceMiss:
				if err := es.handleIfaceCacheMiss(ctx, bp); err != nil {
					return err
				}
				snap := loadIfaceCacheSnapshot()
				if len(snap.entries) > 0 {
					ctx.IfaceCachePtr = unsafe.Pointer(&snap.entries[0])
					ctx.IfaceCacheCount = int32(len(snap.entries))
				}
				// A newly compiled Blueprint may have extended the shared key pool.
				kpSnap := loadKeyPoolSnapshot()
				if kpSnap != nil && len(kpSnap.data) > 0 {
					ctx.KeyPoolBase = unsafe.Pointer(&kpSnap.data[0])
				}
			case yieldFallback:
				// SWITCH_OPS may have moved execution into a child Blueprint.
				activeBP := activeBlueprint(ctx, bp)
				es.traceRecordBlueprint(activeBP)

				if opHdrAt(activeBP.Ops, ctx.PC).OpType == opInterface {
					if err := es.handleInterfaceYield(ctx, activeBP); err == errVMContinue {
						continue
					} else if err != nil {
						return err
					}
				} else if opHdrAt(activeBP.Ops, ctx.PC).OpType == opValueSpread {
					if err := es.handleValueSpreadYield(ctx, activeBP); err != nil {
						return err
					}
				} else if opHdrAt(activeBP.Ops, ctx.PC).OpType == opValue {
					// A value.Value the walk refused (depth beyond the indent
					// bounds) is served by the Go walk directly: its Encode runs
					// the VM, so routing through it would re-enter and re-yield
					// without progress.
					if err := es.handleValueYield(ctx, activeBP); err != nil {
						return err
					}
				} else {
					if err := es.handleFallbackYield(ctx, activeBP); err != nil {
						return err
					}
				}

			case yieldMapHandoff:
				activeBP := activeBlueprint(ctx, bp)
				es.traceRecordBlueprint(activeBP)
				if err := es.handleMapIteration(ctx, activeBP); err != nil {
					return err
				}

			default:
				return fmt.Errorf("venc: unknown yield reason %d", vmstateGetYield(ctx.VMState))
			}

		case vjExitStackOvfl:
			return fmt.Errorf("venc: nesting depth exceeds limit (depth=%d/%d)",
				vmstateGetStackDepth(ctx.VMState), VJ_MAX_STACK_DEPTH)

		case vjExitNanInf:
			return &UnsupportedValueError{Str: "NaN or Inf float value"}

		default:
			return fmt.Errorf("venc: native encoder exit code %d", vmstateGetExit(ctx.VMState))
		}

		// Loop-back edge: the window was exhausted (BUF_FULL) or a yield
		// handler appended to es.buf. Reclaim space for the next VM entry:
		// buffer mode grows, stream mode flushes committed bytes (and grows only
		// on an oversized reservation). Runs once per loop-back, never on the
		// single-iteration fast path above.
		if err := es.reclaim(bufFull, produced); err != nil {
			return err
		}
	}
}

func typeFromRTypePtr(p unsafe.Pointer) reflect.Type {
	var dummy reflect.Type
	eface := (*[2]unsafe.Pointer)(unsafe.Pointer(&dummy))
	donor := reflect.TypeFor[int]()
	donorEface := (*[2]unsafe.Pointer)(unsafe.Pointer(&donor))
	eface[0] = donorEface[0]
	eface[1] = p
	return dummy
}

func (es *encodeState) handleIfaceCacheMiss(ctx *VjExecCtx, bp *Blueprint) error {
	typePtr := ctx.YieldTypePtr
	if typePtr == nil {
		return fmt.Errorf("venc: interface cache miss with nil type pointer")
	}

	rtype := typeFromRTypePtr(typePtr)

	// An unfold miss compiles the body-only Blueprint of the concrete struct
	// and attaches it to the cache entry; the miss re-executes OP_UNFOLD,
	// which then dispatches through the SWITCH_OPS frame.
	if hdr := opHdrAt(activeBlueprint(ctx, bp).Ops, ctx.PC); hdr.OpType == opUnfold {
		bodyBP, err := es.unfoldBodyBlueprint(rtype)
		if err != nil {
			return err
		}
		insertIfaceCacheBody(typePtr, bodyBP)
		es.traceRecordBlueprint(bodyBP)
		return nil
	}

	ti := EncTypeInfoOf(rtype)

	// Primitive tags are opcode values; tag=0 means the cache entry relies on OpsPtr or a future yield.
	var tag uint8
	var fullBP *Blueprint
	var flags uint8

	switch {
	case ti.Kind <= typ.KindString:
		tag = uint8(kindToOpcode(ti.Kind))
	default:
		switch ti.Kind {
		case typ.KindStruct:
			fullBP = ti.getBlueprint()
		case typ.KindSlice:
			fullBP = ti.getBlueprint()
		case typ.KindArray:
			fullBP = ti.getBlueprint()
		case typ.KindMap:
			fullBP = ti.getBlueprint()
			// Map interface payloads are direct, so the VM needs the INDIRECT flag.
			flags = ifaceFlagIndirect
		default:
		}
	}

	insertIfaceCache(typePtr, fullBP, tag, flags)
	if fullBP != nil {
		es.traceRecordBlueprint(fullBP)
	}
	return nil
}

func (es *encodeState) encodeAnyIface(ifacePtr unsafe.Pointer) error {
	return es.encodeAny(*(*any)(ifacePtr))
}

// unfoldBodyBlueprint resolves the body-only Blueprint for the concrete type
// stored in an inline variant field. Pointer cases compile the pointee's
// body: the unfold base is the data-word deref, which addresses the struct
// directly. Non-struct values are a misuse of the decode-side construct.
func (es *encodeState) unfoldBodyBlueprint(rtype reflect.Type) (*Blueprint, error) {
	ti, err := unfoldStructTI(rtype)
	if err != nil {
		return nil, err
	}
	return compileBodyBlueprint(ti), nil
}

func unfoldStructTI(rtype reflect.Type) (*EncTypeInfo, error) {
	ti := EncTypeInfoOf(rtype)
	for ti.Kind == typ.KindPointer {
		ti = ti.ResolvePointer().ElemType
	}
	if ti.Kind != typ.KindStruct {
		return nil, &UnsupportedValueError{
			Str: "inline variant field must hold a struct or nil, got " + rtype.String(),
		}
	}
	return ti, nil
}

func (es *encodeState) handleFallbackYield(ctx *VjExecCtx, bp *Blueprint) error {
	isFirst := vmstateGetFirst(ctx.VMState)

	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("venc: native VM yield at PC=%d with no fallback info", ctx.PC)
	}

	// A reserve-unknown spread beyond the native bounds (deep nesting, or a
	// non-object root the Go walk must reject) runs member-mode in Go: no
	// key, comma state driven by the first latch.
	if fb.Reason == fbReasonSpread {
		return es.spreadFromYield(ctx, bp, fb, isFirst)
	}

	// An unfold field beyond the native bounds (pointer hops, oversized
	// offset) runs the case struct's field loop in Go.
	if fb.Reason == fbReasonUnfold {
		return es.unfoldFromYield(ctx, fb, isFirst)
	}

	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)
	if len(fb.PtrPath) > 0 {
		// Promoted across an embedded pointer, so the offset is relative to the
		// pointee the hops reach. A nil hop means the field has no storage, so
		// the key is skipped entirely, exactly as the interpreter does.
		fieldBase, ok := resolveFieldBase(ctx.CurBase, fb.PtrPath)
		if !ok {
			ctx.PC += 8
			return nil
		}
		fieldPtr = unsafe.Add(fieldBase, fb.Offset)
	}

	if fb.TagFlags&EncTagFlagOmitEmpty != 0 && fb.IsZeroFn != nil {
		if fb.IsZeroFn(fieldPtr) {
			ctx.PC += 8
			return nil
		}
	}

	if !isFirst {
		es.buf = append(es.buf, ',')
		es.writeIndent(ctx)
	}

	if len(fb.KeyBytes) > 0 {
		es.buf = append(es.buf, fb.KeyBytes...)
		es.writeKeySpace(ctx)
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

	ctx.PC += 8

	ctx.VMState &^= vjStFirstBit

	return nil
}

// handleValueYield serves a yield whose op is OP_VALUE itself (indent-depth
// guard): the fb entry carries the field coordinates, the Go walk emits the
// value body.
func (es *encodeState) handleValueYield(ctx *VjExecCtx, bp *Blueprint) error {
	isFirst := vmstateGetFirst(ctx.VMState)
	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("venc: value yield at PC=%d with no fallback info", ctx.PC)
	}

	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)
	if len(fb.PtrPath) > 0 {
		fieldBase, ok := resolveFieldBase(ctx.CurBase, fb.PtrPath)
		if !ok {
			ctx.PC += 8
			return nil
		}
		fieldPtr = unsafe.Add(fieldBase, fb.Offset)
	}

	if !isFirst {
		es.buf = append(es.buf, ',')
		es.writeIndent(ctx)
	}

	if len(fb.KeyBytes) > 0 {
		es.buf = append(es.buf, fb.KeyBytes...)
		es.writeKeySpace(ctx)
	}

	if err := es.appendTapeValue((*value.Value)(fieldPtr)); err != nil {
		return err
	}

	ctx.PC += 8
	ctx.VMState &^= vjStFirstBit
	return nil
}

// handleValueSpreadYield serves a yield whose op is OP_VALUE_SPREAD itself
// (depth guard or non-object root): the fb entry carries the same field
// coordinates as the op.
func (es *encodeState) handleValueSpreadYield(ctx *VjExecCtx, bp *Blueprint) error {
	isFirst := vmstateGetFirst(ctx.VMState)
	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("venc: value spread yield at PC=%d with no fallback info", ctx.PC)
	}
	return es.spreadFromYield(ctx, bp, fb, isFirst)
}

// spreadFromYield runs the Go member-mode spread for a yielded spread op.
// The first latch is consumed only when a member is written; an empty or
// zero Value leaves it untouched, mirroring the native walk.
func (es *encodeState) spreadFromYield(ctx *VjExecCtx, bp *Blueprint, fb *fbInfo, isFirst bool) error {
	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)
	if len(fb.PtrPath) > 0 {
		fieldBase, ok := resolveFieldBase(ctx.CurBase, fb.PtrPath)
		if !ok {
			ctx.PC += 8
			return nil
		}
		fieldPtr = unsafe.Add(fieldBase, fb.Offset)
	}

	first := isFirst
	if err := es.appendTapeSpread((*value.Value)(fieldPtr), &first); err != nil {
		return err
	}
	ctx.PC += 8
	if !first {
		ctx.VMState &^= vjStFirstBit
	}
	return nil
}

// unfoldFromYield emits an inline variant case's fields in Go: the cold
// mirror of the body-only Blueprint. Field order, omitempty, keys, and the
// comma state follow emitStructBody's compiled form.
func (es *encodeState) unfoldFromYield(ctx *VjExecCtx, fb *fbInfo, isFirst bool) error {
	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)
	if len(fb.PtrPath) > 0 {
		fieldBase, ok := resolveFieldBase(ctx.CurBase, fb.PtrPath)
		if !ok {
			ctx.PC += 8
			return nil
		}
		fieldPtr = unsafe.Add(fieldBase, fb.Offset)
	}

	typePtr := *(*unsafe.Pointer)(fieldPtr)
	if typePtr == nil {
		ctx.PC += 8
		return nil
	}
	if fb.TI.Kind == typ.KindIface {
		typePtr = *(*unsafe.Pointer)(unsafe.Add(typePtr, 8))
	}

	bodyBP, err := es.unfoldBodyBlueprint(typeFromRTypePtr(typePtr))
	if err != nil {
		return err
	}
	ti, err := unfoldStructTI(typeFromRTypePtr(typePtr))
	if err != nil {
		return err
	}
	si := ti.ResolveStruct()
	dataPtr := *(*unsafe.Pointer)(unsafe.Add(fieldPtr, 8))

	first := isFirst
	for i := range si.Fields {
		fi := &si.Fields[i]
		// The offset is relative to the base the hops reach for promoted
		// fields; a nil hop means no storage, and the field is omitted.
		fptr := unsafe.Add(dataPtr, uintptr(fi.Offset))
		if len(fi.PtrPath) > 0 {
			hopBase, ok := resolveFieldBase(dataPtr, fi.PtrPath)
			if !ok {
				continue
			}
			fptr = unsafe.Add(hopBase, uintptr(fi.Offset))
		}
		if fi.TagFlags&EncTagFlagOmitEmpty != 0 && fi.IsZeroFn != nil && fi.IsZeroFn(fptr) {
			continue
		}
		if !first {
			es.buf = append(es.buf, ',')
			if es.indentString != "" {
				es.appendNewlineIndent()
			}
		} else if es.indentString != "" {
			es.appendNewlineIndent()
		}
		first = false
		es.buf = append(es.buf, fi.KeyBytes...)
		if es.indentString != "" {
			es.buf = append(es.buf, ' ')
		}
		if err := fi.Type.Encode(es, fptr); err != nil {
			return err
		}
	}

	// bodyBP was compiled for the cache so later encodes stay native.
	_ = bodyBP
	ctx.PC += 8
	if !first {
		ctx.VMState &^= vjStFirstBit
	}
	return nil
}

func (es *encodeState) handleMapIteration(ctx *VjExecCtx, bp *Blueprint) error {
	hdr := opHdrAt(bp.Ops, ctx.PC)
	opCodeVal := hdr.OpType

	isFirst := vmstateGetFirst(ctx.VMState)

	mapPtr := unsafe.Add(ctx.CurBase, uintptr(hdr.FieldOff))

	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("venc: native VM map at PC=%d (op=%d) with no fallback info", ctx.PC, opCodeVal)
	}

	mapInfo := fb.TI.ResolveMap()

	if !isFirst {
		es.buf = append(es.buf, ',')
		es.writeIndent(ctx)
	}

	if hdr.KeyLen > 0 {
		es.buf = append(es.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
		es.writeKeySpace(ctx)
	}

	if mapInfo.ValType.Kind == typ.KindAny && mapInfo.IsStringKey {
		mp := *(*map[string]any)(mapPtr)
		if err := es.encodeAnyMap(mp); err != nil {
			return err
		}
	} else {
		if err := fb.TI.Encode(es, mapPtr); err != nil {
			return err
		}
	}

	ctx.PC += 8

	ctx.VMState &^= vjStFirstBit

	return nil
}
