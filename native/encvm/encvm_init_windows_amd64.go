//go:build windows && amd64 && !vj_noencvm

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

func stackReserve()

// The exec chains (inlined itoa and escape bodies) exceed the 800B
// nosplit budget on Win64, so every entry grows the goroutine stack
// first: the stackReserve stub (assembler-defined) goes through a normal
// stack check, and returning from it guarantees the reserved space below
// the current SP is committed for the NOSPLIT C call. The reserve costs
// one call per Marshal (~0.05% of a typical encode), which keeps the
// per-element inlining that leafing would take away (~7% on int-heavy
// Values). noinline keeps the reserve and the C entry on one stable
// stack shape, same as ndec's BindParseRun.
//
//go:noinline
func VMExec(ctx unsafe.Pointer) {
	stackReserve()
	vjVMExecFull(ctx)
}

//go:noinline
func VMExecFast(ctx unsafe.Pointer) {
	stackReserve()
	vjVMExecFast(ctx)
}

//go:noinline
func VMExecCompact(ctx unsafe.Pointer) {
	stackReserve()
	vjVMExecCompact(ctx)
}
