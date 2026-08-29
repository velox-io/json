#!/usr/bin/env bash
# lookup module build configuration.
#
# Builds a single ELF blob that exports the ndec_lookup_* build path
# (size_for / init / get_tier / tier_name / footprint). The hot query
# ndec_lookup_find lives inline in impl/lookup.h and is not part of the
# export surface: consumers embed the header directly.
#
# The blob is not linked. vlib_blob_*.go embeds it with go:embed and
# native/execblob maps it into executable memory at startup, so no linker
# ever sees it and the artifact needs no per-OS object format.
#
# Sourced by gen-natives.sh; all paths relative to REPO_ROOT.

SOURCE_FILE="native/vlib/impl/vlib/lookup.c"

# Provides memcpy / memset / memcmp / bzero without pulling in libc.
STDLIB_SOURCES="
  native/stdlib/memory.c
  native/stdlib/assert.c
"

EXTRA_SOURCES=""

# Append include paths to any caller-supplied EXTRA_CFLAGS (e.g. -DVJ_DEBUG
# from make gen-debug) instead of overwriting them.
EXTRA_CFLAGS="${EXTRA_CFLAGS:-} -I$REPO_ROOT/native/vlib/impl -I$REPO_ROOT/native"

TARGET_DIR="native/vlib"

# Optional base name for the generated artifact. When set, it replaces the
# default (source file basename) in the artifact file name and the mode/isa
# segments are dropped, yielding "{SYSO_PREFIX}_{os}_{arch}${SYSO_EXT}".
# Leave empty to keep the default "{basename}_{mode}_{isa}_{os}_{arch}${SYSO_EXT}".
SYSO_PREFIX="vlib"

# SYSO_EXT=".elf": the artifact is embedded and mapped at runtime, never
# linked, so it must not carry the .syso extension (the Go tool links every
# *.syso sitting in a package directory).
SYSO_EXT=".elf"

# SYSO_ARCH_ONLY=1 drops the operating system segment from the artifact
# name, so it becomes "vlib_${arch}.elf" instead of
# "vlib_${os}_${arch}.elf".
#
# The prelinked blob carries position independent code and zero relocations,
# so one build per arch serves every platform. linux builds both, and:
#
#   arm64   AAPCS64 and Apple arm64 pass integer arguments identically, so
#           darwin/arm64 runs vlib_arm64.elf unchanged. The build pins
#           -ffixed-x18 to keep clear of macOS's platform register.
#   amd64   darwin/amd64 runs vlib_amd64.elf unchanged. windows/amd64 calls
#           the same System V blob through the bridge in
#           trampoline_windows_amd64.s, which moves the arguments into RDI,
#           RSI, RDX. The build pins -mno-red-zone, since Win64 grants no red
#           zone (see gen-natives.sh for both flags).
#
# Go's register ABI is uniform across operating systems, so a blob that follows
# one platform's C convention serves the others as long as the trampolines move
# the arguments and align the stack.
#
# Nothing consumes the object format: the ELF container is parsed by
# debug/elf at startup, so linking modes (cgo, -linkmode=external, LTO) no
# longer constrain the artifact at all.
SYSO_ARCH_ONLY=1

if [ -z "$MODES" ]; then
  MODES="default"
fi

MODE_FLAGS_default=""

# All exported entry points share the ndec_lookup_ prefix. prelink-obj's
# HasPrefix filter keeps every global symbol that starts with this prefix
# and demotes everything else to local.
EXPORT_SYMBOL_PREFIX_PATTERN="ndec_lookup_"
EXPORT_SYMBOL_NAMES="ndec_lookup_size_for ndec_lookup_scratch_size ndec_lookup_init ndec_lookup_get_tier ndec_lookup_tier_name ndec_lookup_footprint"
