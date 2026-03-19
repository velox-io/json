package main

import "debug/elf"

// SymInfo holds a function symbol's name, offset, size, and binding.
type SymInfo struct {
	Name   string
	Offset uint64
	Size   uint64
	Local  bool // true for STB_LOCAL, false for STB_GLOBAL
}

// BuildVersion holds Mach-O LC_BUILD_VERSION info.
type BuildVersion struct {
	Platform uint32
	MinOS    uint32
	SDK      uint32
}

// ExtractResult is the shared data struct returned by extractFromELF,
// extractFromMachO, and extractFromPE. It carries everything needed to
// write the output.
type ExtractResult struct {
	Blob       []byte        // combined code+data blob
	Syms       []SymInfo     // function symbols with blob-relative offsets
	CodeExtent uint64        // end of code region (for ADRP scan range)
	BlobExtent uint64        // total blob size (code + rodata/data)
	IsARM64    bool          // whether ARM64 ADRP patching is needed
	ELFMachine elf.Machine   // ELF machine type (only for ELF output)
	COFFMachine uint16       // COFF machine type (only for PE/COFF output)
	BuildVer   *BuildVersion // Mach-O build version (only for Mach-O output)
}
