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

Benchmark environment: **Apple M4 Pro**, Go 1.24, `GOMAXPROCS=14`

### Unmarshal (single-thread)

| Dataset | Library | ns/op | MB/s | B/op | allocs/op |
|---------|---------|------:|-----:|-----:|----------:|
| EscapeHeavy | Sonic | 2890 | 1416 | 6365 | 10 |
| EscapeHeavy | **Velox** | **2466** | **1659** | **3244** | **4** |
| KubePods | Sonic | 21481 | 1188 | 39445 | 171 |
| KubePods | **Velox** | **14518** | **1758** | **12579** | **99** |
| Twitter | Sonic | 440115 | 1435 | 810546 | 1525 |
| Twitter | **Velox** | **269002** | **2348** | **167477** | **1018** |

### Unmarshal (parallel, 14 goroutines)

| Dataset | Library | ns/op | MB/s | B/op | allocs/op |
|---------|---------|------:|-----:|-----:|----------:|
| EscapeHeavy | Sonic | 510 | 8021 | 7147 | 10 |
| EscapeHeavy | **Velox** | **397** | **10314** | **3119** | **4** |
| KubePods | Sonic | 2747 | 9292 | 40974 | 171 |
| KubePods | **Velox** | **2363** | **10807** | **12579** | **99** |
| Twitter | Sonic | 52974 | 11927 | 825118 | 1525 |
| Twitter | **Velox** | **31784** | **19870** | **167477** | **1018** |

### Reproduce

```bash
cd benchmark
go test -bench="Benchmark_(Parallel_)?(EscapeHeavy|KubePods|Twitter)_(Velox|Sonic)" -benchmem . -count=3
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


## License

[MIT](./LICENSE)
