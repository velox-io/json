// TypeTree is an immutable Go-layout table shared by binding backends. Build
// records cross-table references as indexes while slices can grow, then resolves
// hot references to ABI pointers after their backing arrays are frozen. Per-call
// input, destination, allocation, and parser control state remain outside it.

package vbind

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// Kind selects both the parser dispatch path and the legal BindType payload
// view. Scalar kinds leave the payload zero. Kinds without a dedicated payload
// are handled by hooks or backend policy.
//
// Kind is uint8 so BindType can pack a self-describing TypeIdx (uint16) into
// the same 4B header word without growing the struct.
type Kind uint8

const (
	_ Kind = iota // 0 reserved: uninitialized
	KindBool
	KindInt
	KindInt8
	KindInt16
	KindInt32
	KindInt64
	KindUint
	KindUint8
	KindUint16
	KindUint32
	KindUint64
	KindFloat32
	KindFloat64
	KindString
	KindStruct
	KindSlice
	KindPointer
	KindAny
	KindMap
	KindRawMessage
	KindNumber
	KindArray
	KindIface
	KindUnmarshaler     // implements json.Unmarshaler
	KindTextUnmarshaler // implements encoding.TextUnmarshaler (only when UnmarshalJSON absent)
	KindValue           // value.Value uses tape backed navigation
	KindStream          // stream.Stream[T]: slice storage + yield policy (empty-open / close / drain)
)

// TapeBindUnsupportedPos names the first path outside UnmarshalValue's tape
// walker capabilities. Build computes it once so entry validation can report a
// path-aware error before native traversal begins.
type TapeBindUnsupportedPos struct {
	Path    string // dotted path from root, e.g. "User.Profile.Data"
	TypeIdx uint16 // type index at the unsupported position
	Reason  string // e.g. "any at root", "*any field", "variant case has cold kind"
}

// These bit positions are part of the Go/C ABI and must match BIND_FLAG_*.
const (
	// bindFlagMayPhase2 marks a struct that may require merged-tape work at close.
	// It is a type-local superset covering inline variants, reserve-unknown, and
	// polymorphic fields; the cold close path selects the precise work.
	bindFlagMayPhase2 uint8 = 1 << 5
	// Any type requiring predispatch work must set this bit because the native
	// hot path treats flags == 0 as permission to dispatch directly.
	bindFlagCold uint8 = 1 << 6
	// bindFlagContainsDeferred marks an aggregate (struct, array, slice, or map)
	// whose transitive type tree reaches an Unmarshaler, TextUnmarshaler,
	// RawMessage, or KindValue. A map value with this flag is staged in a
	// scannable SlotClass, with only its address in the noscan KV entry; see
	// mapValueNeedsIndirection, which also answers the drain's half.
	bindFlagContainsDeferred uint8 = 1 << 7
	// bindFlagElemHasStream marks a Stream[T] whose element type tree contains
	// a Stream field. Triggers per-element yield in native array_value so Go can
	// register nested OnRead via Item.Target() before the element body binds.
	// Bit 2 is free at type level: bit 0 is field-local QUOTED, bit 1 is
	// runtime-only STREAM_SKIP.
	bindFlagElemHasStream uint8 = 1 << 2
)

// BindType is a 16 byte Go/C ABI record. The selected payload view and child
// target are valid only for Kind. child stays uintptr to keep the record noscan;
// TypeTree owns the referenced backing slices, and Build resolves the pointer
// only after those slices are frozen.
type BindType struct {
	Kind    Kind    // off 0  parser dispatch discriminator (heavy read)
	flags   uint8   // off 1  BIND_FLAG_* (ANY/DEFERRED fast check), set at build time
	TypeIdx uint16  // off 2  self-describing index into TypeTree.Types
	inner   uint32  // off 4  kind specific u32 (child_size / alloc_class / field_count)
	child   uintptr // off 8  kind specific pointer stored as uintptr to keep BindType noscan
}

// SetStreamSkip puts a mutable frame copy into stream drain mode. Native skips
// element bodies until bind_pop discards that frame; callers must use a frame
// copy rather than an immutable TypeTree entry.
func (bt *BindType) SetStreamSkip() {
	const bindFlagStreamSkip uint8 = 1 << 1
	bt.flags |= bindFlagStreamSkip
}

// HasElemHasStream reports whether this BindType carries the per-element yield
// flag (set at build time on non-leaf Stream[T] types whose element type tree
// contains a Stream field). The decode/bind driver reads it to select the
// per-element + Value-triggers-bind path instead of the batch path.
func (bt *BindType) HasElemHasStream() bool {
	return bt.flags&bindFlagElemHasStream != 0
}

// HasContainsDeferred reports the binder side of map-value indirection. It must
// agree with MapDrainInfo.ValIsDeferred so the drain interprets each staging
// entry with the representation native wrote.
func (bt *BindType) HasContainsDeferred() bool {
	return bt.flags&bindFlagContainsDeferred != 0
}

type StructPayload struct {
	FieldCount uint32
}

// ChildSize stays in the hot record so element dispatch does not read TypeMeta.
type SlicePayload struct {
	ChildSize uint32
}

// Its layout must match SlicePayload because native element dispatch is shared.
type ArrayPayload struct {
	ChildSize uint32
}

// AllocClass identifies pointee storage. It is distinct from any SlotClass
// used to back slices of the same element type.
type PointerPayload struct {
	AllocClass int32
}

// AllocClass stays in the hot record because map open needs the hmap header
// slot before consulting map metadata.
type MapPayload struct {
	AllocClass int32
}

// Struct returns the KindStruct payload view. Call it only after checking Kind.
func (bt *BindType) Struct() *StructPayload {
	return (*StructPayload)(unsafe.Pointer(&bt.inner))
}

// StructFirstFieldIndex returns the Fields index of this struct's first field,
// given the base of the owning Fields slice. Like ChildIndex it stays in
// uintptr space and never resurrects bt.child as an unsafe.Pointer, so callers
// can index the Fields slice directly instead of doing pointer arithmetic that
// trips -d=checkptr. Valid only when Kind == KindStruct.
func (bt *BindType) StructFirstFieldIndex(base *BindField) uint32 {
	return uint32((bt.child - uintptr(unsafe.Pointer(base))) / unsafe.Sizeof(BindField{}))
}

// Slice returns the KindSlice payload view. Call it only after checking Kind.
func (bt *BindType) Slice() *SlicePayload {
	return (*SlicePayload)(unsafe.Pointer(&bt.inner))
}

// Array returns the KindArray payload view. Call it only after checking Kind.
func (bt *BindType) Array() *ArrayPayload {
	return (*ArrayPayload)(unsafe.Pointer(&bt.inner))
}

// Pointer returns the KindPointer payload view. Call it only after checking Kind.
func (bt *BindType) Pointer() *PointerPayload {
	return (*PointerPayload)(unsafe.Pointer(&bt.inner))
}

// ChildIndex is valid for pointer, slice, array, and map kinds. Arithmetic
// remains in uintptr space because reconstructing unsafe.Pointer from child
// trips checkptr.
func (bt *BindType) ChildIndex(base *BindType) uint32 {
	return uint32((bt.child - uintptr(unsafe.Pointer(base))) / unsafe.Sizeof(BindType{}))
}

// Map returns the KindMap payload view. Call it only after checking Kind.
func (bt *BindType) Map() *MapPayload {
	return (*MapPayload)(unsafe.Pointer(&bt.inner))
}

// setChild stores a raw pointer into the child slot as a uintptr. Callers
// use this from the second builder pass; the pointer must reference memory
// held alive by the enclosing TypeTree.
func (bt *BindType) setChild(p unsafe.Pointer) {
	bt.child = uintptr(p)
}

func (bt *BindType) InnerRaw() uint32 { return bt.inner }

func (bt *BindType) ChildRaw() uintptr { return bt.child }

// AnyMeta is valid only for KindAny and KindIface. Index arithmetic avoids a
// uintptr to unsafe.Pointer round trip that checkptr rejects.
func (bt *BindType) AnyMeta(anyMetas []BindAnyMeta) *BindAnyMeta {
	base := uintptr(unsafe.Pointer(unsafe.SliceData(anyMetas)))
	idx := int((bt.child - base) / unsafe.Sizeof(BindAnyMeta{}))
	return &anyMetas[idx]
}

// TypeMeta is a 32 byte Go/C ABI record parallel to Types. payload is declared
// as [3]uintptr to provide eight byte alignment while keeping the record noscan.
// Reinterpreting it through views containing unsafe.Pointer does not change its
// GC bitmap. Every referenced object must therefore remain reachable through a
// scannable owner or a process lifetime root.
type TypeMeta struct {
	Flags   uint32     // off 0  reserved by the ABI
	Size    uint32     // off 4  sizeof(T) (all Kinds)
	payload [3]uintptr // off 8  24B kind exclusive union
}

type ArrayMetaPayload struct {
	ArrayLen uint32 // fixed element count
}

type PointerMetaPayload struct {
	PtrChildSize uint32 // sizeof(pointee)
}

// MapMetaPayload is a Go/C ABI view. Stride is 16 plus sizeof(V), rounded to
// eight bytes, because the noscan staging entry starts with a string header.
// DrainInfo is safe only while TypeTree.MapDrainInfo keeps its referent reachable;
// the unsafe.Pointer view does not make TypeMeta scannable.
type MapMetaPayload struct {
	KeyType   uint32         // off 0  key type in-tree index (Build-only, used to construct MapDrainInfo)
	DrainInfo unsafe.Pointer // off 8  *MapDrainInfo (drain path; noscan via payload [3]uintptr)
	Stride    uint32         // off 16 entry_slot stride in bytes (== 16 + sizeof(V) padded to 8)
}

// ValIsDeferred means the map value is staged in a scannable SlotClass. The map
// entry holds its address until drain assigns the value.
//
// ValIndirect is a different indirection with a different owner: Go itself stores
// an element larger than gort.MapMaxElemBytes behind a pointer, so the drain must
// assign it with the generic mapassign. It occupies padding the struct already
// had, so the ABI record is unchanged.
type MapDrainInfo struct {
	MapRType      unsafe.Pointer // off 0   map *_type (mapassign / mapassign_faststr)
	KVStride      uint32         // off 8   SAX slot width (bind reads stride from map_region_header)
	KeyKind       Kind           // off 12  selects string vs int conversion in drain
	ValSize       uint32         // off 16  sizeof(val) for copyMapValue
	ValIsDeferred bool           // off 20  map value is staged in a scannable SlotClass
	ValIndirect   bool           // off 21  Go stores this element behind a pointer; drain must not use faststr
	ValSlotClass  int32          // off 24  SlotClass idx for deferred val intermediate; -1 = N/A
}

// BindAnyMeta is a read only Go/C ABI record reached through a noscan child
// pointer. TypeTree.AnyMetas must keep the record alive. Each SlotClass RType
// must describe the exact boxed storage so the GC scans string and slice data
// pointers correctly. StaticTrue and StaticFalse use package lifetime storage.
type BindAnyMeta struct {
	Float64Type unsafe.Pointer // off 0  runtime type for JSON numbers
	StringType  unsafe.Pointer // off 8  runtime type for JSON strings
	BoolType    unsafe.Pointer // off 16 runtime type for JSON booleans
	NilType     unsafe.Pointer // off 24 nil for JSON null
	SliceType   unsafe.Pointer // off 32 runtime type for JSON arrays
	MapType     unsafe.Pointer // off 40 runtime type for JSON objects

	StaticTrue  *uint64 // off 48 *uint64 = 1
	StaticFalse *uint64 // off 56 *uint64 = 0

	Float64SlotClass int32 // off 64 8B elem
	StringSlotClass  int32 // off 68 16B elem
	SliceSlotClass   int32 // off 72 24B elem
	MapSlotClass     int32 // off 76 reuse map[string]any hmap class

	SliceAnyTypeIdx uint16         // off 80 []any
	MapAnyTypeIdx   uint16         // off 82 map[string]any
	_pad            uint32         // off 84
	NumberType      unsafe.Pointer // off 88 json.Number runtime type for useNumber
}

// variantNoDefaultCase is BindPolyTable.DefaultCaseIdx's "none declared"
// sentinel. A case index of 0 is a real case, so absence needs a value outside
// the index space rather than a zero test.
const variantNoDefaultCase = 0xFFFF

const polyKindCount = 5

// A variant maps a discriminator Go string through Lookup to a case index. A
// kindof table indexes the same arrays directly by JSON kind, so it carries no
// Lookup and no discriminator. Lookup is a scannable pointer that keeps the case
// lookup blob alive; PolyCases owns the arrays behind the remaining pointers.
type BindPolyTable struct {
	// off 0, byte offset of the discriminator field in the host struct; zero for
	// a kindof, which has no discriminator.
	DiscFieldOff uint32
	// DefaultCaseIdx is the case chosen when the discriminator value matched no
	// case, or variantNoDefaultCase when the descriptor declares no default (the
	// unmatched value then reports). It indexes the case arrays like any hit, so
	// the default gets its rtype and slot class from them; the default is appended
	// last and kept out of Lookup, since a lookup miss is what selects it.
	//
	// This governs unmatched VALUES only. A discriminator key absent from the input
	// selects nothing and leaves the target nil, with or without a default: the two
	// answer different questions ("cannot resolve this value" vs "no value given").
	DefaultCaseIdx    uint16         // off 4
	CaseCount         uint16         // off 6  cases, or polyKindCount for a kindof
	caseTypeIdxData   unsafe.Pointer // off 8  case or kind to Types index
	caseRTypeData     unsafe.Pointer // off 16 case or kind to runtime type, itab, or nil
	caseSlotClassData unsafe.Pointer // off 24 case or kind to SlotClass index
	Lookup            unsafe.Pointer // off 32 case string to case index lookup; nil for a kindof
}

// caseIdx must already be validated against CaseCount.
func (p *BindPolyTable) CaseTypeIdx(caseIdx int) uint16 {
	return *(*uint16)(unsafe.Add(p.caseTypeIdxData, uintptr(caseIdx)*2))
}

// CaseRType contains a runtime type pointer for eface targets, an itab for
// nonempty interface targets, and nil for a kind its descriptor did not
// register.
func (p *BindPolyTable) CaseRType(caseIdx int) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Add(p.caseRTypeData, uintptr(caseIdx)*8))
}

func (p *BindPolyTable) CaseSlotClass(caseIdx int) int32 {
	return *(*int32)(unsafe.Add(p.caseSlotClassData, uintptr(caseIdx)*4))
}

// PolyCaseData holds the scannable owners of the arrays a BindPolyTable
// references through raw ABI pointers.
type PolyCaseData struct {
	TypeIdx   []uint16
	RType     []unsafe.Pointer
	SlotClass []int32
}

// StructMetaPayload is a Go/C ABI view. Lookup points to process-owned field
// metadata and always names a valid header. InlineVariantIdx uses 0xFFFF as its
// absence sentinel. ReserveUnknownFieldOff uses 0xFFFFFFFF as its absence
// sentinel and otherwise addresses the destination Value from the struct base.
type StructMetaPayload struct {
	Lookup           unsafe.Pointer // off 0  perfect-hash field-name blob base (process-global; noscan via payload [3]uintptr)
	InlineVariantIdx uint16         // off 8  0-based index into TypeTree.Polys[]; 0xFFFF = none
	// off 10..11 padding (uint32 alignment)
	ReserveUnknownFieldOff uint32 // off 12  byte offset of the reserve-unknown Value field; 0xFFFFFFFF = none

	// PtrHops is the base of this struct's embedded-pointer hop array, or nil
	// when no field is promoted across a pointer (nearly always). A field with
	// BIND_FF_VIA_PTR names a run inside this array; see BindPtrHop. The array is
	// owned by TypeTree.PtrHops, which keeps it reachable, so the payload stays
	// noscan despite the pointer view.
	PtrHops unsafe.Pointer // off 16
}

// BindPtrHop is an 8 byte Go/C ABI record describing one embedded-pointer
// crossing on a promoted field's path.
//
// The hot path addresses a field as cur_dst+offset. That identity holds only
// while every embedding hop is inlined storage. A hop tells native how to get
// across one pointer: read the pointer word at SlotOffset relative to the
// current base, allocate from AllocClass when it is nil, and continue from the
// pointee. A field's Offset is relative to the base its last hop establishes.
//
// Last marks the final hop of a field's run, so a run needs no separate length:
// the field carries only its start index.
type BindPtrHop struct {
	SlotOffset uint32 // off 0  pointer word, relative to the previous hop's base
	AllocClass int32  // off 4  SlotClass for the pointee; sign bit carries Last
}

// ptrHopLastBit marks the end of a field's hop run. It rides in the AllocClass
// sign bit so the record stays 8 bytes; a SlotClass index is always positive.
const ptrHopLastBit = int32(-1) << 31

func (h *BindPtrHop) IsLast() bool     { return h.AllocClass&ptrHopLastBit != 0 }
func (h *BindPtrHop) SlotClass() int32 { return h.AllocClass & ^ptrHopLastBit }

// ElemRType and EmptySliceData have process lifetime owners. The payload remains
// noscan despite the unsafe.Pointer view. AllocClass names a SlotClass dedicated
// to slice backing storage; it must not share consumption state with the pointer
// pointee SlotClass for the same element type.
type SliceMetaPayload struct {
	ElemRType      unsafe.Pointer // off 0
	EmptySliceData uintptr        // off 8
	AllocClass     int32          // off 16  SlotClass index for element backing
}

// ArrayMeta returns the KindArray payload view. Call only when Kind==KindArray.
func (m *TypeMeta) ArrayMeta() *ArrayMetaPayload {
	return (*ArrayMetaPayload)(unsafe.Pointer(&m.payload))
}

// PointerMeta returns the KindPointer payload view. Call only when Kind==KindPointer.
func (m *TypeMeta) PointerMeta() *PointerMetaPayload {
	return (*PointerMetaPayload)(unsafe.Pointer(&m.payload))
}

// MapMeta returns the KindMap payload view. Call only when Kind==KindMap.
func (m *TypeMeta) MapMeta() *MapMetaPayload { return (*MapMetaPayload)(unsafe.Pointer(&m.payload)) }

// StructMeta returns the KindStruct payload view. Call only when Kind==KindStruct.
func (m *TypeMeta) StructMeta() *StructMetaPayload {
	return (*StructMetaPayload)(unsafe.Pointer(&m.payload))
}

// SliceMeta returns the KindSlice payload view. Call only when Kind==KindSlice.
func (m *TypeMeta) SliceMeta() *SliceMetaPayload {
	return (*SliceMetaPayload)(unsafe.Pointer(&m.payload))
}

// BindField is a 16 byte Go/C ABI record. Type stays uintptr so the record is
// noscan; TypeTree.Types owns the target and Build resolves it only after freeze.
// Flags combines parse hot tag bits, inherited type flags, polymorphic markers,
// and a table index in the high 16 bits.
type BindField struct {
	Type   uintptr // off 0  *BindType stored as uintptr
	Offset uint32  // off 8
	Flags  uint32  // off 12 tag bits, type flags, poly markers, and table index
}

// FieldTypeIndex remains in uintptr space because reconstructing unsafe.Pointer
// from Type trips checkptr.
func (f *BindField) FieldTypeIndex(base *BindType) uint32 {
	return uint32((f.Type - uintptr(unsafe.Pointer(base))) / unsafe.Sizeof(BindType{}))
}

func (f *BindField) setFieldType(p unsafe.Pointer) {
	f.Type = uintptr(p)
}

// These low bit positions are part of the Go/C ABI and must match BIND_FF_*.
type FieldTagFlag uint32

const (
	TagQuoted FieldTagFlag = 1 << 0
	// The static field type remains any or interface until discriminator
	// dispatch selects a case.
	TagVariant FieldTagFlag = 1 << 3
	// A discriminator field carries a poly index in the high 16 bits, but only the
	// embedded case reads it: TagInlineVDisc routes the value onto the merged tape,
	// and the phase2 scan that binds it matches on the host's InlineVariantIdx.
	// Sibling dispatch locates the discriminator through its table's own
	// DiscFieldOff instead, which is why several sibling variants may name one
	// discriminator field.
	TagVDisc FieldTagFlag = 1 << 4
	// Bit 8 lies above the inherited type flag byte. The JSON value kind selects
	// the case that supplies the concrete type.
	TagKindof FieldTagFlag = 1 << 8
	// The table index is also stamped on StructMetaPayload.InlineVariantIdx so
	// the native struct-open path intercepts before IDX_CONSUME and routes the
	// whole struct through vd_dispatch.
	TagInlineVariant FieldTagFlag = 1 << 9
	// TagReserveUnknown marks a value.Value field that reserves all unmatched
	// object keys into itself (the reserve-unknown). Bit 10 stays clear of
	// the inherited type-flag byte (bits 0..7) and the high-16 table-index
	// space. Read by the bind path's object_field miss handler to divert
	// unmatched keys into the reserve-unknown tape.
	TagReserveUnknown FieldTagFlag = 1 << 10
	// TagInlineVDisc marks the discriminator of the host's OWN inline variant,
	// as opposed to a sibling variant's discriminator. Such a field's value goes
	// onto the merged tape rather than straight to Go, because the selected case
	// may be a value.Value that must see the discriminator among its fields.
	//
	// The distinction is static (poly table construction knows whether it is
	// building an inline variant), so it is a bit rather than a runtime compare of
	// the field's poly index against the host's InlineVariantIdx. That keeps the
	// question inside the one cold-path flag mask instead of on the hot path.
	TagInlineVDisc FieldTagFlag = 1 << 11
	// TagViaPtr marks a field promoted across at least one embedded pointer, so
	// its Offset is relative to the pointee of its last hop rather than to the
	// host. Offset alone cannot reach it; the hops must be walked first.
	//
	// The field's hop run starts at StructMetaPayload.PtrHops[HopStart] and ends
	// at the hop whose Last bit is set. HopStart rides in the high 16 bits, which
	// it can share with the poly table index only because a
	// via-ptr field is never a polymorphic target: attachVariantsForStruct and
	// attachKindofsForStruct refuse that combination. The refusal is what keeps
	// this bit space unambiguous, so it is not an assumption but an invariant.
	TagViaPtr FieldTagFlag = 1 << 12
)

// The high 16 bits hold an index into the shared poly table. The marker bit
// decides whether the variant or the kindof interpretation applies.
const fieldFlagPolyIdxShift = 16

func PackVariantFieldFlags(variantIdx uint16, inheritedTypeFlags uint8) uint32 {
	return uint32(TagVariant) | (uint32(variantIdx) << fieldFlagPolyIdxShift) | uint32(inheritedTypeFlags)
}

func PackKindofFieldFlags(kindofIdx uint16, inheritedTypeFlags uint8) uint32 {
	return uint32(TagKindof) | (uint32(kindofIdx) << fieldFlagPolyIdxShift) | uint32(inheritedTypeFlags)
}

func PackInlineVariantFieldFlags(variantIdx uint16, inheritedTypeFlags uint8) uint32 {
	return uint32(TagInlineVariant) | (uint32(variantIdx) << fieldFlagPolyIdxShift) | uint32(inheritedTypeFlags)
}

// FieldPolyIdx is valid for every polymorphic field: variant targets,
// discriminators, inline variant targets, and kindof targets.
func FieldPolyIdx(f *BindField) uint16 {
	return uint16(f.Flags >> fieldFlagPolyIdxShift)
}

// FieldHopStart is valid only for TagViaPtr fields. It shares the high-16 index
// space with the poly table, which is sound because a promoted field is never a
// polymorphic target.
func FieldHopStart(f *BindField) uint16 {
	return uint16(f.Flags >> fieldFlagPolyIdxShift)
}

func FieldViaPtr(f *BindField) bool {
	return f.Flags&uint32(TagViaPtr) != 0
}

func FieldHasVariant(f *BindField) bool {
	return f.Flags&uint32(TagVariant) != 0
}

func FieldHasKindof(f *BindField) bool {
	return f.Flags&uint32(TagKindof) != 0
}

func FieldHasInlineVariant(f *BindField) bool {
	return f.Flags&uint32(TagInlineVariant) != 0
}

func FieldIsDiscriminator(f *BindField) bool {
	return f.Flags&uint32(TagVDisc) != 0
}

// slotMode is part of the Go/C ABI. Native slice dispatch uses it to distinguish
// RecBatch from the shared bump path.
type slotMode uint8

const (
	slotBump     slotMode = 0 // non-recursive pointer/slice/map/primitive
	slotRecBump  slotMode = 1 // recursive pointer/map: bump dispatch + detach
	slotRecBatch slotMode = 2 // recursive slice: RecBatchMatrix + detach
)

// SlotTemplate is Go only. Native code reads the SlotClass materialized by an
// Allocator, never this layout.
type SlotTemplate struct {
	ElemSize uint32
	Batch    uint32
	Flags    SlotFlag
	Mode     slotMode
	Group    uint32         // SCC group ID (1..GroupCount); 0 = non-recursive
	RType    unsafe.Pointer // runtime *_type for typed batch allocation
	IsStream bool           // stream.Stream[T] uses a fixed Go-allocated batch buffer
}

// SlotClass is the 48 byte BindSlotClass Go/C ABI sum type. Mode selects the
// valid overlay. Do not reorder or resize these fields.
//
// Layout:
//
//	@0  Block     8  bump arena base OR *RecBatchMatrix        [all]
//	@8  RType     8  runtime *_type (Go-only)              [all]
//	@16 ElemSize  4  immutable                              [all]
//	@20 Mode      1  slotMode (C reads for slice dispatch)  [all]
//	@21 Flags     1  SlotIsMap (Go-only)                    [all]
//	@22 pad       2
//	@24 Offset    4  byte cursor                            [Bump, RecBump]
//	@28 Limit     4  byte limit                             [Bump, RecBump]
//	@32 Len       4  cumulative elem count (EWMA sample)    [Bump]
//	@36 Cap       4  elem capacity                          [Bump]
//	@40 Aux       4  MuBlock [Bump] | Group [RecBump, RecBatch] (Go-only)
//	@44 pad       4
type SlotClass struct {
	Block    unsafe.Pointer // off 0  8  bump arena OR *RecBatchMatrix
	RType    unsafe.Pointer // off 8  8  runtime *_type (Go-only; C never reads)
	ElemSize uint32         // off 16 4  immutable
	Mode     slotMode       // off 20 1  Bump/RecBump/RecBatch
	Flags    SlotFlag       // off 21 1  SlotIsMap (Go-only; C never reads)
	_pad0    [2]byte        // off 22 2
	Offset   uint32         // off 24 4  byte cursor [Bump, RecBump]
	Limit    uint32         // off 28 4  byte limit  [Bump, RecBump]
	Len      uint32         // off 32 4  cumulative elem count [Bump]
	Cap      uint32         // off 36 4  elem cap [Bump]
	Aux      uint32         // off 40 4  MuBlock [Bump] | Group [RecBump, RecBatch]
	_pad1    [4]byte        // off 44 4
}

// IsBumpTail reports whether this class hands out slice backings by borrowing
// the tail of an installed bump block, which is the only mode where a cursor can
// be narrowed back down to the region a slice actually wrote. RecBatch serves
// backings from its matrix, and a detached class has no block at all.
func (sc *SlotClass) IsBumpTail() bool {
	return sc.Mode == slotBump && sc.Block != nil
}

// This overlay is valid only when Mode is slotBump. Offset 40 holds MuBlock.
type BumpSlotClass struct {
	Block    unsafe.Pointer
	RType    unsafe.Pointer
	ElemSize uint32
	Mode     slotMode
	Flags    SlotFlag
	_pad0    [2]byte
	Offset   uint32
	Limit    uint32
	Len      uint32
	Cap      uint32
	MuBlock  uint32
	_pad1    [4]byte
}

// This overlay is valid only when Mode is slotRecBump. Offset 40 holds Group;
// Len and Cap are not valid in this mode.
type RecBumpSlotClass struct {
	Block    unsafe.Pointer
	RType    unsafe.Pointer
	ElemSize uint32
	Mode     slotMode
	Flags    SlotFlag
	_pad0    [2]byte
	Offset   uint32
	Limit    uint32
	_pad1    [8]byte
	Group    uint32
	_pad2    [4]byte
}

// This overlay is valid only when Mode is slotRecBatch. Block points directly
// at RecBatchMatrix data and offset 40 holds Group.
type RecBatchSlotClass struct {
	Block    unsafe.Pointer
	RType    unsafe.Pointer
	ElemSize uint32
	Mode     slotMode
	Flags    SlotFlag
	_pad0    [2]byte
	_pad1    [16]byte
	Group    uint32
	_pad2    [4]byte
}

// SlotFlag is Go only. Native code must branch on Mode instead.
type SlotFlag uint8

const (
	// Zeroed map slots contain nil hmap pointers and cannot be published. The
	// allocator prewires each slot, and the scannable parent block keeps every
	// inner map allocation reachable through Block[i].
	SlotIsMap SlotFlag = 1 << iota
)

// Scannable TypeTree slices keep builder owned arrays and variant lookup blobs
// alive. Struct lookup blobs and runtime type pointers depend on separate
// process lifetime roots.
type TypeTree struct {
	Root       uint32 // root index into Types
	Types      []BindType
	Fields     []BindField
	FieldNames []string   // Go-only (not in C ABI); parallel to Fields, JSON name per field for the tape walker
	TypeMeta   []TypeMeta // parallel to Types, per-Kind metadata (union payload)
	Slots      []SlotTemplate

	// This slice owns the records referenced by MapMetaPayload.DrainInfo.
	MapDrainInfo []MapDrainInfo

	// This slice owns the metadata referenced by KindAny and KindIface child pointers.
	AnyMetas []BindAnyMeta

	// Polys contains the variant and kindof tables. Associated target and
	// discriminator fields carry the table index in Flags bits 16 through 31,
	// and their tag bit selects the interpretation.
	Polys []BindPolyTable

	// PolyCases is parallel to Polys and owns the case arrays behind its raw
	// pointers, which keeps them reachable.
	PolyCases []PolyCaseData

	// PtrHops owns every struct's embedded-pointer hop array. StructMetaPayload
	// keeps only a raw base pointer (the payload must stay noscan), so this slice
	// is what makes those hops reachable. Empty for a type graph with no field
	// promoted across a pointer, which is the usual case.
	PtrHops [][]BindPtrHop

	// AnyTypeIdx indexes the universal any type when it is reachable. Index zero
	// is valid and must not be interpreted as an absence sentinel.
	AnyTypeIdx uint32

	// Group values from 1 through GroupCount identify backing dependency SCCs.
	// Their pooled storage detaches periodically to break cross parse reachability.
	GroupCount uint32

	// UnmarshalHooks is parallel to Types so drain can select a prebound hook
	// without reflection.
	UnmarshalHooks []*typ.InterfaceHooks

	// MapBufMinBytes is a capacity floor for all map regions simultaneously live
	// on one descent path. FLUSH_MAP handles growth beyond this floor.
	MapBufMinBytes uint32

	// ReflectTypes is parallel to Types and is reserved for error reporting.
	ReflectTypes []reflect.Type

	// TapeBindUnsupported names the first position from Root that the tape-bind
	// sub-routine cannot walk. nil means the whole tree is supported by
	// UnmarshalValue. Populated by a post-collect tree walk in Build.
	TapeBindUnsupported *TapeBindUnsupportedPos

	// HasValueField reports that some KindValue is reachable from Root, i.e. this
	// tree can put a value.Value in a destination. Only then is there a tape whose
	// coordinates a reader will resolve, so only then is a Doc worth
	// publishing.
	//
	// HasPolyField reports that some variant or kindof table exists, i.e. a field
	// binds through a merged tape as an intermediate. That tape is scratch,
	// consumed by the case walker and never published, which is why it is tracked
	// apart from HasValueField even though both need the tape arena.
	//
	// HasSplitTape reports that some reachable struct hosts an inline variant AND
	// a reserve-unknown. One merged tape then serves two consumers wanting
	// different subsets, which is what pushes its word count past srcLen. Neither
	// feature alone does it.
	//
	// TapeBindMayAppendStrings reports that UnmarshalValue can append copied
	// discriminators or quoted strings to the input document's string arena.
	//
	// These properties are settled while building the type graph rather than
	// rediscovered by each consumer. Allocation remains the consumer's policy.
	HasValueField            bool
	HasPolyField             bool
	HasSplitTape             bool
	TapeBindMayAppendStrings bool

	// SplitTapeSites bounds how many dual-view merged tapes ONE parse can build,
	// or SplitTapeSitesUnbounded when the document decides.
	//
	// It exists because the dual-view budget term is per tape, not per byte. A
	// dual shared root spends the same words as a single-view tape, but the
	// cumulative span a nested tape rebind can copy is not yet proven against
	// srcLen, so each dual site keeps two words of retained headroom: a budget
	// of 2*srcLen reads as though the content doubled, and a host can be as
	// short as `{}`, so a document of minimal hosts pays that headroom every 3
	// source bytes, which is where the 1.667 worst case comes from.
	//
	// So the working bound is srcLen+3+2*K. When every dual-view host sits at a
	// fixed struct-field position, K is a small constant settled here and the
	// budget is 1x plus change. K stops being static exactly when a host is
	// reachable under a slice, array, map or stream, or through a pointer cycle:
	// then the input chooses the count and only srcLen can bound it.
	//
	// Counted as POSITIONS, not types: one type reached from two fields builds a
	// tape per field, so the walk adds up occurrences and does not memoize. That is
	// also why it is bounded separately from the acyclic-ness of the type graph.
	SplitTapeSites int
}

// SplitTapeSitesUnbounded marks a tree where the document, not the type, decides
// how many dual-view merged tapes a parse builds. Callers must fall back to a
// per-byte bound; see TypeTree.SplitTapeSites.
const SplitTapeSitesUnbounded = -1

// IsStreamType reports whether the type at typeIdx is a stream.Stream[T]
// field collected as a slice. The Go driver reads this to intercept
// SLICE_GROW yields and invoke the per-batch handler.
func (t *TypeTree) IsStreamType(typeIdx uint32) bool {
	return t.Types[typeIdx].Kind == KindStream
}

// These size checks keep the Go layout aligned with backend ABI assumptions.
var (
	_ = [1]struct{}{}[unsafe.Sizeof(BindType{})-16]
	_ = [1]struct{}{}[unsafe.Offsetof(BindType{}.Kind)-0]
	_ = [1]struct{}{}[unsafe.Offsetof(BindType{}.flags)-1]
	_ = [1]struct{}{}[unsafe.Offsetof(BindType{}.TypeIdx)-2]
	_ = [1]struct{}{}[unsafe.Offsetof(BindType{}.inner)-4]
	_ = [1]struct{}{}[unsafe.Offsetof(BindType{}.child)-8]
	_ = [1]struct{}{}[unsafe.Sizeof(StructPayload{})-4]
	_ = [1]struct{}{}[unsafe.Sizeof(SlicePayload{})-4]
	_ = [1]struct{}{}[unsafe.Sizeof(ArrayPayload{})-4]
	_ = [1]struct{}{}[unsafe.Sizeof(PointerPayload{})-4]
	_ = [1]struct{}{}[unsafe.Sizeof(MapPayload{})-4]
	_ = [1]struct{}{}[unsafe.Sizeof(BindField{})-16]
	_ = [1]struct{}{}[unsafe.Sizeof(SlotClass{})-48]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.Block)-0]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.RType)-8]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.ElemSize)-16]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.Mode)-20]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.Offset)-24]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.Limit)-28]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.Len)-32]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.Cap)-36]
	_ = [1]struct{}{}[unsafe.Offsetof(SlotClass{}.Aux)-40]
	_ = [1]struct{}{}[unsafe.Sizeof(BumpSlotClass{})-48]
	_ = [1]struct{}{}[unsafe.Offsetof(BumpSlotClass{}.Flags)-21]
	_ = [1]struct{}{}[unsafe.Offsetof(BumpSlotClass{}.MuBlock)-40]
	_ = [1]struct{}{}[unsafe.Sizeof(RecBumpSlotClass{})-48]
	_ = [1]struct{}{}[unsafe.Offsetof(RecBumpSlotClass{}.Flags)-21]
	_ = [1]struct{}{}[unsafe.Offsetof(RecBumpSlotClass{}.Group)-40]
	_ = [1]struct{}{}[unsafe.Sizeof(RecBatchSlotClass{})-48]
	_ = [1]struct{}{}[unsafe.Offsetof(RecBatchSlotClass{}.Flags)-21]
	_ = [1]struct{}{}[unsafe.Offsetof(RecBatchSlotClass{}.Group)-40]
	_ = [1]struct{}{}[unsafe.Sizeof(TypeMeta{})-32]
	_ = [1]struct{}{}[unsafe.Alignof(TypeMeta{})-8]
	_ = [1]struct{}{}[unsafe.Sizeof(ArrayMetaPayload{})-4]
	_ = [1]struct{}{}[unsafe.Sizeof(PointerMetaPayload{})-4]
	_ = [1]struct{}{}[unsafe.Sizeof(MapMetaPayload{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(StructMetaPayload{})-24]
	_ = [1]struct{}{}[unsafe.Offsetof(StructMetaPayload{}.PtrHops)-16]
	_ = [1]struct{}{}[unsafe.Sizeof(BindPtrHop{})-8]
	_ = [1]struct{}{}[unsafe.Offsetof(BindPtrHop{}.AllocClass)-4]
	_ = [1]struct{}{}[unsafe.Sizeof(SliceMetaPayload{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(BindAnyMeta{})-96]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.Float64Type)-0]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.StringType)-8]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.BoolType)-16]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.NilType)-24]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.SliceType)-32]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.MapType)-40]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.StaticTrue)-48]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.StaticFalse)-56]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.Float64SlotClass)-64]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.StringSlotClass)-68]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.SliceSlotClass)-72]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.MapSlotClass)-76]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.SliceAnyTypeIdx)-80]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.MapAnyTypeIdx)-82]
	_ = [1]struct{}{}[unsafe.Offsetof(BindAnyMeta{}.NumberType)-88]
)
