#!/usr/bin/env bash
#
# perf-native.sh - Profile the native C (syso) portion of a benchmark with perf,
# and resolve hot instruction addresses back to C source lines.
#
# The committed .syso carries only a .symtab (function names), so `perf report`
# already attributes hotspots to C functions. Source LINES need DWARF, which
# lives in the intermediate base-0 debug .so kept by a `PROFILE=1` build (see
# scripts/prelink.sh / gen-natives.sh). Because prelink-obj copies .text
# verbatim, every C function sits at the SAME offset-within-.text in the debug
# .so (base 0) and in the final Go benchmark binary. So a perf sample at final
# vaddr V maps to debug-.so address:  V - final_func_vaddr + so_func_offset.
# This script computes that per-function shift automatically.
#
# Usage:
#   scripts/perf-native.sh [options] -- [extra go test flags]
#
# Options:
#   -m <module>   Native module to (re)build with PROFILE=1: encvm | ndec | vlib.
#                 May be repeated. Default: ndec (the decode/bind path).
#                 (encvm -> `make encvm`; Marshal/encode paths use encvm.)
#   -b <filter>   Benchmark filter (go test -bench regex).
#                 Default: Benchmark_Unmarshal_KubePods_Velox$
#   -t <time>     benchtime (go test -benchtime). Default: 8s
#   -F <freq>     perf sampling frequency Hz. Default: 4999
#   -g <gogc>     GOGC value during profiling. Default: off (isolates parser CPU
#                 from GC noise; the C code is what we're profiling). Use a
#                 number (e.g. 100) or "on" to profile with GC included.
#   -n <N>        annotate top-N hot instructions per hot C function. Default: 15
#   -k <name>     annotate a specific C function (repeatable). Default: auto
#                 (every C function above the -p threshold in perf report).
#   -p <pct>      self-% threshold for auto-selecting C functions. Default: 3.0
#   --no-build    Skip the PROFILE=1 native rebuild and bench-build.
#   --no-record   Skip perf record; reuse existing perf.data (for -k re-analysis).
#   -o <dir>      Output dir. Default: local/perf/<timestamp>
#   -h            Help.
#
# GOGC tuning:
#   Default GOGC=off isolates parser CPU from GC noise, ideal for low-alloc
#   marshal/encode benches (encvm). For high-alloc unmarshal benches
#   (map[string]any, []any: ~100KB/op), GOGC=off causes unbounded heap growth:
#   kernel mmap/page-fault time drowns out the samples and the process slows
#   ~2x. Use -g 100 for those.
#
# make perf pitfall:
#   `make perf PERF_ARGS='-t 8s'` FAILS: make parses -t as its own touch flag.
#   Either quote the whole arg list, or (preferred) call the script directly:
#     bash scripts/perf-native.sh -m ndec -b 'Bench$' -t 8s -g 100
#   Only -m/-b/-t have make variables (PERF_MODULE/PERF_BENCH/PERF_TIME); for
#   -F/-g/-n/-k/-p always use the script form.
#
# Benchmark binary:
#   The script runs local/bin/vjson-benchmark_<os>_<arch> (built by
#   `make bench-build`). Do NOT use benchmark/benchmark.test (the `go test -c`
#   default product); its bench set is incomplete.
#
# Requires: perf, addr2line, readelf, awk (all standard).

# Note: intentionally NOT using `set -e`/`pipefail`. This script runs many
# exploratory pipelines that legitimately end in `head` (→ upstream SIGPIPE) or
# arithmetic that can evaluate to 0 (→ non-zero $?), neither of which is a real
# error. Errors are handled explicitly where they matter.
set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH=$(uname -m); case "$HOST_ARCH" in x86_64) HOST_ARCH=amd64;; aarch64) HOST_ARCH=arm64;; esac

MODULES=()
BENCH='Benchmark_Unmarshal_KubePods_Velox$'
BENCHTIME=8s
FREQ=4999
GOGC=off
TOPN=15
PCT=3.0
FUNCS=()
DO_BUILD=true
DO_RECORD=true
OUTDIR=""
EXTRA=()

while [ $# -gt 0 ]; do
    case "$1" in
        -m) MODULES+=("$2"); shift 2 ;;
        -b) BENCH="$2"; shift 2 ;;
        -t) BENCHTIME="$2"; shift 2 ;;
        -F) FREQ="$2"; shift 2 ;;
        -g) GOGC="$2"; shift 2 ;;
        -n) TOPN="$2"; shift 2 ;;
        -k) FUNCS+=("$2"); shift 2 ;;
        -p) PCT="$2"; shift 2 ;;
        --no-build) DO_BUILD=false; shift ;;
        --no-record) DO_RECORD=false; shift ;;
        -o) OUTDIR="$2"; shift 2 ;;
        --) shift; EXTRA=("$@"); break ;;
        -h|--help) sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "Unknown option: $1" >&2; exit 2 ;;
    esac
done

[ ${#MODULES[@]} -eq 0 ] && MODULES=(ndec)
[ -z "$OUTDIR" ] && OUTDIR="local/perf/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUTDIR"

BIN="local/bin/vjson-benchmark_${HOST_OS}_${HOST_ARCH}"
PERF_DATA="$OUTDIR/perf.data"

need() { command -v "$1" >/dev/null 2>&1 || { echo "Error: '$1' not found in PATH" >&2; exit 1; }; }
need perf; need addr2line; need readelf; need awk

# ------------------------------------------------------------------
# Phase 1: build native modules with PROFILE=1 (keeps base-0 debug .so),
# then build the standalone benchmark binary.
# ------------------------------------------------------------------
if [ "$DO_BUILD" = true ]; then
    for m in "${MODULES[@]}"; do
        # Module name -> make target. encvm's syso is built by the generic `gen`
        # target (native/encvm/sources.sh); ndec/vlib have same-named targets.
        case "$m" in
            encvm) tgt=gen ;;
            *)     tgt="$m" ;;
        esac
        echo ">> make $tgt PROFILE=1 (module $m; keeps DWARF in build/prelink/*.so)"
        make "$tgt" PROFILE=1 TARGET_OS="$HOST_OS" TARGET_ARCH="$HOST_ARCH" >/dev/null \
            || { echo "Error: 'make $tgt PROFILE=1' failed" >&2; exit 1; }
    done
    echo ">> make bench-build"
    make bench-build GOOS="$HOST_OS" GOARCH="$HOST_ARCH" >/dev/null \
        || { echo "Error: 'make bench-build' failed" >&2; exit 1; }
fi
[ -x "$BIN" ] || { echo "Error: benchmark binary missing: $BIN (drop --no-build)" >&2; exit 1; }

# Collect the debug .so set produced by the PROFILE build(s). Only keep .so
# files that actually carry DWARF (.debug_line): build/prelink/ can contain
# stale non-debug .so leftovers from earlier non-PROFILE builds, and those would
# give wrong function offsets. Prefer newest first so a fresh PROFILE build wins.
DBG_SOS=()
for so in $(ls -t build/prelink/*_"${HOST_OS}_${HOST_ARCH}".so 2>/dev/null); do
    [ -f "$so" ] || continue
    if readelf -SW "$so" 2>/dev/null | grep -q '\.debug_line'; then
        DBG_SOS+=("$so")
    fi
done
[ ${#DBG_SOS[@]} -eq 0 ] && echo "Warning: no DWARF-carrying .so under build/prelink/ — did the PROFILE=1 build run? source lines unavailable" >&2

# ------------------------------------------------------------------
# Phase 2: perf record
# ------------------------------------------------------------------
if [ "$DO_RECORD" = true ]; then
    echo ">> perf record -F $FREQ --call-graph fp  bench='$BENCH' benchtime=$BENCHTIME GOGC=$GOGC"
    GOGC="$GOGC" perf record -F "$FREQ" -g --call-graph fp -o "$PERF_DATA" -- \
        "$BIN" -test.run='^$' -test.bench="$BENCH" -test.benchtime="$BENCHTIME" \
        "${EXTRA[@]}" 2>&1 | grep -vE '^Warning:|cc-wrapper|--target' || true
fi
[ -f "$PERF_DATA" ] || { echo "Error: no perf.data at $PERF_DATA" >&2; exit 1; }

# ------------------------------------------------------------------
# Phase 3: report (self%), save + show top symbols
# ------------------------------------------------------------------
REPORT="$OUTDIR/report.txt"
perf report -i "$PERF_DATA" --stdio --no-children 2>/dev/null > "$REPORT" || true
NSAMP=$(perf report -i "$PERF_DATA" --stdio 2>/dev/null | sed -n 's/^# Samples: *\([0-9KMG]*\).*/\1/p' | head -1)
echo ""
echo "=== Top symbols (self)  [samples: ${NSAMP:-?}] ==="
{ grep -vE '^#|^$' "$REPORT" | grep -E '^\s+[0-9]' | head -15; } || true
if ! grep -qE '^\s+[0-9]' "$REPORT"; then
    echo "  (no samples captured — increase -t benchtime or -F, or the run was too short)"
else
    # Warn on low sample counts. perf may throttle sampling under fp-based
    # call-graph on Go binaries, or cgroup limits may cap perf_event_open.
    # Expand K/M/G suffixes to a numeric value for the threshold check.
    nsamp_num=$(printf '%s' "${NSAMP:-0}" | awk '{
        s=$1; mult=1;
        if (s ~ /K$/) { mult=1000; sub(/K$/,"",s) }
        else if (s ~ /M$/) { mult=1000000; sub(/M$/,"",s) }
        else if (s ~ /G$/) { mult=1000000000; sub(/G$/,"",s) }
        print s*mult
    }')
    if [ "${nsamp_num:-0}" -gt 0 ] && [ "${nsamp_num:-0}" -lt 500 ]; then
        echo "  WARNING: only ${NSAMP} samples — hotspot attribution will be coarse."
        echo "    Try higher -F (e.g. -F 9999), longer -t, or re-record manually with"
        echo "    --call-graph=lbr (cheaper than fp on Go binaries):"
        echo "      perf record -F 9999 --call-graph=lbr -o $PERF_DATA -- \\"
        echo "        $BIN -test.run='^$' -test.bench='$BENCH' -test.benchtime=$BENCHTIME"
    fi
fi

# ------------------------------------------------------------------
# Helpers for address -> source-line mapping.
# For a C function name, find (a) its vaddr in the final binary and
# (b) its offset in whichever debug .so defines it. The uniform shift is
#   debug_addr = V - final_vaddr + so_offset.
# ------------------------------------------------------------------
final_vaddr() { readelf -sW "$BIN" | awk -v f="$1" '$8==f{print "0x"$2; exit}'; }
so_for_func() { # echo "<so> <offset>" for the .so that defines func $1
    local f="$1" so off
    for so in "${DBG_SOS[@]}"; do
        off=$(readelf -sW "$so" | awk -v f="$f" '$8==f{print "0x"$2; exit}')
        [ -n "$off" ] && { echo "$so $off"; return 0; }
    done
    return 1
}

# Auto-select hot C functions (those with a matching debug .so) above threshold.
if [ ${#FUNCS[@]} -eq 0 ]; then
    while read -r pct name; do
        # Keep only symbols we can resolve via a debug .so (i.e. native C funcs).
        if so_for_func "$name" >/dev/null 2>&1; then
            FUNCS+=("$name")
        fi
    done < <(grep -E '^\s+[0-9]+\.[0-9]+%' "$REPORT" \
             | awk -v p="$PCT" '{gsub(/%/,"",$1)} ($1+0)>=p {print $1, $NF}' \
             | head -20)
fi

if [ ${#FUNCS[@]} -eq 0 ]; then
    echo ""
    echo "No native C functions above ${PCT}% with a debug .so. Nothing to annotate."
    echo "perf.data: $PERF_DATA   report: $REPORT"
    exit 0
fi

# ------------------------------------------------------------------
# Phase 4: per-function hot-instruction -> source line
# ------------------------------------------------------------------
LINES="$OUTDIR/hotlines.txt"
: > "$LINES"
for fn in "${FUNCS[@]}"; do
    read -r SO OFF < <(so_for_func "$fn") || { echo "  ($fn: no debug .so)"; continue; }
    FV=$(final_vaddr "$fn"); [ -z "$FV" ] && continue

    {
        echo "############################################################"
        echo "# $fn"
        echo "#   final vaddr = $FV   debug .so = $SO (offset $OFF)"
        echo "############################################################"
    } >> "$LINES"

    # Top-N hottest instruction addresses within this function.
    perf annotate -i "$PERF_DATA" --stdio -q --no-source "$fn" 2>/dev/null \
        | grep -E '^\s+[0-9]+\.[0-9]+ :' \
        | sort -rn | head -n "$TOPN" \
        | while read -r line; do
            pct=$(echo "$line" | awk '{print $1}')
            va=$(echo "$line" | sed -E 's/^[^:]*:\s*([0-9a-f]+):.*/\1/')
            insn=$(echo "$line" | sed -E 's/^[^:]*:\s*[0-9a-f]+:\s*(.*)$/\1/')
            [ -z "$va" ] && continue
            da=$(printf '0x%x' $(( 0x$va - FV + OFF )))
            src=$(addr2line -e "$SO" -i "$da" 2>/dev/null | paste -sd' <- ' -)
            printf '%6s%%  %-8s  %-32s  %s\n' "$pct" "0x$va" "$insn" "$src"
        done >> "$LINES"
    echo "" >> "$LINES"
done

echo ""
echo "=== Hot instructions -> C source lines ==="
cat "$LINES"
echo ""
echo "Artifacts:"
echo "  perf.data : $PERF_DATA   (perf report -i \"$PERF_DATA\")"
echo "  report    : $REPORT"
echo "  hotlines  : $LINES"
