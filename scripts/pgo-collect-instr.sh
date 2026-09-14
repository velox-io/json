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
#   1. Build an instrumented blob    (--pgo-instr => -fprofile-instr-generate,
#                                     forced no-prelink so __llvm_prf_* survive)
#   2. Build the benchmark binary with the profile runtime linked in AND a
#      TestMain flush hook (-tags vjpgoinstr): the Go runtime does not run C
#      atexit handlers, so we must call __llvm_profile_write_file() explicitly.
#   3. Run the workload with LLVM_PROFILE_FILE set  -> .profraw
#   4. llvm-profdata merge  -> .local/pgo-data/instr-<mode>.profdata
#   5. Rebuild the production blob with --pgo-instr-use (prelinked,
#      self-contained; each mode consumes its own profile)
#
# Both modules ship arch-canonical linux-built blobs (SYSO_ARCH_ONLY), so
# the profile must be collected on a linux target of the matching arch.
#
# Usage:
#   scripts/pgo-collect-instr.sh [target_os] [target_arch]
#     target_os    default: host OS; must be linux
#     target_arch  default: host arch (amd64/arm64)
#
# Determinism: the workload runs with per-benchmark FIXED iteration counts
# from scripts/pgo-bench-iters.txt, not a wall-clock budget. A time budget
# makes per-benchmark iteration counts (and so the merged profile weights)
# drift with machine conditions, and LLVM layout decisions then flip between
# otherwise identical collections; the measured swing on KubePods Unmarshal
# was ~3% between two identical runs. The table is calibrated once
# (PGO_ITERS_REGEN=1) and committed, so the same tree yields a byte-identical
# blob on every machine. To re-weight a workload, edit the table (or regen),
# re-collect, and gate the result: make pgo-gate MODULE=<module>.
#
# Environment overrides:
#   MODULE             Which native module to PGO. Default: encvm.
#                      encvm -> encode (Marshal/Encoder) workload
#                      ndec  -> decode (Unmarshal/Velox) workload
#   MODES              Collected modes. Default: per-module (encvm: fast,
#                      ndec: default). encvm accepts one or more modes
#                      (fast|full|compact), space separated; modes are
#                      collected SEQUENTIALLY: each mode gets its own
#                      workload run and its own merged profile
#                      (instr-<mode>.profdata), and the final blob rebuild
#                      resolves each mode's own profile. Same-named functions
#                      across mode copies collide in a single shared profile,
#                      so multiple modes must never be driven within one
#                      collection run. Modes left uncollected rebuild at
#                      baseline codegen.
#   PGO_BENCH_FILTER   -test.bench regex. Default: per-module AND per-mode.
#                      encvm fast:    '^Benchmark_(Marshal)_.*_Velox$' (fast VM:
#                                     plain Marshal, no indent/escape flags)
#                      encvm full:    '^Benchmark_PGOWorkload_Full$' (PGO-only
#                                     MarshalIndent workload, benchmark/pgo_workload_test.go)
#                      encvm compact: '^Benchmark_PGOWorkload_Compact$' (PGO-only
#                                     Marshal+WithStdCompat workload)
#                      ndec:          '^Benchmark_Unmarshal_.*_Velox$'
#                      An explicit value overrides every mode's default.
#   PGO_ITERS_FILE     Per-benchmark fixed iteration table (committed; the
#                      frozen profile weights). Default:
#                      scripts/pgo-bench-iters.txt. Every benchmark matched
#                      by the active filters must have a "Name=N" entry.
#   PGO_ITERS_REGEN    If 1, re-measure the matched benchmarks with the
#                      instrumented binary and update their table entries
#                      before collecting. Default: 0.
#   PGO_ITERS_TARGET   Regen calibration: per-benchmark target run time.
#                      Default: 6s (matches the historical 3s x count=2
#                      profile density).
#   PGO_ITERS_CAL      Regen measurement time per benchmark. Default: 0.5s.
#   PGO_EXTRA_BENCH_FILTER
#                      Optional second workload set, looked up in the same
#                      iteration table. To up-weight a single benchmark,
#                      raise its table entry instead. Default: empty, except
#                      ndec, which appends a Decoder+Valid run to cover the
#                      streaming engine and the valid entry.
#   PGO_KEEP_SYSO      If 1, leave the freshly built PGO blob in the tree.
#                      Default: 0 (the blob is a local, non-committed artifact).

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

MODULE="${MODULE:-encvm}"

# Per-module defaults: sources.sh, blob path, symbol name for size report,
# default MODES, default bench filter.
case "$MODULE" in
encvm)
  _sources_sh="native/encvm/sources.sh"
  _syso_dir="native/encvm"
  _syso_prefix="encvm"
  # encvm exports vj_vm_exec_<mode>; the blob is arch-canonical and merges
  # every mode, so the symbol carries no isa/os suffix.
  _symbol_fn() { echo "vj_vm_exec_${1}"; }
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

# Both modules are arch-canonical (SYSO_ARCH_ONLY): the blob is always
# linux-built, so the profile must come from a linux target. Collect on linux;
# any other TARGET_OS is invalid.
if [ "$TARGET_OS" != "linux" ]; then
  echo "pgo-collect-instr: $MODULE is arch-canonical (SYSO_ARCH_ONLY): its blob is" >&2
  echo "                   always linux-built, so the profile must come from a linux" >&2
  echo "                   target. Collect on linux; TARGET_OS=$TARGET_OS is invalid." >&2
  exit 1
fi

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

# Fixed-iteration helpers. The table maps a benchmark name to the exact
# iteration count the workload runs; a wall-clock budget here is what made
# collection nondeterministic (see the header note).
_iters_lookup() { # $1: benchmark name; prints N, fails when the table lacks it
  local _n
  _n=$(awk -F= -v n="$1" 'index($0, n "=") == 1 { print substr($0, length(n) + 2); exit }' \
    "$PGO_ITERS_FILE" 2>/dev/null)
  if [ -z "$_n" ] || [ "$_n" -lt 1 ] 2>/dev/null; then
    return 1
  fi
  printf '%s\n' "$_n"
}

# One benchmark for exactly N iterations; counters append to the shared
# profraw pattern. Exact counts make the merged profile machine-independent.
_run_bench_fixed() { # $1: name, $2: iterations
  LLVM_PROFILE_FILE="$(_pgo_profile_file)" \
    "$BENCH_TEST" -test.run='^$' -test.bench="^$1\$" \
    -test.benchtime="$2x" -test.count=1 >/dev/null
}

# -test.list for $1 (anchored regex), without littering profraw files.
# PGO_DATA_DIR resolves at call time: it is defined further down.
_list_benchmarks() { # $1: filter regex
  LLVM_PROFILE_FILE="$PGO_DATA_DIR/list-%p.profraw" "$BENCH_TEST" -test.list "$1"
}

# Run every benchmark whose -test.list name matches $1 (anchored regex) under
# the fixed-iteration table. A missing entry is a hard error: silently falling
# back to a default count would reintroduce weight drift.
_run_filter_fixed() { # $1: filter regex
  local _name _n _total=0
  while IFS= read -r _name; do
    [ -n "$_name" ] || continue
    if ! _n=$(_iters_lookup "$_name"); then
      echo "pgo-collect-instr: '$_name' has no entry in $PGO_ITERS_FILE" >&2
      echo "                   regenerate with: PGO_ITERS_REGEN=1 $0" >&2
      exit 1
    fi
    _run_bench_fixed "$_name" "$_n"
    _total=$((_total + 1))
    echo "    ran $_name x$_n"
  done < <(_list_benchmarks "$1")
  if [ "$_total" -eq 0 ]; then
    echo "pgo-collect-instr: filter '$1' matched no benchmarks" >&2
    exit 1
  fi
}

# Go duration string (s/ms/ns) to integer nanoseconds; 0 when unparsable.
_dur_ns() {
  echo "$1" | awk '{
    if ($0 ~ /^[0-9]+(\.[0-9]+)?ns$/) { printf "%d", substr($0, 1, length($0) - 2) }
    else if ($0 ~ /^[0-9]+(\.[0-9]+)?ms$/) { printf "%d", substr($0, 1, length($0) - 2) * 1e6 }
    else if ($0 ~ /^[0-9]+(\.[0-9]+)?s$/) { printf "%d", substr($0, 1, length($0) - 1) * 1e9 }
    else { print 0 }
  }'
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
PGO_ITERS_FILE="${PGO_ITERS_FILE:-$REPO_ROOT/scripts/pgo-bench-iters.txt}"
PGO_ITERS_REGEN="${PGO_ITERS_REGEN:-0}"
PGO_ITERS_TARGET="${PGO_ITERS_TARGET:-6s}"
PGO_ITERS_CAL="${PGO_ITERS_CAL:-0.5s}"
PGO_EXTRA_BENCH_FILTER="${PGO_EXTRA_BENCH_FILTER:-}"
PGO_KEEP_SYSO="${PGO_KEEP_SYSO:-0}"

# ndec default extra run: the Unmarshal filter never drives the feed-driver
# path (ndec_bind_parse_stream / ndec_window_scan) or ndec_valid, so their
# counters would stay zero and the PGO rebuild would codegen them cold. The
# Decoder and Valid suites cover both; counters accumulate across runs, so the
# merged profile carries every engine's real frequencies.
if [ "$MODULE" = ndec ] && [ -z "$PGO_EXTRA_BENCH_FILTER" ]; then
  PGO_EXTRA_BENCH_FILTER='^Benchmark_(Decoder|Valid)_.*_Velox$'
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

# Artifact path. Both modules ship one arch-canonical .elf per arch
# (SYSO_ARCH_ONLY, see each module's sources.sh), so the mode/isa/os
# segments collapse away; encvm's blob additionally merges every mode.
_blob_path() {
  echo "${_syso_dir}/${_syso_prefix}_${TARGET_ARCH}.elf"
}

_all_blob_paths() {
  echo "$(_blob_path)"
}

PGO_DATA_DIR="$REPO_ROOT/.local/pgo-data"
PROFDATA="$PGO_DATA_DIR/instr.profdata"
BENCH_TEST="$PGO_DATA_DIR/bench_pgo_instr.test"

# nm: llvm-nm substitutes for binutils nm.
NM="$(command -v nm 2>/dev/null || command -v llvm-nm 2>/dev/null || true)"

# Profile file pattern for LLVM_PROFILE_FILE.
_pgo_profile_file() {
  echo "$PGO_DATA_DIR/vj-%p.profraw"
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

# $1 = artifact path, $2 = entry symbol. The [[:space:]] anchor is load-bearing:
# sibling symbols like __profc_<name> (instrumentation counter arrays) also
# end in the entry name, and under --size-sort | head -1 the tiny counter
# symbol would shadow the real function body.
_blob_fn_size() {
  local _sz
  _sz=$("$NM" --print-size --size-sort "$1" 2>/dev/null |
    grep "[[:space:]]$2\$" | awk '{print $2}' | head -1 || true)
  if [ -n "$_sz" ] && [ "$_sz" != "0000000000000000" ]; then
    echo "0x$_sz"
    return
  fi
  echo "0x${_sz:-0}"
}

# Set after step 1: the renamed linkable instrumented artifact (see below).
# Referenced by _report_blob_sizes and the final cleanup.
INSTR_SYSO=""

# $1 = label; prints one "<label> <symbol> size: ..." line per mode.
# $2 = optional mode list (default: all of $MODES).
_report_blob_sizes() {
  local _label=$1 _modes="${2:-$MODES}" mode art sym
  for mode in $_modes; do
    art=$(_blob_path)
    # Instrumented artifact lives under the renamed .syso; the .elf
    # path only reappears at step 5.
    if [ -n "$INSTR_SYSO" ] && [ ! -f "$art" ]; then
      art="$INSTR_SYSO"
    fi
    sym=$(_symbol_fn "$mode" "$ISA")
    echo "    $_label $sym size: $(_blob_fn_size "$art" "$sym")"
  done
}

echo "==> pgo-collect-instr: module=$MODULE target=$TARGET_OS/$TARGET_ARCH isa=$ISA modes='$MODES'"
echo "    iters table: $PGO_ITERS_FILE (regen=$PGO_ITERS_REGEN)"
for _mode in $MODES; do
  echo "    bench[$_mode]='$(_mode_bench_filter "$_mode")' fixed iterations per benchmark"
done
if [ -n "$PGO_EXTRA_BENCH_FILTER" ]; then
  echo "    extra='$PGO_EXTRA_BENCH_FILTER' (after every mode)"
fi

# ------------------------------------------------------------------
# Tree-state guard (the collection counterpart of pgo-gate.sh's EXIT trap).
# From step 1 on, the working-tree blob is overwritten with the instrumented
# build and renamed to a .syso, and step 5 rebuilds the blob only on success,
# so an abort in between leaves the tree with a missing blob plus a stray
# _instr_.syso. Back up the pre-run blob and restore it on failure; the
# regenerated iteration table is deliberately kept, its calibration is valid
# whatever the collection outcome.
# ------------------------------------------------------------------
_ORIG_BLOB="$PGO_DATA_DIR/orig-${MODULE}-${TARGET_ARCH}.elf"
_COLLECT_OK=0
if [ -f "$(_blob_path)" ]; then
  cp "$(_blob_path)" "$_ORIG_BLOB"
fi
_collect_cleanup() {
  if [ "$_COLLECT_OK" = "1" ]; then
    rm -f "$_ORIG_BLOB"
    return
  fi
  if [ -n "$INSTR_SYSO" ]; then
    rm -f "$INSTR_SYSO"
  fi
  if [ -f "$_ORIG_BLOB" ]; then
    mv "$_ORIG_BLOB" "$(_blob_path)" || true
    echo "pgo-collect-instr: aborted; restored pre-collection $(_blob_path)" >&2
  elif [ -f "$(_blob_path)" ]; then
    rm -f "$(_blob_path)"
    echo "pgo-collect-instr: aborted; removed partial $(_blob_path) (none existed before the run)" >&2
  fi
}
trap _collect_cleanup EXIT

# ------------------------------------------------------------------
# Step 1: instrumented blob (--pgo-instr forces no-prelink internally)
#   MODES is deliberately NOT forwarded: the instrumented artifact must
#   carry EVERY mode's entry (the workload filters drive each of them), so
#   sources.sh defaults to the module's full mode set. env -u is required:
#   make exports MODES into this script (empty when unset, defaulted above),
#   and the reassignment keeps it exported, so a plain invocation would
#   leak the collection modes into gen-natives and shrink the blob to a
#   single mode, breaking the linked trampolines' symbol resolution.
# ------------------------------------------------------------------
echo "==> [1/5] building instrumented blob (-fprofile-instr-generate, no-prelink)"
env -u MODES scripts/gen-natives.sh --pgo-instr "$_sources_sh" "$TARGET_OS" "$TARGET_ARCH" >/dev/null

# The instrumented artifact is a relocatable (no-prelink) object carrying
# __llvm_prf_* sections that prelink cannot merge, so it must be linked by
# Go's linker instead of mapped by execblob. gen-natives still names it
# ${module}_${arch}.elf; rename it to .syso so the Go tool links it, and build
# the benchmark with the module's link tag so the linked trampolines replace
# the pointer-based ones (native/ndec/ndec_init_linked.go,
# native/encvm/encvm_init_linked.go).
INSTR_SYSO="native/${MODULE}/${MODULE}_instr_${TARGET_ARCH}.syso"
mv "native/${MODULE}/${MODULE}_${TARGET_ARCH}.elf" "$INSTR_SYSO"

_report_blob_sizes "instrumented blob"

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
# Linux defaults CC to gcc, so point it at the clang on PATH. A pre-set CC
# wins.
export CC="${CC:-clang}"
#   - -tags vj_<module>link: the instrumented artifact is linked
#     instead of execblob-mapped, so the linked trampolines must replace the
#     pointer-based ones (ndec_init_linked.go / encvm_init_linked.go).
_tags="vjpgoinstr,vj_${MODULE}link"
(cd benchmark && GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
  go test -c -tags "$_tags" -o "$BENCH_TEST" \
  -ldflags="-linkmode=external -extldflags=-fprofile-instr-generate" .)

# NOTE: do NOT pipe `nm | grep -q`. Under `set -o pipefail`, grep -q closes the
# pipe on first match and nm dies with SIGPIPE (141), which pipefail propagates
# as a pipeline failure, a false negative. Capture first, then match.
# Count matches with `grep -c` (consumes all input) rather than `grep -q`
# (exits on first match). Under `set -o pipefail`, an early-closing grep -q makes
# the upstream producer die with SIGPIPE and the pipeline reports failure, a
# false negative even when the symbol is present.
_rt_hits=$("$NM" "$BENCH_TEST" 2>/dev/null | grep -c "__llvm_profile_write_file" || true)
if [ "${_rt_hits:-0}" -eq 0 ]; then
  echo "pgo-collect-instr: benchmark binary is missing the profile runtime;" >&2
  echo "                   -extldflags=-fprofile-instr-generate did not take effect." >&2
  echo "                   (nm='$NM'; first lines of nm output:)" >&2
  "$NM" "$BENCH_TEST" 2>&1 | head -3 >&2 || true
  exit 1
fi

# ------------------------------------------------------------------
# Optional step 2b: regenerate the iteration-table entries for the active
# filter set. Calibration runs the INSTRUMENTED binary (that is what the
# workload will run), measuring each benchmark briefly and scaling to
# PGO_ITERS_TARGET. Calibration profraws are discarded: they carry short-run
# counts, not the real workload weights. The regenerated table is meant to be
# committed together with the blob it produced.
# ------------------------------------------------------------------
if [ "$PGO_ITERS_REGEN" = "1" ]; then
  echo "==> [2b/5] regenerating iteration entries (target=$PGO_ITERS_TARGET cal=$PGO_ITERS_CAL per benchmark)"
  _target_ns=$(_dur_ns "$PGO_ITERS_TARGET")
  if [ "$_target_ns" -le 0 ] 2>/dev/null; then
    echo "pgo-collect-instr: bad PGO_ITERS_TARGET '$PGO_ITERS_TARGET' (want e.g. 6s)" >&2
    exit 1
  fi

  _regen_bench="$_default_bench_filter"
  for _mode in $MODES; do
    _f="$(_mode_bench_filter "$_mode")"
    case "|$_regen_bench|" in
    *"|$_f|"*) ;;
    *) _regen_bench="$_regen_bench|$_f" ;;
    esac
  done
  if [ -n "$PGO_EXTRA_BENCH_FILTER" ]; then
    _regen_bench="$_regen_bench|$PGO_EXTRA_BENCH_FILTER"
  fi

  _cal_dir="$PGO_DATA_DIR/calib"
  mkdir -p "$_cal_dir"
  rm -f "$_cal_dir"/*.profraw
  touch "$PGO_ITERS_FILE"
  _tmp_table="$(mktemp)"
  cp "$PGO_ITERS_FILE" "$_tmp_table"

  while IFS= read -r _name; do
    [ -n "$_name" ] || continue
    _ns=$(LLVM_PROFILE_FILE="$_cal_dir/cal-%p.profraw" \
      "$BENCH_TEST" -test.run='^$' -test.bench="^$_name\$" \
      -test.benchtime="$PGO_ITERS_CAL" -test.count=1 |
      awk -v n="$_name" '$1 ~ "^" n "-" { for (i = 2; i < NF; i++) if ($(i + 1) == "ns/op") { print $i; exit } }')
    if [ -z "$_ns" ] || [ "$_ns" -le 0 ] 2>/dev/null; then
      echo "pgo-collect-instr: calibration of '$_name' produced no ns/op" >&2
      rm -f "$_tmp_table"
      exit 1
    fi
    _n=$(awk -v t="$_target_ns" -v ns="$_ns" 'BEGIN { n = t / ns + 0.5; if (n < 1) n = 1; printf "%d", n }')
    awk -v n="$_name" -v iters="$_n" '
      index($0, n "=") == 1 { print n "=" iters; found = 1; next }
      { print }
      END { if (!found) print n "=" iters }
    ' "$_tmp_table" > "$_tmp_table.new" && mv "$_tmp_table.new" "$_tmp_table"
    echo "    calibrated $_name: ${_ns}ns/op -> ${_n}x"
  done < <(_list_benchmarks "$_regen_bench")

  # Sorted entries make table diffs reviewable; the comment header stays.
  {
    grep '^#' "$_tmp_table" 2>/dev/null || true
    grep -v '^#' "$_tmp_table" 2>/dev/null | grep -v '^$' | sort -u || true
  } > "$PGO_ITERS_FILE"
  rm -f "$_tmp_table" "$_cal_dir"/*.profraw
  echo "    table updated: $PGO_ITERS_FILE"
fi

# ------------------------------------------------------------------
# Steps 3-4 run once PER MODE, sequentially; step 5 runs once at the end:
#   Step 3: run that mode's workload filter. The bench binary embeds every
#     mode's instrumented copy, but each filter drives exactly one VM (fast
#     suite -> plain Marshal; PGOWorkload_Full -> MarshalIndent;
#     PGOWorkload_Compact -> Marshal+WithStdCompat), so the other copies
#     stay at zero counts and merge away harmlessly.
#   Step 4: merge that mode's profraw -> instr.profdata (the shared fallback
#     path gen-natives reads), archiving a per-mode copy.
#   Step 5: rebuild the production blob ONCE. The blob merges every mode
#     (encvm) or is single-mode (ndec), so a per-mode rebuild is impossible;
#     gen-natives resolves each mode's own instr-<mode>.profdata instead.
#
# The extra workload set runs after each mode's main one. LLVM
# instrumentation counters are additive: every run contributes to the same
# merged profdata in step 4.
# ------------------------------------------------------------------
for mode in $MODES; do
  _bench_filter="$(_mode_bench_filter "$mode")"

  echo "==> [3/5] mode=$mode: running workload to collect counters"
  echo "    bench='$_bench_filter' (fixed iterations from $PGO_ITERS_FILE)"
  rm -f "$PGO_DATA_DIR"/vj-*.profraw
  _run_filter_fixed "$_bench_filter"

  if [ -n "$PGO_EXTRA_BENCH_FILTER" ]; then
    echo "==> [3b/5] mode=$mode: running extra workload (counters accumulate)"
    echo "         filter='$PGO_EXTRA_BENCH_FILTER'"
    _run_filter_fixed "$PGO_EXTRA_BENCH_FILTER"
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
done

# ----------------------------------------------------------------
# Step 5: production PGO blob (--pgo-instr-use, prelinked & self-
#         contained). MODES is deliberately NOT forwarded (env -u, see
#         step 1): sources.sh defaults to the module's full mode set, so
#         a partial collection (e.g. MODES=fast) still rebuilds every
#         mode's entry into the merged blob; uncollected modes compile
#         at baseline codegen.
# ----------------------------------------------------------------
echo "==> [5/5] rebuilding production blob with --pgo-instr-use"
env -u MODES scripts/gen-natives.sh --pgo-instr-use "$_sources_sh" "$TARGET_OS" "$TARGET_ARCH" >/dev/null
_report_blob_sizes "PGO blob"

# The renamed instrumented object was link-time scaffolding; it has no
# life outside this script.
rm -f "$INSTR_SYSO"
# Step 5 delivered the candidate blob; later steps cannot fail, and the
# EXIT trap clears the backup instead of restoring over the candidate.
_COLLECT_OK=1

echo ""
echo "Done. Artifacts (gitignored, under .local/pgo-data/):"
for mode in $MODES; do
  echo "  profile : $PGO_DATA_DIR/instr-$mode.profdata"
done
echo "  blob    : $(_blob_path)"
if [ "$PGO_KEEP_SYSO" != "1" ]; then
  echo ""
  echo "NOTE: instrumentation PGO blob is a LOCAL artifact and is NOT meant to be committed."
  echo "      To restore the committed blob:  git checkout -- $(_all_blob_paths)"
  echo "      (set PGO_KEEP_SYSO=1 to suppress this note)"
fi
