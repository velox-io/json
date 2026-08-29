#!/usr/bin/env bash
#
# prelink.sh - Prelink object files into a single relocatable object with resolved relocations
#
# This script produces a relocatable object (.o/.syso) with all relocations resolved.
# The output can be linked by any linker (Go, ld, lld, etc.) without further relocation processing.
#
# For native and cross builds, clang (LLVM) is used exclusively.
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
#   -r <map>      Rename symbol map: old=new. Repeat for multiple renames. Forwarded
#                 to prelink-obj's -rename flag.
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
#   The output has zero relocations; it can be used as input to any downstream
#   linker without needing to resolve any relocations.

set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Default values
SOURCE=""
OUTPUT=""
TARGET=""
ISA=""
EXPORT_LIST=""
RENAME_ARGS=()
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
while getopts "s:o:t:i:e:r:lqh" opt; do
    case $opt in
        s) SOURCE="$OPTARG" ;;
        o) OUTPUT="$OPTARG" ;;
        t) TARGET="$OPTARG" ;;
        i) ISA="$OPTARG" ;;
        e) EXPORT_LIST="$OPTARG" ;;
        r) RENAME_ARGS+=(-rename "$OPTARG") ;;
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

# Select compiler: clang for both native and cross (LLVM is the only toolchain).
select_compiler() {
    if command -v clang &> /dev/null; then
        echo "clang"
        return 0
    fi
    echo ""
    return 1
}

# Check for available compilers
COMPILER=""
if [ "${USE_LLD_LINK:-0}" = "1" ]; then
    # lld-link pipeline: no compiler needed for linking stage
    # (Windows only; compilation was already done in gen-natives.sh)
    COMPILER="lld-link"
elif [ "${USE_LLVM:-0}" = "1" ]; then
    # LLVM pipeline: use the full CC command exported by gen-natives.sh
    # (includes --target, -ffreestanding, --sysroot etc.)
    if [ -n "${CC:-}" ]; then
        COMPILER="clang"
        # Use exported CC directly; it already has all target-specific flags
        _EXPORTED_CC="$CC"
    else
        COMPILER="clang"
    fi
fi

if [ -z "$COMPILER" ]; then
    COMPILER=$(select_compiler)
    if [ -z "$COMPILER" ]; then
        echo "Error: clang not found. Install LLVM clang >= 22." >&2
        exit 1
    fi
fi

if [ -n "$COMPILER" ]; then
    case "$COMPILER" in
        lld-link) log "Using linker: lld-link (Windows COFF pipeline)" ;;
        *)        log "Using compiler: $COMPILER (target: $TARGET, native: $(is_native_target "$TARGET" && echo 'yes' || echo 'no'))" ;;
    esac
fi

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

# Build compiler command prefix
compiler_cmd() {
    case "$COMPILER" in
        lld-link)
            # No compiler needed for Windows lld-link pipeline
            echo ""
            ;;
        clang)
            # If gen-natives.sh exported a full CC command (with --target, --sysroot, etc.), use it
            if [ -n "${_EXPORTED_CC:-}" ]; then
                echo "$_EXPORTED_CC"
            else
                local cmd="clang"
                # Add -isysroot on macOS so non-Apple clang can find system headers
                if [ "$(uname -s)" = "Darwin" ]; then
                    cmd="$cmd -isysroot /Library/Developer/CommandLineTools/SDKs/MacOSX.sdk"
                fi
                echo "$cmd"
            fi
            ;;
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

# Derive export prefix from EXPORT_LIST (shared by ELF and Windows).
# If the caller has set EXPORT_PREFIX env var, honor that verbatim.
get_export_prefix() {
    if [ -n "${EXPORT_PREFIX:-}" ]; then
        printf '%s' "$EXPORT_PREFIX"
        return
    fi
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

    local flags=()
    [ -n "$prefix" ] && flags+=(-export-prefix "$prefix")
    [ "$QUIET" = true ] && flags+=(-q)
    if [ ${#RENAME_ARGS[@]} -gt 0 ]; then
        flags+=("${RENAME_ARGS[@]}")
    fi

    "$PRELINK_OBJ" "${flags[@]}" -o "$output" "$input"
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
    # The ALIGN(64) ensures SIMD constant tables are properly aligned.
    #
    # PRELINK_KEEP_DEBUG=1 (set by gen-natives.sh PROFILE=1): keep .debug_* and
    # .eh_frame in the merged .so so it can serve as a base-0 debug companion
    # for perf/addr2line. These sections are non-ALLOC, so they never enter the
    # PT_LOAD and do not perturb .text layout, so the extracted .syso is
    # unchanged. Otherwise (production) they are discarded as before.
    local discard_debug=$'    *(.debug*)\n    *(.eh_frame*)'
    if [ "${PRELINK_KEEP_DEBUG:-0}" = "1" ]; then
        discard_debug=""
    fi
    cat > "$TMPDIR/merge.ld" << LDEOF
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
$discard_debug
  }
}
LDEOF

    # -Bsymbolic-functions: bind function references to local definitions,
    #   preventing PLT indirection for internal calls. Without this, the linker
    #   creates PLT stubs for exported functions called within the same .so,
    #   which land outside .text and are lost during prelink-obj extraction.
    local symbolic_flag="-Wl,-Bsymbolic-functions"

    # LLD links LLVM bitcode natively; GNU ld.bfd needs LLVMgold.so for LTO,
    # which the LLVM prebuilt tarball doesn't ship.
    local lld_flag=""
    if [ "${USE_LLVM:-0}" = "1" ] && [ "$TARGET_OS" = "linux" ]; then
        lld_flag="-fuse-ld=lld"
    fi

    # With LTO the input objects are LLVM bitcode; DWARF is only materialized at
    # link time, so -g must be on the link command (not just compile) for the
    # merged .so to carry .debug_*.
    local debug_flag=""
    if [ "${PRELINK_KEEP_DEBUG:-0}" = "1" ]; then
        debug_flag="-g"
    fi

    # Promote link-time warnings to errors so LTO backend stack-size violations
    # (see STACK_WARN_SIZE in gen-natives.sh) fail the build instead of scrolling
    # by. Empty when the guard is disabled or the link is not driven by lld.
    local strict_flag=""
    if [ "${STACK_WARN_SIZE:-0}" != "0" ]; then
        strict_flag="-Wl,--fatal-warnings"
    fi

    log "  Linking..."
    $CC_CMD $lld_flag -shared $lto_flag $debug_flag -nostdlib $symbolic_flag $strict_flag -Wl,--build-id=none -Wl,-T,"$TMPDIR/merge.ld" $objs -o "$merged_so"

    log "  Creating object file..."
    run_prelink_obj "$output" "$merged_so" "$(get_export_prefix)"

    # Preserve the intermediate .so as a base-0 debug companion for profiling.
    # $merged_so lives under $WORKDIR (build/prelink) and normally survives, but
    # make it explicit and report its path so users can point addr2line at it.
    if [ "${PRELINK_KEEP_DEBUG:-0}" = "1" ]; then
        log "  Debug companion (base-0 DWARF) kept: $merged_so"
    fi
}

# Darwin (Mach-O): -dynamiclib → prelink-obj
prelink_darwin() {
    local output="$1"
    shift
    local objs="$@"
    local dylib_tmp="$WORKDIR/${BASENAME_NOEXT}.dylib"
    local lto_flag=""
    [ "$LTO" = true ] && lto_flag="-flto"

    # Use LLD instead of Apple's ld64: it has no "must link with
    # libSystem" restriction and supports -nostdlib for dylibs.
    # This avoids needing any real system SDK or stub libraries.
    local lld_flags="-fuse-ld=lld -nostdlib"

    local export_flag=""
    if [ -n "$EXPORT_LIST" ] && [ -f "$EXPORT_LIST" ]; then
        export_flag="-Wl,-exported_symbols_list,$EXPORT_LIST"
    fi

    log "  Linking dylib..."
    log "    $dylib_tmp"
    $CC_CMD $lld_flags -O3 $lto_flag -dynamiclib $export_flag \
        $objs -o "$dylib_tmp"

    log "  Converting dylib to object..."
    run_prelink_obj "$output" "$dylib_tmp" ""
}

# Windows (PE/COFF): lld-link /DLL /NOENTRY /MERGE → prelink-obj coff
# No DllMain stub needed. Sections merged at link time.
prelink_windows_lld() {
    local output="$1"
    shift
    local objs="$@"
    local dll_tmp="$WORKDIR/${BASENAME_NOEXT}.dll"

    # Build prelink-obj if not already built
    if [ ! -x "$PRELINK_OBJ" ]; then
        log "  Building prelink-obj..."
        mkdir -p "$(dirname "$PRELINK_OBJ")"
        (cd "$REPO_ROOT/scripts/cmd/prelink-obj" && go build -o "$PRELINK_OBJ" .)
    fi

    # /WX promotes lld-link warnings (including LTO backend stack-size violations
    # from -Xclang -fwarn-stack-size) to errors; see STACK_WARN_SIZE.
    local strict_flag=""
    if [ "${STACK_WARN_SIZE:-0}" != "0" ]; then
        strict_flag="/WX"
    fi

    # /MAP produces a linker map file alongside the DLL. lld-link strips the
    # COFF symbol table for DLLs (only the export table survives), so the
    # stackdepth tool reads this map file to recover internal function symbols
    # for call-graph analysis.
    local map_file="$WORKDIR/${BASENAME_NOEXT}.map"

    # Under the MSYS2 runtime, arguments beginning with "/" are path-converted
    # (lld-link's /DLL would become C:/msys64/DLL). Convert the path arguments
    # to Windows form explicitly and disable automatic conversion for this
    # invocation only. Git bash reports MINGW64_NT-*, so match it too.
    local link_env=()
    local map_arg="$map_file" out_arg="$dll_tmp"
    local obj_args=($objs)
    case "$(uname -s)" in
    MSYS* | MINGW* | CYGWIN*)
        if command -v cygpath >/dev/null 2>&1; then
            map_arg="$(cygpath -w "$map_file")"
            out_arg="$(cygpath -w "$dll_tmp")"
            obj_args=()
            local o
            for o in $objs; do
                obj_args+=("$(cygpath -w "$o")")
            done
            link_env=(MSYS2_ARG_CONV_EXCL="*")
        fi
        ;;
    esac

    log "  Linking DLL (lld-link /MERGE)..."
    log "    $dll_tmp"
    env ${link_env[@]+"${link_env[@]}"} lld-link /DLL /NOENTRY /NODEFAULTLIB $strict_flag \
        /MERGE:.rdata=.text /MERGE:.pdata=.text \
        /MAP:"$map_arg" \
        /OUT:"$out_arg" \
        "${obj_args[@]}"

    log "  Converting to COFF object..."
    local coff_args=("$PRELINK_OBJ" coff)
    local prefix
    prefix="$(get_export_prefix)"
    if [ -n "$prefix" ]; then
        coff_args+=(-e "$prefix")
    fi
    if [ ${#RENAME_ARGS[@]} -gt 0 ]; then
        coff_args+=("${RENAME_ARGS[@]}")
    fi
    coff_args+=("$dll_tmp" "$output")
    "${coff_args[@]}"
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
    darwin)  prelink_darwin       "$OUTPUT" $ALL_OBJS ;;
    windows) prelink_windows_lld "$OUTPUT" $ALL_OBJS ;;
    linux)   prelink_elf         "$OUTPUT" $ALL_OBJS ;;
    *)       echo "Error: unsupported target OS: $TARGET_OS"; exit 1 ;;
esac

log "  Done: $OUTPUT ($(wc -c < "$OUTPUT" | tr -d ' ') bytes)"
