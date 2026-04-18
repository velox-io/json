// parity tests for embedded (anonymous) struct fields

package ndec

import (
	"encoding/json"
	"reflect"
	"testing"
)

type EmbInner struct {
	X int
	Y int
}

type EmbOuter struct {
	EmbInner
	Z string
}

type EmbMultiLevel1 struct {
	A int
}

type EmbMultiLevel2 struct {
	EmbMultiLevel1
	B string
}

type EmbMultiLevel3 struct {
	EmbMultiLevel2
	C bool
}

type EmbShadow struct {
	X float64 // shadows EmbInner.X which is int
	EmbInner
}

type EmbNamed struct {
	Data EmbInner `json:"data"` // named tag stops promotion
	Flag bool
}

func TestEmbeddedBasicParity(t *testing.T) {
	input := `{"X":10,"Y":20,"Z":"hello"}`
	got := &EmbOuter{}
	want := &EmbOuter{}

	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("basic embedded:\n  ndec = %+v\n  json = %+v", got, want)
	}
}

func TestEmbeddedMultiLevelParity(t *testing.T) {
	input := `{"A":1,"B":"text","C":true}`
	got := &EmbMultiLevel3{}
	want := &EmbMultiLevel3{}

	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-level embedded:\n  ndec = %+v\n  json = %+v", got, want)
	}
}

func TestEmbeddedShadowParity(t *testing.T) {
	// EmbShadow.X (float64, depth 0) shadows EmbInner.X (int, depth 1)
	input := `{"X":3.14,"Y":42}`
	got := &EmbShadow{}
	want := &EmbShadow{}

	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shadowed embedded:\n  ndec = %+v\n  json = %+v", got, want)
	}
}

func TestEmbeddedNamedTagParity(t *testing.T) {
	// EmbNamed.Data has json:"data" tag, NOT promoted
	input := `{"data":{"X":5,"Y":6},"Flag":true}`
	got := &EmbNamed{}
	want := &EmbNamed{}

	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("named tag embedded:\n  ndec = %+v\n  json = %+v", got, want)
	}
}

func TestEmbeddedPartialFields(t *testing.T) {
	input := `{"X":99,"Z":"partial"}`
	got := &EmbOuter{}
	want := &EmbOuter{}

	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partial embedded:\n  ndec = %+v\n  json = %+v", got, want)
	}
}

func TestEmbeddedUnexportedTypeParity(t *testing.T) {
	type embUnexported struct{ Name string }
	type outer struct {
		embUnexported // anonymous, unexported type
		Age           int
	}
	input := `{"Name":"Alice","Age":30}`
	got := &outer{}
	want := &outer{}

	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexported embedded type:\n  ndec = %+v\n  json = %+v", got, want)
	}
}

func TestEmbeddedComplex(t *testing.T) {
	// A realistic complex struct with multiple embedded types
	type Address struct {
		Street string
		City   string
	}
	type Contact struct {
		Email string
		Phone string
	}
	type Person struct {
		Name string
		Age  int
	}
	type Employee struct {
		Person
		Address  `json:"addr"`
		Contact  // promoted
		Position string
	}

	input := `{"Name":"Bob","Age":30,"Email":"bob@test.com","Phone":"1234","addr":{"Street":"1st","City":"NY"},"Position":"manager"}`
	got := &Employee{}
	want := &Employee{}

	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complex embedded:\n  ndec = %+v\n  json = %+v", got, want)
	}
}
