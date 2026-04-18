// Compile-time and init-time verification that Go struct layouts match the
// C-side ABI.
//
// Compile-time asserts use const + array subscript to catch size mismatches
// at build time. Critical field offsets are verified in init using
// unsafe.Offsetof; C-side offsets are annotated as "off NN" in the native
// header and verified one-to-one here.
package ndec

import "unsafe"

const (
	_ uintptr = 64 - unsafe.Sizeof(fieldLookupABI{})
	_ uintptr = unsafe.Sizeof(fieldLookupABI{}) - 64

	_ uintptr = 24 - unsafe.Sizeof(bindFieldInfo{})
	_ uintptr = unsafe.Sizeof(bindFieldInfo{}) - 24

	_ uintptr = 64 - unsafe.Sizeof(bindTypeInfo{})
	_ uintptr = unsafe.Sizeof(bindTypeInfo{}) - 64

	_ uintptr = 96 - unsafe.Sizeof(bindUserData{})
	_ uintptr = unsafe.Sizeof(bindUserData{}) - 96
)

func init() {
	must := func(name string, got, want uintptr) {
		if got != want {
			panic("ndec ABI offset mismatch: " + name)
		}
	}

	// fieldLookupABI
	must("fieldLookupABI.Kind", unsafe.Offsetof(fieldLookupABI{}.Kind), 0)
	must("fieldLookupABI.MaxKeyLen", unsafe.Offsetof(fieldLookupABI{}.MaxKeyLen), 4)
	must("fieldLookupABI.Bitmap", unsafe.Offsetof(fieldLookupABI{}.Bitmap), 8)
	must("fieldLookupABI.HashSeed", unsafe.Offsetof(fieldLookupABI{}.HashSeed), 24)
	must("fieldLookupABI.PerfectTable", unsafe.Offsetof(fieldLookupABI{}.PerfectTable), 40)

	must("bindFieldInfo.Kind", unsafe.Offsetof(bindFieldInfo{}.Kind), 0)
	must("bindFieldInfo.NameLen", unsafe.Offsetof(bindFieldInfo{}.NameLen), 2)
	must("bindFieldInfo.Offset", unsafe.Offsetof(bindFieldInfo{}.Offset), 4)
	must("bindFieldInfo.Name", unsafe.Offsetof(bindFieldInfo{}.Name), 8)
	must("bindFieldInfo.Type", unsafe.Offsetof(bindFieldInfo{}.Type), 16)

	must("bindTypeInfo.Kind", unsafe.Offsetof(bindTypeInfo{}.Kind), 0)
	must("bindTypeInfo.FieldCount", unsafe.Offsetof(bindTypeInfo{}.FieldCount), 2)
	must("bindTypeInfo.Size", unsafe.Offsetof(bindTypeInfo{}.Size), 4)
	must("bindTypeInfo.ElemKind", unsafe.Offsetof(bindTypeInfo{}.ElemKind), 8)
	must("bindTypeInfo.ElemSize", unsafe.Offsetof(bindTypeInfo{}.ElemSize), 12)
	must("bindTypeInfo.FixedCount", unsafe.Offsetof(bindTypeInfo{}.FixedCount), 16)
	must("bindTypeInfo.Fields", unsafe.Offsetof(bindTypeInfo{}.Fields), 24)
	must("bindTypeInfo.Lookup", unsafe.Offsetof(bindTypeInfo{}.Lookup), 32)
	must("bindTypeInfo.ElemType", unsafe.Offsetof(bindTypeInfo{}.ElemType), 40)
	must("bindTypeInfo.EmptySliceData", unsafe.Offsetof(bindTypeInfo{}.EmptySliceData), 48)
	must("bindTypeInfo.CapHint", unsafe.Offsetof(bindTypeInfo{}.CapHint), 56)

	// ndecFrame still overlays STRUCT and SLICE state in the shared 8 byte union
	// slot at offsets 24 through 31.
	must("ndecFrame.Phase", unsafe.Offsetof(ndecFrame{}.Phase), 0)
	must("ndecFrame.Data", unsafe.Offsetof(ndecFrame{}.Data), 4)
	must("ndecFrame.BindType", unsafe.Offsetof(ndecFrame{}.BindType), 8)
	must("ndecFrame.BindDst", unsafe.Offsetof(ndecFrame{}.BindDst), 16)
	must("ndecFrame.BindPendingFieldIdx", unsafe.Offsetof(ndecFrame{}.BindPendingFieldIdx), 24)
	must("ndecFrame.BindArrayCap", unsafe.Offsetof(ndecFrame{}.BindArrayCap), 28)
	must("ndecFrame.BindContainerKind", unsafe.Offsetof(ndecFrame{}.BindContainerKind), 32)
	must("ndecFrame.BindSliceHdr", unsafe.Offsetof(ndecFrame{}.BindSliceHdr), 40)

	must("bindUserData.PendingAction", unsafe.Offsetof(bindUserData{}.PendingAction), 0)
	must("bindUserData.PendingFieldIdx", unsafe.Offsetof(bindUserData{}.PendingFieldIdx), 4)
	must("bindUserData.RawPtr", unsafe.Offsetof(bindUserData{}.RawPtr), 8)
	must("bindUserData.RawLen", unsafe.Offsetof(bindUserData{}.RawLen), 16)
	must("bindUserData.YieldFlags", unsafe.Offsetof(bindUserData{}.YieldFlags), 20)
	must("bindUserData.OptFlags", unsafe.Offsetof(bindUserData{}.OptFlags), 24)
	must("bindUserData.ScratchPtr", unsafe.Offsetof(bindUserData{}.ScratchPtr), 32)
	must("bindUserData.ScratchCap", unsafe.Offsetof(bindUserData{}.ScratchCap), 40)
	must("bindUserData.ScratchLen", unsafe.Offsetof(bindUserData{}.ScratchLen), 44)
	must("bindUserData.AtofCtx", unsafe.Offsetof(bindUserData{}.AtofCtx), 48)
	must("bindUserData.BufEnd", unsafe.Offsetof(bindUserData{}.BufEnd), 56)
	must("bindUserData.KvBufBase", unsafe.Offsetof(bindUserData{}.KvBufBase), 64)
	must("bindUserData.KvBufLen", unsafe.Offsetof(bindUserData{}.KvBufLen), 72)
	must("bindUserData.KvBufCap", unsafe.Offsetof(bindUserData{}.KvBufCap), 76)
}
