package vjson

import (
	"strings"
	"testing"
	"unsafe"
)

// ---------- helpers ----------

// isZeroCopy returns true if the string data pointer lies within the given buffer.
func isZeroCopy(s string, buf []byte) bool {
	if len(s) == 0 || len(buf) == 0 {
		return false
	}
	sp := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	lo := uintptr(unsafe.Pointer(&buf[0]))
	hi := lo + uintptr(len(buf))
	return sp >= lo && sp < hi
}

// corruptBuf overwrites every byte in buf.
func corruptBuf(buf []byte) {
	for i := range buf {
		buf[i] = 'X'
	}
}

// ---------- field-level copy tag ----------

func TestCopyTag_FieldLevel(t *testing.T) {
	type S struct {
		Copied  string `json:"copied,copy"`
		NoCopy  string `json:"noCopy"`
	}
	input := []byte(`{"copied":"hello","noCopy":"world"}`)
	buf := make([]byte, len(input))
	copy(buf, input)

	var v S
	if err := Unmarshal(buf, &v); err != nil {
		t.Fatal(err)
	}

	if v.Copied != "hello" || v.NoCopy != "world" {
		t.Fatalf("unexpected values: %+v", v)
	}

	// Copied field must NOT be zero-copy (was copied).
	if isZeroCopy(v.Copied, buf) {
		t.Fatal("copied field is still zero-copy")
	}
	// NoCopy field should be zero-copy.
	if !isZeroCopy(v.NoCopy, buf) {
		t.Fatal("noCopy field is not zero-copy")
	}

	// Verify: corrupting buf does not affect the copied field.
	corruptBuf(buf)
	if v.Copied != "hello" {
		t.Fatalf("copied field corrupted: %q", v.Copied)
	}
}

func TestCopyTag_OnlyAffectsStringKind(t *testing.T) {
	// The copy tag on non-string fields should be silently ignored.
	type S struct {
		Num int    `json:"num,copy"`
		Str string `json:"str,copy"`
	}
	input := []byte(`{"num":42,"str":"hello"}`)
	var v S
	if err := Unmarshal(input, &v); err != nil {
		t.Fatal(err)
	}
	if v.Num != 42 {
		t.Fatalf("Num = %d, want 42", v.Num)
	}
	if v.Str != "hello" {
		t.Fatalf("Str = %q, want hello", v.Str)
	}
}

// ---------- global WithCopyString option ----------

func TestWithCopyString_Struct(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	buf := []byte(`{"name":"hello"}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var v S
	if err := Unmarshal(input, &v, WithCopyString()); err != nil {
		t.Fatal(err)
	}
	if v.Name != "hello" {
		t.Fatalf("Name = %q", v.Name)
	}

	// Must not be zero-copy.
	if isZeroCopy(v.Name, input) {
		t.Fatal("global copy: string is still zero-copy")
	}

	corruptBuf(input)
	if v.Name != "hello" {
		t.Fatalf("string corrupted: %q", v.Name)
	}
}

func TestWithCopyString_MapStringString(t *testing.T) {
	buf := []byte(`{"key":"value"}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var m map[string]string
	if err := Unmarshal(input, &m, WithCopyString()); err != nil {
		t.Fatal(err)
	}

	corruptBuf(input)

	val, ok := m["key"]
	if !ok {
		t.Fatal("key not found after buffer corruption")
	}
	if val != "value" {
		t.Fatalf("value = %q, want %q", val, "value")
	}
}

func TestWithCopyString_MapAny(t *testing.T) {
	buf := []byte(`{"key":"value"}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var m map[string]any
	if err := Unmarshal(input, &m, WithCopyString()); err != nil {
		t.Fatal(err)
	}

	corruptBuf(input)

	val, ok := m["key"].(string)
	if !ok {
		t.Fatal("value not string")
	}
	if val != "value" {
		t.Fatalf("value = %q", val)
	}
}

func TestWithCopyString_Interface(t *testing.T) {
	type S struct {
		Data any `json:"data"`
	}
	buf := []byte(`{"data":"hello"}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var v S
	if err := Unmarshal(input, &v, WithCopyString()); err != nil {
		t.Fatal(err)
	}

	corruptBuf(input)

	s, ok := v.Data.(string)
	if !ok {
		t.Fatal("data not string")
	}
	if s != "hello" {
		t.Fatalf("data = %q", s)
	}
}

// ---------- escaped strings ----------

func TestCopyTag_EscapedString(t *testing.T) {
	type S struct {
		Name string `json:"name,copy"`
	}
	buf := []byte(`{"name":"hello\nworld"}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var v S
	if err := Unmarshal(input, &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "hello\nworld" {
		t.Fatalf("Name = %q", v.Name)
	}

	corruptBuf(input)
	if v.Name != "hello\nworld" {
		t.Fatalf("escaped string corrupted: %q", v.Name)
	}
}

func TestWithCopyString_EscapedString(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	buf := []byte(`{"name":"hello\tworld"}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var v S
	if err := Unmarshal(input, &v, WithCopyString()); err != nil {
		t.Fatal(err)
	}
	if v.Name != "hello\tworld" {
		t.Fatalf("Name = %q", v.Name)
	}

	corruptBuf(input)
	if v.Name != "hello\tworld" {
		t.Fatalf("escaped string corrupted: %q", v.Name)
	}
}

// ---------- priority: tag OR global ----------

func TestCopyString_Priority(t *testing.T) {
	type S struct {
		WithTag    string `json:"withTag,copy"`
		WithoutTag string `json:"withoutTag"`
	}

	// Case 1: without global option — only tagged field copied.
	buf := []byte(`{"withTag":"a","withoutTag":"b"}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var v1 S
	if err := Unmarshal(input, &v1); err != nil {
		t.Fatal(err)
	}
	if isZeroCopy(v1.WithTag, input) {
		t.Fatal("tagged field should not be zero-copy")
	}
	if !isZeroCopy(v1.WithoutTag, input) {
		t.Fatal("untagged field should be zero-copy")
	}

	corruptBuf(input)
	if v1.WithTag != "a" {
		t.Fatalf("tagged field corrupted: %q", v1.WithTag)
	}

	// Case 2: with global option — both fields copied.
	input2 := []byte(`{"withTag":"a","withoutTag":"b"}`)
	buf2 := make([]byte, len(input2))
	copy(buf2, input2)

	var v2 S
	if err := Unmarshal(buf2, &v2, WithCopyString()); err != nil {
		t.Fatal(err)
	}
	if isZeroCopy(v2.WithTag, buf2) {
		t.Fatal("tagged field should not be zero-copy with global")
	}
	if isZeroCopy(v2.WithoutTag, buf2) {
		t.Fatal("untagged field should not be zero-copy with global")
	}

	corruptBuf(buf2)
	if v2.WithTag != "a" {
		t.Fatalf("tagged field corrupted with global: %q", v2.WithTag)
	}
	if v2.WithoutTag != "b" {
		t.Fatalf("untagged field corrupted with global: %q", v2.WithoutTag)
	}
}

// ---------- default behavior (zero-copy preserved) ----------

func TestDefault_ZeroCopy(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	input := []byte(`{"name":"hello"}`)

	var v S
	if err := Unmarshal(input, &v); err != nil {
		t.Fatal(err)
	}
	if !isZeroCopy(v.Name, input) {
		t.Fatal("default mode: string should be zero-copy")
	}
}

// ---------- Decoder ----------

func TestDecoder_CopyString_Option(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	dec := NewDecoder(strings.NewReader(`{"name":"hello"}`), DecoderCopyString())

	var v S
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "hello" {
		t.Fatalf("Name = %q", v.Name)
	}
}

func TestDecoder_CopyString_Method(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	dec := NewDecoder(strings.NewReader(`{"name":"hello"}`))
	dec.CopyString()

	var v S
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "hello" {
		t.Fatalf("Name = %q", v.Name)
	}
}

// ---------- combined options ----------

func TestCopyString_WithUseNumber(t *testing.T) {
	type S struct {
		Name string `json:"name"`
		Val  any    `json:"val"`
	}
	buf := []byte(`{"name":"test","val":42}`)
	input := make([]byte, len(buf))
	copy(input, buf)

	var v S
	if err := Unmarshal(input, &v, WithCopyString(), WithUseNumber()); err != nil {
		t.Fatal(err)
	}

	corruptBuf(input)
	if v.Name != "test" {
		t.Fatalf("Name corrupted: %q", v.Name)
	}
	// val should be json.Number
	if _, ok := v.Val.(interface{ String() string }); !ok {
		t.Fatalf("Val type = %T, want json.Number", v.Val)
	}
}

// ---------- empty string ----------

func TestCopyString_EmptyString(t *testing.T) {
	type S struct {
		Name string `json:"name,copy"`
	}
	input := []byte(`{"name":""}`)
	var v S
	if err := Unmarshal(input, &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "" {
		t.Fatalf("Name = %q, want empty", v.Name)
	}
}
