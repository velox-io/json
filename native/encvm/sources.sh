#!/usr/bin/env bash
# sources.sh — encvm module build configuration
#
# Sourced by gen-natives.sh.  All paths are relative to REPO_ROOT.
#
# Variables defined here:
#   SOURCE_FILE     Main C source (compiled per mode x ISA, with LTO)
#   STDLIB_SOURCES  Minimal C runtime (memcpy/memset); compiled with -fno-builtin-*
#   EXTRA_SOURCES   Additional C sources compiled once per target (LTO, min ISA)
#   TARGET_DIR      Directory where the final .syso is placed

SOURCE_FILE="native/encvm/impl/encvm.c"

STDLIB_SOURCES="native/stdlib/memory.c"

EXTRA_SOURCES="
  native/encvm/impl/memfn.c
  native/encvm/impl/iface.c
  native/encvm/impl/pointer.c
  native/encvm/impl/uscale.c
"

TARGET_DIR="native/encvm"
