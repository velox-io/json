package typ

import (
	"math"
	"reflect"
	"testing"
	"unsafe"
)

// makeReflectZeroFn must mirror reflect.Value.IsZero exactly; these are the
// corners where a hand-rolled walk most easily drifts.
func TestMakeReflectZeroFn(t *testing.T) {
	check := func(t *testing.T, name string, v any, want bool) {
		t.Helper()
		rv := reflect.ValueOf(v)
		fn := makeReflectZeroFn(rv.Type())
		if fn == nil {
			t.Fatalf("%s: no closure built for %v", name, rv.Type())
		}
		pv := reflect.New(rv.Type())
		pv.Elem().Set(rv)
		got := fn(pv.UnsafePointer())
		if got != want {
			t.Errorf("%s: closure says %v, reflect says %v", name, got, rv.IsZero())
		}
		if rv.IsZero() != want {
			t.Fatalf("%s: bad expectation, reflect.IsZero = %v", name, rv.IsZero())
		}
	}

	neg := math.Copysign(0, -1)
	check(t, "-0.0 float64", neg, true)
	check(t, "-0.0 float32", float32(neg), true)
	check(t, "0 complex", complex(0, 0), true)
	check(t, "-0 complex", complex(neg, neg), true)
	check(t, "non-zero complex", complex(0, 1), false)

	check(t, "nil slice", []int(nil), true)
	check(t, "empty slice", []int{}, false)
	check(t, "nil map", map[string]int(nil), true)
	check(t, "empty map", map[string]int{}, false)
	check(t, "typed nil ptr", (*int)(nil), true)
	check(t, "typed nil func", (func())(nil), true)
	check(t, "typed nil chan", (chan int)(nil), true)

	// A nil interface needs its own slot: reflect.ValueOf(nil) carries no type.
	var nilAny any
	fnAny := makeReflectZeroFn(reflect.TypeFor[any]())
	if !fnAny(unsafe.Pointer(&nilAny)) {
		t.Error("nil interface must be zero")
	}
	nilAny = 1
	if fnAny(unsafe.Pointer(&nilAny)) {
		t.Error("non-nil interface must not be zero")
	}

	check(t, "zero array", [2]int{}, true)
	check(t, "non-zero array", [2]int{0, 1}, false)
	check(t, "empty array", [0]int{}, true)

	check(t, "empty struct", struct{}{}, true)

	type blank struct {
		A int
		_ int
	}
	check(t, "blank field", blank{}, true)

	// reflect reads unexported fields too; the closures take a pointer to the
	// value directly.
	type hidden struct {
		A int
		m int
	}
	hz := hidden{}
	hn := hidden{m: 1}
	fn := makeReflectZeroFn(reflect.TypeFor[hidden]())
	if !fn(unsafe.Pointer(&hz)) {
		t.Error("all-zero struct with unexported field must be zero")
	}
	if fn(unsafe.Pointer(&hn)) {
		t.Error("unexported non-zero field must make the struct non-zero")
	}
}
