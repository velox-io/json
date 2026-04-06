package venc

import (
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/typ"
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

// execVM drives the Go<->C VM loop around one Blueprint.
func (es *encodeState) execVM(bp *Blueprint, base unsafe.Pointer) error {
	// m.vmCtx is shared state, so native VM entry cannot be re-entrant.
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

	ctx.VMState = vmstateBuildInitial(es.flags)

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
	if es.nativeIndent && es.indentString != "" {
		es.buildIndentTpl(es.indentPrefix, es.indentString)
		ctx.IndentTpl = unsafe.Pointer(&es.indentTpl[0])
		ctx.IndentStep = uint8(len(es.indentString))
		ctx.IndentPrefixLen = uint8(len(es.indentPrefix))
		ctx.IndentDepth = 0

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
	if es.nativeIndent && es.indentString != "" {
		ctx.IndentTpl = nil
		ctx.IndentStep = 0
		ctx.IndentPrefixLen = 0
		ctx.IndentDepth = 0
	}
	return err
}

// execVMLoop keeps the hot VM loop free of defer overhead.
func (es *encodeState) execVMLoop(ctx *VjExecCtx, bp *Blueprint, vmExec func(unsafe.Pointer)) error {
	for {
		// Flush before re-entry so yield-written bytes reach the writer promptly.
		if es.flushFn != nil && len(es.buf) > 0 {
			if err := es.flush(); err != nil {
				return err
			}
		}

		// Ensure workBuf is non-empty. Besides vjExitBufFull, the YieldToGo
		// handlers also append to m.buf and may fill it to cap exactly,
		// so we must check on every iteration, not just after BufFull.
		if len(es.buf) == cap(es.buf) {
			newCap := max(cap(es.buf)*2, 4096)
			newBuf := gort.MakeDirtyBytes(len(es.buf), newCap)
			copy(newBuf, es.buf)
			es.buf = newBuf
		}

		// Hand the spare capacity [len:cap) to the C VM as its write region.
		workBuf := es.buf[len(es.buf):cap(es.buf)]
		bufStart := uintptr(unsafe.Pointer(&workBuf[0]))
		ctx.BufCur = bufStart
		ctx.BufEnd = bufStart + uintptr(len(workBuf))

		vmExec(unsafe.Pointer(ctx))

		es.flushVMTrace()

		// Bytes written by the VM this iteration.
		written := int(ctx.BufCur - bufStart)

		switch vmstateGetExit(ctx.VMState) {
		case vjExitOK:
			es.buf = es.buf[:len(es.buf)+written]
			return nil

		case vjExitBufFull:
			es.buf = es.buf[:len(es.buf)+written]

			if es.flushFn != nil {
				if err := es.flush(); err != nil {
					return err
				}
			} else {
				newCap := max(cap(es.buf)*2, len(es.buf)+4096)
				newBuf := gort.MakeDirtyBytes(len(es.buf), newCap)
				copy(newBuf, es.buf)
				es.buf = newBuf
			}

		case vjExitYieldToGo:
			es.buf = es.buf[:len(es.buf)+written]

			// Go-side fallback paths must see the VM's current indent depth.
			if ctx.IndentStep > 0 {
				es.indentDepth = int(ctx.IndentDepth)
			}

			switch vmstateGetYield(ctx.VMState) {
			case yieldIfaceMiss:
				if err := es.handleIfaceCacheMiss(ctx); err != nil {
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
	}
}

// typeFromRTypePtr rebuilds reflect.Type from a raw runtime type pointer.
func typeFromRTypePtr(p unsafe.Pointer) reflect.Type {
	var dummy reflect.Type
	eface := (*[2]unsafe.Pointer)(unsafe.Pointer(&dummy))
	donor := reflect.TypeFor[int]()
	donorEface := (*[2]unsafe.Pointer)(unsafe.Pointer(&donor))
	eface[0] = donorEface[0]
	eface[1] = p
	return dummy
}

// handleIfaceCacheMiss compiles or tags the concrete interface type and publishes it into the shared cache.
func (es *encodeState) handleIfaceCacheMiss(ctx *VjExecCtx) error {
	typePtr := ctx.YieldTypePtr
	if typePtr == nil {
		return fmt.Errorf("venc: interface cache miss with nil type pointer")
	}

	rtype := typeFromRTypePtr(typePtr)

	ti := EncTypeInfoOf(rtype)

	// Primitive tags are opcode values; tag=0 means the cache entry relies on OpsPtr or a future yield.
	var tag uint8
	var bp *Blueprint
	var flags uint8

	switch {
	case ti.Kind <= typ.KindString:
		tag = uint8(kindToOpcode(ti.Kind))
	default:
		switch ti.Kind {
		case typ.KindStruct:
			bp = ti.getBlueprint()
		case typ.KindSlice:
			bp = ti.getBlueprint()
		case typ.KindArray:
			bp = ti.getBlueprint()
		case typ.KindMap:
			bp = ti.getBlueprint()
			// Map interface payloads are direct, so the VM needs the INDIRECT flag.
			flags = ifaceFlagIndirect
		default:
		}
	}

	insertIfaceCache(typePtr, bp, tag, flags)
	if bp != nil {
		es.traceRecordBlueprint(bp)
	}
	return nil
}

func (es *encodeState) encodeAnyIface(ifacePtr unsafe.Pointer) error {
	return es.encodeAny(*(*any)(ifacePtr))
}

// handleFallbackYield runs the Go-side fallback for one yielded field.
func (es *encodeState) handleFallbackYield(ctx *VjExecCtx, bp *Blueprint) error {
	isFirst := vmstateGetFirst(ctx.VMState)

	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("venc: native VM yield at PC=%d with no fallback info", ctx.PC)
	}

	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)

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
		if err := es.encodeTop(fb.TI, fieldPtr); err != nil {
			return err
		}
	}

	ctx.PC += 8

	ctx.VMState &^= vjStFirstBit

	return nil
}

// handleMapIteration runs the Go-side map encoder for OP_MAP yields.
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

	// map[string]any keeps the value fast path inline.
	if mapInfo.ValType.Kind == typ.KindAny && mapInfo.IsStringKey {
		mp := *(*map[string]any)(mapPtr)
		if err := es.encodeAnyMap(mp); err != nil {
			return err
		}
	} else {
		if err := es.encodeTop(fb.TI, mapPtr); err != nil {
			return err
		}
	}

	ctx.PC += 8

	ctx.VMState &^= vjStFirstBit

	return nil
}
