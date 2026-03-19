# Velox JSON (vjson)

Velox is a high-performance JSON encoder/decoder for Go.

- Marshal uses a native C JSON VM backend (`native/encvm`) behind the same Go API
- Unmarshal is implemented in pure Go with a single-pass decoder and zero-copy strings
- Low-allocation design with parser/marshaler pooling
- Fast struct handling with cached type metadata

## Design

Velox is currently focused on **binding-style** JSON ↔ typed Go value conversion (`Unmarshal`/`Decode` + `Marshal`/`Encode`), targeting strong throughput and a low-allocation profile with tunable trade-offs between processing speed and GC pressure.

- **Multi-platform first.** Velox keeps one public API across platforms. Native marshal acceleration is available on `darwin/arm64`, `linux/amd64`, and `linux/arm64`, while other platforms automatically use the Go fallback path.
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

## Performance & benchmarks

See [detailed benchmark results](docs/benchmark.md).

### Highlights

- Benchmark coverage includes Tiny/Small/EscapeHeavy/KubePods/Twitter payloads, from ~80 B to ~600 KB (`docs/benchmark.md`).
- Marshal benchmarks compare `Velox` with `encoding/json`, `sonic`, `go-json`, and selected `easyjson` cases (`benchmark/b1_marshal_test.go`).
- Unmarshal/Decoder benchmarks include single-thread and parallel scenarios (`benchmark/b1_unmarshal_test.go`, `benchmark/b3_decoder_test.go`).
- In the published benchmark charts, Velox Marshal shows top-tier results on Apple M4 Pro (ARM64), with comparative AMD/Intel (x86_64) results published in the same report.
- Decoder buffer prediction design and rationale are documented in `docs/decoder_buffer_prediction.md`.

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

On other platforms, Velox keeps the same public API and automatically falls back to the Go struct encode path.

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

Velox is already usable in performance-sensitive services, but it is still evolving.

We now maintain a public roadmap in [ROADMAP.md](./ROADMAP.md), and current high-priority contribution areas are:

- `map[string]T` marshal optimization

If you run Velox on real workloads, benchmark reports, edge-case JSON samples, and focused PRs are highly valuable.

## License

[MIT](./LICENSE)
