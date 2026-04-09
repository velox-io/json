package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strings"
)

// extractFromPE reads a Windows PE DLL and returns an ExtractResult with
// the .text + .rdata blob and exported function symbols.
func extractFromPE(path string) (*ExtractResult, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	// Only support AMD64 COFF
	fh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return nil, fmt.Errorf("expected PE32+ (64-bit), got PE32")
	}

	machine := f.FileHeader.Machine
	if machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return nil, fmt.Errorf("unsupported PE machine type: 0x%x (only AMD64 supported)", machine)
	}

	// Find .text section
	var textSec *pe.Section
	for _, s := range f.Sections {
		if s.Name == ".text" {
			textSec = s
			break
		}
	}
	if textSec == nil {
		return nil, fmt.Errorf("no .text section in %s", path)
	}

	textData, err := textSec.Data()
	if err != nil {
		return nil, fmt.Errorf("reading .text: %w", err)
	}
	textVA := textSec.VirtualAddress

	// Find .rdata section (contains constants/rodata)
	var rdataSec *pe.Section
	for _, s := range f.Sections {
		if s.Name == ".rdata" {
			rdataSec = s
			break
		}
	}

	// Build blob: .text + padding + .rdata
	blob := make([]byte, len(textData))
	copy(blob, textData)
	codeExtent := uint64(len(textData))

	var rdataVA uint32
	var rdataBlobOffset uint64
	if rdataSec != nil {
		rdataData, err := rdataSec.Data()
		if err != nil {
			return nil, fmt.Errorf("reading .rdata: %w", err)
		}
		if len(rdataData) > 0 {
			rdataVA = rdataSec.VirtualAddress

			// Place .rdata at exactly (rdataVA - textVA) to preserve the
			// RIP-relative displacements the linker resolved in the DLL.
			rdataBlobOffset = uint64(rdataVA - textVA)
			if rdataBlobOffset < uint64(len(blob)) {
				return nil, fmt.Errorf(".rdata VA 0x%x overlaps .text end (0x%x bytes)",
					rdataVA, len(textData))
			}
			blob = append(blob, make([]byte, rdataBlobOffset-uint64(len(blob)))...)
			blob = append(blob, rdataData...)
		}
	}

	// Parse PE export directory for symbols
	exportDir := fh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_EXPORT]
	if exportDir.VirtualAddress == 0 || exportDir.Size == 0 {
		return nil, fmt.Errorf("no export directory in PE — ensure entry functions use __declspec(dllexport)")
	}

	syms, err := parseExports(f, exportDir, textVA, rdataVA, uint32(rdataBlobOffset))
	if err != nil {
		return nil, fmt.Errorf("parsing PE exports: %w", err)
	}

	return &ExtractResult{
		Blob:        blob,
		Syms:        syms,
		CodeExtent:  codeExtent,
		BlobExtent:  uint64(len(blob)),
		IsARM64:     false,
		COFFMachine: machine,
	}, nil
}

// parseExports reads the PE export directory and returns SymInfo entries.
// RVAs are converted to blob-relative offsets.
func parseExports(f *pe.File, dir pe.DataDirectory, textVA, rdataVA, rdataBlobOffset uint32) ([]SymInfo, error) {
	// Read export directory data from the PE
	data, err := readRVA(f, dir.VirtualAddress, dir.Size)
	if err != nil {
		return nil, fmt.Errorf("reading export directory: %w", err)
	}

	if len(data) < 40 {
		return nil, fmt.Errorf("export directory too small (%d bytes)", len(data))
	}

	numFunctions := binary.LittleEndian.Uint32(data[20:24])
	numNames := binary.LittleEndian.Uint32(data[24:28])
	addrTableRVA := binary.LittleEndian.Uint32(data[28:32])
	namePointerRVA := binary.LittleEndian.Uint32(data[32:36])
	ordinalTableRVA := binary.LittleEndian.Uint32(data[36:40])

	if numNames == 0 {
		return nil, fmt.Errorf("no named exports in PE")
	}

	// Read function address table
	addrData, err := readRVA(f, addrTableRVA, numFunctions*4)
	if err != nil {
		return nil, fmt.Errorf("reading export address table: %w", err)
	}

	// Read name pointer table
	nameData, err := readRVA(f, namePointerRVA, numNames*4)
	if err != nil {
		return nil, fmt.Errorf("reading name pointer table: %w", err)
	}

	// Read ordinal table
	ordData, err := readRVA(f, ordinalTableRVA, numNames*2)
	if err != nil {
		return nil, fmt.Errorf("reading ordinal table: %w", err)
	}

	var syms []SymInfo
	for i := range numNames {
		nameRVA := binary.LittleEndian.Uint32(nameData[i*4:])
		ordinal := binary.LittleEndian.Uint16(ordData[i*2:])
		funcRVA := binary.LittleEndian.Uint32(addrData[uint32(ordinal)*4:])

		// Read the null-terminated name
		name, err := readString(f, nameRVA)
		if err != nil {
			return nil, fmt.Errorf("reading export name at RVA 0x%x: %w", nameRVA, err)
		}

		// Convert RVA to blob offset
		offset, err := rvaToBlob(funcRVA, textVA, rdataVA, rdataBlobOffset)
		if err != nil {
			return nil, fmt.Errorf("converting RVA 0x%x for %s: %w", funcRVA, name, err)
		}

		syms = append(syms, SymInfo{
			Name:   name,
			Offset: offset,
			Size:   0, // PE exports don't carry size info; 0 is fine for COFF output
			Local:  false,
		})
	}

	return syms, nil
}

// rvaToBlob converts a PE RVA to a blob-relative offset.
func rvaToBlob(rva, textVA, rdataVA, rdataBlobOffset uint32) (uint64, error) {
	// Check if RVA falls in .text
	if rva >= textVA {
		off := uint64(rva - textVA)
		return off, nil
	}
	// Check if RVA falls in .rdata
	if rdataVA > 0 && rva >= rdataVA {
		off := uint64(rdataBlobOffset) + uint64(rva-rdataVA)
		return off, nil
	}
	return 0, fmt.Errorf("RVA 0x%x does not fall in .text (VA 0x%x) or .rdata (VA 0x%x)", rva, textVA, rdataVA)
}

// readRVA reads `size` bytes from the PE file at the given RVA by finding
// which section contains it.
func readRVA(f *pe.File, rva, size uint32) ([]byte, error) {
	for _, s := range f.Sections {
		if rva >= s.VirtualAddress && rva+size <= s.VirtualAddress+s.VirtualSize {
			offset := rva - s.VirtualAddress
			data, err := s.Data()
			if err != nil {
				return nil, err
			}
			if uint32(len(data)) < offset+size {
				return nil, fmt.Errorf("section %s data too short for RVA 0x%x+%d", s.Name, rva, size)
			}
			return data[offset : offset+size], nil
		}
	}
	return nil, fmt.Errorf("RVA 0x%x (size %d) not in any section", rva, size)
}

// readString reads a null-terminated string from the PE file at the given RVA.
func readString(f *pe.File, rva uint32) (string, error) {
	// Find the section, then scan for null
	for _, s := range f.Sections {
		if rva >= s.VirtualAddress && rva < s.VirtualAddress+s.VirtualSize {
			offset := rva - s.VirtualAddress
			data, err := s.Data()
			if err != nil {
				return "", err
			}
			data = data[offset:]
			before, _, ok := bytes.Cut(data, []byte{0})
			if !ok {
				return string(data), nil
			}
			return string(before), nil
		}
	}
	return "", fmt.Errorf("RVA 0x%x not in any section", rva)
}

// COFF constants
const (
	coffHeaderSize  = 20
	coffSectionSize = 40
	coffSymSize     = 18

	// Section characteristics
	coffTextFlags = 0x00000020 | // IMAGE_SCN_CNT_CODE
		0x20000000 | // IMAGE_SCN_MEM_EXECUTE
		0x40000000 | // IMAGE_SCN_MEM_READ
		0x00600000 // IMAGE_SCN_ALIGN_64BYTES

	// Symbol storage classes
	coffSymExternal = 2
	coffSymStatic   = 3

	// Symbol type: function
	coffSymTypeFunc = 0x20
)

// writeCOFFObject writes a raw COFF .o file (no MZ/PE wrapper) with:
//   - A single .text section containing the merged code+data blob
//   - COFF symbols for exported functions (EXTERNAL) and local functions (STATIC)
//   - Zero relocations
func writeCOFFObject(path string, textData []byte, syms []SymInfo, machine uint16) error {
	var buf bytes.Buffer

	// Sort symbols: locals first, then globals (same convention as ELF output)
	sort.SliceStable(syms, func(i, j int) bool {
		if syms[i].Local != syms[j].Local {
			return syms[i].Local
		}
		return syms[i].Offset < syms[j].Offset
	})

	// Build COFF string table (for names > 8 chars).
	// COFF string table starts at offset 4 (the 4-byte size prefix),
	// so the first string entry is at offset 4.
	var strtab bytes.Buffer
	strtab.Write([]byte{0, 0, 0, 0}) // placeholder for size

	type coffSymEntry struct {
		name    [8]byte
		value   uint32
		section uint16
		symType uint16
		class   uint8
		aux     uint8
	}

	symEntries := make([]coffSymEntry, len(syms))
	for i, s := range syms {
		var e coffSymEntry
		e.value = uint32(s.Offset)
		e.section = 1 // .text section number (1-based)
		e.symType = coffSymTypeFunc
		if s.Local {
			e.class = coffSymStatic
		} else {
			e.class = coffSymExternal
		}

		if len(s.Name) <= 8 {
			copy(e.name[:], s.Name)
		} else {
			// Long name: store offset into string table.
			// First 4 bytes = 0 (indicates long name), next 4 = offset.
			nameOff := uint32(strtab.Len())
			strtab.WriteString(s.Name)
			strtab.WriteByte(0)
			binary.LittleEndian.PutUint32(e.name[4:], nameOff)
		}

		symEntries[i] = e
	}

	// Patch string table size (first 4 bytes)
	strtabBytes := strtab.Bytes()
	binary.LittleEndian.PutUint32(strtabBytes[:4], uint32(len(strtabBytes)))

	// Layout:
	//   [COFF header: 20 bytes]
	//   [Section header: 40 bytes]
	//   [Section data: len(textData) bytes, aligned to start]
	//   [Symbol table: len(syms)*18 bytes]
	//   [String table]

	sectionDataOff := uint32(coffHeaderSize + coffSectionSize)
	symtabOff := sectionDataOff + uint32(len(textData))
	numSymbols := uint32(len(syms))

	// Write COFF file header
	var hdr [coffHeaderSize]byte
	binary.LittleEndian.PutUint16(hdr[0:], machine)     // Machine
	binary.LittleEndian.PutUint16(hdr[2:], 1)           // NumberOfSections
	binary.LittleEndian.PutUint32(hdr[8:], symtabOff)   // PointerToSymbolTable
	binary.LittleEndian.PutUint32(hdr[12:], numSymbols) // NumberOfSymbols
	binary.LittleEndian.PutUint16(hdr[16:], 0)          // SizeOfOptionalHeader
	binary.LittleEndian.PutUint16(hdr[18:], 0)          // Characteristics
	buf.Write(hdr[:])

	// Write .text section header
	var sec [coffSectionSize]byte
	copy(sec[0:8], ".text")                                        // Name
	binary.LittleEndian.PutUint32(sec[16:], uint32(len(textData))) // SizeOfRawData
	binary.LittleEndian.PutUint32(sec[20:], sectionDataOff)        // PointerToRawData
	binary.LittleEndian.PutUint32(sec[24:], 0)                     // PointerToRelocations
	binary.LittleEndian.PutUint16(sec[32:], 0)                     // NumberOfRelocations
	binary.LittleEndian.PutUint32(sec[36:], coffTextFlags)         // Characteristics
	buf.Write(sec[:])

	// Write section data
	buf.Write(textData)

	// Write symbol table
	for _, e := range symEntries {
		var sym [coffSymSize]byte
		copy(sym[0:8], e.name[:])
		binary.LittleEndian.PutUint32(sym[8:], e.value)
		binary.LittleEndian.PutUint16(sym[12:], e.section)
		binary.LittleEndian.PutUint16(sym[14:], e.symType)
		sym[16] = e.class
		sym[17] = e.aux
		buf.Write(sym[:])
	}

	// Write string table
	buf.Write(strtabBytes)

	return os.WriteFile(path, buf.Bytes(), 0644)
}

// ConvertPEToCOFF reads a PE DLL (e.g., one produced with lld-link /MERGE:.rdata=.text,
// which has a single merged .text section with zero relocations) and writes a raw
// COFF .o file suitable for Go's linker (.syso).
//
// This is the standalone equivalent of the inline Python script in gen-windows-syso.sh.
// It reuses ExtractFromPE for parsing and WriteCOFFObject for output.
func ConvertPEToCOFF(inputPath, outputPath string, exportPrefix string) error {
	result, err := extractFromPE(inputPath)
	if err != nil {
		return fmt.Errorf("extracting PE: %w", err)
	}

	if len(result.Blob) == 0 {
		return fmt.Errorf("empty .text in input")
	}
	if len(result.Syms) == 0 {
		return fmt.Errorf("no exports found in PE")
	}

	// Optional export-prefix filtering: demote non-matching symbols to LOCAL
	if exportPrefix != "" {
		hasGlobal := false
		for i := range result.Syms {
			if !result.Syms[i].Local && !strings.HasPrefix(result.Syms[i].Name, exportPrefix) {
				result.Syms[i].Local = true
			} else if !result.Syms[i].Local {
				hasGlobal = true
			}
		}
		if !hasGlobal {
			return fmt.Errorf("no global symbols match prefix %q", exportPrefix)
		}
	}

	if err := writeCOFFObject(outputPath, result.Blob, result.Syms, result.COFFMachine); err != nil {
		return fmt.Errorf("writing COFF: %w", err)
	}

	return verifyCOFFOutput(outputPath)
}

// verifyCOFFOutput manually parses the COFF header of the output file
// (Go's debug/pe cannot open raw COFF .o files) and checks:
//   - Zero relocations in the .text section
//   - At least one EXTERNAL symbol
func verifyCOFFOutput(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading output: %w", err)
	}

	if len(data) < coffHeaderSize {
		return fmt.Errorf("file too small for COFF header (%d bytes)", len(data))
	}

	numSections := binary.LittleEndian.Uint16(data[2:4])
	symtabOff := binary.LittleEndian.Uint32(data[8:12])
	numSymbols := binary.LittleEndian.Uint32(data[12:16])

	// Check relocations in each section header
	off := coffHeaderSize
	for i := range numSections {
		if off+coffSectionSize > len(data) {
			return fmt.Errorf("truncated section header %d", i)
		}
		nRelocs := binary.LittleEndian.Uint16(data[off+32 : off+34])
		if nRelocs != 0 {
			name := string(bytes.TrimRight(data[off:off+8], "\x00"))
			return fmt.Errorf("section %s has %d relocations, expected 0", name, nRelocs)
		}
		off += coffSectionSize
	}

	// Check for at least one EXTERNAL symbol
	if symtabOff == 0 || numSymbols == 0 {
		return fmt.Errorf("no symbol table in output")
	}

	hasExternal := false
	for i := range numSymbols {
		symOff := int(symtabOff) + int(i)*coffSymSize
		if symOff+coffSymSize > len(data) {
			return fmt.Errorf("truncated symbol table at entry %d", i)
		}
		storageClass := data[symOff+16]
		if storageClass == coffSymExternal {
			hasExternal = true
			break
		}
	}

	if !hasExternal {
		return fmt.Errorf("no EXTERNAL symbols in output")
	}

	return nil
}
