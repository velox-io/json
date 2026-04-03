package venc

import (
	"fmt"
	"maps"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// Opcodes must stay in sync with native/encvm/impl/types.h.
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

	opInterface  uint16 = 15 // interface{} dispatch
	opRawMessage uint16 = 16 // raw byte copy
	opNumber     uint16 = 17 // json.Number copy
	opByteSlice  uint16 = 18 // []byte base64

	opSkipIfZero uint16 = 19 // omitempty jump
	opCall       uint16 = 20 // subroutine call
	opPtrDeref   uint16 = 21 // nil -> null, else deref
	opPtrEnd     uint16 = 22 // restore parent base
	opSliceBegin uint16 = 23 // slice loop start
	opSliceEnd   uint16 = 24 // slice loop back-edge
	opMap        uint16 = 25 // yield whole map to Go
	_opMapEnd    uint16 = 26 // reserved
	opObjOpen    uint16 = 27 // write key + '{'
	opObjClose   uint16 = 28 // write '}'
	opArrayBegin uint16 = 29 // fixed array loop start
	opMapStrStr  uint16 = 30 // native map[string]string
	opRet        uint16 = 31 // subroutine return

	opFallback uint16 = 32 // Go fallback

	opKString uint16 = 33
	opKInt    uint16 = 34
	opKInt64  uint16 = 35

	opMapStrInt   uint16 = 36 // native map[string]int
	opMapStrInt64 uint16 = 37 // native map[string]int64

	opSeqFloat64 uint16 = 38 // native []/[N]float64
	opSeqInt     uint16 = 39 // native []/[N]int
	opSeqInt64   uint16 = 40 // native []/[N]int64
	opSeqString  uint16 = 41 // native []/[N]string

	opMapStrIter    uint16 = 42 // native map[string]V key iterator
	opMapStrIterEnd uint16 = 43 // iterator back-edge

	opKQInt   uint16 = 44 // int with ,string
	opKQInt64 uint16 = 45 // int64 with ,string

	opTime uint16 = 46 // native time.Time
)

func kindToOpcode(k typ.ElemTypeKind) uint16 {
	switch {
	case k <= typ.KindString:
		return uint16(k)
	case k == typ.KindAny:
		return opInterface
	case k == typ.KindRawMessage:
		return opRawMessage
	case k == typ.KindNumber:
		return opNumber
	default:
		panic(fmt.Sprintf("kindToOpcode: no direct opcode for typ.ElemTypeKind %d", k)) // internal bug: callers guard with kind checks
	}
}

// VM exits live in VMState bits [32..39]. YIELD means Go must inspect vmstateGetYield.
const (
	vjExitOK        int32 = 0
	vjExitBufFull   int32 = 1
	vjExitStackOvfl int32 = 3
	vjExitNanInf    int32 = 5
	vjExitYieldToGo int32 = 6 // VM yielded to Go semantic handlers
)

// Yield reasons live in VMState bits [40..47].
const (
	yieldFallback   uint32 = 1 // custom marshaler / ,string / unsupported type
	yieldIfaceMiss  uint32 = 2 // interface{} cache miss — need Go compilation
	yieldMapHandoff uint32 = 3 // map encoding handoff — Go takes over full map field encoding
)

// fbInfo.Reason values; keep them in sync with native/encvm/impl/types.h.
const (
	fbReasonUnknown       int32 = 0 // unspecified / unknown kind
	fbReasonMarshaler     int32 = 1 // implements json.Marshaler
	fbReasonTextMarshaler int32 = 2 // implements encoding.TextMarshaler
	fbReasonQuoted        int32 = 3 // field has `,string` struct tag
	fbReasonByteSlice     int32 = 4 // []byte — base64 encoding
	fbReasonByteArray     int32 = 5 // [N]byte — base64 encoding
	fbReasonMapOmitempty  int32 = 6 // map with omitempty (needs Go len check)
	fbReasonKeyPoolFull   int32 = 7 // global key pool exhausted (>64KB); key read from fbInfo.TI.Ext.KeyBytes
)

// VMState mirrors the native packed state: depth in [0..7], first in bit 16, flags in [17..31], exit in [32..39], yield in [40..47].
const (
	vjStStackDepthMask = uint64(0x000000FF)
	vjStFirstBit       = uint64(1) << 16
	vjStFlagsShift     = 17
	vjStExitShift      = 32
	vjStYieldShift     = 40
)

func vmstateGetExit(st uint64) int32 {
	return int32((st >> vjStExitShift) & 0xFF)
}

func vmstateGetYield(st uint64) uint32 {
	return uint32((st >> vjStYieldShift) & 0xFF)
}

func vmstateGetFirst(st uint64) bool {
	return (st & vjStFirstBit) != 0
}

func vmstateGetStackDepth(st uint64) int32 {
	return int32(st & vjStStackDepthMask)
}

// vmstateBuildInitial sets first=1 and copies the encode flags.
func vmstateBuildInitial(flags uint32) uint64 {
	return vjStFirstBit | (uint64(flags) << vjStFlagsShift)
}

// VjOpHdr matches the native 8-byte instruction header. KeyOff indexes the global key pool.
type VjOpHdr struct {
	OpType   uint16 //  0: opcode | 0x8000 for extended (16-byte) instructions
	KeyLen   uint8  //  2: pre-encoded key length (max 255)
	_pad0    uint8  //  3: alignment padding
	FieldOff uint16 //  4: field offset in struct (max 65535)
	KeyOff   uint16 //  6: offset into global key pool
}

var _ [8]byte = [unsafe.Sizeof(VjOpHdr{})]byte{}

// VjOpExt is the 8-byte suffix for 16-byte instructions.
type VjOpExt struct {
	OperandA int32 //  0: jump byte offset, elem_size, etc.
	OperandB int32 //  4: body byte length, ZeroCheckTag, etc.
}

var _ [8]byte = [unsafe.Sizeof(VjOpExt{})]byte{}

// Blueprint is an immutable compiled VM program. Keys live in the shared key pool.
type Blueprint struct {
	Name        string          // debug/trace only
	Ops         []byte          // linear instruction byte stream, terminated by opRet (8-byte aligned via alignedOps)
	Fallbacks   map[int]*fbInfo // byte offset → fallback field info (only for OP_FALLBACK instructions)
	Annotations map[int]string  // byte offset → debug annotation (type names for OBJ_OPEN/CALL); nil in non-debug builds
}

// fbInfo records the Go fallback attached to one OP_FALLBACK pc.
type fbInfo struct {
	TI     *EncTypeInfo // field's EncTypeInfo (for EncodeFn dispatch)
	Offset uintptr      // field offset within struct
	Reason int32        // fallback reason code for diagnostics/debug metadata
}

// opIsLong marks the 16-byte instruction forms for trace/debug helpers.
var opIsLong [256]bool //nolint:unused

func init() { //nolint:unused
	for _, op := range []uint16{
		opSkipIfZero, opCall, opPtrDeref,
		opSliceBegin, opSliceEnd,
		opArrayBegin,
		opSeqFloat64, opSeqInt, opSeqInt64, opSeqString,
		opMapStrIter, opMapStrIterEnd,
	} {
		opIsLong[op] = true
	}
}

// opSizeOf reports the encoded size for trace/debug helpers.
func opSizeOf(opType uint16) int32 { //nolint:unused
	if opIsLong[opType] {
		return 16
	}
	return 8
}

func opHdrAt(ops []byte, pc int32) *VjOpHdr {
	return (*VjOpHdr)(unsafe.Pointer(&ops[pc]))
}

func opExtAt(ops []byte, pc int32) *VjOpExt {
	return (*VjOpExt)(unsafe.Pointer(&ops[pc+8]))
}

// opSizeAt reports the size of the instruction at pc.
func opSizeAt(ops []byte, pc int32) int32 { //nolint:unused
	hdr := opHdrAt(ops, pc)
	return opSizeOf(hdr.OpType)
}

// VjStackFrame matches the native 32-byte frame ABI. `first` stays in VMState.
// _union stores CALL {ret_ops, ret_pc} or ITER/MAP {data, count, idx}.
type VjStackFrame struct {
	RetBase unsafe.Pointer // 0: parent base
	_union  [20]byte       // 8: native union payload
	State   int32          // 28: iter-active bit + trace depth
}

var _ [32]byte = [unsafe.Sizeof(VjStackFrame{})]byte{}

func (f *VjStackFrame) iterData() unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&f._union[0]))
}

func (f *VjStackFrame) iterCount() int64 {
	return *(*int64)(unsafe.Pointer(&f._union[8]))
}

func (f *VjStackFrame) iterIdx() int64 {
	return int64(*(*int32)(unsafe.Pointer(&f._union[16])))
}

// Must match native VJ_MAX_STACK_DEPTH.
const VJ_MAX_STACK_DEPTH = 64 //nolint:revive

const maxIndentDepth = VJ_MAX_STACK_DEPTH

// VjExecCtx matches the native 2152-byte exec ABI. Field order and offsets are fixed.
type VjExecCtx struct {
	// Hot registers.
	// BufCur is uintptr (not unsafe.Pointer) because the VM may advance it to
	// one-past-end (BufCur == BufEnd), which is not a valid GC pointer.
	BufCur          uintptr        //   0: current write position (NOT GC-traced; may be one-past-end)
	BufEnd          uintptr        //   8: one past last writable byte (NOT GC-traced)
	OpsPtr          unsafe.Pointer //  16: &Blueprint.Ops[0] (current active byte stream)
	PC              int32          //  24: current byte offset into ops
	_padPC          int32          //  28: alignment padding
	CurBase         unsafe.Pointer //  32: current struct/elem base address
	VMState         uint64         //  40: packed state register (see VMState layout)
	IfaceCachePtr   unsafe.Pointer //  48: *VjIfaceCacheEntry sorted array
	IfaceCacheCount int32          //  56: number of entries
	_padIface       int32          //  60: alignment padding

	// Less-hot indent and yield state.
	IndentTpl       unsafe.Pointer //  64: precomputed indent template
	IndentDepth     int16          //  72: logical nesting depth
	IndentStep      uint8          //  74: bytes per indent level (0 = compact)
	IndentPrefixLen uint8          //  75: bytes of prefix before indent
	TraceDepth      int32          //  76: trace indent depth (debug only, else padding)
	YieldTypePtr    unsafe.Pointer //  80: interface cache miss: eface.type_ptr
	KeyPoolBase     unsafe.Pointer //  88: global key pool base pointer

	// Unified stack and optional trace buffer.
	Stack    [VJ_MAX_STACK_DEPTH]VjStackFrame //  96: 64 x 32 = 2048 bytes
	TraceBuf unsafe.Pointer                   // 2144: Go-allocated VjTraceBuf
}

var _ [2152]byte = [unsafe.Sizeof(VjExecCtx{})]byte{}

// VjIfaceCacheEntry maps a Go type to a primitive tag or compiled ops.
type VjIfaceCacheEntry struct {
	TypePtr unsafe.Pointer // *abi.Type
	OpsPtr  unsafe.Pointer // &Blueprint.Ops[0] (byte stream), nil if not compilable by C
	Tag     uint8          // opcode for primitives (= typ.ElemTypeKind); 0 = none
	Flags   uint8          // VJ_IFACE_FLAG_* bits (e.g. INDIRECT for maps)
	_pad    [6]byte
}

var _ [24]byte = [unsafe.Sizeof(VjIfaceCacheEntry{})]byte{}

// ifaceCacheSnapshot is an immutable sorted cache snapshot.
type ifaceCacheSnapshot struct {
	entries []VjIfaceCacheEntry // sorted by TypePtr (ascending)
}

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

var globalIfaceCache struct {
	current atomic.Pointer[ifaceCacheSnapshot]
	mu      sync.Mutex
}

func loadIfaceCacheSnapshot() *ifaceCacheSnapshot {
	return globalIfaceCache.current.Load()
}

// blueprintRegistry resolves ctx.OpsPtr back to the active Blueprint after SWITCH_OPS.
var blueprintRegistry atomic.Pointer[map[unsafe.Pointer]*Blueprint]

func init() {
	globalIfaceCache.current.Store(&ifaceCacheSnapshot{})
	empty := make(map[unsafe.Pointer]*Blueprint)
	blueprintRegistry.Store(&empty)
	globalKeyPool.current.Store(&keyPoolSnapshot{
		idx: make(map[string]keyPoolEntry),
	})
}

// globalKeyPool stores all pre-encoded JSON keys in one append-only COW pool.
var globalKeyPool struct {
	current atomic.Pointer[keyPoolSnapshot]
	mu      sync.Mutex // guards writes (append + publish)
}

// keyPoolSnapshot is an immutable key-pool snapshot.
type keyPoolSnapshot struct {
	data []byte                  // contiguous key bytes
	idx  map[string]keyPoolEntry // dedup index: key_bytes → (offset, len)
}

type keyPoolEntry struct {
	off uint16
	len uint8
}

// globalKeyPoolInsert deduplicates key bytes and appends them to the shared pool.
// ok=false means only a new key overflowed the 64KB address space; dedup hits still succeed.
func globalKeyPoolInsert(keyBytes []byte) (off uint16, klen uint8, ok bool) {
	if len(keyBytes) == 0 {
		return 0, 0, true
	}
	if len(keyBytes) > 255 {
		panic("venc: key too long for uint8 key_len (>255 bytes)") // internal bug: compiler falls back before reaching here
	}

	key := string(keyBytes)

	snap := globalKeyPool.current.Load()
	if snap != nil {
		if entry, found := snap.idx[key]; found {
			return entry.off, entry.len, true
		}
	}

	globalKeyPool.mu.Lock()
	defer globalKeyPool.mu.Unlock()

	snap = globalKeyPool.current.Load()
	if entry, found := snap.idx[key]; found {
		return entry.off, entry.len, true
	}

	newOff := len(snap.data)
	if newOff+len(keyBytes) > 65535 {
		return 0, 0, false // pool full: caller should emit Go fallback for this field
	}

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
	return entry.off, entry.len, true
}

func loadKeyPoolSnapshot() *keyPoolSnapshot {
	return globalKeyPool.current.Load()
}

// keyPoolBytes reads key bytes from the current pool snapshot.
func keyPoolBytes(off uint16, klen uint8) []byte {
	snap := globalKeyPool.current.Load()
	return snap.data[off : uint16(off)+uint16(klen)]
}

// registerBlueprintOps publishes the ops pointer -> Blueprint mapping for SWITCH_OPS.
func registerBlueprintOps(bp *Blueprint) {
	if bp == nil || len(bp.Ops) == 0 {
		return
	}
	key := unsafe.Pointer(&bp.Ops[0])
	cur := blueprintRegistry.Load()
	if _, ok := (*cur)[key]; ok {
		return // already registered
	}
	newMap := make(map[unsafe.Pointer]*Blueprint, len(*cur)+1)
	maps.Copy(newMap, *cur)
	newMap[key] = bp
	blueprintRegistry.Store(&newMap)
}

// Must match native VJ_IFACE_FLAG_INDIRECT.
const ifaceFlagIndirect uint8 = 0x01

// insertIfaceCache publishes one more interface cache entry via COW.
func insertIfaceCache(typePtr unsafe.Pointer, bp *Blueprint, tag uint8, flags uint8) {
	globalIfaceCache.mu.Lock()
	defer globalIfaceCache.mu.Unlock()

	cur := globalIfaceCache.current.Load()
	if cur.lookup(typePtr) != nil {
		return
	}

	entry := VjIfaceCacheEntry{
		TypePtr: typePtr,
		Tag:     tag,
		Flags:   flags,
	}
	if bp != nil && len(bp.Ops) > 0 {
		entry.OpsPtr = unsafe.Pointer(&bp.Ops[0])
	}

	newEntries := make([]VjIfaceCacheEntry, len(cur.entries)+1)
	copy(newEntries, cur.entries)
	newEntries[len(cur.entries)] = entry
	sort.Slice(newEntries, func(i, j int) bool {
		return uintptr(newEntries[i].TypePtr) < uintptr(newEntries[j].TypePtr)
	})

	// Register ops before publishing so SWITCH_OPS can always resolve the active Blueprint.
	registerBlueprintOps(bp)

	globalIfaceCache.current.Store(&ifaceCacheSnapshot{entries: newEntries})
}

// initPrimitiveIfaceCache seeds primitive entries and pre-warms common composite interface types.
func initPrimitiveIfaceCache() {
	primitives := []struct {
		t   reflect.Type
		tag uint8
	}{
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

	table := make([]VjIfaceCacheEntry, 0, len(primitives)+8)
	for _, e := range primitives {
		table = append(table, VjIfaceCacheEntry{
			TypePtr: rtypePtr(e.t),
			Tag:     e.tag,
		})
	}

	// Pre-warm the common composite types that show up in interface{} payloads.
	compositeSlices := []reflect.Type{
		reflect.TypeFor[[]any](),
		reflect.TypeFor[[]string](),
		reflect.TypeFor[[]float64](),
		reflect.TypeFor[[]int](),
		reflect.TypeFor[[]int64](),
	}
	for _, t := range compositeSlices {
		ti := EncTypeInfoOf(t)
		sliceInfo := ti.ResolveSlice()
		bp := compileStandaloneSliceBlueprint(sliceInfo)
		entry := VjIfaceCacheEntry{TypePtr: rtypePtr(t)}
		if bp != nil && len(bp.Ops) > 0 {
			entry.OpsPtr = unsafe.Pointer(&bp.Ops[0])
			registerBlueprintOps(bp)
		}
		table = append(table, entry)
	}

	compositeMaps := []reflect.Type{
		reflect.TypeFor[map[string]any](),
		reflect.TypeFor[map[string]string](),
	}
	for _, t := range compositeMaps {
		ti := EncTypeInfoOf(t)
		mapInfo := ti.ResolveMap()
		bp := compileStandaloneMapBlueprint(mapInfo)
		entry := VjIfaceCacheEntry{
			TypePtr: rtypePtr(t),
			Flags:   ifaceFlagIndirect, // map is reference type
		}
		if bp != nil && len(bp.Ops) > 0 {
			entry.OpsPtr = unsafe.Pointer(&bp.Ops[0])
			registerBlueprintOps(bp)
		}
		table = append(table, entry)
	}

	sort.Slice(table, func(i, j int) bool {
		return uintptr(table[i].TypePtr) < uintptr(table[j].TypePtr)
	})
	globalIfaceCache.current.Store(&ifaceCacheSnapshot{entries: table})
}

var initPrimitiveIfaceCacheOnce sync.Once

// activeBlueprint resolves the Blueprint currently backing ctx.OpsPtr.
func activeBlueprint(ctx *VjExecCtx, rootBP *Blueprint) *Blueprint {
	if ctx.OpsPtr == unsafe.Pointer(&rootBP.Ops[0]) {
		return rootBP
	}

	m := blueprintRegistry.Load()
	bp := (*m)[ctx.OpsPtr]
	if bp != nil {
		return bp
	}
	panic("venc: activeBlueprint: unknown ops pointer (SWITCH_OPS without registry entry)")
}

func (si *EncStructInfo) getBlueprint() *Blueprint {
	cache := si.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileBlueprint(si)
	})
	return cache.blueprint
}

func (si *EncSliceInfo) getBlueprint() *Blueprint {
	cache := si.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileStandaloneSliceBlueprint(si)
	})
	return cache.blueprint
}

func (ai *EncArrayInfo) getBlueprint() *Blueprint {
	cache := ai.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileStandaloneArrayBlueprint(ai)
	})
	return cache.blueprint
}

func (mi *EncMapInfo) getBlueprint() *Blueprint {
	cache := mi.vmCache()
	cache.once.Do(func() {
		cache.blueprint = compileStandaloneMapBlueprint(mi)
	})
	return cache.blueprint
}
