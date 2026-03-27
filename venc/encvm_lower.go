package venc

import (
	"fmt"
	"unsafe"
)

// lower resolves labels and encodes IR into the flat VM byte stream.
func lower(insts []IRInst) (ops []byte, fallbacks map[int]*fbInfo, annotations map[int]string) {
	fallbacks = make(map[int]*fbInfo)

	labelOffsets := make(map[Label]int)
	instOffsets := make([]int, len(insts))
	offset := 0
	for i := range insts {
		instOffsets[i] = offset
		inst := &insts[i]
		switch {
		case inst.Op == irLabel:
			labelOffsets[inst.LabelID] = offset
		case inst.Op == irComment:
		case isLongOp(inst.Op):
			offset += 16
		default:
			offset += 8
		}
	}
	totalSize := offset

	ops = make([]byte, 0, totalSize)

	for i := range insts {
		inst := &insts[i]
		byteOff := instOffsets[i]

		if inst.Op == irLabel || inst.Op == irComment {
			continue
		}

		if inst.Annotation != "" {
			if annotations == nil {
				annotations = make(map[int]string)
			}
			annotations[byteOff] = inst.Annotation
		}

		if inst.Fallback != nil {
			fallbacks[byteOff] = inst.Fallback
		}

		operandA := inst.OperandA
		operandB := inst.OperandB
		resolveOperands(inst, byteOff, labelOffsets, &operandA, &operandB)

		hdr := VjOpHdr{
			OpType:   inst.Op,
			KeyLen:   inst.KeyLen,
			KeyOff:   inst.KeyOff,
			FieldOff: inst.FieldOff,
		}
		ops = append(ops, (*[8]byte)(unsafe.Pointer(&hdr))[:]...)

		if isLongOp(inst.Op) {
			ext := VjOpExt{
				OperandA: operandA,
				OperandB: operandB,
			}
			ops = append(ops, (*[8]byte)(unsafe.Pointer(&ext))[:]...)
		}
	}

	if len(ops) != totalSize {
		panic(fmt.Sprintf("vjson: lower: expected %d bytes, got %d", totalSize, len(ops))) // internal bug: pass1/pass2 size mismatch
	}

	return ops, fallbacks, annotations
}

// resolveOperands converts symbolic labels into the opcode-specific byte offsets.
func resolveOperands(inst *IRInst, selfOff int, labelOffsets map[Label]int, a, b *int32) {
	switch inst.Op {
	case opSkipIfZero:
		if inst.Target != InvalidLabel {
			targetOff := mustResolve(labelOffsets, inst.Target, "SKIP_IF_ZERO")
			*a = int32(targetOff - selfOff)
		}

	case opCall:
		if inst.Target != InvalidLabel {
			targetOff := mustResolve(labelOffsets, inst.Target, "CALL")
			*a = int32(targetOff)
		}

	case opPtrDeref:
		if inst.Target != InvalidLabel {
			targetOff := mustResolve(labelOffsets, inst.Target, "PTR_DEREF")
			*a = int32(targetOff - selfOff)
		}

	case opSliceBegin:
		if inst.Target != InvalidLabel {
			afterSliceOff := mustResolve(labelOffsets, inst.Target, "SLICE_BEGIN")
			bodyStart := selfOff + 16
			*b = int32(afterSliceOff - bodyStart - 16)
		}

	case opSliceEnd:
		if inst.LoopBack != InvalidLabel {
			loopBackOff := mustResolve(labelOffsets, inst.LoopBack, "SLICE_END")
			*a = int32(loopBackOff - selfOff)
		}

	case opArrayBegin:
		if inst.Target != InvalidLabel {
			afterArrayOff := mustResolve(labelOffsets, inst.Target, "ARRAY_BEGIN")
			bodyStart := selfOff + 16
			*b = int32(afterArrayOff - bodyStart - 16)
		}

	case opMapStrIter:
		// Native skip logic adds self and ITER_END around body_len.
		if inst.Target != InvalidLabel {
			afterIterOff := mustResolve(labelOffsets, inst.Target, "MAP_STR_ITER")
			bodyStart := selfOff + 16
			*b = int32(afterIterOff - bodyStart)
		}

	case opMapStrIterEnd:
		if inst.LoopBack != InvalidLabel {
			loopBackOff := mustResolve(labelOffsets, inst.LoopBack, "MAP_STR_ITER_END")
			*a = int32(loopBackOff - selfOff)
		}
	}
}

// isLongOp reports whether the opcode carries a VjOpExt suffix.
func isLongOp(op uint16) bool {
	switch op {
	case opSkipIfZero, opCall, opPtrDeref,
		opSliceBegin, opSliceEnd,
		opArrayBegin,
		opSeqFloat64, opSeqInt, opSeqInt64, opSeqString,
		opMapStrIter, opMapStrIterEnd:
		return true
	}
	return false
}

func mustResolve(offsets map[Label]int, l Label, context string) int {
	off, ok := offsets[l]
	if !ok {
		panic(fmt.Sprintf("vjson: lower: unresolved label %d in %s", l, context))
	}
	return off
}
