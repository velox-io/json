package venc

import (
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

// bindSizeFn compiles a per-type size prediction function that scans runtime
// data (slice/map/string lengths, pointer nil-ness) without touching values.
// Returns 0 for types that can't be predicted (interface{}, custom marshal).
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
		return buildSizeStruct(ti.ResolveStruct(), depth)
	case typ.KindSlice:
		return buildSizeSlice(ti.ResolveSlice(), depth)
	case typ.KindArray:
		return buildSizeArray(ti.ResolveArray(), depth)
	case typ.KindMap:
		return buildSizeMap(ti.ResolveMap(), depth)
	case typ.KindPointer:
		return buildSizePointer(ti.ResolvePointer(), depth)
	default:
		return nil
	}
}

// --- Scalar size functions (constant or near-constant) ---

func sizeBool(_ unsafe.Pointer) int  { return 5 } // "false"
func sizeInt(_ unsafe.Pointer) int   { return 12 }
func sizeFloat(_ unsafe.Pointer) int { return 24 }

// sizeAny returns a conservative estimate for interface{} fields.
// Most interface{} values in real-world JSON are null (4 bytes) or short
// strings/numbers. 16 is a reasonable middle ground.
func sizeAny(_ unsafe.Pointer) int { return 16 }

// sizeString reads the string length and estimates JSON output size.
// Estimate: 2 (quotes) + len. Most strings have minimal escape overhead;
// the +20% headroom in marshalHint covers the rare escape cases.
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

func buildSizeStruct(si *EncStructInfo, depth int) func(ptr unsafe.Pointer) int {
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

func buildSizeSlice(si *EncSliceInfo, depth int) func(ptr unsafe.Pointer) int {
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

func buildSizeArray(ai *EncArrayInfo, _ int) func(ptr unsafe.Pointer) int {
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

func buildSizeMap(mi *EncMapInfo, depth int) func(ptr unsafe.Pointer) int {
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

func buildSizePointer(pi *EncPointerInfo, depth int) func(ptr unsafe.Pointer) int {
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
