#!/usr/bin/env bash
#
# gen-natives.sh - Compile C sources, generate Go integration files
#
# Usage:
#   ./gen-natives.sh [--zig] [--asm] [--pgo-use] [--no-prelink] <sources.sh> [target_os] [target_arch]
#
# Options:
#   --zig           - Force using zig cc even for native (non-cross) builds.
#   --asm           - Generate assembly files for debugging.
#   --pgo-use       - Enable AutoFDO profile-guided optimization (-fprofile-sample-use).
#                     Uses profile data from local/pgo-data/merged.profdata
#   --no-prelink    - Disable prelink path and force relocatable link (ld -r / zig ld.lld -r).
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
#   DEBUG_SYMBOLS - If true/1, keep richer syso symbols for debugging and compile with -g3.
#   NO_PRELINK    - If true/1, disable prelink path (same as --no-prelink).
#
# Output:
#   - {OUTPUT_DIR}/{basename}[_{mode}]_{os}_{arch}_{isa}.o
#   - {TARGET_DIR}/{basename}_{mode}_{isa}_{os}_{arch}.syso  (one per mode×ISA)
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
PGO_USE=false
DISABLE_PRELINK=false
while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --zig) FORCE_ZIG=true; shift ;;
        --asm) GEN_ASM=true; shift ;;
        --pgo-use) PGO_USE=true; shift ;;
        --no-prelink) DISABLE_PRELINK=true; shift ;;
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
    echo "Usage: gen-natives.sh [--zig] [--asm] [--pgo-use] [--no-prelink] <sources.sh> [target_os] [target_arch]"
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

is_true() {
    case "${1:-}" in
        1|true|TRUE|yes|YES|on|ON) return 0 ;;
        *) return 1 ;;
    esac
}

if is_true "${NO_PRELINK:-}"; then
    DISABLE_PRELINK=true
fi

DEBUG_SYMBOLS_ENABLED=false
C_DEBUG_FLAGS="-g0"
if is_true "${DEBUG_SYMBOLS:-}"; then
    DEBUG_SYMBOLS_ENABLED=true
    C_DEBUG_FLAGS="-g3 -fno-omit-frame-pointer"
    echo "Debug: DEBUG_SYMBOLS enabled (keep richer syso symbols)"
fi

# ============================================================
#  AutoFDO (Profile-Guided Optimization) Configuration
# ============================================================

PGO_DATA_DIR="$REPO_ROOT/local/pgo-data"
PGO_CFLAGS=""

if [ "$PGO_USE" = true ]; then
    PROFDATA="$PGO_DATA_DIR/merged.profdata"
    if [ ! -f "$PROFDATA" ]; then
        echo "Error: PGO profile not found: $PROFDATA"
        echo "  Run AutoFDO collection first to generate the profile data."
        exit 1
    fi
    # -fprofile-sample-use: AutoFDO sample-based optimization (no instrumentation needed)
    PGO_CFLAGS="-fprofile-sample-use=$PROFDATA"
    echo "PGO: AutoFDO optimization enabled (profile: $PROFDATA)"
fi

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

# ============================================================
#  Unified Toolchain Selection
#
#  TOOLCHAIN (env): auto | llvm | zig
#    auto  = prefer LLVM when tools are available, fallback to zig
#    llvm = force clang/lld-link for all platforms
#    zig  = force zig cc (bundles its own sysroot)
#
#  LINUX_SYSROOT (env): path to musl sysroot headers for LLVM Linux targets
#     Default: /opt/musl/x86_64/current if it exists
#
#  Output variables:
#    RESOLVED_TOOLCHAIN - resolved value: "lld" or "zig"
#    USE_LLVM           - true if using LLVM toolchain
#    USE_ZIG            - true if using zig toolchain
#    USE_LLD_LINK        - true if Windows lld-link pipeline is active
#    CC                 - compiler command with all flags
# ============================================================

resolve_toolchain() {
    local requested="${TOOLCHAIN:-auto}"
    case "$requested" in
        auto)
            # Auto-detect: prefer llvm when the target's requirements are met
            if [ "$TARGET_OS" = "windows" ] && command -v lld-link &>/dev/null; then
                echo "lld"
            elif [ "$TARGET_OS" = "linux" ] && [ -n "${LINUX_SYSROOT:-}" ]; then
                echo "lld"
            elif [ "$NEEDS_CROSS_COMPILE" != true ] && command -v clang &>/dev/null; then
                echo "lld"
            elif command -v zig &>/dev/null; then
                echo "zig"
            else
                echo "Error: no suitable toolchain found (need clang or zig)"
                exit 1
            fi
            ;;
        llvm) echo "lld" ;;
        zig)  echo "zig" ;;
        *)
            echo "Error: unknown TOOLCHAIN=$requested (expected: auto, llvm, zig)"
            exit 1
            ;;
    esac
}

RESOLVED_TOOLCHAIN=$(resolve_toolchain)
USE_LLVM=0
USE_ZIG=0
USE_LLD_LINK=0

if [ "$RESOLVED_TOOLCHAIN" = "lld" ]; then
    USE_LLVM=1

    case "$TARGET_OS" in
        windows)
            # Windows: clang compiles .c → .obj, lld-link links → DLL (no DllMain needed)
            USE_LLD_LINK=1
            CC="clang --target=x86_64-pc-windows-msvc -ffreestanding"
            echo "Using llvm pipeline for windows (clang + lld-link)"
            ;;
        linux)
            # Linux: clang with musl target triple + optional sysroot
            linux_triple=""
            case "$TARGET_ARCH" in
                amd64|x86_64)   linux_triple="x86_64-linux-musl" ;;
                arm64|aarch64)  linux_triple="aarch64-linux-musl" ;;
                *)               linux_triple="${TARGET_ARCH}-linux-musl" ;;
            esac
            CC="clang --target=$linux_triple -ffreestanding"
            if [ -n "${LINUX_SYSROOT:-}" ]; then
                CC="$CC --sysroot=$LINUX_SYSROOT"
                sysroot_note=" (sysroot: $LINUX_SYSROOT)"
            fi
            echo "Using llvm pipeline for linux ($linux_triple$sysroot_note)"
            ;;
        darwin)
            # Darwin: clang with Apple target triple + macOS SDK
            darwin_target=""
            case "$TARGET_ARCH" in
                amd64|x86_64)   darwin_target="x86_64-apple-darwin" ;;
                arm64|aarch64)  darwin_target="arm64-apple-darwin" ;;
                *)               darwin_target="${TARGET_ARCH}-apple-darwin" ;;
            esac
            CC="clang --target=$darwin_target"
            if [ "$(uname -s)" = "Darwin" ]; then
                CC="$CC -isysroot /Library/Developer/CommandLineTools/SDKs/MacOSX.sdk"
            fi
            echo "Using llvm pipeline for darwin ($darwin_target)"
            ;;
    esac

elif [ "$RESOLVED_TOOLCHAIN" = "zig" ]; then
    USE_ZIG=1

    if [ "$FORCE_ZIG" = true ] || [ "$NEEDS_CROSS_COMPILE" = true ]; then
        if command -v zig &> /dev/null; then
            ZIG_TARGET=$(get_zig_target "$TARGET_OS" "$TARGET_ARCH")
            CC="zig cc -target $ZIG_TARGET"
            if [ "$FORCE_ZIG" = true ]; then
                echo "Using zig cc (forced, target: $ZIG_TARGET)"
            else
                echo "Cross-compiling with zig cc (target: $ZIG_TARGET)"
            fi
        else
            echo "Error: zig requested but zig is not installed."
            exit 1
        fi
    else
        # Native build with zig on non-cross target — unusual but supported
        ZIG_TARGET=$(get_zig_target "$TARGET_OS" "$TARGET_ARCH")
        CC="zig cc -target $ZIG_TARGET"
        echo "Using zig cc (native, target: $ZIG_TARGET)"
    fi
fi

export RESOLVED_TOOLCHAIN USE_LLVM USE_ZIG USE_LLD_LINK CC

# ============================================================
#  Platform-ISA constraints
#  Each platform may compile one or more ISA variants. When multiple
#  ISAs are listed, the Go init() in encvm_<os>_<arch>.go selects
#  the best one at runtime via golang.org/x/sys/cpu detection.
#  darwin: arm64 (neon)
#  linux:  arm64 (neon) or amd64 (avx2)
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
                amd64) echo "avx2" ;;
            esac
            ;;
        windows)
            if [ "$arch" = "amd64" ]; then
                echo "avx2"
            else
                echo ""
            fi
            ;;
        *)
            case "$arch" in
                arm64) echo "neon" ;;
                amd64) echo "avx2" ;;
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

# Modes (from sources.sh; default to single "fast" mode if not set)
ALL_MODES="${MODES:-fast}"

# ============================================================
#  LTO support
#
#  zig cc does not support -flto when targeting darwin (Mach-O)
#  because its LLD Mach-O backend lacks LTO. For darwin cross-builds
#  we skip LTO; the native build on a real Mac with clang still uses
#  full LTO.
# ============================================================

USE_LTO=true
if [ "$DISABLE_PRELINK" = true ]; then
    USE_LTO=false
    echo "Note: LTO disabled (prelink disabled; relocatable -r path requires native object format)"
elif [ "$USE_ZIG" = "1" ] && [ "$TARGET_OS" = "darwin" ]; then
    USE_LTO=false
    echo "Note: LTO disabled (zig cc does not support -flto for darwin targets)"
elif [ "$USE_LLVM" = "1" ] && [ "$TARGET_OS" = "darwin" ]; then
    USE_LTO=false
    echo "Note: LTO disabled (lld Mach-O backend does not reliably support -flto)"
elif [ "$TARGET_OS" = "linux" ] && [ "$TARGET_ARCH" = "arm64" ]; then
    USE_LTO=false
    echo "Note: LTO disabled for linux/arm64 (prelink uses native object format, not LLVM IR bitcode)"
elif [ "$TARGET_OS" = "windows" ]; then
    USE_LTO=false
    echo "Note: LTO disabled for windows (lld-link /DLL does not support -flto for COFF/PE targets)"
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
_evex_probe_dir=$(mktemp -d)
: > "$_evex_probe_dir/probe.c"
if $CC -mevex512 -xc -c "$_evex_probe_dir/probe.c" -o "$_evex_probe_dir/probe.o" 2>&1 | grep -q 'deprecated'; then
    : # Clang 21+: flag deprecated, not needed
else
    _EVEX512_FLAG="-mevex512"
fi
rm -rf "$_evex_probe_dir"

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
    if [ "$TARGET_OS" = "linux" ]; then
        # Go's linux/arm64 runtime uses X28 as the current goroutine pointer (g).
        # C code must not clobber it, otherwise crashes on return to Go.
        ARCH_FLAGS="$ARCH_FLAGS -ffixed-x28"
    fi
fi

# PIC flag: only for ELF/Mach-O targets (Windows MSVC does not support -fPIC)
PIC_FLAG="-fPIC"
if [ "$TARGET_OS" = "windows" ]; then
    PIC_FLAG=""
fi

# ============================================================
#  Main build process
# ============================================================

echo "Building native files for: $TARGET_OS/$TARGET_ARCH (ISAs: $ISAS)"
echo "  Source: $SOURCE_FILE"
echo "  Output: $OUTPUT_DIR"
echo ""

# Prevent LTO from replacing hand-written loops with libc calls.
# The .syso is linked with -nostdlib — any unresolved libc symbol
# becomes a call past the end of .text, causing SIGSEGV at runtime.
#
# Only list functions that LTO's loop idiom recognizer can synthesize:
#   - strlen/strnlen:  while(*p) p++
#   - memcmp/bcmp:     byte-by-byte compare loops
#   - memchr/strchr:   scan-for-byte loops
#   - bzero:           zero-fill loops (memset(p,0,n) variant)
#   - printf/fprintf/sprintf/snprintf: va_list formatting patterns
#
# Note: memcpy/memset are NOT listed here because the codebase uses
# __builtin_memcpy/__builtin_memset exclusively.  -fno-builtin-memcpy
# would prevent the compiler from inlining __builtin_memcpy(buf,"true",4)
# as a single mov instruction.  The stdlib sources (memory.c) have their
# own -fno-builtin-memcpy/memset flags to prevent recursive calls.
NO_BUILTIN_FLAGS="-fno-builtin-strlen -fno-builtin-strnlen"
#NO_BUILTIN_FLAGS="$NO_BUILTIN_FLAGS -fno-builtin-memcmp -fno-builtin-bcmp"
#NO_BUILTIN_FLAGS="$NO_BUILTIN_FLAGS -fno-builtin-memchr -fno-builtin-strchr"
#NO_BUILTIN_FLAGS="$NO_BUILTIN_FLAGS -fno-builtin-bzero"
NO_BUILTIN_FLAGS="$NO_BUILTIN_FLAGS -fno-builtin-printf -fno-builtin-fprintf"
NO_BUILTIN_FLAGS="$NO_BUILTIN_FLAGS -fno-builtin-sprintf -fno-builtin-snprintf"

# Suppress C2y extension warnings for MSVC target:
# Project uses __forceinline inline (non-static) functions with static const
# variables inside, which Clang reports as -Wstatic-in-inline for Windows targets.
if [ "$TARGET_OS" = "windows" ]; then
    NO_BUILTIN_FLAGS="$NO_BUILTIN_FLAGS -Wno-static-in-inline"
fi

STDLIB_OBJS=""
EXTRA_OBJS=""

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
        $CC -O3 $PIC_FLAG $C_DEBUG_FLAGS -fno-stack-protector -fno-builtin-memcpy -fno-builtin-memset $ARCH_FLAGS \
            -I"$(dirname "$stdlib_src")" -I"$REPO_ROOT/native/include" -I"$REPO_ROOT/native" \
            -c "$stdlib_src" -o "$stdlib_obj"
        STDLIB_OBJS="$STDLIB_OBJS $stdlib_obj"
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
        EXTRA_LTO_LABEL=""
        if [ "$USE_LTO" = true ]; then EXTRA_LTO_LABEL=" LTO,"; fi
        echo "  Compiling $(basename "$extra_obj") (extra source,${EXTRA_LTO_LABEL} min ISA: $MIN_ISA)"
        $CC -O3 $LTO_FLAG $PIC_FLAG $C_DEBUG_FLAGS -fno-stack-protector $NO_BUILTIN_FLAGS $ARCH_FLAGS $MIN_ISA_FLAGS \
            -I"$(dirname "$extra_src")" -I"$REPO_ROOT/native/include" -I"$REPO_ROOT/native" \
            ${EXTRA_CFLAGS:-} ${PGO_CFLAGS:-} \
            -c "$extra_src" -o "$extra_obj"
        EXTRA_OBJS="$EXTRA_OBJS $extra_obj"
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
        COMMON_DEFS="$ISA_MACRO $MODE_FLAG -DOS=${TARGET_OS} -DARCH=${TARGET_ARCH} ${EXTRA_CFLAGS:-}"
        COMMON_INCLUDES="-I$(dirname "$SOURCE_FILE") -I$VJ_LIB_DIR -I$REPO_ROOT/native/include -I$REPO_ROOT/native"

        # Step 1: Compile to object (with LTO when supported, for cross-TU inlining)
        echo "  Compiling $(basename "$OFILE")"
        $CC -O3 $LTO_FLAG $PIC_FLAG $C_DEBUG_FLAGS -fno-stack-protector $NO_BUILTIN_FLAGS $ARCH_FLAGS $ISA_FLAGS \
            $COMMON_DEFS $COMMON_INCLUDES ${PGO_CFLAGS:-} \
            -c "$SOURCE_FILE" -o "$OFILE"

        # Capture flags for .clangd from the first ISA/mode combination
        if [ "$CLANGD_FLAGS_COLLECTED" = false ]; then
            CLANGD_FLAGS_COLLECTED=true
            CLANGD_ADD_FLAGS="$ISA_MACRO $MODE_FLAG -DOS=${TARGET_OS} -DARCH=${TARGET_ARCH} -I$REPO_ROOT/native/include -I$REPO_ROOT/native"
        fi

        # Step 2: Generate assembly for debugging (optional)
        if [ "$GEN_ASM" = true ]; then
            mkdir -p "$OUTPUT_DIR/asm"
            SFILE="$OUTPUT_DIR/asm/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.s"
            echo "  Generating asm: "$SFILE""
            $CC -S -O3 $C_DEBUG_FLAGS -fno-stack-protector $NO_BUILTIN_FLAGS $ARCH_FLAGS -fno-asynchronous-unwind-tables $ISA_FLAGS \
                $COMMON_DEFS $COMMON_INCLUDES \
                "$SOURCE_FILE" -o "$SFILE"

            # # Remove debug directives (use -i.bak for BSD/GNU sed compatibility)
            # sed -i.bak '/^[[:space:]]*\.file[[:space:]]/d' "$SFILE" && rm -f "${SFILE}.bak"
            # sed -i.bak '/^[[:space:]]*\.loc[[:space:]]/d' "$SFILE" && rm -f "${SFILE}.bak"
            # sed -i.bak '/^[[:space:]]*\.cfi_[[:alpha:]]/d' "$SFILE" && rm -f "${SFILE}.bak"
            # sed -i.bak '/^[[:space:]]*#DEBUG_VALUE/d' "$SFILE" && rm -f "${SFILE}.bak"
            # sed -i.bak '/^[[:space:]]*\.Lfunc_begin/d' "$SFILE" && rm -f "${SFILE}.bak"
            # sed -i.bak '/^[[:space:]]*\.Lfunc_end/d' "$SFILE" && rm -f "${SFILE}.bak"
            # sed -i.bak '/^[[:space:]]*\.Ltmp/d' "$SFILE" && rm -f "${SFILE}.bak"
            # # Remove .size directives that reference removed labels
            # sed -i.bak '/\.size.*\.Lfunc_end/d' "$SFILE" && rm -f "${SFILE}.bak"

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
#  Link each mode×ISA into separate .syso
#
#  Strategy based on target platform (default):
#  - darwin, linux, windows: prelink (LTO link → extract .text → zero-relocation object)
#
#  Override:
#  - --no-prelink / NO_PRELINK=1: force ld -r / zig ld.lld -r for all platforms
#
#  Each (mode, isa) combination produces one syso.
#  - prelink path: each syso contains stdlib + extra + mode/isa main object
# ============================================================

echo ""

# Check if target platform needs prelink (resolved relocations, zero-reloc output)
needs_prelink() {
    if [ "$DISABLE_PRELINK" = true ]; then return 1; fi
    if [ "$TARGET_OS" = "darwin" ]; then return 0; fi
    if [ "$TARGET_OS" = "linux" ]; then return 0; fi
    if [ "$TARGET_OS" = "windows" ]; then return 0; fi
    return 1
}

if [ "$DISABLE_PRELINK" = true ]; then
    echo "Note: prelink disabled (--no-prelink or NO_PRELINK=1); using relocatable link path"
fi

ALL_SYSO_PATHS=""
COMMON_OBJS_LINKED=false

for isa in $ISAS; do
    for mode in $ALL_MODES; do
        MODE_SUFFIX="_${mode}"
        MAIN_OBJ="${OUTPUT_DIR}/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.o"

        SYSO_NAME="${BASENAME}_${mode}_${isa}_${TARGET_OS}_${TARGET_ARCH}.syso"
        SYSO_PATH="$TARGET_DIR/$SYSO_NAME"

        echo "Linking $SYSO_NAME..."

        if needs_prelink; then
            # Prelink path (darwin, linux, windows):
            # Each syso is fully linked from stdlib + extra + main, then
            # prelink-obj strips all relocations. Since every syso contains
            # the same stdlib/extra code, the export filter below demotes
            # internal symbols to local, keeping only vj_vm_exec_<mode>_<isa>
            # as global — otherwise Go's linker would see duplicate definitions.
            LINK_OBJS="$STDLIB_OBJS $EXTRA_OBJS $MAIN_OBJ"

            ZIG_TARGET=$(get_zig_target "$TARGET_OS" "$TARGET_ARCH")
            PRELINK_FLAGS="-o $SYSO_PATH -t $ZIG_TARGET -i $isa"
            if [ "$USE_LTO" = true ]; then
                PRELINK_FLAGS="-l $PRELINK_FLAGS"
            fi

            # Export symbol list: keep only vj_vm_exec_* as global.
            # Without this, internal helpers (e.g. us_write_float32) remain
            # global in every syso, causing duplicate symbol errors.
            if [ -n "${EXPORT_SYMBOL_PREFIX:-}" ]; then
                EXPORT_LIST="$OUTPUT_DIR/_exports_${mode}_${isa}.txt"
                if [ "$TARGET_OS" = "darwin" ]; then
                    # macOS ld: symbol names must be prefixed with '_'
                    echo "_${EXPORT_SYMBOL_PREFIX}_${mode}_${isa}" > "$EXPORT_LIST"
                else
                    # ELF/COFF: no leading underscore
                    echo "${EXPORT_SYMBOL_PREFIX}_${mode}_${isa}" > "$EXPORT_LIST"
                fi
                PRELINK_FLAGS="$PRELINK_FLAGS -e $EXPORT_LIST"
            fi

            "$REPO_ROOT/scripts/prelink.sh" $PRELINK_FLAGS $LINK_OBJS
        else
            # Relocatable link path:
            # - Include stdlib/extra objects only once to avoid duplicate symbol definitions across multiple mode×ISA .syso files.
            # - Link through compiler driver for better compatibility (e.g. LTO plugins).
            if [ "$COMMON_OBJS_LINKED" = false ]; then
                LINK_OBJS="$STDLIB_OBJS $EXTRA_OBJS $MAIN_OBJ"
                COMMON_OBJS_LINKED=true
            else
                LINK_OBJS="$MAIN_OBJ"
            fi
            $CC -r $LTO_FLAG $LINK_OBJS -o "$SYSO_PATH"
        fi

        ALL_SYSO_PATHS="$ALL_SYSO_PATHS $SYSO_PATH"
    done
done

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
for syso in $ALL_SYSO_PATHS; do
    echo "  $syso"
done
echo ""
echo "Done!"
