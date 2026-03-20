# Velox JSON (vjson)

Velox is a high-performance JSON encoder/decoder for Go.

- Marshal uses a native C JSON VM backend (`native/encvm`) behind the same Go API
- Unmarshal is implemented in pure Go with a single-pass decoder and zero-copy strings
- Fast struct handling with cached type metadata

## Design

Velox is currently focused on **binding-style** JSON ↔ typed Go value conversion (`Unmarshal`/`Decode` + `Marshal`/`Encode`), targeting strong throughput and a low-allocation profile with tunable trade-offs between processing speed and GC pressure.

- **Multi-platform first.** Native marshal acceleration is available on `darwin/arm64`, `linux/amd64`, `linux/arm64`, and `windows/amd64`, while other platforms automatically use the Go fallback path.
- **Go-first evolution.** Core decode logic is implemented in Go so the project can evolve with Go compiler/runtime improvements and remain maintainable.
- **Native where it matters.** Marshal hot paths use the native C VM backend, while preserving the same Go-facing API and fallback behavior.

For a deeper look at the architecture — why unmarshal uses pure Go and marshal uses a C VM — see [How Velox JSON Works](docs/architecture.md).

## Install

```bash
go get github.com/velox-io/json@latest
```

## Quick start

### Unmarshal

```go
package main

import (
	"fmt"

	json "github.com/velox-io/json"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	data := []byte(`{"id":1,"name":"alice"}`)

	var u User
	if err := json.Unmarshal(data, &u); err != nil {
		panic(err)
	}

	fmt.Printf("%+v\n", u)
}
```

#### Zero-copy note

`Unmarshal` may keep **string fields** referencing the input buffer for zero-copy decoding. Do **not** modify (or reuse/mutate) the `data` buffer after calling `Unmarshal`.

### Marshal

```go
package main

import (
	"fmt"

	json "github.com/velox-io/json"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	u := User{ID: 1, Name: "alice"}

	b, err := json.Marshal(&u)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(b))
}
```

### Map serialization

**Do not write to a map concurrently while it is being marshaled.** Go's runtime detects concurrent map read+write and panics. Velox's native encoder reads map memory directly, bypassing this detection — a concurrent write during serialization may cause **silent data corruption** instead of a clean crash.

   ```go
   // ❌ DANGEROUS — concurrent write during marshal.
   // Standard encoding/json panics; Velox may silently corrupt.
   m := map[string]string{"existing": "data"}
   go func() { m["key"] = "value" }()
   b, _ := json.Marshal(&m)
   ```

## Performance & benchmarks

* Environment: Linux, **AMD EPYC 7K62 48-Core Processor**, x86_64, 8 cores / 16 threads, KVM virtualized

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

* Environment: **Apple M4 Pro**

```
goos: darwin
goarch: arm64
pkg: dev.local/benchmark
cpu: Apple M4 Pro
                                 │    Velox     │                StdJSON                │                 Sonic                 │
                                 │    sec/op    │    sec/op      vs base                │    sec/op      vs base                │
_Unmarshal_Tiny-14                 95.77n ± ∞ ¹   764.80n ± ∞ ¹   +698.58% (p=0.008 n=5)   234.70n ± ∞ ¹  +145.07% (p=0.008 n=5)
_Unmarshal_TinyCompact-14          86.52n ± ∞ ¹   716.30n ± ∞ ¹   +727.90% (p=0.008 n=5)   214.60n ± ∞ ¹  +148.04% (p=0.008 n=5)
_Unmarshal_Small-14                520.3n ± ∞ ¹   3993.0n ± ∞ ¹   +667.44% (p=0.008 n=5)   1121.0n ± ∞ ¹  +115.45% (p=0.008 n=5)
_Unmarshal_SmallCompact-14         410.6n ± ∞ ¹   3151.0n ± ∞ ¹   +667.41% (p=0.008 n=5)    977.6n ± ∞ ¹  +138.09% (p=0.008 n=5)
_Unmarshal_EscapeHeavy-14          2.281µ ± ∞ ¹   18.294µ ± ∞ ¹   +702.02% (p=0.008 n=5)    2.910µ ± ∞ ¹   +27.58% (p=0.008 n=5)
_Unmarshal_EscapeHeavyCompact-14   1.931µ ± ∞ ¹   14.337µ ± ∞ ¹   +642.47% (p=0.008 n=5)    2.548µ ± ∞ ¹   +31.95% (p=0.008 n=5)
_Unmarshal_KubePods-14             14.72µ ± ∞ ¹   130.89µ ± ∞ ¹   +789.09% (p=0.008 n=5)    22.03µ ± ∞ ¹   +49.61% (p=0.008 n=5)
_Unmarshal_KubePodsCompact-14      11.71µ ± ∞ ¹    87.27µ ± ∞ ¹   +645.21% (p=0.008 n=5)    18.94µ ± ∞ ¹   +61.70% (p=0.008 n=5)
_Unmarshal_Twitter-14              245.4µ ± ∞ ¹   2719.1µ ± ∞ ¹  +1008.05% (p=0.008 n=5)    463.4µ ± ∞ ¹   +88.82% (p=0.008 n=5)
_Unmarshal_TwitterCompact-14       216.7µ ± ∞ ¹   2158.5µ ± ∞ ¹   +896.12% (p=0.008 n=5)    398.3µ ± ∞ ¹   +83.80% (p=0.008 n=5)
_Unmarshal_TwitterTyped-14         206.0µ ± ∞ ¹   2162.3µ ± ∞ ¹   +949.85% (p=0.008 n=5)    373.2µ ± ∞ ¹   +81.20% (p=0.008 n=5)
geomean                            4.484µ          38.33µ         +754.79%                  8.232µ         +83.59%

                         │    Velox     │                StdJSON                │                 Sonic                 │
                         │    sec/op    │    sec/op      vs base                │    sec/op      vs base                │
_Marshal_Tiny-14           62.07n ± ∞ ¹   153.60n ± ∞ ¹  +147.46% (p=0.008 n=5)   199.60n ± ∞ ¹  +221.57% (p=0.008 n=5)
_Marshal_Small-14          161.2n ± ∞ ¹    551.3n ± ∞ ¹  +242.00% (p=0.008 n=5)    820.5n ± ∞ ¹  +409.00% (p=0.008 n=5)
_Marshal_EscapeHeavy-14    994.1n ± ∞ ¹   2785.0n ± ∞ ¹  +180.15% (p=0.008 n=5)   3856.0n ± ∞ ¹  +287.89% (p=0.008 n=5)
_Marshal_KubePods-14       2.808µ ± ∞ ¹   13.989µ ± ∞ ¹  +398.18% (p=0.008 n=5)   20.685µ ± ∞ ¹  +636.65% (p=0.008 n=5)
_Marshal_Twitter-14        44.37µ ± ∞ ¹   261.87µ ± ∞ ¹  +490.15% (p=0.008 n=5)   230.88µ ± ∞ ¹  +420.30% (p=0.008 n=5)
_Marshal_TwitterTyped-14   35.08µ ± ∞ ¹   207.02µ ± ∞ ¹  +490.17% (p=0.008 n=5)   185.53µ ± ∞ ¹  +428.90% (p=0.008 n=5)
_Marshal_MapAny-14         8.734µ ± ∞ ¹   58.440µ ± ∞ ¹  +569.11% (p=0.008 n=5)   58.491µ ± ∞ ¹  +569.69% (p=0.008 n=5)
geomean                    2.336µ          10.06µ        +330.77%                  11.85µ        +407.07%
```

See [detailed benchmark results](docs/benchmark.md).

### Highlights

- Benchmark coverage includes Tiny/Small/EscapeHeavy/KubePods/Twitter payloads, from ~80 B to ~600 KB (`docs/benchmark.md`).
- Marshal benchmarks compare `Velox` with `encoding/json`, `sonic`, `go-json`, and selected `easyjson` cases (`benchmark/b1_marshal_test.go`).

## Compatibility

Velox keeps the `encoding/json`-style API surface, and exposes explicit options for behavior that teams usually need to control in production.

- Marshal behavior controls:
  - `WithEscapeHTML()` / `WithoutEscapeHTML()`
  - `WithEscapeLineTerms()` / `WithoutEscapeLineTerms()`
  - `WithUTF8Correction()` / `WithoutUTF8Correction()`
  - `WithStdCompat()` for full escaping compatibility mode
  - `WithFastEscape()` for the minimal-escape fast path
- Number decoding controls:
  - `WithUseNumber()` for `Unmarshal`
  - `(*Decoder).UseNumber()` for streaming `Decoder`
  - Note: these options affect numbers decoded into `any`/`interface{}` (decoded as `json.Number` instead of `float64`)

## Error handling

Velox returns typed errors and supports `errors.As` bridging to `encoding/json` error types for compatibility with existing error handling code.

| Velox error | Typical case |
|---|---|
| `*SyntaxError` | Invalid JSON syntax |
| `*UnmarshalTypeError` | JSON value cannot be assigned to target Go type |
| `*InvalidUnmarshalError` | Non-pointer or nil passed to `Unmarshal` |
| `*UnsupportedTypeError` | Unsupported Go type for `Marshal` |
| `*UnsupportedValueError` | Unsupported value (for example `NaN`/`Inf`) |
| `*MarshalerError` | Error returned by custom `MarshalJSON` |

Example (`errors.As` to stdlib type):

```go
import (
	"errors"
	stdjson "encoding/json"

	json "github.com/velox-io/json"
)

var data []byte
var v User
if err := json.Unmarshal(data, &v); err != nil {
	var te *stdjson.UnmarshalTypeError
	if errors.As(err, &te) {
		// handle type mismatch
	}
}
```

## Development

Native C VM encoding is currently linked on:

- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`

To regenerate/update native artifacts:

```bash
make gen
```

## Testing

```bash
make test
```

Fuzz targets:

```bash
make fuzz
# or customize duration
make fuzz FUZZ_TIME=2m
```

## Lint / formatting

```bash
make fmt
make lint
```


## Help improve Velox

Velox is already usable, but it is still evolving.
If you run Velox on real workloads, benchmark reports,
edge-case JSON samples, and focused PRs are highly valuable.

## License

[MIT](./LICENSE)
