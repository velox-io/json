//go:build vj_ndeclink && !vj_nondec

package ndec

import "unsafe"

// Linked-syso build path, used only by instrumentation PGO collection
// (scripts/pgo-collect-instr.sh, MODULE=ndec). The instrumented artifact
// carries relocations and __llvm_prf_* sections that the prelink step
// cannot merge, so it must be linked by Go's linker instead of mapped by
// execblob; the trampoline_*_linked.s files call the exported ndec_*
// symbols directly. Production builds never set this tag.

// stackReserve grows the Go stack before entering the deepest native bind
// chain. The body lives in trampoline_amd64_linked.s / trampoline_arm64_linked.s.
func stackReserve()

//go:noescape
//go:nosplit
func vjNdecDOMParseCounted(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecDOMBuild(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecBindParse(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecBindParseStream(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecWindowScan(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecFmtParse(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecValid(ctx unsafe.Pointer)
