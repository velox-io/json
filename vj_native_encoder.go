package vjson

import (
	"reflect"
	"sort"
	"sync"
	"unsafe"

	"github.com/velox-io/json/native/encoder"
)

// ================================================================
// Go mirror types for C engine data structures (native/impl/encoder.h)
//
// These structs have identical memory layouts to their C counterparts.
// The C engine reads/writes these directly via pointer, so field
// order, size, and alignment must match exactly.
// ================================================================

// COpStep mirrors C OpStep (encoder.h Section 2).
//
// Layout (64-bit, 24 bytes):
//
//	offset 0: OpType   uint16    (2 bytes)
//	offset 2: KeyLen   uint16    (2 bytes)
//	offset 4: FieldOff uint32    (4 bytes)
//	offset 8: KeyPtr   pointer   (8 bytes) — pre-escaped key, e.g. "\"name\":"
//	offset 16: SubOps  pointer   (8 bytes) — child COpStep[] for OP_STRUCT
type COpStep struct {
	OpType   uint16
	KeyLen   uint16
	FieldOff uint32
	KeyPtr   unsafe.Pointer // points to pre-escaped key bytes
	SubOps   unsafe.Pointer // *COpStep for nested structs, nil otherwise
}

// CVjStackFrame mirrors C VjStackFrame (encoder.h Section 6).
//
// 24 bytes with explicit padding for 8-byte alignment.
type CVjStackFrame struct {
	RetOp   unsafe.Pointer // *COpStep — resume point in parent
	RetBase unsafe.Pointer // parent struct base address
	First   int32          // was parent on its first field?
	_pad    int32          // alignment padding
}

// CVjEncodingCtx mirrors C VjEncodingCtx (encoder_types.h Section 6).
//
// This is the sole state passed between Go and the C engine.
// Total: 64 bytes header + 16 * 24 bytes stack = 448 bytes.
type CVjEncodingCtx struct {
	BufCur         unsafe.Pointer // current write position
	BufEnd         unsafe.Pointer // one past last writable byte
	CurOp          unsafe.Pointer // *COpStep — current instruction
	CurBase        unsafe.Pointer // current struct base address
	Depth          int32          // stack depth (0 = top-level)
	ErrorCode      int32          // VjError enum value
	EncFlags       uint32         // VjEncFlags bitmask
	EscOpIdx       uint32         // index of op needing Go fallback
	IfaceTypeTable unsafe.Pointer // *CIfaceTypeEntry sorted type tag table
	IfaceTypeCount int32          // number of entries in type tag table
	_pad2          int32          // alignment padding
	Stack          [vjMaxDepth]CVjStackFrame
}

// ================================================================
// Compile-time size assertions (must match C _Static_assert values)
// ================================================================

var _ [24]byte = [unsafe.Sizeof(COpStep{})]byte{}
var _ [24]byte = [unsafe.Sizeof(CVjStackFrame{})]byte{}
var _ [448]byte = [unsafe.Sizeof(CVjEncodingCtx{})]byte{}
var _ [16]byte = [unsafe.Sizeof(CIfaceTypeEntry{})]byte{}

// CVjArrayCtx mirrors C VjArrayCtx (encoder.h Section 11).
//
// Wraps VjEncodingCtx with array-specific fields so vj_encode_array
// can loop over elements entirely in C.
type CVjArrayCtx struct {
	Enc      CVjEncodingCtx // offset 0
	ArrData  unsafe.Pointer // offset 448 — array base pointer
	ArrCount int64          // offset 456 — total element count
	ArrIdx   int64          // offset 464 — current element index (for resume)
	ElemSize int64          // offset 472 — sizeof(element)
	ElemOps  unsafe.Pointer // offset 480 — *COpStep for each element
}

// Compile-time size assertion for CVjArrayCtx.
// 448 (VjEncodingCtx) + 8 + 8 + 8 + 8 + 8 = 488 bytes.
var _ [488]byte = [unsafe.Sizeof(CVjArrayCtx{})]byte{}

// CIfaceTypeEntry mirrors C VjIfaceTypeEntry (encoder_types.h).
//
// Maps a Go *abi.Type pointer to a primitive opcode tag for inline
// interface{} encoding in the C engine.  16 bytes with padding.
type CIfaceTypeEntry struct {
	TypePtr unsafe.Pointer // *abi.Type (address-comparable)
	Tag     uint8          // OP_BOOL..OP_STRING, or 0 = unknown
	_pad    [7]byte        // alignment padding
}

// ifaceTypeTable is a global sorted array of primitive type→tag mappings.
// Built once at first use, then reused for all C engine calls.
// Sorted by TypePtr (ascending) for binary search in C.
var (
	ifaceTypeTable     []CIfaceTypeEntry
	ifaceTypeTableOnce sync.Once
)

func getIfaceTypeTable() []CIfaceTypeEntry {
	ifaceTypeTableOnce.Do(func() {
		entries := []struct {
			t   reflect.Type
			tag uint8
		}{
			{reflect.TypeOf(false), uint8(opBool)},
			{reflect.TypeOf(int(0)), uint8(opInt)},
			{reflect.TypeOf(int8(0)), uint8(opInt8)},
			{reflect.TypeOf(int16(0)), uint8(opInt16)},
			{reflect.TypeOf(int32(0)), uint8(opInt32)},
			{reflect.TypeOf(int64(0)), uint8(opInt64)},
			{reflect.TypeOf(uint(0)), uint8(opUint)},
			{reflect.TypeOf(uint8(0)), uint8(opUint8)},
			{reflect.TypeOf(uint16(0)), uint8(opUint16)},
			{reflect.TypeOf(uint32(0)), uint8(opUint32)},
			{reflect.TypeOf(uint64(0)), uint8(opUint64)},
			{reflect.TypeOf(float32(0)), uint8(opFloat32)},
			{reflect.TypeOf(float64(0)), uint8(opFloat64)},
			{reflect.TypeOf(""), uint8(opString)},
		}
		table := make([]CIfaceTypeEntry, len(entries))
		for i, e := range entries {
			table[i] = CIfaceTypeEntry{
				TypePtr: rtypePtr(e.t),
				Tag:     e.tag,
			}
		}
		// Sort by TypePtr for binary search in C.
		sort.Slice(table, func(i, j int) bool {
			return uintptr(table[i].TypePtr) < uintptr(table[j].TypePtr)
		})
		ifaceTypeTable = table
	})
	return ifaceTypeTable
}

// setIfaceTypeTable populates the interface type tag table in a VjEncodingCtx.
func setIfaceTypeTable(ctx *CVjEncodingCtx) {
	table := getIfaceTypeTable()
	ctx.IfaceTypeTable = unsafe.Pointer(&table[0])
	ctx.IfaceTypeCount = int32(len(table))
}

// ================================================================
// C engine constants — mirror native/impl/encoder.h enums
// ================================================================

// OpType codes. Values 0-20 are intentionally equal to ElemTypeKind
// so the pre-compiler can cast directly for simple fields.
const (
	opBool       uint16 = 0
	opInt        uint16 = 1
	opInt8       uint16 = 2
	opInt16      uint16 = 3
	opInt32      uint16 = 4
	opInt64      uint16 = 5
	opUint       uint16 = 6
	opUint8      uint16 = 7
	opUint16     uint16 = 8
	opUint32     uint16 = 9
	opUint64     uint16 = 10
	opFloat32    uint16 = 11
	opFloat64    uint16 = 12
	opString     uint16 = 13
	opStruct     uint16 = 14
	opSlice      uint16 = 15
	opPointer    uint16 = 16
	opInterface  uint16 = 17
	opMap        uint16 = 18
	opRawMessage uint16 = 19
	opNumber     uint16 = 20
	opByteSlice  uint16 = 21
	opFallback   uint16 = 22 // Go-only fallback (marshalers, complex structs)
	opEnd        uint16 = 0xFF

	// Flag bit OR-ed into OpType to indicate omitempty semantics.
	// The C engine strips this flag before dispatch-table lookup and
	// checks vj_is_zero() to skip zero-valued fields.
	opFlagOmitempty uint16 = 0x8000
)

// VjError codes returned in CVjEncodingCtx.ErrorCode.
const (
	vjOK            int32 = 0
	vjErrBufFull    int32 = 1
	vjErrGoFallback int32 = 2
	vjErrStackOvfl  int32 = 3
	vjErrCycle      int32 = 4
	vjErrNanInf     int32 = 5
)

// VjEncFlags bitmask — matches Go escapeFlags (vj_escape.go).
const (
	vjEncEscapeHTML        uint32 = 1 << 0
	vjEncEscapeLineTerms   uint32 = 1 << 1
	vjEncEscapeInvalidUTF8 uint32 = 1 << 2

	// Hot resume flags for re-entering C after Go handles a fallback field.
	vjEncResume      uint32 = 1 << 7 // skip opening '{'; resume mid-struct
	vjEncResumeFirst uint32 = 1 << 8 // with RESUME: no field written yet (first=1)
)

// Bitmask constants for decoding esc_op_idx (packed by C on fallback).
// Bits [0:30] = op index relative to CurOp, bit 31 = first flag.
const (
	escOpIdxMask  uint32 = 0x7FFFFFFF
	escOpFirstBit uint32 = 0x80000000
)

// vjMaxDepth matches C VJ_MAX_DEPTH.
const vjMaxDepth = 16

// ================================================================
// Pre-compiler: StructCodec → COpStep[]
// ================================================================

// compiledOps holds the result of compiling a StructCodec for the C engine.
type compiledOps struct {
	ops     []COpStep // instruction stream, terminated by opEnd
	keyRefs [][]byte  // keeps key byte slices alive for GC
}

// compileStructOps translates a StructCodec into a COpStep instruction stream.
//
// The returned ops slice is terminated by an opEnd sentinel. keyRefs holds
// references to all pre-escaped key byte slices so they are not garbage
// collected while the C engine holds raw pointers to them.
func compileStructOps(dec *StructCodec) compiledOps {
	n := len(dec.Fields)
	ops := make([]COpStep, 0, n+1)
	keyRefs := make([][]byte, 0, n)

	for i := range dec.Fields {
		fi := &dec.Fields[i]

		op := COpStep{
			OpType:   uint16(fi.Kind), // ElemTypeKind values == OpType values
			KeyLen:   uint16(len(fi.Ext.KeyBytes)),
			FieldOff: uint32(fi.Offset),
		}

		// Fields with custom marshalers or ,string tag need Go encoding.
		// Mark them as opFallback to trigger vj_op_fallback in C.
		// Note: opInterface (17) is reserved for actual interface{} fields
		// where the C engine can attempt inline encoding of primitive values.
		if fi.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn|tiFlagQuoted) != 0 {
			op.OpType = opFallback
		}

		// Set omitempty flag so the C engine skips zero-valued fields.
		// For opInterface/opFallback fields, vj_is_zero returns 0 (never skip),
		// so the Go-side fallback handler checks omitempty instead.
		if fi.Flags&tiFlagOmitEmpty != 0 {
			op.OpType |= opFlagOmitempty
		}

		// Pin key bytes via keyRefs and set the raw pointer.
		keyBytes := fi.Ext.KeyBytes
		keyRefs = append(keyRefs, keyBytes)
		if len(keyBytes) > 0 {
			op.KeyPtr = unsafe.Pointer(&keyBytes[0])
		}

		// Nested struct: recursively compile sub-instructions only if
		// the sub-struct is fully native-encodable. If not, mark this
		// field as a fallback op (opFallback) so the C engine triggers
		// a top-level fallback — Go handles the entire nested struct.
		// This ensures fallback always occurs at depth=0, keeping the
		// hot resume logic simple (no nested stack reconstruction).
		if fi.Kind == KindStruct {
			subDec := fi.Codec.(*StructCodec)
			if canNativeEncode(subDec) {
				sub := compileStructOps(subDec)
				keyRefs = append(keyRefs, sub.keyRefs...)
				if len(sub.ops) > 0 {
					op.SubOps = unsafe.Pointer(&sub.ops[0])
				}
				// Keep the sub.ops slice alive for GC. The GC only
				// traces typed pointers; an unsafe.Pointer in
				// COpStep.SubOps won't keep sub.ops alive. Store the
				// backing array as a []byte alias.
				subOpsBytes := unsafe.Slice(
					(*byte)(unsafe.Pointer(&sub.ops[0])),
					len(sub.ops)*int(unsafe.Sizeof(COpStep{})),
				)
				keyRefs = append(keyRefs, subOpsBytes)
			} else {
				// Sub-struct has unsupported fields — use opFallback
				// to route to vj_op_fallback in the C engine.
				op.OpType = opFallback
			}
		}

		// Pointer field: compile sub-ops describing the element type.
		// Only proceed if op.OpType is still opPointer (not overridden
		// by marshalers/quoted check above which sets opFallback).
		if fi.Kind == KindPointer && op.OpType&opTypeMask == opPointer {
			keyRefs = compilePointerSubOps(&op, fi, keyRefs)
		}

		ops = append(ops, op)
	}

	// Append END sentinel.
	ops = append(ops, COpStep{OpType: opEnd})
	return compiledOps{ops: ops, keyRefs: keyRefs}
}

// opTypeMask extracts the opcode from op_type, stripping the omitempty flag.
const opTypeMask = uint16(0x00FF)

// isNativeElemKind reports whether a KindXxx value represents a type
// that the C engine can encode natively as a pointer element.
func isNativeElemKind(k ElemTypeKind) bool {
	switch k {
	case KindBool,
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
		KindFloat32, KindFloat64,
		KindString,
		KindRawMessage, KindNumber:
		return true
	}
	return false
}

// canNativeEncodePointerElem reports whether a pointer element type can
// be natively encoded by the C engine. The element type must not have
// custom marshalers (which bypass field-level flag detection for pointer
// types since the marshaler check is skipped for reflect.Pointer kinds).
func canNativeEncodePointerElem(elemTI *TypeInfo) bool {
	// Element with custom marshalers must use Go encoding.
	if elemTI.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
		return false
	}

	switch elemTI.Kind {
	case KindBool,
		KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
		KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
		KindFloat32, KindFloat64,
		KindString,
		KindRawMessage, KindNumber:
		return true
	case KindStruct:
		subDec := elemTI.Codec.(*StructCodec)
		return canNativeEncode(subDec)
	}
	return false
}

// compilePointerSubOps compiles the sub-ops for a pointer field and
// updates the COpStep in-place. Returns the (possibly extended) keyRefs.
func compilePointerSubOps(op *COpStep, fi *TypeInfo, keyRefs [][]byte) [][]byte {
	pDec := fi.Codec.(*PointerCodec)
	elemTI := pDec.ElemTI

	if !canNativeEncodePointerElem(elemTI) {
		op.OpType = opFallback
		return keyRefs
	}

	switch elemTI.Kind {
	case KindStruct:
		subDec := elemTI.Codec.(*StructCodec)
		sub := compileStructOps(subDec)
		keyRefs = append(keyRefs, sub.keyRefs...)
		// Wrapper: [{OP_STRUCT, sub_ops=inner_ops}, {OP_END}]
		elemOps := make([]COpStep, 2)
		elemOps[0] = COpStep{OpType: opStruct}
		if len(sub.ops) > 0 {
			elemOps[0].SubOps = unsafe.Pointer(&sub.ops[0])
		}
		elemOps[1] = COpStep{OpType: opEnd}
		op.SubOps = unsafe.Pointer(&elemOps[0])
		// Pin elemOps and sub.ops for GC.
		keyRefs = append(keyRefs, unsafe.Slice(
			(*byte)(unsafe.Pointer(&elemOps[0])),
			len(elemOps)*int(unsafe.Sizeof(COpStep{})),
		))
		keyRefs = append(keyRefs, unsafe.Slice(
			(*byte)(unsafe.Pointer(&sub.ops[0])),
			len(sub.ops)*int(unsafe.Sizeof(COpStep{})),
		))

	default:
		// Primitive: single-element sub_ops.
		elemOps := make([]COpStep, 2)
		elemOps[0] = COpStep{OpType: uint16(elemTI.Kind)}
		elemOps[1] = COpStep{OpType: opEnd}
		op.SubOps = unsafe.Pointer(&elemOps[0])
		keyRefs = append(keyRefs, unsafe.Slice(
			(*byte)(unsafe.Pointer(&elemOps[0])),
			len(elemOps)*int(unsafe.Sizeof(COpStep{})),
		))
	}

	return keyRefs
}

// canNativeEncode reports whether all fields of a StructCodec can be
// handled entirely by the C engine (no Go fallback required).
//
// omitempty is supported for primitive types, strings, and pointers
// (nil check). Struct-level omitempty (recursive zero-check) still
// requires Go.
func canNativeEncode(dec *StructCodec) bool {
	for i := range dec.Fields {
		fi := &dec.Fields[i]

		// Custom marshalers require Go callbacks.
		if fi.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
			return false
		}

		// ,string tag requires quoted encoding logic.
		if fi.Flags&tiFlagQuoted != 0 {
			return false
		}

		switch fi.Kind {
		case KindBool,
			KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
			KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
			KindFloat32, KindFloat64,
			KindString,
			KindRawMessage, KindNumber:
			// Supported by C engine (including omitempty).

		case KindStruct:
			// omitempty on a struct field requires recursive zero-check
			// which the C engine cannot do — reject.
			if fi.Flags&tiFlagOmitEmpty != 0 {
				return false
			}
			// Recurse: nested struct must also be fully native-encodable.
			subDec := fi.Codec.(*StructCodec)
			if !canNativeEncode(subDec) {
				return false
			}

		case KindPointer:
			// Pointer to a native-eligible type — the C engine handles
			// nil check + deref + inline encode (or nested struct entry).
			// omitempty is OK: vj_is_zero checks *(void**)ptr == NULL.
			pDec := fi.Codec.(*PointerCodec)
			if !canNativeEncodePointerElem(pDec.ElemTI) {
				return false
			}

		default:
			// KindSlice, KindMap, KindAny — not supported.
			return false
		}
	}
	return true
}

// ================================================================
// StructCodec integration: lazy-init native ops cache
// ================================================================

// canPartialNativeEncode reports whether a StructCodec has at least one
// field that the C engine can handle natively. This is used to decide
// whether the hot-resume path is worthwhile for mixed structs.
//
// Returns false if all fields require Go (no benefit from C engine).
func canPartialNativeEncode(dec *StructCodec) bool {
	for i := range dec.Fields {
		fi := &dec.Fields[i]

		// Fields with custom marshalers or ,string tag always need Go.
		if fi.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn|tiFlagQuoted) != 0 {
			continue
		}

		switch fi.Kind {
		case KindBool,
			KindInt, KindInt8, KindInt16, KindInt32, KindInt64,
			KindUint, KindUint8, KindUint16, KindUint32, KindUint64,
			KindFloat32, KindFloat64,
			KindString,
			KindRawMessage, KindNumber:
			return true // at least one native-encodable field

		case KindStruct:
			// Even if sub-struct isn't fully native, the struct field
			// itself is a native type (we'll mark non-native sub-structs
			// as fallback ops in compileStructOps).
			return true

		case KindPointer:
			// Pointer to native-eligible element is handled by C.
			pDec := fi.Codec.(*PointerCodec)
			if canNativeEncodePointerElem(pDec.ElemTI) {
				return true
			}
		}
	}
	return false
}

// nativeMode indicates the level of native encoder support for a struct.
type nativeMode int

const (
	nativeNone    nativeMode = iota // no native encoding possible
	nativeFull                      // all fields handled by C engine
	nativePartial                   // some fields need Go fallback (hot resume)
)

// nativeEncoderCache holds compiled native encoder data for a StructCodec.
// Stored as a separate struct to avoid bloating the StructCodec with
// fields that are only relevant when the native encoder is available.
type nativeEncoderCache struct {
	once    sync.Once
	mode    nativeMode // nativeNone / nativeFull / nativePartial
	ops     []COpStep  // compiled instruction stream
	keyRefs [][]byte   // GC anchors for key strings and sub-op slices
}

// getNativeOps returns the compiled COpStep stream for this StructCodec.
// The returned nativeMode indicates whether native encoding is possible:
//   - nativeNone: no native encoding, use pure Go
//   - nativeFull: all fields handled by C, no fallback possible
//   - nativePartial: some fields need Go fallback (hot resume path)
//
// Results are cached after the first call (thread-safe).
func (dec *StructCodec) getNativeOps() ([]COpStep, nativeMode) {
	cache := dec.nativeCache()
	cache.once.Do(func() {
		if canNativeEncode(dec) {
			cache.mode = nativeFull
		} else if canPartialNativeEncode(dec) {
			cache.mode = nativePartial
		} else {
			cache.mode = nativeNone
			return
		}
		compiled := compileStructOps(dec)
		cache.ops = compiled.ops
		cache.keyRefs = compiled.keyRefs
	})
	return cache.ops, cache.mode
}

// ================================================================
// Native encoder call path: Go → Assembly → C → Go
// ================================================================

// nativeResult holds the result of a C engine invocation.
type nativeResult struct {
	written  int   // bytes written to buffer
	errCode  int32 // VjError enum value
	escOpIdx int   // absolute index in ops[] of the fallback field (-1 if N/A)
	cFirst   bool  // was C still on its first field when fallback occurred
	depth    int32 // C nesting depth at return (0 = top-level)
}

// nativeEncodeStruct encodes a struct using the C engine via the
// assembly bridge. It sets up CVjEncodingCtx on the goroutine stack,
// calls into C, and returns the result.
//
// Parameters:
//   - buf: destination buffer (must have sufficient capacity)
//   - base: pointer to the Go struct instance
//   - ops: compiled COpStep instruction stream (full, unsliced)
//   - startIdx: index in ops[] to start encoding from (0 for initial call)
//   - flags: escapeFlags cast to uint32, may include vjEncResume bits
//
// The startIdx parameter supports hot resume: after Go handles a fallback
// field, it re-enters C at ops[startIdx] with VJ_ENC_RESUME set in flags.
func nativeEncodeStruct(buf []byte, base unsafe.Pointer, ops []COpStep, startIdx int, flags uint32) nativeResult {
	if !encoder.Available {
		return nativeResult{errCode: vjErrGoFallback, escOpIdx: -1}
	}

	if len(buf) == 0 {
		return nativeResult{errCode: vjErrBufFull, escOpIdx: -1}
	}

	// CVjEncodingCtx lives on the goroutine stack (432 bytes).
	// No heap allocation, no runtime.Pinner needed.
	//
	// Safety: the assembly trampoline is NOSPLIT, so from the moment we
	// enter C until it returns there are no GC safe-points. The GC
	// cannot relocate the goroutine stack or any heap objects that C
	// references (buf, ops, base, key pointers) during this window.
	// This is the same pattern used by the jsonmarker scanner.
	var ctx CVjEncodingCtx

	bufStart := unsafe.Pointer(&buf[0])
	ctx.BufCur = bufStart
	ctx.BufEnd = unsafe.Add(bufStart, len(buf))
	ctx.CurOp = unsafe.Pointer(&ops[startIdx])
	ctx.CurBase = base
	ctx.EncFlags = flags
	setIfaceTypeTable(&ctx)

	encoder.Encode(unsafe.Pointer(&ctx))

	r := nativeResult{
		written:  int(uintptr(ctx.BufCur) - uintptr(bufStart)),
		errCode:  ctx.ErrorCode,
		escOpIdx: -1,
		depth:    ctx.Depth,
	}

	if ctx.ErrorCode == vjErrGoFallback {
		rawEsc := ctx.EscOpIdx
		r.escOpIdx = startIdx + int(rawEsc&escOpIdxMask)
		r.cFirst = (rawEsc & escOpFirstBit) != 0
	}

	return r
}

// nativeEncodeStructFast is the lean path for fully-native structs.
// It always starts at ops[0] and returns only (written, errCode) as a
// register-friendly tuple, avoiding the overhead of constructing a
// nativeResult struct. Use this when no hot resume is needed.
func nativeEncodeStructFast(buf []byte, base unsafe.Pointer, ops []COpStep, flags uint32) (written int, errCode int32) {
	if !encoder.Available {
		return 0, vjErrGoFallback
	}

	if len(buf) == 0 {
		return 0, vjErrBufFull
	}

	var ctx CVjEncodingCtx

	bufStart := unsafe.Pointer(&buf[0])
	ctx.BufCur = bufStart
	ctx.BufEnd = unsafe.Add(bufStart, len(buf))
	ctx.CurOp = unsafe.Pointer(&ops[0])
	ctx.CurBase = base
	ctx.EncFlags = flags
	setIfaceTypeTable(&ctx)

	encoder.Encode(unsafe.Pointer(&ctx))

	written = int(uintptr(ctx.BufCur) - uintptr(bufStart))
	errCode = ctx.ErrorCode
	return
}

// nativeEncodeArray batch-encodes a []NativeStruct slice using the C engine.
// The C function loops over elements calling vj_encode_struct per element.
// Returns bytes written, error code, and current array index (for BUF_FULL resume).
//
// The caller is responsible for writing '[' before and ']' after.
// startIdx allows resuming after a BUF_FULL — elements [0, startIdx) are skipped.
func nativeEncodeArray(buf []byte, data unsafe.Pointer, count int,
	elemSize uintptr, ops []COpStep, flags uint32, startIdx int) (written int, errCode int32, arrIdx int) {
	if !encoder.Available {
		return 0, vjErrGoFallback, 0
	}

	if len(buf) == 0 {
		return 0, vjErrBufFull, startIdx
	}

	var actx CVjArrayCtx

	bufStart := unsafe.Pointer(&buf[0])
	actx.Enc.BufCur = bufStart
	actx.Enc.BufEnd = unsafe.Add(bufStart, len(buf))
	actx.Enc.EncFlags = flags
	setIfaceTypeTable(&actx.Enc)
	actx.ArrData = data
	actx.ArrCount = int64(count)
	actx.ArrIdx = int64(startIdx)
	actx.ElemSize = int64(elemSize)
	actx.ElemOps = unsafe.Pointer(&ops[0])

	encoder.EncodeArray(unsafe.Pointer(&actx))

	written = int(uintptr(actx.Enc.BufCur) - uintptr(bufStart))
	errCode = actx.Enc.ErrorCode
	arrIdx = int(actx.ArrIdx)
	return
}
