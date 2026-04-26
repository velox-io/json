package vdec

import (
	"unsafe"

	"github.com/velox-io/json/gort"
)

// PtrBatchSize is intentionally small: most JSON objects have few pointer fields,
// and each batch keeps the entire array alive until the Parser is returned to
// the pool. A large batch would retain excessive unused memory per type.
const PtrBatchSize = 2

type TypeAllocator struct {
	rtype    unsafe.Pointer // *abi.Type for element
	elemSize uintptr        // size of one element
	block    unsafe.Pointer // current batch base pointer
	offset   int            // next free index in current batch
	cap      int            // batch capacity (= PtrBatchSize)
}

func (a *TypeAllocator) Alloc() unsafe.Pointer {
	if a.offset >= a.cap {
		a.block = gort.UnsafeNewArray(a.rtype, a.cap)
		a.offset = 0
	}
	ptr := unsafe.Add(a.block, uintptr(a.offset)*a.elemSize)
	a.offset++
	return ptr
}

func (a *TypeAllocator) Reset() {
	a.block = nil
	a.offset = a.cap
}
