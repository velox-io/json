package vjson

import (
	"errors"
	"unsafe"
)

// errVMContinue is a sentinel returned by handleInterfaceYield to signal
// that the caller should `continue` the VM loop (batch slice takeover path).
// It is never exposed outside the execVM loop.
var errVMContinue = errors.New("vjson: vm continue")

// handleInterfaceYield handles the hot-path OP_INTERFACE yield inline.
// It encodes the interface{} value and optionally performs batch slice
// takeover to encode remaining []interface{} elements in Go, saving
// N-1 C↔Go round-trips.
//
// Returns errVMContinue when the caller must `continue` the VM loop
// (PC and VMState already updated for resume past the slice).
// Returns nil when the caller should re-enter C at PC+1.
// Returns any other error on encoding failure.
func (m *Marshaler) handleInterfaceYield(ctx *VjExecCtx, activeBP *Blueprint) error {
	hdr := opHdrAt(activeBP.Ops, ctx.PC)
	isFirst := vmstateGetFirst(ctx.VMState)
	ifacePtr := unsafe.Add(ctx.CurBase, uintptr(hdr.FieldOff))

	if !isFirst {
		m.buf = append(m.buf, ',')
		m.vmWriteIndent(ctx)
	}
	if hdr.KeyLen > 0 {
		m.buf = append(m.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
		m.vmWriteKeySpace(ctx)
	}

	// Encode the current interface{} element.
	if err := m.encodeAnyIface(ifacePtr); err != nil {
		return err
	}

	// Batch slice takeover: encode remaining []interface{} elements in Go,
	// saving N-1 C↔Go round-trips.
	// Only safe when parent frame is a SLICE loop (verified by opSliceBegin check).
	// SLICE_BEGIN is always 16 bytes (extended instruction), so prevPC = ctx.PC - 16.
	stackDepth := vmstateGetStackDepth(ctx.VMState)
	if stackDepth > 0 && ctx.PC >= 16 {
		frame := &ctx.Stack[stackDepth-1]
		prevPC := ctx.PC - 16
		if int(prevPC) >= 0 &&
			opHdrAt(activeBP.Ops, prevPC).OpType == opSliceBegin {
			// Encode remaining slice elements in Go.
			// elem_size is in the SLICE_BEGIN instruction's ext.OperandA.
			prevExt := opExtAt(activeBP.Ops, prevPC)
			elemSize := uintptr(prevExt.OperandA)
			count := frame.iterCount()
			for idx := frame.iterIdx() + 1; idx < count; idx++ {
				m.buf = append(m.buf, ',')
				m.vmWriteIndent(ctx)
				elemPtr := unsafe.Add(frame.iterData(), uintptr(idx)*elemSize)
				if err := m.encodeAnyIface(elemPtr); err != nil {
					return err
				}
			}
			// Close array, pop iter frame.
			// Advance past SLICE_END: body byte length is in SLICE_BEGIN ext.OperandB,
			// then skip 16 bytes for SLICE_END itself.
			ctx.IndentDepth--
			m.vmWriteIndent(ctx)
			m.buf = append(m.buf, ']')
			// Decrement stack depth in vmstate and clear first flag.
			ctx.VMState-- // VJ_ST_DEC_STACK_DEPTH: stack_depth at bits [0..7]
			ctx.VMState &^= vjStFirstBit
			ctx.CurBase = frame.RetBase
			bodyByteLen := prevExt.OperandB
			// New PC = body start (ctx.PC) + body bytes + 16 (SLICE_END size)
			ctx.PC = ctx.PC + bodyByteLen + 16
			return errVMContinue
		}
	}

	// Normal advance: skip past the 8-byte OP_INTERFACE instruction.
	ctx.PC += 8
	// A field was written, so clear first flag in vmstate.
	ctx.VMState &^= vjStFirstBit
	return nil
}
