package ndec

import "unsafe"

type yieldAction uint32

const (
	yaNone               yieldAction = 0
	yaBeginPtr           yieldAction = 1
	yaGrowSlice          yieldAction = 2
	yaTypeMismatch       yieldAction = 3
	yaUnknownField       yieldAction = 4
	yaGrowSliceStruct    yieldAction = 5
	yaBeginMap           yieldAction = 6
	yaFlushMap           yieldAction = 7
	yaBeginPtrMapValue   yieldAction = 8
	yaBase64Slice        yieldAction = 9
	yaGrowSlicePtrStruct yieldAction = 10
)

// bindKind values must stay ABI aligned with the native NdecBindKind enum
// because typeinfo serialization passes them across the Go/C boundary.
type bindKind uint8

const (
	bkInvalid    bindKind = 0
	bkBool       bindKind = 1
	bkInt        bindKind = 2
	bkInt8       bindKind = 3
	bkInt16      bindKind = 4
	bkInt32      bindKind = 5
	bkInt64      bindKind = 6
	bkUint       bindKind = 7
	bkUint8      bindKind = 8
	bkUint16     bindKind = 9
	bkUint32     bindKind = 10
	bkUint64     bindKind = 11
	bkFloat32    bindKind = 12
	bkFloat64    bindKind = 13
	bkString     bindKind = 14
	bkStruct     bindKind = 15
	bkFixedArray bindKind = 16
	bkSlice      bindKind = 17
	bkPtr        bindKind = 18
	bkAny        bindKind = 19
	bkMap        bindKind = 20
	bkIface      bindKind = 21
	bkRawMessage bindKind = 22
	bkNumber     bindKind = 23
)

// bff* flags must stay aligned with typ.TagFlag because field metadata is
// copied directly into the native binding tables.
const (
	bffQuoted     uint8 = 1 << 0
	bffOmitEmpty  uint8 = 1 << 1
	bffCopyString uint8 = 1 << 2
)

const (
	yfNone              uint8 = 0
	yfGrowNull          uint8 = 1
	yfMapValuePtrStruct uint8 = 2
	yfPtrToSlice        uint8 = 0x10
	yfPtrToMap          uint8 = 0x20
	yfGrowAllocPtr      uint8 = 0x40
	yfPtrToStruct       uint8 = 0xFF

	yfTokenNull   uint8 = 1
	yfTokenBool   uint8 = 2
	yfTokenNumber uint8 = 3
	yfTokenString uint8 = 4
	yfTokenObject uint8 = 5
	yfTokenArray  uint8 = 6
)

type lookupKind uint8

const (
	flkEmpty   lookupKind = 0
	flkBitmap8 lookupKind = 1
	flkPerfect lookupKind = 2
	flkMap     lookupKind = 3
)

type fieldLookupABI struct {
	Kind         uint8   // off  0
	HasMixedCase uint8   // off  1
	_            [2]byte // off  2
	MaxKeyLen    uint8   // off  4
	_            [3]byte // off  5

	Bitmap  unsafe.Pointer // off  8 *uint8
	LenMask unsafe.Pointer // off 16 *uint8

	HashSeed      uint64 // off 24
	HashShift     uint8  // off 32
	HashMixer     uint8  // off 33
	TableSizeLog2 uint16 // off 34
	_             uint32 // off 36

	PerfectTable unsafe.Pointer // off 40

	EntryCount uint32         // off 48
	_          uint32         // off 52
	MapEntries unsafe.Pointer // off 56
}

type bindFieldInfo struct {
	Kind     uint8          // off  0
	TagFlags uint8          // off  1
	NameLen  uint16         // off  2
	Offset   uint32         // off  4
	Name     unsafe.Pointer // off  8 *uint8
	Type     unsafe.Pointer // off 16 *bindTypeInfo
}

type bindTypeInfo struct {
	Kind       uint8   // off  0
	TypeFlags  uint8   // off  1
	FieldCount uint16  // off  2
	Size       uint32  // off  4
	ElemKind   uint8   // off  8
	_          [3]byte // off  9
	ElemSize   uint32  // off 12
	FixedCount uint32  // off 16
	_          uint32  // off 20

	Fields   unsafe.Pointer // off 24 *bindFieldInfo
	Lookup   unsafe.Pointer // off 32 *fieldLookupABI
	ElemType unsafe.Pointer // off 40 *bindTypeInfo

	// EmptySliceData (SLICE only): zerobase data pointer from
	// reflect.MakeSlice(t, 0, 0). begin_array writes the header as
	// (EmptySliceData, 0, 0) as the lazy-allocation starting point.
	EmptySliceData unsafe.Pointer // off 48

	// CapHint (SLICE only): EMA-adaptive initial capacity.
	CapHint int32 // off 56
	_       int32 // off 60
}

// User data fields populated by the driver and passed to the native parser
// via NdecCtx.user_data. Carries non-frame state: yield channel, scratch
// buffer, decode options, atof reuse slot, buf_end (for number padded path).
type bindUserData struct {
	PendingAction   uint32 // off  0 yieldAction
	PendingFieldIdx uint32 // off  4

	RawPtr     unsafe.Pointer // off  8 *uint8
	RawLen     uint32         // off 16
	YieldFlags uint8          // off 20 NDEC_YF_*
	_          [3]byte        // off 21

	OptFlags uint32 // off 24
	_        uint32 // off 28

	ScratchPtr unsafe.Pointer // off 32 *uint8
	ScratchCap uint32         // off 40
	ScratchLen uint32         // off 44

	// AtofCtx points to the start of driverState.atofCtx.
	AtofCtx unsafe.Pointer // off 48

	// BufEnd stores the input end address (input + len) for the current
	// Unmarshal call.
	BufEnd uintptr // off 56

	// KvBufBase / KvBufLen / KvBufCap: BEGIN_MAP fast path bypass.
	KvBufBase unsafe.Pointer // off 64
	KvBufLen  uint32         // off 72
	KvBufCap  uint32         // off 76

	_ [16]byte // off 80 padding to 96
}
