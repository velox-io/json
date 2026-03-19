#!/usr/bin/env bash
# Two-phase benchmark runner.
#
# Phase 1 — Collect: run benchmarks, save raw data (no extra dependencies).
# Phase 2 — Analyze: run benchstat/benchviz on collected data (needs Go toolchain).
#
# Automatically selects local/bin/vjson-benchmark_${os}_${arch}.
# Each run creates a timestamped directory: local/benchdata/YYYYMMDD-HHMM/
#
# Usage:
#   scripts/bench-run.sh                          Collect full benchmark suite
#   scripts/bench-run.sh [bench.sh options]       Collect with custom options (pass-through)
#   scripts/bench-run.sh --analyze [DIR]          Analyze collected data in DIR (default: latest)
#
# Examples:
#   scripts/bench-run.sh                          # collect on remote machine
#   scripts/bench-run.sh -f Marshal_Tiny -c 3     # collect subset
#   scripts/bench-run.sh --analyze                # analyze latest run (on dev machine)
#   scripts/bench-run.sh --analyze local/benchdata/20260315-1423

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

BENCH_SH="$SCRIPT_DIR/bench.sh"

# --- Suite configuration (override via environment) ---
BENCH_COUNT="${BENCH_COUNT:-5}"
BENCH_TIME="${BENCH_TIME:-3s}"
BENCH_CMP_LIBS="${BENCH_CMP_LIBS:-Velox StdJSON Sonic}"
BENCH_FILTER_MARSHAL="${BENCH_FILTER_MARSHAL:-(Marshal)_(Tiny|Small|EscapeHeavy|KubePods|Twitter|MapAny)}"
BENCH_FILTER_UNMARSHAL="${BENCH_FILTER_UNMARSHAL:-(Unmarshal)_(Tiny|Small|EscapeHeavy|KubePods|Twitter|MapAny)}"

# --- helpers ---

detect_binary() {
    local os arch binary
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    case "$arch" in
        x86_64)  arch=amd64 ;;
        aarch64) arch=arm64 ;;
    esac
    binary="$PROJECT_ROOT/local/bin/vjson-benchmark_${os}_${arch}"
    if [[ ! -x "$binary" ]]; then
        echo "ERROR: benchmark binary not found: $binary" >&2
        echo "Available binaries:" >&2
        ls "$PROJECT_ROOT"/local/bin/vjson-benchmark_* 2>/dev/null | sed 's/^/  /' >&2 || echo "  (none)" >&2
        exit 1
    fi
    echo "$binary"
}

# Strip library suffix from benchmark names for benchstat matching.
# e.g. "Benchmark_Marshal_Tiny_Velox" -> "Benchmark_Marshal_Tiny"
strip_lib_suffix() {
    local lib="$1"
    sed "s/_${lib}\b//"
}

find_latest_datadir() {
    local latest
    latest=$(ls -d local/benchdata/*/ 2>/dev/null | sort | tail -1)
    if [[ -z "$latest" ]]; then
        echo "ERROR: no benchmark data found in local/benchdata/" >&2
        exit 1
    fi
    echo "${latest%/}"
}

# --- Phase 2: Analyze ---

do_analyze() {
    local datadir="${1:-}"
    if [[ -z "$datadir" ]]; then
        datadir=$(find_latest_datadir)
    fi
    # Resolve relative to project root
    if [[ "$datadir" != /* ]]; then
        datadir="$PROJECT_ROOT/$datadir"
    fi
    if [[ ! -d "$datadir" ]]; then
        echo "ERROR: directory not found: $datadir" >&2
        exit 1
    fi

    echo "Analyzing: $datadir" >&2

    # Generate benchviz HTML/SVG from all.txt
    if [[ -f "$datadir/benchmark.txt" ]]; then
        local benchviz_dir="$PROJECT_ROOT/benchmark/benchviz"
        echo "--- benchviz ---" >&2
        (cd "$PROJECT_ROOT/benchmark" && go run ./benchviz/ -title 'Benchmark Results' -format html < "$datadir/benchmark.txt" > "$datadir/benchmark.html")
        (cd "$PROJECT_ROOT/benchmark" && go run ./benchviz/ -title 'Benchmark Results' -format svg  < "$datadir/benchmark.txt" > "$datadir/benchmark.svg")
    fi

    # Ensure benchstat is available
    if ! command -v benchstat &>/dev/null; then
        echo "Installing benchstat..." >&2
        go install golang.org/x/perf/cmd/benchstat@latest
    fi

    # Use BENCH_CMP_LIBS order (first lib = benchstat base), skip missing files
    read -ra cmp_libs <<< "$BENCH_CMP_LIBS"

    local args=()
    for lib in "${cmp_libs[@]}"; do
        [[ -f "$datadir/marshal-${lib}.txt" ]] && args+=("$lib=$datadir/marshal-${lib}.txt")
    done
    if [[ ${#args[@]} -ge 2 ]]; then
        echo "--- benchstat: marshal ---" >&2
        benchstat "${args[@]}" | tee "$datadir/benchcmp-marshal.txt"
        echo "" >&2
    fi

    args=()
    for lib in "${cmp_libs[@]}"; do
        [[ -f "$datadir/unmarshal-${lib}.txt" ]] && args+=("$lib=$datadir/unmarshal-${lib}.txt")
    done
    if [[ ${#args[@]} -ge 2 ]]; then
        echo "--- benchstat: unmarshal ---" >&2
        benchstat "${args[@]}" | tee "$datadir/benchcmp-unmarshal.txt"
        echo "" >&2
    fi

    echo "Results in: $datadir" >&2
}

# --- Phase 1: Collect ---

do_collect() {
    local binary
    binary=$(detect_binary)

    local datadir="local/benchdata/$(date +%Y%m%d-%H%M)"
    mkdir -p "$datadir"

    echo "=== Collecting benchmarks → $datadir ===" >&2

    read -ra cmp_libs <<< "$BENCH_CMP_LIBS"

    # Full run for benchviz (all libs, all benchmarks)
    echo "--- all benchmarks ---" >&2
    bash "$BENCH_SH" -b "$binary" -f '.' -t "$BENCH_TIME" -c "$BENCH_COUNT" -w -o "$datadir/benchmark.txt"

    # Per-lib runs for benchcmp (strip suffix so benchstat can match)
    for lib in "${cmp_libs[@]}"; do
        echo "--- marshal: $lib ---" >&2
        bash "$BENCH_SH" -b "$binary" -f "$BENCH_FILTER_MARSHAL" -l "$lib" -c "$BENCH_COUNT" -t "$BENCH_TIME" -w --no-benchmem \
            | strip_lib_suffix "$lib" > "$datadir/marshal-${lib}.txt"

        echo "--- unmarshal: $lib ---" >&2
        bash "$BENCH_SH" -b "$binary" -f "$BENCH_FILTER_UNMARSHAL" -l "$lib" -c "$BENCH_COUNT" -t "$BENCH_TIME" -w --no-benchmem \
            | strip_lib_suffix "$lib" > "$datadir/unmarshal-${lib}.txt"
    done

    echo "=== Done. Data in: $datadir ===" >&2
    echo "To analyze: scripts/bench-run.sh --analyze $datadir" >&2
}

# --- main ---

case "${1:-}" in
    --analyze)
        shift
        do_analyze "${1:-}"
        ;;
    "")
        do_collect
        ;;
    *)
        # Pass-through mode
        binary=$(detect_binary)
        exec bash "$BENCH_SH" -b "$binary" "$@"
        ;;
esac
