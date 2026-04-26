package venc

import (
	"errors"
	"unsafe"
)

var errVMContinue = errors.New("venc: vm continue")

func (es *encodeState) handleInterfaceYield(ctx *VjExecCtx, activeBP *Blueprint) error {
	hdr := opHdrAt(activeBP.Ops, ctx.PC)
	isFirst := vmstateGetFirst(ctx.VMState)
	ifacePtr := unsafe.Add(ctx.CurBase, uintptr(hdr.FieldOff))

	if !isFirst {
		es.buf = append(es.buf, ',')
		es.writeIndent(ctx)
	}
	if hdr.KeyLen > 0 {
		es.buf = append(es.buf, keyPoolBytes(hdr.KeyOff, hdr.KeyLen)...)
		es.writeKeySpace(ctx)
	}

	if err := es.encodeAnyIface(ifacePtr); err != nil {
		return err
	}

	// Only slice loops can hand off the remaining interface{} elements in one batch.
	stackDepth := vmstateGetStackDepth(ctx.VMState)
	if stackDepth > 0 && ctx.PC >= 16 {
		frame := &ctx.Stack[stackDepth-1]
		prevPC := ctx.PC - 16
		if int(prevPC) >= 0 &&
			opHdrAt(activeBP.Ops, prevPC).OpType == opSliceBegin {
			prevExt := opExtAt(activeBP.Ops, prevPC)
			elemSize := uintptr(prevExt.OperandA)
			count := frame.iterCount()
			for idx := frame.iterIdx() + 1; idx < count; idx++ {
				es.buf = append(es.buf, ',')
				es.writeIndent(ctx)
				elemPtr := unsafe.Add(frame.iterData(), uintptr(idx)*elemSize)
				if err := es.encodeAnyIface(elemPtr); err != nil {
					return err
				}
			}
			ctx.IndentDepth--
			es.writeIndent(ctx)
			es.buf = append(es.buf, ']')
			ctx.VMState--
			ctx.VMState &^= vjStFirstBit
			ctx.CurBase = frame.RetBase
			bodyByteLen := prevExt.OperandB
			ctx.PC = ctx.PC + bodyByteLen + 16
			return errVMContinue
		}
	}

	ctx.PC += 8
	ctx.VMState &^= vjStFirstBit
	return nil
}
