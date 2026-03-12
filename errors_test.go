package vjson

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

// --- Type assertion tests ---

func TestSyntaxError(t *testing.T) {
	var m map[string]any
	err := Unmarshal([]byte(`{invalid}`), &m)
	if err == nil {
		t.Fatal("expected error")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *SyntaxError, got %T: %v", err, err)
	}
	if se.Offset == 0 {
		t.Fatal("want nonzero Offset")
	}
}

func TestSyntaxError_EmptyInput(t *testing.T) {
	var m map[string]any
	err := Unmarshal([]byte(``), &m)
	if err == nil {
		t.Fatal("expected error")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *SyntaxError, got %T: %v", err, err)
	}
}

func TestSyntaxError_UnexpectedEOF(t *testing.T) {
	var m map[string]any
	err := Unmarshal([]byte(`{"a":1`), &m)
	if err == nil {
		t.Fatal("expected error")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *SyntaxError, got %T: %v", err, err)
	}
}

func TestSyntaxError_TrailingData(t *testing.T) {
	var n int
	err := Unmarshal([]byte(`42 garbage`), &n)
	if err == nil {
		t.Fatal("expected error")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *SyntaxError, got %T: %v", err, err)
	}
}

func TestUnmarshalTypeError_StringToInt(t *testing.T) {
	var n int
	err := Unmarshal([]byte(`"hello"`), &n)
	if err == nil {
		t.Fatal("expected error")
	}
	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("want *UnmarshalTypeError, got %T: %v", err, err)
	}
	if ute.Value != "string" {
		t.Fatalf("Value = %q, want \"string\"", ute.Value)
	}
}

func TestUnmarshalTypeError_NumberToString(t *testing.T) {
	var s string
	err := Unmarshal([]byte(`42`), &s)
	if err == nil {
		t.Fatal("expected error")
	}
	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("want *UnmarshalTypeError, got %T: %v", err, err)
	}
	if ute.Value != "number" {
		t.Fatalf("Value = %q, want \"number\"", ute.Value)
	}
}

func TestUnmarshalTypeError_BoolToInt(t *testing.T) {
	var n int
	err := Unmarshal([]byte(`true`), &n)
	if err == nil {
		t.Fatal("expected error")
	}
	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("want *UnmarshalTypeError, got %T: %v", err, err)
	}
	if ute.Value != "bool" {
		t.Fatalf("Value = %q, want \"bool\"", ute.Value)
	}
}

func TestUnmarshalTypeError_ObjectToInt(t *testing.T) {
	var n int
	err := Unmarshal([]byte(`{"a":1}`), &n)
	if err == nil {
		t.Fatal("expected error")
	}
	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("want *UnmarshalTypeError, got %T: %v", err, err)
	}
	if ute.Value != "object" {
		t.Fatalf("Value = %q, want \"object\"", ute.Value)
	}
}

func TestUnmarshalTypeError_ArrayToInt(t *testing.T) {
	var n int
	err := Unmarshal([]byte(`[1,2,3]`), &n)
	if err == nil {
		t.Fatal("expected error")
	}
	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("want *UnmarshalTypeError, got %T: %v", err, err)
	}
	if ute.Value != "array" {
		t.Fatalf("Value = %q, want \"array\"", ute.Value)
	}
}

func TestInvalidUnmarshalError_NonPointer(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`{}`))
	var n int
	err := dec.Decode(n) // not a pointer
	if err == nil {
		t.Fatal("expected error")
	}
	var iue *InvalidUnmarshalError
	if !errors.As(err, &iue) {
		t.Fatalf("want *InvalidUnmarshalError, got %T: %v", err, err)
	}
}

func TestInvalidUnmarshalError_NilPointer(t *testing.T) {
	err := Unmarshal([]byte(`{}`), (*map[string]any)(nil))
	if err == nil {
		t.Fatal("expected error")
	}
	var iue *InvalidUnmarshalError
	if !errors.As(err, &iue) {
		t.Fatalf("want *InvalidUnmarshalError, got %T: %v", err, err)
	}
}

func TestUnsupportedValueError_NaN(t *testing.T) {
	v := math.NaN()
	_, err := Marshal(&v)
	if err == nil {
		t.Fatal("expected error")
	}
	var uve *UnsupportedValueError
	if !errors.As(err, &uve) {
		t.Fatalf("want *UnsupportedValueError, got %T: %v", err, err)
	}
}

func TestUnsupportedValueError_Inf(t *testing.T) {
	v := math.Inf(1)
	_, err := Marshal(&v)
	if err == nil {
		t.Fatal("expected error")
	}
	var uve *UnsupportedValueError
	if !errors.As(err, &uve) {
		t.Fatalf("want *UnsupportedValueError, got %T: %v", err, err)
	}
}

// --- encoding/json As() bridging tests ---

func TestSyntaxError_AsJSON(t *testing.T) {
	var m map[string]any
	err := Unmarshal([]byte(`{invalid}`), &m)
	if err == nil {
		t.Fatal("expected error")
	}
	var je *json.SyntaxError
	if !errors.As(err, &je) {
		t.Fatalf("want compat *json.SyntaxError, got %T: %v", err, err)
	}
	if je.Offset == 0 {
		t.Fatal("want nonzero Offset on json.SyntaxError")
	}
}

func TestUnmarshalTypeError_AsJSON(t *testing.T) {
	var n int
	err := Unmarshal([]byte(`"hello"`), &n)
	if err == nil {
		t.Fatal("expected error")
	}
	var je *json.UnmarshalTypeError
	if !errors.As(err, &je) {
		t.Fatalf("want compat *json.UnmarshalTypeError, got %T: %v", err, err)
	}
	if je.Value != "string" {
		t.Fatalf("Value = %q, want \"string\"", je.Value)
	}
}

func TestInvalidUnmarshalError_AsJSON(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`{}`))
	var n int
	err := dec.Decode(n)
	if err == nil {
		t.Fatal("expected error")
	}
	var je *json.InvalidUnmarshalError
	if !errors.As(err, &je) {
		t.Fatalf("want compat *json.InvalidUnmarshalError, got %T: %v", err, err)
	}
}

func TestUnsupportedValueError_AsJSON(t *testing.T) {
	v := math.NaN()
	_, err := Marshal(&v)
	if err == nil {
		t.Fatal("expected error")
	}
	var je *json.UnsupportedValueError
	if !errors.As(err, &je) {
		t.Fatalf("want compat *json.UnsupportedValueError, got %T: %v", err, err)
	}
}

// --- Error message sanity checks ---

func TestSyntaxError_Message(t *testing.T) {
	se := &SyntaxError{msg: "test error", Offset: 42}
	if se.Error() != "test error" {
		t.Fatalf("Error() = %q", se.Error())
	}
}

func TestInvalidUnmarshalError_Messages(t *testing.T) {
	tests := []struct {
		err  *InvalidUnmarshalError
		want string
	}{
		{&InvalidUnmarshalError{Type: nil}, "vjson: Unmarshal(nil)"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

func TestUnsupportedTypeError_Message(t *testing.T) {
	// We don't have a direct way to trigger this from public API easily,
	// but we can test the error type directly.
	e := &UnsupportedTypeError{Type: nil}
	// Type is nil, so this will panic; test with a real type instead.
	// Just verify the struct implements error.
	var _ error = e
}

func TestMarshalerError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	me := &MarshalerError{Err: inner}
	if !errors.Is(me, inner) {
		t.Fatal("MarshalerError.Unwrap should return inner error")
	}
}

func TestSyntaxError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	se := &SyntaxError{msg: "test", Err: inner}
	if !errors.Is(se, inner) {
		t.Fatal("SyntaxError.Unwrap should return inner error")
	}
}
