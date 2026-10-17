package vjson

import (
	"reflect"
	"unsafe"
)

// gcShadeSlot exists for its write barrier: a pointer store into it shades
// the pointee, and the stored value is never read.
var gcShadeSlot unsafe.Pointer

// Pin shades what p refers to for the garbage collection cycle already
// in flight and returns p.
//
// Unmarshal overwrites a destination without Go's deletion barrier, and
// the assignment binding a field's old value to a local carries no
// barrier either. In the rare case a local outlives its field across an
// Unmarshal, the cycle in flight can free the old value while the local
// still refers to it. Pin is that shading managed by the caller:
//
//	old := vjson.Pin(v.Greeting)  // a shaded copy of the old value
//	vjson.Unmarshal(data, v)      // the overwrite drops the field without a barrier
//	log.Println(old)              // safe: the old backing survived the cycle
//
// A string or slice shades its backing; a pointer shades its pointee,
// and a multi-level chain every link's, since null clears the outermost
// slot and a value fills the leaf slot.
//
//go:nosplit
//go:norace
func Pin[T any](p T) T {
	switch t := reflect.TypeFor[T](); t.Kind() {
	case reflect.String, reflect.UnsafePointer, reflect.Slice:
		// All three layouts lead with the data pointer: a string header's
		// first word is StringData, a slice header's is Data, and a pointer
		// word is the pointer itself. Storing the word into the global slot goes
		// through the write barrier, shading the pointee for the mark cycle
		// already in flight.
		gcShadeSlot = *(*unsafe.Pointer)(unsafe.Pointer(&p))
	case reflect.Pointer:
		// A multi-level chain pins link by link: unmarshal clears the
		// outermost slot on null and replaces the leaf pointer slot on a
		// fill, so each level's preimage needs its own shade. A *T box is
		// one pointer word, so the pointee's first word is the next link.
		v := unsafe.Pointer(&p)
		for t.Kind() == reflect.Pointer {
			word := *(*unsafe.Pointer)(v)
			if word == nil {
				break
			}
			gcShadeSlot = word
			v = word
			t = t.Elem()
		}
	}
	// Every value ever stored into gcShadeSlot was shaded by the insertion
	// barrier at store time, so the deletion side of the hybrid barrier is
	// redundant for this slot. Clearing through a pointer-free type drops
	// the barrier entirely.
	*(*uintptr)(unsafe.Pointer(&gcShadeSlot)) = 0
	return p
}
