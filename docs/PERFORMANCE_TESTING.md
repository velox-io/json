# Performance Regression Testing

This document describes the performance regression testing setup for this project.

## Important: Environment Consistency

**Baselines are environment-specific.** Different machines will produce different absolute numbers due to:

- CPU architecture (ARM vs x86)
- CPU frequency and cache size
- Memory speed
- OS scheduling behavior

### Recommended Approach

| Scenario | Approach |
|----------|----------|
| **CI/CD** | Use CI environment baseline (stored in cache) |
| **Local Development** | Only for quick checks, not for CI comparison |

```
❌ Bad:  Local baseline vs CI baseline
✅ Good: CI baseline (PR) vs CI baseline (main)
✅ Good: Local baseline vs Local baseline (same machine)
```

## Overview

The performance testing system consists of:

1. **Local scripts** - For development-time benchmarking and regression checks
2. **CI/CD integration** - Automated benchmarking on every PR and push to main

## Quick Start

### Local Usage

```bash
# Save current benchmark results as baseline
make bench-baseline

# Check for regressions (>10% threshold)
make bench-check

# Check with custom threshold (e.g., 5%)
make bench-check-threshold THRESHOLD=5

# Or run the script directly
./scripts/benchcheck.sh .benchdata/baseline.txt 5
```

### CI/CD (GitHub Actions)

The CI automatically:
1. Runs benchmarks on every push to `main`/`master` and every PR
2. Compares against the saved baseline
3. Fails the build if regressions exceed 10%
4. Updates the baseline on successful main branch merges

## Files

```
.
├── .github/workflows/
│   └── benchmark.yml      # CI workflow for benchmarking
├── scripts/
│   ├── benchcheck.sh      # Local regression check script
│   └── bench-save.sh      # Save baseline script
├── .benchdata/            # Local benchmark data (gitignored)
│   └── baseline.txt       # Current baseline results
└── Makefile               # Make targets for convenience
```

## How It Works

### Threshold-Based Detection

The system detects performance regressions by:

1. Running benchmarks multiple times (`count=5` for statistical stability)
2. Using `benchstat` to compare with baseline
3. Flagging any benchmark that regressed more than the threshold percentage

Example output:

```
=== Comparison Results ===
name                           old time/op  new time/op  delta
Benchmark_Twitter_Velox-8      2.45ms ± 1%  2.48ms ± 2%  +1.22%  (ok)
Benchmark_EscapeHeavy_Velox-8  89.1µs ± 1%  98.5µs ± 2%  +10.5%  (FAIL)

✗ Benchmark_EscapeHeavy_Velox: +10.5%
ERROR: Performance regression detected!
```

### When to Update Baseline

You should update the baseline (`make bench-baseline`) when:

1. **After performance improvements** - Commit the new baseline with your optimization
2. **Adding new benchmarks** - Include them in the baseline
3. **After dependency updates** - That legitimately affect performance

**Important:** Always commit baseline updates with a clear message explaining why.

## Configuration

### Adjusting Thresholds

Default threshold is 10%. You can customize:

```bash
# Local check with 5% threshold
make bench-check-threshold THRESHOLD=5

# Or in the script directly
./scripts/benchcheck.sh .benchdata/baseline.txt 5
```

For CI, edit `.github/workflows/benchmark.yml`:

```yaml
# Change this line to adjust the threshold
if [ $(echo "$percent > 5" | bc -l) -eq 1 ]; then
```

### Adding New Benchmarks

1. Add your benchmark function (must start with `Benchmark`)
2. Run `make bench-baseline` to include it
3. Commit the updated baseline

## Best Practices

### Writing Stable Benchmarks

```go
func Benchmark_MyFunc(b *testing.B) {
    b.ReportAllocs()  // Track memory allocations
    b.ResetTimer()     // Reset timer after setup

    // Use b.Loop() for Go 1.24+ or b.N for older versions
    for b.Loop() {
        MyFunc(testData)
    }
}
```

### Avoiding Flaky Benchmarks

1. **Run multiple times** - Use `count=5` or higher
2. **Minimize setup in the loop** - Use `b.ResetTimer()`
3. **Avoid I/O in benchmarks** - Keep everything in memory
4. **Use consistent test data** - Embed test files with `//go:embed`

## Troubleshooting

### "No baseline found"

Run `make bench-baseline` first to create the baseline.

### Inconsistent Results

Benchmarks can vary due to:
- CPU frequency scaling
- Background processes
- Thermal throttling

Solutions:
- Close other applications
- Run on a quiet machine
- Increase `count` for more samples

### CI vs Local Discrepancies

CI environments may show different absolute numbers. Focus on:
- Relative changes (percentage)
- Consistent patterns across runs
