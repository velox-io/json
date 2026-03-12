#!/usr/bin/env bash
#
# gen-natives.sh - Expand macros, compile, and generate Go integration files
#
# Usage:
#   ./gen-natives.sh [--zig] [target_os] [target_arch]
#
# Options:
#   --zig         - Force using zig cc even for native (non-cross) builds.
#
# Arguments:
#   target_os   - Target OS (linux, darwin, windows). Default: host OS.
#   target_arch - Target architecture (arm64, amd64). Default: host arch.
#
# Environment variables (required):
#   SOURCE_FILE   - Source C file.
#   TARGET_DIR    - Target directory for .syso/.s files.
#   OUTPUT_DIR    - Output directory for .c/.o files. Default: build/native
#
# Environment variables (optional):
#   STDLIB_SOURCES - Space-separated list of stdlib .c files (e.g. memcpy/memset
#                    implementations). Compiled with -fno-builtin-memcpy/memset.
#   EXTRA_SOURCES  - Space-separated list of additional .c files to compile
#                    and link.
#
# Output:
#   - {OUTPUT_DIR}/{basename}[_{mode}]_{os}_{arch}_{isa}.c
#   - {OUTPUT_DIR}/{basename}[_{mode}]_{os}_{arch}_{isa}.o
#   - {TARGET_DIR}/{basename}_{os}_{arch}.syso  (all ISAs, all modes combined)
#   - {TARGET_DIR}/asm/{basename}[_{mode}]_{os}_{arch}_{isa}.s
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
while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --zig) FORCE_ZIG=true; shift ;;
        *)     echo "Error: Unknown option: $1"; exit 1 ;;
    esac
done

# ============================================================
#  Configuration (required)
# ============================================================

if [ -z "$SOURCE_FILE" ]; then
    echo "Error: SOURCE_FILE is required"
    exit 1
fi

if [ -z "$TARGET_DIR" ]; then
    echo "Error: TARGET_DIR is required"
    exit 1
fi

OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/build/native}"

VJ_LIB_DIR="native/encvm/impl"

# Derive base name from source file
BASENAME=$(basename "$SOURCE_FILE" .c)

mkdir -p "$OUTPUT_DIR" "$TARGET_DIR/asm"

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

# Modes
ALL_MODES="${MODES:-default fast}"

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
#  Compile extra sources (ISA-independent, compiled once per target)
#
#  Each extra source is compiled once and linked with all ISA objects.
# ============================================================

for extra_src in $EXTRA_SOURCES; do
    if [ -f "$extra_src" ]; then
        extra_base=$(basename "$extra_src" .c)
        extra_obj="${OUTPUT_DIR}/${extra_base}_${TARGET_OS}_${TARGET_ARCH}.o"
        echo "  Compiling $(basename "$extra_obj") (extra source)"
        $CC -O3 -fPIC -g0 -fno-stack-protector $ARCH_FLAGS \
            -I"$(dirname "$extra_src")" -I"$REPO_ROOT/native/include" \
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
        # Determine mode suffix and compiler flag
        MODE_SUFFIX="_${mode}"
        MODE_FLAG=""
        if [ "$mode" = "default" ]; then
            MODE_FLAG="-DMODE_default"
        elif [ "$mode" = "fast" ]; then
            MODE_FLAG="-DMODE_fast"
        fi
        # full: no MODE_FLAG needed, SJ_MODE defaults to SJ_MODE_FULL

        # File names
        CFILE="${OUTPUT_DIR}/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.c"
        OFILE="${OUTPUT_DIR}/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.o"
        SFILE="$TARGET_DIR/asm/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.s"

        # Step 1: Expand macros (use target compiler for correct headers)
        echo "  Expanding $(basename "$CFILE")"
        # 使用 ISA_xxx 宏而非 ISA=xxx 避免预处理标识符比较问题
        ISA_MACRO="-DISA_${isa}"
        $CC -E -P \
            -DOS=${TARGET_OS} \
            -DARCH=${TARGET_ARCH} \
            $ISA_MACRO \
            $MODE_FLAG \
            -I"$(dirname "$SOURCE_FILE")" \
            -I"$REPO_ROOT/$VJ_LIB_DIR" \
            -I"$REPO_ROOT/native/include" \
            -I"$REPO_ROOT/native" \
            "$SOURCE_FILE" \
            -o "$CFILE"

        # Step 2: Compile to object
        echo "  Compiling $(basename "$OFILE")"
        $CC -O3 -fPIC -g0 -fno-stack-protector $ARCH_FLAGS $ISA_FLAGS \
            -I"$REPO_ROOT/native" -c "$CFILE" -o "$OFILE"
        ALL_OBJS="$ALL_OBJS $OFILE"

        # Capture flags for .clangd from the first ISA/mode combination
        if [ "$CLANGD_FLAGS_COLLECTED" = false ]; then
            CLANGD_FLAGS_COLLECTED=true
            CLANGD_ADD_FLAGS="-DOS=${TARGET_OS} -DARCH=${TARGET_ARCH} $MODE_FLAG -I$REPO_ROOT/native/include"
        fi

        # Step 3: Generate assembly for reference (strip debug info)
        echo "  Generating $(basename "$SFILE")"
        $CC -S -O3 -g0 -fno-stack-protector $ARCH_FLAGS -fno-asynchronous-unwind-tables $ISA_FLAGS \
            -I"$REPO_ROOT/native" "$CFILE" -o "$SFILE"

        # Remove debug directives
        sed -i '/^[[:space:]]*\.file[[:space:]]/d' "$SFILE"
        sed -i '/^[[:space:]]*\.loc[[:space:]]/d' "$SFILE"
        sed -i '/^[[:space:]]*\.cfi_[[:alpha:]]/d' "$SFILE"
        sed -i '/^[[:space:]]*#DEBUG_VALUE/d' "$SFILE"
        sed -i '/^[[:space:]]*\.Lfunc_begin/d' "$SFILE"
        sed -i '/^[[:space:]]*\.Lfunc_end/d' "$SFILE"
        sed -i '/^[[:space:]]*\.Ltmp/d' "$SFILE"
        # Remove .size directives that reference removed labels
        sed -i '/\.size.*\.Lfunc_end/d' "$SFILE"

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
    done
done

# ============================================================
#  Link into single .syso
#
#  Strategy based on target platform:
#  - Linux amd64: use prelink.sh (Go internal linker cannot handle R_X86_64_PC32)
#  - Linux arm64: ld -r (NEON has no cross-section relocations)
#  - Darwin arm64: dylib prelink (zero-relocation syso via dylib_to_obj.py)
#  - Windows: ld -r
# ============================================================

SYSO_NAME="${BASENAME}_${TARGET_OS}_${TARGET_ARCH}.syso"
SYSO_PATH="$TARGET_DIR/$SYSO_NAME"

echo ""
echo "Linking $SYSO_NAME..."
echo "Objects to link:"
for obj in $ALL_OBJS; do
    echo "  $obj"
done

# Check if target platform needs prelink (ET_DYN → ET_REL conversion)
NEEDS_PRELINK=false
if [ "$TARGET_OS" = "linux" ] && [ "$TARGET_ARCH" = "amd64" ]; then
    NEEDS_PRELINK=true

    # Linux amd64: use prelink.sh (Go internal linker cannot handle R_X86_64_PC32)
    ZIG_TARGET=$(get_zig_target "$TARGET_OS" "$TARGET_ARCH")
    "$REPO_ROOT/scripts/prelink.sh" -l -o "$SYSO_PATH" -t "$ZIG_TARGET" $ALL_OBJS
fi

if [ "$NEEDS_PRELINK" != true ]; then
    echo "Create syso without pre-linking..."
    if [ "$USE_ZIG" = true ]; then
        # Use zig's bundled lld for relocatable link.
        # "zig cc -r" on older zig versions produces ET_EXEC instead of ET_REL,
        # so we invoke ld.lld directly which correctly produces ET_REL.
        zig ld.lld -r $ALL_OBJS -o "$SYSO_PATH"
    elif [ "$TARGET_OS" = "darwin" ]; then
        # Native darwin: use system ld (Apple ld64) explicitly
        /usr/bin/ld -r -arch "$TARGET_ARCH" -o "$SYSO_PATH" $ALL_OBJS
    else
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

CLANGD_PATH="$REPO_ROOT/$VJ_LIB_DIR/.clangd"
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
for isa in $ISAS; do
    for mode in $ALL_MODES; do
        MODE_SUFFIX="_${mode}"
        echo "  $TARGET_DIR/asm/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.s"
    done
done
echo ""
echo "Done!"
