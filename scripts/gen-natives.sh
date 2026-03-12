#!/usr/bin/env bash
#
# gen-natives.sh - Compile C sources, generate Go integration files
#
# Usage:
#   ./gen-natives.sh [--zig] [--asm] <sources.sh> [target_os] [target_arch]
#
# Options:
#   --zig         - Force using zig cc even for native (non-cross) builds.
#   --asm         - Generate assembly files for debugging.
#
# Arguments:
#   sources.sh  - Build configuration file (relative to repo root). Defines:
#                    SOURCE_FILE, STDLIB_SOURCES, EXTRA_SOURCES, TARGET_DIR,
#                    MODES, MODE_FLAGS_<mode>, EXPORT_SYMBOL_PREFIX
#   target_os   - Target OS (linux, darwin, windows). Default: host OS.
#   target_arch - Target architecture (arm64, amd64). Default: host arch.
#
# Environment variables (optional):
#   OUTPUT_DIR    - Output directory for intermediate .o files. Default: build/native
#
# Output:
#   - {OUTPUT_DIR}/{basename}[_{mode}]_{os}_{arch}_{isa}.o
#   - {TARGET_DIR}/{basename}_{os}_{arch}.syso  (all ISAs, all modes combined)
#   - {TARGET_DIR}/asm/{basename}[_{mode}]_{os}_{arch}_{isa}.s (if --asm)
#
# Cross-compilation terminology:
#   HOST  - The machine running the compiler (current machine)
#   TARGET - The machine that will run the compiled code
#

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ============================================================
#  Parse options
# ============================================================

FORCE_ZIG=false
GEN_ASM=false
while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --zig) FORCE_ZIG=true; shift ;;
        --asm) GEN_ASM=true; shift ;;
        *)     echo "Error: Unknown option: $1"; exit 1 ;;
    esac
done

# ============================================================
#  Load build configuration from sources.sh
# ============================================================

SOURCES_FILE="${1:-}"
shift || true

if [ -z "$SOURCES_FILE" ]; then
    echo "Error: sources.sh path is required as first argument"
    echo "Usage: gen-natives.sh [--zig] [--asm] <sources.sh> [target_os] [target_arch]"
    exit 1
fi

if [ ! -f "$REPO_ROOT/$SOURCES_FILE" ]; then
    echo "Error: sources file not found: $REPO_ROOT/$SOURCES_FILE"
    exit 1
fi

# Source the configuration (sets SOURCE_FILE, STDLIB_SOURCES, EXTRA_SOURCES, TARGET_DIR,
# MODES, MODE_FLAGS_*, EXPORT_SYMBOL_PREFIX)
source "$REPO_ROOT/$SOURCES_FILE"

# Validate required variables
if [ -z "$SOURCE_FILE" ]; then
    echo "Error: SOURCE_FILE not defined in $SOURCES_FILE"
    exit 1
fi
if [ -z "$TARGET_DIR" ]; then
    echo "Error: TARGET_DIR not defined in $SOURCES_FILE"
    exit 1
fi

# Convert relative paths to absolute
SOURCE_FILE="$REPO_ROOT/$SOURCE_FILE"
TARGET_DIR="$REPO_ROOT/$TARGET_DIR"

# Convert space-separated relative paths to absolute
_abs_paths() {
    local result=""
    for p in $1; do
        p=$(echo "$p" | xargs)  # trim whitespace
        [ -z "$p" ] && continue
        result="$result $REPO_ROOT/$p"
    done
    echo "$result"
}
STDLIB_SOURCES=$(_abs_paths "$STDLIB_SOURCES")
EXTRA_SOURCES=$(_abs_paths "$EXTRA_SOURCES")

OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/build/native}"
mkdir -p "$OUTPUT_DIR"

# Derive VJ_LIB_DIR from the source file's directory
VJ_LIB_DIR=$(dirname "$SOURCE_FILE")

# Derive base name from source file
BASENAME=$(basename "$SOURCE_FILE" .c)

# ============================================================
#  Host platform detection
# ============================================================

# OS detection
HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$HOST_OS" in
    darwin)              HOST_OS="darwin" ;;
    linux)               HOST_OS="linux" ;;
    mingw*|msys*|cygwin*) HOST_OS="windows" ;;
esac

# Arch detection
HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
    arm64|aarch64) HOST_ARCH="arm64" ;;
    x86_64|amd64)  HOST_ARCH="amd64" ;;
esac

# ============================================================
#  Target platform (can be overridden by arguments)
# ============================================================

TARGET_OS="${1:-$HOST_OS}"
TARGET_ARCH="${2:-$HOST_ARCH}"

# Normalize target OS name
case "$TARGET_OS" in
    darwin)  TARGET_OS="darwin" ;;
    linux)   TARGET_OS="linux" ;;
    windows) TARGET_OS="windows" ;;
esac

# ============================================================
#  Compiler selection (zig cc for cross-compilation)
# ============================================================

# Check if cross-compiling
NEEDS_CROSS_COMPILE=false
if [ "$TARGET_OS" != "$HOST_OS" ] || [ "$TARGET_ARCH" != "$HOST_ARCH" ]; then
    NEEDS_CROSS_COMPILE=true
fi

# Build zig target triple
get_zig_target() {
    local os=$1
    local arch=$2

    case "$arch" in
        amd64)  arch="x86_64" ;;
        arm64)  arch="aarch64" ;;
    esac

    case "$os" in
        darwin)  echo "${arch}-macos" ;;
        linux)   echo "${arch}-linux" ;;
        windows) echo "${arch}-windows" ;;
        *)       echo "${arch}-${os}" ;;
    esac
}

# Build clang target triple (for clangd --target)
get_clang_target() {
    local os=$1
    local arch=$2

    case "$arch" in
        amd64)  arch="x86_64" ;;
        arm64)  arch="aarch64" ;;
    esac

    case "$os" in
        darwin)  echo "${arch}-apple-darwin" ;;
        linux)   echo "${arch}-unknown-linux-gnu" ;;
        windows) echo "${arch}-pc-windows-msvc" ;;
        *)       echo "${arch}-unknown-${os}" ;;
    esac
}

# Select compiler
USE_ZIG=false
if [ "$FORCE_ZIG" = true ] || [ "$NEEDS_CROSS_COMPILE" = true ]; then
    if command -v zig &> /dev/null; then
        USE_ZIG=true
        ZIG_TARGET=$(get_zig_target "$TARGET_OS" "$TARGET_ARCH")
        CC="zig cc -target $ZIG_TARGET"
        if [ "$FORCE_ZIG" = true ] && [ "$NEEDS_CROSS_COMPILE" = false ]; then
            echo "Using zig cc (forced, target: $ZIG_TARGET)"
        else
            echo "Cross-compiling with zig cc (target: $ZIG_TARGET)"
        fi
    else
        if [ "$FORCE_ZIG" = true ]; then
            echo "Error: --zig requested but zig is not installed."
        else
            echo "Error: Cross-compilation requires zig."
        fi
        exit 1
    fi
else
    CC="clang"
fi

# ============================================================
#  Platform-ISA constraints
#  Each platform may compile one or more ISA variants. When multiple
#  ISAs are listed, the Go init() in encvm_<os>_<arch>.go selects
#  the best one at runtime via golang.org/x/sys/cpu detection.
#  darwin: arm64 (neon)
#  linux:  arm64 (neon) or amd64 (sse42 avx2 avx512)
# ============================================================

get_available_isas() {
    local os=$1
    local arch=$2

    case "$os" in
        darwin)
            if [ "$arch" = "arm64" ]; then
                echo "neon"
            else
                echo ""
            fi
            ;;
        linux)
            case "$arch" in
                arm64) echo "neon" ;;
                amd64) echo "sse42 avx2 avx512" ;;
            esac
            ;;
        windows)
            if [ "$arch" = "amd64" ]; then
                echo "sse42 avx2 avx512"
            else
                echo ""
            fi
            ;;
        *)
            case "$arch" in
                arm64) echo "neon" ;;
                amd64) echo "sse42 avx2 avx512" ;;
            esac
            ;;
    esac
}


DEFAULT_ISAS=$(get_available_isas "$TARGET_OS" "$TARGET_ARCH")
ISAS="${ISAs:-$DEFAULT_ISAS}"

if [ -z "$ISAS" ]; then
    echo "Error: No valid ISA for $TARGET_OS/$TARGET_ARCH"
    exit 1
fi

# Modes (from sources.sh; default to single "default" mode if not set)
ALL_MODES="${MODES:-default}"

# ============================================================
#  LTO support
#
#  zig cc does not support -flto when targeting darwin (Mach-O)
#  because its LLD Mach-O backend lacks LTO. For darwin cross-builds
#  we skip LTO; the native build on a real Mac with clang still uses
#  full LTO.
# ============================================================

USE_LTO=true
if [ "$USE_ZIG" = true ] && [ "$TARGET_OS" = "darwin" ]; then
    USE_LTO=false
    echo "Note: LTO disabled (zig cc does not support -flto for darwin targets)"
fi

LTO_FLAG=""
if [ "$USE_LTO" = true ]; then
    LTO_FLAG="-flto"
fi

# ============================================================
#  ISA-specific compiler flags
# ============================================================

# -mevex512: required by LLVM ≤20 (including zig cc) to enable 512-bit EVEX
# encoding; without it, 512-bit AVX-512 intrinsics fail to compile.
# Clang 21+ (LLVM 21) deprecated this flag — -mavx512f alone is sufficient.
# Probe the compiler to decide.
_EVEX512_FLAG=""
if $CC -mevex512 -xc -c /dev/null -o /dev/null 2>&1 | grep -q 'deprecated'; then
    : # Clang 21+: flag deprecated, not needed
else
    _EVEX512_FLAG="-mevex512"
fi

get_isa_flags() {
    case "$1" in
        neon)   echo "" ;;
        sse42)  echo "-msse4.2 -mpclmul" ;;
        avx2)   echo "-mavx2 -msse4.2 -mpclmul" ;;
        avx512) echo "-mavx512f -mavx512bw -mpclmul $_EVEX512_FLAG" ;;
        *)      echo "" ;;
    esac
}

# Architecture-specific compiler flags
ARCH_FLAGS=""
if [ "$TARGET_ARCH" = "arm64" ]; then
    # -mno-outline: prevent compiler from outlining code sequences into
    # separate functions, which would create additional relocations.
    ARCH_FLAGS="-mno-outline"
fi

# ============================================================
#  Main build process
# ============================================================

echo "Building native files for: $TARGET_OS/$TARGET_ARCH (ISAs: $ISAS)"
echo "  Source: $SOURCE_FILE"
echo "  Output: $OUTPUT_DIR"
echo ""

ALL_OBJS=""

# ============================================================
#  Compile stdlib sources (minimal C runtime, ISA-independent)
#
#  These provide basic libc functions (memcpy, memset, etc.) that the
#  main code may call. We compile them with -fno-builtin-memcpy/memset
#  because they contain manual loops that the compiler could otherwise
#  optimize into memcpy/memset calls, causing infinite recursion.
# ============================================================

for stdlib_src in $STDLIB_SOURCES; do
    if [ -f "$stdlib_src" ]; then
        stdlib_base=$(basename "$stdlib_src" .c)
        stdlib_obj="${OUTPUT_DIR}/${stdlib_base}_${TARGET_OS}_${TARGET_ARCH}.o"
        echo "  Compiling $(basename "$stdlib_obj") (stdlib)"
        $CC -O3 -fPIC -g0 -fno-stack-protector -fno-builtin-memcpy -fno-builtin-memset $ARCH_FLAGS \
            -I"$(dirname "$stdlib_src")" -I"$REPO_ROOT/native/include" -I"$REPO_ROOT/native" \
            -c "$stdlib_src" -o "$stdlib_obj"
        ALL_OBJS="$ALL_OBJS $stdlib_obj"
    else
        echo "Warning: STDLIB_SOURCES file not found: $stdlib_src"
    fi
done

# ============================================================
#  Compile extra sources (compiled once per target with minimum ISA)
#
#  Each extra source is compiled once and linked with all ISA objects.
#  Uses the minimum (first) ISA's flags so that intrinsics like SSSE3's
#  _mm_shuffle_epi8 compile correctly.
# ============================================================

# Get minimum ISA flags (first ISA in the list is the baseline)
MIN_ISA=$(echo $ISAS | awk '{print $1}')
MIN_ISA_FLAGS=$(get_isa_flags "$MIN_ISA")

for extra_src in $EXTRA_SOURCES; do
    if [ -f "$extra_src" ]; then
        extra_base=$(basename "$extra_src" .c)
        extra_obj="${OUTPUT_DIR}/${extra_base}_${TARGET_OS}_${TARGET_ARCH}.o"
        echo "  Compiling $(basename "$extra_obj") (extra source,${USE_LTO:+ LTO,} min ISA: $MIN_ISA)"
        $CC -O3 $LTO_FLAG -fPIC -g0 -fno-stack-protector $ARCH_FLAGS $MIN_ISA_FLAGS \
            -I"$(dirname "$extra_src")" -I"$REPO_ROOT/native/include" -I"$REPO_ROOT/native" \
            -c "$extra_src" -o "$extra_obj"
        ALL_OBJS="$ALL_OBJS $extra_obj"
    else
        echo "Warning: EXTRA_SOURCES file not found: $extra_src"
    fi
done

# Collect flags for .clangd generation (from first ISA/mode combination)
CLANGD_FLAGS_COLLECTED=false
CLANGD_ADD_FLAGS=""

for isa in $ISAS; do
    ISA_FLAGS=$(get_isa_flags "$isa")

    for mode in $ALL_MODES; do
        # Determine mode suffix and compiler flag (from sources.sh MODE_FLAGS_<mode>)
        MODE_SUFFIX="_${mode}"
        MODE_FLAG_VAR="MODE_FLAGS_${mode}"
        MODE_FLAG="${!MODE_FLAG_VAR:-}"

        # File names
        OFILE="${OUTPUT_DIR}/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.o"

        # Use ISA_XXX macro instead of ISA=xxx to avoid preprocessor identifier comparison issues
        ISA_UPPER=$(printf '%s' "$isa" | tr '[:lower:]' '[:upper:]')
        ISA_MACRO="-DISA_${ISA_UPPER}"
        COMMON_DEFS="$ISA_MACRO $MODE_FLAG -DOS=${TARGET_OS} -DARCH=${TARGET_ARCH}"
        COMMON_INCLUDES="-I$(dirname "$SOURCE_FILE") -I$VJ_LIB_DIR -I$REPO_ROOT/native/include -I$REPO_ROOT/native"

        # Step 1: Compile to object (with LTO when supported, for cross-TU inlining)
        echo "  Compiling $(basename "$OFILE")"
        $CC -O3 $LTO_FLAG -fPIC -g0 -fno-stack-protector $ARCH_FLAGS $ISA_FLAGS \
            $COMMON_DEFS $COMMON_INCLUDES \
            -c "$SOURCE_FILE" -o "$OFILE"
        ALL_OBJS="$ALL_OBJS $OFILE"

        # Capture flags for .clangd from the first ISA/mode combination
        if [ "$CLANGD_FLAGS_COLLECTED" = false ]; then
            CLANGD_FLAGS_COLLECTED=true
            CLANGD_ADD_FLAGS="$ISA_MACRO $MODE_FLAG -DOS=${TARGET_OS} -DARCH=${TARGET_ARCH} -I$REPO_ROOT/native/include -I$REPO_ROOT/native"
        fi

        # Step 2: Generate assembly for debugging (optional)
        if [ "$GEN_ASM" = true ]; then
            mkdir -p "$OUTPUT_DIR/asm"
            SFILE="$OUTPUT_DIR/asm/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.s"
            echo "  Generating $(basename "$SFILE")"
            $CC -S -O3 -g0 -fno-stack-protector $ARCH_FLAGS -fno-asynchronous-unwind-tables $ISA_FLAGS \
                $COMMON_DEFS $COMMON_INCLUDES \
                "$SOURCE_FILE" -o "$SFILE"

            # Remove debug directives (use -i.bak for BSD/GNU sed compatibility)
            sed -i.bak '/^[[:space:]]*\.file[[:space:]]/d' "$SFILE" && rm -f "${SFILE}.bak"
            sed -i.bak '/^[[:space:]]*\.loc[[:space:]]/d' "$SFILE" && rm -f "${SFILE}.bak"
            sed -i.bak '/^[[:space:]]*\.cfi_[[:alpha:]]/d' "$SFILE" && rm -f "${SFILE}.bak"
            sed -i.bak '/^[[:space:]]*#DEBUG_VALUE/d' "$SFILE" && rm -f "${SFILE}.bak"
            sed -i.bak '/^[[:space:]]*\.Lfunc_begin/d' "$SFILE" && rm -f "${SFILE}.bak"
            sed -i.bak '/^[[:space:]]*\.Lfunc_end/d' "$SFILE" && rm -f "${SFILE}.bak"
            sed -i.bak '/^[[:space:]]*\.Ltmp/d' "$SFILE" && rm -f "${SFILE}.bak"
            # Remove .size directives that reference removed labels
            sed -i.bak '/\.size.*\.Lfunc_end/d' "$SFILE" && rm -f "${SFILE}.bak"

            TMP_ASM=$(mktemp)
            cat > "$TMP_ASM" << HEADER
// ============================================================
//
//  Platform: $TARGET_OS/$TARGET_ARCH ($isa)
//
// ============================================================

HEADER
            cat "$SFILE" >> "$TMP_ASM"
            mv "$TMP_ASM" "$SFILE"
        fi

    done
done

# ============================================================
#  Link into single .syso
#
#  Strategy based on target platform:
#  - darwin, linux/amd64: prelink (LTO link → extract .text → zero-relocation object)
#  - linux/arm64, windows: ld -r (simple relocatable link)
# ============================================================

SYSO_NAME="${BASENAME}_${TARGET_OS}_${TARGET_ARCH}.syso"
SYSO_PATH="$TARGET_DIR/$SYSO_NAME"

echo ""
echo "Linking $SYSO_NAME..."
echo "Objects to link:"
for obj in $ALL_OBJS; do
    echo "  $obj"
done

# Check if target platform needs prelink (resolved relocations, zero-reloc output)
needs_prelink() {
    if [ "$TARGET_OS" = "darwin" ]; then return 0; fi
    if [ "$TARGET_OS" = "linux" ] && [ "$TARGET_ARCH" = "amd64" ]; then return 0; fi
    return 1
}

if needs_prelink; then
    ZIG_TARGET=$(get_zig_target "$TARGET_OS" "$TARGET_ARCH")
    PRELINK_FLAGS="-o $SYSO_PATH -t $ZIG_TARGET"
    if [ "$USE_LTO" = true ]; then
        PRELINK_FLAGS="-l $PRELINK_FLAGS"
    fi

    # Darwin export symbol list (from EXPORT_SYMBOL_PREFIX in sources.sh)
    if [ "$TARGET_OS" = "darwin" ] && [ -n "${EXPORT_SYMBOL_PREFIX:-}" ]; then
        EXPORT_LIST="$OUTPUT_DIR/_exports.txt"
        > "$EXPORT_LIST"
        for isa in $ISAS; do
            for mode in $ALL_MODES; do
                echo "_${EXPORT_SYMBOL_PREFIX}_${mode}_${isa}" >> "$EXPORT_LIST"
            done
        done
        PRELINK_FLAGS="$PRELINK_FLAGS -e $EXPORT_LIST"
    fi

    "$REPO_ROOT/scripts/prelink.sh" $PRELINK_FLAGS $ALL_OBJS
else
    # Simple relocatable link (linux/arm64, windows, etc.)
    if [ "$USE_ZIG" = true ]; then
        echo "Create syso without pre-linking..."
        zig ld.lld -r $ALL_OBJS -o "$SYSO_PATH"
    else
        echo "Create syso without pre-linking..."
        ld -r -o "$SYSO_PATH" $ALL_OBJS
    fi
fi

# ============================================================
#  Generate .clangd for IDE support (clangd, ccls, etc.)
#
#  Unlike compile_commands.json (which only covers .c files),
#  .clangd applies CompileFlags to ALL files in the directory
#  including headers — so macros like ISA_neon are resolved
#  when editing .h files directly.
# ============================================================

CLANGD_PATH="$VJ_LIB_DIR/.clangd"
CLANG_TARGET=$(get_clang_target "$TARGET_OS" "$TARGET_ARCH")
{
    echo "# Auto-generated by gen-natives.sh — do not edit manually."
    echo "# Target: ${TARGET_OS}/${TARGET_ARCH} ($(echo $ISAS | tr ' ' ','))"
    echo "CompileFlags:"
    echo "  Add:"
    echo "    - --target=$CLANG_TARGET"
    for flag in $CLANGD_ADD_FLAGS; do
        echo "    - $flag"
    done
} > "$CLANGD_PATH"
echo "Generated $CLANGD_PATH"

# ============================================================
#  Summary
# ============================================================

echo ""
echo "Generated files:"
echo "  $SYSO_PATH"
echo ""
echo "Done!"
