//go:build darwin && arm64

package encoder

import "unsafe"

//go:noescape
//go:nosplit
func vjVMExecNeon(ctx unsafe.Pointer)

func init() {
	vmExec = vjVMExecNeon
	Available = true
}
