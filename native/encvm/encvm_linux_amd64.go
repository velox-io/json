//go:build linux && amd64

package encvm

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
//nolint:unused // AVX2 entry kept for generated/native symbol compatibility.
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
//nolint:unused // AVX2 entry kept for generated/native symbol compatibility.
func vjVMExecFastAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFastAVX512(ctx unsafe.Pointer)

// ---- Compact mode ----

//go:noescape
//go:nosplit
func vjVMExecCompactSSE42(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
//nolint:unused // AVX2 entry kept for generated/native symbol compatibility.
func vjVMExecCompactAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecCompactAVX512(ctx unsafe.Pointer)

func init() {
	if cpu.X86.HasAVX512BW {
		vmExec = vjVMExecDefaultAVX512
		vmExecFast = vjVMExecFastAVX512
		vmExecCompact = vjVMExecCompactAVX512
	} else {
		vmExec = vjVMExecDefaultSSE42
		vmExecFast = vjVMExecFastSSE42
		vmExecCompact = vjVMExecCompactSSE42
	}
	Available = true
}
