//go:build darwin && amd64

package execblob

import (
	"syscall"
	"unsafe"
)

// writeExec maps code into executable memory, applying words while the
// mapping is still writable. Intel macs enforce no W^X split, so plain RWX
// pages need no MAP_JIT flag and no jit write toggle;
// pthread_jit_write_protect_np is a no-op there.
func writeExec(code []byte, words []wordAt) (uintptr, error) {
	b, err := syscall.Mmap(-1, 0, len(code),
		syscall.PROT_READ|syscall.PROT_WRITE|syscall.PROT_EXEC,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return 0, err
	}
	copy(b, code)
	applyWords(b, words)
	return uintptr(unsafe.Pointer(unsafe.SliceData(b))), nil
}
