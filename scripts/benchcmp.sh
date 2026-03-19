#!/usr/bin/env bash
# Compare benchmarks between libraries using benchstat.
#
# Runs each library in isolation via bench.sh, strips library suffixes from
# benchmark names so benchstat can match them, then prints the comparison.
#
# Usage: scripts/benchcmp.sh [options] LIB1 LIB2 [LIB3 ...]
#   -f, --filter PATTERN   Benchmark name filter regex (default: '.')
#   -c, --count N          -count=N for go test (default: 6)
#   -t, --benchtime T      -benchtime=T (default: bench.sh default)
#   -b, --binary PATH      Use precompiled test binary instead of go test
#   -w, --warmup           Run warmup pass per library
#   -C, --cpu N            Pin to CPU core N (Linux only)
#   -m, --benchmem         Include B/op and allocs/op (default: off)
#   -o, --output FILE      Save benchstat output to file
#
# Examples:
#   scripts/benchcmp.sh Velox StdJSON
#   scripts/benchcmp.sh -f Marshal -c 5 -w Velox Sonic StdJSON
#   scripts/benchcmp.sh Velox Sonic GoJSON EasyJSON StdJSON

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BENCH_SH="$SCRIPT_DIR/bench.sh"

# Defaults
FILTER='.'
COUNT=6
BENCHTIME_ARG=""
WARMUP_ARG=""
CPU_ARG=""
BENCHMEM_ARG="--no-benchmem"
BINARY_ARG=""
OUTPUT=""

usage() {
    sed -n '2,18p' "$0" | sed 's/^# \?//'
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--filter)    FILTER="$2";          shift 2 ;;
        -c|--count)     COUNT="$2";           shift 2 ;;
        -t|--benchtime) BENCHTIME_ARG="-t $2"; shift 2 ;;
        -b|--binary)    BINARY_ARG="-b $2";   shift 2 ;;
        -w|--warmup)    WARMUP_ARG="-w";      shift ;;
        -C|--cpu)       CPU_ARG="-C $2";      shift 2 ;;
        -m|--benchmem)  BENCHMEM_ARG="";      shift ;;
        -o|--output)    OUTPUT="$2";          shift 2 ;;
        -h|--help)      usage 0 ;;
        -*)             echo "Unknown option: $1" >&2; usage 1 ;;
        *)              break ;;
    esac
done

if [[ $# -lt 2 ]]; then
    echo "ERROR: at least two library names required" >&2
    echo "" >&2
    usage 1
fi

LIBS=("$@")

# Ensure benchstat is installed
if ! command -v benchstat &>/dev/null; then
    echo "Installing benchstat..." >&2
    go install golang.org/x/perf/cmd/benchstat@latest
fi

# Temp dir for per-library results
TMPDIR_CMP=$(mktemp -d)
trap "rm -rf $TMPDIR_CMP" EXIT

# Strip library suffix from benchmark names so benchstat can match them.
# e.g. "Benchmark_Marshal_Tiny_Velox" -> "Benchmark_Marshal_Tiny"
strip_lib_suffix() {
    local lib="$1"
    sed "s/_${lib}\b//"
}

# Run each library and collect results
BENCHSTAT_ARGS=()
for lib in "${LIBS[@]}"; do
    outfile="$TMPDIR_CMP/$lib.txt"

    bash "$BENCH_SH" -f "$FILTER" -l "$lib" -c "$COUNT" $BENCHTIME_ARG $WARMUP_ARG $CPU_ARG $BENCHMEM_ARG $BINARY_ARG \
        | strip_lib_suffix "$lib" > "$outfile"

    BENCHSTAT_ARGS+=("$lib=$outfile")
done

echo "" >&2
if [[ -n "$OUTPUT" ]]; then
    # Resolve relative paths against project root
    if [[ "$OUTPUT" != /* ]]; then
        OUTPUT="$(cd "$SCRIPT_DIR/.." && pwd)/$OUTPUT"
    fi
    mkdir -p "$(dirname "$OUTPUT")"
    benchstat "${BENCHSTAT_ARGS[@]}" | tee "$OUTPUT"
    echo "Results saved to: $OUTPUT" >&2
else
    benchstat "${BENCHSTAT_ARGS[@]}"
fi
