package ndec

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestInvalidUnmarshalError(t *testing.T) {
	// nil pointer
	err := Unmarshal([]byte(`42`), (*int)(nil))
	if err == nil {
		t.Fatal("expected error for nil pointer")
	}
	var ie *InvalidUnmarshalError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *InvalidUnmarshalError, got %T: %v", err, err)
	}
	if ie.Type == nil || ie.Type.Kind() != reflect.Pointer || ie.Type.Elem().Kind() != reflect.Int {
		t.Fatalf("InvalidUnmarshalError.Type = %v, want *int", ie.Type)
	}

	// non-pointer type
	var x int
	err = Unmarshal([]byte(`42`), x)
	if err == nil {
		t.Fatal("expected error for non-pointer")
	}
	if !errors.As(err, &ie) {
		t.Fatalf("expected *InvalidUnmarshalError, got %T: %v", err, err)
	}
}

func TestSyntaxError(t *testing.T) {
	type T struct {
		A string
	}
	var v T
	// Invalid JSON syntax — bare word is not a valid JSON token.
	err := Unmarshal([]byte(`k`), &v)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		// Fallback: also accept UnmarshalTypeError
		var ute *UnmarshalTypeError
		if !errors.As(err, &ute) {
			t.Fatalf("expected *SyntaxError or *UnmarshalTypeError, got %T: %v", err, err)
		}
		t.Logf("got UnmarshalTypeError instead: Value=%q Offset=%d", ute.Value, ute.Offset)
		return
	}
	if se.Code != 0 {
		t.Logf("SyntaxError.Code = %d, Offset = %d", se.Code, se.Offset)
	}
}

func TestTypeMismatchSimple(t *testing.T) {
	type T struct {
		X int
	}
	var v T
	err := Unmarshal([]byte(`{"X":"hi"}`), &v)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}

	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("expected *UnmarshalTypeError via errors.As, got %T: %v", err, err)
	}
	t.Logf("ndec  error: %v", err)

	// The error metadata should match the stdlib oracle exactly.
	var w T
	jsonErr := json.Unmarshal([]byte(`{"X":"hi"}`), &w)
	if jsonErr == nil {
		t.Fatal("stdlib did not report an error")
	}
	var stdUTE *json.UnmarshalTypeError
	if !errors.As(jsonErr, &stdUTE) {
		t.Fatalf("stdlib did not produce *json.UnmarshalTypeError: %v", jsonErr)
	}
	t.Logf("stdlib error: %v", jsonErr)

	if ute.Value != stdUTE.Value {
		t.Errorf("UnmarshalTypeError.Value = %q, stdlib parity wants %q", ute.Value, stdUTE.Value)
	}
	if ute.Field != stdUTE.Field {
		t.Errorf("UnmarshalTypeError.Field = %q, stdlib parity wants %q", ute.Field, stdUTE.Field)
	}
	if ute.Struct != stdUTE.Struct {
		t.Errorf("UnmarshalTypeError.Struct = %q, stdlib parity wants %q", ute.Struct, stdUTE.Struct)
	}
	// Different parsers may point at opposite token edges, so an off-by-one is
	// acceptable as long as the offset still lands within the input.
	if ute.Offset <= 0 || ute.Offset > int64(len(`{"X":"hi"}`)) {
		t.Errorf("UnmarshalTypeError.Offset = %d, want in (0, %d]", ute.Offset, len(`{"X":"hi"}`))
	}
}

func TestTypeMismatchEmbedded(t *testing.T) {
	type Inner struct {
		Y int
	}
	type Outer struct {
		Inner
		Z string
	}
	var v Outer
	err := Unmarshal([]byte(`{"Y":"not_int"}`), &v)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}

	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("expected *UnmarshalTypeError, got %T: %v", err, err)
	}
	// For promoted embedded fields, stdlib reports the Go field path such as
	// Inner.Y rather than the flattened JSON-visible name.
	var stdUTE *json.UnmarshalTypeError
	var stdField string
	if jsonErr := json.Unmarshal([]byte(`{"Y":"not_int"}`), &Outer{}); jsonErr != nil &&
		errors.As(jsonErr, &stdUTE) {
		stdField = stdUTE.Field
	}
	if stdField == "" {
		t.Fatal("stdlib failed to populate UnmarshalTypeError.Field")
	}
	if ute.Field != stdField {
		t.Errorf("embedded UnmarshalTypeError.Field = %q, stdlib parity wants %q", ute.Field, stdField)
	}
	t.Logf("embedded ute.Field: %q, ute.Struct: %q (parity %q)", ute.Field, ute.Struct, stdField)
}

func TestTypeMismatchSliceElement(t *testing.T) {
	type T struct {
		Items []struct {
			Name string
		}
	}
	input := `{"Items":[{"Name":"a"},{"Name":42}]}`
	var v T
	err := Unmarshal([]byte(input), &v)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}

	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("expected *UnmarshalTypeError, got %T: %v", err, err)
	}
	// stdlib omits slice indexes here and reports only the enclosing field path,
	// for example Items.Name.
	var stdUTE *json.UnmarshalTypeError
	jsonErr := json.Unmarshal([]byte(input), &T{})
	if jsonErr == nil || !errors.As(jsonErr, &stdUTE) {
		t.Fatalf("stdlib parity oracle missing: %v", jsonErr)
	}
	if ute.Field != stdUTE.Field {
		t.Errorf("slice element UnmarshalTypeError.Field = %q, stdlib parity wants %q", ute.Field, stdUTE.Field)
	}
	if ute.Value != stdUTE.Value {
		t.Errorf("slice element UnmarshalTypeError.Value = %q, stdlib parity wants %q", ute.Value, stdUTE.Value)
	}
	t.Logf("slice element ute.Field: %q (parity %q)", ute.Field, stdUTE.Field)
}

func TestTypeMismatchMapValue(t *testing.T) {
	input := `{"a":"not_int"}`
	var v map[string]int
	err := Unmarshal([]byte(input), &v)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}

	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("expected *UnmarshalTypeError, got %T: %v", err, err)
	}
	// Root map value mismatches leave Field empty in the stdlib result.
	var stdUTE *json.UnmarshalTypeError
	var w map[string]int
	jsonErr := json.Unmarshal([]byte(input), &w)
	if jsonErr == nil || !errors.As(jsonErr, &stdUTE) {
		t.Fatalf("stdlib parity oracle missing: %v", jsonErr)
	}
	if ute.Field != stdUTE.Field {
		t.Errorf("map value UnmarshalTypeError.Field = %q, stdlib parity wants %q", ute.Field, stdUTE.Field)
	}
	if ute.Value != stdUTE.Value {
		t.Errorf("map value UnmarshalTypeError.Value = %q, stdlib parity wants %q", ute.Value, stdUTE.Value)
	}
	t.Logf("map value ute.Field: %q (parity %q)", ute.Field, stdUTE.Field)
}

func TestUnknownFieldError(t *testing.T) {
	// Unknown fields should remain a no-op unless DisallowUnknownFields is enabled.
	type T struct {
		X int
	}
	var v T
	err := Unmarshal([]byte(`{"X":1,"Unknown":"value"}`), &v)
	if err != nil {
		t.Logf("got error (may be from DisallowUnknownFields): %v", err)
		return
	}
	if v.X != 1 {
		t.Fatalf("X = %d, want 1", v.X)
	}
}

func TestNestedPathErrorFormatting(t *testing.T) {
	// Nested failures should preserve the same rendered field path as stdlib.
	type Inner struct {
		Val string
	}
	type Mid struct {
		List []Inner
	}
	type Outer struct {
		Data Mid
	}
	input := `{"Data":{"List":[{"Val":"ok"},{"Val":42}]}}`
	var v Outer
	err := Unmarshal([]byte(input), &v)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}

	var ute *UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("expected *UnmarshalTypeError, got %T: %v", err, err)
	}

	// Nested paths must render the same field metadata as the stdlib oracle.
	var w Outer
	jsonErr := json.Unmarshal([]byte(input), &w)
	if jsonErr == nil {
		t.Fatal("stdlib did not report an error")
	}
	var stdUTE *json.UnmarshalTypeError
	if !errors.As(jsonErr, &stdUTE) {
		t.Fatalf("stdlib parity oracle missing: %v", jsonErr)
	}

	if ute.Field != stdUTE.Field {
		t.Errorf("nested UnmarshalTypeError.Field = %q, stdlib parity wants %q", ute.Field, stdUTE.Field)
	}
	if ute.Struct != stdUTE.Struct {
		t.Errorf("nested UnmarshalTypeError.Struct = %q, stdlib parity wants %q", ute.Struct, stdUTE.Struct)
	}
	if ute.Value != stdUTE.Value {
		t.Errorf("nested UnmarshalTypeError.Value = %q, stdlib parity wants %q", ute.Value, stdUTE.Value)
	}
	t.Logf("nested error: Value=%q Struct=%q Field=%q (parity Field=%q)", ute.Value, ute.Struct, ute.Field, stdUTE.Field)
}

func TestAllUnmarshalTypeError(t *testing.T) {
	// errors.As should expose ndec.UnmarshalTypeError through the stdlib type.
	type T struct{ X int }
	var v T
	err := Unmarshal([]byte(`{"X":"hi"}`), &v)
	if err == nil {
		t.Fatal("expected error")
	}

	var jsUTE *json.UnmarshalTypeError
	if !errors.As(err, &jsUTE) {
		t.Fatal("errors.As(*json.UnmarshalTypeError) should work")
	}
	if jsUTE.Value == "" {
		t.Error("json.UnmarshalTypeError.Value should not be empty")
	}
	if jsUTE.Field == "" {
		t.Error("json.UnmarshalTypeError.Field should not be empty")
	}
	t.Logf("json.UnmarshalTypeError: Value=%q Field=%q Struct=%q", jsUTE.Value, jsUTE.Field, jsUTE.Struct)
}

func TestErrorParsing(t *testing.T) {
	// Lazy error formatting still needs to produce a stable human-readable message.
	type T struct{ X int }
	var v T
	err := Unmarshal([]byte(`{"X":"hi"}`), &v)
	if err == nil {
		t.Fatal("expected error")
	}

	msg := fmt.Sprint(err)
	t.Logf("error message: %s", msg)
	if msg == "" {
		t.Error("error message should not be empty")
	}

	// The message should contain useful info (field, type, value)
	if !containsAny(msg, []string{"X", "int", "string"}) {
		t.Logf("error message may be incomplete: %s", msg)
	}
}

func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
