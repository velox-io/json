package vjson

import (
	"fmt"
	"maps"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

// OpCode constants — mirror native/encvm/impl/types.h enum OpType.
// Primitives 1–14 = ElemTypeKind; 17–20 data ops; 33–46 control-flow; 0x40 fallback.
const (
	opBool    uint16 = 1
	opInt     uint16 = 2
	opInt8    uint16 = 3
	opInt16   uint16 = 4
	opInt32   uint16 = 5
	opInt64   uint16 = 6
	opUint    uint16 = 7
	opUint8   uint16 = 8
	opUint16  uint16 = 9
	opUint32  uint16 = 10
	opUint64  uint16 = 11
	opFloat32 uint16 = 12
	opFloat64 uint16 = 13
	opString  uint16 = 14

	// Non-primitive data ops (17-20).
	opInterface  uint16 = 17 // interface{} — noinline C encoder or yield
	opRawMessage uint16 = 18 // json.RawMessage — direct byte copy
	opNumber     uint16 = 19 // json.Number — direct string copy
	opByteSlice  uint16 = 20 // []byte — base64, yield to Go

	// Structural control-flow instructions (33-46).
	opSkipIfZero uint16 = 33 // conditional forward jump (omitempty)
	opRecurse    uint16 = 35 // intra-Blueprint call: push CALL frame, jump to ops[operand_a]
	opPtrDeref   uint16 = 36 // deref pointer, nil→null+jump
	opPtrEnd     uint16 = 37 // pop ptr-deref frame, restore base
	opSliceBegin uint16 = 38 // slice loop start
	opSliceEnd   uint16 = 39 // slice loop end / back-edge
	opMapBegin   uint16 = 40 // map iteration start (yield-driven)
	opMapEnd     uint16 = 41 // map iteration end (yield)
	opObjOpen    uint16 = 42 // write key + '{', set first=1 (no frame push)
	opObjClose   uint16 = 43 // write '}', set first=0 (no frame pop)
	opArrayBegin uint16 = 44 // array loop start (inline data, fixed length)
	// 45: reserved (formerly opMapStrKV)
	opMapStrStr uint16 = 46 // map[string]string: C-native Swiss Map iteration

	// Go-only fallback (0x40).
	opFallback uint16 = 0x40 // custom marshalers, ,string, complex structs

	// Sentinel.
	opEnd uint16 = 0xFF
)

// kindToOpcode maps an ElemTypeKind to its VM opcode.
// Panics if k has no single-instruction opcode.
func kindToOpcode(k ElemTypeKind) uint16 {
	switch {
	case k <= KindString:
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

// VM exit codes returned via vmstate high bits.
// Includes both terminal statuses (OK/errors) and control-flow exits.
//
// YIELD is intentionally separate from BUF_FULL:
//   - BUF_FULL: capacity event; Go only needs to grow/flush buffer and retry.
//   - YIELD: semantic handoff; Go must run a handler based on yield_reason
//     (iface cache miss, fallback, map handoff, ...) before re-entering C,
//     if needed.
const (
	vjExitOK        int32 = 0
	vjExitBufFull   int32 = 1
	vjExitStackOvfl int32 = 3
	vjExitNanInf    int32 = 5
	vjExitYieldToGo int32 = 6 // VM yielded to Go semantic handlers
)

// Encoding flags — Go-side bit positions (low 4 bits).
// These match the VJ_FLAGS_* constants extracted by VJ_ST_GET_FLAGS().
// Bits 0-2 mirror escapeFlags (escapeHTML, escapeLineTerms, escapeInvalidUTF8).
const (
	vjEncFloatExpAuto uint32 = 1 << 3 // scientific notation for |f|<1e-6 or |f|>=1e21
)

// Yield reason values extracted from vmstate bits [40..47].
const (
	yieldFallback   uint32 = 1 // custom marshaler / ,string / unsupported type
	yieldIfaceMiss  uint32 = 2 // interface{} cache miss — need Go compilation
	yieldMapHandoff uint32 = 3 // map encoding handoff — Go takes over full map field encoding
)

// FallbackReason constants — stored in VjOpStep.OperandB for OP_FALLBACK.
// Mirrors enum FallbackReason in native/encvm/impl/types.h.
// Used by debug trace to display why a field was delegated to Go.
const (
	fbReasonUnknown       int32 = 0 // unspecified / unknown kind
	fbReasonMarshaler     int32 = 1 // implements json.Marshaler
	fbReasonTextMarshaler int32 = 2 // implements encoding.TextMarshaler
	fbReasonQuoted        int32 = 3 // field has `,string` struct tag
	fbReasonByteSlice     int32 = 4 // []byte — base64 encoding
	fbReasonByteArray     int32 = 5 // [N]byte — base64 encoding
	fbReasonMapOmitempty  int32 = 6 // map with omitempty (needs Go len check)
)

// ================================================================
//  VMState — packed 64-bit VM state register (mirrors C layout)
//
//  Layout:
//    bits [0..7]   = depth        (unified stack depth)
//    bits [8..9]   = top_frame    (VJ_FRAME_CALL/LOOP/MAP — topmost frame type)
//    bits [10..15] = reserved
//    bit  [16]     = first        (comma latch: 0 => write ',' before next item)
//    bits [17..31] = enc_flags    (encoding config: escape, float fmt)
//    bits [32..39] = exit_code    (VM exit status)
//    bits [40..47] = yield_reason (VjYieldReason, valid when exit_code=YIELD)
//    bits [48..63] = reserved
// ================================================================

const (
	vjStDepthMask     = uint64(0x000000FF)
	vjStTopFrameShift = 8
	vjStTopFrameMask  = uint64(0x00000300) // bits [8..9]
	vjStFirstBit      = uint64(1) << 16
	vjStFlagsShift    = 17
	vjStExitShift     = 32
	vjStYieldShift    = 40
)

// vmstateGetExit extracts the VM exit code from vmstate.
func vmstateGetExit(st uint64) int32 {
	return int32((st >> vjStExitShift) & 0xFF)
}

// vmstateGetYield extracts the yield reason from vmstate.
// Only meaningful when vmstateGetExit(st) == vjExitYieldToGo.
func vmstateGetYield(st uint64) uint32 {
	return uint32((st >> vjStYieldShift) & 0xFF)
}

// vmstateGetFirst extracts the first-field flag from vmstate.
func vmstateGetFirst(st uint64) bool {
	return (st & vjStFirstBit) != 0
}

// vmstateGetDepth extracts the unified stack depth from vmstate.
func vmstateGetDepth(st uint64) int32 {
	return int32(st & vjStDepthMask)
}

// vmstateGetTopFrame extracts the topmost frame type from vmstate.
// Only meaningful when vmstateGetDepth(st) > 0.
func vmstateGetTopFrame(st uint64) int32 {
	return int32((st & vjStTopFrameMask) >> vjStTopFrameShift)
}

// vmstateBuildInitial builds the initial vmstate for VM entry.
// flags contains escape flags (bits 0-2) and vjEncFloatExpAuto (bit 3).
// The first bit is set. depth=0, exit=0, yield=0.
func vmstateBuildInitial(flags uint32) uint64 {
	return vjStFirstBit | (uint64(flags) << vjStFlagsShift)
}

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
	Name      string          // type name (debug/trace only)
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

// VJ_MAX_DEPTH matches the C VJ_MAX_DEPTH.
const VJ_MAX_DEPTH = 64 //nolint

// maxIndentDepth is the combined max nesting for indent template sizing.
const maxIndentDepth = VJ_MAX_DEPTH

// VjStackFrame mirrors the C VjStackFrame (56 bytes).
// Unified frame for all stack-using ops: ptr_deref, interface/switch_ops,
// slice_begin, array_begin, map_str_str.
//
// The frame type discriminator lives in vmstate bits [8..9] and only
// records the topmost frame's type.  Instruction pairing (begin/end)
// ensures correct pop semantics without per-frame type tags.
//
// NOTE: 'first' is tracked in VMState bit 16 (set on object entry,
// test-and-clear when writing a key). Stack frames do not store/restore it.
type VjStackFrame struct {
	RetOps      unsafe.Pointer //  0: parent ops array base (CALL only)
	RetPC       int32          //  8: return PC (CALL only)
	_reservedFT int32          // 12: reserved (formerly frame_type)
	RetBase     unsafe.Pointer // 16: parent data base
	_reserved0  int32          // 24: reserved (legacy first slot)
	_pad0       int32          // 28: reserved
	_union      [24]byte       // 32-55: call/loop/map iteration state
}

var _ [56]byte = [unsafe.Sizeof(VjStackFrame{})]byte{}

// Top-frame type constants matching C VJ_FRAME_* defines.
// Stored in vmstate bits [8..9], not per-frame.
const (
	vjFrameCall = int32(0) // VJ_FRAME_CALL
	vjFrameLoop = int32(1) // VJ_FRAME_LOOP
	vjFrameMap  = int32(2) // VJ_FRAME_MAP
)

// --- LOOP iter frame helpers (read-only from Go side) ---

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
//	Cache line 0 (0-63):  hot VM registers (buf, ops, pc, base, vmstate)
//	Cache line 1 (64-95): indent state, yield metadata
//	96+:                  unified stack + debug trace
type VjExecCtx struct {
	// Cache line 0: hot VM registers
	BufCur          unsafe.Pointer //   0: current write position
	BufEnd          uintptr        //   8: one past last writable byte (NOT GC-traced)
	OpsPtr          unsafe.Pointer //  16: &Blueprint.Ops[0] (current active ops)
	PC              int32          //  24: current instruction index
	_padPC          int32          //  28: alignment padding
	CurBase         unsafe.Pointer //  32: current struct/elem base address
	VMState         uint64         //  40: packed state register (see VMState layout)
	IfaceCachePtr   unsafe.Pointer //  48: *VjIfaceCacheEntry sorted array
	IfaceCacheCount int32          //  56: number of entries
	_padIface       int32          //  60: alignment padding

	// Cache line 1: less-hot state
	IndentTpl       unsafe.Pointer //  64: precomputed indent template
	IndentDepth     int16          //  72: logical nesting depth
	IndentStep      uint8          //  74: bytes per indent level (0 = compact)
	IndentPrefixLen uint8          //  75: bytes of prefix before indent
	_pad1           int32          //  76: alignment padding
	YieldTypePtr    unsafe.Pointer //  80: interface cache miss: eface.type_ptr
	_yieldReserved  [2]int32       //  88: reserved (keep C/Go ABI layout stable)

	// Unified stack + debug trace
	Stack    [VJ_MAX_DEPTH]VjStackFrame //  96: 24 x 56 = 1344 bytes
	TraceBuf unsafe.Pointer             // 1440: Go-allocated VjTraceBuf
}

var _ [3688]byte = [unsafe.Sizeof(VjExecCtx{})]byte{}

// VjIfaceCacheEntry maps a Go *abi.Type to its compiled Blueprint ops (24 bytes).
type VjIfaceCacheEntry struct {
	TypePtr unsafe.Pointer // *abi.Type
	OpsPtr  unsafe.Pointer // &Blueprint.Ops[0], nil if not compilable by C
	Tag     uint8          // opcode for primitives (= ElemTypeKind); 0 = none
	_pad    [7]byte
}

var _ [24]byte = [unsafe.Sizeof(VjIfaceCacheEntry{})]byte{}

// ifaceCacheSnapshot is an immutable sorted array of cache entries.
// Once published, it is never modified — new entries produce a new snapshot.
type ifaceCacheSnapshot struct {
	entries []VjIfaceCacheEntry // sorted by TypePtr (ascending)
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

// globalIfaceCache is the process-wide interface type cache (COW snapshots).
var globalIfaceCache struct {
	current atomic.Pointer[ifaceCacheSnapshot]
	mu      sync.Mutex
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
	// Initialize globalIfaceCache with an empty snapshot so Load() never returns nil.
	globalIfaceCache.current.Store(&ifaceCacheSnapshot{})
	// Initialize blueprintRegistry with an empty map.
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
	maps.Copy(newMap, *cur)
	newMap[key] = bp
	blueprintRegistry.Store(&newMap)
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
		// Tag = opcode directly; tag=0 means "no tag" (all opcodes >= 1).
		{reflect.TypeFor[bool](), uint8(opBool)},
		{reflect.TypeFor[int](), uint8(opInt)},
		{reflect.TypeFor[int8](), uint8(opInt8)},
		{reflect.TypeFor[int16](), uint8(opInt16)},
		{reflect.TypeFor[int32](), uint8(opInt32)},
		{reflect.TypeFor[int64](), uint8(opInt64)},
		{reflect.TypeFor[uint](), uint8(opUint)},
		{reflect.TypeFor[uint8](), uint8(opUint8)},
		{reflect.TypeFor[uint16](), uint8(opUint16)},
		{reflect.TypeFor[uint32](), uint8(opUint32)},
		{reflect.TypeFor[uint64](), uint8(opUint64)},
		{reflect.TypeFor[float32](), uint8(opFloat32)},
		{reflect.TypeFor[float64](), uint8(opFloat64)},
		{reflect.TypeFor[string](), uint8(opString)},
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

var initPrimitiveIfaceCacheOnce sync.Once

// initMarshalerVMCtx sets up the interface cache snapshot on the pooled
// Marshaler's VjExecCtx. Called once per getMarshaler() so that execVM
// doesn't need a per-call atomic.Load + sync.Once check.
func initMarshalerVMCtx(m *Marshaler) {
	initPrimitiveIfaceCacheOnce.Do(initPrimitiveIfaceCache)
	snap := loadIfaceCacheSnapshot()
	if len(snap.entries) > 0 {
		m.vmCtx.IfaceCachePtr = unsafe.Pointer(&snap.entries[0])
		m.vmCtx.IfaceCacheCount = int32(len(snap.entries))
	} else {
		m.vmCtx.IfaceCachePtr = nil
		m.vmCtx.IfaceCacheCount = 0
	}
}

// activeBlueprint returns the Blueprint whose ops the VM is currently executing.
// Hot path (no SWITCH_OPS): single pointer compare against the root Blueprint.
// Cold path (SWITCH_OPS active): registry lookup by ctx.OpsPtr.
func activeBlueprint(ctx *VjExecCtx, rootBP *Blueprint) *Blueprint {
	if ctx.OpsPtr == unsafe.Pointer(&rootBP.Ops[0]) {
		return rootBP // hot path: still executing root Blueprint
	}

	m := blueprintRegistry.Load()
	bp := (*m)[ctx.OpsPtr]
	// Cold path: VM switched to a child Blueprint via SWITCH_OPS
	if bp != nil {
		return bp
	}
	panic("vjson: activeBlueprint: unknown ops pointer (SWITCH_OPS without registry entry)")
}
