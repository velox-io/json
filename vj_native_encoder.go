package vjson

import (
	"runtime"
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

// CVjEncodingCtx mirrors C VjEncodingCtx (encoder.h Section 6).
//
// This is the sole state passed between Go and the C engine.
// Total: 48 bytes header + 64 * 24 bytes stack = 1584 bytes.
type CVjEncodingCtx struct {
	BufCur    unsafe.Pointer // current write position
	BufEnd    unsafe.Pointer // one past last writable byte
	CurOp     unsafe.Pointer // *COpStep — current instruction
	CurBase   unsafe.Pointer // current struct base address
	Depth     int32          // stack depth (0 = top-level)
	ErrorCode int32          // VjError enum value
	EncFlags  uint32         // VjEncFlags bitmask
	EscOpIdx  uint32         // index of op needing Go fallback
	Stack     [vjMaxDepth]CVjStackFrame
}

// ================================================================
// Compile-time size assertions (must match C _Static_assert values)
// ================================================================

var _ [24]byte = [unsafe.Sizeof(COpStep{})]byte{}
var _ [24]byte = [unsafe.Sizeof(CVjStackFrame{})]byte{}
var _ [1584]byte = [unsafe.Sizeof(CVjEncodingCtx{})]byte{}

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
	opEnd        uint16 = 0xFF
)

// VjError codes returned in CVjEncodingCtx.ErrorCode.
const (
	vjOK             int32 = 0
	vjErrBufFull     int32 = 1
	vjErrGoFallback  int32 = 2
	vjErrStackOvfl   int32 = 3
	vjErrCycle       int32 = 4
	vjErrNanInf      int32 = 5
)

// VjEncFlags bitmask — matches Go escapeFlags (vj_escape.go).
const (
	vjEncEscapeHTML        uint32 = 1 << 0
	vjEncEscapeLineTerms   uint32 = 1 << 1
	vjEncEscapeInvalidUTF8 uint32 = 1 << 2
)

// vjMaxDepth matches C VJ_MAX_DEPTH.
const vjMaxDepth = 64

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

		// Pin key bytes via keyRefs and set the raw pointer.
		keyBytes := fi.Ext.KeyBytes
		keyRefs = append(keyRefs, keyBytes)
		if len(keyBytes) > 0 {
			op.KeyPtr = unsafe.Pointer(&keyBytes[0])
		}

		// Nested struct: recursively compile sub-instructions.
		if fi.Kind == KindStruct {
			subDec := fi.Decoder.(*StructCodec)
			sub := compileStructOps(subDec)
			keyRefs = append(keyRefs, sub.keyRefs...)
			// Point SubOps to the first element of the sub-ops slice.
			// The slice is kept alive via the compiledOps.ops reference
			// chain (stored in StructCodec.nativeOps).
			if len(sub.ops) > 0 {
				op.SubOps = unsafe.Pointer(&sub.ops[0])
			}
			// We need to keep the sub.ops slice itself alive.
			// Stash it in keyRefs as a raw byte reference won't work;
			// instead we rely on the fact that sub.ops is reachable
			// from op.SubOps, and the top-level compiledOps.ops slice
			// is stored in StructCodec.nativeOps which is never freed.
			//
			// However, the GC only traces typed pointers. An
			// unsafe.Pointer in COpStep.SubOps won't keep sub.ops alive.
			// We must store the sub-slices explicitly.
			//
			// Solution: keyRefs also stores the sub.ops backing array
			// as a []byte alias. This is safe because we only need to
			// prevent GC from collecting the memory; we never access
			// it through the []byte alias.
			subOpsBytes := unsafe.Slice(
				(*byte)(unsafe.Pointer(&sub.ops[0])),
				len(sub.ops)*int(unsafe.Sizeof(COpStep{})),
			)
			keyRefs = append(keyRefs, subOpsBytes)
		}

		ops = append(ops, op)
	}

	// Append END sentinel.
	ops = append(ops, COpStep{OpType: opEnd})
	return compiledOps{ops: ops, keyRefs: keyRefs}
}

// canNativeEncode reports whether all fields of a StructCodec can be
// handled entirely by the C engine (no Go fallback required).
//
// This is a conservative check. Fields with custom marshalers,
// omitempty, ,string tags, or complex types (slice, map, interface,
// pointer) cause a false return.
func canNativeEncode(dec *StructCodec) bool {
	for i := range dec.Fields {
		fi := &dec.Fields[i]

		// Custom marshalers require Go callbacks.
		if fi.Flags&(tiFlagHasMarshalFn|tiFlagHasTextMarshalFn) != 0 {
			return false
		}

		// omitempty requires runtime zero-value checks in Go.
		if fi.Flags&tiFlagOmitEmpty != 0 {
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
			// KindFloat32, KindFloat64: Phase 4 — floats fall back to Go (no libc snprintf).
			// TODO(Phase 8): Enable floats after implementing ryu/Grisu2 in C.
			KindString,
			KindRawMessage, KindNumber:
			// Supported by C engine.

		case KindStruct:
			// Recurse: nested struct must also be fully native-encodable.
			subDec := fi.Decoder.(*StructCodec)
			if !canNativeEncode(subDec) {
				return false
			}

		default:
			// KindSlice, KindPointer, KindMap, KindAny — not supported.
			return false
		}
	}
	return true
}

// ================================================================
// StructCodec integration: lazy-init native ops cache
// ================================================================

// nativeEncoderCache holds compiled native encoder data for a StructCodec.
// Stored as a separate struct to avoid bloating the StructCodec with
// fields that are only relevant when the native encoder is available.
type nativeEncoderCache struct {
	once    sync.Once
	ok      bool         // true if all fields are native-encodable
	ops     []COpStep    // compiled instruction stream
	keyRefs [][]byte     // GC anchors for key strings and sub-op slices
}

// getNativeOps returns the compiled COpStep stream for this StructCodec.
// The second return value indicates whether native encoding is possible.
// Results are cached after the first call (thread-safe).
func (dec *StructCodec) getNativeOps() ([]COpStep, bool) {
	cache := dec.nativeCache()
	cache.once.Do(func() {
		cache.ok = canNativeEncode(dec)
		if cache.ok {
			compiled := compileStructOps(dec)
			cache.ops = compiled.ops
			cache.keyRefs = compiled.keyRefs
		}
	})
	return cache.ops, cache.ok
}

// ================================================================
// Native encoder call path: Go → Assembly → C → Go
// ================================================================

// nativeEncodeStruct encodes a struct using the C engine via the
// assembly bridge. It sets up CVjEncodingCtx, pins all Go memory,
// calls into C, and returns the encoded bytes or an error code.
//
// Parameters:
//   - buf: destination buffer (must have sufficient capacity)
//   - base: pointer to the Go struct instance
//   - ops: compiled COpStep instruction stream
//   - flags: escapeFlags cast to uint32
//
// Returns:
//   - written: number of bytes written to buf
//   - errCode: VjError value (vjOK = success)
func nativeEncodeStruct(buf []byte, base unsafe.Pointer, ops []COpStep, flags uint32) (written int, errCode int32) {
	if !encoder.Available {
		return 0, vjErrGoFallback
	}

	if len(buf) == 0 {
		return 0, vjErrBufFull
	}

	// Pin all Go memory that the C engine will access:
	// 1. The output buffer
	// 2. The struct being encoded
	// 3. The ops instruction array
	//
	// Key byte pointers (in COpStep.KeyPtr) point into TypeInfoExt.KeyBytes
	// slices which are allocated once during codec construction and are
	// reachable from the global codecCache — they won't be collected.
	// The ops themselves and their sub-ops slices are anchored by
	// nativeEncoderCache.keyRefs.
	var pinner runtime.Pinner
	pinner.Pin(&buf[0])
	pinner.Pin(&ops[0])
	pinner.Pin((*byte)(base))
	defer pinner.Unpin()

	// Allocate encoding context on the heap. CVjEncodingCtx is 1584 bytes,
	// too large for the goroutine stack with a NOSPLIT trampoline.
	// We allocate fresh each time rather than pooling to avoid GC issues:
	// the C engine writes raw pointers into the context (stack frames),
	// and pooled objects with stale unsafe.Pointer values cause
	// "marked free object in span" panics during GC sweeps.
	ctx := new(CVjEncodingCtx)

	bufStart := unsafe.Pointer(&buf[0])
	ctx.BufCur = bufStart
	ctx.BufEnd = unsafe.Add(bufStart, len(buf))
	ctx.CurOp = unsafe.Pointer(&ops[0])
	ctx.CurBase = base
	ctx.EncFlags = flags
	ctx.Depth = 0
	ctx.ErrorCode = 0
	ctx.EscOpIdx = 0

	// Call through the assembly bridge into C.
	encoder.Encode(unsafe.Pointer(ctx))

	written = int(uintptr(ctx.BufCur) - uintptr(bufStart))
	errCode = ctx.ErrorCode

	return written, errCode
}
