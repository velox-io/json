package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

const arm64NOP = 0xD503201F

// decodeADRP decodes an AArch64 ADRP instruction.
// Returns (rd, pageDelta, true) if inst is ADRP, or (0, 0, false) otherwise.
func decodeADRP(inst uint32) (rd uint32, pageDelta int64, ok bool) {
	// ADRP: [31]=1 [30:29]=immlo [28:24]=10000 [23:5]=immhi [4:0]=rd
	if (inst>>24)&0x9F != 0x90 {
		return 0, 0, false
	}
	rd = inst & 0x1F
	immlo := (inst >> 29) & 0x3
	immhi := (inst >> 5) & 0x7FFFF
	imm := int64((immhi << 2) | immlo)
	if imm&(1<<20) != 0 {
		imm -= 1 << 21
	}
	pageDelta = imm << 12
	return rd, pageDelta, true
}

// encodeADR encodes an AArch64 ADR instruction: ADR Xd, #offset.
func encodeADR(rd uint32, offset int64) uint32 {
	imm := uint32(offset) & 0x1FFFFF // 21-bit signed, mask to unsigned
	immlo := imm & 0x3
	immhi := (imm >> 2) & 0x7FFFF
	return (0 << 31) | (immlo << 29) | (0b10000 << 24) | (immhi << 5) | rd
}

// patchADRPtoADR patches ADRP instructions in the blob whose targets
// lie within the blob. Since Go's linker does not guarantee page-aligned
// placement, ADRP (which uses page(PC) + page_offset) would break.
//
// Every in-blob ADRP is rewritten to ADR pointing at the ORIGINAL page
// base, and the consuming instruction (ADD #imm / LDR [rd,#imm]) is left
// untouched. This is semantically identical to the original ADRP: it
// loaded the page base into rd, and every consumer added its own offset.
//
// An earlier design tried to fold the consumer's offset into the ADR and
// zero the consumer immediate. That is unsafe: clang routinely emits a
// single ADRP whose page-base result is shared by SEVERAL consumers (e.g.
// the CMP_64 SIMD compare loads two constants via [x16] and [x16,#0xf80]
// off the same ADRP). Folding the offset for the first consumer and zeroing
// its immediate leaves the other consumers reading rd+their_own_offset from
// the wrong base. Pointing the ADR at the page base keeps every consumer,
// adjacent or not, correct.
func patchADRPtoADR(textData []byte, codeExtent, blobExtent uint64, quiet bool) ([]byte, error) {
	blob := make([]byte, len(textData))
	copy(blob, textData)

	var patches int

	for off := uint64(0); off+4 <= codeExtent; off += 4 {
		inst := binary.LittleEndian.Uint32(blob[off:])

		rd, pageDelta, ok := decodeADRP(inst)
		if !ok {
			continue
		}

		adrpPCPage := int64(off>>12) << 12
		targetPage := adrpPCPage + pageDelta

		// Skip ADRP whose target is outside the blob
		if targetPage < 0 || targetPage >= int64(blobExtent)+0x1000 {
			continue
		}

		// Rewrite ADRP -> ADR to the page base. The consumer instruction
		// (ADD/LDR with its own immediate) is preserved verbatim, so any
		// number of consumers sharing this rd stay correct.
		adrOffset := targetPage - int64(off)
		if adrOffset < -(1<<20) || adrOffset >= (1<<20) {
			return nil, fmt.Errorf("ADRP at 0x%X target page 0x%X out of ADR range (%d)",
				off, targetPage, adrOffset)
		}

		binary.LittleEndian.PutUint32(blob[off:], encodeADR(rd, adrOffset))
		patches++
	}

	if !quiet {
		if patches > 0 {
			fmt.Printf("  ADRP patches: %d\n", patches)
		} else {
			fmt.Printf("  No ADRP patches needed\n")
		}
	}

	// Verify no ADRP referencing within the blob remains
	var remaining int
	for off := uint64(0); off+4 <= codeExtent; off += 4 {
		inst := binary.LittleEndian.Uint32(blob[off:])
		_, pageDelta, ok := decodeADRP(inst)
		if !ok {
			continue
		}
		adrpPCPage := int64(off>>12) << 12
		tp := adrpPCPage + pageDelta
		if tp >= 0 && tp < int64(blobExtent)+0x1000 {
			remaining++
			if !quiet {
				fmt.Fprintf(os.Stderr, "  ERROR: remaining ADRP at 0x%X target page 0x%X\n", off, tp)
			}
		}
	}
	if remaining > 0 {
		return nil, fmt.Errorf("%d ADRP instruction(s) still reference within the blob after patching", remaining)
	}

	return blob, nil
}
