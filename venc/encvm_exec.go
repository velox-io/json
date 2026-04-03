package venc

import (
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/typ"
)

// vmWriteIndent appends the current VM newline+indent prefix.
func (m *marshaler) vmWriteIndent(ctx *VjExecCtx) {
	if ctx.IndentStep == 0 {
		return
	}
	n := 1 + int(ctx.IndentPrefixLen) + int(ctx.IndentDepth)*int(ctx.IndentStep)
	tpl := m.indentTpl
	m.buf = append(m.buf, tpl[:n]...)
}

// vmWriteKeySpace mirrors the indented `": ` layout.
func (m *marshaler) vmWriteKeySpace(ctx *VjExecCtx) {
	if ctx.IndentStep != 0 {
		m.buf = append(m.buf, ' ')
	}
}

// execVM drives the Go<->C VM loop around one Blueprint.
func (m *marshaler) execVM(bp *Blueprint, base unsafe.Pointer) error {
	// m.vmCtx is shared state, so native VM entry cannot be re-entrant.
	m.inVM = true

	if vjTraceEnabled {
		m.traceRecordBlueprint(bp)
		defer m.traceFlushBlueprints()
	}

	ctx := &m.vmCtx
	ctx.OpsPtr = unsafe.Pointer(&bp.Ops[0])
	ctx.PC = 0
	// CurBase lives in heap state, so it must never point at stack memory.
	ctx.CurBase = base

	ctx.VMState = vmstateBuildInitial(m.flags)

	initPrimitiveIfaceCacheOnce.Do(initPrimitiveIfaceCache)
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

	// Indent uses the full VM; compact mode selects compact vs fast escaping.
	var vmExec func(unsafe.Pointer)
	if step := isSimpleIndent(m.prefix, m.indent); step > 0 {
		m.buildIndentTpl(m.prefix, m.indent)
		ctx.IndentTpl = unsafe.Pointer(&m.indentTpl[0])
		ctx.IndentStep = uint8(step)
		ctx.IndentPrefixLen = uint8(len(m.prefix))
		ctx.IndentDepth = 0
		vmExec = encvm.VMExec
	} else {
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

// execVMLoop keeps the hot VM loop free of defer overhead.
func (m *marshaler) execVMLoop(ctx *VjExecCtx, bp *Blueprint, vmExec func(unsafe.Pointer)) error {
	for {
		// Flush before re-entry so yield-written bytes reach the writer promptly.
		if m.flushFn != nil && len(m.buf) > 0 {
			if err := m.flush(); err != nil {
				return err
			}
		}

		// Ensure workBuf is non-empty. Besides vjExitBufFull, the YieldToGo
		// handlers also append to m.buf and may fill it to cap exactly,
		// so we must check on every iteration, not just after BufFull.
		if len(m.buf) == cap(m.buf) {
			newCap := max(cap(m.buf)*2, 4096)
			newBuf := gort.MakeDirtyBytes(len(m.buf), newCap)
			copy(newBuf, m.buf)
			m.buf = newBuf
		}

		// Hand the spare capacity [len:cap) to the C VM as its write region.
		workBuf := m.buf[len(m.buf):cap(m.buf)]
		bufStart := uintptr(unsafe.Pointer(&workBuf[0]))
		ctx.BufCur = bufStart
		ctx.BufEnd = bufStart + uintptr(len(workBuf))

		vmExec(unsafe.Pointer(ctx))

		m.flushVMTrace()

		// Bytes written by the VM this iteration.
		written := int(ctx.BufCur - bufStart)

		switch vmstateGetExit(ctx.VMState) {
		case vjExitOK:
			m.buf = m.buf[:len(m.buf)+written]
			return nil

		case vjExitBufFull:
			m.buf = m.buf[:len(m.buf)+written]

			if m.flushFn != nil {
				if err := m.flush(); err != nil {
					return err
				}
			} else {
				newCap := max(cap(m.buf)*2, len(m.buf)+4096)
				newBuf := gort.MakeDirtyBytes(len(m.buf), newCap)
				copy(newBuf, m.buf)
				m.buf = newBuf
			}

		case vjExitYieldToGo:
			m.buf = m.buf[:len(m.buf)+written]

			// Go-side fallback paths must see the VM's current indent depth.
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
				// A newly compiled Blueprint may have extended the shared key pool.
				kpSnap := loadKeyPoolSnapshot()
				if kpSnap != nil && len(kpSnap.data) > 0 {
					ctx.KeyPoolBase = unsafe.Pointer(&kpSnap.data[0])
				}
			case yieldFallback:
				// SWITCH_OPS may have moved execution into a child Blueprint.
				activeBP := activeBlueprint(ctx, bp)
				m.traceRecordBlueprint(activeBP)

				if opHdrAt(activeBP.Ops, ctx.PC).OpType == opInterface {
					if err := m.handleInterfaceYield(ctx, activeBP); err == errVMContinue {
						continue
					} else if err != nil {
						return err
					}
				} else {
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
func (m *marshaler) handleIfaceCacheMiss(ctx *VjExecCtx) error {
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
			si := ti.ResolveStruct()
			bp = si.getBlueprint()
		case typ.KindSlice:
			sliceInfo := ti.ResolveSlice()
			bp = compileStandaloneSliceBlueprint(sliceInfo)
		case typ.KindArray:
			arrayInfo := ti.ResolveArray()
			bp = compileStandaloneArrayBlueprint(arrayInfo)
		case typ.KindMap:
			mapInfo := ti.ResolveMap()
			bp = compileStandaloneMapBlueprint(mapInfo)
			// Map interface payloads are direct, so the VM needs the INDIRECT flag.
			flags = ifaceFlagIndirect
		default:
		}
	}

	insertIfaceCache(typePtr, bp, tag, flags)
	if bp != nil {
		m.traceRecordBlueprint(bp)
	}
	return nil
}

func (m *marshaler) encodeAnyIface(ifacePtr unsafe.Pointer) error {
	return m.encodeAny(*(*any)(ifacePtr))
}

// handleYieldFallback runs the Go-side fallback for one yielded field.
func (m *marshaler) handleYieldFallback(ctx *VjExecCtx, bp *Blueprint) error {
	isFirst := vmstateGetFirst(ctx.VMState)

	fb, ok := bp.Fallbacks[int(ctx.PC)]
	if !ok {
		return fmt.Errorf("venc: native VM yield at PC=%d with no fallback info", ctx.PC)
	}

	fieldPtr := unsafe.Add(ctx.CurBase, fb.Offset)

	if fb.TI.TagFlags&EncTagFlagOmitEmpty != 0 && fb.TI.IsZeroFn != nil {
		if fb.TI.IsZeroFn(fieldPtr) {
			ctx.PC += 8
			return nil
		}
	}

	if !isFirst {
		m.buf = append(m.buf, ',')
		m.vmWriteIndent(ctx)
	}

	if len(fb.TI.KeyBytes) > 0 {
		m.buf = append(m.buf, fb.TI.KeyBytes...)
		m.vmWriteKeySpace(ctx)
	}

	if fb.TI.TagFlags&EncTagFlagQuoted != 0 {
		if err := m.encodeValueQuoted(fb.TI, fieldPtr); err != nil {
			return err
		}
	} else {
		if err := m.encodeTop(fb.TI, fieldPtr); err != nil {
			return err
		}
	}

	ctx.PC += 8

	ctx.VMState &^= vjStFirstBit

	return nil
}

// handleMapIteration runs the Go-side map encoder for OP_MAP yields.
func (m *marshaler) handleMapIteration(ctx *VjExecCtx, bp *Blueprint) error {
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
		m.buf = append(m.buf, ',')
		m.vmWriteIndent(ctx)
	}

	if hdr.KeyLen > 0 {
		m.buf = append(m.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
		m.vmWriteKeySpace(ctx)
	}

	// map[string]any keeps the value fast path inline.
	if mapInfo.ValTI.Kind == typ.KindAny && mapInfo.KeyType.Kind() == reflect.String {
		mp := *(*map[string]any)(mapPtr)
		if err := m.encodeAnyMap(mp); err != nil {
			return err
		}
	} else {
		if err := m.encodeTop(fb.TI, mapPtr); err != nil {
			return err
		}
	}

	ctx.PC += 8

	ctx.VMState &^= vjStFirstBit

	return nil
}
