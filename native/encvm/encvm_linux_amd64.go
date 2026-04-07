//go:build linux && amd64 && !vj_noencvm

package encvm

import "unsafe"

//go:noescape
//go:nosplit
func vjVMExecFullAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFastAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecCompactAVX2(ctx unsafe.Pointer)

func init() {
	vmExec = vjVMExecFullAVX2
	vmExecFast = vjVMExecFastAVX2
	vmExecCompact = vjVMExecCompactAVX2
	Available = true
}
