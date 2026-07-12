//go:build darwin && arm64 && !vj_nolookup

package vlib

import "unsafe"

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
	Available = true

	sizeFor = vjLookupSizeFor
	scratchSize = vjLookupScratchSize
	lookupInit = vjLookupInit
	getTier = vjLookupGetTier
	footprint = vjLookupFootprint
}
