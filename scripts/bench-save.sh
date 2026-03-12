#!/bin/bash
# Run benchmarks and save/update baseline
# Usage: ./scripts/bench-save.sh
#
# IMPORTANT: Baseline is environment-specific!
# Each environment should maintain its own baseline.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BENCHDATA_DIR="$PROJECT_ROOT/.benchdata"

mkdir -p "$BENCHDATA_DIR"

# Detect current environment
ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ENV_ID="$OS-$ARCH"

echo "Saving baseline for environment: $ENV_ID"
echo "Running benchmarks..."

cd "$PROJECT_ROOT"

# Create baseline with environment metadata
{
    echo "# env: $ENV_ID"
    echo "# created: $(date -Iseconds)"
    echo "# go: $(go version | awk '{print $3}')"
    echo "# threshold: 10%"
    echo ""
} > "$BENCHDATA_DIR/baseline.txt"

go test -bench=. -benchmem -count=5 ./... >> "$BENCHDATA_DIR/baseline.txt" 2>/dev/null || true
cd benchmark && go test -bench=. -benchmem -count=5 . >> "../$BENCHDATA_DIR/baseline.txt" 2>/dev/null || true

echo ""
echo "Baseline saved to: $BENCHDATA_DIR/baseline.txt"
echo ""
echo "NOTE: This baseline is specific to $ENV_ID"
echo "      Do not compare with baselines from other environments."
