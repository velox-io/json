package vjson

import "unsafe"

const ptrBatchSize = 4

// rtypeAllocator provides batched allocation for a single element type.
// It uses unsafe_NewArray to allocate ptrBatchSize elements at once, then
// hands them out one at a time. GC safety is preserved because
// unsafe_NewArray carries full type metadata for scanning.
type rtypeAllocator struct {
	rtype    unsafe.Pointer // *abi.Type for element
	elemSize uintptr        // size of one element
	block    unsafe.Pointer // current batch base pointer
	offset   int            // next free index in current batch
	cap      int            // batch capacity (= ptrBatchSize)
}

// alloc returns a pointer to a zeroed element of the allocator's type.
// When the current batch is exhausted, a new batch is allocated.
func (a *rtypeAllocator) alloc() unsafe.Pointer {
	if a.offset >= a.cap {
		a.block = unsafe_NewArray(a.rtype, a.cap)
		a.offset = 0
	}
	ptr := unsafe.Add(a.block, uintptr(a.offset)*a.elemSize)
	a.offset++
	return ptr
}

// reset releases the current batch reference so GC can collect unused
// elements. The next alloc() call will allocate a fresh batch.
func (a *rtypeAllocator) reset() {
	a.block = nil
	a.offset = a.cap
}
