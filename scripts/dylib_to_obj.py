#!/usr/bin/env python3
"""
dylib-to-obj: Extract a zero-relocation Mach-O MH_OBJECT from a dylib.

This script reads a Mach-O dylib (where all relocations are resolved),
extracts the __TEXT segment content, and writes a new MH_OBJECT with:
  - Single __TEXT,__text section covering VA 0 to end-of-text
  - Symbol table with function symbols at their original VA offsets
  - Zero relocation entries

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
    symtab_info = None
    build_version = None

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

            if segname == '__TEXT':
                # Parse sections
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

                text_seg = {
                    'vmaddr': vmaddr,
                    'vmsize': vmsize,
                    'fileoff': fileoff,
                    'filesize': filesize,
                    'sections': sections,
                }

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

    return text_seg, symbols, build_version


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


def patch_adrp_to_adr(blob, extent):
    """Patch ADRP+ADD and ADRP+LDR pairs within the code section.

    Go's linker does not guarantee page-aligned placement of syso sections.
    ADRP computes page(PC) + page_offset, which breaks when the section
    isn't page-aligned.

    We handle two patterns:

    1. ADRP+ADD → ADR+NOP
       ADRP Xd, #page_delta; ADD Xd, Xd, #imm12
       → ADR Xd, target; NOP

    2. ADRP+LDR → ADR+LDR[offset=0]
       ADRP Xd, #page_delta; LDR Rt, [Xd, #imm]
       → ADR Xd, target; LDR Rt, [Xd]
       (Works for all LDR variants: Xt, Dt, Qt, St, etc.)
    """
    blob = bytearray(blob)
    patches_add = 0
    patches_ldr = 0

    NOP = 0xD503201F

    for off in range(0, extent - 4, 4):
        inst = struct.unpack_from('<I', blob, off)[0]

        adrp = decode_adrp(inst)
        if adrp is None:
            continue

        rd, page_delta = adrp
        adrp_pc_page = (off >> 12) << 12  # page(PC of ADRP)

        if off + 4 >= extent:
            continue
        next_inst = struct.unpack_from('<I', blob, off + 4)[0]

        # --- Pattern 1: ADRP+ADD ---
        # ADD (immediate, 64-bit): [31]=1 [30:29]=00 [28:24]=10001
        if (next_inst >> 24) & 0xFF == 0x91:
            add_rd = next_inst & 0x1F
            add_rn = (next_inst >> 5) & 0x1F
            if add_rd != rd or add_rn != rd:
                continue
            add_imm12 = (next_inst >> 10) & 0xFFF
            add_shift = (next_inst >> 22) & 0x3
            if add_shift == 1:
                add_imm12 <<= 12

            target_va = adrp_pc_page + page_delta + add_imm12
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

        # --- Pattern 2: ADRP+LDR (unsigned offset) ---
        # LDR (unsigned offset): [31:30]=size [29:27]=111 [26]=V [25:24]=01
        # Matches: LDR Xt, LDR Dt, LDR Qt, LDR St, LDR Wt, etc.
        if (next_inst >> 24) & 0x3F in (0xF9, 0xFD, 0x3D, 0xB9, 0xBD, 0x79, 0x39):
            # All unsigned-offset LDR variants share:
            #   [21:10]=imm12 (scaled by access size)
            #   [9:5]=Rn  [4:0]=Rt
            ldr_rn = (next_inst >> 5) & 0x1F
            if ldr_rn != rd:
                continue

            # Determine access size for scaling
            opc_hi = (next_inst >> 24) & 0xFF
            if opc_hi == 0xF9:    scale = 8   # LDR Xt
            elif opc_hi == 0xFD:  scale = 8   # LDR Dt (64-bit SIMD)
            elif opc_hi == 0x3D:
                opc = (next_inst >> 22) & 0x3
                if opc == 0x3:    scale = 16  # LDR Qt (128-bit SIMD)
                elif opc == 0x1:  scale = 4   # LDR St (32-bit SIMD)
                else:             continue
            elif opc_hi == 0xB9:  scale = 4   # LDR Wt
            elif opc_hi == 0xBD:  scale = 4   # LDR St
            elif opc_hi == 0x79:  scale = 2   # LDRH Wt
            elif opc_hi == 0x39:  scale = 1   # LDRB Wt
            else:                 continue

            imm12 = (next_inst >> 10) & 0xFFF
            byte_offset = imm12 * scale

            target_va = adrp_pc_page + page_delta + byte_offset
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

    total = patches_add + patches_ldr
    if total:
        print(f"  Total ADRP patches: {total} ({patches_add} ADD, {patches_ldr} LDR)")
    else:
        print(f"  No ADRP pairs found to patch")

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

    text_seg, symbols, build_version = parse_macho(data)

    # Determine extent of useful content in __TEXT
    extent = find_text_extent(text_seg)
    print(f"__TEXT segment: vmaddr=0x{text_seg['vmaddr']:X}, vmsize=0x{text_seg['vmsize']:X}")
    print(f"Useful content extent: 0x{extent:X} ({extent} bytes)")
    print(f"Symbols: {len(symbols)}")
    for sym in symbols:
        print(f"  {sym['name']} at 0x{sym['value']:X}")

    # Extract bytes from file: VA 0 maps to fileoff of segment
    # We need bytes from fileoff to fileoff + extent
    seg_fileoff = text_seg['fileoff']
    text_blob = data[seg_fileoff:seg_fileoff + extent]

    # Pad to extent if needed (shouldn't be, but just in case)
    if len(text_blob) < extent:
        text_blob += b'\0' * (extent - len(text_blob))

    # Patch ADRP+ADD pairs to ADR+NOP (Go's linker doesn't page-align sections)
    text_blob = patch_adrp_to_adr(text_blob, extent)

    # Build output MH_OBJECT
    obj_data = build_mh_object(text_blob, symbols, build_version)

    with open(output_path, 'wb') as f:
        f.write(obj_data)

    print(f"\nOutput: {output_path} ({len(obj_data)} bytes)")
    print(f"  Section: __TEXT,__text ({extent} bytes, 0 relocations)")
    print(f"  Symbols: {len(symbols)}")


if __name__ == '__main__':
    main()
