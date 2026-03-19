# Benchmarks

### Apple M4 Pro

Environment: **Apple M4 Pro**, Go 1.24, `GOMAXPROCS=14`

<p align="center"><img src="benchmarks/darwin-arm64-apple-m4/benchmark.svg" alt="Benchmark chart (Apple M4 Pro)"></p>

### AMD EPYC 7K62

Environment: Linux, **AMD EPYC 7K62 48-Core Processor**, x86_64, 8 cores / 16 threads, KVM virtualized

<p align="center"><img src="benchmarks/linux-amd64-amd-epyc7k62/benchmark.svg" alt="Benchmark chart (AMD EPYC 7K62)"></p>

### Intel Xeon Gold 6133

Environment: Linux, **Intel(R) Xeon(R) Gold 6133 CPU @ 2.50GHz**, x86_64, 4 cores / 4 threads, 1 socket, KVM virtualized

<p align="center"><img src="benchmarks/linux-amd64-intel-gold6133/benchmark.svg" alt="Benchmark chart (Intel Xeon Gold 6133)"></p>

### HiSilicon Kunpeng-920

Environment: Linux, **Kunpeng-920**, aarch64, 4 cores / 4 threads, 1 socket, L3 32 MB

<p align="center"><img src="benchmarks/linux-arm64-hisilicon-kunpeng920/benchmark.svg" alt="Benchmark chart (HiSilicon Kunpeng-920)"></p>

## Test Data

| File | Description |
|------|-------------|
| [tiny.json](../benchmark/testdata/tiny.json) | Minimal flat object (5 fields, ~80 B) |
| [small.json](../benchmark/testdata/small.json) | Small mixed object with nested structs and arrays (~370 B) |
| [escape_heavy.json](../benchmark/testdata/escape_heavy.json) | String-heavy payload with many escape sequences |
| [kubepods.json](../benchmark/testdata/kubepods.json) | Kubernetes pod list (~500 KB) |
| [twitter.json](../benchmark/testdata/twitter.json) | Twitter timeline (~600 KB) |

