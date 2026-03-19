package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Mach-O constants
const (
	machHeaderSize64 = 32
	segmentCmdSize64 = 72
	section64Size    = 80
	nlist64Size      = 16

	mhMagic64 = 0xFEEDFACF
	mhObject  = 0x1
	// mhDylib            = 0x6
	cpuTypeARM64       = 0x0100000C
	cpuSubtypeARM64All = 0x0

	lcSegment64         = 0x19
	lcSymtab            = 0x2
	lcBuildVersion      = 0x32
	lcDyldChainedFixups = 0x80000034

	nSect = 0x0E
	nExt  = 0x01

	platformMacOS = 1
)

// machoSegment holds parsed Mach-O segment info.
type machoSegment struct {
	Name     string
	VMAddr   uint64
	VMSize   uint64
	FileOff  uint64
	FileSize uint64
	Sections []machoSection
}

// machoSection holds parsed Mach-O section info.
type machoSection struct {
	SectName string
	SegName  string
	Addr     uint64
	Size     uint64
	Offset   uint32
	NReloc   uint32
}

// machoSymtabInfo holds LC_SYMTAB parameters.
type machoSymtabInfo struct {
	SymOff  uint32
	NSyms   uint32
	StrOff  uint32
	StrSize uint32
}

// machoSymbol holds a parsed Mach-O symbol.
type machoSymbol struct {
	Name  string
	Value uint64
	Sect  uint8
}

func readU32(data []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(data[off:])
}

func readU64(data []byte, off int) uint64 {
	return binary.LittleEndian.Uint64(data[off:])
}

func readCString(data []byte, off int) string {
	end := off
	for end < len(data) && data[end] != 0 {
		end++
	}
	return string(data[off:end])
}

func readFixedString(data []byte, off, maxLen int) string {
	end := off
	for i := 0; i < maxLen && end < len(data); i++ {
		if data[end] == 0 {
			break
		}
		end++
	}
	return string(data[off:end])
}

// parseMachO parses a Mach-O dylib and extracts segments, symbols, build version,
// and chained fixups offset.
func parseMachO(data []byte) (textSeg *machoSegment, extraSegs, allSegs []machoSegment,
	symbols []machoSymbol, buildVer *BuildVersion, chainedFixupsOff int, err error) {

	magic := readU32(data, 0)
	if magic != mhMagic64 {
		return nil, nil, nil, nil, nil, 0, fmt.Errorf("not a 64-bit Mach-O file (magic=0x%08X)", magic)
	}

	cputype := readU32(data, 4)
	if cputype != cpuTypeARM64 {
		return nil, nil, nil, nil, nil, 0, fmt.Errorf("not ARM64 (cputype=0x%X)", cputype)
	}

	ncmds := readU32(data, 16)

	var symtabInfo *machoSymtabInfo
	chainedFixupsOff = -1

	off := machHeaderSize64
	for range ncmds {
		cmd := readU32(data, off)
		cmdsize := readU32(data, off+4)

		switch cmd {
		case lcSegment64:
			segname := readFixedString(data, off+8, 16)
			vmaddr := readU64(data, off+24)
			vmsize := readU64(data, off+32)
			fileoff := readU64(data, off+40)
			filesize := readU64(data, off+48)
			nsects := readU32(data, off+64)

			var sections []machoSection
			sectOff := off + segmentCmdSize64
			for range nsects {
				sectname := readFixedString(data, sectOff, 16)
				secSegname := readFixedString(data, sectOff+16, 16)
				secAddr := readU64(data, sectOff+32)
				secSize := readU64(data, sectOff+40)
				secOffset := readU32(data, sectOff+48)
				secNReloc := readU32(data, sectOff+56)

				sections = append(sections, machoSection{
					SectName: sectname,
					SegName:  secSegname,
					Addr:     secAddr,
					Size:     secSize,
					Offset:   secOffset,
					NReloc:   secNReloc,
				})
				sectOff += section64Size
			}

			seg := machoSegment{
				Name:     segname,
				VMAddr:   vmaddr,
				VMSize:   vmsize,
				FileOff:  fileoff,
				FileSize: filesize,
				Sections: sections,
			}
			allSegs = append(allSegs, seg)

			if segname == "__TEXT" {
				segCopy := seg
				textSeg = &segCopy
			} else if segname != "__LINKEDIT" && segname != "__PAGEZERO" {
				extraSegs = append(extraSegs, seg)
			}

		case lcDyldChainedFixups:
			chainedFixupsOff = int(readU32(data, off+8))

		case lcSymtab:
			symtabInfo = &machoSymtabInfo{
				SymOff:  readU32(data, off+8),
				NSyms:   readU32(data, off+12),
				StrOff:  readU32(data, off+16),
				StrSize: readU32(data, off+20),
			}

		case lcBuildVersion:
			buildVer = &BuildVersion{
				Platform: readU32(data, off+8),
				MinOS:    readU32(data, off+12),
				SDK:      readU32(data, off+16),
			}
		}

		off += int(cmdsize)
	}

	if textSeg == nil {
		return nil, nil, nil, nil, nil, 0, fmt.Errorf("no __TEXT segment found")
	}
	if symtabInfo == nil {
		return nil, nil, nil, nil, nil, 0, fmt.Errorf("no LC_SYMTAB found")
	}

	// Extract symbols: only N_EXT | N_SECT
	for i := uint32(0); i < symtabInfo.NSyms; i++ {
		symOff := int(symtabInfo.SymOff) + int(i)*nlist64Size
		nStrx := readU32(data, symOff)
		nType := data[symOff+4]
		nSectVal := data[symOff+5]
		nValue := readU64(data, symOff+8)

		if (nType&nExt) != 0 && (nType&0x0E) == nSect {
			name := readCString(data, int(symtabInfo.StrOff)+int(nStrx))
			symbols = append(symbols, machoSymbol{
				Name:  name,
				Value: nValue,
				Sect:  nSectVal,
			})
		}
	}

	return textSeg, extraSegs, allSegs, symbols, buildVer, chainedFixupsOff, nil
}

// findTextExtent returns the end VA of the last useful section in __TEXT.
func findTextExtent(textSeg *machoSegment) uint64 {
	var maxEnd uint64
	for _, sec := range textSeg.Sections {
		if sec.SectName == "__unwind_info" {
			continue
		}
		secEnd := sec.Addr + sec.Size
		if secEnd > maxEnd {
			maxEnd = secEnd
		}
	}
	return maxEnd
}

// chainedRebase represents a resolved rebase fixup.
type chainedRebase struct {
	FileOffset int
	ResolvedVA uint64
}

// parseChainedFixups parses LC_DYLD_CHAINED_FIXUPS and returns resolved rebase entries.
func parseChainedFixups(data []byte, fixupsOffset int, allSegs []machoSegment) ([]chainedRebase, error) {
	startsOffset := readU32(data, fixupsOffset+4)
	startsAbs := fixupsOffset + int(startsOffset)

	segCount := readU32(data, startsAbs)

	var rebases []chainedRebase

	for segIdx := range segCount {
		segInfoOff := readU32(data, startsAbs+4+int(segIdx)*4)
		if segInfoOff == 0 {
			continue
		}

		segStartsAbs := startsAbs + int(segInfoOff)

		pageSize := int(binary.LittleEndian.Uint16(data[segStartsAbs+4:]))
		pointerFormat := binary.LittleEndian.Uint16(data[segStartsAbs+6:])
		segmentOffset := readU64(data, segStartsAbs+8)
		pageCount := int(binary.LittleEndian.Uint16(data[segStartsAbs+20:]))

		if pointerFormat != 2 && pointerFormat != 6 {
			return nil, fmt.Errorf("unsupported chained fixup pointer_format=%d in segment %d (only 2 and 6 supported)",
				pointerFormat, segIdx)
		}

		if int(segIdx) >= len(allSegs) {
			return nil, fmt.Errorf("chained fixups reference segment %d but only %d segments exist",
				segIdx, len(allSegs))
		}

		const chainedPtrStartNone = 0xFFFF

		for pageIdx := range pageCount {
			pageStart := int(binary.LittleEndian.Uint16(data[segStartsAbs+22+pageIdx*2:]))
			if pageStart == chainedPtrStartNone {
				continue
			}

			pageFileOffset := int(segmentOffset) + pageIdx*pageSize
			chainOffset := pageFileOffset + pageStart

			for {
				rawValue := readU64(data, chainOffset)

				// bit 63 = bind(1) / rebase(0)
				bind := (rawValue >> 63) & 1
				if bind == 0 {
					target := rawValue & 0x7FFFFFFFF // bits [0:35]
					high8 := (rawValue >> 36) & 0xFF // bits [36:43]
					resolvedVA := target | (high8 << 56)
					rebases = append(rebases, chainedRebase{
						FileOffset: chainOffset,
						ResolvedVA: resolvedVA,
					})
				}

				// next delta (bits 51..62, 12 bits)
				nextDelta := (rawValue >> 51) & 0xFFF
				if nextDelta == 0 {
					break
				}
				chainOffset += int(nextDelta) * 4
			}
		}
	}

	return rebases, nil
}

// extractFromMachO reads a Mach-O dylib and returns an ExtractResult.
func extractFromMachO(path string) (*ExtractResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	textSeg, extraSegs, allSegs, symbols, buildVer, chainedFixupsOff, err := parseMachO(data)
	if err != nil {
		return nil, err
	}

	// Determine code extent
	codeExtent := findTextExtent(textSeg)

	// Determine blob extent: max VA end across __TEXT and extra segments
	blobExtent := codeExtent
	for _, seg := range extraSegs {
		segEnd := seg.VMAddr + seg.VMSize
		if segEnd > blobExtent {
			blobExtent = segEnd
		}
	}

	// Build combined blob: __TEXT content + zero-fill gaps + extra segments
	segFileOff := int(textSeg.FileOff)
	blob := make([]byte, blobExtent)

	// Copy __TEXT content
	copyEnd := min(segFileOff+int(codeExtent), len(data))
	copy(blob, data[segFileOff:copyEnd])

	// Append extra segments (with zero-fill gaps between)
	for _, seg := range extraSegs {
		segStart := int(seg.VMAddr)
		segDataEnd := min(int(seg.FileOff)+int(seg.FileSize), len(data))
		segDataLen := segDataEnd - int(seg.FileOff)
		if segDataLen > 0 && segStart+segDataLen <= len(blob) {
			copy(blob[segStart:], data[int(seg.FileOff):int(seg.FileOff)+segDataLen])
		}
	}

	// Apply chained fixup rebases
	if chainedFixupsOff >= 0 {
		rebases, err := parseChainedFixups(data, chainedFixupsOff, allSegs)
		if err != nil {
			return nil, fmt.Errorf("parsing chained fixups: %w", err)
		}

		for _, r := range rebases {
			// Find which segment contains this file offset, compute blob offset
			blobOff := -1
			segments := append([]machoSegment{*textSeg}, extraSegs...)
			for _, seg := range segments {
				if r.FileOffset >= int(seg.FileOff) && r.FileOffset < int(seg.FileOff)+int(seg.FileSize) {
					blobOff = int(seg.VMAddr) + (r.FileOffset - int(seg.FileOff))
					break
				}
			}
			if blobOff >= 0 && blobOff+8 <= int(blobExtent) {
				binary.LittleEndian.PutUint64(blob[blobOff:], r.ResolvedVA)
			}
		}
	}

	// Convert symbols to SymInfo
	var syms []SymInfo
	for _, sym := range symbols {
		syms = append(syms, SymInfo{
			Name:   sym.Name,
			Offset: sym.Value,
			Size:   0, // Mach-O nlist_64 doesn't carry size
			Local:  false,
		})
	}

	return &ExtractResult{
		Blob:       blob,
		Syms:       syms,
		CodeExtent: codeExtent,
		BlobExtent: blobExtent,
		IsARM64:    true, // Mach-O path is always ARM64
		BuildVer:   buildVer,
	}, nil
}

// writeMachOObject builds a Mach-O MH_OBJECT with zero relocations.
func writeMachOObject(path string, blob []byte, syms []SymInfo, buildVer *BuildVersion) error {
	// Build string table: null byte + symbol names
	strtab := []byte{0}
	symStrx := make([]uint32, len(syms))
	for i, sym := range syms {
		symStrx[i] = uint32(len(strtab))
		strtab = append(strtab, []byte(sym.Name)...)
		strtab = append(strtab, 0)
	}

	// Build nlist_64 entries
	symtabData := make([]byte, nlist64Size*len(syms))
	for i, sym := range syms {
		off := nlist64Size * i
		binary.LittleEndian.PutUint32(symtabData[off:], symStrx[i])   // n_strx
		symtabData[off+4] = nExt | nSect                              // n_type
		symtabData[off+5] = 1                                         // n_sect (section 1: __text)
		binary.LittleEndian.PutUint16(symtabData[off+6:], 0)          // n_desc
		binary.LittleEndian.PutUint64(symtabData[off+8:], sym.Offset) // n_value
	}

	// Layout
	headerSize := machHeaderSize64
	segCmdSize := segmentCmdSize64 + section64Size // 1 section
	symtabCmdSize := 24
	buildVerCmdSize := 24 // no tools
	totalCmdSize := segCmdSize + symtabCmdSize + buildVerCmdSize

	textOffset := int(align(uint64(headerSize+totalCmdSize), 8))
	textSize := len(blob)
	symtabOffset := int(align(uint64(textOffset+textSize), 8))
	strtabOffset := symtabOffset + len(symtabData)

	out := make([]byte, 0, strtabOffset+len(strtab))

	// Helper to append packed data
	appendU32 := func(v uint32) { out = binary.LittleEndian.AppendUint32(out, v) }
	appendU64 := func(v uint64) { out = binary.LittleEndian.AppendUint64(out, v) }

	// ---- mach_header_64 ----
	appendU32(mhMagic64)            // magic
	appendU32(cpuTypeARM64)         // cputype
	appendU32(cpuSubtypeARM64All)   // cpusubtype
	appendU32(mhObject)             // filetype
	appendU32(3)                    // ncmds
	appendU32(uint32(totalCmdSize)) // sizeofcmds
	appendU32(0x00002000)           // flags: MH_SUBSECTIONS_VIA_SYMBOLS
	appendU32(0)                    // reserved

	// ---- LC_SEGMENT_64 with 1 section ----
	appendU32(lcSegment64)                 // cmd
	appendU32(uint32(segCmdSize))          // cmdsize
	out = append(out, make([]byte, 16)...) // segname (empty)
	appendU64(0)                           // vmaddr
	appendU64(uint64(textSize))            // vmsize
	appendU64(uint64(textOffset))          // fileoff
	appendU64(uint64(textSize))            // filesize
	appendU32(0x7)                         // maxprot (rwx)
	appendU32(0x7)                         // initprot (rwx)
	appendU32(1)                           // nsects
	appendU32(0)                           // flags

	// section_64: __TEXT,__text
	sectname := [16]byte{}
	copy(sectname[:], "__text")
	out = append(out, sectname[:]...)
	segname := [16]byte{}
	copy(segname[:], "__TEXT")
	out = append(out, segname[:]...)
	appendU64(0)                  // addr
	appendU64(uint64(textSize))   // size
	appendU32(uint32(textOffset)) // offset
	appendU32(12)                 // align (2^12 = 4096)
	appendU32(0)                  // reloff
	appendU32(0)                  // nreloc (ZERO!)
	appendU32(0x80000400)         // flags: S_REGULAR | S_ATTR_PURE_INSTRUCTIONS | S_ATTR_SOME_INSTRUCTIONS
	appendU32(0)                  // reserved1
	appendU32(0)                  // reserved2
	appendU32(0)                  // reserved3

	// ---- LC_SYMTAB ----
	appendU32(lcSymtab)              // cmd
	appendU32(uint32(symtabCmdSize)) // cmdsize
	appendU32(uint32(symtabOffset))  // symoff
	appendU32(uint32(len(syms)))     // nsyms
	appendU32(uint32(strtabOffset))  // stroff
	appendU32(uint32(len(strtab)))   // strsize

	// ---- LC_BUILD_VERSION ----
	appendU32(lcBuildVersion)
	appendU32(uint32(buildVerCmdSize))
	if buildVer != nil {
		appendU32(buildVer.Platform)
		appendU32(buildVer.MinOS)
		appendU32(buildVer.SDK)
	} else {
		appendU32(platformMacOS)
		appendU32(0x000F0000) // minos: 15.0
		appendU32(0x000F0500) // sdk: 15.5
	}
	appendU32(0) // ntools

	// ---- Padding to textOffset ----
	for len(out) < textOffset {
		out = append(out, 0)
	}

	// ---- Section data ----
	out = append(out, blob...)

	// ---- Padding to symtabOffset ----
	for len(out) < symtabOffset {
		out = append(out, 0)
	}

	// ---- Symbol table + string table ----
	out = append(out, symtabData...)
	out = append(out, strtab...)

	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
