//go:build linux && arm64 && !vj_noencvm

package encvm

import "unsafe"

func init() {
	Available = true
}

//go:noescape
//go:nosplit
func vjVMExecFull(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFast(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecCompact(ctx unsafe.Pointer)

// VMExec calls the full-mode native encoder.
func VMExec(ctx unsafe.Pointer) { vjVMExecFull(ctx) }

// VMExecFast calls the fast-mode native encoder.
func VMExecFast(ctx unsafe.Pointer) { vjVMExecFast(ctx) }

// VMExecCompact calls the compact-mode native encoder.
func VMExecCompact(ctx unsafe.Pointer) { vjVMExecCompact(ctx) }
