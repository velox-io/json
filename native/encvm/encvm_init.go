//go:build (amd64 || arm64) && !vj_noencvm && !vj_encvmlink

//lint:file-ignore U1000 the c* function-pointer vars are only read from
// the trampoline .s files, which the unused check cannot observe

package encvm

import (
	"unsafe"

	"github.com/velox-io/json/native/execblob"
)

// The native blob ships as an embedded prelinked ELF image instead of linked
// per-OS .syso artifacts. execblob maps it into executable memory at startup,
// which keeps it invisible to every linker: cgo, external linking, and LTO
// builds all consume the same binary unchanged. Every OS on a given arch
// shares one linux-built blob, which merges the three mode specializations
// (full/compact/fast); the windows/amd64 trampoline bridges Win64 to System
// V, while AAPCS64 and Apple arm64 pass integer arguments identically so the
// arm64 trampoline serves every OS without a bridge.

// Entry points into the mapped blob, resolved at init. The trampolines load
// these pointers and jump through them.
var (
	cVMExecFull    uintptr
	cVMExecFast    uintptr
	cVMExecCompact uintptr
)

// stackReserve grows the goroutine stack before entering the native exec
// chain. The body lives in trampoline_{amd64,arm64}.s (plus
// trampoline_windows_amd64.s on windows/amd64).
func stackReserve()

//go:noescape
//go:nosplit
func vjVMExecFull(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFast(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecCompact(ctx unsafe.Pointer)

func init() {
	funcs, err := execblob.Load(blobImage, []string{
		"vj_vm_exec_full",
		"vj_vm_exec_fast",
		"vj_vm_exec_compact",
	}, execblob.LogWritePatch())
	if err != nil {
		// Available stays false and callers take the pure-Go encoder, the
		// same state as the vj_noencvm build. The blob is embedded and
		// arch-canonical, so a failure here means the OS refused the
		// executable mapping (e.g. a policy-restricted GOOS), not a broken
		// build.
		return
	}
	cVMExecFull = funcs[0]
	cVMExecFast = funcs[1]
	cVMExecCompact = funcs[2]

	Available = true
}
