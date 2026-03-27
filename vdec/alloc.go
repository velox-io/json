package vdec

import (
	"unsafe"

	"github.com/velox-io/json/gort"
)

// PtrBatchSize is intentionally small: most JSON objects have few pointer fields,
// and each batch keeps the entire array alive until the Parser is returned to
// the pool. A large batch would retain excessive unused memory per type.
const PtrBatchSize = 2

// TypeAllocator provides batched allocation for a single element type.
// It uses unsafe_NewArray to allocate PtrBatchSize elements at once, then
// hands them out one at a time. GC safety is preserved because
// unsafe_NewArray carries full type metadata for scanning.
type TypeAllocator struct {
	rtype    unsafe.Pointer // *abi.Type for element
	elemSize uintptr        // size of one element
	block    unsafe.Pointer // current batch base pointer
	offset   int            // next free index in current batch
	cap      int            // batch capacity (= PtrBatchSize)
}

// When the current batch is exhausted, a new batch is allocated.
func (a *TypeAllocator) Alloc() unsafe.Pointer {
	if a.offset >= a.cap {
		a.block = gort.UnsafeNewArray(a.rtype, a.cap)
		a.offset = 0
	}
	ptr := unsafe.Add(a.block, uintptr(a.offset)*a.elemSize)
	a.offset++
	return ptr
}

// reset releases the current batch reference so GC can collect unused
// elements. The next Alloc() call will allocate a fresh batch.
func (a *TypeAllocator) Reset() {
	a.block = nil
	a.offset = a.cap
}
