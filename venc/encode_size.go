package venc

import (
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

// bindSizeFn compiles a per-type size prediction function that scans runtime
// data (slice/map/string lengths, pointer nil-ness) without touching values.
// Sets SizeFn to nil for types that can't be predicted (interface{}, custom marshal).
func bindSizeFn(ti *EncTypeInfo) {
	ti.SizeFn = buildSizeFn(ti, 0)
}

func buildSizeFn(ti *EncTypeInfo, depth int) func(ptr unsafe.Pointer) int {
	if depth > 8 {
		return nil // too deep, fall back to static hint
	}

	// Custom marshal hooks: unpredictable output.
	if ti.TypeFlags&EncTypeFlagHasMarshalFn != 0 || ti.TypeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		return nil
	}

	switch ti.Kind {
	case typ.KindBool:
		return sizeBool
	case typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64:
		return sizeInt
	case typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64:
		return sizeInt
	case typ.KindFloat32, typ.KindFloat64:
		return sizeFloat
	case typ.KindString:
		return sizeString
	case typ.KindNumber:
		return sizeNumber
	case typ.KindRawMessage:
		return sizeRawMessage
	case typ.KindAny, typ.KindIface:
		return sizeAny // conservative constant for interface{} fields
	case typ.KindStruct:
		return buildSizeStruct(ti, depth)
	case typ.KindSlice:
		return buildSizeSlice(ti, depth)
	case typ.KindArray:
		return buildSizeArray(ti, depth)
	case typ.KindMap:
		return buildSizeMap(ti, depth)
	case typ.KindPointer:
		return buildSizePointer(ti, depth)
	default:
		return nil
	}
}

// --- Constant size functions ---

func sizeBool(_ unsafe.Pointer) int  { return 5 } // "false"
func sizeInt(_ unsafe.Pointer) int   { return 12 }
func sizeFloat(_ unsafe.Pointer) int { return 24 }

// --- Runtime-dependent size functions ---

// sizeAny returns a conservative estimate for interface{} fields.
// Most interface{} values in real-world JSON are null (4 bytes) or short
// strings/numbers. 16 is a reasonable middle ground.
func sizeAny(_ unsafe.Pointer) int { return 16 }

// sizeString reads the string length and estimates JSON output as 2 + len.
// Escape overhead is not accounted for; callers add their own headroom if needed.
func sizeString(ptr unsafe.Pointer) int {
	slen := *(*int)(unsafe.Add(ptr, unsafe.Sizeof(uintptr(0)))) // string.len
	return 2 + slen
}

func sizeNumber(ptr unsafe.Pointer) int {
	slen := *(*int)(unsafe.Add(ptr, unsafe.Sizeof(uintptr(0))))
	if slen == 0 {
		return 1 // "0"
	}
	return slen
}

func sizeRawMessage(ptr unsafe.Pointer) int {
	sh := (*gort.SliceHeader)(ptr)
	if sh.Data == nil || sh.Len == 0 {
		return 4 // "null"
	}
	return sh.Len
}

// --- Container size functions (compiled closures) ---

func buildSizeStruct(ti *EncTypeInfo, depth int) func(ptr unsafe.Pointer) int {
	si := ti.ResolveStruct()
	if si == nil {
		return nil
	}

	type fieldSizer struct {
		offset    uintptr
		overhead  int // len(KeyBytes) + 1 (comma)
		sizeFn    func(ptr unsafe.Pointer) int
		omitEmpty bool
		isZeroFn  func(ptr unsafe.Pointer) bool
	}

	sizers := make([]fieldSizer, 0, len(si.Fields))
	fixedTotal := 2 // "{" + "}"

	for i := range si.Fields {
		fi := &si.Fields[i]
		overhead := len(fi.KeyBytes) + 1 // key + comma
		omit := fi.TagFlags&EncTagFlagOmitEmpty != 0

		fn := buildSizeFn(fi.Type, depth+1)
		if fn == nil {
			return nil
		}

		// Struct fields use tighter estimates than top-level scalars because
		// the per-field overhead (key+comma) already provides buffer headroom.
		var fixedFieldSize int
		switch fi.Type.Kind {
		case typ.KindBool:
			fixedFieldSize = 5
		case typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
			typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64:
			fixedFieldSize = 6 // typical int ~3-6 digits
		case typ.KindFloat32, typ.KindFloat64:
			fixedFieldSize = 12
		}

		if fixedFieldSize > 0 && !omit {
			fixedTotal += overhead + fixedFieldSize
			continue
		}

		// For omitempty scalars, add to sizers with isZeroFn check.
		if fixedFieldSize > 0 && omit {
			fixed := overhead + fixedFieldSize
			sizers = append(sizers, fieldSizer{
				offset:    fi.Offset,
				overhead:  fixed,
				omitEmpty: true,
				isZeroFn:  fi.IsZeroFn,
			})
			continue
		}

		// Variable-size field (string, slice, map, struct, etc.)
		sizers = append(sizers, fieldSizer{
			offset:    fi.Offset,
			overhead:  overhead,
			sizeFn:    fn,
			omitEmpty: omit,
			isZeroFn:  fi.IsZeroFn,
		})
	}

	if len(sizers) == 0 {
		total := fixedTotal
		return func(_ unsafe.Pointer) int { return total }
	}

	return func(ptr unsafe.Pointer) int {
		n := fixedTotal
		for i := range sizers {
			s := &sizers[i]
			fieldPtr := unsafe.Add(ptr, s.offset)
			if s.omitEmpty && s.isZeroFn != nil && s.isZeroFn(fieldPtr) {
				continue // omitempty zero field → omitted entirely
			}
			if s.sizeFn != nil {
				n += s.overhead + s.sizeFn(fieldPtr)
			} else {
				n += s.overhead // fixed-size omitempty scalar (not zero)
			}
		}
		return n
	}
}

func buildSizeSlice(ti *EncTypeInfo, depth int) func(ptr unsafe.Pointer) int {
	si := ti.ResolveSlice()
	if si == nil {
		return nil
	}

	// For []byte, estimate base64 output size.
	if si.ElemType.Kind == typ.KindUint8 && si.ElemSize == 1 {
		return func(ptr unsafe.Pointer) int {
			sh := (*gort.SliceHeader)(ptr)
			if sh.Data == nil {
				return 4 // "null"
			}
			// base64: ceil(len/3)*4, plus 2 quotes
			return 2 + (sh.Len*4+2)/3
		}
	}

	elemSizeFn := buildSizeFn(si.ElemType, depth+1)

	if elemSizeFn != nil {
		return func(ptr unsafe.Pointer) int {
			sh := (*gort.SliceHeader)(ptr)
			if sh.Data == nil {
				return 4 // "null"
			}
			n := sh.Len
			if n == 0 {
				return 2 // "[]"
			}
			// Sample first element to estimate per-element size.
			elemHint := elemSizeFn(sh.Data)
			return 2 + n*elemHint + n - 1
		}
	}

	// Fallback: static per-element hint.
	elemHint := computeHintBytes(si.ElemType, depth+1)

	return func(ptr unsafe.Pointer) int {
		sh := (*gort.SliceHeader)(ptr)
		if sh.Data == nil {
			return 4 // "null"
		}
		if sh.Len == 0 {
			return 2 // "[]"
		}
		// "[" + len * elemHint + (len-1) commas + "]"
		return 2 + sh.Len*elemHint + sh.Len - 1
	}
}

func buildSizeArray(ti *EncTypeInfo, _ int) func(ptr unsafe.Pointer) int {
	ai := ti.ResolveArray()
	if ai == nil {
		return nil
	}

	elemHint := computeHintBytes(ai.ElemType, 0)
	arrayLen := ai.ArrayLen

	// Array size is data-independent (fixed element count).
	if arrayLen == 0 {
		return func(_ unsafe.Pointer) int { return 2 }
	}
	total := 2 + arrayLen*elemHint + arrayLen - 1
	return func(_ unsafe.Pointer) int { return total }
}

func buildSizeMap(ti *EncTypeInfo, depth int) func(ptr unsafe.Pointer) int {
	mi := ti.ResolveMap()
	if mi == nil {
		return nil
	}

	// Per-entry estimate: "key": value, = keyHint + 1(:) + valHint
	keyHint := computeHintBytes(mi.KeyType, depth+1)
	valHint := computeHintBytes(mi.ValType, depth+1)
	entryHint := keyHint + 1 + valHint

	return func(ptr unsafe.Pointer) int {
		mp := *(*unsafe.Pointer)(ptr)
		if mp == nil {
			return 4 // "null"
		}
		n := gort.MapLen(mp)
		if n == 0 {
			return 2 // "{}"
		}
		// "{" + n * entryHint + (n-1) commas + "}"
		return 2 + n*entryHint + n - 1
	}
}

func buildSizePointer(ti *EncTypeInfo, depth int) func(ptr unsafe.Pointer) int {
	pi := ti.ResolvePointer()
	if pi == nil {
		return nil
	}
	elemFn := buildSizeFn(pi.ElemType, depth+1)
	if elemFn == nil {
		return nil
	}

	return func(ptr unsafe.Pointer) int {
		p := *(*unsafe.Pointer)(ptr)
		if p == nil {
			return 4 // "null"
		}
		return elemFn(p)
	}
}

// computeHintBytes returns a one-time static output-size estimate.
func computeHintBytes(ti *EncTypeInfo, depth int) int {
	if depth > 8 {
		return 32 // cap recursive hint growth
	}
	if ti.TypeFlags&EncTypeFlagHasMarshalFn != 0 || ti.TypeFlags&EncTypeFlagHasTextMarshalFn != 0 {
		return 64
	}
	switch ti.Kind {
	case typ.KindBool:
		return 5
	case typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64:
		return 12
	case typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64:
		return 12
	case typ.KindFloat32, typ.KindFloat64:
		return 20
	case typ.KindString:
		return 32
	case typ.KindStruct:
		si := ti.ResolveStruct()
		if si == nil {
			return 64
		}
		n := 2
		for i := range si.Fields {
			fi := &si.Fields[i]
			n += len(fi.KeyBytes) + 1
			n += computeHintBytes(fi.Type, depth+1)
		}
		return n
	case typ.KindSlice:
		si := ti.ResolveSlice()
		if si == nil {
			return 64
		}
		return 2 + 4*(computeHintBytes(si.ElemType, depth+1)+1)
	case typ.KindPointer:
		pi := ti.ResolvePointer()
		if pi == nil {
			return 64
		}
		return computeHintBytes(pi.ElemType, depth+1)
	case typ.KindMap:
		return 128
	case typ.KindRawMessage:
		return 64
	case typ.KindNumber:
		return 12
	case typ.KindAny:
		return 64
	default:
		return 32
	}
}
