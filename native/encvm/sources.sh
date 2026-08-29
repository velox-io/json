#!/usr/bin/env bash
# encvm module build configuration
#
# Sourced by gen-natives.sh.  All paths are relative to REPO_ROOT.
#
# Variables:
#   SOURCE_FILE           Main C source (compiled once per mode, with LTO)
#   STDLIB_SOURCES        Minimal C runtime (memcpy/memset/memmove/bzero); compiled with stdlib specific no-builtin flags
#   EXTRA_SOURCES         Additional C sources compiled once per target (LTO, min ISA)
#   TARGET_DIR            Directory where the final artifact is placed
#   MODES                 Space-separated build modes; SOURCE_FILE is compiled once per mode
#   MODE_FLAGS_<mode>     C preprocessor flags for each mode
#   SYSO_MERGE_MODES      Link all mode objects into ONE arch-canonical blob
#   EXPORT_SYMBOL_PREFIX_PATTERN
#                         prelink-obj HasPrefix filter keeping every vj_vm_exec_* global

SOURCE_FILE="native/encvm/impl/encvm.c"

STDLIB_SOURCES="native/stdlib/memory.c"

# util/log.c provides vj_fprintf_stderr (and the loader-patched vj_log_write_nr)
EXTRA_SOURCES="
  native/util/log.c
"

TARGET_DIR="native/encvm"

# Artifact naming: one arch-canonical ELF blob per arch (encvm_amd64.elf /
# encvm_arm64.elf), exactly like ndec and vlib. SYSO_PREFIX replaces the default
# "{basename}_{mode}_{isa}_{os}_{arch}" name; SYSO_EXT=".elf" keeps the Go tool
# from linking it (the Go linker picks up every *.syso in a package directory);
# SYSO_ARCH_ONLY=1 drops the os segment. The blob is embedded by
# encvm_blob_*.go and mapped at runtime through native/execblob.
SYSO_PREFIX="encvm"
SYSO_EXT=".elf"
SYSO_ARCH_ONLY=1

# All three mode specializations share the single blob: full (indent + escape
# flags), fast (no indent, unconditional fast escape), compact (no indent).
# Each mode compiles SOURCE_FILE separately with its MODE_FLAGS_, then the
# mode objects are linked together; the entry names carry no ISA segment (one
# ISA per arch), so the Go trampolines call stable names.
SYSO_MERGE_MODES=1

# Build modes.  Each mode compiles SOURCE_FILE with the corresponding MODE_FLAGS.
if [ -z "$MODES" ];then
  MODES="full compact fast"
fi
MODE_FLAGS_full="-DMODE_FULL"
MODE_FLAGS_compact="-DMODE_COMPACT"
MODE_FLAGS_fast="-DMODE_FAST"

# Entry points share the vj_vm_exec_ prefix. prelink-obj's HasPrefix filter
# keeps every global symbol with this prefix and demotes the rest to local, so
# only vj_vm_exec_full / vj_vm_exec_fast / vj_vm_exec_compact reach the loader
# as globals. Impl helpers are static inline; stdlib symbols are HIDDEN;
# util/log's vj_fprintf_stderr starts with vj_, not vj_vm_exec_.
EXPORT_SYMBOL_PREFIX_PATTERN="vj_vm_exec_"
EXPORT_SYMBOL_NAMES="vj_vm_exec_full vj_vm_exec_fast vj_vm_exec_compact"
