# Velox

Velox is a high-performance JSON library for Go.

## Performance

![](docs/benchmarks/linux-amd64/unmarshal-2.svg)
![](docs/benchmarks/linux-amd64/marshal-2.svg)

[docs/benchmarks](docs/benchmarks).

## Design

Velox focuses on **binding-style** conversion between JSON and typed Go values (`Unmarshal` and `Marshal`), targeting high throughput with few allocations. See [architecture](docs/arch_2_en.md).

## Compatibility

Velox follows `encoding/json` semantics on the common path:

- field tags: custom names, `-`, `,string`, `,omitempty`
- anonymous (embedded) structs, pointers, `json.Number`, `json.RawMessage`
- `json.Marshaler`/`json.Unmarshaler` and `encoding.TextMarshaler`/`TextUnmarshaler`
- boxing into `any` (`[]any`, `map[string]any`)
- unmarshal errors can be inspected with `errors.As` against the `encoding/json` error types

Deliberate differences:

- **Case-sensitive field matching.**

  For performance, Velox deliberately matches field names by exact bytes rather than performing the case-insensitive matching supported by `encoding/json`.


### Requirements

- Golang Version: 1.24+
- Platform: `darwin/arm64`, `linux/amd64`, `linux/arm64`, `windows/amd64`.

## `json.Value`

Velox provides `Value` for dynamic JSON and partial-access workloads where binding the entire document to a predefined Go type or `map[string]any` would be unnecessary. It is a tape-backed view for navigating parsed JSON directly:

```go
import json "github.com/velox-io/json"

doc, err := json.Parse(src)
if err != nil {
    return err
}

name, ok := doc.GetString("user", "name")
items := doc.Get("items")
first := items.Index(0)
id, ok := first.GetInt("id")
```

A `Value` can also appear anywhere in a typed destination and is populated directly by `Unmarshal`:

```go
type Envelope struct {
    Code int        `json:"code"`
    Data json.Value `json:"data"`
}

var envelope Envelope
err := json.Unmarshal(src, &envelope)
```

Tape-backed values have two representation limits:

- the JSON document must be smaller than 4 GiB because source offsets are 32-bit;
- each decoded string or object key must be smaller than 16 MiB because string lengths are 24-bit.

These string limits apply to `Value`, not to ordinary Go `string` fields decoded directly by `Unmarshal`.

## Zero-copy decoding

`Unmarshal` is zero-copy by default: escape-free strings alias the caller's input buffer, skipping the per-string copies through the internal arena. Escaped strings still decode through the arena. The caller must preserve the input's bytes while any decoded value remains reachable:

```go
var pod KubePodList
err := json.Unmarshal(src, &pod) // pod's clean strings alias src
```

Pass `json.WithZeroCopy(false)` when the destination must own its bytes, for example when the input buffer is reused after decoding.

## Extensions

- **Reserve unknown keys.**

  A `Value` field tagged `json:",embed"` collects every key not matched by a named field:

  ```go
  type Foo struct {
      Name string    `json:"name"`
      Exts json.Value `json:",embed"` // unmatched keys land here
  }
  ```

- **Polymorphic decoding.** 

  1. A `vjson:"variant=<disc>"` field binds to the concrete Go type selected by a discriminator field.

  2. A `vjson:"kindof"` field selects the case by the JSON value's kind.

  ```go
  type EventEnvelope struct {
      Type string `json:"type"`
      Data any    `json:"data" vjson:"variant=type"` // "user"→User, "product"→Product
  }

  type User struct {
  	Name string `json:"name"`
  	Role string `json:"role"`
  }

  type Product struct {
  	Title string `json:"title"`
  	Price int    `json:"price"`
  }

  func init() {
	vjson.DefineVariantCases[EventEnvelope, struct {
		user    User
		product Product
	}]()
  }
  ```

Example detail: [partial](examples/unmarshal/partial), [poly](examples/unmarshal/poly).

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned work.


## Acknowledgements

Thanks to the [simdjson](https://github.com/simdjson/simdjson) for the work on high-performance JSON parsing.

## License

[MIT](./LICENSE)
