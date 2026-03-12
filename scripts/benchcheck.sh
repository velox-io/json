#!/usr/bin/env bash
# Performance Regression Test Script
# Usage: ./scripts/benchcheck.sh [baseline_file] [threshold_percent]
#
# IMPORTANT: Baseline should be created in the same environment.
# If the baseline env differs, the script warns and prompts before continuing.
# For CI, use CI-created baselines; local baselines are not comparable.
#
# Examples:
#   ./scripts/benchcheck.sh                           # Compare with saved baseline
#   ./scripts/benchcheck.sh .benchdata/baseline.txt   # Compare with specific baseline
#   ./scripts/benchcheck.sh .benchdata/baseline.txt 5 # Use 5% threshold

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BENCHDATA_DIR="$PROJECT_ROOT/.benchdata"
BASELINE_FILE="${1:-$BENCHDATA_DIR/baseline.txt}"
THRESHOLD="${2:-10}"  # Default 10% threshold
COUNT="${BENCH_COUNT:-6}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "=== Performance Regression Test ==="
echo ""
echo -e "${YELLOW}WARNING: Baselines are environment-specific!${NC}"
echo "  - Local baseline cannot be compared with CI baseline"
echo "  - Use this script with baselines created on the same machine"
echo ""

# Detect current environment
ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
echo "Current environment: $OS-$ARCH"

# Check baseline environment if exists
if [ -f "$BASELINE_FILE" ]; then
    if head -5 "$BASELINE_FILE" | grep -q "env:"; then
        BASELINE_ENV=$(head -5 "$BASELINE_FILE" | grep "env:" | cut -d: -f2- | xargs || echo "unknown")
        echo "Baseline environment: $BASELINE_ENV"
        if [ "$BASELINE_ENV" != "$OS-$ARCH" ]; then
            echo -e "${YELLOW}WARNING: Baseline was created on a different environment!${NC}"
            echo "  Results may not be comparable."
            read -p "Continue anyway? (y/N) " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                exit 1
            fi
        fi
    fi
fi

echo "Baseline: $BASELINE_FILE"
echo "Threshold: ${THRESHOLD}%"
echo ""

# Ensure benchdata directory exists
mkdir -p "$BENCHDATA_DIR"

# Install benchstat if not present
if ! command -v benchstat &> /dev/null; then
    echo "Installing benchstat..."
    go install golang.org/x/perf/cmd/benchstat@latest
fi

# Run benchmarks
echo "Running benchmarks (count=$COUNT)..."
TEMP_FILE=$(mktemp)
trap "rm -f $TEMP_FILE" EXIT

cd "$PROJECT_ROOT"
cd benchmark && go test -bench=Velox -benchmem -count=$COUNT . | tee -a "$TEMP_FILE" 2>/dev/null || true

# Check if we have a baseline to compare
if [ ! -f "$BASELINE_FILE" ]; then
    echo -e "${YELLOW}No baseline found. Saving current results as baseline.${NC}"
    # Add environment info to baseline
    echo "# env: $OS-$ARCH" > "$BASELINE_FILE"
    echo "# created: $(date -Iseconds)" >> "$BASELINE_FILE"
    echo "# go: $(go version | awk '{print $3}')" >> "$BASELINE_FILE"
    echo "" >> "$BASELINE_FILE"
    cat "$TEMP_FILE" >> "$BASELINE_FILE"
    echo "Baseline saved to: $BASELINE_FILE"
    exit 0
fi

# Compare with baseline
# Use short names for better readability in benchstat output
TEMP_BASENAME=$(basename "$TEMP_FILE")
BASELINE_BASENAME=$(basename "$BASELINE_FILE")

# Create a temp dir with symlinks to both files using short names
COMPARE_DIR=$(mktemp -d)
trap "rm -f $TEMP_FILE; rm -rf $COMPARE_DIR" EXIT
ln -s "$BASELINE_FILE" "$COMPARE_DIR/$BASELINE_BASENAME"
ln -s "$TEMP_FILE" "$COMPARE_DIR/$TEMP_BASENAME"

echo ""
echo "=== Comparison Results ==="
cd "$COMPARE_DIR" && benchstat "$BASELINE_BASENAME" "$TEMP_BASENAME"

# Check for regressions
REGRESSIONS=()
IMPROVEMENTS=()

while IFS= read -r line; do
    # Skip header and empty lines
    if [[ -z "$line" ]] || [[ "$line" =~ ^(Benchmark|name|pkg:) ]]; then
        continue
    fi

    # Check for percentage changes (format: "BenchmarkName  old  new  +X.XX%")
    if echo "$line" | grep -qE '\+[0-9]+\.[0-9]+%'; then
        # Extract the benchmark name and percentage
        bench_name=$(echo "$line" | awk '{print $1}')
        percent=$(echo "$line" | grep -oE '\+[0-9]+\.[0-9]+%' | sed 's/[+%]//g')

        if [ ! -z "$percent" ]; then
            is_regression=$(echo "$percent > $THRESHOLD" | bc -l)
            if [ "$is_regression" -eq 1 ]; then
                REGRESSIONS+=("$bench_name: +${percent}%")
            fi
        fi
    elif echo "$line" | grep -qE '\-[0-9]+\.[0-9]+%'; then
        bench_name=$(echo "$line" | awk '{print $1}')
        percent=$(echo "$line" | grep -oE '\-[0-9]+\.[0-9]+%' | sed 's/[-%]//g')
        if [ ! -z "$percent" ]; then
            is_improvement=$(echo "$percent > $THRESHOLD" | bc -l)
            if [ "$is_improvement" -eq 1 ]; then
                IMPROVEMENTS+=("$bench_name: -${percent}%")
            fi
        fi
    fi
done < <(cd "$COMPARE_DIR" && benchstat "$BASELINE_BASENAME" "$TEMP_BASENAME" 2>/dev/null)

# Report results
echo ""
if [ ${#IMPROVEMENTS[@]} -gt 0 ]; then
    echo -e "${GREEN}Significant Improvements (>${THRESHOLD}%):${NC}"
    for imp in "${IMPROVEMENTS[@]}"; do
        echo -e "  ${GREEN}✓${NC} $imp"
    done
fi

if [ ${#REGRESSIONS[@]} -gt 0 ]; then
    echo -e "${RED}Significant Regressions (>${THRESHOLD}%):${NC}"
    for reg in "${REGRESSIONS[@]}"; do
        echo -e "  ${RED}✗${NC} $reg"
    done
    echo ""
    echo -e "${RED}ERROR: Performance regression detected!${NC}"
    echo "To update baseline after intentional changes:"
    echo "  ./scripts/bench-save.sh"
    exit 1
else
    echo -e "${GREEN}✓ No significant regressions detected.${NC}"
    echo ""
    echo "To update baseline:"
    echo "  ./scripts/bench-save.sh"
fi

# Optionally save current results
echo ""
read -p "Save current results as new baseline? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    "$SCRIPT_DIR/bench-save.sh"
fi
