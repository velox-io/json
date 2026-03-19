#!/usr/bin/env bash
# Isolated Benchmark Runner
#
# Runs each library's benchmarks in its own go test process to avoid
# cross-library interference (GC pressure, cache pollution, thermal throttling).
# Output is standard go test -bench format, compatible with benchviz pipeline.
#
# Usage: scripts/bench.sh [options]
#   -f, --filter PATTERN   Benchmark name filter regex (default: '.')
#   -l, --libs LIBS        Comma-separated libraries (default: all)
#   -c, --count N          -count=N for go test (default: 3)
#   -t, --benchtime T      -benchtime=T (default: go test default)
#   -b, --binary PATH      Use precompiled test binary instead of go test
#   -C, --cpu N            Pin to CPU core N (Linux only; macOS warns and skips)
#   -w, --warmup           Run a warmup pass per library before real measurement
#   -o, --output FILE      Write to file in addition to stdout (default: stdout only)
#   --no-benchmem          Disable -benchmem (omit B/op and allocs/op from output)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BENCH_DIR="$PROJECT_ROOT/benchmark"

# Defaults
FILTER='.'
LIBS="StdJSON,Sonic,GoJSON,EasyJSON,Velox"
COUNT=3
BENCHTIME="3s"
PIN_CPU=""
WARMUP=false
OUTPUT=""
BENCHMEM=true
BINARY=""

usage() {
    sed -n '2,17p' "$0" | sed 's/^# \?//'
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--filter)   FILTER="$2";   shift 2 ;;
        -l|--libs)     LIBS="$2";     shift 2 ;;
        -c|--count)    COUNT="$2";    shift 2 ;;
        -t|--benchtime) BENCHTIME="$2"; shift 2 ;;
        -b|--binary)   BINARY="$2";   shift 2 ;;
        -C|--cpu)      PIN_CPU="$2";  shift 2 ;;
        -w|--warmup)   WARMUP=true;   shift ;;
        -o|--output)   OUTPUT="$2";   shift 2 ;;
        --no-benchmem) BENCHMEM=false; shift ;;
        -h|--help)     usage 0 ;;
        *)             echo "Unknown option: $1" >&2; usage 1 ;;
    esac
done

# Build CPU pinning command prefix
PIN=""
if [[ -n "$PIN_CPU" ]]; then
    if [[ "$(uname -s)" == "Linux" ]]; then
        if command -v taskset &>/dev/null; then
            PIN="taskset -c $PIN_CPU"
        else
            echo "WARNING: taskset not found; skipping CPU pinning" >&2
        fi
    else
        echo "WARNING: CPU pinning not supported on $(uname -s); skipping" >&2
    fi
fi

# Split libs into array
IFS=',' read -ra LIB_ARRAY <<< "$LIBS"

# Temp file for collecting output
TMPFILE=$(mktemp)
trap "rm -f $TMPFILE" EXIT

# Resolve binary path (relative to project root)
if [[ -n "$BINARY" && "$BINARY" != /* ]]; then
    BINARY="$PROJECT_ROOT/$BINARY"
fi

# Build the run command: either precompiled binary or go test
if [[ -n "$BINARY" ]]; then
    if [[ ! -x "$BINARY" ]]; then
        echo "ERROR: binary not found or not executable: $BINARY" >&2
        exit 1
    fi
    run_bench() {
        $PIN "$BINARY" "$@"
    }
    # Binary uses -test.xxx flags
    FLAG_RUN="-test.run"
    FLAG_BENCH="-test.bench"
    FLAG_BENCHMEM="-test.benchmem"
    FLAG_COUNT="-test.count"
    FLAG_BENCHTIME="-test.benchtime"
else
    run_bench() {
        $PIN go test "$@"
    }
    FLAG_RUN="-run"
    FLAG_BENCH="-bench"
    FLAG_BENCHMEM="-benchmem"
    FLAG_COUNT="-count"
    FLAG_BENCHTIME="-benchtime"
    # cd into benchmark dir for go test mode
    cd "$BENCH_DIR"
fi

# Build benchmem arg with correct flag name
BENCHMEM_ARG=""
if $BENCHMEM; then
    BENCHMEM_ARG="$FLAG_BENCHMEM"
fi

# Build benchtime arg with correct flag name
BENCHTIME_ARG=""
if [[ -n "$BENCHTIME" ]]; then
    BENCHTIME_ARG="${FLAG_BENCHTIME}=${BENCHTIME}"
fi

# Build common args for go test mode (the trailing "." package arg)
DOT_ARG=""
if [[ -z "$BINARY" ]]; then
    DOT_ARG="."
fi

# Capture header (goos/goarch/pkg/cpu) from a quick dry run.
# For precompiled binary, we need to run at least one real benchmark to get the header
# (the binary only emits header lines when benchmarks actually match).
_header_bench="${FLAG_BENCH}=^$"
if [[ -n "$BINARY" ]]; then
    _header_bench="${FLAG_BENCH}=Benchmark_${FILTER}.*_${LIB_ARRAY[0]}\$"
fi
HEADER=$(run_bench "${FLAG_RUN}=^$" "$_header_bench" "${FLAG_BENCHTIME}=1x" $DOT_ARG "${FLAG_COUNT}=1" 2>&1 \
    | grep -E '^(goos:|goarch:|pkg:|cpu:)' || true)

# Write header once
{
    echo "$HEADER"
    echo ""
} >> "$TMPFILE"

for lib in "${LIB_ARRAY[@]}"; do
    # Construct bench regex: filter AND library suffix
    # Match: Benchmark_<filter>.*_<lib>$
    bench_re="Benchmark_${FILTER}.*_${lib}\$"

    echo "--- Running benchmarks for: $lib" >&2

    # Optional warmup (output discarded)
    if $WARMUP; then
        run_bench "${FLAG_RUN}=^$" "${FLAG_BENCH}=${bench_re}" "${FLAG_BENCHTIME}=100ms" "${FLAG_COUNT}=1" $DOT_ARG >/dev/null 2>&1 || true
    fi

    # Real run — capture only benchmark lines and PASS/ok lines
    run_bench "${FLAG_RUN}=^$" "${FLAG_BENCH}=${bench_re}" $BENCHMEM_ARG "${FLAG_COUNT}=${COUNT}" $BENCHTIME_ARG $DOT_ARG 2>&1 \
        | grep -E '^(Benchmark_|ok |PASS)' >> "$TMPFILE" || true

    echo "" >> "$TMPFILE"
done

# Output results
if [[ -n "$OUTPUT" ]]; then
    mkdir -p "$(dirname "$OUTPUT")"
    # Resolve relative paths against project root
    if [[ "$OUTPUT" != /* ]]; then
        OUTPUT="$PROJECT_ROOT/$OUTPUT"
    fi
    cp "$TMPFILE" "$OUTPUT"
    echo "Results written to: $OUTPUT" >&2
fi

cat "$TMPFILE"
