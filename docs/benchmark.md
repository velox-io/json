# Benchmarks

```
cpu: AMD EPYC 7K62 48-Core Processor
                                 │    Velox     │                StdJSON                │                Sonic                │
                                 │    sec/op    │    sec/op      vs base                │    sec/op     vs base               │
_Unmarshal_Tiny-16                 309.1n ± ∞ ¹   2394.0n ± ∞ ¹  +674.51% (p=0.008 n=5)   477.3n ± ∞ ¹  +54.42% (p=0.008 n=5)
_Unmarshal_TinyCompact-16          303.6n ± ∞ ¹   2274.0n ± ∞ ¹  +649.01% (p=0.008 n=5)   465.3n ± ∞ ¹  +53.26% (p=0.008 n=5)
_Unmarshal_Small-16                1.686µ ± ∞ ¹   12.433µ ± ∞ ¹  +637.43% (p=0.008 n=5)   1.999µ ± ∞ ¹  +18.56% (p=0.008 n=5)
_Unmarshal_SmallCompact-16         1.417µ ± ∞ ¹   10.322µ ± ∞ ¹  +628.44% (p=0.008 n=5)   1.722µ ± ∞ ¹  +21.52% (p=0.008 n=5)
_Unmarshal_EscapeHeavy-16          6.012µ ± ∞ ¹   50.560µ ± ∞ ¹  +740.98% (p=0.008 n=5)   7.355µ ± ∞ ¹  +22.34% (p=0.008 n=5)
_Unmarshal_EscapeHeavyCompact-16   5.305µ ± ∞ ¹   39.271µ ± ∞ ¹  +640.26% (p=0.008 n=5)   6.134µ ± ∞ ¹  +15.63% (p=0.008 n=5)
_Unmarshal_KubePods-16             38.05µ ± ∞ ¹   365.20µ ± ∞ ¹  +859.88% (p=0.008 n=5)   48.42µ ± ∞ ¹  +27.27% (p=0.008 n=5)
_Unmarshal_KubePodsCompact-16      31.97µ ± ∞ ¹   254.80µ ± ∞ ¹  +696.97% (p=0.008 n=5)   40.08µ ± ∞ ¹  +25.37% (p=0.008 n=5)
_Unmarshal_Twitter-16              657.5µ ± ∞ ¹   6595.2µ ± ∞ ¹  +903.13% (p=0.008 n=5)   867.6µ ± ∞ ¹  +31.96% (p=0.008 n=5)
_Unmarshal_TwitterCompact-16       572.1µ ± ∞ ¹   5165.6µ ± ∞ ¹  +802.95% (p=0.008 n=5)   742.8µ ± ∞ ¹  +29.83% (p=0.008 n=5)
_Unmarshal_TwitterTyped-16         527.7µ ± ∞ ¹   5196.7µ ± ∞ ¹  +884.76% (p=0.008 n=5)   661.5µ ± ∞ ¹  +25.36% (p=0.008 n=5)
geomean                            12.96µ          107.8µ        +732.08%                 16.72µ        +29.05%

                         │    Velox     │                StdJSON                │                Sonic                 │
                         │    sec/op    │    sec/op      vs base                │    sec/op      vs base               │
_Marshal_Tiny-16           208.8n ± ∞ ¹    540.2n ± ∞ ¹  +158.72% (p=0.008 n=5)    239.2n ± ∞ ¹   +14.56% (p=0.008 n=5)
_Marshal_Small-16          500.9n ± ∞ ¹   1785.0n ± ∞ ¹  +256.36% (p=0.008 n=5)    612.3n ± ∞ ¹   +22.24% (p=0.008 n=5)
_Marshal_EscapeHeavy-16    1.843µ ± ∞ ¹    7.226µ ± ∞ ¹  +292.08% (p=0.008 n=5)    3.040µ ± ∞ ¹   +64.95% (p=0.008 n=5)
_Marshal_KubePods-16       6.805µ ± ∞ ¹   42.293µ ± ∞ ¹  +521.50% (p=0.008 n=5)   13.326µ ± ∞ ¹   +95.83% (p=0.008 n=5)
_Marshal_Twitter-16        126.1µ ± ∞ ¹    813.7µ ± ∞ ¹  +545.27% (p=0.008 n=5)    185.4µ ± ∞ ¹   +47.01% (p=0.008 n=5)
_Marshal_TwitterTyped-16   93.47µ ± ∞ ¹   638.61µ ± ∞ ¹  +583.24% (p=0.008 n=5)   128.80µ ± ∞ ¹   +37.80% (p=0.008 n=5)
_Marshal_MapAny-16         19.69µ ± ∞ ¹   179.87µ ± ∞ ¹  +813.47% (p=0.008 n=5)    64.12µ ± ∞ ¹  +225.65% (p=0.008 n=5)
geomean                    6.072µ          31.00µ        +410.59%                  9.864µ         +62.44%
```

### AMD EPYC 7K62

Environment: Linux, **AMD EPYC 7K62 48-Core Processor**, x86_64, 8 cores / 16 threads, KVM virtualized

<p align="center"><img src="benchmarks/linux-amd64-amd-epyc7k62/benchmark.svg" alt="Benchmark chart (AMD EPYC 7K62)"></p>

### Intel Xeon Gold 6133

Environment: Linux, **Intel(R) Xeon(R) Gold 6133 CPU @ 2.50GHz**, x86_64, 4 cores / 4 threads, 1 socket, KVM virtualized

<p align="center"><img src="benchmarks/linux-amd64-intel-gold6133/benchmark.svg" alt="Benchmark chart (Intel Xeon Gold 6133)"></p>

### Apple M4 Pro

Environment: **Apple M4 Pro**, Go 1.24, `GOMAXPROCS=14`

<p align="center"><img src="benchmarks/darwin-arm64-apple-m4/benchmark.svg" alt="Benchmark chart (Apple M4 Pro)"></p>

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

