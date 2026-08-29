#!/usr/bin/env bash
# bench/run.sh: JSON DOM parse benchmark runner.
#
# Namespace groups:
#   tape  cross-engine: ndec_dom_tape vs simdjson vs yyjson vs sonic
#   dom   ndec flavor:  ndec_dom_tape{,_zc} comparison
#   s1    stage1:       ndec_dom_s1 vs simdjson_dom_s1
#
# Format selector (--compact or --pretty):
#   --compact   run on compact JSON files only (*.c.json)   (default)
#   --pretty    run on pretty JSON files only (*.json)
#
# Pins to a fixed CPU via taskset (default core 2).
#
# Format selection (positional; --compact is the default):
#   bench/run.sh --compact          # compact JSON only (*.c.json, default)
#   bench/run.sh --pretty           # pretty JSON only (*.json)
#   bench/run.sh --pretty tape      # pretty JSON, tape namespace only
#
# Env overrides:
#   CORE          CPU to pin to                  (default: 2)
#   ITERS         precision scale factor         (default: 1)
#                 multiplies the target byte budget per measurement;
#                 each file's iter count is recomputed from the scaled
#                 target. ITERS=2 doubles precision (and wall time);
#                 ITERS=0.5 halves it for a quick sanity check.
#   RUNS          repeat N times                 (default: 3)
#   TESTDATA      directory of JSON files (default: build/data, auto-
#                 materialized from the shared corpus via `make data`;
#                 set it to serve a custom directory)
#   FILTER        regex matching dataset basenames
#   TARGET_BYTES  base byte budget per measurement, before ITERS scale
#                 (default: 60_000_000; s1 namespace uses 3x this)
#   MIN_ITERS     lower clamp on per-file iter count   (default: 500)
#   MAX_ITERS     upper clamp on per-file iter count   (default: 50000)
#
# Per-file iter count = clamp(MIN_ITERS, MAX_ITERS,
#                             TARGET_BYTES * ITERS / file_size).
# A single global iter count either jitters on small files (escape_heavy
# at 2.8KB needs ~20K iters to stabilize) or wastes time on the large
# ones (citm at 500KB is stable at a few hundred). To pin every file to
# the same iter count for reproducibility, set MIN_ITERS == MAX_ITERS.
#
# Usage:
#   bench/run.sh [tape|dom|s1]      # run one group
#   bench/run.sh                    # run all groups
#   bench/run.sh dom,s1             # run multiple
#   FILTER=tape:citm_catalog bench/run.sh   # filter namespace:pattern
#   ITERS=2 bench/run.sh tape       # double-precision pass (slower)
#   ITERS=0.5 bench/run.sh          # quick sanity check
#   MIN_ITERS=10000 MAX_ITERS=10000 bench/run.sh   # pin all to 10K
#
# Pre-req: bench/build.sh must have produced the required binaries.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NDEC_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="${BUILD_DIR:-$NDEC_DIR/build}"

: "${CORE:=2}"
: "${RUNS:=3}"
: "${TESTDATA:=$NDEC_DIR/build/data}"
: "${FILTER:=}"

# Materialize the default dataset directory from the corpus on demand;
# a user-supplied TESTDATA is served as-is.
if [[ "$TESTDATA" == "$NDEC_DIR/build/data" ]] && ! compgen -G "$TESTDATA/*.json" >/dev/null; then
  echo "materializing corpus into $TESTDATA ..." >&2
  make -C "$NDEC_DIR" data >&2 || { echo "error: corpus materialization failed" >&2; exit 1; }
fi

# JSON_EXT selects the file pattern: .c.json (compact, default) or .json
# (pretty). Set via --compact or --pretty; --compact is the default.
JSON_EXT=".c.json"

# Parse --compact / --pretty (positional) and strip them from $@.
while [[ $# -gt 0 ]]; do
  case "$1" in
    --compact) JSON_EXT=".c.json"; shift ;;
    --pretty)  JSON_EXT=".json"; shift ;;
    *) break ;;
  esac
done

# Per-file iter count = clamp(MIN, MAX, TARGET_BYTES * ITERS_SCALE / size).
# ITERS is the user-facing precision multiplier (default 1). It scales
# the byte budget rather than overriding it, so every file scales
# proportionally. To pin every file to a single iter count, set
# MIN_ITERS == MAX_ITERS.
: "${TARGET_BYTES:=60000000}"
: "${MIN_ITERS:=500}"
: "${MAX_ITERS:=50000}"
ITERS_SCALE="${ITERS:-1}"

# FILTER supports namespace prefix: FILTER=tape:pattern
# The prefix selects the namespace to run, pattern filters datasets.
filter_ns=""; filter_re=""
if [[ -n "$FILTER" ]]; then
  if [[ "$FILTER" == *:* ]]; then
    filter_ns="${FILTER%%:*}"
    filter_re="${FILTER#*:}"
  else
    filter_re="$FILTER"
  fi
fi

# --- helpers ------------------------------------------------------------

# Per-file iter count. base is the namespace's nominal byte budget
# (TARGET_BYTES for tape/dom, 3x for s1); ITERS_SCALE multiplies it.
# Returns clamp(MIN_ITERS, MAX_ITERS, base * scale / file_size). The
# multiplication is done in awk so ITERS_SCALE can be fractional
# (e.g. 0.5 for a quick run, 2 for a high-precision pass).
auto_iters() {
  local file="$1" base="${2:-$TARGET_BYTES}"
  local size
  size="$(wc -c < "$file" | tr -d ' ')"
  awk -v b="$base" -v s="$ITERS_SCALE" -v sz="$size" \
      -v lo="$MIN_ITERS" -v hi="$MAX_ITERS" 'BEGIN{
    n = int(b * s / (sz > 0 ? sz : 1))
    if (n < lo) n = lo
    if (n > hi) n = hi
    print n
  }'
}

parse_gb() { awk '/GB\/s/{print $(NF-1); exit}'; }
parse_gb_at() {
  # extract GB/s from a line containing $1 substring
  awk -v want="$1" 'index($0, want) { for (i=1;i<=NF;i++) if ($i=="GB/s") { print $(i-1); exit } }'
}

median() {
  local n=$#
  printf '%s\n' "$@" | sort -n | awk -v n="$n" 'NR == int((n+1)/2) {print}'
}

run_one() {
  local bin="$1" file="$2"
  env ITERS="$ITERS" "${TASKSET[@]+"${TASKSET[@]}"}" "$bin" "$file" 2>/dev/null | parse_gb
}

run_one_at() {
  local bin="$1" file="$2" label="$3"
  env ITERS="$ITERS" "${TASKSET[@]+"${TASKSET[@]}"}" "$bin" "$file" 2>/dev/null | parse_gb_at "$label"
}

pct() {
  awk -v a="$1" -v b="$2" 'BEGIN{ if (b>0) printf "%+.0f%%", 100*(a-b)/b; else print "?" }'
}

# --- preamble -----------------------------------------------------------

if ! command -v taskset >/dev/null 2>&1; then
  TASKSET=()
else
  TASKSET=(taskset -c "$CORE")
fi

if [[ ! -d "$TESTDATA" ]]; then
  echo "error: testdata dir not found: $TESTDATA" >&2
  exit 1
fi

# collect files (shared across namespaces).
# With --pretty, *.json would also match *.c.json; only filter when asking
# for pretty JSON.
files=()
for f in "${TESTDATA}"/*"$JSON_EXT"; do
  [[ -f "$f" ]] || continue
  if [[ "$JSON_EXT" == ".json" ]]; then
    case "$f" in *.c.json) continue ;; esac
    name="$(basename "$f" ".json")"
  else
    # compact: strip .json but keep the .c suffix so the dataset column
    # reads canada_geometry.c instead of canada_geometry.
    name="$(basename "$f" ".json")"
  fi
  if [[ -n "$filter_re" ]] && ! [[ "$name" =~ $filter_re ]]; then
    continue
  fi
  files+=("$f")
done

if [[ ${#files[@]} -eq 0 ]]; then
  echo "error: no matching datasets in $TESTDATA" >&2
  exit 1
fi

printf 'CORE=%s RUNS=%s TESTDATA=%s\n' "$CORE" "$RUNS" "$TESTDATA"

# --- namespace runners --------------------------------------------------

check_bin() {
  for b in "$@"; do
    if [[ ! -x "$b" ]]; then
      echo "  skip: $b not found" >&2
      return 1
    fi
  done
}

run_tape() {
  local ndec="$BUILD_DIR/ndec_dom_tape"
  local simd="$BUILD_DIR/simdjson_dom_tape"
  local yyjson="$BUILD_DIR/yyjson"
  local sonic="$BUILD_DIR/sonic"

  if ! check_bin "$ndec" "$simd" "$yyjson" "$sonic"; then
    echo "  run: bench/build.sh && bench/build.sh comparison" >&2
    return
  fi

  printf '\n=== tape (cross-engine: ndec vs simdjson vs yyjson vs sonic) ===\n'
  printf 'ITERS=%s (target ~%s MB, clamp [%s, %s])\n\n' \
         "$ITERS_SCALE" "$((TARGET_BYTES / 1000000))" "$MIN_ITERS" "$MAX_ITERS"
  printf '%-20s | %5s | %8s GB/s | %8s GB/s | %8s GB/s | %8s GB/s | %10s | %10s\n' \
         'Dataset' 'iters' 'ndec' 'simd' 'yyjson' 'sonic' 'vs simd' 'vs yyjson'
  echo    '-------------------- | ----- | ------------- | ------------- | ------------- | ------------- | ---------- | ----------'

  for f in "${files[@]}"; do
    name="$(basename "$f" ".json")"
    ITERS="$(auto_iters "$f")"
    ndec_samples=""; simd_samples=""; yyjson_samples=""; sonic_samples=""
    for ((i = 0; i < RUNS; i++)); do
      ndec_samples+=" $(run_one "$ndec" "$f")"
      simd_samples+=" $(run_one "$simd" "$f")"
      yyjson_samples+=" $(run_one "$yyjson" "$f")"
      sonic_samples+=" $(run_one "$sonic" "$f")"
    done
    read -ra n_arr <<< "$ndec_samples"
    read -ra s_arr <<< "$simd_samples"
    read -ra y_arr <<< "$yyjson_samples"
    read -ra o_arr <<< "$sonic_samples"
    ndec_v="$(median "${n_arr[@]}")"
    simd_v="$(median "${s_arr[@]}")"
    yyjson_v="$(median "${y_arr[@]}")"
    sonic_v="$(median "${o_arr[@]}")"
    diff_s="$(pct "$ndec_v" "$simd_v")"
    diff_y="$(pct "$ndec_v" "$yyjson_v")"
    printf '%-20s | %5s | %8s GB/s | %8s GB/s | %8s GB/s | %8s GB/s | %10s | %10s\n' \
           "$name" "$ITERS" "$ndec_v" "$simd_v" "$yyjson_v" "$sonic_v" "$diff_s" "$diff_y"
  done
}

run_ndec_dom() {
  local bin="$BUILD_DIR/ndec_dom_tape"

  if ! check_bin "$bin"; then
    echo "  run: bench/build.sh ndec_dom_tape" >&2
    return
  fi

  printf '\n=== dom (ndec_dom flavor comparison, e2e) ===\n'
  printf 'ITERS=%s (target ~%s MB, clamp [%s, %s])\n\n' \
         "$ITERS_SCALE" "$((TARGET_BYTES / 1000000))" "$MIN_ITERS" "$MAX_ITERS"
  printf '%-20s | %5s | %8s GB/s | %8s GB/s | %8s\n' \
         'Dataset' 'iters' 'tape' 'zc' 'zc/tape'
  echo    '-------------------- | ----- | ------------- | ------------- | --------'

  for f in "${files[@]}"; do
    name="$(basename "$f" ".json")"
    ITERS="$(auto_iters "$f")"
    tape_samples=""; zc_samples=""
    for ((i = 0; i < RUNS; i++)); do
      tape_samples+=" $(run_one_at "$bin" "$f" "dom (copy)")"
      zc_samples+=" $(run_one_at "$bin" "$f" "dom (zc)")"
    done
    read -ra t_arr <<< "$tape_samples"
    read -ra z_arr <<< "$zc_samples"
    tape_v="$(median "${t_arr[@]}")"
    zc_v="$(median "${z_arr[@]}")"
    printf '%-20s | %5s | %8s GB/s | %8s GB/s | %8s\n' \
           "$name" "$ITERS" "$tape_v" "$zc_v" "$(pct "$zc_v" "$tape_v")"
  done
}

run_s1() {
  local ndec="$BUILD_DIR/ndec_dom_s1"
  local simd="$BUILD_DIR/simdjson_dom_s1"

  if ! check_bin "$ndec" "$simd"; then
    echo "  run: bench/build.sh ndec_dom_s1 && bench/build.sh simdjson_dom_s1" >&2
    return
  fi

  # stage1 is roughly 3x faster than full tape build, so it needs ~3x
  # more bytes processed per measurement to land in the same wall-clock
  # range and average out timer jitter.
  local s1_target=$((TARGET_BYTES * 3))

  printf '\n=== s1 (stage1: ndec_dom_s1 vs simdjson_dom_s1) ===\n'
  printf 'ITERS=%s (target ~%s MB, clamp [%s, %s])\n\n' \
         "$ITERS_SCALE" "$((s1_target / 1000000))" "$MIN_ITERS" "$MAX_ITERS"
  printf '%-20s | %5s | %8s GB/s | %8s GB/s | %s\n' \
         'Dataset' 'iters' 'ndec_s1' 'simd_s1' 'ndec vs simd'
  echo    '-------------------- | ----- | ------------- | ------------- | -----------'

  for f in "${files[@]}"; do
    name="$(basename "$f" ".json")"
    ITERS="$(auto_iters "$f" "$s1_target")"
    ndec_samples=""; simd_samples=""
    for ((i = 0; i < RUNS; i++)); do
      ndec_samples+=" $(run_one "$ndec" "$f")"
      simd_samples+=" $(run_one "$simd" "$f")"
    done
    read -ra n_arr <<< "$ndec_samples"
    read -ra s_arr <<< "$simd_samples"
    ndec_v="$(median "${n_arr[@]}")"
    simd_v="$(median "${s_arr[@]}")"
    printf '%-20s | %5s | %8s GB/s | %8s GB/s | %s\n' \
           "$name" "$ITERS" "$ndec_v" "$simd_v" "$(pct "$ndec_v" "$simd_v")"
  done
}

# --- dispatch -----------------------------------------------------------

ns="${filter_ns:-${1:-tape,dom,s1}}"
IFS=',' read -ra groups <<< "$ns"
for g in "${groups[@]}"; do
  case "$g" in
    tape) run_tape ;;
    dom)  run_ndec_dom ;;
    s1)   run_s1 ;;
    *)    echo "unknown namespace: $g (expect tape, dom, s1)" >&2; exit 2 ;;
  esac
done

echo
echo "done."
