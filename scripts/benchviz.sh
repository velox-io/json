#!/usr/bin/env bash
# Benchmark visualization pipeline behind `make benchviz`.
#
# Runs one suite at a time (Unmarshal, Marshal) through scripts/bench.sh, then
# renders each with benchviz. Every run writes <suite>-<N>.txt and
# <suite>-<N>.svg into the output directory, where N is the next free index in
# that directory, so a run always produces a matched unmarshal/marshal pair and
# never overwrites an earlier one.
#
# Usage: scripts/benchviz.sh [options]
#   -b, --binary PATH   Precompiled benchmark binary (required)
#   -d, --dir DIR       Output directory (default: docs/benchmarks/<goos>-<goarch>)
#   -s, --suites LIST   Space-separated suite filters (default: "Unmarshal Marshal")
#   -l, --libs LIBS     Comma-separated libraries (default: bench.sh default)
#   -t, --benchtime T   -benchtime=T (default: 5s)
#   -c, --count N       -count=N (default: 2)
#   -x, --exclude RE    Regexp of group names to drop from the charts
#   --no-warmup         Skip the per-library warmup pass

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BENCH_DIR="$PROJECT_ROOT/benchmark"

BINARY=""
OUTDIR=""
SUITES="Unmarshal Marshal"
LIBS=""
BENCHTIME="5s"
COUNT=2
EXCLUDE=""
WARMUP=true

usage() {
    sed -n '2,18p' "$0" | sed 's/^# \?//'
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -b|--binary)    BINARY="$2";    shift 2 ;;
        -d|--dir)       OUTDIR="$2";    shift 2 ;;
        -s|--suites)    SUITES="$2";    shift 2 ;;
        -l|--libs)      LIBS="$2";      shift 2 ;;
        -t|--benchtime) BENCHTIME="$2"; shift 2 ;;
        -c|--count)     COUNT="$2";     shift 2 ;;
        -x|--exclude)   EXCLUDE="$2";   shift 2 ;;
        --no-warmup)    WARMUP=false;   shift ;;
        -h|--help)      usage 0 ;;
        *)              echo "Unknown option: $1" >&2; usage 1 ;;
    esac
done

if [[ -z "$BINARY" ]]; then
    echo "ERROR: -b/--binary is required" >&2
    usage 1
fi
if [[ "$BINARY" != /* ]]; then
    BINARY="$PROJECT_ROOT/$BINARY"
fi
if [[ ! -x "$BINARY" ]]; then
    echo "ERROR: benchmark binary not found: $BINARY" >&2
    exit 1
fi

if [[ -z "$OUTDIR" ]]; then
    OUTDIR="$PROJECT_ROOT/docs/benchmarks/$(go env GOOS)-$(go env GOARCH)"
fi
if [[ "$OUTDIR" != /* ]]; then
    OUTDIR="$PROJECT_ROOT/$OUTDIR"
fi
mkdir -p "$OUTDIR"

LIBS_ARG=()
if [[ -n "$LIBS" ]]; then
    LIBS_ARG=(-l "$LIBS")
fi

WARMUP_ARG=()
if $WARMUP; then
    WARMUP_ARG=(-w)
fi

EXCLUDE_ARG=()
if [[ -n "$EXCLUDE" ]]; then
    EXCLUDE_ARG=(-exclude "$EXCLUDE")
fi

# Next free run index: one past the highest <suite>-<N>.* already in the
# directory, so unmarshal and marshal from the same run share an index.
next_index() {
    local max=0 base n
    for f in "$OUTDIR"/*-*.txt; do
        [[ -e "$f" ]] || continue
        base="${f##*/}"
        base="${base%.txt}"
        n="${base##*-}"
        [[ "$n" =~ ^[0-9]+$ ]] || continue
        if (( 10#$n > max )); then
            max=$((10#$n))
        fi
    done
    echo $((max + 1))
}

IDX=$(next_index)

read -ra SUITE_ARRAY <<< "$SUITES"

for suite in "${SUITE_ARRAY[@]}"; do
    name="$(printf '%s' "$suite" | tr '[:upper:]' '[:lower:]')"
    txt="$OUTDIR/$name-$IDX.txt"
    svg="$OUTDIR/$name-$IDX.svg"

    echo "=== $suite: benchmarking (benchtime=$BENCHTIME count=$COUNT) -> $txt" >&2
    bash "$SCRIPT_DIR/bench.sh" -b "$BINARY" -f "$suite" -t "$BENCHTIME" -c "$COUNT" \
        ${WARMUP_ARG[@]+"${WARMUP_ARG[@]}"} ${LIBS_ARG[@]+"${LIBS_ARG[@]}"} -o "$txt" >/dev/null

    echo "=== $suite: rendering -> $svg" >&2
    (cd "$BENCH_DIR" && go run ./benchviz/ -format svg ${EXCLUDE_ARG[@]+"${EXCLUDE_ARG[@]}"} < "$txt" > "$svg")
done

echo "Results in: $OUTDIR ($IDX)" >&2
