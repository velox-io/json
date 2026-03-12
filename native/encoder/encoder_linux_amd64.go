//go:build linux && amd64

package encoder

import (
	"unsafe"

	"golang.org/x/sys/cpu"
)

// ---- Default mode ----

//go:noescape
//go:nosplit
func vjVMExecDefaultSSE42(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecDefaultAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecDefaultAVX512(ctx unsafe.Pointer)

// ---- Fast mode ----

//go:noescape
//go:nosplit
func vjVMExecFastSSE42(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFastAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFastAVX512(ctx unsafe.Pointer)

func init() {
	if cpu.X86.HasAVX512BW {
		vmExec = vjVMExecDefaultAVX512
		vmExecFast = vjVMExecFastAVX512
	} else {
		vmExec = vjVMExecDefaultSSE42
		vmExecFast = vjVMExecFastSSE42
	}
	Available = true
}
