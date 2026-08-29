package vcopy

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/velox-io/json/typ"
)

// copierPool recycles Copier instances across DeepCopy/CopyInto calls so the
// acyclicCache persists across calls (most types are seen repeatedly in real
// workloads). A Copier is not concurrency-safe; the pool hands out
// exclusive ownership for the duration of a call.
var copierPool = sync.Pool{
	New: func() any { return &Copier{} },
}

// DeepCopy produces a deep copy of v and returns it. The result has the same
// static type as v. Scalars are returned by value; containers are newly
// allocated.
//
// Cyclic graphs are handled: a visiting map records source→destination
// pointer pairs and breaks back edges. Most acyclic types skip the visiting
// map entirely via a compile-time-style graph analysis cached on the Copier.
//
// DeepCopy returns an error if v's type contains an unsupported kind
// (chan, func, unsafe.Pointer).
func DeepCopy[T any](v T) (T, error) {
	var dst T
	c := copierPool.Get().(*Copier)
	c.beginCall()
	err := c.copyValue(typ.UniTypeOf(reflect.TypeFor[T]()),
		unsafe.Pointer(&v), unsafe.Pointer(&dst))
	copierPool.Put(c)
	if err != nil {
		var zero T
		return zero, err
	}
	return dst, nil
}

// CopyInto deep-copies src into *dst. *dst must be a value of the same type
// as src; the existing contents of *dst are overwritten.
func CopyInto[T any](src T, dst *T) error {
	c := copierPool.Get().(*Copier)
	c.beginCall()
	err := c.copyValue(typ.UniTypeOf(reflect.TypeFor[T]()),
		unsafe.Pointer(&src), unsafe.Pointer(dst))
	copierPool.Put(c)
	return err
}

// CopyWith is the dynamic-typed entry point. src and dst must point to
// values of the same type t. Pass a non-nil Copier to reuse its acyclic
// cache across calls; pass nil to use a pooled Copier.
func CopyWith(c *Copier, src, dst unsafe.Pointer, t reflect.Type) error {
	pooled := false
	if c == nil {
		c = copierPool.Get().(*Copier)
		pooled = true
	}
	c.beginCall()
	err := c.copyValue(typ.UniTypeOf(t), src, dst)
	if pooled {
		copierPool.Put(c)
	}
	return err
}
