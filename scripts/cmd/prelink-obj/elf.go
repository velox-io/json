package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

// extractFromELF reads an ELF shared object and returns an ExtractResult
// with the .text section bytes and function symbols.
func extractFromELF(path string) (*ExtractResult, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	if f.Type != elf.ET_DYN {
		return nil, fmt.Errorf("input must be ET_DYN (shared object), got: %s", f.Type)
	}

	machine := f.Machine

	// Find .text section
	textSec := f.Section(".text")
	if textSec == nil {
		return nil, fmt.Errorf("no .text section in %s", path)
	}

	textData, err := textSec.Data()
	if err != nil {
		return nil, fmt.Errorf("reading .text: %w", err)
	}

	textAddr := textSec.Addr

	// Read symbols
	symbols, err := f.Symbols()
	if err != nil {
		return nil, fmt.Errorf("reading symbols: %w", err)
	}

	var syms []SymInfo
	for _, s := range symbols {
		bind := elf.ST_BIND(s.Info)
		if bind != elf.STB_GLOBAL && bind != elf.STB_LOCAL {
			continue
		}
		if elf.ST_TYPE(s.Info) != elf.STT_FUNC {
			continue
		}
		if s.Size == 0 {
			continue
		}
		// Verify the symbol belongs to .text section
		if s.Section == elf.SHN_UNDEF || s.Section == elf.SHN_ABS {
			continue
		}
		if int(s.Section) >= len(f.Sections) {
			continue
		}
		sec := f.Sections[s.Section]
		if sec.Name != ".text" {
			continue
		}

		offset := s.Value - textAddr
		syms = append(syms, SymInfo{
			Name:   s.Name,
			Offset: offset,
			Size:   s.Size,
			Local:  bind == elf.STB_LOCAL,
		})
	}

	// Determine code extent: end of the last function symbol.
	codeExtent := findCodeExtent(syms)
	blobExtent := uint64(len(textData))

	return &ExtractResult{
		Blob:       textData,
		Syms:       syms,
		CodeExtent: codeExtent,
		BlobExtent: blobExtent,
		IsARM64:    machine == elf.EM_AARCH64,
		ELFMachine: machine,
	}, nil
}

// findCodeExtent returns the end offset of the last function symbol.
func findCodeExtent(syms []SymInfo) uint64 {
	var maxEnd uint64
	for _, s := range syms {
		end := s.Offset + s.Size
		if end > maxEnd {
			maxEnd = end
		}
	}
	return maxEnd
}

// writeRelocatableELF generates an ELF ET_REL object with:
//   - A single .text section containing the provided code+data blob
//   - Local and global function symbols at their correct offsets
//   - Zero relocation entries
func writeRelocatableELF(path string, textData []byte, syms []SymInfo, machine elf.Machine) error {
	var buf bytes.Buffer

	// Sort symbols: locals first, then globals
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

	// Build string tables

	// .shstrtab: section name strings
	shstrtab := newStringTable()
	shstrtab.add("") // index 0: null section name
	shText := shstrtab.add(".text")
	shSymtab := shstrtab.add(".symtab")
	shStrtab := shstrtab.add(".strtab")
	shNoteStack := shstrtab.add(".note.GNU-stack")
	shShstrtab := shstrtab.add(".shstrtab")

	// .strtab: symbol name strings
	strtab := newStringTable()
	strtab.add("") // index 0: null symbol name
	symNameIdx := make([]uint32, len(syms))
	for i, s := range syms {
		symNameIdx[i] = strtab.add(s.Name)
	}

	// Build .symtab
	const symSize = 24 // sizeof(Elf64_Sym)
	symtabData := make([]byte, symSize*(1+len(syms)))

	// Entry 0 is all zeros (null symbol) -- already zero-initialized

	for i, s := range syms {
		off := symSize * (1 + i)
		binary.LittleEndian.PutUint32(symtabData[off:], symNameIdx[i]) // st_name

		bind := elf.STB_GLOBAL
		if s.Local {
			bind = elf.STB_LOCAL
		}
		symtabData[off+4] = byte(bind)<<4 | byte(elf.STT_FUNC)      // st_info
		symtabData[off+5] = byte(elf.STV_DEFAULT)                    // st_other
		binary.LittleEndian.PutUint16(symtabData[off+6:], 1)         // st_shndx = 1 (.text)
		binary.LittleEndian.PutUint64(symtabData[off+8:], s.Offset)  // st_value
		binary.LittleEndian.PutUint64(symtabData[off+16:], s.Size)   // st_size
	}

	firstGlobal := uint32(1 + numLocal)

	// Compute layout offsets
	ehdrSize := 64
	shdrSize := 64

	textOff := align(uint64(ehdrSize), 64)
	textEnd := textOff + uint64(len(textData))

	symtabOff := align(textEnd, 8)
	symtabEnd := symtabOff + uint64(len(symtabData))

	strtabOff := symtabEnd
	strtabEnd := strtabOff + uint64(len(strtab.data))

	shstrtabOff := strtabEnd
	shstrtabEnd := shstrtabOff + uint64(len(shstrtab.data))

	shdrOff := align(shstrtabEnd, 8)

	const numSections = 6 // null, .text, .note.GNU-stack, .symtab, .strtab, .shstrtab
	totalSize := shdrOff + uint64(numSections*shdrSize)

	// Write ELF header
	buf.Grow(int(totalSize))

	var ehdr [64]byte
	copy(ehdr[0:4], "\x7fELF")
	ehdr[4] = byte(elf.ELFCLASS64)
	ehdr[5] = byte(elf.ELFDATA2LSB)
	ehdr[6] = byte(elf.EV_CURRENT)
	ehdr[7] = byte(elf.ELFOSABI_NONE)
	binary.LittleEndian.PutUint16(ehdr[16:], uint16(elf.ET_REL))
	binary.LittleEndian.PutUint16(ehdr[18:], uint16(machine))
	binary.LittleEndian.PutUint32(ehdr[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint64(ehdr[40:], shdrOff)
	binary.LittleEndian.PutUint16(ehdr[52:], uint16(ehdrSize))
	binary.LittleEndian.PutUint16(ehdr[58:], uint16(shdrSize))
	binary.LittleEndian.PutUint16(ehdr[60:], numSections)
	binary.LittleEndian.PutUint16(ehdr[62:], 5) // e_shstrndx

	buf.Write(ehdr[:])

	// Write padding + section data
	writePadding(&buf, textOff-uint64(buf.Len()))
	buf.Write(textData)

	writePadding(&buf, symtabOff-uint64(buf.Len()))
	buf.Write(symtabData)

	buf.Write(strtab.data)
	buf.Write(shstrtab.data)

	writePadding(&buf, shdrOff-uint64(buf.Len()))

	// Write section headers
	writeShdr(&buf, elf.Section64{}) // Section 0: null

	writeShdr(&buf, elf.Section64{ // Section 1: .text
		Name:      shText,
		Type:      uint32(elf.SHT_PROGBITS),
		Flags:     uint64(elf.SHF_ALLOC | elf.SHF_EXECINSTR),
		Off:       textOff,
		Size:      uint64(len(textData)),
		Addralign: 64,
	})

	writeShdr(&buf, elf.Section64{ // Section 2: .note.GNU-stack
		Name:      shNoteStack,
		Type:      uint32(elf.SHT_PROGBITS),
		Addralign: 1,
	})

	writeShdr(&buf, elf.Section64{ // Section 3: .symtab
		Name:      shSymtab,
		Type:      uint32(elf.SHT_SYMTAB),
		Off:       symtabOff,
		Size:      uint64(len(symtabData)),
		Link:      4,           // .strtab section index
		Info:      firstGlobal, // index of first global symbol
		Addralign: 8,
		Entsize:   symSize,
	})

	writeShdr(&buf, elf.Section64{ // Section 4: .strtab
		Name:      shStrtab,
		Type:      uint32(elf.SHT_STRTAB),
		Off:       strtabOff,
		Size:      uint64(len(strtab.data)),
		Addralign: 1,
	})

	writeShdr(&buf, elf.Section64{ // Section 5: .shstrtab
		Name:      shShstrtab,
		Type:      uint32(elf.SHT_STRTAB),
		Off:       shstrtabOff,
		Size:      uint64(len(shstrtab.data)),
		Addralign: 1,
	})

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

func writeShdr(buf *bytes.Buffer, s elf.Section64) {
	binary.Write(buf, binary.LittleEndian, s)
}

// verifyELFOutput re-opens the generated file and checks it has zero relocations
// and at least one exported symbol.
func verifyELFOutput(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("opening output: %w", err)
	}
	defer f.Close()

	for _, sec := range f.Sections {
		if sec.Type == elf.SHT_RELA || sec.Type == elf.SHT_REL {
			return fmt.Errorf("output has relocation section %s", sec.Name)
		}
	}

	symbols, err := f.Symbols()
	if err != nil {
		return fmt.Errorf("reading symbols from output: %w", err)
	}

	count := 0
	for _, s := range symbols {
		if elf.ST_BIND(s.Info) == elf.STB_GLOBAL && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("output has no exported function symbols")
	}

	return nil
}
