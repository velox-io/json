//go:build windows

package execblob

import (
	"syscall"
	"unsafe"
)

const (
	memCommit       = 0x1000
	memReserve      = 0x2000
	pageExecuteRead = 0x20
)

// currentProcess is the pseudo handle GetCurrentProcess returns: it needs
// no CloseHandle and grants full access to the calling process.
const currentProcess = ^uintptr(0)

var (
	modKernel32            = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc       = modKernel32.NewProc("VirtualAlloc")
	procWriteProcessMemory = modKernel32.NewProc("WriteProcessMemory")
)

// writeExec maps code into memory sealed to execute and read, with words
// applied through WriteProcessMemory.
//
// VirtualAlloc returns a raw OS address with no Go pointer origin, so no
// conversion of it into a Go pointer exists that unsafe.Pointer rules (or
// vet's unsafeptr) can accept. The OS is therefore the writer: the final
// image is built in ordinary Go memory and delivered in one WriteProcessMemory
// call, which lifts page protection for the duration of the write. The
// mapping itself is never writable by ordinary stores.
//
// Calls go through syscall.SyscallN, not LazyProc.Call: only the Syscall
// functions carry //go:uintptrkeepalive, so pointer arguments converted for
// the argument list are retained and not moved for the duration of the call
// (unsafe rule 4).
func writeExec(code []byte, words []wordAt) (uintptr, error) {
	if err := procVirtualAlloc.Find(); err != nil {
		return 0, err
	}
	if err := procWriteProcessMemory.Find(); err != nil {
		return 0, err
	}

	p, _, err := syscall.SyscallN(procVirtualAlloc.Addr(),
		0, uintptr(len(code)), memCommit|memReserve, pageExecuteRead)
	if p == 0 {
		return 0, err
	}

	image := make([]byte, len(code))
	copy(image, code)
	applyWords(image, words)

	var written uintptr
	r, _, err := syscall.SyscallN(procWriteProcessMemory.Addr(),
		currentProcess, p,
		uintptr(unsafe.Pointer(unsafe.SliceData(image))), uintptr(len(image)),
		uintptr(unsafe.Pointer(&written)))
	if r == 0 {
		return 0, err
	}
	return p, nil
}
