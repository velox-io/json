package venc

import "reflect"

type Label uint32

const InvalidLabel Label = 0

const (
	irLabel   uint16 = 0x100 // defines a label position (LabelID)
	irComment uint16 = 0x101 // debug comment (no output)
)

type IRInst struct {
	Op       uint16 // VM opcode or IR pseudo-op
	KeyLen   uint8
	KeyOff   uint16
	FieldOff uint16

	OperandA int32 // semantics depend on opcode
	OperandB int32

	Target   Label // forward jumps: SKIP_IF_ZERO/PTR_DEREF/CALL/SLICE_BEGIN/ARRAY_BEGIN
	LoopBack Label // back-edge: SLICE_END -> body start

	// Label definition (Op == irLabel).
	LabelID Label

	Annotation string       // debug note, e.g. type name for OBJ_OPEN/CALL
	Fallback   *fbInfo      // OP_FALLBACK / OP_MAP fallback info
	SourceType reflect.Type // associated struct type for dedup
}

type irBuilder struct {
	insts     []IRInst
	nextLabel Label // starts at 1; 0 is InvalidLabel

	// visiting[ti]: InvalidLabel => compiling now; other label => entry already allocated.
	visiting  map[*EncTypeInfo]Label
	cycleSubs []cycleSub
}

type cycleSub struct {
	ti    *EncTypeInfo
	label Label
}

func (b *irBuilder) allocLabel() Label {
	b.nextLabel++
	return b.nextLabel
}

func (b *irBuilder) defineLabel(l Label) {
	b.insts = append(b.insts, IRInst{
		Op:      irLabel,
		LabelID: l,
	})
}

func (b *irBuilder) emit(inst IRInst) int {
	idx := len(b.insts)
	b.insts = append(b.insts, inst)
	return idx
}

func (b *irBuilder) addKey(keyBytes []byte) (keyOff uint16, keyLen uint8, ok bool) {
	if len(keyBytes) == 0 {
		return 0, 0, true
	}
	return globalKeyPoolInsert(keyBytes)
}
