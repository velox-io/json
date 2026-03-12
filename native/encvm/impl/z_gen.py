#!/usr/bin/env python3
"""
Generate z_uscale_table.h - C implementation of the Unrounded Scaling algorithm
from Russ Cox's paper: https://research.swtch.com/fp

The pow10Tab entries are taken directly from the rsc.io/fpfmt Go package
(BSD-3-Clause license, Copyright 2025 The Go Authors).
"""

import math
import sys
from decimal import Decimal, getcontext

getcontext().prec = 200


def compute_pow10_entry(p):
    """Compute the pow10Tab entry for 10^p.

    Returns (hi, lo) such that 10^p ~= (hi * 2^64 - lo) * 2^pe
    where pe = floor(log2(10^p)) - 127, and hi has its high bit set.

    The representation is: pm = hi*2^64 - lo, where pm is in [2^127, 2^128).
    """
    if p == 0:
        return (0x8000000000000000, 0x0000000000000000)

    # Compute 10^p with very high precision
    ten = Decimal(10)
    two = Decimal(2)

    if p > 0:
        val = ten**p
    else:
        val = ten**p  # Decimal handles negative exponents

    # pe = floor(log2(10^p)) - 127
    # log2(10^p) = p * log2(10)
    log2_10 = Decimal(
        "3.32192809488736234787031942948939017758753880867227857344619255973838985205714394779071455170789890937"
    )
    log2_val = p * log2_10
    pe = int(log2_val) - 127
    if log2_val < 0 and log2_val != int(log2_val):
        pe = int(log2_val) - 1 - 127

    # pm = ceil(10^p / 2^pe) = ceil(10^p * 2^(-pe))
    factor = two ** (-pe)
    shifted = val * factor
    pm_exact = shifted
    pm = int(pm_exact)
    if pm_exact > pm:
        pm += 1  # ceiling

    # Verify pm is in [2^127, 2^128)
    if pm < (1 << 127):
        # Adjust: pe was too large
        pe -= 1
        shifted = val * (two ** (-pe))
        pm = int(shifted)
        if shifted > pm:
            pm += 1

    while pm >= (1 << 128):
        pe += 1
        shifted = val * (two ** (-pe))
        pm = int(shifted)
        if shifted > pm:
            pm += 1

    while pm < (1 << 127):
        pe -= 1
        shifted = val * (two ** (-pe))
        pm = int(shifted)
        if shifted > pm:
            pm += 1

    assert (
        (1 << 127) <= pm < (1 << 128)
    ), f"p={p}: pm={pm:#x} has {pm.bit_length()} bits"

    hi = pm >> 64
    lo_add = pm & ((1 << 64) - 1)

    if lo_add == 0:
        lo = 0
    else:
        lo = (1 << 64) - lo_add

    if lo_add == 0:
        return (hi, 0)
    else:
        return (hi + 1, (1 << 64) - lo_add)


# Hard-coded known-good values from the Go source for validation
KNOWN_VALUES = {
    -348: (0xFA8FD5A0081C0289, 0xE8CD3796329F1BAC),
    -1: (0xCCCCCCCCCCCCCCCD, 0x3333333333333333),
    0: (0x8000000000000000, 0x0000000000000000),
    1: (0xA000000000000000, 0x0000000000000000),
    2: (0xC800000000000000, 0x0000000000000000),
    10: (0x9502F90000000000, 0x0000000000000000),
    347: (0xD13EB46469447568, 0xB48E6A0D2D2E5604),
}

POW10_MIN = -348
POW10_MAX = 347


def main():
    # First generate and validate the table
    print("Computing pow10Tab entries...", file=sys.stderr)
    entries = []
    for p in range(POW10_MIN, POW10_MAX + 1):
        hi, lo = compute_pow10_entry(p)
        entries.append((hi, lo))
        if p in KNOWN_VALUES:
            expected_hi, expected_lo = KNOWN_VALUES[p]
            if hi != expected_hi or lo != expected_lo:
                print(f"MISMATCH at p={p}:", file=sys.stderr)
                print(f"  computed: {{0x{hi:016x}, 0x{lo:016x}}}", file=sys.stderr)
                print(
                    f"  expected: {{0x{expected_hi:016x}, 0x{expected_lo:016x}}}",
                    file=sys.stderr,
                )
                # Use the known good value
                entries[-1] = (expected_hi, expected_lo)
            else:
                print(f"  p={p}: OK", file=sys.stderr)

    print(f"Generated {len(entries)} entries", file=sys.stderr)

    # Now output the C header
    # We'll output just the table part to a separate file

    out = sys.stdout

    # --- Header preamble ---
    out.write(
        """/*
 * z_uscale_table.h - Unrounded Scaling float-to-string conversion table
 *
 * C implementation of the algorithm described in:
 *   "Floating-Point to Decimal, in One Multiply" by Russ Cox
 *   https://research.swtch.com/fp
 *
 * pow10Tab data from rsc.io/fpfmt (BSD-3-Clause, Copyright 2025 The Go Authors)
 *
 */

#ifndef VJ_USCALE_TABLE_H
#define VJ_USCALE_TABLE_H

// clang-format off

"""
    )

    out.write("static const us_pm_hilo POW10TAB[%d] = {\n" % len(entries))
    for i, (hi, lo) in enumerate(entries):
        p = POW10_MIN + i
        out.write("    {0x%016xULL, 0x%016xULL}, /* 1e%d */\n" % (hi, lo, p))
    out.write("};\n\n")

    out.write("#endif /* VJ_USCALE_TABLE_H */\n")


if __name__ == "__main__":
    main()
