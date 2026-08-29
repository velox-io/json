package gort

import (
	"reflect"
	"testing"
	"unsafe"
)

func TestTypeFromRType_RoundTrip(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeFor[int](),
		reflect.TypeFor[string](),
		reflect.TypeFor[[]byte](),
		reflect.TypeFor[map[string]int](),
		reflect.TypeFor[*struct{ X int }](),
		reflect.TypeFor[struct {
			A int
			B string
		}](),
	}
	for _, rt := range types {
		rtp := TypePtr(rt)
		got := TypeFromRType(rtp)
		if got != rt {
			t.Errorf("TypeFromRType round-trip failed: got %v want %v", got, rt)
		}
		// Critical: constructed reflect.Type must be usable as a map key
		// that matches the canonical reflect.Type. UniTypeOf relies on this.
		if got != reflect.TypeOf(zeroOf(rt)) {
			t.Errorf("TypeFromRType not map-key-equal to canonical: got %v want %v",
				got, reflect.TypeOf(zeroOf(rt)))
		}
	}
}

func zeroOf(rt reflect.Type) any {
	return reflect.Zero(rt).Interface()
}

func TestEfaceRType(t *testing.T) {
	var v any = 42
	got := EfaceRType(unsafe.Pointer(&v))
	want := TypePtr(reflect.TypeOf(v))
	if got != want {
		t.Errorf("EfaceRType: got %p want %p", got, want)
	}
	// Construct reflect.Type and verify Kind.
	rt := TypeFromRType(got)
	if rt.Kind() != reflect.Int {
		t.Errorf("TypeFromRType(EfaceRType): kind %v, want Int", rt.Kind())
	}
}

func TestEfaceRType_Nil(t *testing.T) {
	var v any
	got := EfaceRType(unsafe.Pointer(&v))
	if got != nil {
		t.Errorf("EfaceRType nil: got %p, want nil", got)
	}
}

func TestIfaceConcreteRType(t *testing.T) {
	var s fmtStringer = myStringer{}
	got := IfaceConcreteRType(unsafe.Pointer(&s))
	want := TypePtr(reflect.TypeOf(s))
	if got != want {
		t.Errorf("IfaceConcreteRType: got %p want %p", got, want)
	}
	rt := TypeFromRType(got)
	if rt.Kind() != reflect.Struct {
		t.Errorf("TypeFromRType(IfaceConcreteRType): kind %v, want Struct", rt.Kind())
	}
}

func TestIfaceConcreteRType_Nil(t *testing.T) {
	var s fmtStringer
	got := IfaceConcreteRType(unsafe.Pointer(&s))
	if got != nil {
		t.Errorf("IfaceConcreteRType nil: got %p, want nil", got)
	}
}

type fmtStringer = interface {
	String() string
}

type myStringer struct{}

func (myStringer) String() string { return "x" }
