//go:build darwin && arm64

package encvm

import "unsafe"

// ---- Default mode ----

//go:noescape
//go:nosplit
func vjVMExecDefaultNeon(ctx unsafe.Pointer)

// ---- Fast mode ----

//go:noescape
//go:nosplit
func vjVMExecFastNeon(ctx unsafe.Pointer)

func init() {
	vmExec = vjVMExecDefaultNeon
	vmExecFast = vjVMExecFastNeon
	Available = true
}
