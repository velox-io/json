package venc

import (
	"fmt"
	"maps"
	"math/bits"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/typ"
	"github.com/velox-io/json/value"
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

	opValue uint16 = 47

	opValueSpread uint16 = 48

	opUnfold uint16 = 49
)

func kindToOpcode(k typ.ElemTypeKind) uint16 {
	switch {
	case k <= typ.KindString:
		return uint16(k)
	case k == typ.KindAny:
		return opInterface
	case k == typ.KindRawMessage:
		return opRawMessage
	case k == typ.KindValue:
		return opValue
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

// fbInfo.Reason values: Go-side only diagnostic codes for trace output.
// Describes why the compiler yielded a field to Go-side encoding.
const (
	fbReasonUnknown       int32 = iota // catch-all for unrecognized kinds
	fbReasonMarshaler                  // implements json.Marshaler
	fbReasonTextMarshaler              // implements encoding.TextMarshaler
	fbReasonQuoted                     // field has `,string` struct tag
	fbReasonByteArray                  // [N]byte, base64 encoding
	fbReasonIface                      // non-empty interface
	fbReasonOverflow                   // field offset or key exceeds native encoding limits
	fbReasonViaPtr                     // promoted across an embedded pointer; needs a hop walk
	fbReasonValue                      // value.Value deeper than the walk's native bounds
	fbReasonSpread                     // reserve-unknown spread beyond native bounds or via pointer hops
	fbReasonUnfold                     // inline variant unfold via pointer hops or over the offset limit
	fbReasonStream                     // stream.Stream[T] producer activation (lazy member prefix)
)

// opFlagIfaceField mirrors native VJ_OP_FLAG_IFACE_FIELD: the unfold field's
// word 0 is an itab rather than an rtype.
const opFlagIfaceField uint8 = 0x01

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
	Flags    uint8 // VJ_OP_FLAG_* bits (native mirror)
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

	// PtrPath is non-empty for a field promoted across an embedded pointer.
	// Offset is then relative to the base the hops reach rather than to the
	// struct, so the hops must be walked before it is applied.
	PtrPath []typ.PtrHop
}

// resolveFieldBase walks a promoted field's embedded-pointer hops and reports
// false if any hop is nil, meaning the field has no storage and is omitted.
// encoding/json behaves the same way: there is nothing to read.
//
// Unlike the decode side this never allocates. Encoding must not mutate the
// value it is encoding.
func resolveFieldBase(base unsafe.Pointer, path []typ.PtrHop) (unsafe.Pointer, bool) {
	for i := range path {
		p := *(*unsafe.Pointer)(unsafe.Add(base, path[i].SlotOffset))
		if p == nil {
			return nil, false
		}
		base = p
	}
	return base, true
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
	BufCur         uintptr        //   0: current write position (NOT GC-traced; may be one-past-end)
	BufEnd         uintptr        //   8: one past last writable byte (NOT GC-traced)
	OpsPtr         unsafe.Pointer //  16: &Blueprint.Ops[0] (current active byte stream)
	PC             int32          //  24: current byte offset into ops
	_padPC         int32          //  28: alignment padding
	CurBase        unsafe.Pointer //  32: current struct/elem base address
	VMState        uint64         //  40: packed state register (see VMState layout)
	IfaceHashSlots unsafe.Pointer //  48: *VjIfaceCacheEntry open-addressed slot array
	IfaceHashShift int32          //  56: slot index = hash >> shift
	_padIface      int32          //  60: alignment padding

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
	TypePtr unsafe.Pointer //  0
	OpsPtr  unsafe.Pointer //  8
	// BodyOpsPtr addresses a body-only Blueprint (struct fields without the
	// OBJ_OPEN/OBJ_CLOSE pair) for OP_UNFOLD dispatch; nil when not compiled.
	BodyOpsPtr unsafe.Pointer // 16
	Tag        uint8          // 24
	Flags      uint8          // 25
	_pad       [6]byte        // 26
}

var _ [32]byte = [unsafe.Sizeof(VjIfaceCacheEntry{})]byte{}
var _ [0]byte = [unsafe.Offsetof(VjIfaceCacheEntry{}.BodyOpsPtr) - 16]byte{}

type ifaceCacheSnapshot struct {
	slots []VjIfaceCacheEntry
	shift uint8 // slot index = hash >> shift; capacity = 1 << (64-shift)
	count int
}

// ifaceHashIdx matches the native vj_iface_cache_lookup hash: the high
// bits of a multiply-shift hash over the type pointer (>=8-byte aligned).
func ifaceHashIdx(typePtr unsafe.Pointer, shift uint8) int {
	const mult = 0x9E3779B97F4A7C15
	h := (uint64(uintptr(typePtr)) >> 3) * mult
	return int(h >> shift)
}

// find returns the slot index of typePtr, or -1 on miss.
func (s *ifaceCacheSnapshot) find(typePtr unsafe.Pointer) int {
	if len(s.slots) == 0 {
		return -1
	}
	idx := ifaceHashIdx(typePtr, s.shift)
	for {
		e := &s.slots[idx]
		if e.TypePtr == typePtr {
			return idx
		}
		if e.TypePtr == nil {
			return -1
		}
		if idx++; idx == len(s.slots) {
			idx = 0
		}
	}
}

func (s *ifaceCacheSnapshot) lookup(typePtr unsafe.Pointer) *VjIfaceCacheEntry {
	if idx := s.find(typePtr); idx >= 0 {
		return &s.slots[idx]
	}
	return nil
}

// place inserts entry into the first empty slot of its probe run. The
// caller guarantees the type is absent and the table is not full.
func (s *ifaceCacheSnapshot) place(entry VjIfaceCacheEntry) {
	idx := ifaceHashIdx(entry.TypePtr, s.shift)
	for s.slots[idx].TypePtr != nil {
		if idx++; idx == len(s.slots) {
			idx = 0
		}
	}
	s.slots[idx] = entry
}

// withEntry returns a snapshot carrying one more entry. When capacity
// suffices, existing entries keep their slots and the new entry probes
// into a copy; otherwise the table rebuilds at double capacity.
func (s *ifaceCacheSnapshot) withEntry(entry VjIfaceCacheEntry) *ifaceCacheSnapshot {
	if (s.count+1)*2 > len(s.slots) {
		entries := make([]VjIfaceCacheEntry, 0, s.count+1)
		for i := range s.slots {
			if s.slots[i].TypePtr != nil {
				entries = append(entries, s.slots[i])
			}
		}
		entries = append(entries, entry)
		return buildIfaceTable(entries)
	}
	next := &ifaceCacheSnapshot{
		slots: make([]VjIfaceCacheEntry, len(s.slots)),
		shift: s.shift,
		count: s.count + 1,
	}
	copy(next.slots, s.slots)
	next.place(entry)
	return next
}

// buildIfaceTable lays entries into a fresh table sized for load factor
// <= 0.5. Capacity stays >= 32, keeping the shift far from the 64-bit
// edge where h >> shift would be undefined.
func buildIfaceTable(entries []VjIfaceCacheEntry) *ifaceCacheSnapshot {
	tableCap := 32
	for tableCap < 2*len(entries) {
		tableCap <<= 1
	}
	snap := &ifaceCacheSnapshot{
		slots: make([]VjIfaceCacheEntry, tableCap),
		shift: uint8(64 - bits.Len64(uint64(tableCap-1))),
		count: len(entries),
	}
	for _, e := range entries {
		snap.place(e)
	}
	return snap
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

	// Register ops before publishing so SWITCH_OPS can always resolve the active Blueprint.
	registerBlueprintOps(bp)

	globalIfaceCache.current.Store(cur.withEntry(entry))
}

// insertIfaceCacheBody attaches a body-only Blueprint to an existing entry
// (or inserts a new one carrying only the body). The snapshot is
// copy-on-write: in-flight VMs keep reading the table they were handed.
func insertIfaceCacheBody(typePtr unsafe.Pointer, bodyBP *Blueprint) {
	globalIfaceCache.mu.Lock()
	defer globalIfaceCache.mu.Unlock()

	bodyPtr := unsafe.Pointer(&bodyBP.Ops[0])

	cur := globalIfaceCache.current.Load()
	if idx := cur.find(typePtr); idx >= 0 {
		if cur.slots[idx].BodyOpsPtr == bodyPtr {
			return
		}
		registerBlueprintOps(bodyBP)
		slots := make([]VjIfaceCacheEntry, len(cur.slots))
		copy(slots, cur.slots)
		slots[idx].BodyOpsPtr = bodyPtr
		globalIfaceCache.current.Store(&ifaceCacheSnapshot{slots: slots, shift: cur.shift, count: cur.count})
		return
	}

	registerBlueprintOps(bodyBP)
	globalIfaceCache.current.Store(cur.withEntry(VjIfaceCacheEntry{TypePtr: typePtr, BodyOpsPtr: bodyPtr}))
}

// bodyBlueprintCache holds body-only Blueprints keyed by rtype pointer, so
// the interp path and the miss handler share one compilation per type.
var bodyBlueprintCache sync.Map

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

	entries := make([]VjIfaceCacheEntry, 0, len(primitives)+8)
	for _, e := range primitives {
		entries = append(entries, VjIfaceCacheEntry{
			TypePtr: rtypePtr(e.t),
			Tag:     e.tag,
		})
	}

	// A boxed value.Value encodes through the native tape walk: the tag
	// routes OP_INTERFACE into the walk instead of the fail-closed
	// primitive encoder.
	entries = append(entries, VjIfaceCacheEntry{
		TypePtr: rtypePtr(reflect.TypeFor[value.Value]()),
		Tag:     uint8(opValue),
	})

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
		entries = append(entries, entry)
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
		entries = append(entries, entry)
	}

	globalIfaceCache.current.Store(buildIfaceTable(entries))
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
