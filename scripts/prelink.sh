#!/usr/bin/env bash
#
# prelink.sh - Prelink object files into a single relocatable object with resolved relocations
#
# This script produces a relocatable object (.o/.syso) with all relocations resolved.
# The output can be linked by any linker (Go, ld, lld, etc.) without further relocation processing.
#
# For native builds, clang is preferred. For cross-compilation, zig cc is used.
#
# Usage:
#   ./prelink.sh -o <output> -t <target> [-s <source>] [-i <isa>] [-e <exports>] [<object.o>...]
#
# Options:
#   -s <file>     Source file: .c or .s (optional if .o files provided)
#   -o <file>     Output file, e.g. output.o or output.syso (required)
#   -t <triple>   Target triple: x86_64-linux, aarch64-linux, aarch64-macos, etc. (required)
#   -i <isa>      ISA variant: sse42, avx512, neon (optional)
#   -e <file>     Export symbol list file (darwin only; one symbol per line, with _ prefix)
#   -l            Enable Link Time Optimization (LTO)
#   -q            Quiet mode (suppress progress messages)
#   -h            Show this help
#   <object.o>    Additional pre-compiled .o files to link (optional)
#
# Examples:
#   # From source file:
#   ./prelink.sh -s native/sjmarker.c -o sjmarker_linux_amd64_avx512.o \
#                       -t x86_64-linux -i avx512
#   ./prelink.sh -s native/vector.s -o vector_linux_amd64.o \
#                       -t x86_64-linux
#
#   # From pre-compiled object files:
#   ./prelink.sh -o combined.o -t x86_64-linux \
#                       -q file1.o file2.o file3.o
#
#   # Darwin prelink with export list:
#   ./prelink.sh -l -o output.syso -t aarch64-macos \
#                       -e exports.txt file1.o file2.o
#
# Technical Background:
#   This script performs "prelinking" - it links object files together, resolves all
#   relocations, but outputs a relocatable object (not a shared library or executable).
#
#   Platform strategies:
#     Linux (ELF):
#       1. Link with -shared + custom linker script to merge .rodata into .text
#       2. Use prelink-obj tool to convert ET_DYN to ET_REL
#     Darwin (Mach-O):
#       1. Link with -dynamiclib + export list to resolve all relocations
#       2. Use prelink-obj tool to convert dylib to MH_OBJECT
#     Windows (PE/COFF):
#       1. Link with -shared to produce a DLL (requires DllMain stub)
#       2. Use prelink-obj tool to extract exports and produce raw COFF .o
#
#   The output has zero relocations — it can be used as input to any downstream
#   linker without needing to resolve any relocations.

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Default values
SOURCE=""
OUTPUT=""
TARGET=""
ISA=""
EXPORT_LIST=""
LTO=false
QUIET=false

usage() {
    sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# //'
    exit 1
}

# Log message (respects QUIET flag)
log() {
    if [ "$QUIET" = false ]; then
        echo "$@"
    fi
}

# Parse arguments
while getopts "s:o:t:i:e:lqh" opt; do
    case $opt in
        s) SOURCE="$OPTARG" ;;
        o) OUTPUT="$OPTARG" ;;
        t) TARGET="$OPTARG" ;;
        i) ISA="$OPTARG" ;;
        e) EXPORT_LIST="$OPTARG" ;;
        l) LTO=true ;;
        q) QUIET=true ;;
        h) usage ;;
        *) usage ;;
    esac
done
shift $((OPTIND-1))

# Remaining arguments are additional .o files
EXTRA_OBJS="$@"

# Validate required arguments
if [ -z "$OUTPUT" ] || [ -z "$TARGET" ]; then
    echo "Error: -o and -t are required"
    usage
fi

if [ -z "$SOURCE" ] && [ -z "$EXTRA_OBJS" ]; then
    echo "Error: Either -s <source> or .o files must be provided"
    usage
fi

if [ -n "$SOURCE" ] && [ ! -f "$SOURCE" ]; then
    echo "Error: source file not found: $SOURCE"
    exit 1
fi

# ============================================================
#  Detect native target and choose compiler
# ============================================================

# Get the native target triple for current system
get_native_target() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)  echo "x86_64-$os" ;;
        aarch64|arm64) echo "aarch64-$os" ;;
        *)             echo "$arch-$os" ;;
    esac
}

# Check if target matches native architecture
is_native_target() {
    local native=$(get_native_target)
    # Normalize target comparison (handle darwin vs macos)
    local target_norm=$(echo "$1" | sed 's/macos/darwin/')
    local native_norm=$(echo "$native" | sed 's/macos/darwin/')
    [[ "$target_norm" == "$native_norm" ]]
}

# Select compiler based on native vs cross compilation
select_compiler() {
    local target="$1"
    if is_native_target "$target"; then
        # Native build: prefer clang
        if command -v clang &> /dev/null; then
            echo "clang"
            return 0
        fi
    fi
    # Cross compilation or clang not available: use zig cc
    if command -v zig &> /dev/null; then
        echo "zig"
        return 0
    fi
    echo ""
    return 1
}

# Check for available compilers
COMPILER=""
COMPILER=$(select_compiler "$TARGET")
if [ -z "$COMPILER" ]; then
    echo "Error: No suitable compiler found."
    echo "  - For native builds: install clang"
    echo "  - For cross-compilation: install zig (brew install zig)"
    exit 1
fi

log "Using compiler: $COMPILER (target: $TARGET, native: $(is_native_target "$TARGET" && echo 'yes' || echo 'no'))"

# ============================================================
#  Detect target OS from triple
# ============================================================

get_target_os() {
    case "$1" in
        *-macos*|*-darwin*) echo "darwin" ;;
        *-linux*)           echo "linux" ;;
        *-windows*)         echo "windows" ;;
        *)                  echo "unknown" ;;
    esac
}

TARGET_OS=$(get_target_os "$TARGET")

# ============================================================
#  ISA-specific compiler flags
# ============================================================

get_isa_flags() {
    case "$1" in
        neon)   echo "" ;;
        sse42)  echo "-msse4.2 -mpclmul" ;;
        avx2)   echo "-mavx2 -mpclmul" ;;
        avx512) echo "-mavx512f -mavx512bw -mpclmul" ;;
        *)      echo "" ;;
    esac
}

# ============================================================
#  Compiler invocation functions
# ============================================================

# Build compiler command prefix (handles zig target)
compiler_cmd() {
    case "$COMPILER" in
        clang) echo "clang" ;;
        zig)   echo "zig cc -target $TARGET" ;;
    esac
}

CC_CMD=$(compiler_cmd)

# Compile C file
compile_c() {
    local src="$1"
    local out="$2"
    local lto_flag=""
    [ "$LTO" = true ] && lto_flag="-flto"
    $CC_CMD -O3 $lto_flag -fPIC $ISA_FLAGS -c "$src" -o "$out"
}

# Compile assembly file
compile_asm() {
    local src="$1"
    local out="$2"
    $CC_CMD -c "$src" -o "$out"
}

# ============================================================
#  Prelink functions (platform-specific)
#
#  Each function encapsulates the full pipeline for its platform:
#    link → (optional export filtering) → prelink-obj conversion
# ============================================================

# Derive export prefix from EXPORT_LIST (shared by ELF and Windows)
get_export_prefix() {
    if [ -n "$EXPORT_LIST" ] && [ -f "$EXPORT_LIST" ]; then
        local first_sym=$(grep -m1 . "$EXPORT_LIST")
        if [ -n "$first_sym" ]; then
            # Strip the last two underscore-delimited tokens (mode and isa)
            # e.g. "vj_vm_exec_fast_sse42" → "vj_vm_exec"
            printf '%s' "$first_sym" | sed 's/_[^_]*_[^_]*$//'
        fi
    fi
}

# Run prelink-obj with optional export prefix and quiet flag
run_prelink_obj() {
    local output="$1"
    local input="$2"
    local prefix="$3"

    local flags=""
    [ -n "$prefix" ] && flags="-export-prefix $prefix"
    [ "$QUIET" = true ] && flags="-q $flags"

    "$PRELINK_OBJ" $flags -o "$output" "$input"
}

# ELF (Linux, etc.): -shared + linker script → prelink-obj
prelink_elf() {
    local output="$1"
    shift
    local objs="$@"
    local merged_so="$WORKDIR/${BASENAME_NOEXT}.so"
    local lto_flag=""
    [ "$LTO" = true ] && lto_flag="-flto"

    # Create linker script that merges .rodata into .text
    # The ALIGN(64) ensures SIMD constant tables are properly aligned
    cat > "$TMPDIR/merge.ld" << 'LDEOF'
PHDRS {
  text PT_LOAD FLAGS(5); /* R_X = 4 | 1 = 5 */
}
SECTIONS {
  .text : {
    *(.text*)
    . = ALIGN(64);
    *(.rodata*)
    *(.rodata.cst16*)
    *(.rodata.cst32*)
  } :text
  /DISCARD/ : {
    *(.comment)
    *(.note*)
    *(.debug*)
    *(.eh_frame*)
  }
}
LDEOF

    # -Bsymbolic-functions: bind function references to local definitions,
    #   preventing PLT indirection for internal calls. Without this, the linker
    #   creates PLT stubs for exported functions called within the same .so,
    #   which land outside .text and are lost during prelink-obj extraction.
    #   Note: zig's LLD does not support -Bsymbolic-functions, so we skip it.
    local symbolic_flag=""
    case "$COMPILER" in
        clang) symbolic_flag="-Wl,-Bsymbolic-functions" ;;
        zig)   symbolic_flag="" ;;
    esac

    log "  Linking..."
    $CC_CMD -shared $lto_flag -nostdlib $symbolic_flag -Wl,--build-id=none -Wl,-T,"$TMPDIR/merge.ld" $objs -o "$merged_so"

    log "  Creating object file..."
    run_prelink_obj "$output" "$merged_so" "$(get_export_prefix)"
}

# Darwin (Mach-O): -dynamiclib → prelink-obj
prelink_darwin() {
    local output="$1"
    shift
    local objs="$@"
    local dylib_tmp="$WORKDIR/${BASENAME_NOEXT}.dylib"
    local lto_flag=""
    [ "$LTO" = true ] && lto_flag="-flto"

    local export_flag=""
    if [ -n "$EXPORT_LIST" ] && [ -f "$EXPORT_LIST" ]; then
        # zig's Mach-O LLD does not support -exported_symbols_list;
        # skip the flag when cross-compiling with zig. The extra exported
        # symbols are harmless — prelink-obj extracts all N_EXT symbols.
        if [ "$COMPILER" != "zig" ]; then
            export_flag="-Wl,-exported_symbols_list,$EXPORT_LIST"
        fi
    fi

    log "  Linking dylib..."
    log "    $dylib_tmp"
    $CC_CMD -O3 $lto_flag -dynamiclib $export_flag $objs -o "$dylib_tmp"

    log "  Converting dylib to object..."
    run_prelink_obj "$output" "$dylib_tmp" ""
}

# Windows (PE/COFF): -shared DLL → prelink-obj
prelink_windows() {
    local output="$1"
    shift
    local objs="$@"
    local dll_tmp="$WORKDIR/${BASENAME_NOEXT}.dll"
    local lto_flag=""
    [ "$LTO" = true ] && lto_flag="-flto"

    # Compile a minimal DllMain stub (required for -shared on Windows)
    log "  Compiling DllMain stub..."
    echo 'int _DllMainCRTStartup(void *h, unsigned r, void *p) { (void)h; (void)r; (void)p; return 1; }' > "$TMPDIR/dllmain.c"
    $CC_CMD -c "$TMPDIR/dllmain.c" -o "$TMPDIR/dllmain.obj"

    log "  Linking DLL..."
    log "    $dll_tmp"
    $CC_CMD -shared $lto_flag -nostdlib -fno-sanitize=undefined $objs "$TMPDIR/dllmain.obj" -o "$dll_tmp"

    log "  Converting DLL to COFF object..."
    run_prelink_obj "$output" "$dll_tmp" "$(get_export_prefix)"
}

# ============================================================
#  Build process
# ============================================================

ISA_FLAGS=$(get_isa_flags "$ISA")
WORKDIR="$REPO_ROOT/build/prelink"
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

BASENAME=$(basename "$OUTPUT")
BASENAME_NOEXT="${BASENAME%.*}"

# Build list of object files to link
ALL_OBJS=""

if [ -n "$SOURCE" ]; then
    [ "$QUIET" = false ] && echo "Building $OUTPUT from $SOURCE (isa: ${ISA:-default})"
    # Step 1: Compile to object
    log "  Compiling..."
    EXT="${SOURCE##*.}"
    case "$EXT" in
        c)
            compile_c "$SOURCE" "$TMPDIR/input.o"
            ;;
        s|S)
            compile_asm "$SOURCE" "$TMPDIR/input.o"
            ;;
        *)
            echo "Error: unsupported source file extension: .$EXT (expected .c, .s, or .S)"
            exit 1
            ;;
    esac
    ALL_OBJS="$TMPDIR/input.o"
else
    log "Building $OUTPUT from object files (isa: ${ISA:-default})"
fi

# Add extra object files
for obj in $EXTRA_OBJS; do
    if [ ! -f "$obj" ]; then
        echo "Error: object file not found: $obj"
        exit 1
    fi
    ALL_OBJS="$ALL_OBJS $obj"
done

# ============================================================
#  Platform-specific linking
# ============================================================

mkdir -p "$WORKDIR"
mkdir -p "$(dirname "$OUTPUT")"

# Build unified prelink-obj tool (used by both ELF and Mach-O paths)
PRELINK_OBJ="$REPO_ROOT/build/bin/prelink-obj"
if [ ! -x "$PRELINK_OBJ" ]; then
    log "  Building prelink-obj..."
    mkdir -p "$(dirname "$PRELINK_OBJ")"
    (cd "$REPO_ROOT/scripts/cmd/prelink-obj" && go build -o "$PRELINK_OBJ" .)
fi

case "$TARGET_OS" in
    darwin)  prelink_darwin  "$OUTPUT" $ALL_OBJS ;;
    windows) prelink_windows "$OUTPUT" $ALL_OBJS ;;
    linux)   prelink_elf     "$OUTPUT" $ALL_OBJS ;;
    *)       echo "Error: unsupported target OS: $TARGET_OS"; exit 1 ;;
esac

log "  Done: $OUTPUT ($(wc -c < "$OUTPUT" | tr -d ' ') bytes)"
