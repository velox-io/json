#!/usr/bin/env bash
# sources.sh — encvm module build configuration
#
# Sourced by gen-natives.sh.  All paths are relative to REPO_ROOT.
#
# Variables:
#   SOURCE_FILE           Main C source (compiled per mode × ISA, with LTO)
#   STDLIB_SOURCES        Minimal C runtime (memcpy/memset); compiled with -fno-builtin-*
#   EXTRA_SOURCES         Additional C sources compiled once per target (LTO, min ISA)
#   TARGET_DIR            Directory where the final .syso is placed
#   MODES                 Space-separated build modes; SOURCE_FILE is compiled once per mode × ISA
#   MODE_FLAGS_<mode>     C preprocessor flags for each mode
#   EXPORT_SYMBOL_PREFIX  Prefix for darwin export list: _${prefix}_${mode}_${isa}
#                         Leave empty to export all symbols.

SOURCE_FILE="native/encvm/impl/encvm.c"

STDLIB_SOURCES="native/stdlib/memory.c"

EXTRA_SOURCES="
  native/encvm/impl/iface.c
  native/encvm/impl/strfn_nonascii.c
  native/encvm/impl/uscale.c
  native/encvm/impl/log.c
  native/encvm/impl/number.c
  native/encvm/impl/base64.c
"

TARGET_DIR="native/encvm"

# Build modes.  Each mode compiles SOURCE_FILE with the corresponding MODE_FLAGS.
if [ -z "$MODES" ];then
  MODES="full compact fast"
fi
MODE_FLAGS_full="-DMODE_FULL"
MODE_FLAGS_compact="-DMODE_COMPACT"
MODE_FLAGS_fast="-DMODE_FAST"

# Darwin export symbol pattern: gen-natives.sh generates _${prefix}_${mode}_${isa}
# for the -exported_symbols_list.  Matches the C macro VJ_VM_EXEC_NAME in encvm.c.
EXPORT_SYMBOL_PREFIX="vj_vm_exec"
