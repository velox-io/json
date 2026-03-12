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
	op := activeBP.Ops[ctx.PC]
	isFirst := vmstateGetFirst(ctx.VMState)
	ifacePtr := unsafe.Add(ctx.CurBase, uintptr(op.FieldOff))

	if !isFirst {
		m.buf = append(m.buf, ',')
		m.vmWriteIndent(ctx)
	}
	if op.KeyLen > 0 {
		keyBytes := unsafe.Slice((*byte)(op.KeyPtr), op.KeyLen)
		m.buf = append(m.buf, keyBytes...)
		m.vmWriteKeySpace(ctx)
	}

	// Encode the current interface{} element.
	if err := m.encodeAnyIface(ifacePtr); err != nil {
		return err
	}

	// Batch slice takeover: encode remaining []interface{} elements in Go,
	// saving N-1 C↔Go round-trips.
	// Only safe when parent frame is a SLICE in activeBP.Ops.
	depth := vmstateGetDepth(ctx.VMState)
	if depth > 0 && ctx.PC > 0 {
		frame := &ctx.Stack[depth-1]
		if vmstateGetTopFrame(ctx.VMState) == vjFrameLoop &&
			int(ctx.PC-1) < len(activeBP.Ops) &&
			activeBP.Ops[ctx.PC-1].OpType == opSliceBegin {
			// Encode remaining slice elements in Go.
			// elem_size is in the SLICE_BEGIN instruction's OperandA.
			elemSize := uintptr(activeBP.Ops[ctx.PC-1].OperandA)
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
			// PC past SLICE_END = PC + body_len + 1.
			ctx.IndentDepth--
			m.vmWriteIndent(ctx)
			m.buf = append(m.buf, ']')
			// Decrement depth in vmstate and clear first flag.
			ctx.VMState-- // VJ_ST_DEC_DEPTH: depth at bits [0..7]
			ctx.VMState &^= vjStFirstBit
			ctx.CurBase = frame.RetBase
			bodyLen := activeBP.Ops[ctx.PC-1].OperandB
			ctx.PC = ctx.PC + bodyLen + 1
			return errVMContinue
		}
	}

	ctx.PC++
	// A field was written, so clear first flag in vmstate.
	ctx.VMState &^= vjStFirstBit
	return nil
}
