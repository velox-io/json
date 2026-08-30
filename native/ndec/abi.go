// This file mirrors the DOM, formatter, and binding entry ABIs exposed by
// native/ndec. Field order, size, and alignment are shared with C.
//
// The binding driver allocates the complete NdecBindMachine as noscan bytes.
// Go mirrors the bridge, scalar core prefix, frames, and input cursor pair;
// C-private state occupies the remainder. Go owners, retained storage, and
// explicit KeepAlive calls provide pointer reachability.

package ndec

import (
	"unsafe"

	"github.com/velox-io/json/vbind"
)

// SAX exit codes, mirroring enum NdecExit in impl/ndec/core/sapi.h.
const (
	ErrSyntax   = 2
	ErrEOF      = 3
	ErrDepth    = 4
	ErrKeyword  = 5
	ErrTrailing = 6
	ErrUTF8     = 7
)

// FmtFull is the fmt entry's "dst too small" code: DstLen then holds
// the exact needed size.
const FmtFull = 1

// DomTapeFull is the counted DOM entry's "tape arena too small" code: TapeNeed
// then holds the exact word bound, the structural scan is already delivered,
// and the build entry finishes the parse after the caller grows the arena.
const DomTapeFull = 1

const (
	ScanPadding   = 64 // 0x20 sentinel padding appended past src
	AtofStateSize = 2688
	DOMStateSize  = 4096
	FmtStateSize  = 4608
)

type DOMContext struct {
	Src           *byte          // off 0
	SrcLen        uintptr        // off 8
	Tape          *uint64        // off 16
	TapeCap       uintptr        // off 24
	StrArena      *byte          // off 32
	StrArenaCap   uintptr        // off 40
	Structural    *uint32        // off 48
	StructuralCap uint32         // off 56
	StrMode       uint32         // off 60
	DOMState      unsafe.Pointer // off 64
	AtofState     unsafe.Pointer // off 72
	TapeLen       uintptr        // off 80
	StrUsed       uintptr        // off 88
	Err           int32          // off 96
	NStructural   uint32         // off 100; parse_counted out, build in
	TapeNeed      uint32         // off 104; parse_counted out: counted tape-word bound
	ScanStrict    uint32         // off 108; 1 = strict scan: validate UTF-8, reject raw control bytes
}

// RunCounted scans with the mode selected by ScanStrict and counts the scalar
// population. It builds immediately when the tape arena holds TapeNeed. A short
// arena returns DomTapeFull with the structural scan available to RunBuild.
func (c *DOMContext) RunCounted() { DomParseCountedRun(unsafe.Pointer(c)) }

// RunBuild finishes a counted parse after DomTapeFull: it builds the tape from
// the structural scan the counted entry already wrote, using the NStructural it
// reported.
func (c *DOMContext) RunBuild() { DomBuildRun(unsafe.Pointer(c)) }

type FmtContext struct {
	Src       *byte          // off 0
	SrcLen    uintptr        // off 8
	Dst       *byte          // off 16
	DstCap    uintptr        // off 24
	Compact   uint32         // off 32; 1 = compact, 0 = indent
	PrefixLen uint32         // off 36
	IndentLen uint32         // off 40
	_         uint32         // off 44
	Prefix    *byte          // off 48
	Indent    *byte          // off 56
	State     unsafe.Pointer // off 64; >= FmtStateSize bytes
	DstLen    uintptr        // off 72; written, or the needed size on FULL
	ErrPos    uint32         // off 80
	Err       int32          // off 84; 0 ok, FmtFull, else Err* code
}

func (c *FmtContext) Run() { FmtParseRun(unsafe.Pointer(c)) }

// These aliases let C consume the TypeTree and allocator tables without a
// translated mirror. Their layouts are part of the binding ABI.
type (
	BindType      = vbind.BindType
	BindField     = vbind.BindField
	BindTypeMeta  = vbind.TypeMeta
	BindSlotClass = vbind.SlotClass
	BindAnyMeta   = vbind.BindAnyMeta
	BindPolyTable = vbind.BindPolyTable
)

// BindBridge must match NdecBindBridge in bind_bridge.h byte for byte.
type BindBridge struct {
	Ctx   BindContext   // off 0
	Alloc BindAllocator // off 64
	Yield BindYield     // off 184
}

// BindMachine mirrors NdecBindMachine through the scalar core prefix. Native
// frames and private state occupy the same allocation immediately afterward.
type BindMachine struct {
	BindBridge                // off 0
	Core       BindCoreHeader // off 208
}

// BindContext holds borrowed per-call inputs. Types and TypeMeta remain owned
// and rooted by the Go TypeTree. Src and RootDst remain owned and kept alive by
// the caller until native returns. Struct fields are reached through BindType's
// child pointer, while TypeMeta supplies the parallel kind-specific metadata.
type BindContext struct {
	Types    *BindType     // off 0
	TypeMeta *BindTypeMeta // off 8
	Src      *byte         // off 16
	SrcLen   uint64        // off 24
	RootType uint32        // off 32
	// RootViewMode selects the logical seam view of tape input, packing the
	// seam shift with descriptor mode flags. Ordinary tapes use view A; a
	// reserve-unknown Value may use view B. Case descent changes the active
	// mode in native machine state, not in this per-call input.
	RootViewMode uint32 // off 36
	// RootDst must remain unsafe.Pointer so Go assignments preserve pointer and
	// write-barrier semantics. The caller's typed reference remains the lifetime
	// root because the machine backing is noscan. uintptr would erase that GC
	// identity and may preserve a stale address across stack movement.
	RootDst    unsafe.Pointer // off 40
	OptFlags   uint32         // off 48
	AnyTypeIdx uint32         // off 52
	// Polys holds the variant and kindof tables in one array. A field's high 16
	// flag bits index it, and its tag bit selects the interpretation. The array
	// needs no count: the builder is its only writer, so every index it stamps
	// on a field is in range.
	Polys *BindPolyTable // off 56
}

// BindAllocator exposes Go-owned memory to C. C advances cursors and installs
// references, while Go allocates, grows, drains, publishes, and releases the
// backing storage. The layout must match NdecBindAllocator in bind_bridge.h.
type BindAllocator struct {
	SlotClasses *BindSlotClass // off 0

	StrArena    *byte  // off 8
	StrArenaCap uint64 // off 16
	// StrGenStart is the immutable lower bound of strings written by this root.
	StrGenStart uint64 // off 24

	Structural    *uint32 // off 32
	StructuralCap uint32  // off 40
	// TapeNeed is bidirectional. Go supplies a source-size ceiling; the root scan
	// replaces it with the smaller token bound. Go uses the settled word count if
	// BindYieldTapeArena requests growth. It is meaningful only with
	// BindOptSizeTape.
	TapeNeed uint32 // off 44

	// C appends UnmarshalRecord entries; Go drains them before map entries so
	// deferred writes are complete before values are copied into runtime maps.
	DeferredDrain     *byte  // off 48
	DeferredDrainCap  uint32 // off 56
	DeferredDrainUsed uint32 // off 60

	// All map types stage regions in this noscan byte buffer. StrArena owns staged
	// key bytes. Typed SlotClass backings own map and value storage, and the typed
	// destination owns published maps. MapBuf itself retains none of these pointers.
	MapBuf     *byte  // off 64
	MapBufCap  uint32 // off 72
	MapBufUsed uint32 // off 76

	// ValueTape is the base used by the active tape walk. JSON binding points it
	// into TapeArena after an inline carve; tape binding borrows the source
	// ValueDoc tape. Value coordinates remain relative to this base across yields.
	ValueTape *uint64 // off 80

	// ValueDoc mirrors the C field value_doc: a Go-owned *valueabi.Doc. C copies it
	// into installed Values but never constructs or mutates the document. Go
	// keeps a typed local alive until drain and publication because this noscan
	// machine field is not a GC root. It remains unsafe.Pointer so Go stores
	// preserve pointer and barrier semantics; uintptr would erase them.
	ValueDoc unsafe.Pointer // off 88

	// TapeArena backs user-visible Value tapes. C uses unchecked bump writes only
	// after the JSON scan proves the document bound or the tape-input path proves
	// its derived bound. Go resets TapeUsed on entry, publishes ValueDoc before
	// committing the used span, and advances the allocator only after success.
	// Published ValueDoc slice headers retain old backings across later growth.
	TapeArena    *uint64 // off 96
	TapeArenaCap uint64  // off 104
	TapeUsed     uint64  // off 112
}

// BindYield is the native-to-Go control channel. Native publishes the resume
// Phase and input cursor before PendingAction. Go preserves both while it may
// update allocator views and action-specific spill state, then re-enters. For
// errors, Arg0 is the code, Arg1 is error-specific detail, and FirstErrorPos is
// the only source byte position; math.MaxUint32 means unavailable. Target names
// a variant host for variant errors and is nil for other errors.
type BindYield struct {
	PendingAction uint32         // off 0
	Arg0          uint32         // off 4
	Arg1          uint32         // off 8
	FirstErrorPos uint32         // off 12
	Target        unsafe.Pointer // off 16
}

// BindCoreHeader mirrors the scalar prefix of NdecBindCore. JSON entry starts at
// BindPhaseRoot. Native spills the register-resident parse state here before
// every yield. Stash mirrors a phase-tagged C union for site-specific resume
// data; Go must leave it opaque and unscanned.
// CurType is embedded so native can restore a container without dereferencing
// the TypeTree.
//
// Map close leaves entries staged. A flush walks every region in MapBufUsed,
// drains complete entries, and compacts only live regions with an incomplete
// entry. Compaction repairs frame region pointers, parent slots, and destination
// pointers before native resumes. This linear ownership model preserves closed
// sibling maps even after their frame slots are reused.
type BindCoreHeader struct {
	Phase          uint32   // off 0
	Depth          int32    // off 4
	CurType        BindType // off 8
	CurDst         *byte    // off 24
	CurCount       uint32   // off 32
	FirstErrorKind uint32   // off 36
	// StrUsed is the next-free byte offset in StrArena, spilled across yields.
	StrUsed uint64 // off 40
	// Atof is a non-owning address into parser-owned scratch. The Parser retains
	// that backing for the machine lifetime.
	Atof  uintptr  // off 48
	Stash [16]byte // off 56
	// CurAux is a kind-tagged native address: map region, slice or stream write
	// cursor, or struct lookup. Frames preserve it across descent. It is uintptr
	// because it is non-owning and intentionally unscanned. Reachability must come
	// from the typed allocator, TypeTree, destination, or retained backing that
	// owns the referenced storage.
	CurAux uintptr // off 72
}

// UnmarshalRecord defers Go callbacks and RawMessage publication. Target must
// address scannable storage because the drain may publish heap pointers there.
// Arg0 and Arg1 identify an immutable Src span for JSON and RawMessage, or a
// StrArena span for TextUnmarshaler. Both backing stores remain valid until the
// Go drain consumes the record.
type UnmarshalRecord struct {
	Target  *byte   // off 0
	TypeIdx uint32  // off 8
	Kind    uint8   // off 12
	_pad    [3]byte // off 13
	Arg0    uint32  // off 16
	Arg1    uint32  // off 20
}

// UnmarshalRecordSize is shared with native deferred-drain cursor arithmetic.
const UnmarshalRecordSize = 24

// BindFrame must match the native container frame byte for byte. A frame saves
// the parent before descent. U is interpreted by Kind: map region pointer,
// slice or stream write cursor, struct lookup pointer, or array index. Slice and
// stream counts live in their Go headers; map counts live in the region.
//
// Child is uintptr because the frame does not own the descriptor. The TypeTree
// keeps the referenced BindType or BindField reachable for the parse lifetime.
type BindFrame struct {
	Dst       unsafe.Pointer // off 0
	Kind      uint8          // off 8
	Flags     uint8          // off 9
	TypeIdx   uint16         // off 10
	ChildSize uint32         // off 12
	Child     uintptr        // off 16
	U         [8]byte        // off 24
}

// FrameCounter reads U as the array index. Calling it for another Kind is invalid.
func (f *BindFrame) FrameCounter() uint32 {
	return uint32(f.U[0]) | uint32(f.U[1])<<8 | uint32(f.U[2])<<16 | uint32(f.U[3])<<24
}

// SetFrameCounter initializes U for a fabricated array frame. Native owns live
// frame writes; tests use this helper and require the unused bytes to be zero.
func (f *BindFrame) SetFrameCounter(v uint32) {
	f.U[0] = byte(v)
	f.U[1] = byte(v >> 8)
	f.U[2] = byte(v >> 16)
	f.U[3] = byte(v >> 24)
	f.U[4] = 0
	f.U[5] = 0
	f.U[6] = 0
	f.U[7] = 0
}

// FrameMapRegion reads U as a map region pointer. Calling it for another Kind
// is invalid. The Go drain uses it to find and relocate live regions.
func (f *BindFrame) FrameMapRegion() *BindMapRegionHeader {
	return *(**BindMapRegionHeader)(unsafe.Pointer(&f.U[0]))
}

// SetFrameMapRegion publishes a region's relocated address before native resumes.
func (f *BindFrame) SetFrameMapRegion(r *BindMapRegionHeader) {
	*(**BindMapRegionHeader)(unsafe.Pointer(&f.U[0])) = r
}

// BindMapRegionHeader precedes one map's staged entries in MapBuf. Hmap is the
// drain destination. ParentSlot identifies where native published that map and
// must be repaired if a containing incomplete entry moves during compaction.
// NextEntryOff may include one reserved but incomplete entry beyond EntryCount.
type BindMapRegionHeader struct {
	Stride       uint32         // off 0
	NextEntryOff uint32         // off 4
	EntryCount   uint32         // off 8
	TypeIdx      uint32         // off 12
	Hmap         unsafe.Pointer // off 16
	ParentSlot   unsafe.Pointer // off 24
}

// FramesBase addresses NdecBindCore.frames, which immediately follows the Go
// mirror of the scalar prefix.
func FramesBase(m *BindMachine) *BindFrame {
	return (*BindFrame)(unsafe.Add(unsafe.Pointer(m), unsafe.Sizeof(BindMachine{})))
}

// Yield actions must match BIND_YIELD_* in bind_bridge.h. Native sets a resume
// phase before yielding. Go services one action, preserves Phase and the input
// cursor, and may update allocator or action-specific spill state before reentry.
const (
	BindYieldNone  uint32 = 0
	BindYieldError uint32 = 1
	// Arg0 is the SlotClass index. Arg1 is reserved. Target is the active destination.
	BindYieldBlockFull uint32 = 2
	// Arg0 is the slice type index. Arg1 is the stream element index or zero.
	// Target is the slice or Stream header.
	BindYieldSliceGrow uint32 = 3
	// Arg0 is the slice type index. Arg1 is the row index. Target is the slice header.
	BindYieldRecBatchRefill uint32 = 4
	// Arg0 is the slice type index. Arg1 is the requested capacity. Target is the slice header.
	BindYieldRecBatchBypass uint32 = 5
	BindYieldFlushMap       uint32 = 6
	BindYieldFlushUnmarshal uint32 = 7
	// BindYieldTapeBindValue asks Go to install a Value that aliases the source
	// tape. Arg0 is the root word offset, Arg1 is the subtree extent, and Stash
	// identifies the destination. The phase selects the exact native continuation.
	BindYieldTapeBindValue uint32 = 10
	// BindYieldTapeArena occurs only after the root scan and before the first tape
	// write. Go may therefore grow to TapeNeed without invalidating any live tape
	// index, then re-enter at the scanned-root phase.
	BindYieldTapeArena uint32 = 11
)

// Resume phases must match BIND_PHASE_* in bind_bridge.h. Go selects the Root
// entry for JSON input and TapeBindRoot for tape input. Native owns every
// continuation phase written before a yield. Each continuation records the
// cursor effects already completed, so reentry must not replay the dispatch site.
const (
	BindPhaseRoot                uint32 = 0
	BindPhaseArrayValue          uint32 = 2
	BindPhaseObjectFieldValue    uint32 = 3
	BindPhaseMapContinue         uint32 = 4
	BindPhaseMapOpenRetry        uint32 = 5
	BindPhaseMapValue            uint32 = 6
	BindPhaseDocumentEnd         uint32 = 7
	BindPhaseAnyResume           uint32 = 8
	BindPhaseUnmarshalResume     uint32 = 9
	BindPhaseRootUnwrap          uint32 = 10
	BindPhaseValueResume         uint32 = 11
	BindPhaseArrayClose          uint32 = 12
	BindPhaseVariantRebindResume uint32 = 13
	BindPhaseVariantInlineResume uint32 = 14
	BindPhaseKindofInlineResume  uint32 = 15

	// BindPhaseTapeBindRoot tells the native wrapper to seed a tape walk from the
	// borrowed cursor pair and RootViewMode. Subsequent tape phases are native
	// continuations and must survive every yield and nested case descent.
	BindPhaseTapeBindRoot uint32 = 16

	// BindPhaseArrayValueBegin resumes a stream element after its slot and count
	// are committed but before its body binds, allowing Go to register nested
	// stream handlers at that boundary.
	BindPhaseArrayValueBegin uint32 = 32
)

// Error codes must match BIND_ERR_*. Yield.Arg1 carries error-specific detail;
// Yield.FirstErrorPos alone carries an optional source byte position.
const (
	BindErrSyntax             uint32 = 1
	BindErrEOF                uint32 = 2
	BindErrDepth              uint32 = 3
	BindErrUTF8               uint32 = 4
	BindErrTrailing           uint32 = 5
	BindErrTypeMismatch       uint32 = 32
	BindErrUnknownField       uint32 = 33
	BindErrFixedOverflow      uint32 = 34
	BindErrUnsupportedTag     uint32 = 35
	BindErrVariantUnknownDisc uint32 = 36
	BindErrVariantMissingDisc uint32 = 37
	BindErrKindofUnregistered uint32 = 38
)

// BindMachineSize is the shared allocation limit for the complete native
// machine, including frames and C-private auxiliary state. The C entry point
// asserts sizeof(NdecBindMachine) does not exceed it. Atof storage is allocated
// separately by Go and referenced through Core.Atof.
const (
	BindMachineSize = 16384
	BindScanPad     = 64 // 0x20 sentinel padding past src
	BindMaxDepth    = 255

	// These constants define the map-buffer byte layout shared with C. The value
	// follows the Go string key header, and each region reserves a fixed number of
	// entry slots so both sides compute identical region boundaries.
	BindMapRegionHeaderSize = 32
	BindMapValOff           = 16
	BindMapRegionSlots      = 16
)

// BindMachineCursorOffset locates NdecBindMachine.idx_p immediately after the
// native frame array. JSON binding owns this structural-index cursor. Tape
// binding type-puns the same pair as the borrowed tape range, so Go writes both
// words only when seeding BindPhaseTapeBindRoot.
const BindMachineCursorOffset = unsafe.Sizeof(BindMachine{}) + unsafe.Sizeof(BindFrame{})*(BindMaxDepth+1)

// Option bits must match BIND_OPT_* in bind_bridge.h.
const (
	BindOptDisallowUnknown uint32 = 1 << 0
	// BindOptUseNumber decodes any/interface{} numbers as json.Number instead
	// of float64. The number text is copied into str_arena and the eface is
	// tagged with BindAnyMeta.NumberType.
	BindOptUseNumber uint32 = 1 << 2
	// BindOptSizeTape selects the root scan that computes TapeNeed. Set it only
	// when the destination may write a Value or merged tape.
	BindOptSizeTape uint32 = 1 << 3
	// BindOptTapeDual adds the per-tape dual-view prologue to the scan bound.
	BindOptTapeDual uint32 = 1 << 4
	// BindOptStrictScan validates raw UTF-8 and rejects unescaped C0 bytes
	// during the native root scan.
	BindOptStrictScan  uint32 = 1 << 5
	BindOptSkipLenient uint32 = 1 << 6
)

// Type flag bits must match BIND_FLAG_* in bind_bridge.h. StreamSkip exists
// only on the active CurType copy and is preserved by native frames; it is never
// stored in the immutable TypeTree.
const (
	BindFlagStreamSkip uint8 = 1 << 1

	// BindFlagElemHasStream inserts the per-element registration boundary needed
	// before a nested stream body binds.
	BindFlagElemHasStream uint8 = 1 << 2
)

// These assertions pin the Go half of the ABI. C independently asserts the
// mirrored sizes and offsets in machine.h and bind_bridge.h, while the entry
// point checks the complete native machine against BindMachineSize.

var (
	_ = [1]struct{}{}[unsafe.Sizeof(BindType{})-16]
	_ = [1]struct{}{}[unsafe.Sizeof(BindField{})-16]
	_ = [1]struct{}{}[unsafe.Sizeof(BindSlotClass{})-48]
	_ = [1]struct{}{}[unsafe.Sizeof(BindContext{})-64]
	_ = [1]struct{}{}[unsafe.Sizeof(BindAllocator{})-120]
	_ = [1]struct{}{}[unsafe.Sizeof(BindYield{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(BindMachine{})-288]
	_ = [1]struct{}{}[unsafe.Sizeof(BindCoreHeader{})-80]
	_ = [1]struct{}{}[unsafe.Sizeof(BindFrame{})-32]
	_ = [1]struct{}{}[unsafe.Sizeof(BindMapRegionHeader{})-32]
	_ = [1]struct{}{}[unsafe.Sizeof(UnmarshalRecord{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(BindPolyTable{})-40]
	// Size assertions cannot detect TapeNeed moving within padding, so both
	// languages assert this offset explicitly.
	_ = [1]struct{}{}[unsafe.Offsetof(BindAllocator{}.TapeNeed)-44]

	// DOM mirror. NdecDomContext in entry/ndec.c asserts the same offsets.
	_ = [1]struct{}{}[unsafe.Sizeof(DOMContext{})-112]
	_ = [1]struct{}{}[unsafe.Offsetof(DOMContext{}.NStructural)-100]
	_ = [1]struct{}{}[unsafe.Offsetof(DOMContext{}.TapeNeed)-104]
	_ = [1]struct{}{}[unsafe.Offsetof(DOMContext{}.ScanStrict)-108]
)
