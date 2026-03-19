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
_Marshal_Tiny-16           209.6n ± ∞ ¹    544.8n ± ∞ ¹  +159.92% (p=0.008 n=5)    238.0n ± ∞ ¹  +13.55% (p=0.008 n=5)
_Marshal_Small-16          502.8n ± ∞ ¹   1738.0n ± ∞ ¹  +245.66% (p=0.008 n=5)    611.5n ± ∞ ¹  +21.62% (p=0.008 n=5)
_Marshal_EscapeHeavy-16    1.817µ ± ∞ ¹    7.279µ ± ∞ ¹  +300.61% (p=0.008 n=5)    3.038µ ± ∞ ¹  +67.20% (p=0.008 n=5)
_Marshal_KubePods-16       6.882µ ± ∞ ¹   41.895µ ± ∞ ¹  +508.76% (p=0.008 n=5)   12.939µ ± ∞ ¹  +88.01% (p=0.008 n=5)
_Marshal_Twitter-16        160.1µ ± ∞ ¹    794.1µ ± ∞ ¹  +395.89% (p=0.008 n=5)    185.5µ ± ∞ ¹  +15.82% (p=0.008 n=5)
_Marshal_TwitterTyped-16   94.33µ ± ∞ ¹   617.80µ ± ∞ ¹  +554.92% (p=0.008 n=5)   128.92µ ± ∞ ¹  +36.67% (p=0.008 n=5)
_Marshal_MapAny-16         49.01µ ± ∞ ¹   173.20µ ± ∞ ¹  +253.37% (p=0.008 n=5)    62.77µ ± ∞ ¹  +28.06% (p=0.008 n=5)
geomean                    7.172µ          30.50µ        +325.24%                  9.785µ        +36.43%
```

* Environment: **Apple M4 Pro**

```
goos: darwin
goarch: arm64
pkg: dev.local/benchmark
cpu: Apple M4 Pro
                                 │    Velox     │                StdJSON                │                 Sonic                 │
                                 │    sec/op    │    sec/op      vs base                │    sec/op      vs base                │
_Unmarshal_Tiny-14                 88.58n ± ∞ ¹   710.10n ± ∞ ¹   +701.65% (p=0.008 n=5)   241.00n ± ∞ ¹  +172.07% (p=0.008 n=5)
_Unmarshal_TinyCompact-14          80.24n ± ∞ ¹   656.60n ± ∞ ¹   +718.30% (p=0.008 n=5)   221.70n ± ∞ ¹  +176.30% (p=0.008 n=5)
_Unmarshal_Small-14                490.5n ± ∞ ¹   3848.0n ± ∞ ¹   +684.51% (p=0.008 n=5)   1128.0n ± ∞ ¹  +129.97% (p=0.008 n=5)
_Unmarshal_SmallCompact-14         387.9n ± ∞ ¹   3002.0n ± ∞ ¹   +673.91% (p=0.008 n=5)    981.4n ± ∞ ¹  +153.00% (p=0.008 n=5)
_Unmarshal_EscapeHeavy-14          2.081µ ± ∞ ¹   17.267µ ± ∞ ¹   +729.75% (p=0.008 n=5)    2.904µ ± ∞ ¹   +39.55% (p=0.008 n=5)
_Unmarshal_EscapeHeavyCompact-14   1.763µ ± ∞ ¹   13.248µ ± ∞ ¹   +651.45% (p=0.008 n=5)    2.546µ ± ∞ ¹   +44.41% (p=0.008 n=5)
_Unmarshal_KubePods-14             13.79µ ± ∞ ¹   124.28µ ± ∞ ¹   +801.33% (p=0.008 n=5)    21.92µ ± ∞ ¹   +58.94% (p=0.008 n=5)
_Unmarshal_KubePodsCompact-14      10.94µ ± ∞ ¹    80.86µ ± ∞ ¹   +639.41% (p=0.008 n=5)    18.86µ ± ∞ ¹   +72.50% (p=0.008 n=5)
_Unmarshal_Twitter-14              226.5µ ± ∞ ¹   2509.5µ ± ∞ ¹  +1008.05% (p=0.008 n=5)    456.5µ ± ∞ ¹  +101.56% (p=0.008 n=5)
_Unmarshal_TwitterCompact-14       198.4µ ± ∞ ¹   1983.2µ ± ∞ ¹   +899.37% (p=0.008 n=5)    394.1µ ± ∞ ¹   +98.59% (p=0.008 n=5)
_Unmarshal_TwitterTyped-14         189.7µ ± ∞ ¹   1977.2µ ± ∞ ¹   +942.41% (p=0.008 n=5)    376.7µ ± ∞ ¹   +98.60% (p=0.008 n=5)
geomean                            4.155µ          35.75µ         +760.29%                  8.262µ         +98.84%

                         │    Velox     │                StdJSON                │                 Sonic                 │
                         │    sec/op    │    sec/op      vs base                │    sec/op      vs base                │
_Marshal_Tiny-14           63.40n ± ∞ ¹   163.70n ± ∞ ¹  +158.20% (p=0.008 n=5)   199.00n ± ∞ ¹  +213.88% (p=0.008 n=5)
_Marshal_Small-14          163.5n ± ∞ ¹    589.9n ± ∞ ¹  +260.80% (p=0.008 n=5)    794.2n ± ∞ ¹  +385.75% (p=0.008 n=5)
_Marshal_EscapeHeavy-14    999.3n ± ∞ ¹   2842.0n ± ∞ ¹  +184.40% (p=0.008 n=5)   3893.0n ± ∞ ¹  +289.57% (p=0.008 n=5)
_Marshal_KubePods-14       2.840µ ± ∞ ¹   13.946µ ± ∞ ¹  +391.06% (p=0.008 n=5)   20.195µ ± ∞ ¹  +611.09% (p=0.008 n=5)
_Marshal_Twitter-14        44.33µ ± ∞ ¹   266.53µ ± ∞ ¹  +501.22% (p=0.008 n=5)   224.23µ ± ∞ ¹  +405.80% (p=0.008 n=5)
_Marshal_TwitterTyped-14   35.30µ ± ∞ ¹   213.61µ ± ∞ ¹  +505.18% (p=0.008 n=5)   176.12µ ± ∞ ¹  +398.97% (p=0.008 n=5)
_Marshal_MapAny-14         8.673µ ± ∞ ¹   57.480µ ± ∞ ¹  +562.75% (p=0.008 n=5)   58.892µ ± ∞ ¹  +579.03% (p=0.008 n=5)
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
