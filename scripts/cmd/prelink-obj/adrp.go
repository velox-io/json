package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
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
// Three patterns are handled:
//  1. ADRP+ADD (consecutive) -> ADR+NOP
//  2. ADRP+LDR (consecutive, unsigned offset) -> ADR+LDR[offset=0]
//  3. ADRP (split -- non-adjacent consumer) -> ADR to page base
func patchADRPtoADR(textData []byte, codeExtent, blobExtent uint64, quiet bool) ([]byte, error) {
	blob := make([]byte, len(textData))
	copy(blob, textData)

	var patchesADD, patchesLDR, patchesSplit int

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

		if off+8 > codeExtent {
			continue
		}
		nextInst := binary.LittleEndian.Uint32(blob[off+4:])

		// --- Pattern 1: ADRP+ADD (consecutive) ---
		// ADD (immediate, 64-bit): [31]=1 [30:29]=00 [28:24]=10001
		if (nextInst>>24)&0xFF == 0x91 {
			addRd := nextInst & 0x1F
			addRn := (nextInst >> 5) & 0x1F
			if addRd == rd && addRn == rd {
				addImm12 := int64((nextInst >> 10) & 0xFFF)
				addShift := (nextInst >> 22) & 0x3
				if addShift == 1 {
					addImm12 <<= 12
				}

				targetVA := targetPage + addImm12
				adrOffset := targetVA - int64(off)

				if adrOffset < -(1<<20) || adrOffset >= (1<<20) {
					if !quiet {
						fmt.Printf("  WARNING: ADRP+ADD at 0x%X target 0x%X out of ADR range (%d), skipping\n",
							off, targetVA, adrOffset)
					}
					continue
				}

				binary.LittleEndian.PutUint32(blob[off:], encodeADR(rd, adrOffset))
				binary.LittleEndian.PutUint32(blob[off+4:], arm64NOP)
				patchesADD++
				continue
			}
		}

		// --- Pattern 2: ADRP+LDR (consecutive, unsigned offset) ---
		ldrOpcHi := (nextInst >> 24) & 0xFF
		isLDR := ldrOpcHi == 0xF9 || ldrOpcHi == 0xFD || ldrOpcHi == 0x3D ||
			ldrOpcHi == 0xB9 || ldrOpcHi == 0xBD || ldrOpcHi == 0x79 || ldrOpcHi == 0x39
		if isLDR {
			ldrRn := (nextInst >> 5) & 0x1F
			if ldrRn == rd {
				var scale int64
				switch ldrOpcHi {
				case 0xF9:
					scale = 8 // LDR Xt
				case 0xFD:
					scale = 8 // LDR Dt (64-bit SIMD)
				case 0x3D:
					opc := (nextInst >> 22) & 0x3
					switch opc {
					case 0x3:
						scale = 16 // LDR Qt (128-bit SIMD)
					case 0x1:
						scale = 4 // LDR St (32-bit SIMD)
					}
				case 0xB9:
					scale = 4 // LDR Wt
				case 0xBD:
					scale = 4 // LDR St
				case 0x79:
					scale = 2 // LDRH Wt
				case 0x39:
					scale = 1 // LDRB Wt
				}

				if scale > 0 {
					imm12 := int64((nextInst >> 10) & 0xFFF)
					byteOffset := imm12 * scale

					targetVA := targetPage + byteOffset
					adrOffset := targetVA - int64(off)

					if adrOffset < -(1<<20) || adrOffset >= (1<<20) {
						if !quiet {
							fmt.Printf("  WARNING: ADRP+LDR at 0x%X target 0x%X out of ADR range (%d), skipping\n",
								off, targetVA, adrOffset)
						}
						continue
					}

					// Patch ADRP -> ADR (pointing to exact target)
					binary.LittleEndian.PutUint32(blob[off:], encodeADR(rd, adrOffset))
					// Patch LDR: zero out the imm12 field (offset now in ADR)
					ldrZeroed := nextInst &^ (0xFFF << 10)
					binary.LittleEndian.PutUint32(blob[off+4:], ldrZeroed)
					patchesLDR++
					continue
				}
			}
		}

		// --- Pattern 3: ADRP with non-adjacent consumer (split pair) ---
		adrOffset := targetPage - int64(off)
		if adrOffset < -(1<<20) || adrOffset >= (1<<20) {
			return nil, fmt.Errorf("split ADRP at 0x%X target page 0x%X out of ADR range (%d)",
				off, targetPage, adrOffset)
		}

		binary.LittleEndian.PutUint32(blob[off:], encodeADR(rd, adrOffset))
		patchesSplit++
	}

	total := patchesADD + patchesLDR + patchesSplit
	if !quiet {
		if total > 0 {
			var parts []string
			if patchesADD > 0 {
				parts = append(parts, fmt.Sprintf("%d ADD", patchesADD))
			}
			if patchesLDR > 0 {
				parts = append(parts, fmt.Sprintf("%d LDR", patchesLDR))
			}
			if patchesSplit > 0 {
				parts = append(parts, fmt.Sprintf("%d split", patchesSplit))
			}
			fmt.Printf("  ADRP patches: %d (%s)\n", total, strings.Join(parts, ", "))
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
