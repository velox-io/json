# Velox JSON (vjson)

Velox is a high-performance JSON encoder/decoder for Go.

- Single-pass decoding into Go values
- Pure Go implementation (no CGO), consistent across platforms
- Zero-copy strings when possible
- Low-allocation, GC-friendly design
- Fast struct decoding with cached type metadata
- Streaming `Decoder` API for `io.Reader`

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

#### Indented output

```go
b, err := json.MarshalIndent(&u, "", "  ")
```

#### Append to an existing buffer

```go
dst := make([]byte, 0, 1024)

dst, err := json.AppendMarshal(dst, &u)
```

#### Marshal options

- `WithEscapeHTML()` / `WithNoEscapeHTML()`

Example:

```go
b, err := json.Marshal(&u, json.WithEscapeHTML())
```

## Performance & benchmarks

See [detailed benchmark results](docs/benchmark.md).

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


## License

[MIT](./LICENSE)
