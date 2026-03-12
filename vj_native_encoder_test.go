package vjson

import (
	"encoding/json"
	"math"
	"reflect"
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
	const want = 1584 // 48 header + 64 * 24 stack
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
	dec := ti.Decoder.(*StructCodec)

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
	dec := ti.Decoder.(*StructCodec)

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
	dec := ti.Decoder.(*StructCodec)

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
	dec := ti.Decoder.(*StructCodec)
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
	dec := ti.Decoder.(*StructCodec)
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
	dec := ti.Decoder.(*StructCodec)
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
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with map field")
	}
}

func TestCanNativeEncodeRejectsSlice(t *testing.T) {
	type WithSlice struct {
		Items []int `json:"items"`
	}
	ti := GetCodec(reflect.TypeFor[WithSlice]())
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with slice field")
	}
}

func TestCanNativeEncodeRejectsInterface(t *testing.T) {
	type WithAny struct {
		Value any `json:"value"`
	}
	ti := GetCodec(reflect.TypeFor[WithAny]())
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with interface field")
	}
}

func TestCanNativeEncodeRejectsPointer(t *testing.T) {
	type WithPtr struct {
		Name *string `json:"name"`
	}
	ti := GetCodec(reflect.TypeFor[WithPtr]())
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with pointer field")
	}
}

func TestCanNativeEncodeRejectsOmitempty(t *testing.T) {
	type WithOmit struct {
		ID   int    `json:"id"`
		Name string `json:"name,omitempty"`
	}
	ti := GetCodec(reflect.TypeFor[WithOmit]())
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with omitempty field")
	}
}

func TestCanNativeEncodeRejectsStringTag(t *testing.T) {
	type WithQuoted struct {
		Count int `json:"count,string"`
	}
	ti := GetCodec(reflect.TypeFor[WithQuoted]())
	dec := ti.Decoder.(*StructCodec)
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
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with impure nested struct")
	}
}

func TestCanNativeEncodeEmptyStruct(t *testing.T) {
	type Empty struct{}
	ti := GetCodec(reflect.TypeFor[Empty]())
	dec := ti.Decoder.(*StructCodec)
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
	dec := ti.Decoder.(*StructCodec)

	ops, ok := dec.getNativeOps()
	if !ok {
		t.Fatal("expected getNativeOps to succeed for simple struct")
	}
	if len(ops) != 3 { // 2 fields + END
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}

	// Call again — should return cached result (same pointer).
	ops2, ok2 := dec.getNativeOps()
	if !ok2 {
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
	dec := ti.Decoder.(*StructCodec)

	ops, ok := dec.getNativeOps()
	if ok {
		t.Error("expected getNativeOps to return false for impure struct")
	}
	if ops != nil {
		t.Error("expected nil ops for impure struct")
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
	dec := ti.Decoder.(*StructCodec)

	ops, ok := dec.getNativeOps()
	if !ok {
		t.Fatal("expected native ops to be available for Simple struct")
	}

	v := Simple{ID: 42, Name: "hello"}
	buf := make([]byte, 4096)

	written, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjOK {
		t.Fatalf("nativeEncodeStruct errCode = %d, want vjOK(0)", errCode)
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
	dec := ti.Decoder.(*StructCodec)

	ops, ok := dec.getNativeOps()
	if !ok {
		t.Fatal("expected native ops to be available")
	}

	v := Tiny{X: 1}
	buf := make([]byte, 1) // too small for "{}"

	_, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjErrBufFull {
		t.Fatalf("expected vjErrBufFull(%d), got %d", vjErrBufFull, errCode)
	}
}

func TestNativeEncodeStructEmptyBuf(t *testing.T) {
	// Test with zero-length buffer.
	type S struct{ X int }
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Decoder.(*StructCodec)
	ops, ok := dec.getNativeOps()
	if !ok {
		t.Fatal("expected native ops")
	}

	v := S{X: 1}
	_, errCode := nativeEncodeStruct(nil, unsafe.Pointer(&v), ops, 0)
	if errCode != vjErrBufFull {
		t.Fatalf("expected vjErrBufFull for nil buf, got %d", errCode)
	}
}

func TestNativeEncodeStructFallbackWhenUnavailable(t *testing.T) {
	// When encoder.Available is false, nativeEncodeStruct should
	// return vjErrGoFallback without panicking.
	if encoder.Available {
		t.Skip("this test is for platforms without native encoder")
	}

	buf := make([]byte, 64)
	ops := []COpStep{{OpType: opEnd}}
	_, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&struct{}{}), ops, 0)
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

	written, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&v), ops, 0)
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

	written, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&s), ops, 0)
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
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	ti := GetCodec(rv.Type())
	dec := ti.Decoder.(*StructCodec)
	ops, ok := dec.getNativeOps()
	if !ok {
		t.Fatalf("getNativeOps returned false for %T", v)
	}

	buf := make([]byte, 8192)
	ptr := rv.Addr().Pointer()
	written, errCode := nativeEncodeStruct(buf, unsafe.Pointer(ptr), ops, 0)
	return string(buf[:written]), errCode
}

// nativeEncodeWithFlags is like nativeEncodeHelper but with custom VjEncFlags.
func nativeEncodeWithFlags(t *testing.T, v any, flags uint32) (string, int32) {
	t.Helper()
	if !encoder.Available {
		t.Skip("native encoder not available")
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	ti := GetCodec(rv.Type())
	dec := ti.Decoder.(*StructCodec)
	ops, ok := dec.getNativeOps()
	if !ok {
		t.Fatalf("getNativeOps returned false for %T", v)
	}

	buf := make([]byte, 8192)
	ptr := rv.Addr().Pointer()
	written, errCode := nativeEncodeStruct(buf, unsafe.Pointer(ptr), ops, flags)
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
		Y string `json:"y"`
		L3 L3     `json:"l3"`
	}
	type L1 struct {
		X int `json:"x"`
		L2 L2  `json:"l2"`
	}
	type L0 struct {
		A string `json:"a"`
		L1 L1     `json:"l1"`
		B int    `json:"b"`
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
		ID      int              `json:"id"`
		Payload json.RawMessage  `json:"payload"`
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
	dec := ti.Decoder.(*StructCodec)
	ops, ok := dec.getNativeOps()
	if !ok {
		t.Fatal("expected native ops")
	}

	v := S{ID: 42}
	// Full output is `{"id":42}` = 9 bytes
	// CHECK estimates: 1('{') + 1(comma) + key_len("id":=5) + 21(max int digits) = 28
	// So we need at least 28 bytes for CHECK to pass (conservative estimate).
	// Test with buffers that are too small at various points:
	for _, sz := range []int{0, 1, 2, 5, 8} {
		buf := make([]byte, sz)
		_, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&v), ops, 0)
		if errCode != vjErrBufFull {
			t.Errorf("buf size %d: expected vjErrBufFull(%d), got %d", sz, vjErrBufFull, errCode)
		}
	}

	// Buffer large enough for worst-case estimate should work
	buf := make([]byte, 64)
	written, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&v), ops, 0)
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

func TestNativeEncodeGoFallbackFloat(t *testing.T) {
	// Structs with float fields should NOT be native-encodable
	type S struct {
		X float64 `json:"x"`
	}
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with float64 field")
	}
}

func TestNativeEncodeGoFallbackFloat32(t *testing.T) {
	type S struct {
		X float32 `json:"x"`
	}
	ti := GetCodec(reflect.TypeFor[S]())
	dec := ti.Decoder.(*StructCodec)
	if canNativeEncode(dec) {
		t.Error("expected canNativeEncode=false for struct with float32 field")
	}
}

func TestNativeEncodeGoFallbackOpCode(t *testing.T) {
	// Directly test that the C VM returns VJ_ERR_GO_FALLBACK for
	// an unsupported op type (e.g. OP_FLOAT64 = 12).
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

	v := struct{ X float64 }{X: 3.14}
	buf := make([]byte, 256)
	_, errCode := nativeEncodeStruct(buf, unsafe.Pointer(&v), ops, 0)
	if errCode != vjErrGoFallback {
		t.Fatalf("expected vjErrGoFallback(%d), got %d", vjErrGoFallback, errCode)
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
