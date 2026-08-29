# Velox

Velox is a high-performance JSON library for Go.

## Performance

![](docs/benchmarks/linux-amd64-amd-epyc7k62/unmarshal-1.svg)
![](docs/benchmarks/linux-amd64-amd-epyc7k62/marshal-1.svg)

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

By default, `Parse` copies the string data needed by the returned `Value`, so `src` may be modified or reused after parsing. If retaining the input buffer is acceptable, zero-copy mode can reduce this copying:

```go
padded := json.Pad(src)
doc, err := json.ParsePadded(padded, json.WithZeroCopy())
```

Do not modify `padded` or any slice sharing its backing array until you have finished using `doc` and every `Value` obtained from it.

Tape-backed values have two representation limits:

- the JSON document must be smaller than 4 GiB because source offsets are 32-bit;
- each decoded string or object key must be smaller than 16 MiB because string lengths are 24-bit.

These string limits apply to `Value`, not to ordinary Go `string` fields decoded directly by `Unmarshal`.

## Extensions

**Reserve unknown keys.** A `Value` field tagged `json:",embed"` collects every key not matched by a named field, zero-copy:

```go
type Foo struct {
    Name string    `json:"name"`
    Exts json.Value `json:",embed"` // unmatched keys land here
}
```

**Polymorphic decoding.** A `vjson:"variant=<disc>"` field binds to the concrete Go type selected by a sibling discriminator field; `vjson:"kindof"` selects the case by the JSON value's kind. Case sets are declared explicitly with `vbind.DefineVariantCases`:

```go
type EventEnvelope struct {
    Type string `json:"type"`
    Data any    `json:"data" vjson:"variant=type"` // "user"→User, "product"→Product
}
```

Runnable examples: [examples/unmarshal/partial](examples/unmarshal/partial), [examples/unmarshal/poly](examples/unmarshal/poly).

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned work.


## Acknowledgements

Thanks to the [simdjson](https://github.com/simdjson/simdjson) contributors for their work on high-performance JSON parsing.

## License

[MIT](./LICENSE)
