package vjson

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encoder"
)

// ================================================================
// Compile-time layout assertions (redundant with var _ in source,
// but explicit tests give better error messages)
// ================================================================

func TestCOpStepSize(t *testing.T) {
	const want = 24
	got := unsafe.Sizeof(COpStep{})
	if got != want {
		t.Fatalf("sizeof(COpStep) = %d, want %d", got, want)
	}
}

func TestCVjStackFrameSize(t *testing.T) {
	const want = 24
	got := unsafe.Sizeof(CVjStackFrame{})
	if got != want {
		t.Fatalf("sizeof(CVjStackFrame) = %d, want %d", got, want)
	}
}

func TestCVjEncodingCtxSize(t *testing.T) {
	const want = 432 // 48 header + 16 * 24 stack
	got := unsafe.Sizeof(CVjEncodingCtx{})
	if got != want {
		t.Fatalf("sizeof(CVjEncodingCtx) = %d, want %d", got, want)
	}
}

func TestCVjEncodingCtxFieldOffsets(t *testing.T) {
	var ctx CVjEncodingCtx
	base := unsafe.Pointer(&ctx)

	checks := []struct {
		name   string
		field  unsafe.Pointer
		offset uintptr
	}{
		{"BufCur", unsafe.Pointer(&ctx.BufCur), 0},
		{"BufEnd", unsafe.Pointer(&ctx.BufEnd), 8},
		{"CurOp", unsafe.Pointer(&ctx.CurOp), 16},
		{"CurBase", unsafe.Pointer(&ctx.CurBase), 24},
		{"Depth", unsafe.Pointer(&ctx.Depth), 32},
		{"ErrorCode", unsafe.Pointer(&ctx.ErrorCode), 36},
		{"EncFlags", unsafe.Pointer(&ctx.EncFlags), 40},
		{"EscOpIdx", unsafe.Pointer(&ctx.EscOpIdx), 44},
		{"Stack", unsafe.Pointer(&ctx.Stack), 48},
	}

	for _, c := range checks {
		got := uintptr(c.field) - uintptr(base)
		if got != c.offset {
			t.Errorf("CVjEncodingCtx.%s offset = %d, want %d", c.name, got, c.offset)
		}
	}
}

// ================================================================
// OpType ↔ ElemTypeKind alignment
// ================================================================

func TestOpTypeAlignedWithElemTypeKind(t *testing.T) {
	// OpType constants must equal the corresponding ElemTypeKind values
	// so the pre-compiler can do a direct cast.
	checks := []struct {
		kind ElemTypeKind
		op   uint16
		name string
	}{
		{KindBool, opBool, "Bool"},
		{KindInt, opInt, "Int"},
		{KindInt8, opInt8, "Int8"},
		{KindInt16, opInt16, "Int16"},
		{KindInt32, opInt32, "Int32"},
		{KindInt64, opInt64, "Int64"},
		{KindUint, opUint, "Uint"},
		{KindUint8, opUint8, "Uint8"},
		{KindUint16, opUint16, "Uint16"},
		{KindUint32, opUint32, "Uint32"},
		{KindUint64, opUint64, "Uint64"},
		{KindFloat32, opFloat32, "Float32"},
		{KindFloat64, opFloat64, "Float64"},
		{KindString, opString, "String"},
		{KindStruct, opStruct, "Struct"},
		{KindSlice, opSlice, "Slice"},
		{KindPointer, opPointer, "Pointer"},
		{KindAny, opInterface, "Any/Interface"},
		{KindMap, opMap, "Map"},
		{KindRawMessage, opRawMessage, "RawMessage"},
		{KindNumber, opNumber, "Number"},
	}
	for _, c := range checks {
		if uint16(c.kind) != c.op {
			t.Errorf("Kind%s=%d != op%s=%d", c.name, c.kind, c.name, c.op)
		}
	}
}

// ================================================================
// VjEncFlags ↔ escapeFlags alignment
// ================================================================

func TestVjEncFlagsAlignedWithEscapeFlags(t *testing.T) {
	if vjEncEscapeHTML != uint32(escapeHTML) {
		t.Errorf("vjEncEscapeHTML=%d != escapeHTML=%d", vjEncEscapeHTML, escapeHTML)
	}
	if vjEncEscapeLineTerms != uint32(escapeLineTerms) {
		t.Errorf("vjEncEscapeLineTerms=%d != escapeLineTerms=%d", vjEncEscapeLineTerms, escapeLineTerms)
	}
	if vjEncEscapeInvalidUTF8 != uint32(escapeInvalidUTF8) {
		t.Errorf("vjEncEscapeInvalidUTF8=%d != escapeInvalidUTF8=%d", vjEncEscapeInvalidUTF8, escapeInvalidUTF8)
	}
}

// ================================================================
// CompileStructOps tests
// ================================================================

func TestCompileEmptyStruct(t *testing.T) {
	type Empty struct{}
	ti := GetCodec(reflect.TypeFor[Empty]())
	dec := ti.Codec.(*StructCodec)

	compiled := compileStructOps(dec)
	if len(compiled.ops) != 1 {
		t.Fatalf("expected 1 op (END sentinel), got %d", len(compiled.ops))
	}
	if compiled.ops[0].OpType != opEnd {
		t.Fatalf("expected opEnd, got %d", compiled.ops[0].OpType)
	}
}

func TestCompileBasicStruct(t *testing.T) {
	type Basic struct {
		ID    int64   `json:"id"`
		Name  string  `json:"name"`
		Score float64 `json:"score"`
		OK    bool    `json:"ok"`
	}

	ti := GetCodec(reflect.TypeFor[Basic]())
	dec := ti.Codec.(*StructCodec)

	compiled := compileStructOps(dec)

	// 4 fields + 1 END sentinel
	if len(compiled.ops) != 5 {
		t.Fatalf("expected 5 ops, got %d", len(compiled.ops))
	}

	// Verify each field's OpType
	wantTypes := []uint16{opInt64, opString, opFloat64, opBool}
	for i, want := range wantTypes {
		got := compiled.ops[i].OpType
		if got != want {
			t.Errorf("ops[%d].OpType = %d, want %d", i, got, want)
		}
	}

	// Verify END sentinel
	if compiled.ops[4].OpType != opEnd {
		t.Errorf("last op = %d, want opEnd(%d)", compiled.ops[4].OpType, opEnd)
	}

	// Verify key lengths match Ext.KeyBytes
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		op := &compiled.ops[i]
		wantKeyLen := uint16(len(fi.Ext.KeyBytes))
		if op.KeyLen != wantKeyLen {
			t.Errorf("ops[%d].KeyLen = %d, want %d", i, op.KeyLen, wantKeyLen)
		}
	}

	// Verify field offsets
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		op := &compiled.ops[i]
		wantOff := uint32(fi.Offset)
		if op.FieldOff != wantOff {
			t.Errorf("ops[%d].FieldOff = %d, want %d", i, op.FieldOff, wantOff)
		}
	}

	// Verify KeyPtr points to actual key bytes
	for i := range dec.Fields {
		fi := &dec.Fields[i]
		op := &compiled.ops[i]
		if op.KeyPtr == nil {
			t.Errorf("ops[%d].KeyPtr is nil", i)
			continue
		}
		// Read back the key through the pointer
		gotKey := unsafe.Slice((*byte)(op.KeyPtr), op.KeyLen)
		if string(gotKey) != string(fi.Ext.KeyBytes) {
			t.Errorf("ops[%d] key = %q, want %q", i, gotKey, fi.Ext.KeyBytes)
		}
	}

	// SubOps should be nil for basic types
	for i := range 4 {
		if compiled.ops[i].SubOps != nil {
			t.Errorf("ops[%d].SubOps should be nil for basic type", i)
		}
	}
}

func TestCompileNestedStruct(t *testing.T) {
	type Inner struct {
		X int32  `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		A     string `json:"a"`
		Inner Inner  `json:"inner"`
		B     int64  `json:"b"`
	}

	ti := GetCodec(reflect.TypeFor[Outer]())
	dec := ti.Codec.(*StructCodec)

	compiled := compileStructOps(dec)

	// 3 fields + 1 END
	if len(compiled.ops) != 4 {
		t.Fatalf("expected 4 ops, got %d", len(compiled.ops))
	}

	// ops[0] = "a" (string)
	if compiled.ops[0].OpType != opString {
		t.Errorf("ops[0].OpType = %d, want opString(%d)", compiled.ops[0].OpType, opString)
	}

	// ops[1] = "inner" (struct)
	if compiled.ops[1].OpType != opStruct {
		t.Errorf("ops[1].OpType = %d, want opStruct(%d)", compiled.ops[1].OpType, opStruct)
	}
	if compiled.ops[1].SubOps == nil {
		t.Fatal("ops[1].SubOps is nil for nested struct")
	}

	// Verify sub-ops: 2 fields + 1 END
	subOps := unsafe.Slice((*COpStep)(compiled.ops[1].SubOps), 3)
	if subOps[0].OpType != opInt32 {
		t.Errorf("sub[0].OpType = %d, want opInt32(%d)", subOps[0].OpType, opInt32)
	}
	if subOps[1].OpType != opString {
		t.Errorf("sub[1].OpType = %d, want opString(%d)", subOps[1].OpType, opString)
	}
	if subOps[2].OpType != opEnd {
		t.Errorf("sub[2].OpType = %d, want opEnd(%d)", subOps[2].OpType, opEnd)
	}

	// ops[2] = "b" (int64)
	if compiled.ops[2].OpType != opInt64 {
		t.Errorf("ops[2].OpType = %d, want opInt64(%d)", compiled.ops[2].OpType, opInt64)
	}

	// ops[3] = END
	if compiled.ops[3].OpType != opEnd {
		t.Errorf("ops[3].OpType = %d, want opEnd", compiled.ops[3].OpType)
	}
}

func TestCompileAllIntTypes(t *testing.T) {
	type AllInts struct {
		A int    `json:"a"`
		B int8   `json:"b"`
		C int16  `json:"c"`
		D int32  `json:"d"`
		E int64  `json:"e"`
		F uint   `json:"f"`
		G uint8  `json:"g"`
		H uint16 `json:"h"`
		I uint32 `json:"i"`
		J uint64 `json:"j"`
	}

	ti := GetCodec(reflect.TypeFor[AllInts]())
	dec := ti.Codec.(*StructCodec)
	compiled := compileStructOps(dec)

	wantTypes := []uint16{
		opInt, opInt8, opInt16, opInt32, opInt64,
		opUint, opUint8, opUint16, opUint32, opUint64,
	}
	for i, want := range wantTypes {
		got := compiled.ops[i].OpType
		if got != want {
			t.Errorf("ops[%d].OpType = %d, want %d", i, got, want)
		}
	}
}

// ================================================================
// canNativeEncode tests
// ================================================================

func TestCanNativeEncodeBasicStruct(t *testing.T) {
	type Basic struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		OK   bool   `json:"ok"`
	}
	ti := GetCodec(reflect.TypeFor[Basic]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for basic struct")
	}
}

func TestCanNativeEncodeNestedPureStruct(t *testing.T) {
	type Inner struct {
		X int `json:"x"`
	}
	type Outer struct {
		A     string `json:"a"`
		Inner Inner  `json:"inner"`
	}
	ti := GetCodec(reflect.TypeFor[Outer]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for nested pure struct")
	}
}

func TestCanNativeEncodeRejectsMap(t *testing.T) {
	type WithMap struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
	}
	ti := GetCodec(reflect.TypeFor[WithMap]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with map field")
	}
}

func TestCanNativeEncodeRejectsSlice(t *testing.T) {
	type WithSlice struct {
		Items []int `json:"items"`
	}
	ti := GetCodec(reflect.TypeFor[WithSlice]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with slice field")
	}
}

func TestCanNativeEncodeRejectsInterface(t *testing.T) {
	type WithAny struct {
		Value any `json:"value"`
	}
	ti := GetCodec(reflect.TypeFor[WithAny]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with interface field")
	}
}

func TestCanNativeEncodeAcceptsPointerPrimitive(t *testing.T) {
	type WithPtr struct {
		Name *string `json:"name"`
	}
	ti := GetCodec(reflect.TypeFor[WithPtr]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for struct with *string field")
	}
}

func TestCanNativeEncodeAcceptsPointerStruct(t *testing.T) {
	type Inner struct {
		X int `json:"x"`
	}
	type Outer struct {
		P *Inner `json:"p"`
	}
	ti := GetCodec(reflect.TypeFor[Outer]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for struct with *PureStruct field")
	}
}

func TestCanNativeEncodeRejectsPointerToPointer(t *testing.T) {
	type WithPP struct {
		Val **int `json:"val"`
	}
	ti := GetCodec(reflect.TypeFor[WithPP]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with **int field")
	}
}

func TestCanNativeEncodeRejectsPointerToSlice(t *testing.T) {
	type WithPS struct {
		Items *[]int `json:"items"`
	}
	ti := GetCodec(reflect.TypeFor[WithPS]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with *[]int field")
	}
}

func TestCanNativeEncodeRejectsPointerToMap(t *testing.T) {
	type WithPM struct {
		Tags *map[string]string `json:"tags"`
	}
	ti := GetCodec(reflect.TypeFor[WithPM]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with *map field")
	}
}

func TestCanNativeEncodeRejectsPointerToNonNativeStruct(t *testing.T) {
	type Inner struct {
		Items []int `json:"items"`
	}
	type Outer struct {
		P *Inner `json:"p"`
	}
	ti := GetCodec(reflect.TypeFor[Outer]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with *NonNativeStruct field")
	}
}

func TestCanNativeEncodeAcceptsOmitempty(t *testing.T) {
	// omitempty on primitive/string fields is now handled by the C engine.
	type WithOmit struct {
		ID   int    `json:"id"`
		Name string `json:"name,omitempty"`
	}
	ti := GetCodec(reflect.TypeFor[WithOmit]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for struct with omitempty on simple types")
	}
}

func TestCanNativeEncodeRejectsStructOmitempty(t *testing.T) {
	// omitempty on a struct field requires recursive zero-check — not supported.
	type Inner struct {
		X int `json:"x"`
	}
	type WithStructOmit struct {
		A     int   `json:"a"`
		Inner Inner `json:"inner,omitempty"`
	}
	ti := GetCodec(reflect.TypeFor[WithStructOmit]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with omitempty on struct field")
	}
}

func TestCanNativeEncodeRejectsStringTag(t *testing.T) {
	type WithQuoted struct {
		Count int `json:"count,string"`
	}
	ti := GetCodec(reflect.TypeFor[WithQuoted]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with ,string tag")
	}
}

func TestCanNativeEncodeRejectsNestedImpure(t *testing.T) {
	type Inner struct {
		Items []int `json:"items"`
	}
	type Outer struct {
		A     string `json:"a"`
		Inner Inner  `json:"inner"`
	}
	ti := GetCodec(reflect.TypeFor[Outer]())
	dec := ti.Codec.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with impure nested struct")
	}
}

func TestCanNativeEncodeEmptyStruct(t *testing.T) {
	type Empty struct{}
	ti := GetCodec(reflect.TypeFor[Empty]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for empty struct")
	}
}

// ================================================================
// getNativeOps integration test
// ================================================================

func TestGetNativeOps(t *testing.T) {
	type Simple struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	ti := GetCodec(reflect.TypeFor[Simple]())
	dec := ti.Codec.(*StructCodec)

	ops, mode := dec.getNativeOps()
	if mode == nativeNone {
		t.Fatal("expected getNativeOps to succeed for simple struct")
	}
	if len(ops) != 3 { // 2 fields + END
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}

	// Call again — should return cached result (same pointer).
	ops2, mode2 := dec.getNativeOps()
	if mode2 == nativeNone {
		t.Fatal("second call failed")
	}
	if &ops[0] != &ops2[0] {
		t.Error("expected cached result (same slice backing array)")
	}
}

func TestGetNativeOpsReturnsFalseForImpure(t *testing.T) {
	type Impure struct {
		ID   int            `json:"id"`
		Data map[string]any `json:"data"`
	}

	ti := GetCodec(reflect.TypeFor[Impure]())
	dec := ti.Codec.(*StructCodec)

	ops, mode := dec.getNativeOps()
	if mode == nativeFull {
		t.Error("expected getNativeOps to not return nativeFull for impure struct")
	}
	if mode == nativeNone && ops != nil {
		t.Error("expected nil ops for nativeNone struct")
	}
}

// ================================================================
// Phase 3: Assembly bridge integration tests
// ================================================================

func TestNativeEncodeStructBridgeAvailable(t *testing.T) {
	// On darwin/arm64 the native encoder should be available.
	// This test confirms the .syso is linked and the trampoline works.
	if !encoder.Available {
		t.Skip("native encoder not available on this platform")
	}
}

func TestNativeEncodeStructStub(t *testing.T) {
	// Tests the full Go → asm → C → Go path with real VM output.
	if !encoder.Available {
		t.Skip("native encoder not available on this platform")
	}

	type Simple struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	ti := GetCodec(reflect.TypeFor[Simple]())
	dec := ti.Codec.(*StructCodec)

	ops, mode := dec.getNativeOps()
	if mode == nativeNone {
		t.Fatal("expected native ops to be available for Simple struct")
	}

	v := Simple{ID: 42, Name: "hello"}
	buf := make([]byte, 4096)

	written, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjOK {
		t.Fatalf("nativeEncodeStructFast errCode = %d, want vjOK(0)", errCode)
	}

	got := string(buf[:written])
	want := `{"id":42,"name":"hello"}`
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNativeEncodeStructBufFull(t *testing.T) {
	// Test buffer-full error when buffer is too small.
	if !encoder.Available {
		t.Skip("native encoder not available on this platform")
	}

	type Tiny struct {
		X int `json:"x"`
	}

	ti := GetCodec(reflect.TypeFor[Tiny]())
	dec := ti.Codec.(*StructCodec)

	ops, mode := dec.getNativeOps()
	if mode == nativeNone {
		t.Fatal("expected native ops to be available")
	}

	v := Tiny{X: 1}
	buf := make([]byte, 1) // too small for "{}"

	_, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjErrBufFull {
		t.Fatalf("expected vjErrBufFull(%d), got %d", vjErrBufFull, errCode)
	}
}

func TestNativeEncodeStructEmptyBuf(t *testing.T) {
	// Test with zero-length buffer.
	type S struct{ X int }
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Codec.(*StructCodec)
	ops, mode := dec.getNativeOps()
	if mode == nativeNone {
		t.Fatal("expected native ops")
	}

	v := S{X: 1}
	_, errCode := nativeEncodeStructFast(nil, unsafe.Pointer(&v), ops, 0)
	if errCode != vjErrBufFull {
		t.Fatalf("expected vjErrBufFull for nil buf, got %d", errCode)
	}
}

func TestNativeEncodeStructFallbackWhenUnavailable(t *testing.T) {
	// When encoder.Available is false, nativeEncodeStructFast should
	// return vjErrGoFallback without panicking.
	if encoder.Available {
		t.Skip("this test is for platforms without native encoder")
	}

	buf := make([]byte, 64)
	ops := []COpStep{{OpType: opEnd}}
	_, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&struct{}{}), ops, 0)
	if errCode != vjErrGoFallback {
		t.Fatalf("expected vjErrGoFallback, got %d", errCode)
	}
}

// TestNativeEncodeIntOnly tests VM with a struct containing only int fields.
func TestNativeEncodeIntOnly(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type IntOnly struct {
		X int `json:"x"`
	}

	// Manually construct ops to isolate from compileStructOps
	key := []byte(`"x":`)
	ops := []COpStep{
		{
			OpType:   opInt,
			KeyLen:   uint16(len(key)),
			FieldOff: 0, // X is at offset 0
			KeyPtr:   unsafe.Pointer(&key[0]),
		},
		{OpType: opEnd},
	}

	v := IntOnly{X: 42}
	buf := make([]byte, 256)

	written, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjOK {
		t.Fatalf("errCode = %d, want vjOK(0)", errCode)
	}

	got := string(buf[:written])
	want := `{"x":42}`
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestNativeEncodeEmptyStructDirect tests the VM with a minimal
// OP_END-only instruction stream, producing "{}".
func TestNativeEncodeEmptyStructDirect(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	ops := []COpStep{{OpType: opEnd}}
	s := struct{}{}
	buf := make([]byte, 64)

	written, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&s), ops, 0)
	if errCode != vjOK {
		t.Fatalf("errCode = %d, want vjOK(0)", errCode)
	}
	got := string(buf[:written])
	if got != "{}" {
		t.Errorf("output = %q, want \"{}\"", got)
	}
}

// ================================================================
// Phase 4: Comprehensive integration tests for all C VM type handlers
// ================================================================

// helper: nativeEncode compiles ops from a struct type and encodes the value.
// Returns the JSON string and error code.
func nativeEncodeHelper(t *testing.T, v any) (string, int32) {
	t.Helper()
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr {
		t.Fatalf("expected pointer to struct, got %T", v)
	}
	rv = rv.Elem()
	ti := GetCodec(rv.Type())
	dec := ti.Codec.(*StructCodec)
	ops, mode := dec.getNativeOps()
	if mode == nativeNone {
		t.Fatalf("getNativeOps returned nativeNone for %T", v)
	}

	buf := make([]byte, 8192)
	written, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(rv.UnsafeAddr()), ops, 0)
	return string(buf[:written]), errCode
}

// nativeEncodeWithFlags is like nativeEncodeHelper but with custom VjEncFlags.
func nativeEncodeWithFlags(t *testing.T, v any, flags uint32) (string, int32) {
	t.Helper()
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr {
		t.Fatalf("expected pointer to struct, got %T", v)
	}
	rv = rv.Elem()
	ti := GetCodec(rv.Type())
	dec := ti.Codec.(*StructCodec)
	ops, mode := dec.getNativeOps()
	if mode == nativeNone {
		t.Fatalf("getNativeOps returned nativeNone for %T", v)
	}

	buf := make([]byte, 8192)
	written, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(rv.UnsafeAddr()), ops, flags)
	return string(buf[:written]), errCode
}

// ----------------------------------------------------------------
// Bool
// ----------------------------------------------------------------

func TestNativeEncodeBool(t *testing.T) {
	type S struct {
		A bool `json:"a"`
		B bool `json:"b"`
	}
	v := S{A: true, B: false}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"a":true,"b":false}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ----------------------------------------------------------------
// All integer types with boundary values
// ----------------------------------------------------------------

func TestNativeEncodeAllIntTypes(t *testing.T) {
	type AllInts struct {
		A int    `json:"a"`
		B int8   `json:"b"`
		C int16  `json:"c"`
		D int32  `json:"d"`
		E int64  `json:"e"`
		F uint   `json:"f"`
		G uint8  `json:"g"`
		H uint16 `json:"h"`
		I uint32 `json:"i"`
		J uint64 `json:"j"`
	}

	tests := []struct {
		name string
		val  AllInts
		want string
	}{
		{
			name: "zeros",
			val:  AllInts{},
			want: `{"a":0,"b":0,"c":0,"d":0,"e":0,"f":0,"g":0,"h":0,"i":0,"j":0}`,
		},
		{
			name: "positive",
			val:  AllInts{A: 42, B: 127, C: 32767, D: 2147483647, E: 9223372036854775807, F: 42, G: 255, H: 65535, I: 4294967295, J: 18446744073709551615},
			want: `{"a":42,"b":127,"c":32767,"d":2147483647,"e":9223372036854775807,"f":42,"g":255,"h":65535,"i":4294967295,"j":18446744073709551615}`,
		},
		{
			name: "negative",
			val:  AllInts{A: -1, B: -128, C: -32768, D: -2147483648, E: math.MinInt64},
			want: `{"a":-1,"b":-128,"c":-32768,"d":-2147483648,"e":-9223372036854775808,"f":0,"g":0,"h":0,"i":0,"j":0}`,
		},
		{
			name: "one",
			val:  AllInts{A: 1, B: 1, C: 1, D: 1, E: 1, F: 1, G: 1, H: 1, I: 1, J: 1},
			want: `{"a":1,"b":1,"c":1,"d":1,"e":1,"f":1,"g":1,"h":1,"i":1,"j":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errCode := nativeEncodeHelper(t, &tt.val)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestNativeEncodeInt64Boundaries(t *testing.T) {
	type S struct {
		V int64 `json:"v"`
	}
	tests := []struct {
		name string
		val  int64
		want string
	}{
		{"zero", 0, `{"v":0}`},
		{"one", 1, `{"v":1}`},
		{"minus_one", -1, `{"v":-1}`},
		{"max", math.MaxInt64, `{"v":9223372036854775807}`},
		{"min", math.MinInt64, `{"v":-9223372036854775808}`},
		{"ten", 10, `{"v":10}`},
		{"ninety_nine", 99, `{"v":99}`},
		{"hundred", 100, `{"v":100}`},
		{"thousand", 1000, `{"v":1000}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeHelper(t, &v)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNativeEncodeUint64Boundaries(t *testing.T) {
	type S struct {
		V uint64 `json:"v"`
	}
	tests := []struct {
		name string
		val  uint64
		want string
	}{
		{"zero", 0, `{"v":0}`},
		{"one", 1, `{"v":1}`},
		{"max", math.MaxUint64, `{"v":18446744073709551615}`},
		{"large", 12345678901234567890, `{"v":12345678901234567890}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeHelper(t, &v)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ----------------------------------------------------------------
// String encoding with escaping
// ----------------------------------------------------------------

func TestNativeEncodeString(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	tests := []struct {
		name string
		val  string
		want string
	}{
		{"empty", "", `{"v":""}`},
		{"simple", "hello", `{"v":"hello"}`},
		{"spaces", "hello world", `{"v":"hello world"}`},
		{"quote", `say "hi"`, `{"v":"say \"hi\""}`},
		{"backslash", `a\b`, `{"v":"a\\b"}`},
		{"newline", "line1\nline2", `{"v":"line1\nline2"}`},
		{"tab", "a\tb", `{"v":"a\tb"}`},
		{"carriage_return", "a\rb", `{"v":"a\rb"}`},
		{"backspace", "a\bb", `{"v":"a\bb"}`},
		{"formfeed", "a\fb", `{"v":"a\fb"}`},
		{"null_byte", "a\x00b", `{"v":"a\u0000b"}`},
		{"control_char_0x01", "a\x01b", `{"v":"a\u0001b"}`},
		{"control_char_0x1f", "a\x1fb", `{"v":"a\u001fb"}`},
		{"unicode_cjk", "hello\u4e16\u754c", `{"v":"hello世界"}`},
		{"emoji", "hello 🌍", `{"v":"hello 🌍"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeHelper(t, &v)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestNativeEncodeStringHTMLEscape(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	tests := []struct {
		name  string
		val   string
		flags uint32
		want  string
	}{
		{"angle_brackets", "<b>bold</b>", vjEncEscapeHTML, `{"v":"\u003cb\u003ebold\u003c/b\u003e"}`},
		{"ampersand", "a&b", vjEncEscapeHTML, `{"v":"a\u0026b"}`},
		{"no_html_flag", "<b>bold</b>", 0, `{"v":"<b>bold</b>"}`},
		{"mixed", `<a href="x">&</a>`, vjEncEscapeHTML, `{"v":"\u003ca href=\"x\"\u003e\u0026\u003c/a\u003e"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeWithFlags(t, &v, tt.flags)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestNativeEncodeStringLineTerminators(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	// U+2028 LINE SEPARATOR, U+2029 PARAGRAPH SEPARATOR
	tests := []struct {
		name  string
		val   string
		flags uint32
		want  string
	}{
		{"line_sep", "a\u2028b", vjEncEscapeLineTerms, `{"v":"a\u2028b"}`},
		{"para_sep", "a\u2029b", vjEncEscapeLineTerms, `{"v":"a\u2029b"}`},
		{"no_flag", "a\u2028b", 0, "{\x22v\x22:\x22a\xe2\x80\xa8b\x22}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeWithFlags(t, &v, tt.flags)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestNativeEncodeStringInvalidUTF8(t *testing.T) {
	type S struct {
		V string `json:"v"`
	}
	tests := []struct {
		name  string
		val   string
		flags uint32
		want  string
	}{
		{"invalid_utf8", "a\xfe\xffb", vjEncEscapeInvalidUTF8, `{"v":"a\ufffd\ufffdb"}`},
		{"no_flag", "a\xfe\xffb", 0, "{\x22v\x22:\x22a\xfe\xffb\x22}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeWithFlags(t, &v, tt.flags)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// ----------------------------------------------------------------
// Nested structs (1-3 layers)
// ----------------------------------------------------------------

func TestNativeEncodeNestedStruct1Layer(t *testing.T) {
	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		Name  string `json:"name"`
		Inner Inner  `json:"inner"`
	}

	v := Outer{Name: "test", Inner: Inner{X: 42, Y: "hello"}}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"name":"test","inner":{"x":42,"y":"hello"}}`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestNativeEncodeNestedStruct2Layers(t *testing.T) {
	type L2 struct {
		Val int `json:"val"`
	}
	type L1 struct {
		Name string `json:"name"`
		L2   L2     `json:"l2"`
	}
	type L0 struct {
		ID int `json:"id"`
		L1 L1  `json:"l1"`
	}

	v := L0{ID: 1, L1: L1{Name: "nested", L2: L2{Val: 99}}}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"id":1,"l1":{"name":"nested","l2":{"val":99}}}`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestNativeEncodeNestedStruct3Layers(t *testing.T) {
	type L3 struct {
		Z bool `json:"z"`
	}
	type L2 struct {
		Y  string `json:"y"`
		L3 L3     `json:"l3"`
	}
	type L1 struct {
		X  int `json:"x"`
		L2 L2  `json:"l2"`
	}
	type L0 struct {
		A  string `json:"a"`
		L1 L1     `json:"l1"`
		B  int    `json:"b"`
	}

	v := L0{A: "top", L1: L1{X: 10, L2: L2{Y: "mid", L3: L3{Z: true}}}, B: 99}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"a":"top","l1":{"x":10,"l2":{"y":"mid","l3":{"z":true}}},"b":99}`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestNativeEncodeNestedEmptyStruct(t *testing.T) {
	type Inner struct{}
	type Outer struct {
		Name  string `json:"name"`
		Inner Inner  `json:"inner"`
		ID    int    `json:"id"`
	}

	v := Outer{Name: "test", ID: 1}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"name":"test","inner":{},"id":1}`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestNativeEncodeMultipleNestedSiblings(t *testing.T) {
	type Inner struct {
		V int `json:"v"`
	}
	type Outer struct {
		A Inner `json:"a"`
		B Inner `json:"b"`
	}

	v := Outer{A: Inner{V: 1}, B: Inner{V: 2}}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"a":{"v":1},"b":{"v":2}}`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// ----------------------------------------------------------------
// RawMessage
// ----------------------------------------------------------------

func TestNativeEncodeRawMessage(t *testing.T) {
	type S struct {
		ID      int             `json:"id"`
		Payload json.RawMessage `json:"payload"`
	}

	tests := []struct {
		name string
		val  S
		want string
	}{
		{
			name: "object",
			val:  S{ID: 1, Payload: json.RawMessage(`{"a":1}`)},
			want: `{"id":1,"payload":{"a":1}}`,
		},
		{
			name: "array",
			val:  S{ID: 2, Payload: json.RawMessage(`[1,2,3]`)},
			want: `{"id":2,"payload":[1,2,3]}`,
		},
		{
			name: "string",
			val:  S{ID: 3, Payload: json.RawMessage(`"hello"`)},
			want: `{"id":3,"payload":"hello"}`,
		},
		{
			name: "number",
			val:  S{ID: 4, Payload: json.RawMessage(`42`)},
			want: `{"id":4,"payload":42}`,
		},
		{
			name: "null_value",
			val:  S{ID: 5, Payload: json.RawMessage(`null`)},
			want: `{"id":5,"payload":null}`,
		},
		{
			name: "nil_raw",
			val:  S{ID: 6, Payload: nil},
			want: `{"id":6,"payload":null}`,
		},
		{
			name: "empty_raw",
			val:  S{ID: 7, Payload: json.RawMessage{}},
			want: `{"id":7,"payload":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errCode := nativeEncodeHelper(t, &tt.val)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// ----------------------------------------------------------------
// Number (json.Number)
// ----------------------------------------------------------------

func TestNativeEncodeNumber(t *testing.T) {
	type S struct {
		ID  int         `json:"id"`
		Num json.Number `json:"num"`
	}

	tests := []struct {
		name string
		val  S
		want string
	}{
		{
			name: "integer",
			val:  S{ID: 1, Num: json.Number("42")},
			want: `{"id":1,"num":42}`,
		},
		{
			name: "negative",
			val:  S{ID: 2, Num: json.Number("-123")},
			want: `{"id":2,"num":-123}`,
		},
		{
			name: "float",
			val:  S{ID: 3, Num: json.Number("3.14")},
			want: `{"id":3,"num":3.14}`,
		},
		{
			name: "scientific",
			val:  S{ID: 4, Num: json.Number("1e10")},
			want: `{"id":4,"num":1e10}`,
		},
		{
			name: "empty_number",
			val:  S{ID: 5, Num: json.Number("")},
			want: `{"id":5,"num":0}`,
		},
		{
			name: "zero",
			val:  S{ID: 6, Num: json.Number("0")},
			want: `{"id":6,"num":0}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errCode := nativeEncodeHelper(t, &tt.val)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// ----------------------------------------------------------------
// Mixed types in one struct
// ----------------------------------------------------------------

func TestNativeEncodeMixedTypes(t *testing.T) {
	type S struct {
		Name    string          `json:"name"`
		Age     int32           `json:"age"`
		Active  bool            `json:"active"`
		Score   uint64          `json:"score"`
		Payload json.RawMessage `json:"payload"`
		Count   json.Number     `json:"count"`
	}

	v := S{
		Name:    "Alice",
		Age:     30,
		Active:  true,
		Score:   9999,
		Payload: json.RawMessage(`{"x":1}`),
		Count:   json.Number("42"),
	}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"name":"Alice","age":30,"active":true,"score":9999,"payload":{"x":1},"count":42}`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// ----------------------------------------------------------------
// Buffer-full handling
// ----------------------------------------------------------------

func TestNativeEncodeBufFullVariousSizes(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID int `json:"id"`
	}

	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Codec.(*StructCodec)
	ops, mode := dec.getNativeOps()
	if mode == nativeNone {
		t.Fatal("expected native ops")
	}

	v := S{ID: 42}
	// Full output is `{"id":42}` = 9 bytes
	// CHECK estimates: 1('{') + 1(comma) + key_len("id":=5) + 21(max int digits) = 28
	// So we need at least 28 bytes for CHECK to pass (conservative estimate).
	// Test with buffers that are too small at various points:
	for _, sz := range []int{0, 1, 2, 5, 8} {
		buf := make([]byte, sz)
		_, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
		if errCode != vjErrBufFull {
			t.Errorf("buf size %d: expected vjErrBufFull(%d), got %d", sz, vjErrBufFull, errCode)
		}
	}

	// Buffer large enough for worst-case estimate should work
	buf := make([]byte, 64)
	written, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjOK {
		t.Fatalf("buf size 64: errCode = %d, want vjOK", errCode)
	}
	got := string(buf[:written])
	want := `{"id":42}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ----------------------------------------------------------------
// Go fallback for unsupported types
// ----------------------------------------------------------------

// ----------------------------------------------------------------
// Float encoding (native Ryu)
// ----------------------------------------------------------------

func TestNativeEncodeFloat64(t *testing.T) {
	// Structs with float64 fields should now be native-encodable (Ryu).
	type S struct {
		X float64 `json:"x"`
	}
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for struct with float64 field")
	}
}

func TestNativeEncodeFloat32(t *testing.T) {
	type S struct {
		X float32 `json:"x"`
	}
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Codec.(*StructCodec)
	if !canNativeEncode(dec) {
		t.Error("expected canNativeEncode=true for struct with float32 field")
	}
}

func TestNativeEncodeFloat64NaN(t *testing.T) {
	// NaN should return VJ_ERR_NAN_INF.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	key := []byte(`"x":`)
	ops := []COpStep{
		{
			OpType:   opFloat64,
			KeyLen:   uint16(len(key)),
			FieldOff: 0,
			KeyPtr:   unsafe.Pointer(&key[0]),
		},
		{OpType: opEnd},
	}

	v := struct{ X float64 }{X: math.NaN()}
	buf := make([]byte, 256)
	_, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjErrNanInf {
		t.Fatalf("expected vjErrNanInf(%d), got %d", vjErrNanInf, errCode)
	}
}

func TestNativeEncodeFloat64Inf(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	key := []byte(`"x":`)
	ops := []COpStep{
		{
			OpType:   opFloat64,
			KeyLen:   uint16(len(key)),
			FieldOff: 0,
			KeyPtr:   unsafe.Pointer(&key[0]),
		},
		{OpType: opEnd},
	}

	for _, inf := range []float64{math.Inf(1), math.Inf(-1)} {
		v := struct{ X float64 }{X: inf}
		buf := make([]byte, 256)
		_, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
		if errCode != vjErrNanInf {
			t.Fatalf("expected vjErrNanInf for %v, got %d", inf, errCode)
		}
	}
}

func TestNativeEncodeFloat32NaN(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	key := []byte(`"x":`)
	ops := []COpStep{
		{
			OpType:   opFloat32,
			KeyLen:   uint16(len(key)),
			FieldOff: 0,
			KeyPtr:   unsafe.Pointer(&key[0]),
		},
		{OpType: opEnd},
	}

	v := struct{ X float32 }{X: float32(math.NaN())}
	buf := make([]byte, 256)
	_, errCode := nativeEncodeStructFast(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjErrNanInf {
		t.Fatalf("expected vjErrNanInf(%d), got %d", vjErrNanInf, errCode)
	}
}

func TestNativeEncodeFloat64Values(t *testing.T) {
	type S struct {
		V float64 `json:"v"`
	}
	tests := []struct {
		name string
		val  float64
		want string
	}{
		{"zero", 0.0, `{"v":0}`},
		{"neg_zero", math.Copysign(0, -1), `{"v":-0}`},
		{"one", 1.0, `{"v":1}`},
		{"neg_one", -1.0, `{"v":-1}`},
		{"half", 0.5, `{"v":0.5}`},
		{"simple_frac", 9.5, `{"v":9.5}`},
		{"point_one", 0.1, `{"v":0.1}`},
		{"point_two", 0.2, `{"v":0.2}`},
		{"pi", 3.141592653589793, `{"v":3.141592653589793}`},
		{"ten", 10.0, `{"v":10}`},
		{"hundred", 100.0, `{"v":100}`},
		{"large_int", 1e20, `{"v":100000000000000000000}`},
		{"neg_large", -1e20, `{"v":-100000000000000000000}`},
		{"small_frac", 0.001, `{"v":0.001}`},
		{"neg_small", -0.001, `{"v":-0.001}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeHelper(t, &v)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestNativeEncodeFloat32Values(t *testing.T) {
	type S struct {
		V float32 `json:"v"`
	}
	tests := []struct {
		name string
		val  float32
		want string
	}{
		{"zero", 0.0, `{"v":0}`},
		{"neg_zero", float32(math.Copysign(0, -1)), `{"v":-0}`},
		{"one", 1.0, `{"v":1}`},
		{"half", 0.5, `{"v":0.5}`},
		{"simple", 9.5, `{"v":9.5}`},
		{"point_one", 0.1, `{"v":0.1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := S{V: tt.val}
			got, errCode := nativeEncodeHelper(t, &v)
			if errCode != vjOK {
				t.Fatalf("errCode = %d", errCode)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestNativeEncodeStructWithFloats(t *testing.T) {
	type S struct {
		Name  string  `json:"name"`
		Score float64 `json:"score"`
		Rate  float32 `json:"rate"`
		ID    int     `json:"id"`
	}
	v := S{Name: "test", Score: 3.14, Rate: 2.5, ID: 42}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}
	want := `{"name":"test","score":3.14,"rate":2.5,"id":42}`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// ----------------------------------------------------------------
// Correctness: compare native encoder output with standard json.Marshal
// ----------------------------------------------------------------

func TestNativeEncodeMatchesStdlib(t *testing.T) {
	type Inner struct {
		A bool   `json:"a"`
		B string `json:"b"`
	}
	type S struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Inner Inner  `json:"inner"`
		Count uint32 `json:"count"`
	}

	v := S{
		ID:    -42,
		Name:  "test\nvalue",
		Inner: Inner{A: true, B: "nested \"quote\""},
		Count: 100,
	}

	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("errCode = %d", errCode)
	}

	// Standard json.Marshal uses HTML escaping by default,
	// so we compare without HTML escaping (native default flags=0).
	// The standard library also does not escape line terminators or
	// validate UTF-8 by default in regular json.Marshal (it always does HTML).
	// To get a fair comparison, let's just verify the structure is valid JSON
	// and contains the right values.
	var decoded S
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("native output is not valid JSON: %v\noutput: %s", err, got)
	}
	if decoded.ID != v.ID {
		t.Errorf("ID = %d, want %d", decoded.ID, v.ID)
	}
	if decoded.Name != v.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, v.Name)
	}
	if decoded.Inner.A != v.Inner.A {
		t.Errorf("Inner.A = %v, want %v", decoded.Inner.A, v.Inner.A)
	}
	if decoded.Inner.B != v.Inner.B {
		t.Errorf("Inner.B = %q, want %q", decoded.Inner.B, v.Inner.B)
	}
	if decoded.Count != v.Count {
		t.Errorf("Count = %d, want %d", decoded.Count, v.Count)
	}
}

// ----------------------------------------------------------------
// Bit-exact float comparison with Go's strconv.AppendFloat
// ----------------------------------------------------------------

func TestNativeEncodeFloat64BitExact(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		V float64 `json:"v"`
	}

	testValues := []float64{
		0, 1, -1, 0.1, 0.2, 0.3, 0.5, 1.5, 9.5,
		1e1, 1e2, 1e10, 1e15, 1e20, 1e100, 1e200, 1e308,
		-1e20, -0.1, -0.5, -1.5,
		math.SmallestNonzeroFloat64,
		math.MaxFloat64,
		5e-324,
		2.2250738585072014e-308, // normal/subnormal boundary
		2.2250738585072009e-308, // just below boundary
		0.1 + 0.2,              // classic floating point: 0.30000000000000004
		math.Pi, math.E,
		1.7976931348623157e+308,  // max float64
		-1.7976931348623157e+308, // min float64
		2.2204460492503131e-16,   // machine epsilon
		1.0000000000000002,       // 1 + epsilon
		0.9999999999999998,       // 1 - epsilon
		1234567890.123456,
		0.00000001,
		99999999999999.0,
		123456789012345.0,
		1e-10, 1e-20, 1e-100, 1e-300,
		1.23e15, 9.99e307,
		// Powers of 2
		0.25, 0.125, 0.0625,
		2, 4, 8, 16, 32, 64, 128, 256, 512, 1024,
		// Interesting bit patterns
		math.Float64frombits(0x0010000000000000), // smallest normal
		math.Float64frombits(0x000FFFFFFFFFFFFF), // largest subnormal
		math.Float64frombits(0x0000000000000001), // smallest subnormal
		math.Float64frombits(0x7FEFFFFFFFFFFFFF), // largest normal
	}

	for _, f := range testValues {
		v := S{V: f}
		got, errCode := nativeEncodeHelper(t, &v)
		if errCode != vjOK {
			t.Fatalf("f=%v: errCode=%d", f, errCode)
		}
		want := `{"v":` + strconv.FormatFloat(f, 'f', -1, 64) + `}`
		if got != want {
			t.Errorf("f=%v (bits=%016x)\n  got  %q\n  want %q",
				f, math.Float64bits(f), got, want)
		}
	}

	// Also test negative zero specifically.
	negZero := math.Copysign(0, -1)
	v := S{V: negZero}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("neg zero: errCode=%d", errCode)
	}
	want := `{"v":` + strconv.FormatFloat(negZero, 'f', -1, 64) + `}`
	if got != want {
		t.Errorf("neg zero: got %q, want %q", got, want)
	}
}

func TestNativeEncodeFloat32BitExact(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		V float32 `json:"v"`
	}

	testValues := []float32{
		0, 1, -1, 0.1, 0.2, 0.3, 0.5, 1.5, 9.5,
		10, 100, 1000, 10000, 100000,
		-0.1, -0.5, -1.5,
		math.SmallestNonzeroFloat32,
		math.MaxFloat32,
		0.1 + 0.2,
		float32(math.Pi),
		float32(math.E),
		1234.5678,
		0.00001,
		3.4028235e+38,   // max float32
		1.1754944e-38,   // smallest normal float32
		1.4e-45,         // smallest subnormal float32
		-3.4028235e+38,  // min float32
		0.25, 0.125, 0.0625,
		2, 4, 8, 16, 32, 64, 128, 256, 512, 1024,
		// Interesting bit patterns
		math.Float32frombits(0x00800000), // smallest normal
		math.Float32frombits(0x007FFFFF), // largest subnormal
		math.Float32frombits(0x00000001), // smallest subnormal
		math.Float32frombits(0x7F7FFFFF), // largest normal
	}

	for _, f := range testValues {
		v := S{V: f}
		got, errCode := nativeEncodeHelper(t, &v)
		if errCode != vjOK {
			t.Fatalf("f=%v: errCode=%d", f, errCode)
		}
		// Go's strconv.AppendFloat with bitSize=32: cast to float64 first
		want := `{"v":` + strconv.FormatFloat(float64(f), 'f', -1, 32) + `}`
		if got != want {
			t.Errorf("f=%v (bits=%08x)\n  got  %q\n  want %q",
				f, math.Float32bits(f), got, want)
		}
	}

	// Test negative zero.
	negZero := float32(math.Copysign(0, -1))
	v := S{V: negZero}
	got, errCode := nativeEncodeHelper(t, &v)
	if errCode != vjOK {
		t.Fatalf("neg zero: errCode=%d", errCode)
	}
	want := `{"v":` + strconv.FormatFloat(float64(negZero), 'f', -1, 32) + `}`
	if got != want {
		t.Errorf("neg zero: got %q, want %q", got, want)
	}
}

// ================================================================
// omitempty tests
// ================================================================

func TestNativeEncodeOmitemptyZeroValues(t *testing.T) {
	// All zero-valued omitempty fields should be skipped.
	type S struct {
		ID     int     `json:"id"`
		Name   string  `json:"name,omitempty"`
		Score  float64 `json:"score,omitempty"`
		Active bool    `json:"active,omitempty"`
		Count  int32   `json:"count,omitempty"`
	}
	v := S{ID: 42} // only ID is non-zero; rest are omitempty + zero
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":42}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyNonZero(t *testing.T) {
	// Non-zero omitempty fields should be encoded normally.
	type S struct {
		ID     int     `json:"id"`
		Name   string  `json:"name,omitempty"`
		Score  float64 `json:"score,omitempty"`
		Active bool    `json:"active,omitempty"`
	}
	v := S{ID: 1, Name: "hello", Score: 3.14, Active: true}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":1,"name":"hello","score":3.14,"active":true}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyMixed(t *testing.T) {
	// Mix of zero and non-zero omitempty fields.
	type S struct {
		A int    `json:"a,omitempty"`
		B string `json:"b"`
		C int    `json:"c,omitempty"`
		D string `json:"d,omitempty"`
		E int    `json:"e"`
	}
	v := S{A: 0, B: "always", C: 99, D: "", E: 0}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":"always","c":99,"e":0}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyAllZero(t *testing.T) {
	// All fields are omitempty and zero → empty object.
	type S struct {
		A int     `json:"a,omitempty"`
		B string  `json:"b,omitempty"`
		C float64 `json:"c,omitempty"`
		D bool    `json:"d,omitempty"`
	}
	v := S{}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyFirstFieldSkipped(t *testing.T) {
	// First field is omitempty+zero; second is non-omitempty.
	// Verifies comma logic when leading fields are skipped.
	type S struct {
		A int    `json:"a,omitempty"`
		B string `json:"b"`
	}
	v := S{A: 0, B: "hello"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":"hello"}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNativeEncodeOmitemptyAllTypes(t *testing.T) {
	// Verify omitempty for all numeric types and string.
	type S struct {
		B   bool    `json:"b,omitempty"`
		I   int     `json:"i,omitempty"`
		I8  int8    `json:"i8,omitempty"`
		I16 int16   `json:"i16,omitempty"`
		I32 int32   `json:"i32,omitempty"`
		I64 int64   `json:"i64,omitempty"`
		U   uint    `json:"u,omitempty"`
		U8  uint8   `json:"u8,omitempty"`
		U16 uint16  `json:"u16,omitempty"`
		U32 uint32  `json:"u32,omitempty"`
		U64 uint64  `json:"u64,omitempty"`
		F32 float32 `json:"f32,omitempty"`
		F64 float64 `json:"f64,omitempty"`
		S   string  `json:"s,omitempty"`
	}
	// All zero → empty object.
	v := S{}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{}` {
		t.Errorf("all zero: got %s, want {}", got)
	}

	// All non-zero → all fields present.
	v2 := S{
		B: true, I: 1, I8: 2, I16: 3, I32: 4, I64: 5,
		U: 6, U8: 7, U16: 8, U32: 9, U64: 10,
		F32: 1.5, F64: 2.5, S: "hi",
	}
	got2, err := Marshal(&v2)
	if err != nil {
		t.Fatal(err)
	}
	// Compare with encoding/json.
	want2, _ := json.Marshal(&v2)
	if string(got2) != string(want2) {
		t.Errorf("all non-zero:\n  got  %s\n  want %s", got2, want2)
	}
}

func TestNativeEncodeOmitemptyConsistency(t *testing.T) {
	// Compare native encoder output with encoding/json for various inputs.
	type S struct {
		ID     int     `json:"id"`
		Name   string  `json:"name,omitempty"`
		Score  float64 `json:"score,omitempty"`
		Active bool    `json:"active,omitempty"`
		Tag    string  `json:"tag,omitempty"`
	}
	tests := []S{
		{ID: 0},
		{ID: 1, Name: "a"},
		{ID: 2, Score: 3.14},
		{ID: 3, Active: true},
		{ID: 4, Name: "x", Score: 1.0, Active: true, Tag: "test"},
		{},
	}
	for i, v := range tests {
		got, err := Marshal(&v)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		want, _ := json.Marshal(&v)
		if string(got) != string(want) {
			t.Errorf("case %d:\n  got  %s\n  want %s", i, got, want)
		}
	}
}

// ================================================================
// Hot Resume (断点续传) tests
// ================================================================

func TestCanPartialNativeEncode(t *testing.T) {
	// Struct with native + map fields should be partially native-encodable.
	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
	}
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Codec.(*StructCodec)

	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with map field")
	}
	if !canPartialNativeEncode(dec) {
		t.Error("expected canPartialNativeEncode=true for struct with int + map fields")
	}
}

func TestCanPartialNativeEncodeAllUnsupported(t *testing.T) {
	// Struct with only unsupported fields should NOT be partially native-encodable.
	type S struct {
		Tags map[string]string `json:"tags"`
		Data []int             `json:"data"`
	}
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Codec.(*StructCodec)

	if canPartialNativeEncode(dec) {
		t.Error("expected canPartialNativeEncode=false for struct with only unsupported fields")
	}
}

func TestGetNativeOpsPartialMode(t *testing.T) {
	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
	}
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Codec.(*StructCodec)

	ops, mode := dec.getNativeOps()
	if mode != nativePartial {
		t.Fatalf("expected nativePartial, got %d", mode)
	}
	if ops == nil {
		t.Fatal("expected non-nil ops for nativePartial")
	}
}

func TestHotResumeMapFieldMiddle(t *testing.T) {
	// Map field in the middle: C handles ID, Go handles Tags, C resumes for Name.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
		Name string            `json:"name"`
	}

	v := S{ID: 42, Tags: map[string]string{"a": "1"}, Name: "hello"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeMapFieldFirst(t *testing.T) {
	// Map field is the first field: Go handles it, C resumes for the rest.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		Tags map[string]string `json:"tags"`
		ID   int               `json:"id"`
		Name string            `json:"name"`
	}

	v := S{Tags: map[string]string{"x": "y"}, ID: 1, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeMapFieldLast(t *testing.T) {
	// Map field is the last field: C handles all fields until map, Go finishes.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Name string            `json:"name"`
		Tags map[string]string `json:"tags"`
	}

	v := S{ID: 42, Name: "hello", Tags: map[string]string{"k": "v"}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeSliceField(t *testing.T) {
	// Slice field triggers fallback.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID    int    `json:"id"`
		Items []int  `json:"items"`
		Name  string `json:"name"`
	}

	v := S{ID: 1, Items: []int{10, 20, 30}, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeInterfaceField(t *testing.T) {
	// Interface field triggers fallback.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID    int    `json:"id"`
		Value any    `json:"value"`
		Name  string `json:"name"`
	}

	v := S{ID: 1, Value: "dynamic", Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerField(t *testing.T) {
	// Pointer field is now handled natively by C engine.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int     `json:"id"`
		Name *string `json:"name"`
		Age  int     `json:"age"`
	}

	name := "Alice"
	v := S{ID: 1, Name: &name, Age: 30}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerFieldNil(t *testing.T) {
	// Nil pointer field → JSON null, handled natively.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int     `json:"id"`
		Name *string `json:"name"`
		Age  int     `json:"age"`
	}

	v := S{ID: 1, Name: nil, Age: 30}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerPrimitiveTypes(t *testing.T) {
	// Various pointer-to-primitive types, all handled by C engine.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		PBool    *bool    `json:"p_bool"`
		PInt     *int     `json:"p_int"`
		PInt8    *int8    `json:"p_int8"`
		PInt16   *int16   `json:"p_int16"`
		PInt32   *int32   `json:"p_int32"`
		PInt64   *int64   `json:"p_int64"`
		PUint    *uint    `json:"p_uint"`
		PUint8   *uint8   `json:"p_uint8"`
		PUint16  *uint16  `json:"p_uint16"`
		PUint32  *uint32  `json:"p_uint32"`
		PUint64  *uint64  `json:"p_uint64"`
		PFloat32 *float32 `json:"p_float32"`
		PFloat64 *float64 `json:"p_float64"`
		PString  *string  `json:"p_string"`
	}

	b := true
	i := 42
	i8 := int8(-7)
	i16 := int16(-300)
	i32 := int32(-100000)
	i64 := int64(-9999999999)
	u := uint(100)
	u8 := uint8(255)
	u16 := uint16(65535)
	u32 := uint32(4000000000)
	u64 := uint64(18000000000000000000)
	f32 := float32(3.14)
	f64 := 2.718281828
	s := "hello\nworld"

	v := S{
		PBool: &b, PInt: &i, PInt8: &i8, PInt16: &i16, PInt32: &i32, PInt64: &i64,
		PUint: &u, PUint8: &u8, PUint16: &u16, PUint32: &u32, PUint64: &u64,
		PFloat32: &f32, PFloat64: &f64, PString: &s,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerAllNil(t *testing.T) {
	// All pointer fields nil → all "null".
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		A *int    `json:"a"`
		B *string `json:"b"`
		C *bool   `json:"c"`
	}

	v := S{}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerStruct(t *testing.T) {
	// *PureStruct: non-nil → nested JSON object.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		ID int    `json:"id"`
		P  *Inner `json:"p"`
	}

	v := Outer{ID: 1, P: &Inner{X: 42, Y: "hello"}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerStructNil(t *testing.T) {
	// *PureStruct: nil → "null".
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int `json:"x"`
	}
	type Outer struct {
		ID int    `json:"id"`
		P  *Inner `json:"p"`
	}

	v := Outer{ID: 1, P: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerOmitemptyNil(t *testing.T) {
	// *int with omitempty, nil → field omitted.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int  `json:"id"`
		Val *int `json:"val,omitempty"`
	}

	v := S{ID: 1, Val: nil}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerOmitemptyNonNil(t *testing.T) {
	// *int with omitempty, non-nil → field present.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int  `json:"id"`
		Val *int `json:"val,omitempty"`
	}

	val := 42
	v := S{ID: 1, Val: &val}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerOmitemptyZeroValue(t *testing.T) {
	// *int with omitempty, pointing to zero value → field present (not omitted).
	// Only nil pointers are considered "empty", not zero-valued pointees.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int  `json:"id"`
		Val *int `json:"val,omitempty"`
	}

	zero := 0
	v := S{ID: 1, Val: &zero}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerMixedWithRegularFields(t *testing.T) {
	// Struct with mix of regular and pointer fields → should be nativeFull.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		A int `json:"a"`
	}
	type S struct {
		ID    int     `json:"id"`
		Name  *string `json:"name"`
		Score float64 `json:"score"`
		Inner *Inner  `json:"inner"`
		OK    bool    `json:"ok"`
	}

	name := "test"
	v := S{ID: 1, Name: &name, Score: 99.5, Inner: &Inner{A: 7}, OK: true}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumePointerToNonNativeStruct(t *testing.T) {
	// *MixedStruct (contains map) → falls back to Go for pointer field.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		Tags map[string]string `json:"tags"`
	}
	type Outer struct {
		ID int    `json:"id"`
		P  *Inner `json:"p"`
	}

	v := Outer{ID: 1, P: &Inner{Tags: map[string]string{"a": "b"}}}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumePointerToPointer(t *testing.T) {
	// **int → falls back to Go.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID  int   `json:"id"`
		Val **int `json:"val"`
	}

	inner := 42
	ptr := &inner
	v := S{ID: 1, Val: &ptr}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativePointerConsistencyWithStdlib(t *testing.T) {
	// Comprehensive stdlib consistency check for pointer scenarios.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}

	type S struct {
		A  *int     `json:"a"`
		B  *string  `json:"b"`
		C  *bool    `json:"c"`
		D  *float64 `json:"d"`
		E  *Inner   `json:"e"`
		F  *int     `json:"f,omitempty"`
		G  *string  `json:"g,omitempty"`
		H  int      `json:"h"`
	}

	a := 100
	b := "hello \"world\""
	c := false
	d := 1.5
	g := ""

	tests := []struct {
		name string
		val  S
	}{
		{"all non-nil", S{A: &a, B: &b, C: &c, D: &d, E: &Inner{X: 1, Y: "y"}, F: &a, G: &g, H: 99}},
		{"all nil", S{H: 42}},
		{"mixed nil/non-nil", S{A: &a, B: nil, C: &c, D: nil, E: nil, F: nil, G: &g, H: 0}},
		{"omitempty nil", S{A: &a, F: nil, G: nil, H: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(&tt.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(&tt.val)
			if string(got) != string(want) {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}

func TestNativePointerToCustomMarshaler(t *testing.T) {
	// *big.Int has MarshalJSON on the element type — must fall back to Go,
	// not be treated as native *Struct.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Wallet struct {
		Owner   string   `json:"owner"`
		Balance *big.Int `json:"balance"`
	}

	w := Wallet{Owner: "alice", Balance: new(big.Int).SetInt64(42)}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(w)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeMultipleFallbacks(t *testing.T) {
	// Multiple fallback fields: C→Go→C→Go→C pattern.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		A int               `json:"a"`
		B map[string]string `json:"b"`
		C string            `json:"c"`
		D []int             `json:"d"`
		E bool              `json:"e"`
	}

	v := S{
		A: 1,
		B: map[string]string{"x": "y"},
		C: "middle",
		D: []int{10, 20},
		E: true,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeAllFallbackFields(t *testing.T) {
	// All fields are fallback — still uses hot resume path.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		A map[string]string `json:"a"`
		B []int             `json:"b"`
		C any               `json:"c"`
	}

	v := S{
		A: map[string]string{"k": "v"},
		B: []int{1, 2},
		C: 42,
	}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeWithOmitempty(t *testing.T) {
	// Fallback field with native omitempty fields.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID    int               `json:"id"`
		Empty string            `json:"empty,omitempty"`
		Tags  map[string]string `json:"tags"`
		Name  string            `json:"name,omitempty"`
	}

	// Case 1: omitempty fields are zero, skipped.
	v1 := S{ID: 42, Tags: map[string]string{"a": "1"}}
	got1, err := Marshal(&v1)
	if err != nil {
		t.Fatal(err)
	}
	want1, _ := json.Marshal(&v1)
	if string(got1) != string(want1) {
		t.Errorf("case1: got  %s\nwant %s", got1, want1)
	}

	// Case 2: omitempty fields are non-zero.
	v2 := S{ID: 42, Empty: "yes", Tags: map[string]string{"a": "1"}, Name: "bob"}
	got2, err := Marshal(&v2)
	if err != nil {
		t.Fatal(err)
	}
	want2, _ := json.Marshal(&v2)
	if string(got2) != string(want2) {
		t.Errorf("case2: got  %s\nwant %s", got2, want2)
	}
}

func TestHotResumeEmptyMapField(t *testing.T) {
	// Empty map still goes through fallback path.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
		Name string            `json:"name"`
	}

	v := S{ID: 1, Tags: map[string]string{}, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeNilMapField(t *testing.T) {
	// Nil map still goes through fallback path.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type S struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
		Name string            `json:"name"`
	}

	v := S{ID: 1, Tags: nil, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeNestedImpureStruct(t *testing.T) {
	// Nested struct with unsupported fields gets fallback at depth=0.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X     int    `json:"x"`
		Items []int  `json:"items"`
	}
	type Outer struct {
		ID    int    `json:"id"`
		Inner Inner  `json:"inner"`
		Name  string `json:"name"`
	}

	v := Outer{ID: 1, Inner: Inner{X: 42, Items: []int{1, 2, 3}}, Name: "test"}
	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHotResumeConsistencyWithStdlib(t *testing.T) {
	// Comprehensive consistency test: compare Marshal output with encoding/json
	// for various mixed structs.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Mixed struct {
		ID      int               `json:"id"`
		Tags    map[string]string `json:"tags"`
		Name    string            `json:"name"`
		Items   []int             `json:"items"`
		Score   float64           `json:"score"`
		Value   any               `json:"value"`
		Active  bool              `json:"active"`
	}

	tests := []struct {
		name string
		val  Mixed
	}{
		{
			name: "all_populated",
			val: Mixed{
				ID:     1,
				Tags:   map[string]string{"a": "1", "b": "2"},
				Name:   "test",
				Items:  []int{10, 20, 30},
				Score:  3.14,
				Value:  "dynamic",
				Active: true,
			},
		},
		{
			name: "nil_collections",
			val: Mixed{
				ID:     2,
				Tags:   nil,
				Name:   "test2",
				Items:  nil,
				Score:  0,
				Value:  nil,
				Active: false,
			},
		},
		{
			name: "empty_collections",
			val: Mixed{
				ID:     3,
				Tags:   map[string]string{},
				Name:   "",
				Items:  []int{},
				Score:  -1.5,
				Value:  42,
				Active: true,
			},
		},
		{
			name: "nested_value",
			val: Mixed{
				ID:    4,
				Tags:  map[string]string{"key": "val"},
				Name:  "nested",
				Items: []int{1},
				Score: 100,
				Value: map[string]any{"nested": true},
			},
		},
		{
			name: "zero_struct",
			val:  Mixed{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(&tt.val)
			if err != nil {
				t.Fatal(err)
			}
			// Compare by round-tripping through JSON unmarshal to handle
			// non-deterministic map key ordering.
			var gotMap, wantMap any
			want, _ := json.Marshal(&tt.val)
			if err := json.Unmarshal(got, &gotMap); err != nil {
				t.Fatalf("failed to unmarshal our output: %v\noutput: %s", err, got)
			}
			if err := json.Unmarshal(want, &wantMap); err != nil {
				t.Fatalf("failed to unmarshal stdlib output: %v", err)
			}
			if !reflect.DeepEqual(gotMap, wantMap) {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}

// ================================================================
// []NativeStruct batch encoding tests
// ================================================================

func TestNativeSliceOfStruct(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	items := []Item{
		{ID: 1, Name: "alice"},
		{ID: 2, Name: "bob"},
		{ID: 3, Name: "charlie"},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructEmpty(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		X int `json:"x"`
	}

	items := []Item{}
	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructNil(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		X int `json:"x"`
	}

	var items []Item
	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructSingleElement(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		A int     `json:"a"`
		B string  `json:"b"`
		C float64 `json:"c"`
		D bool    `json:"d"`
	}

	items := []Item{{A: 42, B: "hello", C: 3.14, D: true}}
	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructLarge(t *testing.T) {
	// Large slice to force buffer growth (BUF_FULL resume).
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Score int    `json:"score"`
	}

	items := make([]Item, 500)
	for i := range items {
		items[i] = Item{ID: i, Name: "user_name_with_some_length", Score: i * 100}
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("output mismatch for 500-element slice\ngot  len=%d\nwant len=%d", len(got), len(want))
	}
}

func TestNativeSliceOfStructNested(t *testing.T) {
	// Struct with nested struct — still fully native.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	type Outer struct {
		ID    int   `json:"id"`
		Inner Inner `json:"inner"`
	}

	items := []Outer{
		{ID: 1, Inner: Inner{X: 10, Y: "a"}},
		{ID: 2, Inner: Inner{X: 20, Y: "b"}},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructWithPointers(t *testing.T) {
	// Struct with pointer fields — should be fully native.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int     `json:"id"`
		Name *string `json:"name"`
		Val  *int    `json:"val"`
	}

	name := "test"
	val := 42
	items := []Item{
		{ID: 1, Name: &name, Val: &val},
		{ID: 2, Name: nil, Val: nil},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructWithOmitempty(t *testing.T) {
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name,omitempty"`
		Val  int    `json:"val,omitempty"`
	}

	items := []Item{
		{ID: 1, Name: "alice", Val: 100},
		{ID: 2, Name: "", Val: 0},
		{ID: 3, Name: "charlie", Val: 0},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestSliceOfNonNativeStructFallsBack(t *testing.T) {
	// Struct with map field is not nativeFull — should use Go loop.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		ID   int               `json:"id"`
		Tags map[string]string `json:"tags"`
	}

	items := []Item{
		{ID: 1, Tags: map[string]string{"a": "b"}},
		{ID: 2, Tags: map[string]string{"c": "d"}},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceOfStructConsistencyWithStdlib(t *testing.T) {
	// Comprehensive stdlib consistency across various struct shapes.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Inner struct {
		A int    `json:"a"`
		B string `json:"b"`
	}

	type S struct {
		ID    int     `json:"id"`
		Score float64 `json:"score"`
		Inner Inner   `json:"inner"`
		OK    bool    `json:"ok"`
	}

	items := []S{
		{ID: 1, Score: 99.5, Inner: Inner{A: 10, B: "x"}, OK: true},
		{ID: 2, Score: 0, Inner: Inner{A: 0, B: ""}, OK: false},
		{ID: 3, Score: -1.5, Inner: Inner{A: -1, B: "hello \"world\""}, OK: true},
	}

	got, err := Marshal(&items)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&items)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNativeSliceInStructField(t *testing.T) {
	// []NativeStruct as a field of another struct — triggers hot resume
	// for the outer struct, but the slice itself should be batch-encoded.
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	type Item struct {
		X int `json:"x"`
	}
	type Wrapper struct {
		Name  string `json:"name"`
		Items []Item `json:"items"`
	}

	w := Wrapper{Name: "test", Items: []Item{{X: 1}, {X: 2}, {X: 3}}}
	got, err := Marshal(&w)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(&w)
	if string(got) != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
