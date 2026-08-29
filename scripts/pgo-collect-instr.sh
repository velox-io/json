#!/usr/bin/env bash
#
# End-to-end instrumentation PGO collection.
#
# Uses LLVM instrumentation (-fprofile-instr-generate). It records EXACT
# per-block execution counts, so cold-but-important paths (e.g. GolangSource's
# deep nested-object descent) are not misjudged as cold and evicted the way
# sampling can when a few flat/large workloads dominate the samples.
#
# Pipeline:
#   1. Build an instrumented syso   (--pgo-instr => -fprofile-instr-generate,
#                                     forced no-prelink so __llvm_prf_* survive)
#   2. Build the benchmark binary with the profile runtime linked in AND a
#      TestMain flush hook (-tags vjpgoinstr): the Go runtime does not run C
#      atexit handlers, so we must call __llvm_profile_write_file() explicitly.
#   3. Run the workload with LLVM_PROFILE_FILE set  -> .profraw
#   4. llvm-profdata merge  -> .local/pgo-data/instr.profdata
#   5. Rebuild the production syso with --pgo-instr-use (prelinked, self-contained)
#
# Usage:
#   scripts/pgo-collect-instr.sh [target_os] [target_arch]
#     target_os    default: host OS   (linux/darwin/windows)
#     target_arch  default: host arch (amd64/arm64)
#
#   Windows hosts need MSYS2 with the clang64 environment (clang >= 22,
#   lld, compiler-rt, llvm tools) plus Go on PATH. Launched from a cygwin
#   shell the script re-execs itself under MSYS2 bash (root discovered via
#   MSYS2_ROOT, default /cygdrive/c/msys64); launched from an MSYS2/
#   clang64 shell it runs as-is.
#
# Environment overrides:
#   MODULE             Which native module to PGO. Default: encvm.
#                      encvm -> encode (Marshal/Encoder) workload
#                      ndec  -> decode (Unmarshal/Velox) workload
#   MODES              Build mode. Default: per-module (encvm: fast, ndec: default).
#                      encvm accepts one or more modes (fast|full|compact), space
#                      separated; modes are collected SEQUENTIALLY: each mode gets
#                      its own workload run and its own merged profile, then only
#                      that mode's syso is rebuilt. Same-named functions across
#                      mode copies collide in a single shared profile, so multiple
#                      modes must never be driven within one collection run.
#   PGO_BENCH_FILTER   -test.bench regex. Default: per-module AND per-mode.
#                      encvm fast:    '^Benchmark_(Marshal)_.*_Velox$' (fast VM:
#                                     plain Marshal, no indent/escape flags)
#                      encvm full:    '^Benchmark_PGOWorkload_Full$' (PGO-only
#                                     MarshalIndent workload, benchmark/pgo_workload_test.go)
#                      encvm compact: '^Benchmark_PGOWorkload_Compact$' (PGO-only
#                                     Marshal+WithStdCompat workload)
#                      ndec:          '^Benchmark_Unmarshal_.*_Velox$'
#                      An explicit value overrides every mode's default.
#   PGO_BENCH_TIME     -test.benchtime. Default: 3s
#   PGO_BENCH_COUNT    -test.count.     Default: 2
#   PGO_EXTRA_BENCH_FILTER / _TIME / _COUNT
#                      Optional second bench invocation (weighted run).
#                      Counters accumulate across runs (LLVM instrumentation is
#                      additive), so running a single benchmark for longer here
#                      effectively up-weights its blocks in the merged profdata.
#                      All three must be set together or all empty; partial
#                      sets are rejected. Default: all empty (single run).
#   PGO_KEEP_SYSO      If 1, leave the freshly built PGO syso in the tree.
#                      Default: 0 (the syso is a local, non-committed artifact).

set -euo pipefail

# ------------------------------------------------------------------
# Cygwin -> MSYS2 re-exec (windows hosts)
#   Cygwin passes POSIX path arguments unchanged to native Windows tools
#   (clang.exe, go.exe, llvm-profdata.exe), which reject them. The MSYS2
#   runtime converts such arguments, so re-exec under MSYS2 bash with a
#   PATH covering clang64 LLVM tools, MSYS2 coreutils, and Go.
# ------------------------------------------------------------------
case "$(uname -s)" in
CYGWIN*)
  _msys2_root="${MSYS2_ROOT:-/cygdrive/c/msys64}"
  if [ ! -x "$_msys2_root/usr/bin/bash.exe" ]; then
    echo "pgo-collect-instr: a Cygwin host requires MSYS2; bash not found at" >&2
    echo "                   $_msys2_root/usr/bin/bash.exe. Install MSYS2 or point MSYS2_ROOT at its root." >&2
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

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# ------------------------------------------------------------------
# Host / target resolution
# ------------------------------------------------------------------
_host_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$_host_os" in
mingw* | msys* | cygwin*) _host_os=windows ;;
esac
_host_arch=$(uname -m)
case "$_host_arch" in
x86_64 | amd64) _host_arch=amd64 ;;
arm64 | aarch64) _host_arch=arm64 ;;
esac

TARGET_OS="${1:-$_host_os}"
TARGET_ARCH="${2:-$_host_arch}"

MODULE="${MODULE:-encvm}"

# Per-module defaults: sources.sh, syso path, symbol name for size report,
# default MODES, default bench filter.
case "$MODULE" in
encvm)
  _sources_sh="native/encvm/sources.sh"
  _syso_dir="native/encvm"
  _syso_prefix="encvm"
  # encvm exports vj_vm_exec_<mode>_<isa>; symbol carries mode+isa suffix.
  _symbol_fn() { echo "vj_vm_exec_${1}_${2}"; }
  _default_modes="fast"
  _default_bench_filter='^Benchmark_(Marshal)_.*_Velox$'
  ;;
ndec)
  _sources_sh="native/ndec/sources.sh"
  _syso_dir="native/ndec"
  _syso_prefix="ndec"
  # ndec exports ndec_bind_parse (no mode/isa suffix; single-mode build).
  _symbol_fn() { echo "ndec_bind_parse"; }
  _default_modes="default"
  _default_bench_filter='^Benchmark_Unmarshal_.*_Velox$'
  ;;
*)
  echo "pgo-collect-instr: unknown MODULE='$MODULE' (expected: encvm or ndec)" >&2
  exit 1
  ;;
esac

MODES="${MODES:-$_default_modes}"

# encvm: the default bench filter follows the mode being collected. The
# Benchmark_Marshal_*_Velox suite only drives the fast VM (plain Marshal: no
# indent, no escape flags); full and compact have dedicated PGO-only entries
# in benchmark/pgo_workload_test.go (no library suffix, so bench.sh sweeps
# never match them). An explicit PGO_BENCH_FILTER overrides every mode.
_mode_bench_filter() {
  if [ -n "${PGO_BENCH_FILTER:-}" ]; then
    echo "$PGO_BENCH_FILTER"
    return
  fi
  case "$MODULE/$1" in
  encvm/full) echo '^Benchmark_PGOWorkload_Full$' ;;
  encvm/compact) echo '^Benchmark_PGOWorkload_Compact$' ;;
  *) echo "$_default_bench_filter" ;;
  esac
}

# encvm mode tokens are validated upfront; collection itself is sequential
# (see the steps 3-5 loop below).
if [ "$MODULE" = "encvm" ]; then
  for _mode in $MODES; do
    case "$_mode" in
    fast | full | compact) ;;
    *)
      echo "pgo-collect-instr: encvm MODES entries must be fast|full|compact (got: '$_mode' in '$MODES')" >&2
      exit 1
      ;;
    esac
  done
fi
PGO_BENCH_TIME="${PGO_BENCH_TIME:-3s}"
PGO_BENCH_COUNT="${PGO_BENCH_COUNT:-2}"
PGO_EXTRA_BENCH_FILTER="${PGO_EXTRA_BENCH_FILTER:-}"
PGO_EXTRA_BENCH_TIME="${PGO_EXTRA_BENCH_TIME:-}"
PGO_EXTRA_BENCH_COUNT="${PGO_EXTRA_BENCH_COUNT:-}"
PGO_KEEP_SYSO="${PGO_KEEP_SYSO:-0}"

# PGO_EXTRA_BENCH_* is all-or-nothing: a partial set would silently fall back
# to PGO_BENCH_* defaults for the missing fields, which is almost never what
# the user intended. Reject early.
_extra_set_count=0
[ -n "$PGO_EXTRA_BENCH_FILTER" ] && _extra_set_count=$((_extra_set_count + 1))
[ -n "$PGO_EXTRA_BENCH_TIME" ] && _extra_set_count=$((_extra_set_count + 1))
[ -n "$PGO_EXTRA_BENCH_COUNT" ] && _extra_set_count=$((_extra_set_count + 1))
if [ "$_extra_set_count" -ne 0 ] && [ "$_extra_set_count" -ne 3 ]; then
  echo "pgo-collect-instr: PGO_EXTRA_BENCH_FILTER/TIME/COUNT must be set together or all empty" >&2
  echo "                     got: filter='${PGO_EXTRA_BENCH_FILTER:-<empty>}'" \
    "time='${PGO_EXTRA_BENCH_TIME:-<empty>}'" "count='${PGO_EXTRA_BENCH_COUNT:-<empty>}'" >&2
  exit 1
fi

# ISA for the given target (matches gen-natives.sh get_available_isas)
case "$TARGET_OS/$TARGET_ARCH" in
*/amd64) ISA=avx2 ;;
*/arm64) ISA=neon ;;
*)
  echo "pgo-collect-instr: unsupported target $TARGET_OS/$TARGET_ARCH" >&2
  exit 1
  ;;
esac

# One syso per mode; sizes and artifacts below are reported for every mode.
_syso_path() {
  echo "${_syso_dir}/${_syso_prefix}_${1}_${ISA}_${TARGET_OS}_${TARGET_ARCH}.syso"
}

_all_syso_paths() {
  local mode paths=""
  for mode in $MODES; do paths="$paths $(_syso_path "$mode")"; done
  echo "$paths"
}

PGO_DATA_DIR="$REPO_ROOT/.local/pgo-data"
PROFDATA="$PGO_DATA_DIR/instr.profdata"
BENCH_TEST="$PGO_DATA_DIR/bench_pgo_instr.test"

# ------------------------------------------------------------------
# Windows host adaptations
#   The benchmark binary is a native PE: it needs an .exe suffix for the
#   shell to exec it, and the LLVM profile runtime opens LLVM_PROFILE_FILE
#   through the Windows API, so the value must be a Windows path. cgo
#   defaults CC to gcc on windows; gcc rejects the clang driver flag
#   -fprofile-instr-generate at the external link step, so point CC at
#   the clang64 clang.
# ------------------------------------------------------------------
if [ "$_host_os" = "windows" ]; then
  BENCH_TEST="$PGO_DATA_DIR/bench_pgo_instr.test.exe"
  export CC=clang CGO_ENABLED=1
fi

# nm: llvm-nm substitutes for binutils nm (the MSYS2 base env ships no nm).
NM="$(command -v nm 2>/dev/null || command -v llvm-nm 2>/dev/null || true)"

# Profile file pattern for LLVM_PROFILE_FILE. On a windows host the value
# goes to a native PE, so convert to a Windows path (cygpath exists on both
# cygwin and MSYS2; a pre-converted value is left untouched by their env
# variable conversion).
_pgo_profile_file() {
  if [ "$_host_os" = "windows" ]; then
    cygpath -w "$PGO_DATA_DIR/vj-%p.profraw"
  else
    echo "$PGO_DATA_DIR/vj-%p.profraw"
  fi
}

# ------------------------------------------------------------------
# Preflight
# ------------------------------------------------------------------
_need() { command -v "$1" >/dev/null 2>&1 || {
  echo "pgo-collect-instr: missing required tool: $1" >&2
  exit 1
}; }
_need clang
_need llvm-profdata
_need go
if [ -n "$NM" ]; then
  _need "$NM"
else
  echo "pgo-collect-instr: missing required tool: nm (or llvm-nm)" >&2
  exit 1
fi

if [ "$TARGET_OS" != "$_host_os" ] || [ "$TARGET_ARCH" != "$_host_arch" ]; then
  echo "pgo-collect-instr: cannot run the instrumented workload on a cross target" >&2
  echo "                   ($TARGET_OS/$TARGET_ARCH) from this host ($_host_os/$_host_arch)." >&2
  echo "                   Run this on the target machine." >&2
  exit 1
fi

mkdir -p "$PGO_DATA_DIR"

# ------------------------------------------------------------------
# macOS + LLVM clang auto-config
#   Apple clang knows the macOS SDK path natively; LLVM clang does not.
#   When the clang in PATH is LLVM (e.g. /opt/llvm/current/bin/clang,
#   required by gen-natives.sh for clang >= 22), cgo's C compilation fails
#   on errno.h/stdlib.h/pthread.h unless SDKROOT is set, and the final
#   link step fails to find libclang_rt.profile unless the runtime lib dir
#   is on -L. Auto-set both from xcrun and clang -print-resource-dir when
#   the user hasn't already provided them.
# ------------------------------------------------------------------
if [ "$_host_os" = "darwin" ]; then
  _clang_ver_line=$(clang --version 2>/dev/null | head -1 || true)
  case "$_clang_ver_line" in
  "Apple clang"*) ;; # Apple clang: nothing to do
  "clang version "*)
    if [ -z "${SDKROOT:-}" ]; then
      SDKROOT="$(xcrun --show-sdk-path 2>/dev/null || true)"
      [ -n "$SDKROOT" ] && export SDKROOT
    fi
    if [ -z "${CGO_CFLAGS:-}" ] && [ -n "${SDKROOT:-}" ]; then
      CGO_CFLAGS="-isysroot ${SDKROOT}"
      export CGO_CFLAGS
    fi
    if [ -z "${CGO_LDFLAGS:-}" ]; then
      _resource_dir=$(clang -print-resource-dir 2>/dev/null || true)
      if [ -n "$_resource_dir" ] && [ -d "${_resource_dir}/lib/darwin" ]; then
        CGO_LDFLAGS="-L${_resource_dir}/lib/darwin"
        export CGO_LDFLAGS
      fi
    fi
    echo "    darwin+LLVM clang: SDKROOT=${SDKROOT:-<unset>} CGO_CFLAGS=${CGO_CFLAGS:-<unset>} CGO_LDFLAGS=${CGO_LDFLAGS:-<unset>}"
    ;;
  esac
fi

# $1 = syso path, $2 = entry symbol. The [[:space:]] anchor is load-bearing:
# sibling symbols like __profc_<name> (instrumentation counter arrays) also
# end in the entry name, and under --size-sort | head -1 the tiny counter
# symbol would shadow the real function body.
_syso_fn_size() {
  local _sz _range
  _sz=$("$NM" --print-size --size-sort "$1" 2>/dev/null |
    grep "[[:space:]]$2\$" | awk '{print $2}' | head -1 || true)
  if [ -n "$_sz" ] && [ "$_sz" != "0000000000000000" ]; then
    echo "0x$_sz"
    return
  fi
  # Mach-O nlist has no size field; derive the size from the disassembly
  # (label to next label, arm64 fixed 4-byte instructions).
  if [ "$TARGET_OS/$TARGET_ARCH" = "darwin/arm64" ]; then
    _range=$(otool -tV "$1" 2>/dev/null | awk -v sym="_$2:" '
      $0 == sym { inf = 1; next }
      inf && /^[0-9a-f]+\t/ { if (start == "") start = $1; last = $1; next }
      inf { exit }
      END { if (start != "") print start, last }')
    if [ -n "$_range" ]; then
      set -- $_range
      printf '0x%x\n' $(( 0x$2 + 4 - 0x$1 ))
      return
    fi
  fi
  echo "0x${_sz:-0}"
}

# $1 = label; prints one "<label> <symbol> size: ..." line per mode.
# $2 = optional mode list (default: all of $MODES).
_report_syso_sizes() {
  local _label=$1 _modes="${2:-$MODES}" mode syso sym
  for mode in $_modes; do
    syso=$(_syso_path "$mode")
    sym=$(_symbol_fn "$mode" "$ISA")
    echo "    $_label $sym size: $(_syso_fn_size "$syso" "$sym")"
  done
}

echo "==> pgo-collect-instr: module=$MODULE target=$TARGET_OS/$TARGET_ARCH isa=$ISA modes='$MODES'"
for _mode in $MODES; do
  echo "    bench[$_mode]='$(_mode_bench_filter "$_mode")' time=$PGO_BENCH_TIME count=$PGO_BENCH_COUNT"
done
if [ -n "$PGO_EXTRA_BENCH_FILTER" ]; then
  echo "    extra='$PGO_EXTRA_BENCH_FILTER' time=$PGO_EXTRA_BENCH_TIME count=$PGO_EXTRA_BENCH_COUNT (after every mode)"
fi

# ------------------------------------------------------------------
# Step 1: instrumented syso  (--pgo-instr forces no-prelink internally)
# ------------------------------------------------------------------
echo "==> [1/5] building instrumented syso (-fprofile-instr-generate, no-prelink)"
MODES="$MODES" \
  scripts/gen-natives.sh --pgo-instr "$_sources_sh" "$TARGET_OS" "$TARGET_ARCH" >/dev/null
_report_syso_sizes "instrumented syso"

# ------------------------------------------------------------------
# Step 2: benchmark binary  (profile runtime + flush hook via vjpgoinstr tag)
#   -extldflags=-fprofile-instr-generate: makes the clang link driver pull in
#     libclang_rt.profile (defines __llvm_profile_write_file, the counters, etc.)
#   -tags vjpgoinstr: compiles benchmark/pgo_instr_flush.go + its TestMain, which
#     explicitly flushes counters on exit (Go does not run C atexit handlers).
# ------------------------------------------------------------------
echo "==> [2/5] building instrumented benchmark binary (runtime + flush hook)"
# The external link driver must be clang: -fprofile-instr-generate (and the
# libclang_rt.profile pull-in) is a clang-only flag that GNU gcc rejects.
# Darwin and MSYS2 hosts already default CC to a clang driver; Linux defaults
# to gcc, so point it at the LLVM clang on PATH. A pre-set CC wins.
if [ "$(uname -s)" = "Linux" ]; then
  export CC="${CC:-clang}"
fi
(cd benchmark && GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
  go test -c -tags vjpgoinstr -o "$BENCH_TEST" \
  -ldflags="-linkmode=external -extldflags=-fprofile-instr-generate" .)

# NOTE: do NOT pipe `nm | grep -q`. Under `set -o pipefail`, grep -q closes the
# pipe on first match and nm dies with SIGPIPE (141), which pipefail propagates
# as a pipeline failure, a false negative. Capture first, then match.
# Count matches with `grep -c` (consumes all input) rather than `grep -q`
# (exits on first match). Under `set -o pipefail`, an early-closing grep -q makes
# the upstream producer die with SIGPIPE and the pipeline reports failure, a
# false negative even when the symbol is present.
_rt_hits=$("$NM" "$BENCH_TEST" 2>/dev/null | grep -c "__llvm_profile_write_file" || true)
if [ "${_rt_hits:-0}" -eq 0 ] && [ "$_host_os" = "windows" ]; then
  # An MSVC-target clang drives lld-link, whose final image carries no COFF
  # symbol table, so nm reports "no symbols" even with the runtime linked
  # in (an unresolved __llvm_profile_write_file reference would have failed
  # the link). Fall back to the section table: on COFF the instrumentation
  # counters live in .lprfc$M sections, which the linker merges into .lprfc
  # in the final image. The section table is always present.
  _rt_hits=$(llvm-readobj --sections "$BENCH_TEST" 2>/dev/null |
    grep -c "lprfc" || true)
fi
if [ "${_rt_hits:-0}" -eq 0 ]; then
  echo "pgo-collect-instr: benchmark binary is missing the profile runtime;" >&2
  echo "                   -extldflags=-fprofile-instr-generate did not take effect." >&2
  echo "                   (nm='$NM'; first lines of nm output:)" >&2
  "$NM" "$BENCH_TEST" 2>&1 | head -3 >&2 || true
  exit 1
fi

# ------------------------------------------------------------------
# Steps 3-5 run once PER MODE, sequentially:
#   Step 3: run that mode's workload filter. The bench binary embeds every
#     mode's instrumented copy, but each filter drives exactly one VM (fast
#     suite -> plain Marshal; PGOWorkload_Full -> MarshalIndent;
#     PGOWorkload_Compact -> Marshal+WithStdCompat), so the other copies
#     stay at zero counts and merge away harmlessly.
#   Step 4: merge that mode's profraw -> instr.profdata (the fixed path
#     gen-natives reads), archiving a per-mode copy.
#   Step 5: rebuild ONLY that mode's production syso against the profile.
#
# If PGO_EXTRA_BENCH_* is set, an additional weighted invocation runs after
# each mode's main one. LLVM instrumentation counters are additive: every
# run contributes to the same merged profdata in step 4, so running a
# single benchmark for longer here up-weights its blocks in the profile.
# ------------------------------------------------------------------
for mode in $MODES; do
  _bench_filter="$(_mode_bench_filter "$mode")"

  echo "==> [3/5] mode=$mode: running workload to collect counters"
  echo "    bench='$_bench_filter' time=$PGO_BENCH_TIME count=$PGO_BENCH_COUNT"
  rm -f "$PGO_DATA_DIR"/vj-*.profraw
  LLVM_PROFILE_FILE="$(_pgo_profile_file)" \
    "$BENCH_TEST" -test.run='^$' -test.bench="$_bench_filter" \
    -test.benchtime="$PGO_BENCH_TIME" -test.count="$PGO_BENCH_COUNT" >/dev/null

  if [ -n "$PGO_EXTRA_BENCH_FILTER" ]; then
    echo "==> [3b/5] mode=$mode: running extra weighted workload (counters accumulate)"
    echo "         filter='$PGO_EXTRA_BENCH_FILTER' time=$PGO_EXTRA_BENCH_TIME count=$PGO_EXTRA_BENCH_COUNT"
    LLVM_PROFILE_FILE="$(_pgo_profile_file)" \
      "$BENCH_TEST" -test.run='^$' -test.bench="$PGO_EXTRA_BENCH_FILTER" \
      -test.benchtime="$PGO_EXTRA_BENCH_TIME" -test.count="$PGO_EXTRA_BENCH_COUNT" >/dev/null
  fi

  _raw_count=$(find "$PGO_DATA_DIR" -maxdepth 1 -name 'vj-*.profraw' 2>/dev/null | wc -l | tr -d ' ')
  _raw_bytes=$(cat "$PGO_DATA_DIR"/vj-*.profraw 2>/dev/null | wc -c | tr -d ' ')
  echo "    profraw files: $_raw_count ($_raw_bytes bytes total)"
  if [ "$_raw_count" = "0" ] || [ "$_raw_bytes" = "0" ]; then
    echo "pgo-collect-instr: no counter data written (mode=$mode). The Go exit path may not" >&2
    echo "                   have flushed; ensure -tags vjpgoinstr TestMain is compiled in." >&2
    exit 1
  fi

  # ----------------------------------------------------------------
  # Step 4: merge to instr profdata. Each mode overwrites the fixed path;
  #         instr-<mode>.profdata preserves the per-mode copy so a single
  #         mode can be re-optimized later without recollecting.
  # ----------------------------------------------------------------
  echo "==> [4/5] mode=$mode: llvm-profdata merge -> $PROFDATA"
  llvm-profdata merge "$PGO_DATA_DIR"/vj-*.profraw -o "$PROFDATA"
  echo "    $(llvm-profdata show "$PROFDATA" 2>/dev/null | grep -iE 'Total functions|Total number of blocks' | tr '\n' ' ')"
  cp "$PROFDATA" "$PGO_DATA_DIR/instr-$mode.profdata"

  # ----------------------------------------------------------------
  # Step 5: production PGO syso for THIS mode only (--pgo-instr-use,
  #         prelinked & self-contained)
  # ----------------------------------------------------------------
  echo "==> [5/5] mode=$mode: rebuilding production syso with --pgo-instr-use"
  MODES="$mode" \
    scripts/gen-natives.sh --pgo-instr-use "$_sources_sh" "$TARGET_OS" "$TARGET_ARCH" >/dev/null
  _report_syso_sizes "PGO syso" "$mode"
done

echo ""
echo "Done. Artifacts (gitignored, under .local/pgo-data/):"
for mode in $MODES; do
  echo "  profile : $PGO_DATA_DIR/instr-$mode.profdata"
  echo "  syso    : $(_syso_path "$mode")"
done
if [ "$PGO_KEEP_SYSO" != "1" ]; then
  echo ""
  echo "NOTE: instrumentation PGO syso is a LOCAL artifact and is NOT meant to be committed."
  echo "      To restore the committed syso:  git checkout -- $(_all_syso_paths)"
  echo "      (set PGO_KEEP_SYSO=1 to suppress this note)"
fi
