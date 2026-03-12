//go:build linux && amd64

package encoder

import "unsafe"

// Available reports whether the native C encoder is linked on this platform.
const Available = true

// VMExec calls the C engine entry point vj_vm_exec (native VM).
//
// ctx must point to a VjExecCtx struct with all fields initialized.
func VMExec(ctx unsafe.Pointer) {
	vjVMExec(ctx)
}

// vjVMExec is the assembly trampoline to C vj_vm_exec.
// Defined in trampoline_linux_amd64.s.
//
//go:noescape
//go:nosplit
func vjVMExec(ctx unsafe.Pointer)
