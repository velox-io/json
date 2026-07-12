//go:build linux && arm64 && !vj_noencvm

package encvm

import "unsafe"

//go:noescape
//go:nosplit
func vjVMExecFullNeon(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFastNeon(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecCompactNeon(ctx unsafe.Pointer)

func init() {
	vmExec = vjVMExecFullNeon
	vmExecFast = vjVMExecFastNeon
	vmExecCompact = vjVMExecCompactNeon
	Available = true
}
