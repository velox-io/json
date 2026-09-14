//go:build (amd64 || arm64) && !vj_nolookup

//lint:file-ignore U1000 the c* function-pointer vars are only read from
// the trampoline .s files, which the unused check cannot observe

package vlib

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

// Entry points into the mapped blob, resolved at init. The trampolines load
// these pointers and call through them.
var (
	cLookupSizeFor     uintptr
	cLookupScratchSize uintptr
	cLookupInit        uintptr
	cLookupGetTier     uintptr
	cLookupFootprint   uintptr
)

//go:noescape
//go:nosplit
func vjLookupSizeFor(cfg *Config) uintptr

//go:noescape
//go:nosplit
func vjLookupScratchSize() uintptr

//go:noescape
//go:nosplit
func vjLookupInit(storage unsafe.Pointer, storageSize uintptr, cfg *Config) int32

//go:noescape
//go:nosplit
func vjLookupGetTier(storage unsafe.Pointer) uint32

//go:noescape
//go:nosplit
func vjLookupFootprint(storage unsafe.Pointer) uintptr

func init() {
	funcs, err := execblob.Load(blobImage, []string{
		"ndec_lookup_size_for",
		"ndec_lookup_scratch_size",
		"ndec_lookup_init",
		"ndec_lookup_get_tier",
		"ndec_lookup_footprint",
	}, nil)
	if err != nil {
		// Available stays false and callers take the pure-Go path, the
		// same state as the vj_nolookup build.
		return
	}
	cLookupSizeFor = funcs[0]
	cLookupScratchSize = funcs[1]
	cLookupInit = funcs[2]
	cLookupGetTier = funcs[3]
	cLookupFootprint = funcs[4]

	Available = true

	sizeFor = vjLookupSizeFor
	scratchSize = vjLookupScratchSize
	lookupInit = vjLookupInit
	getTier = vjLookupGetTier
	footprint = vjLookupFootprint
}
