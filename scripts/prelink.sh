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
#   ./prelink.sh -o <output> -t <target> [-s <source>] [-i <isa>] [<object.o>...]
#
# Options:
#   -s <file>     Source file: .c or .s (optional if .o files provided)
#   -o <file>     Output file, e.g. output.o or output.syso (required)
#   -t <triple>   Target triple: x86_64-linux, aarch64-linux, etc. (required)
#   -i <isa>      ISA variant: sse42, avx512, neon (optional)
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
# Technical Background:
#   This script performs "prelinking" - it links object files together, resolves all
#   relocations, but outputs a relocatable object (not a shared library or executable).
#   The key steps are:
#
#   1. Compile with clang (native) or zig cc (cross-compilation)
#   2. Link with -shared + custom linker script to merge .rodata into .text
#   3. Use so-to-obj tool to convert ET_DYN to ET_REL
#
#   The output can be used as input to any downstream linker without needing to
#   resolve any relocations - they are all pre-resolved.
#
#   The linker script ensures proper alignment (64 bytes) for SIMD instructions:
#   - SSE4.2 movdqa requires 16-byte alignment
#   - AVX-512 vmovdqa64 requires 64-byte alignment

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Default values
SOURCE=""
OUTPUT=""
TARGET=""
ISA=""
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
while getopts "s:o:t:i:lqh" opt; do
    case $opt in
        s) SOURCE="$OPTARG" ;;
        o) OUTPUT="$OPTARG" ;;
        t) TARGET="$OPTARG" ;;
        i) ISA="$OPTARG" ;;
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
#  ISA-specific compiler flags
# ============================================================

get_isa_flags() {
    case "$1" in
        neon)   echo "" ;;
        sse42)  echo "-msse4.2 -mpclmul" ;;
        avx512) echo "-mavx512f -mavx512bw -mpclmul" ;;
        *)      echo "" ;;
    esac
}

# ============================================================
#  Compiler invocation functions
# ============================================================

# Compile C file
compile_c() {
    local src="$1"
    local out="$2"
    local lto_flag=""
    [ "$LTO" = true ] && lto_flag="-flto"
    case "$COMPILER" in
        clang)
            clang -O3 $lto_flag -fPIC $ISA_FLAGS -c "$src" -o "$out"
            ;;
        zig)
            zig cc -target "$TARGET" -O3 $lto_flag -fPIC $ISA_FLAGS -c "$src" -o "$out"
            ;;
    esac
}

# Compile assembly file
compile_asm() {
    local src="$1"
    local out="$2"
    case "$COMPILER" in
        clang)
            clang -c "$src" -o "$out"
            ;;
        zig)
            zig cc -target "$TARGET" -c "$src" -o "$out"
            ;;
    esac
}

# Link shared object
link_shared() {
    local out="$1"
    local ldscript="$2"
    shift 2
    local objs="$@"
    local lto_flag=""
    [ "$LTO" = true ] && lto_flag="-flto"
    case "$COMPILER" in
        clang)
            # -nostdlib: don't link standard libraries
            # -Wl,--as-needed: only link needed symbols
            clang -shared $lto_flag -nostdlib -Wl,-T,"$ldscript" $objs -o "$out"
            ;;
        zig)
            zig cc -target "$TARGET" -shared $lto_flag -Wl,-T,"$ldscript" $objs -o "$out"
            ;;
    esac
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
MERGED_SO="$WORKDIR/${BASENAME_NOEXT}.so"

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

# Step 2: Create linker script that merges .rodata into .text
# The ALIGN(64) ensures SIMD constant tables are properly aligned
cat > "$TMPDIR/merge.ld" << 'EOF'
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
EOF

# Step 3: Link to shared object - this resolves all relocations
log "  Linking..."
mkdir -p "$WORKDIR"
link_shared "$MERGED_SO" "$TMPDIR/merge.ld" $ALL_OBJS

# Step 4: Use so-to-obj to convert ET_DYN to ET_REL
# This tool:
#   - Extracts the resolved .text section from the .so
#   - Creates a new ELF relocatable object (ET_REL) with zero relocations
#   - Preserves global function symbols with correct offsets
SO_TO_OBJ="$REPO_ROOT/build/bin/so-to-obj"

if [ ! -x "$SO_TO_OBJ" ]; then
    log "  Building so-to-obj..."
    mkdir -p "$(dirname "$SO_TO_OBJ")"
    (cd "$REPO_ROOT/scripts/cmd/so-to-obj" && go build -o "$SO_TO_OBJ" .)
fi

log "  Creating object file..."
mkdir -p "$(dirname "$OUTPUT")"
if [ "$QUIET" = true ]; then
    "$SO_TO_OBJ" -q -o "$OUTPUT" "$MERGED_SO"
else
    "$SO_TO_OBJ" -o "$OUTPUT" "$MERGED_SO"
fi

log "  Done: $OUTPUT ($(wc -c < "$OUTPUT" | tr -d ' ') bytes)"
