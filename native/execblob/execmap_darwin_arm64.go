//go:build darwin && arm64

package execblob

import (
	"syscall"
	"unsafe"
)

// MAP_JIT is absent from the syscall package. Its value is stable ABI
// (usr/include/mman.h).
const mapJIT = 0x0800

//go:cgo_import_dynamic _pthread_jit_write_protect_np pthread_jit_write_protect_np "/usr/lib/libSystem.B.dylib"

// pthreadJitWriteProtect toggles per-thread write access to MAP_JIT pages.
//
//go:noescape
//go:nosplit
func pthreadJitWriteProtect(enabled int32)

// writeExec maps code into executable memory, applying words inside the
// write window.
//
// On Apple silicon every thread defaults to write protecting MAP_JIT pages,
// even in processes without the hardened runtime, so writing requires the
// pthread toggle. The toggle only gates writes; execution needs no per
// thread state. The write window is a plain copy with no calls that could
// panic or preempt between the two toggles beyond resuming afterwards,
// and every thread except the current one keeps seeing the pages as
// execute only either way.
func writeExec(code []byte, words []wordAt) (uintptr, error) {
	b, err := syscall.Mmap(-1, 0, len(code),
		syscall.PROT_READ|syscall.PROT_WRITE|syscall.PROT_EXEC,
		syscall.MAP_PRIVATE|syscall.MAP_ANON|mapJIT)
	if err != nil {
		return 0, err
	}
	pthreadJitWriteProtect(0)
	copy(b, code)
	applyWords(b, words)
	pthreadJitWriteProtect(1)
	return uintptr(unsafe.Pointer(unsafe.SliceData(b))), nil
}
