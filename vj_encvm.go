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
// Primitives 1–14 = ElemTypeKind; 15–18 data ops; 19–31 control-flow; 32 fallback.
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

	// Non-primitive data ops (15-18).
	opInterface  uint16 = 15 // interface{} — noinline C encoder or yield
	opRawMessage uint16 = 16 // json.RawMessage — direct byte copy
	opNumber     uint16 = 17 // json.Number — direct string copy
	opByteSlice  uint16 = 18 // []byte — base64, yield to Go

	// Structural control-flow instructions (19-31).
	opSkipIfZero uint16 = 19 // conditional forward jump (omitempty)
	opCall       uint16 = 20 // subroutine call: push CALL frame, jump to ops[operand_a]
	opPtrDeref   uint16 = 21 // deref pointer, nil→null+jump
	opPtrEnd     uint16 = 22 // pop ptr-deref frame, restore base
	opSliceBegin uint16 = 23 // slice loop start
	opSliceEnd   uint16 = 24 // slice loop end / back-edge
	opMapBegin   uint16 = 25 // map iteration start (yield-driven)
	opMapEnd     uint16 = 26 // map iteration end (yield)
	opObjOpen    uint16 = 27 // write key + '{', set first=1 (no frame push)
	opObjClose   uint16 = 28 // write '}', set first=0 (no frame pop)
	opArrayBegin uint16 = 29 // array loop start (inline data, fixed length)
	opMapStrStr  uint16 = 30 // map[string]string: C-native Swiss Map iteration
	opRet        uint16 = 31 // subroutine return: pop CALL frame, restore ops/pc/base

	// Go-only fallback (32).
	opFallback uint16 = 32 // custom marshalers, ,string, complex structs

	// Keyed-field variants (33-35) — unconditional key write, no key_len branch.
	opKString uint16 = 33 // struct field string
	opKInt    uint16 = 34 // struct field int
	opKInt64  uint16 = 35 // struct field int64
)

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

// FallbackReason constants — stored in fbInfo for OP_FALLBACK.
// Mirrors enum FallbackReason in native/encvm/impl/types.h.
// Used by Go-side debug trace to display why a field was delegated to Go.
// Note: with variable-length instructions, FALLBACK is 8-byte (no operands);
// the reason is tracked via Blueprint.Fallbacks[pc].Reason instead.
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
//    bits [8..15]  = reserved
//    bit  [16]     = first        (comma latch: 0 => write ',' before next item)
//    bits [17..31] = enc_flags    (encoding config: escape, float fmt)
//    bits [32..39] = exit_code    (VM exit status)
//    bits [40..47] = yield_reason (VjYieldReason, valid when exit_code=YIELD)
//    bits [48..63] = reserved
// ================================================================

const (
	vjStDepthMask  = uint64(0x000000FF)
	vjStFirstBit   = uint64(1) << 16
	vjStFlagsShift = 17
	vjStExitShift  = 32
	vjStYieldShift = 40
)

func vmstateGetExit(st uint64) int32 {
	return int32((st >> vjStExitShift) & 0xFF)
}

// Only meaningful when exit code == vjExitYieldToGo.
func vmstateGetYield(st uint64) uint32 {
	return uint32((st >> vjStYieldShift) & 0xFF)
}

func vmstateGetFirst(st uint64) bool {
	return (st & vjStFirstBit) != 0
}

func vmstateGetDepth(st uint64) int32 {
	return int32(st & vjStDepthMask)
}

// vmstateBuildInitial builds the initial vmstate for VM entry.
// flags contains escape flags (bits 0-2) and vjEncFloatExpAuto (bit 3).
func vmstateBuildInitial(flags uint32) uint64 {
	return vjStFirstBit | (uint64(flags) << vjStFlagsShift)
}

// VjOpHdr mirrors the C VjOpHdr (8 bytes).
// This is the common header for all instructions (both short and extended).
// Keys are stored in the global key pool; KeyOff indexes into it.
type VjOpHdr struct {
	OpType   uint16 //  0: opcode | 0x8000 for extended (16-byte) instructions
	KeyLen   uint8  //  2: pre-encoded key length (max 255)
	_pad0    uint8  //  3: alignment padding
	FieldOff uint16 //  4: field offset in struct (max 65535)
	KeyOff   uint16 //  6: offset into global key pool
}

var _ [8]byte = [unsafe.Sizeof(VjOpHdr{})]byte{}

// VjOpExt holds operands for extended (16-byte) instructions.
// Immediately follows VjOpHdr in the byte stream.
type VjOpExt struct {
	OperandA int32 //  0: jump byte offset, elem_size, etc.
	OperandB int32 //  4: body byte length, ZeroCheckTag, etc.
}

var _ [8]byte = [unsafe.Sizeof(VjOpExt{})]byte{}

// opIsLong maps opcodes to their instruction width.
// Long (16-byte) opcodes have operand_a/operand_b in a VjOpExt extension.
// Short (8-byte) opcodes use only the VjOpHdr header.
// Used by Go-side trace/debug code to iterate the ops byte stream.
var opIsLong [256]bool //nolint:unused

func init() { //nolint:unused
	for _, op := range []uint16{
		opSkipIfZero, opCall, opPtrDeref,
		opSliceBegin, opSliceEnd,
		opMapBegin, opArrayBegin,
	} {
		opIsLong[op] = true
	}
}

// opSizeOf returns the instruction size in bytes (8 or 16) for the given opcode.
// Used in vjdebug build (vj_vm_trace.go); linter may report unused in normal builds.
func opSizeOf(opType uint16) int32 { //nolint:unused
	if opIsLong[opType] {
		return 16
	}
	return 8
}

// opHdrAt returns a pointer to the VjOpHdr at the given byte offset.
func opHdrAt(ops []byte, pc int32) *VjOpHdr {
	return (*VjOpHdr)(unsafe.Pointer(&ops[pc]))
}

// opExtAt returns a pointer to the VjOpExt at the given byte offset + 8.
// Only valid for long (16-byte) instructions.
func opExtAt(ops []byte, pc int32) *VjOpExt {
	return (*VjOpExt)(unsafe.Pointer(&ops[pc+8]))
}

// opSizeAt returns the size of the instruction at the given byte offset.
// Used in vjdebug build (vj_vm_trace.go); linter may report unused in normal builds.
func opSizeAt(ops []byte, pc int32) int32 { //nolint:unused
	hdr := opHdrAt(ops, pc)
	return opSizeOf(hdr.OpType)
}

// Blueprint holds the compiled instruction byte stream for a type.
// It is immutable after construction and safe for concurrent use.
// Keys are stored in the global key pool (globalKeyPool), not per-Blueprint.
type Blueprint struct {
	Name        string          // type name (debug/trace only)
	Ops         []byte          // linear instruction byte stream, terminated by opRet (8-byte aligned via alignedOps)
	Fallbacks   map[int]*fbInfo // byte offset → fallback field info (only for OP_FALLBACK instructions)
	Annotations map[int]string  // byte offset → debug annotation (type names for OBJ_OPEN/CALL); nil in non-debug builds
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

// VjStackFrame mirrors the C VjStackFrame (32 bytes).
// Unified frame for all stack-using ops: ptr_deref, interface/switch_ops,
// slice_begin, array_begin, map_str_str.
//
// Instruction pairing (begin/end) ensures correct pop semantics without
// per-frame type tags.
//
// NOTE: 'first' is tracked in VMState bit 16 (set on object entry,
// test-and-clear when writing a key). Stack frames do not store/restore it.
//
// _union layout (20 bytes) — overlapping branches:
//
//	CALL frame:  [0..7] ret_ops (*byte), [8..11] ret_pc (int32)
//	ITER frame:  [0..7] data (ptr to current elem), [8..15] count (int64), [16..19] idx (int32)
//	MAP  frame:  [0..7] data (ptr), [8..15] count (int64), [16..19] idx (int32)
//
// ret_ops/ret_pc are only used by CALL frames and live inside the call
// union branch.  Go never reads them (only C does on OP_RET).
type VjStackFrame struct {
	RetBase unsafe.Pointer //  0: parent data base (all frame types)
	_union  [20]byte       //  8-27: see union layout above
	State   int32          // 28: bit 0 = iter active; bits 24-31 = trace depth
}

var _ [32]byte = [unsafe.Sizeof(VjStackFrame{})]byte{}

// Frame types matching C VJ_FRAME_* defines (not stored in vmstate):
//   0 = VJ_FRAME_CALL:              subroutine call (recurse / ptr deref / iface switch-ops)
//   1 = VJ_FRAME_ITER:              linear iteration (slice / array)
//   2 = VJ_FRAME_ITER_STR_STR_LEAF: map[string]string iteration

// --- ITER frame field accessors (read-only from Go side) ---

// iterData returns the pointer to the current slice/array element base (_union[0..7]).
func (f *VjStackFrame) iterData() unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&f._union[0]))
}

// iterCount returns the total number of elements (_union[8..15]).
func (f *VjStackFrame) iterCount() int64 {
	return *(*int64)(unsafe.Pointer(&f._union[8]))
}

// iterIdx returns the current iteration index (_union[16..19]).
func (f *VjStackFrame) iterIdx() int64 {
	return int64(*(*int32)(unsafe.Pointer(&f._union[16])))
}

// VjExecCtx mirrors the C VjExecCtx (2152 bytes).
// Layout optimized for cache locality:
//
//	Cache line 0 (0-63):  hot VM registers (buf, ops, pc, base, vmstate)
//	Cache line 1 (64-95): indent state, yield metadata
//	96+:                  unified stack + debug trace
type VjExecCtx struct {
	// Cache line 0: hot VM registers
	BufCur          unsafe.Pointer //   0: current write position
	BufEnd          uintptr        //   8: one past last writable byte (NOT GC-traced)
	OpsPtr          unsafe.Pointer //  16: &Blueprint.Ops[0] (current active byte stream)
	PC              int32          //  24: current byte offset into ops
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
	KeyPoolBase     unsafe.Pointer //  88: global key pool base pointer

	// Unified stack + debug trace
	Stack    [VJ_MAX_DEPTH]VjStackFrame //  96: 64 x 32 = 2048 bytes
	TraceBuf unsafe.Pointer             // 2144: Go-allocated VjTraceBuf
}

var _ [2152]byte = [unsafe.Sizeof(VjExecCtx{})]byte{}

// VjIfaceCacheEntry maps a Go *abi.Type to its compiled Blueprint ops (24 bytes).
type VjIfaceCacheEntry struct {
	TypePtr unsafe.Pointer // *abi.Type
	OpsPtr  unsafe.Pointer // &Blueprint.Ops[0] (byte stream), nil if not compilable by C
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

func loadIfaceCacheSnapshot() *ifaceCacheSnapshot {
	return globalIfaceCache.current.Load()
}

// blueprintRegistry maps ops base pointer → *Blueprint for interface SWITCH_OPS.
// When the C VM switches to a cached Blueprint's ops, Go yield handlers use
// this registry to resolve the active Blueprint (for Fallbacks/KeyPool lookup).
//
// Read via atomic.Load (lock-free); write under globalIfaceCache.mu (COW).
var blueprintRegistry atomic.Pointer[map[unsafe.Pointer]*Blueprint]

func init() {
	// Initialize globalIfaceCache with an empty snapshot so Load() never returns nil.
	globalIfaceCache.current.Store(&ifaceCacheSnapshot{})
	// Initialize blueprintRegistry with an empty map.
	empty := make(map[unsafe.Pointer]*Blueprint)
	blueprintRegistry.Store(&empty)
	// Initialize globalKeyPool with an empty snapshot.
	globalKeyPool.current.Store(&keyPoolSnapshot{
		idx: make(map[string]keyPoolEntry),
	})
}

// ================================================================
//  Global Key Pool — shared across all Blueprints
//
//  All pre-encoded JSON keys ("field_name":) are stored in a single
//  contiguous byte pool. VjOpHdr.KeyOff indexes into this pool.
//  Append-only + COW: offsets are stable forever once assigned.
//  Deduplication: identical key bytes are stored only once.
// ================================================================

// globalKeyPool is the process-wide shared key name pool.
var globalKeyPool struct {
	current atomic.Pointer[keyPoolSnapshot]
	mu      sync.Mutex // guards writes (append + publish)
}

// keyPoolSnapshot is an immutable snapshot of the global key pool.
// Once published, it is never modified — new keys produce a new snapshot.
type keyPoolSnapshot struct {
	data []byte                  // contiguous key bytes
	idx  map[string]keyPoolEntry // dedup index: key_bytes → (offset, len)
}

// keyPoolEntry records the position of a key in the pool.
type keyPoolEntry struct {
	off uint16
	len uint8
}

// globalKeyPoolInsert adds key bytes to the global pool (with deduplication).
// Returns the pool offset and length. Thread-safe via COW + mutex.
// Panics if the pool would exceed 65535 bytes (virtually impossible with dedup).
func globalKeyPoolInsert(keyBytes []byte) (off uint16, klen uint8) {
	if len(keyBytes) == 0 {
		return 0, 0
	}
	if len(keyBytes) > 255 {
		panic("vjson: key too long for uint8 key_len (>255 bytes)")
	}

	key := string(keyBytes)

	// Fast path: check existing snapshot (lock-free).
	snap := globalKeyPool.current.Load()
	if snap != nil {
		if entry, ok := snap.idx[key]; ok {
			return entry.off, entry.len
		}
	}

	// Slow path: acquire lock, double-check, append.
	globalKeyPool.mu.Lock()
	defer globalKeyPool.mu.Unlock()

	snap = globalKeyPool.current.Load()
	if entry, ok := snap.idx[key]; ok {
		return entry.off, entry.len
	}

	// Validate pool capacity.
	newOff := len(snap.data)
	if newOff+len(keyBytes) > 65535 {
		panic("vjson: global key pool overflow (>65535 bytes)")
	}

	// COW: copy data + extend.
	newData := make([]byte, newOff+len(keyBytes))
	copy(newData, snap.data)
	copy(newData[newOff:], keyBytes)

	newIdx := make(map[string]keyPoolEntry, len(snap.idx)+1)
	for k, v := range snap.idx {
		newIdx[k] = v
	}
	entry := keyPoolEntry{off: uint16(newOff), len: uint8(len(keyBytes))}
	newIdx[key] = entry

	globalKeyPool.current.Store(&keyPoolSnapshot{data: newData, idx: newIdx})
	return entry.off, entry.len
}

func loadKeyPoolSnapshot() *keyPoolSnapshot {
	return globalKeyPool.current.Load()
}

// keyPoolBytes returns the key bytes for a given offset and length from the
// current global key pool snapshot. Used by Go-side yield handlers.
func keyPoolBytes(off uint16, klen uint8) []byte {
	snap := globalKeyPool.current.Load()
	return snap.data[off : uint16(off)+uint16(klen)]
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
// Called once via initPrimitiveIfaceCacheOnce at the first VM execution.
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
