//go:build darwin && arm64

package encvm

import "unsafe"

// ---- Full mode ----

//go:noescape
//go:nosplit
func vjVMExecFullNeon(ctx unsafe.Pointer)

// ---- Fast mode ----

//go:noescape
//go:nosplit
func vjVMExecFastNeon(ctx unsafe.Pointer)

// ---- Compact mode ----

//go:noescape
//go:nosplit
func vjVMExecCompactNeon(ctx unsafe.Pointer)

func init() {
	vmExec = vjVMExecFullNeon
	vmExecFast = vjVMExecFastNeon
	vmExecCompact = vjVMExecCompactNeon
	Available = true
}
