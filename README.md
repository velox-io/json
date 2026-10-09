# Velox

Velox is a high-performance JSON library for Go.

## Quick start

```go
import vjson "github.com/velox-io/json"

type User struct {
    Name  string   `json:"name"`
    Roles []string `json:"roles"`
}

var u User
err := vjson.Unmarshal([]byte(`{"name":"alice","roles":["admin"]}`), &u)

out, err := vjson.Marshal(u) // {"name":"alice","roles":["admin"]}
```

The import path ends in `json`, but the package name is `vjson`.

## Performance

![](docs/benchmarks/linux-amd64/unmarshal-4.svg)
![](docs/benchmarks/linux-amd64/marshal-4.svg)

Each library runs in its fastest decode mode:

- Velox: `vjson.Unmarshal(data, &v)` (zero-copy by default)
- GoJSON: `gojson.UnmarshalOf(data, &v, gojson.DecodeNoCopyString())`
- Sonic: `sonic.ConfigFastest.UnmarshalFromString(s, &v)` (string input avoids the initial copy)

[docs/benchmarks](docs/benchmarks).

## Design

Velox focuses on **binding-style** conversion between JSON and typed Go values (`Unmarshal` and `Marshal`), targeting high throughput with few allocations. It uses native acceleration on supported platforms and pure Go elsewhere. Requires Go 1.24+. See [architecture](docs/arch_2_en.md).

### Options


| Option | Scope | Default | Description |
| --- | --- | --- | --- |
| `AllowInvalidUTF8(bool)` | encode + decode | `true` | `false` makes decode fail on invalid UTF-8 and encode replace it with U+FFFD |
| `EscapeHTML(bool)` | encode | `false` | Escape `<` `>` `&` |
| `EscapeLineTerms(bool)` | encode | `false` | Escape U+2028 / U+2029 |
| `FloatExpAuto(bool)` | encode | `false` | Use scientific notation for `abs(f) < 1e-6` or `abs(f) >= 1e21` |
| `UseNumber(bool)` | decode | `false` | Decode numbers bound to `any` as `json.Number` |
| `RejectUnknownMembers(bool)` | decode | `false` | Error on unknown fields |
| `ZeroCopy(bool)` | decode | `true`| Whether strings alias the caller's buffer |
| `SkipLenient(bool)` | decode | `false` | Skip unbound values, and delimit the raw spans hooks receive, by counting brackets only, without validating them |


Options compose with `Join`. For output close to the standard library:

```go
var opts = vjson.Join(vjson.EscapeHTML(true), vjson.EscapeLineTerms(true), vjson.AllowInvalidUTF8(false), vjson.FloatExpAuto(true))
out, err := vjson.Marshal(v, opts)
```

### Compatibility

- **Invalid UTF-8 on decode is preserved as-is by default.** `AllowInvalidUTF8(false)` makes it an error.
- **Invalid UTF-8 on encode is emitted as-is by default.** `AllowInvalidUTF8(false)` replaces it with U+FFFD, so the output meets RFC 8259's requirement that exchanged text be valid UTF-8.
- **HTML escaping and line terminator escaping are off by default.** They serve HTML/JS embedding scenarios and are not required by the JSON spec, so enable them when needed.
- **Floats uses fixed-point notation by default.** The JSON spec does not require scientific notation; it is off by default for performance.
- **`MarshalJSON` output is written verbatim.** No validation, re-indenting or escaping: it must be compact JSON, and Indent and `EscapeHTML` do not apply to it. See [ROADMAP](ROADMAP.md) for the custom codec extension point.
- **Field names are case-sensitive.** Matching is by exact bytes, with no case-insensitive fallback.
- **Tag options are parsed strictly.** Misspelled options (such as `omitEmpty`, `omit_zero`) are an error; a tag on an embedded field with options other than `embed` is also an error rather than being dropped and promoted.

### zero-copy

`Unmarshal` is zero-copy by default: escape-free strings alias the caller's input buffer. Escaped strings are copied because their decoded bytes differ from the input. The caller must preserve the input's bytes while any decoded value remains reachable:

```go
var pod KubePodList
err := vjson.Unmarshal(src, &pod) // pod's clean strings alias src
```

Pass `vjson.ZeroCopy(false)` when the destination must own its bytes, for example when the input buffer is reused after decoding:

```go
var pod KubePodList
err := vjson.Unmarshal(src, &pod, vjson.ZeroCopy(false)) // pod owns its strings
```

### omitzero

A field is skipped when its value is zero, as decided by an `IsZero() bool`
method if the field type or its pointer implements one, otherwise by `reflect.Value.IsZero`.
Unlike `omitempty`, a nil slice or map is zero while an empty non-nil one is not.

```go
type Event struct {
    Name string    `json:"name"`
    At   time.Time `json:"at,omitzero"` // zero time → key absent
    Tags []string  `json:"tags,omitzero"` // nil → absent; empty non-nil → "tags":[]
}
```

## Extensions

### `Value`

Velox provides the `Value` type for dynamic JSON and partial-access workloads where binding the entire document to a predefined Go type or `map[string]any` would be unnecessary. It is a tape-backed view for navigating parsed JSON directly:

```go
doc, err := vjson.Parse(src)
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
    Code int         `json:"code"`
    Data vjson.Value `json:"data"`
}

var envelope Envelope
err := vjson.Unmarshal(src, &envelope)
```

Tape-backed values have two representation limits:

- The JSON document must be smaller than 4 GiB;

- Decoded string or object key must be smaller than 16 MiB

  > [!TIP]
  > The string length limits apply to the `Value` type, not to ordinary Go `string` fields.

### Reserve unknown keys

A field of type `Value` tagged `json:",embed"` collects every key not matched by a named field:

```go
type Foo struct {
    Name string      `json:"name"`
    Exts vjson.Value `json:",embed"` // unmatched keys land here
}
```

### Polymorphic decoding

JSON has no sum types, but payloads often encode them: a discriminator field names the type, and the matching variant arrives either as a sibling field or unfolded into the host object itself. Velox resolves that choice while scanning, so one pass produces the right Go value instead of a `json.RawMessage` you decode yourself. Two selectors are available:

- `vjson:"variant=<disc>"` picks the case by the value of a string field named `<disc>` on the same struct.
- `vjson:"kindof"` picks the case from the JSON value's own kind.

#### Selecting by a discriminator

The discriminator is a string field of the same struct; the variant field is `any` (or an interface) and holds the decoded case:

```go
type EventEnvelope struct {
    Type string `json:"type"`                       // the discriminator
    Data any    `json:"data" vjson:"variant=type"`  // whose type it selects
}

type User struct {
    Name string `json:"name"`
    Role string `json:"role"`
}

type Product struct {
    Title string `json:"title"`
    Price int    `json:"price"`
}
```

The case table is a struct type: each field is one case, and its type is what gets decoded. Register it once, before the first parse of the host type:

```go
func init() {
    vjson.DefineVariantCases[EventEnvelope, struct {
        user User                 // "user"   → User
        _ Product `case:"e-book"` // "e-book" → Product
    }]()
}
```

The case value is the descriptor field's name, so `user User` declares the case `"user"`. Use a blank field with a `case:"..."` tag when the discriminator value is not a Go identifier, as above. A blank field without a tag is the default case, used when no case matches; without one, an unmatched value is an error.

The call site stays an ordinary `Unmarshal` into the host. Afterwards `env.Data` holds the selected case as its concrete Go type:

```go
var env EventEnvelope
err := vjson.Unmarshal([]byte(`{"type":"user","data":{"name":"Alice","role":"admin"}}`), &env)
// env.Type == "user", env.Data == User{Name: "Alice", Role: "admin"}
```

Consume it with the usual Go idiom for a value of varying type:

```go
switch v := env.Data.(type) {
case User:
    fmt.Println(v.Name, v.Role)
case Product:
    fmt.Println(v.Title, v.Price)
}
```

The discriminator may appear after the value it selects: the scan buffers the value and resolves it when the host object closes. A discriminator carried by the input always wins over whatever the destination already held.

#### Selecting by JSON kind

When there is no discriminator, the value's first token is the answer. Declare the kinds you accept and their Go types:

```go
type Response struct {
    Data any `json:"data" vjson:"kindof"`
}

func init() {
    vjson.DefineKindofCases[Response, struct {
        bool   bool
        number float64
        string string
        array  []User
        object User
    }]()
}
```

`{"data":42.5}` yields `float64(42.5)`, `{"data":{"name":"Alice"}}` yields a `User`, and `{"data":null}` leaves the field nil. A kind you did not declare is an error. This is the tool for schemaless members, for example an error field that is a string in one response and an object in another.

#### Unfolding the case into the host

`json:",embed"` turns the variant into an inline one: instead of consuming one named member, the selected case's fields become members of the host object itself.

This is the shape of a flat tagged union, where the discriminator and the case's fields live in the same object:

```go
type Shape struct {
    Type string `json:"type"`
    Body any    `json:",embed" vjson:"variant=type"`
}

type Circle struct {
    Radius float64 `json:"radius"`
}

type Rect struct {
    Width  float64 `json:"width"`
    Height float64 `json:"height"`
}

func init() {
    vjson.DefineVariantCases[Shape, struct {
        _ Circle `case:"circle"`
        _ Rect   `case:"rect"`
    }]()
}
```

`{"type":"rect","width":3,"height":4}` selects `Rect` and binds `width` and `height` into it, one discriminator driving several host members at once. Inline cases must be structs, and a host has at most one inline variant.

#### Multiple polymorphic fields

A host may carry several sibling variants, each with its own discriminator and its own case set. Register an axis by its Go field name:

```go
type K8sObject struct {
    Kind     string `json:"kind"`
    Spec     any    `json:"spec"   vjson:"variant=kind"`
    Observer string `json:"observer"`
    Report   any    `json:"report" vjson:"variant=observer"`
}

vjson.DefineVariantCasesAt[K8sObject, struct {
    _ PodSpec     `case:"Pod"`
    _ ServiceSpec `case:"Service"`
}]("Spec")

vjson.DefineVariantCasesAt[K8sObject, struct {
    _ KubeletReport   `case:"kubelet"`
    _ SchedulerReport `case:"scheduler"`
}]("Report")
```

`DefineVariantCases` is the host-wide fallback used by every axis that has no field-specific registration.

As an alternative to registration, a host may declare its descriptor as the parameter of a `JSONVariantCases` method (or `JSONKindofCases` for kindof). The method is never called; it only carries the type. Pick one form per host, not both.

Example detail: [partial](examples/unmarshal/partial), [poly](examples/unmarshal/poly).

### Stream

A `vjson.Stream[T]` field consumes a large array element by element instead of materializing it as `[]T`. Declare it with the array's JSON key, register an `OnRead` handler before `Unmarshal`, and the parser hands the handler the elements as it reaches them:

```go
type Response struct {
    Users   vjson.Stream[User] `json:"users"`
    Message string             `json:"message"`
}

var resp Response
resp.Users.OnRead(func(users stream.Scope[User]) error { // stream.Scope[T] from github.com/velox-io/json/stream
    for item := range users.Iter() {
        if err := item.Decode(); err != nil { // binds one element
            return err
        }
        u := item.Target()
        fmt.Println(u.ID, u.Name)
    }
    return nil
})

if err := vjson.Unmarshal(src, &resp); err != nil {
    return err
}
fmt.Println(resp.Message) // fields around the stream bind as usual
```

Peak memory is one batch of elements rather than the whole array. Streaming here is element level, not byte level.
Example detail: [stream](examples/unmarshal/stream).

## Roadmap

See [ROADMAP.md](ROADMAP.md) for planned work.


## Acknowledgements

Thanks to the [simdjson](https://github.com/simdjson/simdjson) for the work on high-performance JSON parsing.

## License

[MIT](./LICENSE)
