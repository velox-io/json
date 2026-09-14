//go:build vj_encvmlink && !vj_noencvm

package encvm

import "unsafe"

// Linked-syso build path, used only by instrumentation PGO collection
// (scripts/pgo-collect-instr.sh, MODULE=encvm). The instrumented artifact
// carries relocations and __llvm_prf_* sections that the prelink step
// cannot merge, so it must be linked by Go's linker instead of mapped by
// execblob; the trampoline_*_linked.s files call the exported vj_vm_exec_*
// symbols directly. Production builds never set this tag.
//
// The trampoline entry declarations live here for the linked config; their
// bodies come from the linked trampolines. Available flips true
// unconditionally: the linked artifact defines every entry symbol or the
// link fails.

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
	Available = true
}
