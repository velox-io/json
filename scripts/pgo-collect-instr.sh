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
#   3. Run the encode workload with LLVM_PROFILE_FILE set  -> .profraw
#   4. llvm-profdata merge  -> .local/pgo-data/instr.profdata
#   5. Rebuild the production syso with --pgo-instr-use (prelinked, self-contained)
#
# Usage:
#   scripts/pgo-collect-instr.sh [target_os] [target_arch]
#     target_os    default: host OS   (linux/darwin/windows)
#     target_arch  default: host arch (amd64/arm64)
#
# Environment overrides:
#   MODES              Build mode(s). Default: fast  (the mode plain Marshal uses)
#   PGO_BENCH_FILTER   -test.bench regex. Default: full encode set
#                      '^Benchmark_(Marshal|Encoder)_.*_Velox$'
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

MODES="${MODES:-fast}"
PGO_BENCH_FILTER="${PGO_BENCH_FILTER:-^Benchmark_(Marshal|Encoder)_.*_Velox\$}"
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
[ -n "$PGO_EXTRA_BENCH_TIME" ]   && _extra_set_count=$((_extra_set_count + 1))
[ -n "$PGO_EXTRA_BENCH_COUNT" ]  && _extra_set_count=$((_extra_set_count + 1))
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

# First mode is the one whose syso size we report before/after.
FIRST_MODE=$(echo "$MODES" | awk '{print $1}')
SYSO="native/encvm/encvm_${FIRST_MODE}_${ISA}_${TARGET_OS}_${TARGET_ARCH}.syso"

PGO_DATA_DIR="$REPO_ROOT/.local/pgo-data"
PROFDATA="$PGO_DATA_DIR/instr.profdata"
BENCH_TEST="$PGO_DATA_DIR/bench_pgo_instr.test"

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

if [ "$TARGET_OS" != "$_host_os" ] || [ "$TARGET_ARCH" != "$_host_arch" ]; then
  echo "pgo-collect-instr: cannot run the instrumented workload on a cross target" >&2
  echo "                   ($TARGET_OS/$TARGET_ARCH) from this host ($_host_os/$_host_arch)." >&2
  echo "                   Run this on the target machine." >&2
  exit 1
fi

mkdir -p "$PGO_DATA_DIR"

_syso_fn_size() {
  nm --print-size --size-sort "$SYSO" 2>/dev/null |
    grep "vj_vm_exec_${FIRST_MODE}_${ISA}\$" | awk '{print "0x"$2}' | head -1 || true
}

echo "==> pgo-collect-instr: target=$TARGET_OS/$TARGET_ARCH isa=$ISA modes='$MODES'"
echo "    bench='$PGO_BENCH_FILTER' time=$PGO_BENCH_TIME count=$PGO_BENCH_COUNT"
if [ -n "$PGO_EXTRA_BENCH_FILTER" ]; then
  echo "    extra='$PGO_EXTRA_BENCH_FILTER' time=$PGO_EXTRA_BENCH_TIME count=$PGO_EXTRA_BENCH_COUNT"
fi

# ------------------------------------------------------------------
# Step 1: instrumented syso  (--pgo-instr forces no-prelink internally)
# ------------------------------------------------------------------
echo "==> [1/5] building instrumented syso (-fprofile-instr-generate, no-prelink)"
MODES="$MODES" \
  scripts/gen-natives.sh --pgo-instr native/encvm/sources.sh "$TARGET_OS" "$TARGET_ARCH" >/dev/null
echo "    instrumented syso vj_vm_exec size: $(_syso_fn_size)"

# ------------------------------------------------------------------
# Step 2: benchmark binary  (profile runtime + flush hook via vjpgoinstr tag)
#   -extldflags=-fprofile-instr-generate: makes the clang link driver pull in
#     libclang_rt.profile (defines __llvm_profile_write_file, the counters, etc.)
#   -tags vjpgoinstr: compiles benchmark/pgo_instr_flush.go + its TestMain, which
#     explicitly flushes counters on exit (Go does not run C atexit handlers).
# ------------------------------------------------------------------
echo "==> [2/5] building instrumented benchmark binary (runtime + flush hook)"
(cd benchmark && GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
  go test -c -tags vjpgoinstr -o "$BENCH_TEST" \
  -ldflags="-linkmode=external -extldflags=-fprofile-instr-generate" .)

# NOTE: do NOT pipe `nm | grep -q`. Under `set -o pipefail`, grep -q closes the
# pipe on first match and nm dies with SIGPIPE (141), which pipefail propagates
# as a pipeline failure — a false negative. Capture first, then match.
# Count matches with `grep -c` (consumes all input) rather than `grep -q`
# (exits on first match). Under `set -o pipefail`, an early-closing grep -q makes
# the upstream producer die with SIGPIPE and the pipeline reports failure — a
# false negative even when the symbol is present.
_rt_hits=$(nm "$BENCH_TEST" 2>/dev/null | grep -c "__llvm_profile_write_file" || true)
if [ "${_rt_hits:-0}" -eq 0 ]; then
  echo "pgo-collect-instr: benchmark binary is missing the profile runtime;" >&2
  echo "                   -extldflags=-fprofile-instr-generate did not take effect." >&2
  exit 1
fi

# ------------------------------------------------------------------
# Step 3: run the workload, collect .profraw  (%p = pid, avoids clobber)
#   If PGO_EXTRA_BENCH_* is set, run an additional weighted invocation after
#   the main one. LLVM instrumentation counters are additive: every run
#   contributes to the same merged profdata in step 4, so running a single
#   benchmark for longer here up-weights its blocks in the final profile.
# ------------------------------------------------------------------
echo "==> [3/5] running main workload to collect counters"
rm -f "$PGO_DATA_DIR"/vj-*.profraw
LLVM_PROFILE_FILE="$PGO_DATA_DIR/vj-%p.profraw" \
  "$BENCH_TEST" -test.run='^$' -test.bench="$PGO_BENCH_FILTER" \
  -test.benchtime="$PGO_BENCH_TIME" -test.count="$PGO_BENCH_COUNT" >/dev/null

if [ -n "$PGO_EXTRA_BENCH_FILTER" ]; then
  echo "==> [3b/5] running extra weighted workload (counters accumulate)"
  echo "         filter='$PGO_EXTRA_BENCH_FILTER' time=$PGO_EXTRA_BENCH_TIME count=$PGO_EXTRA_BENCH_COUNT"
  LLVM_PROFILE_FILE="$PGO_DATA_DIR/vj-%p.profraw" \
    "$BENCH_TEST" -test.run='^$' -test.bench="$PGO_EXTRA_BENCH_FILTER" \
    -test.benchtime="$PGO_EXTRA_BENCH_TIME" -test.count="$PGO_EXTRA_BENCH_COUNT" >/dev/null
fi

_raw_count=$(ls "$PGO_DATA_DIR"/vj-*.profraw 2>/dev/null | wc -l | tr -d ' ')
_raw_bytes=$(cat "$PGO_DATA_DIR"/vj-*.profraw 2>/dev/null | wc -c | tr -d ' ')
echo "    profraw files: $_raw_count ($_raw_bytes bytes total)"
if [ "$_raw_count" = 0 ] || [ "$_raw_bytes" = 0 ]; then
  echo "pgo-collect-instr: no counter data written. The Go exit path may not have" >&2
  echo "                   flushed; ensure -tags vjpgoinstr TestMain is compiled in." >&2
  exit 1
fi

# ------------------------------------------------------------------
# Step 4: merge to instr profdata
# ------------------------------------------------------------------
echo "==> [4/5] llvm-profdata merge -> $PROFDATA"
llvm-profdata merge "$PGO_DATA_DIR"/vj-*.profraw -o "$PROFDATA"
echo "    $(llvm-profdata show "$PROFDATA" 2>/dev/null | grep -iE 'Total functions|Total number of blocks' | tr '\n' ' ')"

# ------------------------------------------------------------------
# Step 5: production PGO syso  (--pgo-instr-use, prelinked & self-contained)
# ------------------------------------------------------------------
echo "==> [5/5] rebuilding production syso with --pgo-instr-use"
MODES="$MODES" \
  scripts/gen-natives.sh --pgo-instr-use native/encvm/sources.sh "$TARGET_OS" "$TARGET_ARCH" >/dev/null
echo "    PGO syso vj_vm_exec size: $(_syso_fn_size)"

echo ""
echo "Done. Artifacts (gitignored, under .local/pgo-data/):"
echo "  profile : $PROFDATA"
echo "  syso    : $SYSO"
if [ "$PGO_KEEP_SYSO" != "1" ]; then
  echo ""
  echo "NOTE: instrumentation PGO syso is a LOCAL artifact and is NOT meant to be committed."
  echo "      To restore the committed syso:  git checkout -- $SYSO"
  echo "      (set PGO_KEEP_SYSO=1 to suppress this note)"
fi
