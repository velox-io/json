package vjson

import (
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ================================================================
// OpCode constants — mirror native/impl/encoder_types.h enum OpType
//
// Values 0-13 are primitives, aligned with ElemTypeKind so the
// pre-compiler can map them directly via kindToOpcode().
// Values 16-19 are non-primitive data ops (interface, raw, etc.).
// Values 32-40 are structural control-flow instructions.
// Value 0x3F is the Go-only fallback.
//
// All opcodes share a single sparse dispatch table in the C VM
// (dispatch_table[0x40]).
// ================================================================
const (
	// Primitive types (0-13), aligned with ElemTypeKind.
	opBool    uint16 = 0
	opInt     uint16 = 1
	opInt8    uint16 = 2
	opInt16   uint16 = 3
	opInt32   uint16 = 4
	opInt64   uint16 = 5
	opUint    uint16 = 6
	opUint8   uint16 = 7
	opUint16  uint16 = 8
	opUint32  uint16 = 9
	opUint64  uint16 = 10
	opFloat32 uint16 = 11
	opFloat64 uint16 = 12
	opString  uint16 = 13

	// Non-primitive data ops (16-19).
	opInterface  uint16 = 16 // interface{} — noinline C encoder or yield
	opRawMessage uint16 = 17 // json.RawMessage — direct byte copy
	opNumber     uint16 = 18 // json.Number — direct string copy
	opByteSlice  uint16 = 19 // []byte — base64, yield to Go

	// Structural control-flow instructions (32-42).
	opSkipIfZero  uint16 = 32 // conditional forward jump (omitempty)
	opStructBegin uint16 = 33 // push frame, write '{'
	opStructEnd   uint16 = 34 // write '}', pop frame
	opPtrDeref    uint16 = 35 // deref pointer, nil→null+jump
	opPtrEnd      uint16 = 36 // pop ptr-deref frame, restore base
	opSliceBegin  uint16 = 37 // slice loop start
	opSliceEnd    uint16 = 38 // slice loop end / back-edge
	opMapBegin    uint16 = 39 // map iteration start (yield-driven)
	opMapEnd      uint16 = 40 // map iteration end (yield)
	opObjOpen     uint16 = 41 // write key + '{', set first=1 (no frame push)
	opObjClose    uint16 = 42 // write '}', set first=0 (no frame pop)

	// Go-only fallback (0x3F).
	opFallback uint16 = 0x3F // custom marshalers, ,string, complex structs

	// Sentinel.
	opEnd uint16 = 0xFF
)

// opTypeMask extracts the opcode from OpType, stripping flags and
// the high byte used by opSkipIfZero to encode the ZeroCheckTag.
const opTypeMask = uint16(0x00FF)

// kindToOpcode maps an ElemTypeKind to the corresponding VM instruction
// opcode.  Primitives 0-13 map 1:1; other kinds map to their actual
// instruction opcode (which differs from the Kind value).
//
// Panics for kinds that have no corresponding single instruction
// (KindStruct, KindSlice, KindPointer, KindMap — these use structural
// opcodes like opStructBegin/opPtrDeref/opSliceBegin/opMapBegin instead).
func kindToOpcode(k ElemTypeKind) uint16 {
	switch {
	case k <= KindString:
		// Primitives: 1:1 mapping with ElemTypeKind.
		return uint16(k)
	case k == KindAny:
		return opInterface
	case k == KindRawMessage:
		return opRawMessage
	case k == KindNumber:
		return opNumber
	default:
		panic("kindToOpcode: no direct opcode for this ElemTypeKind")
	}
}

// ================================================================
// Error codes returned in ExecCtx.ErrCode.
// ================================================================
const (
	vjOK           int32 = 0
	vjErrBufFull   int32 = 1
	vjErrStackOvfl int32 = 3
	vjErrNanInf    int32 = 5
	vjErrYield     int32 = 6 // VM yielded to Go (interface miss, fallback, map iter)
)

// ================================================================
// Encoding flags (VjEncFlags bitmask).
// ================================================================
const (
	vjEncEscapeHTML uint32 = 1 << 0

	// Hot resume flags for re-entering C after Go handles a fallback field.
	vjEncResume      uint32 = 1 << 7 // skip opening '{'; resume mid-struct
	vjEncResumeFirst uint32 = 1 << 8 // with RESUME: no field written yet (first=1)
)

// Bitmask constant for decoding esc_op_idx (packed by C on fallback).
// Bit 31 = first flag.
const escOpFirstBit uint32 = 0x80000000

// ================================================================
// Yield reason values stored in ExecCtx.YieldInfo.
// ================================================================
const (
	yieldFallback  uint32 = 1 // custom marshaler / ,string / unsupported type
	yieldIfaceMiss uint32 = 2 // interface{} cache miss — need Go compilation
	yieldMapNext   uint32 = 3 // map iteration — need Go to provide next k/v
)

// ================================================================
// Types — OpStep, Blueprint, StackFrame, ExecCtx, IfaceCache
// ================================================================

// VjOpStep mirrors the C OpStep.
//
// Layout (64-bit, 24 bytes):
//
//	offset  0: OpType    uint16  (2 bytes)
//	offset  2: KeyLen    uint16  (2 bytes)
//	offset  4: FieldOff  uint32  (4 bytes)
//	offset  8: KeyPtr    pointer (8 bytes) — pre-escaped key in KeyPool
//	offset 16: OperandA  int32   (4 bytes) — jump offset / elem_size
//	offset 20: OperandB  int32   (4 bytes) — body length / reserved
type VjOpStep struct {
	OpType   uint16
	KeyLen   uint16
	FieldOff uint32
	KeyPtr   unsafe.Pointer // points into Blueprint.KeyPool
	OperandA int32          // jump offset, elem_size, etc.
	OperandB int32          // loop body len, reserved
}

// Compile-time size assertion: VjOpStep must be exactly 24 bytes.
var _ [24]byte = [unsafe.Sizeof(VjOpStep{})]byte{}

// Blueprint holds the compiled instruction stream for a type.
// It is immutable after construction and safe for concurrent use.
type Blueprint struct {
	Ops       []VjOpStep      // linear instruction stream, terminated by opEnd
	KeyPool   []byte          // contiguous storage for all pre-encoded keys
	Fallbacks map[int]*fbInfo // PC index → fallback field info (only for OP_FALLBACK instructions)
}

// fbInfo describes a fallback field that requires Go encoding.
// Stored in Blueprint.Fallbacks, indexed by the PC of the OP_FALLBACK instruction.
type fbInfo struct {
	TI     *TypeInfo // field's TypeInfo (for EncodeFn dispatch)
	Offset uintptr   // field offset from current struct base
}

// encvmCache holds compiled encoder VM data for a StructCodec.
// Stored as a separate struct to avoid bloating the StructCodec with
// fields that are only relevant when the native encoder is available.
type encvmCache struct {
	once      sync.Once  // once for Blueprint compilation
	blueprint *Blueprint // compiled Blueprint (flat instruction stream)
}

// maxStackDepth matches the C VJ_MAX_DEPTH.
const maxStackDepth = 16

// VjStackFrame mirrors the C VjStackFrame.
//
// Layout (72 bytes):
//
//	offset  0: RetOp      pointer (8) — return instruction pointer
//	offset  8: RetBase    pointer (8) — parent struct/elem base
//	offset 16: First      int32   (4) — parent first-field flag
//	offset 20: FrameType  int32   (4) — FRAME_STRUCT/FRAME_SLICE/FRAME_IFACE
//	offset 24: RetOps     pointer (8) — parent ops base (FRAME_IFACE only)
//	offset 32: IterData   pointer (8) — slice data start
//	offset 40: IterCount  int64   (8) — total elements
//	offset 48: IterIdx    int64   (8) — current index
//	offset 56: ElemSize   int32   (4) — element size in bytes
//	offset 60: _pad       int32   (4)
//	offset 64: LoopPcOp   pointer (8) — loop body first instruction
type VjStackFrame struct {
	RetOp     unsafe.Pointer
	RetBase   unsafe.Pointer
	First     int32
	FrameType int32
	RetOps    unsafe.Pointer
	IterData  unsafe.Pointer
	IterCount int64
	IterIdx   int64
	ElemSize  int32
	_pad      int32
	LoopPcOp  unsafe.Pointer
}

// Compile-time size assertion: VjStackFrame must be 72 bytes.
var _ [72]byte = [unsafe.Sizeof(VjStackFrame{})]byte{}

// VjExecCtx mirrors the C VjExecCtx (runtime context per Marshal call).
//
// Layout:
//
//	offset   0: BufCur          pointer   (8)
//	offset   8: BufEnd          uintptr   (8)  — NOT a GC pointer
//	offset  16: PC              int32     (4)
//	offset  20: _pad1           int32     (4)
//	offset  24: CurBase         pointer   (8)
//	offset  32: Depth           int32     (4)
//	offset  36: ErrorCode       int32     (4)
//	offset  40: EncFlags        uint32    (4)
//	offset  44: YieldInfo       uint32    (4)  — yield reason
//	offset  48: OpsPtr          pointer   (8)  — &Blueprint.Ops[0]
//	offset  56: _reserved56     uintptr   (8)  — reserved (KeyPoolPtr)
//	offset  64: IfaceCachePtr   pointer   (8)  — *VjIfaceCacheEntry sorted array
//	offset  72: IfaceCacheCount int32     (4)
//	offset  76: _pad2           int32     (4)
//	offset  80: YieldTypePtr    pointer   (8)  — eface.type_ptr on iface miss
//	offset  88: YieldFieldIdx   int32     (4)
//	offset  92: _pad3           int32     (4)
//	offset  96: Stack           [16]VjStackFrame (16*72 = 1152)
//
// Total: 96 + 1152 = 1248 bytes
type VjExecCtx struct {
	BufCur    unsafe.Pointer // current write position
	BufEnd    uintptr        // one past last writable byte (NOT GC-traced)
	PC        int32          // current instruction index (relative to OpsPtr)
	_pad1     int32
	CurBase   unsafe.Pointer // current struct/elem base address
	Depth     int32          // stack depth (0 = top-level)
	ErrCode   int32          // VjError enum value
	EncFlags  uint32         // VjEncFlags bitmask
	YieldInfo uint32         // yield reason (yieldFallback, yieldIfaceMiss, etc.)

	OpsPtr      unsafe.Pointer // &Blueprint.Ops[0] (current active instruction stream)
	_reserved56 uintptr        // reserved for KeyPoolPtr

	IfaceCachePtr   unsafe.Pointer // *VjIfaceCacheEntry sorted array
	IfaceCacheCount int32          // number of entries
	_pad2           int32

	YieldTypePtr  unsafe.Pointer // interface cache miss: eface.type_ptr
	YieldFieldIdx int32          // fallback: field index for Go to handle
	_pad3         int32

	Stack [maxStackDepth]VjStackFrame
}

// Compile-time size assertion for VjExecCtx.
// Header: 96 bytes, Stack: 16 * 72 = 1152 bytes, Total: 1248 bytes.
var _ [1248]byte = [unsafe.Sizeof(VjExecCtx{})]byte{}

// VjIfaceCacheEntry maps a Go *abi.Type to its compiled Blueprint ops.
//
// Layout (24 bytes):
//
//	offset  0: TypePtr  pointer (8) — Go *abi.Type address
//	offset  8: OpsPtr   pointer (8) — &Blueprint.Ops[0], or nil
//	offset 16: Tag      uint8   (1) — (opcode+1) for primitives, 0 = none
//	offset 17: _pad     [7]byte (7)
//
// Tag encoding: stored as (opcode + 1) so that tag=0 is an unambiguous
// sentinel for "no primitive tag". OP_BOOL (opcode 0) → tag=1, etc.
// C subtracts 1 before dispatching to vj_encode_ptr_value.
type VjIfaceCacheEntry struct {
	TypePtr unsafe.Pointer // *abi.Type (address-comparable)
	OpsPtr  unsafe.Pointer // &Blueprint.Ops[0], nil if not compilable by C
	Tag     uint8          // (opcode+1) for primitives, 0 for complex/yield
	_pad    [7]byte
}

// Compile-time size assertion for VjIfaceCacheEntry.
var _ [24]byte = [unsafe.Sizeof(VjIfaceCacheEntry{})]byte{}

// ifaceCacheSnapshot is an immutable sorted array of cache entries.
// Once published, it is never modified — new entries produce a new snapshot.
type ifaceCacheSnapshot struct {
	entries []VjIfaceCacheEntry // sorted by TypePtr (ascending)
}

// ================================================================
// Global state
// ================================================================

// globalIfaceCache is the process-wide interface type cache.
// Updated via COW: readers get a snapshot pointer, writers create
// a new snapshot under mu and atomically publish it.
var globalIfaceCache struct {
	current atomic.Pointer[ifaceCacheSnapshot]
	mu      sync.Mutex
}

var initPrimitiveIfaceCacheOnce sync.Once

// ================================================================
// Functions
// ================================================================

func init() {
	// Initialize with an empty snapshot so Load() never returns nil.
	globalIfaceCache.current.Store(&ifaceCacheSnapshot{})
}

// lookup returns the entry for typePtr via binary search, or nil.
func (s *ifaceCacheSnapshot) lookup(typePtr unsafe.Pointer) *VjIfaceCacheEntry {
	tp := uintptr(typePtr)
	lo, hi := 0, len(s.entries)-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		midTP := uintptr(s.entries[mid].TypePtr)
		if midTP == tp {
			return &s.entries[mid]
		}
		if midTP < tp {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return nil
}

// loadIfaceCacheSnapshot returns the current immutable cache snapshot.
func loadIfaceCacheSnapshot() *ifaceCacheSnapshot {
	return globalIfaceCache.current.Load()
}

// insertIfaceCache adds a new type→blueprint mapping to the global cache.
// Thread-safe via mutex; uses COW to avoid interfering with concurrent readers.
func insertIfaceCache(typePtr unsafe.Pointer, bp *Blueprint, tag uint8) {
	globalIfaceCache.mu.Lock()
	defer globalIfaceCache.mu.Unlock()

	// Double-check: another goroutine may have already inserted it.
	cur := globalIfaceCache.current.Load()
	if cur.lookup(typePtr) != nil {
		return
	}

	entry := VjIfaceCacheEntry{
		TypePtr: typePtr,
		Tag:     tag,
	}
	if bp != nil && len(bp.Ops) > 0 {
		entry.OpsPtr = unsafe.Pointer(&bp.Ops[0])
	}

	// COW: create new sorted array = old + new entry.
	newEntries := make([]VjIfaceCacheEntry, len(cur.entries)+1)
	copy(newEntries, cur.entries)
	newEntries[len(cur.entries)] = entry
	sort.Slice(newEntries, func(i, j int) bool {
		return uintptr(newEntries[i].TypePtr) < uintptr(newEntries[j].TypePtr)
	})

	globalIfaceCache.current.Store(&ifaceCacheSnapshot{entries: newEntries})
}

// initPrimitiveIfaceCache seeds the interface cache with all primitive types
// so the C VM can inline-encode bool/int/string etc. without yielding.
func initPrimitiveIfaceCache() {
	entries := []struct {
		t   reflect.Type
		tag uint8
	}{
		// Tag stored as (opcode + 1) so tag=0 means "no tag".
		{reflect.TypeOf(false), uint8(opBool) + 1},
		{reflect.TypeOf(int(0)), uint8(opInt) + 1},
		{reflect.TypeOf(int8(0)), uint8(opInt8) + 1},
		{reflect.TypeOf(int16(0)), uint8(opInt16) + 1},
		{reflect.TypeOf(int32(0)), uint8(opInt32) + 1},
		{reflect.TypeOf(int64(0)), uint8(opInt64) + 1},
		{reflect.TypeOf(uint(0)), uint8(opUint) + 1},
		{reflect.TypeOf(uint8(0)), uint8(opUint8) + 1},
		{reflect.TypeOf(uint16(0)), uint8(opUint16) + 1},
		{reflect.TypeOf(uint32(0)), uint8(opUint32) + 1},
		{reflect.TypeOf(uint64(0)), uint8(opUint64) + 1},
		{reflect.TypeOf(float32(0)), uint8(opFloat32) + 1},
		{reflect.TypeOf(float64(0)), uint8(opFloat64) + 1},
		{reflect.TypeOf(""), uint8(opString) + 1},
	}
	table := make([]VjIfaceCacheEntry, len(entries))
	for i, e := range entries {
		table[i] = VjIfaceCacheEntry{
			TypePtr: rtypePtr(e.t),
			Tag:     e.tag,
		}
	}
	sort.Slice(table, func(i, j int) bool {
		return uintptr(table[i].TypePtr) < uintptr(table[j].TypePtr)
	})
	globalIfaceCache.current.Store(&ifaceCacheSnapshot{entries: table})
}

// ensureIfaceCache initializes the primitive type cache on first use.
func ensureIfaceCache() {
	initPrimitiveIfaceCacheOnce.Do(initPrimitiveIfaceCache)
}

// initMarshalerVMCtx sets up the interface cache snapshot on the pooled
// Marshaler's VjExecCtx. Called once per getMarshaler() so that execVM
// doesn't need a per-call atomic.Load + sync.Once check.
func initMarshalerVMCtx(m *Marshaler) {
	ensureIfaceCache()
	snap := loadIfaceCacheSnapshot()
	if len(snap.entries) > 0 {
		m.vmCtx.IfaceCachePtr = unsafe.Pointer(&snap.entries[0])
		m.vmCtx.IfaceCacheCount = int32(len(snap.entries))
	} else {
		m.vmCtx.IfaceCachePtr = nil
		m.vmCtx.IfaceCacheCount = 0
	}
}
