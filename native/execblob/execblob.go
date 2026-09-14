// Package execblob maps prelinked native code blobs into executable memory
// at runtime.
//
// The blob is not linked into the binary. It is embedded with go:embed and
// mapped here, which keeps it invisible to every linker: cgo, external
// linking, and LTO builds all work unchanged, and one artifact per arch
// serves every OS. The blob itself already satisfies every property a
// loader needs:
//
//   - zero relocations and position independent, so no relocation
//     processing; loading is a memcpy
//   - exactly one SHF_ALLOC section (.text), because prelink merges
//     rodata and data into .text, so section layout is trivial
//
// Entry points are reached through function pointers resolved here, never
// through linker symbols.
package execblob

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
)

// WordPatch rewrites one 8-byte word inside the mapped blob at Symbol+Off.
//
// The arch-canonical blob is built once for linux and consumed by every OS
// on that arch, so constants that the C code cannot resolve without knowing
// the running OS (the debug-log write syscall number, see native/util/log.h)
// cannot be baked in. The loader carries the consuming OS's value instead:
// patches land before the mapping turns executable, through each OS write
// path for not-yet-executing code (unix: the initial writable window;
// windows: WriteProcessMemory).
type WordPatch struct {
	Symbol string
	Off    int
	Value  uint64
}

// wordAt is a resolved patch: a little-endian 8-byte write at a byte offset
// within the mapped image. Both supported arches (amd64, arm64) are
// little-endian, so no target byte order exists to pick.
type wordAt struct {
	off int
	val uint64
}

// Load maps image into executable memory, applies patches, and resolves
// symbols to absolute addresses in the order given. A missing symbol fails
// the whole load.
//
// image must be an ET_REL ELF file produced by the prelink build (see the
// package comment for the invariants this relies on).
//
// Load reports failure; the caller decides between degrading to a fallback
// (optional blobs) and MustLoad (required blobs).
func Load(image []byte, symbols []string, patches []WordPatch) ([]uintptr, error) {
	f, err := elf.NewFile(bytes.NewReader(image))
	if err != nil {
		return nil, fmt.Errorf("execblob: parse: %w", err)
	}
	defer f.Close()

	text, err := soleAllocSection(f)
	if err != nil {
		return nil, err
	}
	code, err := text.Data()
	if err != nil {
		return nil, fmt.Errorf("execblob: read .text: %w", err)
	}

	offs, err := symbolOffsets(f, text)
	if err != nil {
		return nil, err
	}

	words := make([]wordAt, len(patches))
	for i, p := range patches {
		off, ok := offs[p.Symbol]
		if !ok {
			return nil, fmt.Errorf("execblob: patch symbol %s not found", p.Symbol)
		}
		off += uintptr(p.Off)
		if off+8 > uintptr(len(code)) {
			return nil, fmt.Errorf("execblob: patch %s+%d outside .text", p.Symbol, p.Off)
		}
		words[i] = wordAt{off: int(off), val: p.Value}
	}

	base, err := writeExec(code, words)
	if err != nil {
		return nil, fmt.Errorf("execblob: map: %w", err)
	}

	funcs := make([]uintptr, len(symbols))
	for i, name := range symbols {
		off, ok := offs[name]
		if !ok {
			return nil, fmt.Errorf("execblob: symbol %s not found", name)
		}
		funcs[i] = base + off
	}
	return funcs, nil
}

// soleAllocSection returns the single SHF_ALLOC section, which the prelink
// build guarantees is .text with rodata and data merged in. Any second
// alloc section would need real section placement, which this loader
// deliberately does not implement.
func soleAllocSection(f *elf.File) (*elf.Section, error) {
	var sole *elf.Section
	for _, s := range f.Sections {
		if s.Flags&elf.SHF_ALLOC == 0 {
			continue
		}
		if sole != nil {
			return nil, fmt.Errorf("execblob: multiple SHF_ALLOC sections (%s, %s)", sole.Name, s.Name)
		}
		sole = s
	}
	if sole == nil {
		return nil, fmt.Errorf("execblob: no SHF_ALLOC section")
	}
	return sole, nil
}

// symbolOffsets maps defined function and object symbols to their offsets
// within sec. In an ET_REL file st_value is an offset relative to the
// symbol's section, which is exactly what a memcpy loader needs. Object
// symbols carry the blob's patchable data (see WordPatch).
func symbolOffsets(f *elf.File, sec *elf.Section) (map[string]uintptr, error) {
	syms, err := f.Symbols()
	if err != nil {
		return nil, fmt.Errorf("execblob: symtab: %w", err)
	}
	offs := make(map[string]uintptr, len(syms))
	for _, s := range syms {
		if !sameSection(f, elf.SectionIndex(s.Section), sec) {
			continue
		}
		switch elf.ST_TYPE(s.Info) {
		case elf.STT_FUNC, elf.STT_OBJECT:
		default:
			continue
		}
		offs[s.Name] = uintptr(s.Value)
	}
	return offs, nil
}

// MustLoad is Load for modules whose blob is a hard requirement: there is
// no fallback to degrade to, and a zero entry point would crash on first
// use, so a load failure aborts startup with the cause.
func MustLoad(module string, image []byte, symbols []string, patches []WordPatch) []uintptr {
	funcs, err := Load(image, symbols, patches)
	if err != nil {
		panic(module + ": native blob unavailable: " + err.Error())
	}
	return funcs
}

func sameSection(f *elf.File, idx elf.SectionIndex, sec *elf.Section) bool {
	return int(idx) < len(f.Sections) && f.Sections[idx] == sec
}

// applyWords writes resolved patches into mem, which must still be writable.
func applyWords(mem []byte, words []wordAt) {
	for _, w := range words {
		binary.LittleEndian.PutUint64(mem[w.off:w.off+8], w.val)
	}
}
