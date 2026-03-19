package vjson

import (
	"fmt"
	"unsafe"
)

// lower converts an IR instruction sequence into a Blueprint byte stream.
// Two-pass: first computes byte offsets for each label, then emits bytes.
func lower(insts []IRInst) (ops []byte, fallbacks map[int]*fbInfo, annotations map[int]string) {
	fallbacks = make(map[int]*fbInfo)

	// Pass 1: compute byte offset of each instruction and label positions
	labelOffsets := make(map[Label]int)
	instOffsets := make([]int, len(insts))
	offset := 0
	for i := range insts {
		instOffsets[i] = offset
		inst := &insts[i]
		switch {
		case inst.Op == irLabel:
			labelOffsets[inst.LabelID] = offset
			// pseudo-op: 0 bytes
		case inst.Op == irComment:
			// pseudo-op: 0 bytes
		case isLongOp(inst.Op):
			offset += 16
		default:
			offset += 8
		}
	}
	totalSize := offset

	// Pass 2: emit bytes
	ops = make([]byte, 0, totalSize)

	for i := range insts {
		inst := &insts[i]
		byteOff := instOffsets[i]

		// Skip pseudo-ops.
		if inst.Op == irLabel || inst.Op == irComment {
			continue
		}

		// Record annotation if present.
		if inst.Annotation != "" {
			if annotations == nil {
				annotations = make(map[int]string)
			}
			annotations[byteOff] = inst.Annotation
		}

		// Record fallback info.
		if inst.Fallback != nil {
			fallbacks[byteOff] = inst.Fallback
		}

		// Resolve operands from labels.
		operandA := inst.OperandA
		operandB := inst.OperandB
		resolveOperands(inst, byteOff, labelOffsets, &operandA, &operandB)

		// Emit instruction bytes.
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

// resolveOperands fills in operandA/operandB from symbolic labels based on opcode semantics.
func resolveOperands(inst *IRInst, selfOff int, labelOffsets map[Label]int, a, b *int32) {
	switch inst.Op {
	case opSkipIfZero:
		// Target = after the skipped region; OperandA = byte distance from self
		if inst.Target != InvalidLabel {
			targetOff := mustResolve(labelOffsets, inst.Target, "SKIP_IF_ZERO")
			*a = int32(targetOff - selfOff)
		}
		// OperandB = ZeroCheckTag (kind), already set

	case opCall:
		// Target = absolute byte offset of subroutine
		if inst.Target != InvalidLabel {
			targetOff := mustResolve(labelOffsets, inst.Target, "CALL")
			*a = int32(targetOff)
		}

	case opPtrDeref:
		// Target = after PTR_END; OperandA = byte distance from self
		if inst.Target != InvalidLabel {
			targetOff := mustResolve(labelOffsets, inst.Target, "PTR_DEREF")
			*a = int32(targetOff - selfOff)
		}

	case opSliceBegin:
		// Target = afterSlice label (after SLICE_END).
		// OperandA = elem_size (already set).
		// OperandB = body byte length = afterSlice - bodyStart - 16 (exclude SLICE_END).
		if inst.Target != InvalidLabel {
			afterSliceOff := mustResolve(labelOffsets, inst.Target, "SLICE_BEGIN")
			bodyStart := selfOff + 16
			*b = int32(afterSliceOff - bodyStart - 16) // exclude SLICE_END (16 bytes)
		}

	case opSliceEnd:
		// LoopBack = body start; OperandA = relative offset (negative)
		if inst.LoopBack != InvalidLabel {
			loopBackOff := mustResolve(labelOffsets, inst.LoopBack, "SLICE_END")
			*a = int32(loopBackOff - selfOff)
		}
		// OperandB = elem_size, already set

	case opArrayBegin:
		// Same as SLICE_BEGIN for body byte length computation.
		// OperandA = packed (elem_size|array_len), already set
		if inst.Target != InvalidLabel {
			afterArrayOff := mustResolve(labelOffsets, inst.Target, "ARRAY_BEGIN")
			bodyStart := selfOff + 16
			*b = int32(afterArrayOff - bodyStart - 16) // exclude SLICE_END
		}

	case opMapBegin:
		// Target = MAP_END position; OperandA = byte distance from self
		if inst.Target != InvalidLabel {
			targetOff := mustResolve(labelOffsets, inst.Target, "MAP_BEGIN")
			*a = int32(targetOff - selfOff)
		}

	case opMapStrIter:
		// OperandA = slot_size (already set).
		// OperandB = body byte length (between MAP_STR_ITER and MAP_STR_ITER_END).
		// The C VM uses: VM_JUMP_BYTES(16 + body_len + 16) to skip
		// self (16) + body + ITER_END (16) on nil/empty maps.
		if inst.Target != InvalidLabel {
			afterIterOff := mustResolve(labelOffsets, inst.Target, "MAP_STR_ITER")
			bodyStart := selfOff + 16
			*b = int32(afterIterOff - bodyStart)
		}

	case opMapStrIterEnd:
		// LoopBack = body start; OperandA = relative offset (negative)
		if inst.LoopBack != InvalidLabel {
			loopBackOff := mustResolve(labelOffsets, inst.LoopBack, "MAP_STR_ITER_END")
			*a = int32(loopBackOff - selfOff)
		}
		// OperandB = slot_size, already set
	}
}

// isLongOp returns true for 16-byte (header + extension) opcodes.
func isLongOp(op uint16) bool {
	switch op {
	case opSkipIfZero, opCall, opPtrDeref,
		opSliceBegin, opSliceEnd,
		opMapBegin, opArrayBegin,
		opSeqFloat64, opSeqInt, opSeqInt64, opSeqString,
		opMapStrIter, opMapStrIterEnd:
		return true
	}
	return false
}

// mustResolve looks up a label's byte offset, panicking if not found.
func mustResolve(offsets map[Label]int, l Label, context string) int {
	off, ok := offsets[l]
	if !ok {
		panic(fmt.Sprintf("vjson: lower: unresolved label %d in %s", l, context))
	}
	return off
}
