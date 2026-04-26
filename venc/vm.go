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

	opInterface  uint16 = 15
	opRawMessage uint16 = 16
	opNumber     uint16 = 17
	opByteSlice  uint16 = 18

	opSkipIfZero uint16 = 19
	opCall       uint16 = 20
	opPtrDeref   uint16 = 21
	opPtrEnd     uint16 = 22
	opSliceBegin uint16 = 23
	opSliceEnd   uint16 = 24
	opMap        uint16 = 25
	opObjOpen    uint16 = 27
	opObjClose   uint16 = 28
	opArrayBegin uint16 = 29
	opMapStrStr  uint16 = 30
	opRet        uint16 = 31

	opFallback uint16 = 32

	opKString uint16 = 33
	opKInt    uint16 = 34
	opKInt64  uint16 = 35

	opMapStrInt   uint16 = 36
	opMapStrInt64 uint16 = 37

	opSeqFloat64 uint16 = 38
	opSeqInt     uint16 = 39
	opSeqInt64   uint16 = 40
	opSeqString  uint16 = 41

	opMapStrIter    uint16 = 42
	opMapStrIterEnd uint16 = 43

	opKQInt   uint16 = 44
	opKQInt64 uint16 = 45

	opTime uint16 = 46
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

const (
	vjExitOK        int32 = 0
	vjExitBufFull   int32 = 1
	vjExitStackOvfl int32 = 3
	vjExitNanInf    int32 = 5
	vjExitYieldToGo int32 = 6
)

const (
	yieldFallback   uint32 = 1
	yieldIfaceMiss  uint32 = 2
	yieldMapHandoff uint32 = 3
)

// fbInfo.Reason values — Go-side only diagnostic codes for trace output.
// Describes why the compiler yielded a field to Go-side encoding.
const (
	fbReasonUnknown       int32 = iota // catch-all for unrecognized kinds
	fbReasonMarshaler                  // implements json.Marshaler
	fbReasonTextMarshaler              // implements encoding.TextMarshaler
	fbReasonQuoted                     // field has `,string` struct tag
	fbReasonByteArray                  // [N]byte — base64 encoding
	fbReasonIface                      // non-empty interface
	fbReasonOverflow                   // field offset or key exceeds native encoding limits
)

const (
	vjStStackDepthMask = uint64(0x000000FF)
	vjStFirstBit       = uint64(1) << 16
	vjStFlagsShift     = 17
	vjStExitShift      = 32
	vjStYieldShift     = 40
)

// Must match native VJ_IFACE_FLAG_INDIRECT.
const ifaceFlagIndirect uint8 = 0x01

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

type VjOpHdr struct {
	OpType   uint16
	KeyLen   uint8
	_pad0    uint8
	FieldOff uint16
	KeyOff   uint16
}

var _ [8]byte = [unsafe.Sizeof(VjOpHdr{})]byte{}

type VjOpExt struct {
	OperandA int32
	OperandB int32
}

var _ [8]byte = [unsafe.Sizeof(VjOpExt{})]byte{}

type Blueprint struct {
	Name        string
	Ops         []byte
	Fallbacks   map[int]*fbInfo
	Annotations map[int]string
}

// fbInfo records the Go fallback attached to one OP_FALLBACK pc.
type fbInfo struct {
	TI       *EncTypeInfo                  // type descriptor (for encodeTop dispatch)
	Offset   uintptr                       // field offset within struct
	Reason   int32                         // fallback reason code for diagnostics/debug metadata
	TagFlags typ.TagFlag                   // field-level tag flags (omitempty, quoted)
	KeyBytes []byte                        // precomputed `"name":` bytes
	IsZeroFn func(ptr unsafe.Pointer) bool // omitempty zero check
}

func opHdrAt(ops []byte, pc int32) *VjOpHdr {
	return (*VjOpHdr)(unsafe.Pointer(&ops[pc]))
}

func opExtAt(ops []byte, pc int32) *VjOpExt {
	return (*VjOpExt)(unsafe.Pointer(&ops[pc+8]))
}

type VjStackFrame struct {
	RetBase unsafe.Pointer // 0: parent base
	Payload [20]byte       // 8: native union payload
	State   int32          // 28: iter-active bit + trace depth
}

var _ [32]byte = [unsafe.Sizeof(VjStackFrame{})]byte{}

func (f *VjStackFrame) iterData() unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&f.Payload[0]))
}

func (f *VjStackFrame) iterCount() int64 {
	return *(*int64)(unsafe.Pointer(&f.Payload[8]))
}

func (f *VjStackFrame) iterIdx() int64 {
	return int64(*(*int32)(unsafe.Pointer(&f.Payload[16])))
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

type VjIfaceCacheEntry struct {
	TypePtr unsafe.Pointer
	OpsPtr  unsafe.Pointer
	Tag     uint8
	Flags   uint8
	_pad    [6]byte
}

var _ [24]byte = [unsafe.Sizeof(VjIfaceCacheEntry{})]byte{}

type ifaceCacheSnapshot struct {
	entries []VjIfaceCacheEntry
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

var blueprintRegistry atomic.Pointer[map[unsafe.Pointer]*Blueprint]
var initPrimitiveIfaceCacheOnce sync.Once

func init() {
	globalIfaceCache.current.Store(&ifaceCacheSnapshot{})
	empty := make(map[unsafe.Pointer]*Blueprint)
	blueprintRegistry.Store(&empty)
	globalKeyPool.current.Store(&keyPoolSnapshot{
		idx: make(map[string]keyPoolEntry),
	})
	initPrimitiveIfaceCacheOnce.Do(initPrimitiveIfaceCache)
}

var globalKeyPool struct {
	current atomic.Pointer[keyPoolSnapshot]
	mu      sync.Mutex // guards writes (append + publish)
}

type keyPoolSnapshot struct {
	data []byte
	idx  map[string]keyPoolEntry
}

type keyPoolEntry struct {
	off uint16
	len uint8
}

func globalKeyPoolInsert(keyBytes []byte) (off uint16, klen uint8, ok bool) {
	if len(keyBytes) == 0 {
		return 0, 0, true
	}
	if len(keyBytes) > 255 {
		panic("venc: key too long for uint8 key_len (>255 bytes)")
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
	maps.Copy(newIdx, snap.idx)
	entry := keyPoolEntry{off: uint16(newOff), len: uint8(len(keyBytes))}
	newIdx[key] = entry

	globalKeyPool.current.Store(&keyPoolSnapshot{data: newData, idx: newIdx})
	return entry.off, entry.len, true
}

func loadKeyPoolSnapshot() *keyPoolSnapshot {
	return globalKeyPool.current.Load()
}

func keyPoolBytes(off uint16, klen uint8) []byte {
	snap := globalKeyPool.current.Load()
	return snap.data[off : uint16(off)+uint16(klen)]
}

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
		bp := ti.getBlueprint()
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
		bp := ti.getBlueprint()
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
