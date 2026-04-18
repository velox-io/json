// Package ndec provides SIMD-accelerated JSON deserialization via a native C parser.
package ndec

import (
	"reflect"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/gort"
	nativendec "github.com/velox-io/json/native/ndec"
)

const ndecFastCacheSize = 32

type ndecFastCacheEntry struct {
	key uintptr // rtype pointer
	val *builtType
}

type ndecFastCache [ndecFastCacheSize]atomic.Pointer[ndecFastCacheEntry]

func ndecFastCacheIndex(rtp uintptr) uintptr {
	const magic = 0x9e3779b97f4a7c15
	return (rtp * magic) >> (64 - 5)
}

func (c *ndecFastCache) get(t reflect.Type) (*builtType, error) {
	rtp := uintptr(gort.TypePtr(t))
	idx := ndecFastCacheIndex(rtp)
	if p := c[idx].Load(); p != nil && p.key == rtp {
		return p.val, nil
	}
	bt, err := bindTypeInfoOf(t)
	if err != nil {
		return nil, err
	}
	c[idx].Store(&ndecFastCacheEntry{key: rtp, val: bt})
	return bt, nil
}

var typeFastCache ndecFastCache

// Unmarshal decodes JSON data into v. v must be a non-nil pointer to a
// supported target type (struct, *T, []T, map[K]V, etc).
//
// The caller must keep data alive and unmodified for the duration of the
// call. String fields may alias the input backing array; the driver holds
// input alive via runtime.KeepAlive until the parser returns.
//
// The generic type parameter T avoids reflect.ValueOf allocation on the
// concrete-type path. Only the interface{} fallback (when T is any)
// requires a reflect alloc.
func Unmarshal[T any](data []byte, v T) error {
	if !nativendec.Available {
		panic("ndec: native parser not available on this platform")
	}

	rt := reflect.TypeFor[T]()

	var ptr unsafe.Pointer
	var bt *builtType

	if rt.Kind() == reflect.Pointer {
		ptr = *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if ptr == nil {
			return &InvalidUnmarshalError{Type: rt}
		}
		var err error
		bt, err = typeFastCache.get(rt.Elem())
		if err != nil {
			return err
		}
	} else if rt.Kind() == reflect.Interface {
		// interface{} path requires reflect.ValueOf to extract runtime type.
		// Prefer passing a concrete pointer type to avoid this alloc.
		rv := reflect.ValueOf(v)
		if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
			return &InvalidUnmarshalError{Type: rt}
		}
		ptr = rv.UnsafePointer()
		var err error
		bt, err = typeFastCache.get(rv.Elem().Type())
		if err != nil {
			return err
		}
	} else {
		return &InvalidUnmarshalError{Type: rt}
	}

	d := acquireDriverState()
	err := d.runUnmarshal(bt, ptr, data)
	releaseDriverState(d)
	return err
}
