#!/usr/bin/env python3
"""
dylib-to-obj: Extract a zero-relocation Mach-O MH_OBJECT from a dylib.

This script reads a Mach-O dylib (where all relocations are resolved),
extracts the __TEXT segment content plus any data segments (e.g. __DATA_CONST
for jump tables), and writes a new MH_OBJECT with:
  - Single __TEXT,__text section covering VA 0 to end of all included segments
  - Symbol table with function symbols at their original VA offsets
  - Zero relocation entries
  - Chained fixup rebase pointers resolved to raw VAs

Since Go's linker may not page-align the section, ADRP+ADD pairs that
reference within the section are patched to ADR+NOP (PC-relative, no
page alignment dependency).

Usage:
    python3 dylib_to_obj.py <input.dylib> <output.o>
"""

import struct
import sys

# Mach-O constants
MH_MAGIC_64 = 0xFEEDFACF
MH_OBJECT = 0x1
MH_DYLIB = 0x6
CPU_TYPE_ARM64 = 0x0100000C
CPU_SUBTYPE_ARM64_ALL = 0x0
LC_SEGMENT_64 = 0x19
LC_SYMTAB = 0x2
LC_BUILD_VERSION = 0x32
LC_DYLD_CHAINED_FIXUPS = 0x80000034
N_SECT = 0xE
N_EXT = 0x01

PLATFORM_MACOS = 1


def read_u32(data, off):
    return struct.unpack_from('<I', data, off)[0]


def read_u64(data, off):
    return struct.unpack_from('<Q', data, off)[0]


def parse_macho(data):
    """Parse a Mach-O dylib and extract __TEXT segment info and symbols."""
    magic = read_u32(data, 0)
    if magic != MH_MAGIC_64:
        raise ValueError(f"Not a 64-bit Mach-O file (magic=0x{magic:08X})")

    cputype = read_u32(data, 4)
    cpusubtype = read_u32(data, 8)
    filetype = read_u32(data, 12)
    ncmds = read_u32(data, 16)

    if cputype != CPU_TYPE_ARM64:
        raise ValueError(f"Not ARM64 (cputype=0x{cputype:X})")

    # Parse load commands
    text_seg = None
    extra_segments = []  # non-__TEXT, non-__LINKEDIT, non-__PAGEZERO segments
    all_segments = []    # all segments in order (for chained fixups indexing)
    symtab_info = None
    build_version = None
    chained_fixups_offset = None  # file offset of chained fixups data

    off = 32  # sizeof(mach_header_64)
    for _ in range(ncmds):
        cmd = read_u32(data, off)
        cmdsize = read_u32(data, off + 4)

        if cmd == LC_SEGMENT_64:
            segname = data[off+8:off+24].split(b'\0')[0].decode()
            vmaddr = read_u64(data, off + 24)
            vmsize = read_u64(data, off + 32)
            fileoff = read_u64(data, off + 40)
            filesize = read_u64(data, off + 48)
            nsects = read_u32(data, off + 64)

            # Parse sections for every segment
            sections = []
            sect_off = off + 72  # after segment_command_64
            for _ in range(nsects):
                sectname = data[sect_off:sect_off+16].split(b'\0')[0].decode()
                sec_segname = data[sect_off+16:sect_off+32].split(b'\0')[0].decode()
                sec_addr = read_u64(data, sect_off + 32)
                sec_size = read_u64(data, sect_off + 40)
                sec_offset = read_u32(data, sect_off + 48)
                sec_nreloc = read_u32(data, sect_off + 56)
                sections.append({
                    'sectname': sectname,
                    'segname': sec_segname,
                    'addr': sec_addr,
                    'size': sec_size,
                    'offset': sec_offset,
                    'nreloc': sec_nreloc,
                })
                sect_off += 80  # sizeof(section_64)

            seg_info = {
                'segname': segname,
                'vmaddr': vmaddr,
                'vmsize': vmsize,
                'fileoff': fileoff,
                'filesize': filesize,
                'sections': sections,
            }
            all_segments.append(seg_info)

            if segname == '__TEXT':
                text_seg = seg_info
            elif segname not in ('__LINKEDIT', '__PAGEZERO'):
                extra_segments.append(seg_info)

        elif cmd == LC_DYLD_CHAINED_FIXUPS:
            dataoff = read_u32(data, off + 8)
            chained_fixups_offset = dataoff

        elif cmd == LC_SYMTAB:
            symoff = read_u32(data, off + 8)
            nsyms = read_u32(data, off + 12)
            stroff = read_u32(data, off + 16)
            strsize = read_u32(data, off + 20)
            symtab_info = {
                'symoff': symoff,
                'nsyms': nsyms,
                'stroff': stroff,
                'strsize': strsize,
            }

        elif cmd == LC_BUILD_VERSION:
            platform = read_u32(data, off + 8)
            minos = read_u32(data, off + 12)
            sdk = read_u32(data, off + 16)
            build_version = {
                'platform': platform,
                'minos': minos,
                'sdk': sdk,
            }

        off += cmdsize

    if text_seg is None:
        raise ValueError("No __TEXT segment found")
    if symtab_info is None:
        raise ValueError("No LC_SYMTAB found")

    # Extract symbols
    symbols = []
    for i in range(symtab_info['nsyms']):
        sym_off = symtab_info['symoff'] + i * 16  # sizeof(nlist_64)
        n_strx = read_u32(data, sym_off)
        n_type = data[sym_off + 4]
        n_sect = data[sym_off + 5]
        n_desc = struct.unpack_from('<H', data, sym_off + 6)[0]
        n_value = read_u64(data, sym_off + 8)

        # Only keep external defined symbols
        if (n_type & N_EXT) and (n_type & 0x0E) == N_SECT:
            # Read name from string table
            name_start = symtab_info['stroff'] + n_strx
            name_end = data.index(b'\0', name_start)
            name = data[name_start:name_end].decode()
            symbols.append({'name': name, 'value': n_value, 'sect': n_sect})

    return text_seg, extra_segments, all_segments, symbols, build_version, chained_fixups_offset


def find_text_extent(text_seg):
    """Find the byte range we need to extract from the __TEXT segment.

    We extract from VA 0 to the end of the last section that contains
    code or data (skip __unwind_info). This preserves ADRP page offsets.
    """
    max_end = 0
    for sec in text_seg['sections']:
        if sec['sectname'] in ('__unwind_info',):
            continue  # skip unwind info
        sec_end = sec['addr'] + sec['size']
        if sec_end > max_end:
            max_end = sec_end

    return max_end


def parse_chained_fixups(data, fixups_offset, all_segments):
    """Parse LC_DYLD_CHAINED_FIXUPS and return resolved rebase entries.

    Returns list of (file_offset, resolved_va) for each rebase fixup.
    Only supports DYLD_CHAINED_PTR_64_OFFSET (format=6) and
    DYLD_CHAINED_PTR_64 (format=2).
    """
    # dyld_chained_fixups_header: { fixups_version(u32), starts_offset(u32), ... }
    starts_offset = read_u32(data, fixups_offset + 4)
    starts_abs = fixups_offset + starts_offset

    # dyld_chained_starts_in_image: { seg_count(u32), seg_info_offset[seg_count](u32) }
    seg_count = read_u32(data, starts_abs)

    rebases = []

    for seg_idx in range(seg_count):
        seg_info_off = read_u32(data, starts_abs + 4 + seg_idx * 4)
        if seg_info_off == 0:
            continue  # no fixups in this segment

        seg_starts_abs = starts_abs + seg_info_off

        # dyld_chained_starts_in_segment:
        #   size(u32), page_size(u16), pointer_format(u16), segment_offset(u64),
        #   max_valid_pointer(u32), page_count(u16), page_start[page_count](u16)
        page_size = struct.unpack_from('<H', data, seg_starts_abs + 4)[0]
        pointer_format = struct.unpack_from('<H', data, seg_starts_abs + 6)[0]
        segment_offset = read_u64(data, seg_starts_abs + 8)
        page_count = struct.unpack_from('<H', data, seg_starts_abs + 20)[0]

        if pointer_format not in (2, 6):
            raise ValueError(f"Unsupported chained fixup pointer_format={pointer_format} "
                             f"in segment {seg_idx} (only format 2 and 6 supported)")

        # Find this segment's vmaddr for VA resolution
        if seg_idx >= len(all_segments):
            raise ValueError(f"Chained fixups reference segment {seg_idx} but only "
                             f"{len(all_segments)} segments exist")
        seg_vmaddr = all_segments[seg_idx]['vmaddr']

        DYLD_CHAINED_PTR_START_NONE = 0xFFFF

        for page_idx in range(page_count):
            page_start = struct.unpack_from('<H', data, seg_starts_abs + 22 + page_idx * 2)[0]
            if page_start == DYLD_CHAINED_PTR_START_NONE:
                continue

            # Walk the chain within this page
            page_file_offset = segment_offset + page_idx * page_size
            chain_offset = page_file_offset + page_start

            while True:
                raw_value = read_u64(data, chain_offset)

                # Both format 2 and 6 share the same bit layout:
                #   bit 63 = bind(1) / rebase(0)
                #   For rebase:
                #     format 2 (DYLD_CHAINED_PTR_64):
                #       bits[0:35]  = target (absolute VA)
                #       bits[36:51] = high8 (top byte of pointer, shifted to bits 56..63)
                #       bits[51:63] = next (stride=4)
                #     format 6 (DYLD_CHAINED_PTR_64_OFFSET):
                #       bits[0:35]  = target (offset from mach_header, i.e. VA for 0-based)
                #       bits[36:51] = high8
                #       bits[51:63] = next (stride=4)
                bind = (raw_value >> 63) & 1
                if not bind:
                    target = raw_value & 0x7FFFFFFFF       # bits [0:35]
                    high8 = (raw_value >> 36) & 0xFF        # bits [36:43] (8 bits at 36)
                    # Reconstruct full VA: high8 goes to bits 56..63
                    resolved_va = target | (high8 << 56)
                    rebases.append((chain_offset, resolved_va))

                # next delta (bits 51..62, 12 bits)
                next_delta = (raw_value >> 51) & 0xFFF
                if next_delta == 0:
                    break
                chain_offset += next_delta * 4

    return rebases


def encode_adr(rd, offset):
    """Encode ADR Xd, #offset (PC-relative, ±1MB range)."""
    adr_imm = offset & 0x1FFFFF  # 21-bit signed, mask to unsigned
    adr_immlo = adr_imm & 0x3
    adr_immhi = (adr_imm >> 2) & 0x7FFFF
    return (0 << 31) | (adr_immlo << 29) | (0b10000 << 24) | (adr_immhi << 5) | rd


def decode_adrp(inst):
    """Decode ADRP instruction. Returns (rd, page_delta) or None."""
    if (inst >> 24) & 0x9F != 0x90:
        return None
    rd = inst & 0x1F
    immlo = (inst >> 29) & 0x3
    immhi = (inst >> 5) & 0x7FFFF
    imm = (immhi << 2) | immlo
    if imm & (1 << 20):
        imm -= (1 << 21)
    page_delta = imm << 12
    return (rd, page_delta)


def patch_adrp_to_adr(blob, code_extent, blob_extent):
    """Patch ADRP instructions whose targets lie within the blob.

    Go's linker does not guarantee page-aligned placement of syso sections.
    ADRP computes page(PC) + page_offset, which breaks when the section
    isn't page-aligned.

    code_extent: scan range for ADRP instructions (code area only)
    blob_extent: total blob size (code + data), used for target validity check

    We handle three patterns:

    1. ADRP+ADD (consecutive) → ADR+NOP
       ADRP Xd, #page_delta; ADD Xd, Xd, #imm12
       → ADR Xd, exact_target; NOP

    2. ADRP+LDR (consecutive) → ADR+LDR[offset=0]
       ADRP Xd, #page_delta; LDR Rt, [Xd, #imm]
       → ADR Xd, exact_target; LDR Rt, [Xd]

    3. ADRP (split — non-adjacent consumers) → ADR
       ADRP Xd, #page_delta; <unrelated inst>; ...; LDR/ADD [Xd, ...]
       → ADR Xd, page_base; <consumers unchanged>
       The ADR computes the same page-base address as the original ADRP,
       so all downstream LDR [Xd, #imm] / ADD Xd, Xd, #imm continue to
       produce correct final addresses.
    """
    blob = bytearray(blob)
    patches_add = 0
    patches_ldr = 0
    patches_split = 0

    NOP = 0xD503201F

    for off in range(0, code_extent - 4, 4):
        inst = struct.unpack_from('<I', blob, off)[0]

        adrp = decode_adrp(inst)
        if adrp is None:
            continue

        rd, page_delta = adrp
        adrp_pc_page = (off >> 12) << 12  # page(PC of ADRP)
        target_page = adrp_pc_page + page_delta

        # Skip ADRP whose target is outside the blob (not our data).
        if target_page < 0 or target_page >= blob_extent + 0x1000:
            continue

        if off + 4 >= code_extent:
            continue
        next_inst = struct.unpack_from('<I', blob, off + 4)[0]

        # --- Pattern 1: ADRP+ADD (consecutive) ---
        # ADD (immediate, 64-bit): [31]=1 [30:29]=00 [28:24]=10001
        if (next_inst >> 24) & 0xFF == 0x91:
            add_rd = next_inst & 0x1F
            add_rn = (next_inst >> 5) & 0x1F
            if add_rd == rd and add_rn == rd:
                add_imm12 = (next_inst >> 10) & 0xFFF
                add_shift = (next_inst >> 22) & 0x3
                if add_shift == 1:
                    add_imm12 <<= 12

                target_va = target_page + add_imm12
                adr_offset = target_va - off

                if adr_offset < -(1 << 20) or adr_offset >= (1 << 20):
                    print(f"  WARNING: ADRP+ADD at 0x{off:X} target 0x{target_va:X} "
                          f"out of ADR range ({adr_offset}), skipping")
                    continue

                struct.pack_into('<I', blob, off, encode_adr(rd, adr_offset))
                struct.pack_into('<I', blob, off + 4, NOP)
                patches_add += 1
                print(f"  Patched ADRP+ADD at 0x{off:X}: target VA 0x{target_va:X} "
                      f"-> ADR x{rd}, {adr_offset} + NOP")
                continue

        # --- Pattern 2: ADRP+LDR (consecutive, unsigned offset) ---
        # LDR (unsigned offset): [31:30]=size [29:27]=111 [26]=V [25:24]=01
        # Matches: LDR Xt, LDR Dt, LDR Qt, LDR St, LDR Wt, etc.
        if (next_inst >> 24) & 0x3F in (0xF9, 0xFD, 0x3D, 0xB9, 0xBD, 0x79, 0x39):
            ldr_rn = (next_inst >> 5) & 0x1F
            if ldr_rn == rd:
                # Determine access size for scaling
                opc_hi = (next_inst >> 24) & 0xFF
                scale = None
                if opc_hi == 0xF9:    scale = 8   # LDR Xt
                elif opc_hi == 0xFD:  scale = 8   # LDR Dt (64-bit SIMD)
                elif opc_hi == 0x3D:
                    opc = (next_inst >> 22) & 0x3
                    if opc == 0x3:    scale = 16  # LDR Qt (128-bit SIMD)
                    elif opc == 0x1:  scale = 4   # LDR St (32-bit SIMD)
                elif opc_hi == 0xB9:  scale = 4   # LDR Wt
                elif opc_hi == 0xBD:  scale = 4   # LDR St
                elif opc_hi == 0x79:  scale = 2   # LDRH Wt
                elif opc_hi == 0x39:  scale = 1   # LDRB Wt

                if scale is not None:
                    imm12 = (next_inst >> 10) & 0xFFF
                    byte_offset = imm12 * scale

                    target_va = target_page + byte_offset
                    adr_offset = target_va - off

                    if adr_offset < -(1 << 20) or adr_offset >= (1 << 20):
                        print(f"  WARNING: ADRP+LDR at 0x{off:X} target 0x{target_va:X} "
                              f"out of ADR range ({adr_offset}), skipping")
                        continue

                    # Patch ADRP → ADR (pointing to exact target)
                    struct.pack_into('<I', blob, off, encode_adr(rd, adr_offset))
                    # Patch LDR: zero out the imm12 field (offset now in ADR)
                    ldr_zeroed = next_inst & ~(0xFFF << 10)
                    struct.pack_into('<I', blob, off + 4, ldr_zeroed)
                    patches_ldr += 1
                    print(f"  Patched ADRP+LDR at 0x{off:X}: target VA 0x{target_va:X} "
                          f"-> ADR x{rd}, {adr_offset} + LDR [x{rd}]")
                    continue

        # --- Pattern 3: ADRP with non-adjacent consumer (split pair) ---
        # The compiler scheduled other instructions between ADRP and its
        # ADD/LDR consumer.  We simply replace ADRP with ADR pointing to
        # the same page-base address.  Downstream consumers use
        # [Xd, #offset] so the final target = page_base + offset is correct.
        adr_offset = target_page - off
        if adr_offset < -(1 << 20) or adr_offset >= (1 << 20):
            print(f"  ERROR: split ADRP at 0x{off:X} target page 0x{target_page:X} "
                  f"out of ADR range ({adr_offset}), CANNOT patch",
                  file=sys.stderr)
            sys.exit(1)

        struct.pack_into('<I', blob, off, encode_adr(rd, adr_offset))
        patches_split += 1
        print(f"  Patched ADRP (split) at 0x{off:X}: page 0x{target_page:X} "
              f"-> ADR x{rd}, {adr_offset}")

    total = patches_add + patches_ldr + patches_split
    if total:
        parts = []
        if patches_add:
            parts.append(f"{patches_add} ADD")
        if patches_ldr:
            parts.append(f"{patches_ldr} LDR")
        if patches_split:
            parts.append(f"{patches_split} split")
        print(f"  Total ADRP patches: {total} ({', '.join(parts)})")
    else:
        print(f"  No ADRP pairs found to patch")

    # --- Verification: no ADRP referencing within the blob should remain ---
    remaining = []
    for off in range(0, code_extent - 4, 4):
        inst = struct.unpack_from('<I', blob, off)[0]
        adrp = decode_adrp(inst)
        if adrp is None:
            continue
        rd, page_delta = adrp
        target_page = ((off >> 12) << 12) + page_delta
        if 0 <= target_page < blob_extent + 0x1000:
            remaining.append((off, rd, target_page))

    if remaining:
        print(f"\n  ERROR: {len(remaining)} ADRP instruction(s) still reference "
              f"within the blob after patching:", file=sys.stderr)
        for off, rd, tp in remaining:
            print(f"    0x{off:X}: ADRP x{rd}, target page 0x{tp:X}",
                  file=sys.stderr)
        sys.exit(1)

    return bytes(blob)


def build_mh_object(text_blob, symbols, build_version):
    """Build a Mach-O MH_OBJECT with zero relocations."""

    # String table: null byte + symbol names
    strtab = bytearray(b'\0')
    sym_strx = []
    for sym in symbols:
        sym_strx.append(len(strtab))
        strtab.extend(sym['name'].encode())
        strtab.append(0)

    # nlist_64 entries (16 bytes each)
    symtab_data = bytearray()
    for i, sym in enumerate(symbols):
        symtab_data += struct.pack('<I', sym_strx[i])  # n_strx
        symtab_data += struct.pack('B', N_EXT | N_SECT)  # n_type: external, defined in section
        symtab_data += struct.pack('B', 1)  # n_sect: section 1 (__text)
        symtab_data += struct.pack('<H', 0)  # n_desc
        symtab_data += struct.pack('<Q', sym['value'])  # n_value

    # Layout:
    #   mach_header_64:        32 bytes
    #   LC_SEGMENT_64:         72 + 80 = 152 bytes (1 section)
    #   LC_SYMTAB:             24 bytes
    #   LC_BUILD_VERSION:      32 bytes (no tools)
    #   --- header end ---
    #   padding to 8-byte align
    #   section data (__text)
    #   symtab data
    #   strtab data

    header_size = 32  # mach_header_64
    seg_cmd_size = 72 + 80  # segment_command_64 + 1 section_64
    symtab_cmd_size = 24
    build_ver_cmd_size = 24  # sizeof(build_version_command) without tools
    total_cmd_size = seg_cmd_size + symtab_cmd_size + build_ver_cmd_size

    text_offset = align(header_size + total_cmd_size, 8)
    text_size = len(text_blob)
    symtab_offset = align(text_offset + text_size, 8)
    strtab_offset = symtab_offset + len(symtab_data)

    out = bytearray()

    # ---- mach_header_64 ----
    out += struct.pack('<I', MH_MAGIC_64)          # magic
    out += struct.pack('<I', CPU_TYPE_ARM64)         # cputype
    out += struct.pack('<I', CPU_SUBTYPE_ARM64_ALL)  # cpusubtype
    out += struct.pack('<I', MH_OBJECT)              # filetype
    out += struct.pack('<I', 3)                      # ncmds
    out += struct.pack('<I', total_cmd_size)         # sizeofcmds
    out += struct.pack('<I', 0x00002000)             # flags: MH_SUBSECTIONS_VIA_SYMBOLS
    out += struct.pack('<I', 0)                      # reserved

    # ---- LC_SEGMENT_64 with 1 section ----
    out += struct.pack('<I', LC_SEGMENT_64)          # cmd
    out += struct.pack('<I', seg_cmd_size)           # cmdsize
    out += b'\0' * 16                                # segname (empty for MH_OBJECT)
    out += struct.pack('<Q', 0)                      # vmaddr
    out += struct.pack('<Q', text_size)              # vmsize
    out += struct.pack('<Q', text_offset)            # fileoff
    out += struct.pack('<Q', text_size)              # filesize
    out += struct.pack('<I', 0x7)                    # maxprot (rwx)
    out += struct.pack('<I', 0x7)                    # initprot (rwx)
    out += struct.pack('<I', 1)                      # nsects
    out += struct.pack('<I', 0)                      # flags

    # section_64: __TEXT,__text
    out += b'__text\0\0\0\0\0\0\0\0\0\0'            # sectname (16 bytes)
    out += b'__TEXT\0\0\0\0\0\0\0\0\0\0'            # segname (16 bytes)
    out += struct.pack('<Q', 0)                      # addr
    out += struct.pack('<Q', text_size)              # size
    out += struct.pack('<I', text_offset)            # offset
    out += struct.pack('<I', 12)                     # align (2^12 = 4096, page-aligned for ADRP)
    out += struct.pack('<I', 0)                      # reloff (no relocations!)
    out += struct.pack('<I', 0)                      # nreloc (ZERO!)
    out += struct.pack('<I', 0x80000400)             # flags: S_REGULAR | S_ATTR_PURE_INSTRUCTIONS | S_ATTR_SOME_INSTRUCTIONS
    out += struct.pack('<I', 0)                      # reserved1
    out += struct.pack('<I', 0)                      # reserved2
    out += struct.pack('<I', 0)                      # reserved3

    # ---- LC_SYMTAB ----
    out += struct.pack('<I', LC_SYMTAB)              # cmd
    out += struct.pack('<I', symtab_cmd_size)        # cmdsize
    out += struct.pack('<I', symtab_offset)          # symoff
    out += struct.pack('<I', len(symbols))           # nsyms
    out += struct.pack('<I', strtab_offset)          # stroff
    out += struct.pack('<I', len(strtab))            # strsize

    # ---- LC_BUILD_VERSION ----
    if build_version:
        out += struct.pack('<I', LC_BUILD_VERSION)
        out += struct.pack('<I', build_ver_cmd_size)
        out += struct.pack('<I', build_version['platform'])
        out += struct.pack('<I', build_version['minos'])
        out += struct.pack('<I', build_version['sdk'])
        out += struct.pack('<I', 0)  # ntools
    else:
        out += struct.pack('<I', LC_BUILD_VERSION)
        out += struct.pack('<I', build_ver_cmd_size)
        out += struct.pack('<I', PLATFORM_MACOS)
        out += struct.pack('<I', 0x000F0000)  # minos: 15.0
        out += struct.pack('<I', 0x000F0500)  # sdk: 15.5
        out += struct.pack('<I', 0)  # ntools

    # ---- Padding ----
    while len(out) < text_offset:
        out.append(0)

    # ---- Section data ----
    out += text_blob

    # ---- Padding to symtab ----
    while len(out) < symtab_offset:
        out.append(0)

    # ---- Symbol table ----
    out += symtab_data

    # ---- String table ----
    out += strtab

    return bytes(out)


def align(x, a):
    return (x + a - 1) & ~(a - 1)


def main():
    if len(sys.argv) != 3:
        print(f"Usage: {sys.argv[0]} <input.dylib> <output.o>", file=sys.stderr)
        sys.exit(1)

    input_path = sys.argv[1]
    output_path = sys.argv[2]

    with open(input_path, 'rb') as f:
        data = f.read()

    text_seg, extra_segments, all_segments, symbols, build_version, chained_fixups_offset = parse_macho(data)

    # Determine code extent (end of useful __TEXT content, used for ADRP scan range)
    code_extent = find_text_extent(text_seg)
    print(f"__TEXT segment: vmaddr=0x{text_seg['vmaddr']:X}, vmsize=0x{text_seg['vmsize']:X}")
    print(f"Code extent: 0x{code_extent:X} ({code_extent} bytes)")

    if extra_segments:
        for seg in extra_segments:
            print(f"Extra segment {seg['segname']}: vmaddr=0x{seg['vmaddr']:X}, vmsize=0x{seg['vmsize']:X}")
            for sec in seg['sections']:
                print(f"  section {sec['segname']},{sec['sectname']}: "
                      f"addr=0x{sec['addr']:X}, size=0x{sec['size']:X}")

    # Determine blob extent: max VA end across __TEXT and extra segments
    blob_extent = code_extent
    for seg in extra_segments:
        seg_end = seg['vmaddr'] + seg['vmsize']
        if seg_end > blob_extent:
            blob_extent = seg_end

    print(f"Blob extent: 0x{blob_extent:X} ({blob_extent} bytes)")
    print(f"Symbols: {len(symbols)}")
    for sym in symbols:
        print(f"  {sym['name']} at 0x{sym['value']:X}")

    # Build the combined blob: __TEXT content + zero-fill gaps + extra segment content
    seg_fileoff = text_seg['fileoff']
    blob = bytearray(data[seg_fileoff:seg_fileoff + code_extent])

    # Pad __TEXT portion to code_extent if needed
    if len(blob) < code_extent:
        blob += b'\0' * (code_extent - len(blob))

    # Append extra segments (with zero-fill gaps between)
    for seg in extra_segments:
        seg_start = seg['vmaddr']
        seg_end = seg_start + seg['vmsize']

        # Zero-fill gap between current blob end and this segment's start
        if len(blob) < seg_start:
            blob += b'\0' * (seg_start - len(blob))

        # Copy segment content from file
        seg_data = data[seg['fileoff']:seg['fileoff'] + seg['filesize']]
        blob[seg_start:seg_start + len(seg_data)] = seg_data

        # Zero-fill remainder of vmsize if filesize < vmsize
        if seg['filesize'] < seg['vmsize']:
            remaining = seg['vmsize'] - seg['filesize']
            blob[seg_start + seg['filesize']:seg_end] = b'\0' * remaining

    # Ensure blob is exactly blob_extent
    if len(blob) < blob_extent:
        blob += b'\0' * (blob_extent - len(blob))

    # Apply chained fixup rebases (resolve dyld pointer format to raw VAs)
    if chained_fixups_offset is not None:
        rebases = parse_chained_fixups(data, chained_fixups_offset, all_segments)
        applied = 0
        for file_offset, resolved_va in rebases:
            # The file_offset in the dylib maps to a VA; compute blob offset
            # Find which segment contains this file_offset
            blob_off = None
            for seg in [text_seg] + extra_segments:
                if seg['fileoff'] <= file_offset < seg['fileoff'] + seg['filesize']:
                    blob_off = seg['vmaddr'] + (file_offset - seg['fileoff'])
                    break
            if blob_off is not None and blob_off + 8 <= blob_extent:
                struct.pack_into('<Q', blob, blob_off, resolved_va)
                applied += 1
        if applied:
            print(f"Applied {applied} chained fixup rebases")

    # Patch ADRP+ADD pairs to ADR+NOP (Go's linker doesn't page-align sections)
    blob = patch_adrp_to_adr(bytes(blob), code_extent, blob_extent)

    # Build output MH_OBJECT
    obj_data = build_mh_object(blob, symbols, build_version)

    with open(output_path, 'wb') as f:
        f.write(obj_data)

    print(f"\nOutput: {output_path} ({len(obj_data)} bytes)")
    print(f"  Section: __TEXT,__text ({blob_extent} bytes, 0 relocations)")
    print(f"  Symbols: {len(symbols)}")


if __name__ == '__main__':
    main()
