package vjson

import "reflect"

// Label is a symbolic jump target used in the IR before lowering to byte offsets.
// InvalidLabel (0) means "no label assigned".
type Label uint32

const InvalidLabel Label = 0

// IR-only pseudo-opcodes. These are never lowered to the byte stream.
const (
	irLabel   uint16 = 0x100 // defines a label position (LabelID)
	irComment uint16 = 0x101 // debug comment (no output)
)

// IRInst is one instruction in the compiler IR.
// VM opcodes use Op directly; pseudo-ops (irLabel, irComment) carry metadata only.
type IRInst struct {
	Op       uint16 // VM opcode or IR pseudo-op
	KeyLen   uint8
	KeyOff   uint16
	FieldOff uint16

	OperandA int32 // semantics depend on opcode
	OperandB int32

	// Symbolic jump targets (resolved to byte offsets during lowering).
	Target   Label // forward jump: SKIP_IF_ZERO, PTR_DEREF, CALL, MAP_BEGIN, SLICE_BEGIN, ARRAY_BEGIN
	LoopBack Label // back-edge: SLICE_END → body start

	// Label definition (only when Op == irLabel).
	LabelID Label

	// Metadata (not emitted into the Blueprint byte stream).
	Annotation string       // debug annotation (type name for OBJ_OPEN/CALL)
	Fallback   *fbInfo      // OP_FALLBACK / OP_MAP_BEGIN fallback info
	SourceType reflect.Type // associated struct type (used by dedup pass)
}

// irBuilder accumulates IR instructions during compilation.
type irBuilder struct {
	insts     []IRInst
	nextLabel Label // starts at 1; 0 = InvalidLabel

	// Cycle detection / subroutine scheduling.
	// Value semantics:
	//   present && value == InvalidLabel: currently being compiled (on the call chain)
	//   present && value != InvalidLabel: subroutine entry label already allocated
	visiting    map[reflect.Type]Label
	pendingSubs []pendingSub
}

// pendingSub records a struct type that needs subroutine emission,
// along with its pre-allocated entry label.
type pendingSub struct {
	dec   *StructCodec
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
