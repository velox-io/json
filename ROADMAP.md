# Roadmap

This file lists current high-impact improvement areas for contributors.

## Current Focus Areas

1. **Add Windows native backend support (`native/encvm`)**

   Current status: Windows currently uses the Go fallback marshal path; native backend is not available.

1. **Optimize `map[string]T` serialization**

   Current status: all map types fall back to the Go path; the native C VM does not handle maps yet.

1. **Reduce `encvm` context memory usage**

   Current status: execution context can still be optimized.

1. **Improve decoder buffer management flexibility**

   Current status: buffer strategy is usable but not flexible enough for all workloads.


## How to Help

- Open an issue to claim an item and discuss approach.
- Submit focused PRs with tests/benchmarks.
- Share benchmark reports from real workloads and platforms.

## Basic Local Checks

```bash
make fmt
make lint
make test
make bench
```
