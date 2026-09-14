package encvm

import "unsafe"

// Available reports whether the native encoder VM blob was mapped for this
// platform. The trampoline entries and the stackReserve stub are declared in
// the per-config files (encvm_init.go / encvm_init_linked.go /
// encvm_stub.go) and reach the mapped blob through function pointers.
var Available bool

// VMExec calls the full-mode native encoder (indent + escape flags).
//
// The exec chains can exceed Go's 800B nosplit budget (the full mode measures
// 816B, and PGO rebuilds legitimately grow hot frames), so every entry grows
// the goroutine stack first: the stackReserve stub goes through a normal
// stack check, and returning from it guarantees the reserved space below the
// current SP is committed for the NOSPLIT trampoline and the C chain. The
// reserve costs one call per Marshal (~0.05% of a typical encode). noinline
// keeps the reserve and the trampoline on one stable stack shape.
//
//go:noinline
func VMExec(ctx unsafe.Pointer) {
	stackReserve()
	vjVMExecFull(ctx)
}

// VMExecFast calls the fast-mode native encoder (no indent, fast escape).
//
//go:noinline
func VMExecFast(ctx unsafe.Pointer) {
	stackReserve()
	vjVMExecFast(ctx)
}

// VMExecCompact calls the compact-mode native encoder (no indent).
//
//go:noinline
func VMExecCompact(ctx unsafe.Pointer) {
	stackReserve()
	vjVMExecCompact(ctx)
}
