//go:build linux && amd64

package encoder

import (
	"unsafe"

	"golang.org/x/sys/cpu"
)

//go:noescape
//go:nosplit
func vjVMExecSSE42(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecAVX512(ctx unsafe.Pointer)

func init() {
	if cpu.X86.HasAVX512BW {
		vmExec = vjVMExecAVX512
		//} else if cpu.X86.HasAVX2 {
		//}   vmExec = vjVMExecAVX2
	} else {
		vmExec = vjVMExecSSE42
	}
	Available = true
}
