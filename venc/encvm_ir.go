package venc

import "reflect"

// Label is an IR-level jump target.
// InvalidLabel (0) means unassigned.
type Label uint32

const InvalidLabel Label = 0

// IR-only pseudo-opcodes, never emitted to bytecode.
const (
	irLabel   uint16 = 0x100 // defines a label position (LabelID)
	irComment uint16 = 0x101 // debug comment (no output)
)

// IRInst is one compiler IR instruction.
// Op is either a VM opcode or an IR pseudo-op (irLabel/irComment).
type IRInst struct {
	Op       uint16 // VM opcode or IR pseudo-op
	KeyLen   uint8
	KeyOff   uint16
	FieldOff uint16

	OperandA int32 // semantics depend on opcode
	OperandB int32

	// Symbolic jump targets, resolved to byte offsets during lowering.
	Target   Label // forward jumps: SKIP_IF_ZERO/PTR_DEREF/CALL/SLICE_BEGIN/ARRAY_BEGIN
	LoopBack Label // back-edge: SLICE_END -> body start

	// Label definition (Op == irLabel).
	LabelID Label

	// IR metadata, not emitted to Blueprint bytes.
	Annotation string       // debug note, e.g. type name for OBJ_OPEN/CALL
	Fallback   *fbInfo      // OP_FALLBACK / OP_MAP fallback info
	SourceType reflect.Type // associated struct type for dedup
}

// irBuilder collects IR instructions during compilation.
type irBuilder struct {
	insts     []IRInst
	nextLabel Label // starts at 1; 0 is InvalidLabel

	// Cycle detection and subroutine scheduling.
	// visiting[t]: InvalidLabel => compiling now; other label => entry already allocated.
	visiting    map[reflect.Type]Label
	pendingSubs []pendingSub
}

// pendingSub stores a struct type waiting for subroutine emission and its entry label.
type pendingSub struct {
	si    *EncStructInfo
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
