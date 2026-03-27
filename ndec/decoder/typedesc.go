// ndec typedesc flattens vdec descriptors into native repr(C) layouts.
package decoder

import (
	"sync"
	"unsafe"

	"github.com/velox-io/json/typ"
	"github.com/velox-io/json/vdec"
)

// decTypeDesc owns the flat buffers read by native code.
type decTypeDesc struct {
	root unsafe.Pointer
	// Buffers stay referenced so raw native pointers remain valid.
	buffers [][]byte
}

var decTypeDescCache sync.Map // *vdec.DecTypeInfo → *decTypeDesc

// getOrCompileDecTypeDesc returns the native descriptor root for container types.
func getOrCompileDecTypeDesc(ti *vdec.DecTypeInfo) unsafe.Pointer {
	switch ti.Kind {
	case typ.KindStruct, typ.KindSlice, typ.KindMap, typ.KindPointer, typ.KindArray:
	default:
		return nil
	}

	if v, ok := decTypeDescCache.Load(ti); ok {
		return v.(*decTypeDesc).root
	}

	desc := &decTypeDesc{}
	desc.root = compileDecTypeDescRec(desc, ti)

	actual, loaded := decTypeDescCache.LoadOrStore(ti, desc)
	if loaded {
		return actual.(*decTypeDesc).root
	}
	return desc.root
}

const (
	decStructDescSize  = 16
	decFieldDescSize   = 24
	decSliceDescSize   = 32
	decMapDescSize     = 32
	decArrayDescSize   = 24
	decPointerDescSize = 24
)

// compileDecTypeDescRec recursively lowers a DecTypeInfo tree into flat buffers.
func compileDecTypeDescRec(desc *decTypeDesc, ti *vdec.DecTypeInfo) unsafe.Pointer {
	switch ti.Kind {
	case typ.KindStruct:
		return compileStructDesc(desc, ti)
	case typ.KindSlice:
		return compileSliceDesc(desc, ti)
	case typ.KindMap:
		return compileMapDesc(desc, ti)
	case typ.KindPointer:
		return compilePointerDesc(desc, ti)
	case typ.KindArray:
		return compileArrayDesc(desc, ti)
	default:
		return nil
	}
}

// compileStructDesc lowers a struct descriptor header plus field table.
func compileStructDesc(desc *decTypeDesc, ti *vdec.DecTypeInfo) unsafe.Pointer {
	si := ti.ResolveStruct()
	if si == nil {
		return nil
	}
	nFields := len(si.Fields)

	bufSize := decStructDescSize + nFields*decFieldDescSize
	buf := make([]byte, bufSize)
	desc.buffers = append(desc.buffers, buf)

	buf[0] = uint8(typ.KindStruct)
	buf[1] = 0
	*(*uint32)(unsafe.Pointer(&buf[4])) = uint32(ti.Size)
	*(*uint16)(unsafe.Pointer(&buf[8])) = uint16(nFields)

	for i := range si.Fields {
		off := decStructDescSize + i*decFieldDescSize
		compileFieldDesc(desc, buf[off:off+decFieldDescSize], &si.Fields[i])
	}

	return unsafe.Pointer(&buf[0])
}

// compileFieldDesc lowers one field entry.
func compileFieldDesc(desc *decTypeDesc, dst []byte, fi *vdec.DecFieldInfo) {
	// Go strings are non-moving, so native code can read their data directly.
	nameBytes := fi.JSONName
	if len(nameBytes) > 0 {
		*(*uintptr)(unsafe.Pointer(&dst[0])) = (*[2]uintptr)(unsafe.Pointer(&nameBytes))[0]
	} else {
		*(*uintptr)(unsafe.Pointer(&dst[0])) = 0
	}

	*(*uint16)(unsafe.Pointer(&dst[8])) = uint16(len(nameBytes))
	*(*uint16)(unsafe.Pointer(&dst[10])) = uint16(fi.Offset)
	dst[12] = uint8(fi.Kind)
	dst[13] = 0
	*(*uint16)(unsafe.Pointer(&dst[14])) = 0

	var childPtr unsafe.Pointer
	switch fi.Kind {
	case typ.KindStruct, typ.KindSlice, typ.KindMap, typ.KindPointer, typ.KindArray:
		childPtr = compileDecTypeDescRec(desc, fi.TypeInfo)
	}
	*(*uintptr)(unsafe.Pointer(&dst[16])) = uintptr(childPtr)
}

// compileSliceDesc lowers a slice descriptor.
func compileSliceDesc(desc *decTypeDesc, ti *vdec.DecTypeInfo) unsafe.Pointer {
	si := ti.ResolveSlice()
	if si == nil {
		return nil
	}

	buf := make([]byte, decSliceDescSize)
	desc.buffers = append(desc.buffers, buf)

	buf[0] = uint8(typ.KindSlice)
	buf[1] = uint8(si.ElemTI.Kind)
	if si.ElemHasPtr {
		buf[2] = 1
	}
	*(*uint32)(unsafe.Pointer(&buf[4])) = uint32(si.ElemSize)

	var elemDescPtr unsafe.Pointer
	switch si.ElemTI.Kind {
	case typ.KindStruct, typ.KindSlice, typ.KindMap, typ.KindPointer, typ.KindArray:
		elemDescPtr = compileDecTypeDescRec(desc, si.ElemTI)
	}
	*(*uintptr)(unsafe.Pointer(&buf[8])) = uintptr(elemDescPtr)

	// Native yield handlers bounce back through the Go slice descriptor.
	*(*uintptr)(unsafe.Pointer(&buf[16])) = uintptr(unsafe.Pointer(si))

	*(*uintptr)(unsafe.Pointer(&buf[24])) = uintptr(si.ElemRType)

	return unsafe.Pointer(&buf[0])
}

// compileMapDesc lowers a map descriptor.
func compileMapDesc(desc *decTypeDesc, ti *vdec.DecTypeInfo) unsafe.Pointer {
	mi := ti.ResolveMap()
	if mi == nil {
		return nil
	}

	buf := make([]byte, decMapDescSize)
	desc.buffers = append(desc.buffers, buf)

	buf[0] = uint8(typ.KindMap)
	buf[1] = uint8(mi.ValTI.Kind)
	if mi.ValHasPtr {
		buf[2] = 1
	}
	*(*uint32)(unsafe.Pointer(&buf[4])) = uint32(mi.ValSize)

	var valDescPtr unsafe.Pointer
	switch mi.ValTI.Kind {
	case typ.KindStruct, typ.KindSlice, typ.KindMap, typ.KindPointer, typ.KindArray:
		valDescPtr = compileDecTypeDescRec(desc, mi.ValTI)
	}
	*(*uintptr)(unsafe.Pointer(&buf[8])) = uintptr(valDescPtr)

	// Native yield handlers bounce back through the Go map descriptor.
	*(*uintptr)(unsafe.Pointer(&buf[16])) = uintptr(unsafe.Pointer(ti))

	*(*uintptr)(unsafe.Pointer(&buf[24])) = uintptr(ti.TypePtr)

	return unsafe.Pointer(&buf[0])
}

// compilePointerDesc lowers a pointer descriptor.
func compilePointerDesc(desc *decTypeDesc, ti *vdec.DecTypeInfo) unsafe.Pointer {
	pi := ti.ResolvePointer()
	if pi == nil {
		return nil
	}

	buf := make([]byte, decPointerDescSize)
	desc.buffers = append(desc.buffers, buf)

	buf[0] = uint8(typ.KindPointer)
	buf[1] = uint8(pi.ElemTI.Kind)
	if pi.ElemHasPtr {
		buf[2] = 1
	}
	*(*uint32)(unsafe.Pointer(&buf[4])) = uint32(pi.ElemSize)

	var elemDescPtr unsafe.Pointer
	switch pi.ElemTI.Kind {
	case typ.KindStruct, typ.KindSlice, typ.KindMap, typ.KindPointer, typ.KindArray:
		elemDescPtr = compileDecTypeDescRec(desc, pi.ElemTI)
	}
	*(*uintptr)(unsafe.Pointer(&buf[8])) = uintptr(elemDescPtr)

	*(*uintptr)(unsafe.Pointer(&buf[16])) = uintptr(pi.ElemRType)

	return unsafe.Pointer(&buf[0])
}

// compileArrayDesc lowers an array descriptor.
func compileArrayDesc(desc *decTypeDesc, ti *vdec.DecTypeInfo) unsafe.Pointer {
	ai := ti.ResolveArray()
	if ai == nil {
		return nil
	}

	buf := make([]byte, decArrayDescSize)
	desc.buffers = append(desc.buffers, buf)

	buf[0] = uint8(typ.KindArray)
	buf[1] = uint8(ai.ElemTI.Kind)
	if ai.ElemHasPtr {
		buf[2] = 1
	}
	*(*uint32)(unsafe.Pointer(&buf[4])) = uint32(ai.ElemSize)

	var elemDescPtr unsafe.Pointer
	switch ai.ElemTI.Kind {
	case typ.KindStruct, typ.KindSlice, typ.KindMap, typ.KindPointer, typ.KindArray:
		elemDescPtr = compileDecTypeDescRec(desc, ai.ElemTI)
	}
	*(*uintptr)(unsafe.Pointer(&buf[8])) = uintptr(elemDescPtr)

	*(*uint32)(unsafe.Pointer(&buf[16])) = uint32(ai.ArrayLen)
	*(*uint32)(unsafe.Pointer(&buf[20])) = 0

	return unsafe.Pointer(&buf[0])
}
