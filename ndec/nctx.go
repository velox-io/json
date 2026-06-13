// ndecCtx and its helper structs are Go-side ABI shims for the native parser.
// Their field order and size must stay byte-for-byte aligned with the C
// definitions because the driver passes them across the language boundary.

package ndec

import "unsafe"

// ndecMaxDepth must match the native parser so the embedded frame array stays
// the same size on both sides of the ABI.
const ndecMaxDepth = 256

// ndecScanState exists only to preserve the native parser layout inside
// ndecCtx. Go code does not interpret these fields directly.
type ndecScanState struct {
	PrevInString       uint64 // off  0
	PrevEscape         uint64 // off  8
	PrevStructuralOrWs uint64 // off 16
	LastBackslash      uint64 // off 24
	IsFinal            uint32 // off 32
	_                  uint32 // off 36
}

// ndecFrame must preserve the native frame layout while also exposing the
// Go binding fields injected by NDEC_FRAME_EXTRA_FIELDS.
//
// The native parser overlays STRUCT, SLICE, and MAP state onto the same 8 byte
// region:
//
//	STRUCT: as.struct_.pending_field_idx (int32, off 24) + 4B pad (off 28)
//	SLICE:  as.slice_.array_index (uint32, off 24) + array_cap (uint32, off 28)
//	MAP:    as.map_.kv_count (uint32, off 24) + kv_buf_cap (uint32, off 28)
//
// Go has no union, so BindPendingFieldIdx / BindArrayCap express both views.
// The 4B at off 24 exposes array_index / kv_count via uint32 reinterpret;
// BindArrayCap at off 28 doubles as SLICE.array_cap and MAP.kv_buf_cap.
// Callers select the correct view based on BindContainerKind.
type ndecFrame struct {
	Phase               uint32         // off  0
	Data                uint32         // off  4
	BindType            unsafe.Pointer // off  8 *bindTypeInfo
	BindDst             unsafe.Pointer // off 16 SLICE: backing; MAP: map header (*hmap/*swissmap); STRUCT: struct base
	BindPendingFieldIdx int32          // off 24 (STRUCT); also SLICE.array_index / MAP.kv_count (uint32 reinterpret)
	BindArrayCap        uint32         // off 28 SLICE.array_cap or MAP.kv_buf_cap; STRUCT does not read this
	BindContainerKind   uint8          // off 32
	_                   [3]byte        // off 33  C-side _bind_pad[3]
	// ParentFieldIdx stores the parent STRUCT's pending_field_idx when a
	// begin_array / begin_map creates a child SLICE/MAP frame. The parent's
	// pending field is cleared immediately after push, but the error path
	// still needs it to reconstruct the full nested path. STRUCT frames do
	// not read this. Shares an 8-byte slot with BindContainerKind at off 32
	// (1B kind + 3B pad + 4B idx), preserving the overall frame size.
	ParentFieldIdx int32          // off 36 (C-side parent_field_idx)
	BindSliceHdr   unsafe.Pointer // off 40 SLICE: *goSliceHeader; MAP: kvBuf sub-region start
}

// SLICE.array_index shares offset 24 with the STRUCT pending field in the
// native union, so these helpers expose the same bytes as uint32.
func (f *ndecFrame) bindArrayIndex() uint32 {
	return *(*uint32)(unsafe.Pointer(&f.BindPendingFieldIdx))
}

func (f *ndecFrame) setBindArrayIndex(v uint32) {
	*(*uint32)(unsafe.Pointer(&f.BindPendingFieldIdx)) = v
}

// MAP.kv_count shares offset 24 with the same native union slot, so these
// helpers expose that storage as uint32 for MAP frames only.
func (f *ndecFrame) bindKvCount() uint32 {
	return *(*uint32)(unsafe.Pointer(&f.BindPendingFieldIdx))
}

func (f *ndecFrame) setBindKvCount(v uint32) {
	*(*uint32)(unsafe.Pointer(&f.BindPendingFieldIdx)) = v
}

type ndecCtx struct {
	Buf            unsafe.Pointer          // off  0 *uint8
	BufEnd         uintptr                 // off  8 const uint8_t*; uses uintptr to avoid GC scanning end sentinel
	Reactor        unsafe.Pointer          // off 16 *NdecReactor; driver always writes nil
	UserData       unsafe.Pointer          // off 24 *bindUserData
	CurPos         uintptr                 // off 32 const uint8_t*; may be first-unconsumed or sentinel; must not enter GC trace
	ChunkPtr       uintptr                 // off 40 const uint8_t*; may be clamped to buf_end; must not enter GC trace
	StructuralBits uint64                  // off 48
	ScanState      ndecScanState           // off 56  (40 bytes)
	ExitCode       uint32                  // off 96
	ErrorPos       uint32                  // off 100
	Sp             int32                   // off 104  stack top index (-1 unarmed, 0+ armed)
	_              uint32                  // off 108  pad to 8-byte align frames
	Frames         [ndecMaxDepth]ndecFrame // off 112; sizeof = 256*40 = 10240
}

// These assertions fail the build as soon as any Go-side layout drifts from the
// native sizes that the parser expects.
const (
	_ uintptr = 12400 - unsafe.Sizeof(ndecCtx{})
	_ uintptr = unsafe.Sizeof(ndecCtx{}) - 12400
	_ uintptr = 40 - unsafe.Sizeof(ndecScanState{})
	_ uintptr = unsafe.Sizeof(ndecScanState{}) - 40
	_ uintptr = 48 - unsafe.Sizeof(ndecFrame{})
	_ uintptr = unsafe.Sizeof(ndecFrame{}) - 48
)

func init() {
	must := func(name string, got, want uintptr) {
		if got != want {
			panic("ndec NdecCtx layout mismatch: " + name)
		}
	}
	must("ndecCtx.Buf", unsafe.Offsetof(ndecCtx{}.Buf), 0)
	must("ndecCtx.BufEnd", unsafe.Offsetof(ndecCtx{}.BufEnd), 8)
	must("ndecCtx.Reactor", unsafe.Offsetof(ndecCtx{}.Reactor), 16)
	must("ndecCtx.UserData", unsafe.Offsetof(ndecCtx{}.UserData), 24)
	must("ndecCtx.CurPos", unsafe.Offsetof(ndecCtx{}.CurPos), 32)
	must("ndecCtx.ChunkPtr", unsafe.Offsetof(ndecCtx{}.ChunkPtr), 40)
	must("ndecCtx.StructuralBits", unsafe.Offsetof(ndecCtx{}.StructuralBits), 48)
	must("ndecCtx.ScanState", unsafe.Offsetof(ndecCtx{}.ScanState), 56)
	must("ndecCtx.ExitCode", unsafe.Offsetof(ndecCtx{}.ExitCode), 96)
	must("ndecCtx.ErrorPos", unsafe.Offsetof(ndecCtx{}.ErrorPos), 100)
	must("ndecCtx.Sp", unsafe.Offsetof(ndecCtx{}.Sp), 104)
	must("ndecCtx.Frames", unsafe.Offsetof(ndecCtx{}.Frames), 112)
}

const (
	exitOK          uint32 = 0
	exitSuspend     uint32 = 1
	exitErrSyntax   uint32 = 2
	exitErrEOF      uint32 = 3
	exitErrDepth    uint32 = 4
	exitErrKeyword  uint32 = 5
	exitErrTrailing uint32 = 6
)
