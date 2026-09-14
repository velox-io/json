#!/usr/bin/env bash
#
# Pre-commit performance gate for PGO blobs.
#
# Compares the working-tree blob (the freshly collected candidate) against the
# committed one (HEAD) by benching the module's workload suite twice: baseline
# first (HEAD blob), then candidate (tree restored). Fails when any single
# benchmark regresses more than PGO_GATE_TOL, or the geometric mean more than
# PGO_GATE_GEOMEAN_TOL. A geomean-only check would miss the incident this gate
# exists for: one benchmark sliding 2% while the rest hold flat moves the
# geomean by a rounding error.
#
# Blobs are arch-canonical (one .elf per arch) and the bench binary go:embeds
# the one matching its GOARCH, so only the HOST-arch blob is exercised here;
# a differing other-arch blob is reported but must be gated on a host of its
# own arch.
#
# The candidate is an UNCOMMITTED artifact: the tree is restored exactly as
# found, even on failure or interruption (EXIT trap).
#
# Usage:
#   make pgo-gate MODULE=ndec
#   make pgo-gate MODULE=encvm
#
# Environment:
#   MODULE                Which native module to gate (ndec/encvm). Required.
#   PGO_GATE_BENCH        -test.bench regex. Default: the module's full
#                         workload union (main + extra collection filters).
#   PGO_GATE_BENCHTIME    Per benchmark. Default 10s (shorter runs can
#                         invert conclusions).
#   PGO_GATE_COUNT        -test.count. Default 1.
#   PGO_GATE_TOL          Per-benchmark regression tolerance (fraction).
#                         Default 0.025.
#   PGO_GATE_GEOMEAN_TOL  Geomean regression tolerance (fraction).
#                         Default 0.01.
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

MODULE="${MODULE:-}"
case "$MODULE" in
ndec)
  _blob_dir="native/ndec"
  _blob_prefix="ndec"
  _default_bench='^Benchmark_(Unmarshal|Decoder|Valid)_.*_Velox$'
  ;;
encvm)
  _blob_dir="native/encvm"
  _blob_prefix="encvm"
  _default_bench='^Benchmark_(Marshal)_.*_Velox$|^Benchmark_PGOWorkload_(Full|Compact)$'
  ;;
*)
  echo "pgo-gate: MODULE is required (ndec or encvm)" >&2
  exit 1
  ;;
esac
PGO_GATE_BENCH="${PGO_GATE_BENCH:-$_default_bench}"
PGO_GATE_BENCHTIME="${PGO_GATE_BENCHTIME:-10s}"
PGO_GATE_COUNT="${PGO_GATE_COUNT:-1}"
PGO_GATE_TOL="${PGO_GATE_TOL:-0.025}"
PGO_GATE_GEOMEAN_TOL="${PGO_GATE_GEOMEAN_TOL:-0.01}"

_host_arch=$(uname -m)
case "$_host_arch" in
x86_64 | amd64) _host_arch=amd64 ;;
arm64 | aarch64) _host_arch=arm64 ;;
*)
  echo "pgo-gate: unknown host arch '$_host_arch' (expected amd64/arm64)" >&2
  exit 1
  ;;
esac

_need() { command -v "$1" >/dev/null 2>&1 || {
  echo "pgo-gate: missing required tool: $1" >&2
  exit 1
}; }
_need go
_need git

# Candidate set: every tracked module blob of the HOST arch whose tree content
# differs from HEAD (that is the blob the bench binary embeds and exercises).
# Untracked blob files have no baseline to compare against.
declare -a CANDIDATES=()
for _arch in amd64 arm64; do
  _path="${_blob_dir}/${_blob_prefix}_${_arch}.elf"
  [ -f "$_path" ] || continue
  if ! git cat-file -e "HEAD:$_path" 2>/dev/null; then
    echo "pgo-gate: note: $_path has no committed baseline; skipping it" >&2
    continue
  fi
  if git diff --quiet HEAD -- "$_path"; then
    continue
  fi
  if [ "$_arch" = "$_host_arch" ]; then
    CANDIDATES+=("$_path")
  else
    echo "pgo-gate: note: $_path differs from HEAD but this host cannot bench it" >&2
    echo "pgo-gate:       (go:embed only exercises ${_blob_prefix}_${_host_arch}.elf);" >&2
    echo "pgo-gate:       gate it on a $_arch host before committing." >&2
  fi
done

if [ "${#CANDIDATES[@]}" -eq 0 ]; then
  echo "pgo-gate: no $MODULE blob differs from HEAD; nothing to gate."
  exit 0
fi

_BACKUP_DIR="$(mktemp -d)"
_RESTORED=0
_cleanup() {
  if [ "$_RESTORED" -ne 1 ]; then
    local _p
    for _p in "${CANDIDATES[@]}"; do
      cp "$_BACKUP_DIR/$(basename "$_p")" "$_p" 2>/dev/null || true
    done
    echo "pgo-gate: restored candidate blob (interrupted)" >&2
  fi
  rm -rf "$_BACKUP_DIR"
}
trap _cleanup EXIT

for _p in "${CANDIDATES[@]}"; do
  cp "$_p" "$_BACKUP_DIR/$(basename "$_p")"
  echo "pgo-gate: candidate $_p"
done

# ns/op per benchmark, averaged over -count runs: "Name-GOMAXPROCS iters ns ns/op"
# becomes "Name ns".
_parse_results() { # $1: output file
  awk '
    /^Benchmark_/ {
      name = $1; sub(/-[0-9]+$/, "", name)
      for (i = 2; i < NF; i++)
        if ($(i + 1) == "ns/op") { sum[name] += $i; cnt[name]++; break }
    }
    END { for (k in sum) print k, sum[k] / cnt[k] }
  ' "$1"
}

_run_suite() { # $1: output file
  (cd benchmark && go test -run='^$' -bench="$PGO_GATE_BENCH" \
    -benchtime="$PGO_GATE_BENCHTIME" -count="$PGO_GATE_COUNT") | tee "$1"
  if ! grep -q '^Benchmark_' "$1"; then
    echo "pgo-gate: no benchmark results in $1 (filter matched nothing?)" >&2
    exit 1
  fi
}

echo "==> bench='$PGO_GATE_BENCH' benchtime=$PGO_GATE_BENCHTIME count=$PGO_GATE_COUNT"

# Baseline first, candidate second: whatever thermal or cache drift the
# second run suffers biases against the candidate, which is the safe
# direction for a gate.
echo "==> [1/3] benching baseline (HEAD blob)"
for _p in "${CANDIDATES[@]}"; do git show "HEAD:$_p" > "$_p"; done
_run_suite "$_BACKUP_DIR/base.txt"

echo "==> [2/3] benching candidate (working tree)"
for _p in "${CANDIDATES[@]}"; do cp "$_BACKUP_DIR/$(basename "$_p")" "$_p"; done
_RESTORED=1
_run_suite "$_BACKUP_DIR/cand.txt"

echo "==> [3/3] comparing"
_parse_results "$_BACKUP_DIR/base.txt" > "$_BACKUP_DIR/base.parsed"
_parse_results "$_BACKUP_DIR/cand.txt" > "$_BACKUP_DIR/cand.parsed"

if ! awk -v tol="$PGO_GATE_TOL" -v gtol="$PGO_GATE_GEOMEAN_TOL" '
  NR == FNR { base[$1] = $2; next }
  { cand[$1] = $2 }
  END {
    bad = 0; n = 0; logsum = 0
    printf "%-58s %13s %13s %9s\n", "benchmark", "base ns/op", "cand ns/op", "delta"
    for (k in cand) {
      if (!(k in base)) {
        printf "%-58s %13s %13.1f %9s  NOT IN BASELINE\n", k, "-", cand[k], "-"
        bad = 1; continue
      }
      n++; r = cand[k] / base[k]; logsum += log(r)
      flag = (r > 1 + tol) ? "  REGRESSION" : ""
      if (r > 1 + tol) bad = 1
      printf "%-58s %13.1f %13.1f %+8.2f%%%s\n", k, base[k], cand[k], (r - 1) * 100, flag
    }
    for (k in base)
      if (!(k in cand)) { printf "%-58s NOT IN CANDIDATE\n", k; bad = 1 }
    if (n > 0) {
      geo = exp(logsum / n)
      printf "%-58s %13s %13s %+8.2f%%  (tolerance %0.2f%%)\n", "geomean (" n ")", "", "", (geo - 1) * 100, gtol * 100
      if (geo > 1 + gtol) { print "geomean regression exceeds tolerance"; bad = 1 }
    }
    exit bad
  }
' "$_BACKUP_DIR/base.parsed" "$_BACKUP_DIR/cand.parsed"; then
  echo "" >&2
  echo "pgo-gate: FAILED: the candidate blob regresses the workload." >&2
  echo "          re-collect (make pgo-instr-collect MODULE=$MODULE)," >&2
  echo "          or accept the trade explicitly with PGO_GATE_TOL=<fraction>." >&2
  exit 1
fi
echo "pgo-gate: PASS"
