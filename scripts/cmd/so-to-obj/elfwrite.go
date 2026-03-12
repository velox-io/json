package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

// writeRelocatableELF generates an ELF ET_REL object with:
//   - A single .text section containing the provided code+data blob
//   - Local and global function symbols at their correct offsets
//   - Zero relocation entries
//
// Symbols are sorted: local symbols first, then global symbols.
// This ordering is required by ELF: the .symtab section header's Info
// field records the index of the first global symbol.
//
// The layout is:
//
//	ELF Header
//	.text section data (aligned to 64 bytes)
//	.symtab section data
//	.strtab section data
//	.shstrtab section data
//	Section header table (6 entries: null, .text, .note.GNU-stack, .symtab, .strtab, .shstrtab)
func writeRelocatableELF(path string, textData []byte, syms []symInfo) error {
	var buf bytes.Buffer

	// ── Sort symbols: locals first, then globals ────────────────────
	sort.SliceStable(syms, func(i, j int) bool {
		if syms[i].Local != syms[j].Local {
			return syms[i].Local // locals before globals
		}
		return syms[i].Offset < syms[j].Offset
	})

	// Count local symbols to determine first-global index
	numLocal := 0
	for _, s := range syms {
		if s.Local {
			numLocal++
		}
	}

	// ── Build string tables first so we know offsets ──────────────────

	// .shstrtab: section name strings
	shstrtab := newStringTable()
	shNull := shstrtab.add("") // index 0: null section name
	shText := shstrtab.add(".text")
	shSymtab := shstrtab.add(".symtab")
	shStrtab := shstrtab.add(".strtab")
	shNoteStack := shstrtab.add(".note.GNU-stack")
	shShstrtab := shstrtab.add(".shstrtab")
	_ = shNull

	// .strtab: symbol name strings
	strtab := newStringTable()
	strtab.add("") // index 0: null symbol name
	symNameIdx := make([]uint32, len(syms))
	for i, s := range syms {
		symNameIdx[i] = strtab.add(s.Name)
	}

	// ── Build .symtab ────────────────────────────────────────────────
	//
	// Entry 0: null symbol (required)
	// Entry 1..numLocal: local function symbols
	// Entry numLocal+1..N: global function symbols
	// The Info field in section header: index of first global symbol

	const symSize = 24 // sizeof(Elf64_Sym)
	symtabData := make([]byte, symSize*(1+len(syms)))

	// Entry 0 is all zeros (null symbol) — already zero-initialized

	for i, s := range syms {
		off := symSize * (1 + i)
		binary.LittleEndian.PutUint32(symtabData[off:], symNameIdx[i]) // st_name

		bind := elf.STB_GLOBAL
		if s.Local {
			bind = elf.STB_LOCAL
		}
		symtabData[off+4] = byte(bind)<<4 | byte(elf.STT_FUNC)        // st_info
		symtabData[off+5] = byte(elf.STV_DEFAULT)                      // st_other
		binary.LittleEndian.PutUint16(symtabData[off+6:], 1)           // st_shndx = 1 (.text)
		binary.LittleEndian.PutUint64(symtabData[off+8:], s.Offset)    // st_value
		binary.LittleEndian.PutUint64(symtabData[off+16:], s.Size)     // st_size
	}

	// First global symbol index: null(0) + numLocal locals → index numLocal+1
	firstGlobal := uint32(1 + numLocal)

	// ── Compute layout offsets ───────────────────────────────────────

	ehdrSize := 64 // sizeof(Elf64_Ehdr)
	shdrSize := 64 // sizeof(Elf64_Shdr)

	// .text starts right after ELF header, aligned to 64 bytes
	textOff := align(uint64(ehdrSize), 64)
	textEnd := textOff + uint64(len(textData))

	// .symtab follows .text, aligned to 8
	symtabOff := align(textEnd, 8)
	symtabEnd := symtabOff + uint64(len(symtabData))

	// .strtab follows .symtab, aligned to 1
	strtabOff := symtabEnd
	strtabEnd := strtabOff + uint64(len(strtab.data))

	// .shstrtab follows .strtab
	shstrtabOff := strtabEnd
	shstrtabEnd := shstrtabOff + uint64(len(shstrtab.data))

	// Section header table follows .shstrtab, aligned to 8
	shdrOff := align(shstrtabEnd, 8)

	const numSections = 6 // null, .text, .note.GNU-stack, .symtab, .strtab, .shstrtab
	totalSize := shdrOff + uint64(numSections*shdrSize)

	// ── Write ELF header ─────────────────────────────────────────────

	buf.Grow(int(totalSize))

	var ehdr [64]byte
	// e_ident
	copy(ehdr[0:4], "\x7fELF")
	ehdr[4] = byte(elf.ELFCLASS64)
	ehdr[5] = byte(elf.ELFDATA2LSB)
	ehdr[6] = byte(elf.EV_CURRENT)
	ehdr[7] = byte(elf.ELFOSABI_NONE)
	// e_type
	binary.LittleEndian.PutUint16(ehdr[16:], uint16(elf.ET_REL))
	// e_machine
	binary.LittleEndian.PutUint16(ehdr[18:], uint16(elf.EM_X86_64))
	// e_version
	binary.LittleEndian.PutUint32(ehdr[20:], uint32(elf.EV_CURRENT))
	// e_entry = 0
	// e_phoff = 0 (no program headers for relocatable)
	// e_shoff
	binary.LittleEndian.PutUint64(ehdr[40:], shdrOff)
	// e_flags = 0
	// e_ehsize
	binary.LittleEndian.PutUint16(ehdr[52:], uint16(ehdrSize))
	// e_phentsize = 0, e_phnum = 0
	// e_shentsize
	binary.LittleEndian.PutUint16(ehdr[58:], uint16(shdrSize))
	// e_shnum
	binary.LittleEndian.PutUint16(ehdr[60:], numSections)
	// e_shstrndx = 5 (.shstrtab is section index 5)
	binary.LittleEndian.PutUint16(ehdr[62:], 5)

	buf.Write(ehdr[:])

	// ── Write padding + section data ─────────────────────────────────

	// Pad to textOff
	writePadding(&buf, textOff-uint64(buf.Len()))
	buf.Write(textData)

	// Pad to symtabOff
	writePadding(&buf, symtabOff-uint64(buf.Len()))
	buf.Write(symtabData)

	// .strtab (no padding needed, strtabOff == symtabEnd)
	buf.Write(strtab.data)

	// .shstrtab
	buf.Write(shstrtab.data)

	// Pad to shdrOff
	writePadding(&buf, shdrOff-uint64(buf.Len()))

	// ── Write section headers ────────────────────────────────────────

	// Section 0: null
	writeShdr(&buf, elf.Section64{})

	// Section 1: .text
	writeShdr(&buf, elf.Section64{
		Name:      shText,
		Type:      uint32(elf.SHT_PROGBITS),
		Flags:     uint64(elf.SHF_ALLOC | elf.SHF_EXECINSTR),
		Off:       textOff,
		Size:      uint64(len(textData)),
		Addralign: 64,
	})

	// Section 2: .note.GNU-stack (empty, marks stack as non-executable)
	writeShdr(&buf, elf.Section64{
		Name: shNoteStack,
		Type: uint32(elf.SHT_PROGBITS),
		// No SHF_EXECINSTR → non-executable stack
		Addralign: 1,
	})

	// Section 3: .symtab
	writeShdr(&buf, elf.Section64{
		Name:      shSymtab,
		Type:      uint32(elf.SHT_SYMTAB),
		Off:       symtabOff,
		Size:      uint64(len(symtabData)),
		Link:      4,           // .strtab section index
		Info:      firstGlobal, // index of first global symbol
		Addralign: 8,
		Entsize:   symSize,
	})

	// Section 4: .strtab
	writeShdr(&buf, elf.Section64{
		Name:      shStrtab,
		Type:      uint32(elf.SHT_STRTAB),
		Off:       strtabOff,
		Size:      uint64(len(strtab.data)),
		Addralign: 1,
	})

	// Section 5: .shstrtab
	writeShdr(&buf, elf.Section64{
		Name:      shShstrtab,
		Type:      uint32(elf.SHT_STRTAB),
		Off:       shstrtabOff,
		Size:      uint64(len(shstrtab.data)),
		Addralign: 1,
	})

	// ── Write to file ────────────────────────────────────────────────

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// stringTable builds a null-terminated string table (for .strtab / .shstrtab).
type stringTable struct {
	data   []byte
	lookup map[string]uint32
}

func newStringTable() *stringTable {
	return &stringTable{
		lookup: make(map[string]uint32),
	}
}

func (t *stringTable) add(s string) uint32 {
	if idx, ok := t.lookup[s]; ok {
		return idx
	}
	idx := uint32(len(t.data))
	t.lookup[s] = idx
	t.data = append(t.data, []byte(s)...)
	t.data = append(t.data, 0)
	return idx
}

func writeShdr(buf *bytes.Buffer, s elf.Section64) {
	binary.Write(buf, binary.LittleEndian, s)
}

func writePadding(buf *bytes.Buffer, n uint64) {
	for range n {
		buf.WriteByte(0)
	}
}

func align(offset, alignment uint64) uint64 {
	return (offset + alignment - 1) &^ (alignment - 1)
}
