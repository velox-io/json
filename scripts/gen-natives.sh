#!/usr/bin/env bash
#
# gen-natives.sh - Compile C sources, generate Go integration files
#
# Usage:
#   ./gen-natives.sh [--asm] [--pgo-instr] [--pgo-instr-use] [--no-prelink] <sources.sh> [target_os] [target_arch]
#
# Options:
#   --asm           - Generate assembly files for debugging.
#   --pgo-instr     - Build instrumented objects (-fprofile-instr-generate) for
#                     instrumentation PGO. Implies --no-prelink (prf sections must
#                     survive). The syso is a throwaway profiling artifact.
#   --pgo-instr-use - Enable instrumentation PGO optimization (-fprofile-instr-use).
#                     Uses profile data from .local/pgo-data/instr.profdata
#   --no-prelink    - Disable prelink path and force relocatable link (ld -r).
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
#                   darwin: bundle .o as an archive renamed .syso (Apple ld64 -r
#                           drops DWARF; archive members keep per-.o DWARF +
#                           relocations, lets lldb source list work via external
#                           linking). linux: keep ld -r single relocatable object.
#   NO_OPT        - If true/1, compile with -O0 instead of -O3. For lldb source-
#                   level debugging (frame variable / step work as expected with-
#                   out <variable not available>). Auto-disables LTO and the
#                   stack-frame size check (-O0 doesn't inline, so frames exceed
#                   the production 500B nosplit budget). Typically combined with
#                   NO_PRELINK=1 + DEBUG_SYMBOLS=1 (the latter is set by `make
#                   <module>-debug`). See docs/debug-native.md.
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
#  Cygwin -> MSYS2 re-exec (windows hosts)
#    Cygwin passes POSIX path arguments unchanged to native
#    Windows tools (clang.exe, lld-link.exe, llvm-ar.exe), which
#    reject them. The MSYS2 runtime converts such arguments, so
#    re-exec this script under MSYS2 bash with a PATH covering
#    clang64 LLVM tools, MSYS2 coreutils, and Go.
# ============================================================
case "$(uname -s)" in
CYGWIN*)
  _msys2_root="${MSYS2_ROOT:-/cygdrive/c/msys64}"
  if [ ! -x "$_msys2_root/usr/bin/bash.exe" ]; then
    echo "Error: a Cygwin host requires MSYS2; bash not found at $_msys2_root/usr/bin/bash.exe." >&2
    echo "       Install MSYS2 or point MSYS2_ROOT at its root." >&2
    exit 1
  fi
  _go_bin="$(command -v go 2>/dev/null || true)"
  _go_dir=""
  [ -n "$_go_bin" ] && _go_dir="$(dirname "$_go_bin")"
  PATH="$_msys2_root/clang64/bin:$_msys2_root/usr/bin${_go_dir:+:$_go_dir}"
  export PATH
  exec "$_msys2_root/usr/bin/bash" "$0" "$@"
  ;;
esac

# ============================================================
#  Parse options
# ============================================================

GEN_ASM=false
PGO_INSTR=false
PGO_INSTR_USE=false
DISABLE_PRELINK=false
while [[ "${1:-}" == --* ]]; do
  case "$1" in
  --asm)
    GEN_ASM=true
    shift
    ;;
  --pgo-instr)
    PGO_INSTR=true
    shift
    ;;
  --pgo-instr-use)
    PGO_INSTR_USE=true
    shift
    ;;
  --no-prelink)
    DISABLE_PRELINK=true
    shift
    ;;
  *)
    echo "Error: Unknown option: $1"
    exit 1
    ;;
  esac
done

# Instrumentation-generate builds MUST NOT be prelinked: prelink.sh's linker
# script keeps only .text/.rodata and drops the __llvm_prf_* counter sections,
# and its zero-relocation/self-contained output cannot carry the external
# __llvm_profile_* references the instrumented code needs. The relocatable
# ($CC -r) path preserves both.
if [ "$PGO_INSTR" = true ]; then DISABLE_PRELINK=true; fi

# ============================================================
#  Load build configuration from sources.sh
# ============================================================

SOURCES_FILE="${1:-}"
shift || true

if [ -z "$SOURCES_FILE" ]; then
  echo "Error: sources.sh path is required as first argument"
  echo "Usage: gen-natives.sh [--asm] [--no-prelink] <sources.sh> [target_os] [target_arch]"
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
    p=$(echo "$p" | xargs) # trim whitespace
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
  1 | true | TRUE | yes | YES | on | ON) return 0 ;;
  *) return 1 ;;
  esac
}

if is_true "${NO_PRELINK:-}"; then
  DISABLE_PRELINK=true
fi

# NO_OPT: disable optimization (-O0 instead of -O3). Use with DEBUG_SYMBOLS=1
# (and typically NO_PRELINK=1 for darwin lldb source-level debugging). -O0 keeps
# variables in their source-defined locations so lldb frame variable / step work
# as expected; -O3 reorders/inlines and shows <variable not available>.
OPT_FLAG="-O3"
if is_true "${NO_OPT:-}"; then
  OPT_FLAG="-O0"
  echo "Debug: NO_OPT enabled (-O0; for lldb source-level debugging)"
fi

DEBUG_SYMBOLS_ENABLED=false
C_DEBUG_FLAGS="-g0"
if is_true "${DEBUG_SYMBOLS:-}"; then
  DEBUG_SYMBOLS_ENABLED=true
  C_DEBUG_FLAGS="-g3 -fno-omit-frame-pointer"
  echo "Debug: DEBUG_SYMBOLS enabled (keep richer syso symbols)"
fi

# PROFILE: production codegen (-O3 -DNDEBUG, LTO + prelink all ON) but WITH
# DWARF + frame pointers, so the C VM can be profiled with perf at source-line
# granularity. Unlike `make gen-debug`, this does NOT enable VJ_DEBUG/NO_NDEBUG
# (which would add trace logging / live asserts and change performance).
#
# The committed .syso stays byte-identical to a production build (it never
# carries .debug_*; those live only in the intermediate .so). PROFILE=1 makes
# prelink.sh keep .debug_*/.eh_frame in the merged .so and preserve that .so as
# a base-0 debug companion (see PRELINK_KEEP_DEBUG below). Source lines are then
# resolved via `addr2line` against that .so; see the profiling notes in
# scripts/prelink.sh.
if is_true "${PROFILE:-}"; then
  C_DEBUG_FLAGS="-g3 -fno-omit-frame-pointer"
  export PRELINK_KEEP_DEBUG=1
  echo "Profile: PROFILE enabled (-g3 + keep DWARF in intermediate .so; production -O3 -DNDEBUG codegen preserved)"
fi

# Production build: define NDEBUG so <assert.h> assert(...) disappears.
NDEBUG_FLAG="-DNDEBUG"
if is_true "${NO_NDEBUG:-}"; then
  NDEBUG_FLAG=""
  echo "Debug enabled: NO_NDEBUG=1"
fi

# ============================================================
#  Profile-Guided Optimization Configuration
#  Two mutually-exclusive modes (instrumentation-based):
#    --pgo-instr     instrumentation generate (-fprofile-instr-generate)
#    --pgo-instr-use instrumentation use      (-fprofile-instr-use)
# ============================================================

PGO_DATA_DIR="$REPO_ROOT/.local/pgo-data"
PGO_CFLAGS=""

if [ "$PGO_INSTR" = true ]; then
  # Instrumentation generate: insert per-block counters (__llvm_prf_* sections).
  # -fprofile-update=atomic: benchmark drives the VM from b.Loop and possibly
  #   multiple goroutines; non-atomic counters would race and corrupt the profile.
  PGO_CFLAGS="-fprofile-instr-generate -fprofile-update=atomic"
  echo "PGO: instrumentation generate enabled (counters inserted; no-prelink forced)"
elif [ "$PGO_INSTR_USE" = true ]; then
  PROFDATA="$PGO_DATA_DIR/instr.profdata"
  if [ ! -f "$PROFDATA" ]; then
    echo "Error: instrumentation PGO profile not found: $PROFDATA"
    echo "  Run 'make pgo-instr-collect' first to generate the profile data."
    exit 1
  fi
  # MSYS2 hosts: the profile path is embedded in the single argument
  # -fprofile-instr-use=<path>, which the MSYS2 runtime does not path-convert
  # (it only converts standalone path arguments). Hand clang a Windows path.
  if command -v cygpath >/dev/null 2>&1; then
    PROFDATA="$(cygpath -w "$PROFDATA")"
  fi
  # -fprofile-instr-use: precise block-count guided optimization
  PGO_CFLAGS="-fprofile-instr-use=$PROFDATA"
  echo "PGO: instrumentation use enabled (profile: $PROFDATA)"
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
darwin) HOST_OS="darwin" ;;
linux) HOST_OS="linux" ;;
mingw* | msys* | cygwin*) HOST_OS="windows" ;;
esac

# Arch detection
HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
arm64 | aarch64) HOST_ARCH="arm64" ;;
x86_64 | amd64) HOST_ARCH="amd64" ;;
esac

# ============================================================
#  Target platform (can be overridden by arguments)
# ============================================================

TARGET_OS="${1:-$HOST_OS}"
TARGET_ARCH="${2:-$HOST_ARCH}"

# Normalize target OS name
case "$TARGET_OS" in
darwin) TARGET_OS="darwin" ;;
linux) TARGET_OS="linux" ;;
windows) TARGET_OS="windows" ;;
esac

# ============================================================
#  Compiler selection (LLVM clang, with musl sysroot for Linux cross)
# ============================================================

# Check if cross-compiling
NEEDS_CROSS_COMPILE=false
if [ "$TARGET_OS" != "$HOST_OS" ] || [ "$TARGET_ARCH" != "$HOST_ARCH" ]; then
  NEEDS_CROSS_COMPILE=true
fi

# Build short target triple (format prelink.sh expects: x86_64-linux, aarch64-macos)
get_target_triple() {
  local os=$1
  local arch=$2

  case "$arch" in
  amd64) arch="x86_64" ;;
  arm64) arch="aarch64" ;;
  esac

  case "$os" in
  darwin) echo "${arch}-macos" ;;
  linux) echo "${arch}-linux" ;;
  windows) echo "${arch}-windows" ;;
  *) echo "${arch}-${os}" ;;
  esac
}

# Build clang target triple (for clangd --target)
get_clang_target() {
  local os=$1
  local arch=$2

  case "$arch" in
  amd64) arch="x86_64" ;;
  arm64) arch="aarch64" ;;
  esac

  case "$os" in
  darwin) echo "${arch}-apple-darwin" ;;
  linux) echo "${arch}-unknown-linux-gnu" ;;
  windows) echo "${arch}-pc-windows-msvc" ;;
  *) echo "${arch}-unknown-${os}" ;;
  esac
}

# ============================================================
#  Unified Toolchain Selection
#
#  TOOLCHAIN (env): llvm (the only supported toolchain)
#    llvm = force clang/lld-link for all platforms
#
#  LINUX_SYSROOT (env): path to musl sysroot headers for LLVM Linux targets
#     Default: /opt/linux/sysroot if it exists
#
#  Output variables:
#    RESOLVED_TOOLCHAIN - resolved value: "lld"
#    USE_LLVM           - always true
#    USE_LLD_LINK        - true if Windows lld-link pipeline is active
#    CC                 - compiler command with all flags
# ============================================================

resolve_toolchain() {
  local requested="${TOOLCHAIN:-llvm}"
  case "$requested" in
  llvm | auto) echo "lld" ;;
  zig)
    echo "Error: zig toolchain removed; use llvm (clang >= 22 + sysroot)" >&2
    exit 1
    ;;
  *)
    echo "Error: unknown TOOLCHAIN=$requested (expected: llvm)" >&2
    exit 1
    ;;
  esac
}

RESOLVED_TOOLCHAIN=$(resolve_toolchain)
USE_LLVM=1
USE_LLD_LINK=0

if [ "$RESOLVED_TOOLCHAIN" = "lld" ]; then
  case "$TARGET_OS" in
  windows)
    # Windows: clang compiles .c → .obj, lld-link links → DLL (no DllMain needed)
    # -isystem native/stdlib supplies the freestanding assert.h: the host has
    # no MSVC SDK, and assert.h is the only hosted libc header the sources use.
    #
    # The instrumented PGO build (--pgo-instr) targets mingw instead of MSVC.
    # MSVC-target instrumentation embeds /DEFAULTLIB:clang_rt.profile in the
    # objects; the GNU-mode linker of Go's external link cannot resolve that
    # directive and fails with "could not open 'libclang_rt.profile.a'". The
    # mingw target leaves runtime selection to the clang driver, which adds
    # libclang_rt.profile-x86_64.a from the toolchain's library search path.
    # Instrumentation counters are per source block, so the target switch
    # leaves the recorded profile equivalent.
    USE_LLD_LINK=1
    _win_triple="x86_64-pc-windows-msvc"
    if [ "$PGO_INSTR" = true ]; then
      _win_triple="x86_64-w64-windows-gnu"
    fi
    CC="clang --target=$_win_triple -ffreestanding -isystem $REPO_ROOT/native/stdlib"
    echo "Using llvm pipeline for windows (clang + lld-link, target: $_win_triple)"
    ;;
  linux)
    # Linux: clang with musl target triple + optional sysroot
    linux_triple=""
    case "$TARGET_ARCH" in
    amd64 | x86_64) linux_triple="x86_64-linux-musl" ;;
    arm64 | aarch64) linux_triple="aarch64-linux-musl" ;;
    *) linux_triple="${TARGET_ARCH}-linux-musl" ;;
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
    amd64 | x86_64) darwin_target="x86_64-apple-darwin" ;;
    arm64 | aarch64) darwin_target="arm64-apple-darwin" ;;
    *) darwin_target="${TARGET_ARCH}-apple-darwin" ;;
    esac
    CC="clang --target=$darwin_target"
    if [ "$(uname -s)" = "Darwin" ]; then
      CC="$CC -isysroot /Library/Developer/CommandLineTools/SDKs/MacOSX.sdk"
    fi
    echo "Using llvm pipeline for darwin ($darwin_target)"
    ;;
  esac
fi

export RESOLVED_TOOLCHAIN USE_LLVM USE_LLD_LINK CC

# ============================================================
#  Clang version check
#  encvm's computed-goto dispatch and float codegen are sensitive to the clang
#  version. Apple clang 17 / LLVM 20 will tail-merge the dispatch and ruin
#  branch prediction (the barrier in commit 737c23f is only a patch), and the
#  overall codegen quality is worse than LLVM 22 (CanadaGeometry marshal +12%).
#  LLVM clang >= 22 is required.
# ============================================================
clang_ver_line=$(eval "$CC --version" 2>/dev/null | head -1)
clang_major=$(eval "$CC -dumpversion" 2>/dev/null | cut -d. -f1)
echo "Compiler: $clang_ver_line"
if [ -n "$clang_major" ] && [ "$clang_major" -lt 22 ] 2>/dev/null; then
  echo "WARNING: clang $clang_major < 22. encvm codegen may regress" \
    "(dispatch tail-merge, float layout)." >&2
  echo "         Install /opt/llvm/current or prepend" \
    "PATH=/opt/llvm/current/bin:\$PATH." >&2
  echo "         (set VJ_ALLOW_OLD_CLANG=1 to suppress)" >&2
  if [ -z "${VJ_ALLOW_OLD_CLANG:-}" ]; then
    echo "         Aborting. Set VJ_ALLOW_OLD_CLANG=1 to override." >&2
    exit 1
  fi
fi

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
# ============================================================

USE_LTO=true
if [ "$DISABLE_PRELINK" = true ]; then
  USE_LTO=false
  echo "Note: LTO disabled (prelink disabled; relocatable -r path requires native object format)"
elif [ "$USE_LLVM" = "1" ] && [ "$TARGET_OS" = "darwin" ]; then
  USE_LTO=false
  echo "Note: LTO disabled (lld Mach-O backend does not reliably support -flto)"
elif [ "$TARGET_OS" = "linux" ] && [ "$TARGET_ARCH" = "arm64" ]; then
  USE_LTO=false
  echo "Note: LTO disabled for linux/arm64 (prelink uses native object format, not LLVM IR bitcode)"
fi
if is_true "${NO_OPT:-}" && [ "$USE_LTO" = true ]; then
  USE_LTO=false
  echo "Note: LTO disabled (NO_OPT=1; -O0 with LTO defeats the purpose of source-level debugging)"
fi

LTO_FLAG=""
if [ "$USE_LTO" = true ]; then
  LTO_FLAG="-flto"
fi

#
# How the diagnostic reaches the build:
#   * Non-LTO frontend codegen: -Wframe-larger-than=N -Werror=frame-larger-than=
#     turns the warning into a compile-time error.
#   * LTO IR emission: -Xclang -fwarn-stack-size=N stamps the threshold as an
#     IR attribute so the LTO backend (linker plugin) can honor it after
#     cross-TU inlining shakes out the real frame size.
#   * LTO Mach-O link: ld64.lld treats the "stack frame size exceeds limit"
#     message as a hard error by default (no extra flag needed).
#   * LTO ELF/COFF link: ld.lld/lld-link emit a warning; prelink.sh promotes
#     these to hard errors via --fatal-warnings / /WX.
STACK_WARN_FLAG=""
STACK_WARN_SIZE="${STACK_WARN_SIZE:-800}"

if [ "$STACK_WARN_SIZE" != "0" ] && [ "$USE_LLVM" = "1" ]; then
  STACK_WARN_FLAG="-Wframe-larger-than=$STACK_WARN_SIZE -Werror=frame-larger-than= -Xclang -fwarn-stack-size=$STACK_WARN_SIZE"
fi
# NO_OPT disables inlining; stack frames grow well past the production nosplit
# budget. NO_NDEBUG enables debug assertions/tracing. The check exists to
# protect the production call chain, not to constrain debug builds, so drop
# it when either -O0 or debug mode is in effect.
if is_true "${NO_OPT:-}" || is_true "${NO_NDEBUG:-}"; then
  STACK_WARN_FLAG=""
  STACK_WARN_SIZE=0
fi
export STACK_WARN_SIZE # prelink.sh reads this to decide --fatal-warnings/WX

# ============================================================
#  ISA-specific compiler flags
# ============================================================

# -mevex512: required by LLVM ≤20 to enable 512-bit EVEX
# encoding; without it, 512-bit AVX-512 intrinsics fail to compile.
# Clang 21+ (LLVM 21) deprecated this flag; -mavx512f alone is sufficient.
# Probe the compiler to decide.
_EVEX512_FLAG=""
_evex_probe_dir=$(mktemp -d)
: >"$_evex_probe_dir/probe.c"
if $CC -mevex512 -xc -c "$_evex_probe_dir/probe.c" -o "$_evex_probe_dir/probe.o" 2>&1 | grep -q 'deprecated'; then
  : # Clang 21+: flag deprecated, not needed
else
  _EVEX512_FLAG="-mevex512"
fi
rm -rf "$_evex_probe_dir"

get_isa_flags() {
  case "$1" in
  neon)
    # Quote/backslash chunk scanning uses pmull (ARMv8 crypto extension).
    # Apple targets enable crypto by default; generic Linux aarch64 does not.
    if [ "$TARGET_OS" = "linux" ]; then
      echo "-march=armv8-a+crypto"
    else
      echo ""
    fi
    ;;
  sse42) echo "-msse4.2 -mpclmul" ;;
  # -mbmi: BMI1 (tzcnt) ships in every AVX2-class CPU. ndec_ctz64_empty's tzcnt
  # asm branch requires __BMI__.
  avx2) echo "-mavx2 -mbmi -msse4.2 -mpclmul" ;;
  avx512) echo "-mavx512f -mavx512bw -mbmi -mbmi2 -mpclmul $_EVEX512_FLAG" ;;
  *) echo "" ;;
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
# The .syso is linked with -nostdlib; any unresolved libc symbol
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
# as a single mov instruction.  The stdlib sources (memory.c) use their
# own no-builtin flags plus loop idiom disable to prevent recursion.
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
#  These provide basic libc functions that the main code may call.
#  We compile them with stdlib specific no-builtin flags and disable
#  loop idiom recognition so the compiler cannot fold the hand written
#  loops back into libc calls and recurse into the same symbols.
# ============================================================

for stdlib_src in $STDLIB_SOURCES; do
  if [ -f "$stdlib_src" ]; then
    stdlib_base=$(basename "$stdlib_src" .c)
    stdlib_obj="${OUTPUT_DIR}/${stdlib_base}_${TARGET_OS}_${TARGET_ARCH}.o"
    echo "  Compiling $(basename "$stdlib_obj") (stdlib)"
    $CC $OPT_FLAG $PIC_FLAG $C_DEBUG_FLAGS -fno-stack-protector \
      -fno-builtin-memcpy -fno-builtin-memset -fno-builtin-memmove -fno-builtin-bzero -fno-builtin-memcmp \
      -mllvm -disable-loop-idiom-all \
      $ARCH_FLAGS $NDEBUG_FLAG \
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
    $CC $OPT_FLAG $LTO_FLAG $PIC_FLAG $C_DEBUG_FLAGS -fno-stack-protector $NO_BUILTIN_FLAGS $ARCH_FLAGS $MIN_ISA_FLAGS $NDEBUG_FLAG \
      -I"$(dirname "$extra_src")" -I"$REPO_ROOT/native/include" -I"$REPO_ROOT/native" \
      ${EXTRA_CFLAGS:-} ${PGO_CFLAGS:-} $STACK_WARN_FLAG \
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
    COMMON_DEFS="$ISA_MACRO $MODE_FLAG -DOS=${TARGET_OS} -DARCH=${TARGET_ARCH} $NDEBUG_FLAG ${EXTRA_CFLAGS:-}"
    COMMON_INCLUDES="-I$(dirname "$SOURCE_FILE") -I$VJ_LIB_DIR -I$REPO_ROOT/native/include -I$REPO_ROOT/native"

    # Step 1: Compile to object (with LTO when supported, for cross-TU inlining)
    echo "  Compiling $(basename "$OFILE")"
    $CC $OPT_FLAG $LTO_FLAG $PIC_FLAG $C_DEBUG_FLAGS -fno-stack-protector $NO_BUILTIN_FLAGS $ARCH_FLAGS $ISA_FLAGS \
      $COMMON_DEFS $COMMON_INCLUDES ${PGO_CFLAGS:-} $STACK_WARN_FLAG \
      -c "$SOURCE_FILE" -o "$OFILE"

    # Capture flags for .clangd from the first ISA/mode combination
    if [ "$CLANGD_FLAGS_COLLECTED" = false ]; then
      CLANGD_FLAGS_COLLECTED=true
      CLANGD_ADD_FLAGS="$ISA_MACRO $MODE_FLAG -DOS=${TARGET_OS} -DARCH=${TARGET_ARCH} $NDEBUG_FLAG -I$REPO_ROOT/native/include -I$REPO_ROOT/native"
    fi

    # Step 2: Generate assembly for debugging (optional).
    # Force -g here even when the object build is using -g0: --asm implies
    # "I want readable disasm", so we always emit source-line directives so
    # the .s file can be aligned to source with addr2line / grep .file.
    if [ "$GEN_ASM" = true ]; then
      mkdir -p "$OUTPUT_DIR/asm"
      SFILE="$OUTPUT_DIR/asm/${BASENAME}${MODE_SUFFIX}_${TARGET_OS}_${TARGET_ARCH}_${isa}.s"
      echo "  Generating asm: "$SFILE""
      $CC -S -g $OPT_FLAG -fno-stack-protector $NO_BUILTIN_FLAGS $ARCH_FLAGS -fno-asynchronous-unwind-tables $ISA_FLAGS \
        $PGO_CFLAGS $COMMON_DEFS $COMMON_INCLUDES \
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
      cat >"$TMP_ASM" <<HEADER
// ============================================================
//
//  Platform: $TARGET_OS/$TARGET_ARCH ($isa)
//
// ============================================================

HEADER
      cat "$SFILE" >>"$TMP_ASM"
      mv "$TMP_ASM" "$SFILE"

      ALL_ASM_PATHS="$ALL_ASM_PATHS $SFILE"
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
#  - --no-prelink / NO_PRELINK=1: force ld -r for all platforms
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

    if [ -n "${SYSO_PREFIX:-}" ]; then
      SYSO_NAME="${SYSO_PREFIX}_${TARGET_OS}_${TARGET_ARCH}.syso"
    else
      SYSO_NAME="${BASENAME}_${mode}_${isa}_${TARGET_OS}_${TARGET_ARCH}.syso"
    fi
    SYSO_PATH="$TARGET_DIR/$SYSO_NAME"

    echo "Linking $SYSO_NAME..."

    if needs_prelink; then
      # Prelink path (darwin, linux, windows):
      # Each syso is fully linked from stdlib + extra + main, then
      # prelink-obj strips all relocations. Since every syso contains
      # the same stdlib/extra code, the export filter below demotes
      # internal symbols to local, keeping only vj_vm_exec_<mode>_<isa>
      # as global; otherwise Go's linker would see duplicate definitions.
      LINK_OBJS="$STDLIB_OBJS $EXTRA_OBJS $MAIN_OBJ"

      PRELINK_TARGET=$(get_target_triple "$TARGET_OS" "$TARGET_ARCH")
      PRELINK_FLAGS="-o $SYSO_PATH -t $PRELINK_TARGET -i $isa"
      if [ "$USE_LTO" = true ]; then
        PRELINK_FLAGS="-l $PRELINK_FLAGS"
      fi

      # Export symbol list: keep only the per-syso entry-point(s) as
      # global. Without this, internal helpers (e.g. vj_write_float32)
      # remain global in every syso, causing duplicate symbol errors.
      #
      # Three forms are supported (in priority order):
      #
      #   EXPORT_SYMBOL_PREFIX_PATTERN + EXPORT_SYMBOL_NAMES
      #     A TU exporting MULTIPLE fixed-name entry points sharing a
      #     common prefix (e.g. ndec_sax_parse, ndec_dom_parse,
      #     ndec_bind_parse all start with "ndec_"). EXPORT_SYMBOL_NAMES
      #     is a space-separated list written into the darwin
      #     -exported_symbols_list; EXPORT_SYMBOL_PREFIX_PATTERN is
      #     forwarded to prelink-obj's HasPrefix filter so every
      #     ndec_* global stays exported on ELF/PE.
      #
      #   EXPORT_SYMBOL_NAME
      #     A TU exporting a single fixed-name entry point. The
      #     export-list contains that name; prelink-obj uses the same
      #     name as its prefix filter (so HasPrefix is effectively an
      #     exact match for the single global).
      #
      #   EXPORT_SYMBOL_PREFIX
      #     The historical naming convention: the export name is
      #     synthesized as "${EXPORT_SYMBOL_PREFIX}_${mode}_${isa}".
      if [ -n "${EXPORT_SYMBOL_PREFIX_PATTERN:-}" ] ||
        [ -n "${EXPORT_SYMBOL_PREFIX:-}" ] ||
        [ -n "${EXPORT_SYMBOL_NAME:-}" ]; then
        EXPORT_LIST="$OUTPUT_DIR/_exports_${mode}_${isa}.txt"
        : >"$EXPORT_LIST"
        if [ -n "${EXPORT_SYMBOL_NAMES:-}" ]; then
          sym_names="$EXPORT_SYMBOL_NAMES"
        elif [ -n "${EXPORT_SYMBOL_NAME:-}" ]; then
          sym_names="$EXPORT_SYMBOL_NAME"
        else
          sym_names="${EXPORT_SYMBOL_PREFIX}_${mode}_${isa}"
        fi
        for sym_name in $sym_names; do
          if [ "$TARGET_OS" = "darwin" ]; then
            # macOS ld: symbol names must be prefixed with '_'
            echo "_${sym_name}" >>"$EXPORT_LIST"
          else
            # ELF/COFF: no leading underscore
            echo "${sym_name}" >>"$EXPORT_LIST"
          fi
        done
        PRELINK_FLAGS="$PRELINK_FLAGS -e $EXPORT_LIST"
      fi

      # SYMBOL_RENAMES (optional, from sources.sh): a space-separated
      # list of old=new pairs. The {isa} / {mode} placeholders are
      # expanded per-syso. Useful when the C source uses a fixed
      # entry-point name (e.g. ndec_parse_default) but the Go side
      # wants per-ISA names (ndec_parse_default_neon).
      if [ -n "${SYMBOL_RENAMES:-}" ]; then
        for r in $SYMBOL_RENAMES; do
          expanded=${r//\{mode\}/$mode}
          expanded=${expanded//\{isa\}/$isa}
          PRELINK_FLAGS="$PRELINK_FLAGS -r $expanded"
        done
      fi

      # When the export list contains fixed names, the default
      # `_${prefix}_${mode}_${isa}` stripping logic in prelink.sh
      # would return the wrong prefix for ELF/PE export filtering.
      # Pass an explicit prefix through:
      #   EXPORT_SYMBOL_PREFIX_PATTERN: common prefix matching
      #                                  multiple entry points.
      #   EXPORT_SYMBOL_NAME:          the single fixed name itself.
      if [ -n "${EXPORT_SYMBOL_PREFIX_PATTERN:-}" ]; then
        export EXPORT_PREFIX="$EXPORT_SYMBOL_PREFIX_PATTERN"
      elif [ -n "${EXPORT_SYMBOL_NAME:-}" ]; then
        export EXPORT_PREFIX="$EXPORT_SYMBOL_NAME"
      fi

      "$REPO_ROOT/scripts/prelink.sh" $PRELINK_FLAGS $LINK_OBJS
      unset EXPORT_PREFIX
    else
      # Relocatable link path (NO_PRELINK):
      # - Include stdlib/extra objects only once to avoid duplicate symbol
      #   definitions across multiple mode×ISA .syso files.
      # - linux: `ld -r` preserves DWARF + relocations → single relocatable .syso
      # - darwin: Apple ld64 -r drops DWARF, ld64.lld doesn't support -r.
      #   Use `llvm-ar rcs` to bundle .o as an archive renamed .syso. Go loader
      #   detects `!<arch>` magic and iterates members (lib.go:1096); each .o
      #   keeps its DWARF + relocation entries. Apple ld64 processes members
      #   individually, generates STABS from DWARF, dsymutil runs, lldb
      #   source list works. See docs/debug-native.md.
      # - windows: clang's MSVC target rejects the gcc-style -r flag. Bundle
      #   the COFF objects with llvm-ar the same way; the instrumented PGO
      #   build (which forces NO_PRELINK so __llvm_prf_* sections survive)
      #   depends on this path. Go's loader handles PE archive members and
      #   the external linker resolves their relocations.
      if [ "$COMMON_OBJS_LINKED" = false ]; then
        LINK_OBJS="$STDLIB_OBJS $EXTRA_OBJS $MAIN_OBJ"
        COMMON_OBJS_LINKED=true
      else
        LINK_OBJS="$MAIN_OBJ"
      fi
      if [ "$TARGET_OS" = "darwin" ] || [ "$TARGET_OS" = "windows" ]; then
        # Archive path: bundle .o files (each with own DWARF + relocations).
        # llvm-ar lives alongside clang in the LLVM toolchain; rely on PATH
        # (gen-natives.sh runs with the LLVM bin dir on PATH).
        rm -f "$SYSO_PATH"
        llvm-ar rcs "$SYSO_PATH" $LINK_OBJS
      else
        # linux: ld -r preserves DWARF + relocations in a single relocatable obj.
        $CC -r $LTO_FLAG $LINK_OBJS -o "$SYSO_PATH"
      fi
    fi

    ALL_SYSO_PATHS="$ALL_SYSO_PATHS $SYSO_PATH"
  done
done

## ============================================================
##  Generate .clangd for IDE support (clangd, ccls, etc.)
##
##  Unlike compile_commands.json (which only covers .c files),
##  .clangd applies CompileFlags to ALL files in the directory
##  including headers, so macros like ISA_neon are resolved
##  when editing .h files directly.
## ============================================================

#CLANGD_PATH="$VJ_LIB_DIR/.clangd"
#CLANG_TARGET=$(get_clang_target "$TARGET_OS" "$TARGET_ARCH")
#{
#    echo "# Auto-generated by gen-natives.sh — do not edit manually."
#    echo "# Target: ${TARGET_OS}/${TARGET_ARCH} ($(echo $ISAS | tr ' ' ','))"
#    echo "CompileFlags:"
#    echo "  Add:"
#    echo "    - --target=$CLANG_TARGET"
#    for flag in $CLANGD_ADD_FLAGS; do
#        echo "    - $flag"
#    done
#} > "$CLANGD_PATH"
#echo "Generated $CLANGD_PATH"

## ============================================================
##  Summary
## ============================================================

echo ""
echo "Generated files:"
for syso in $ALL_SYSO_PATHS; do
  echo "  $syso"
done
if [ -n "${ALL_ASM_PATHS:-}" ]; then
  echo "Assembly files:"
  for asm in $ALL_ASM_PATHS; do
    echo "  $asm"
  done
fi
echo ""
echo "Done!"
