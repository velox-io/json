//go:build linux

package execblob

import (
	"syscall"
	"unsafe"
)

// writeExec maps code into executable memory, applying words while the
// mapping is still writable, then seals it to read and execute only.
// The base address is the kernel-owned mapping itself, so the returned
// uintptr stays valid for the process lifetime.
func writeExec(code []byte, words []wordAt) (uintptr, error) {
	b, err := syscall.Mmap(-1, 0, len(code),
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return 0, err
	}
	copy(b, code)
	applyWords(b, words)
	if err := syscall.Mprotect(b, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		return 0, err
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(b))), nil
}
