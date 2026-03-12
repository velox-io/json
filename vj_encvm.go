package vjson

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

// OpCode constants — mirror native/impl/encoder_types.h enum OpType.
// Primitives 0-13 align with ElemTypeKind; 16-19 are data ops;
// 32-42 are structural control-flow; 0x3F is Go fallback.
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
	opArrayBegin  uint16 = 43 // array loop start (inline data, fixed length)
	opMapStrKV    uint16 = 44 // encode one map[string]string entry (key + value)
	opMapStrStr   uint16 = 45 // map[string]string: C-native Swiss Map iteration

	// Go-only fallback (0x3F).
	opFallback uint16 = 0x3F // custom marshalers, ,string, complex structs

	// Sentinel.
	opEnd uint16 = 0xFF
)

// opTypeMask extracts the opcode from OpType, stripping flags and
// the high byte used by opSkipIfZero to encode the ZeroCheckTag.
const opTypeMask = uint16(0x00FF)

// kindToOpcode maps an ElemTypeKind to the corresponding VM opcode.
// Primitives 0-13 map 1:1. Panics if k has no single-instruction opcode.
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
		panic(fmt.Sprintf("kindToOpcode: no direct opcode for ElemTypeKind %d", k))
	}
}

// Error codes returned in ExecCtx.ErrCode.
const (
	vjOK           int32 = 0
	vjErrBufFull   int32 = 1
	vjErrStackOvfl int32 = 3
	vjErrNanInf    int32 = 5
	vjErrYield     int32 = 6 // VM yielded to Go (interface miss, fallback, map iter)
)

// Encoding flags (VjEncFlags bitmask).
const (
	vjEncEscapeHTML   uint32 = 1 << 0
	vjEncFloatExpAuto uint32 = 1 << 3 // scientific notation for |f|<1e-6 or |f|>=1e21

	// Hot resume flags for re-entering C after Go handles a fallback field.
	vjEncResume      uint32 = 1 << 7 // skip opening '{'; resume mid-struct
	vjEncResumeFirst uint32 = 1 << 8 // with RESUME: no field written yet (first=1)
)

// Bitmask constant for decoding esc_op_idx (packed by C on fallback).
// Bit 31 = first flag.
const escOpFirstBit uint32 = 0x80000000

// Yield reason values stored in ExecCtx.YieldInfo.
const (
	yieldFallback  uint32 = 1 // custom marshaler / ,string / unsupported type
	yieldIfaceMiss uint32 = 2 // interface{} cache miss — need Go compilation
	yieldMapNext   uint32 = 3 // map iteration — need Go to provide next k/v
)

// VjOpStep mirrors the C OpStep (24 bytes).
type VjOpStep struct {
	OpType   uint16
	KeyLen   uint16
	FieldOff uint32
	KeyPtr   unsafe.Pointer // points into Blueprint.KeyPool
	OperandA int32          // jump offset, elem_size, etc.
	OperandB int32          // loop body len, reserved
}

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
const maxStackDepth = 24

// VjStackFrame mirrors the C VjStackFrame (56 bytes).
// Redesigned as a unified call/loop frame:
//
//	Common fields (0-31): ret_ops, ret_pc, frame_type, ret_base, first, elem_size
//	Union (32-55):        loop iteration state (for VJ_FRAME_LOOP)
type VjStackFrame struct {
	RetOps    unsafe.Pointer //  0: parent ops array base
	RetPC     int32          //  8: return PC (index into ret_ops)
	FrameType int32          // 12: VJ_FRAME_CALL(0) or VJ_FRAME_LOOP(1)
	RetBase   unsafe.Pointer // 16: parent data base address
	First     int32          // 24: parent first-field flag
	ElemSize  int32          // 28: element size (LOOP only, 0 for CALL)
	_union    [24]byte       // 32-55: loop iteration state
}

var _ [56]byte = [unsafe.Sizeof(VjStackFrame{})]byte{}

// Frame type constants matching C VjFrameType enum.
const (
	vjFrameCall = int32(0) // VJ_FRAME_CALL
	vjFrameLoop = int32(1) // VJ_FRAME_LOOP
	vjFrameMap  = int32(2) // VJ_FRAME_MAP — C-native Swiss Map iteration
)

// --- LOOP frame helpers (read-only from Go side) ---

func (f *VjStackFrame) iterData() unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&f._union[0]))
}
func (f *VjStackFrame) iterCount() int64 {
	return *(*int64)(unsafe.Pointer(&f._union[8]))
}
func (f *VjStackFrame) iterIdx() int64 {
	return *(*int64)(unsafe.Pointer(&f._union[16]))
}

// VjExecCtx mirrors the C VjExecCtx (1448 bytes).
// Layout optimized for cache locality:
//
//	Cache line 0 (0-63):  hot VM registers (buf, ops, pc, base, flags)
//	Cache line 1 (64-95): indent state, yield metadata
//	96+:                  stack frames + debug trace
type VjExecCtx struct {
	// Cache line 0: hot VM registers
	BufCur          unsafe.Pointer //   0: current write position
	BufEnd          uintptr        //   8: one past last writable byte (NOT GC-traced)
	OpsPtr          unsafe.Pointer //  16: &Blueprint.Ops[0] (current active ops)
	PC              int32          //  24: current instruction index
	Depth           int32          //  28: stack depth (0 = top-level)
	CurBase         unsafe.Pointer //  32: current struct/elem base address
	EncFlags        uint32         //  40: VjEncFlags bitmask
	ErrCode         int32          //  44: VjError enum value
	IfaceCachePtr   unsafe.Pointer //  48: *VjIfaceCacheEntry sorted array
	IfaceCacheCount int32          //  56: number of entries
	YieldInfo       uint32         //  60: yield reason (yieldFallback, yieldIfaceMiss, etc.)

	// Cache line 1: less-hot state
	IndentTpl       unsafe.Pointer //  64: precomputed indent template
	IndentDepth     int16          //  72: logical nesting depth
	IndentStep      uint8          //  74: bytes per indent level (0 = compact)
	IndentPrefixLen uint8          //  75: bytes of prefix before indent
	_pad1           int32          //  76: alignment padding
	YieldTypePtr    unsafe.Pointer //  80: interface cache miss: eface.type_ptr
	YieldFieldIdx   int32          //  88: fallback: field index for Go to handle
	_pad2           int32          //  92: alignment padding

	// Stack + debug trace
	Stack    [maxStackDepth]VjStackFrame //  96: 24 × 56 = 1344 bytes
	TraceBuf unsafe.Pointer              // 1440: Go-allocated VjTraceBuf
}

var _ [1448]byte = [unsafe.Sizeof(VjExecCtx{})]byte{}

// VjIfaceCacheEntry maps a Go *abi.Type to its compiled Blueprint ops (24 bytes).
// Tag = opcode+1 for primitives (0 = none); C subtracts 1 before dispatch.
type VjIfaceCacheEntry struct {
	TypePtr unsafe.Pointer // *abi.Type (address-comparable)
	OpsPtr  unsafe.Pointer // &Blueprint.Ops[0], nil if not compilable by C
	Tag     uint8          // (opcode+1) for primitives, 0 for complex/yield
	_pad    [7]byte
}

var _ [24]byte = [unsafe.Sizeof(VjIfaceCacheEntry{})]byte{}

// ifaceCacheSnapshot is an immutable sorted array of cache entries.
// Once published, it is never modified — new entries produce a new snapshot.
type ifaceCacheSnapshot struct {
	entries []VjIfaceCacheEntry // sorted by TypePtr (ascending)
}

// globalIfaceCache is the process-wide interface type cache (COW snapshots).
var globalIfaceCache struct {
	current atomic.Pointer[ifaceCacheSnapshot]
	mu      sync.Mutex
}

var initPrimitiveIfaceCacheOnce sync.Once

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

// blueprintRegistry maps ops base pointer → *Blueprint for interface SWITCH_OPS.
// When the C VM switches to a cached Blueprint's ops, Go yield handlers use
// this registry to resolve the active Blueprint (for Fallbacks/KeyPool lookup).
//
// Read via atomic.Load (lock-free); write under globalIfaceCache.mu.
// Immutable after publish (COW), same lifetime as ifaceCacheSnapshot.
var blueprintRegistry atomic.Pointer[map[unsafe.Pointer]*Blueprint]

func init() {
	empty := make(map[unsafe.Pointer]*Blueprint)
	blueprintRegistry.Store(&empty)
}

// registerBlueprintOps records the mapping from &bp.Ops[0] → bp so that
// activeBlueprint can resolve the correct Blueprint after a SWITCH_OPS.
// Must be called under globalIfaceCache.mu.
func registerBlueprintOps(bp *Blueprint) {
	if bp == nil || len(bp.Ops) == 0 {
		return
	}
	key := unsafe.Pointer(&bp.Ops[0])
	cur := blueprintRegistry.Load()
	if _, ok := (*cur)[key]; ok {
		return // already registered
	}
	// COW: copy + insert
	newMap := make(map[unsafe.Pointer]*Blueprint, len(*cur)+1)
	for k, v := range *cur {
		newMap[k] = v
	}
	newMap[key] = bp
	blueprintRegistry.Store(&newMap)
}

// lookupBlueprintByOps returns the Blueprint whose Ops[0] is at opsPtr.
func lookupBlueprintByOps(opsPtr unsafe.Pointer) *Blueprint {
	m := blueprintRegistry.Load()
	return (*m)[opsPtr]
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

	// Register Blueprint in the ops→Blueprint registry BEFORE publishing
	// the cache snapshot, so that SWITCH_OPS yield handlers can always
	// resolve the active Blueprint.
	registerBlueprintOps(bp)

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
		{reflect.TypeFor[bool](), uint8(opBool) + 1},
		{reflect.TypeFor[int](), uint8(opInt) + 1},
		{reflect.TypeFor[int8](), uint8(opInt8) + 1},
		{reflect.TypeFor[int16](), uint8(opInt16) + 1},
		{reflect.TypeFor[int32](), uint8(opInt32) + 1},
		{reflect.TypeFor[int64](), uint8(opInt64) + 1},
		{reflect.TypeFor[uint](), uint8(opUint) + 1},
		{reflect.TypeFor[uint8](), uint8(opUint8) + 1},
		{reflect.TypeFor[uint16](), uint8(opUint16) + 1},
		{reflect.TypeFor[uint32](), uint8(opUint32) + 1},
		{reflect.TypeFor[uint64](), uint8(opUint64) + 1},
		{reflect.TypeFor[float32](), uint8(opFloat32) + 1},
		{reflect.TypeFor[float64](), uint8(opFloat64) + 1},
		{reflect.TypeFor[string](), uint8(opString) + 1},
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
