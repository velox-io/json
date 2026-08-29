//go:build (amd64 || arm64) && !vj_nondec && !vj_ndeclink

//lint:file-ignore U1000 the c* function-pointer vars are only read from
// the trampoline .s files, which the unused check cannot observe

package ndec

import (
	"unsafe"

	"github.com/velox-io/json/native/execblob"
)

// The native blob ships as an embedded prelinked ELF image instead of linked
// per-OS .syso artifacts. execblob maps it into executable memory at startup,
// which keeps it invisible to every linker: cgo, external linking, and LTO
// builds all consume the same binary unchanged. Every OS on a given arch
// shares one linux-built blob; the windows/amd64 trampoline bridges Win64 to
// System V, while AAPCS64 and Apple arm64 pass integer arguments identically
// so the arm64 trampoline serves every OS without a bridge.

// stackReserve grows the Go stack before entering the deepest native bind
// chain. The body lives in trampoline_{amd64,arm64}.s (plus
// trampoline_windows_amd64.s on windows/amd64).
func stackReserve()

// Entry points into the mapped blob, resolved at init. The trampolines load
// these pointers and jump through them.
var (
	cDomParseCounted uintptr
	cDomBuild        uintptr
	cBindParse       uintptr
	cBindParseStream uintptr
	cWindowScan      uintptr
	cFmtParse        uintptr
	cValid           uintptr
)

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

func init() {
	// Native decode is a hard requirement on every OS this file builds for:
	// there is no pure-Go fallback to degrade to (see execblob.MustLoad).
	funcs := execblob.MustLoad("ndec", blobImage, []string{
		"ndec_dom_parse_counted",
		"ndec_dom_build",
		"ndec_bind_parse",
		"ndec_bind_parse_stream",
		"ndec_window_scan",
		"ndec_fmt_parse",
		"ndec_valid",
	}, execblob.LogWritePatch())
	cDomParseCounted = funcs[0]
	cDomBuild = funcs[1]
	cBindParse = funcs[2]
	cBindParseStream = funcs[3]
	cWindowScan = funcs[4]
	cFmtParse = funcs[5]
	cValid = funcs[6]
}
