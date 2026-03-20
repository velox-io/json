# How Velox JSON Works

Every Go developer has used `encoding/json`. It's well designed — clean API, predictable behavior, tight integration with Go's type system. But its implementation favors correctness and maintainability over speed, and the performance is modest.

Velox set out to see how fast JSON serialization and deserialization can get while keeping the same API semantics. The two problems are different enough that they ended up on separate technical paths.

---

## Unmarshal: Single-Pass Scanning in Pure Go

### Why Not SIMD Pre-Scanning?

simdjson takes an elegant approach: use SIMD instructions to make a first pass over the input, locating all quotes, brackets, and commas to build a structural index, then access values on demand. This works extremely well for DOM-style parsing.

But Go's `Unmarshal` does **binding parsing**: it maps JSON directly onto a known Go struct type. The parser has to read every field name byte by byte, match struct tags, and write values into the corresponding memory locations. Every byte gets visited. The structural index built by a pre-scan provides almost no information gain in this scenario — the parser will naturally encounter those structural characters during binding anyway.

Velox initially tried this "scan then bind" architecture and got about halfway through the implementation before profiling showed the pre-scan itself consuming a significant fraction of total time. In practice, the two-pass approach cost roughly 20% more than single-pass scanning. This isn't a problem with simdjson — it's just that binding parsing, as a specific workload, is naturally suited to a single pass.

So Velox uses a single-pass binding parser implemented in pure Go.

### Making Single-Pass Fast

Giving up SIMD pre-scanning doesn't mean giving up optimization. The parser does a number of things within the single-pass framework:

- **Precompiled type metadata**

  The first time a type is parsed, Velox analyzes its struct tags, field layout, and nesting to build a complete set of type metadata (`TypeInfo` / `StructCodec`). Subsequent parses of the same type reuse this metadata directly, eliminating reflection overhead.

- **SWAR bulk processing**

  No SIMD, but Velox makes heavy use of SWAR (SIMD Within A Register) — packing 8 bytes into a `uint64` and using bitwise operations to detect quotes, backslashes, and control characters simultaneously. This applies to both string scanning and number parsing. For example, when scanning a string, 8 bytes are loaded at once and checked for `"`, `\`, or characters below 0x20.

- **Hot/cold path splitting**

  The main parsing function `scanValue()` handles only the most common cases: plain strings, numbers, bools, structs, and slices. Less common situations — fields with the `,string` tag, custom `UnmarshalJSON` methods, `json.RawMessage` — are split off into `scanValueSpecial()` to keep the hot path compact. Some critical code on the hot path is also manually inlined; after extensive testing, this yielded at least a 5% performance improvement — at the cost of making the parser code look a bit "ugly" with noticeable duplication, but it's a worthwhile trade-off here.

- **Minimizing allocations**

  Strings are zero-copy referenced from the input buffer whenever possible. Pointer-typed fields use batch allocators. The goal is to push allocation counts as low as they can go.

---

## Marshal: A Serialization Virtual Machine

The idea behind marshal comes from a simple observation: Go struct fields are laid out contiguously in memory. If we know each field's offset and type, we can walk through them in order, calling the appropriate serialization function for each.

Doesn't that sound like a virtual machine executing bytecode?

Go struct type information is fixed at compile time — it never changes at runtime. So the first time a type is encountered, we can "compile" its field layout into a compact bytecode sequence. Every subsequent serialization of the same type just runs the VM over that bytecode — reading fields instruction by instruction and writing JSON output.

Going further, nested structs and pointers can be "inlined" into the same bytecode stream — `OP_PTR_DEREF` handles pointer dereferencing, for instance — flattening the entire type tree into a linear instruction stream.

Intuitively this should perform well: no reflection, no virtual dispatch, just a tight instruction stream executing over contiguous memory.

### Why Write the VM in C?

The VM could be written in Go, but C has a few practical advantages:

1. **More efficient dispatch.**  Computed goto enables an efficient VM loop — a technique used by many language interpreters.

2. **SIMD friendly.** String encoding (UTF-8 validation, HTML escaping, special character detection) is a serialization hotspot, and C makes it straightforward to use SIMD instructions for acceleration.

The cross-language call itself has a cost (Go → C calling convention conversion). Velox bridges this with Plan9 assembly trampolines, which means the VM runs directly on the goroutine's stack.

### Bytecode Compilation

The compiler (`encvm_compiler.go`) walks struct type metadata and emits an intermediate representation (IR). Each struct field maps to one or two IR instructions: if the field has an `omitempty` tag, an `OP_SKIP_IF_ZERO` (conditional forward jump) is inserted first, followed by the field's serialization instruction. Nested structs are inlined; recursive types (like linked list nodes) generate `OP_CALL` subroutine calls. The compiler also applies specialization — for example, `[]float64` can be handled by a single `OP_SEQ_FLOAT64` instruction instead of a loop.

```
// A simple struct compiles to roughly:
OP_OBJ_OPEN                    // write '{'
OP_SKIP_IF_ZERO  offset=0      // check if field1 is zero
OP_KSTRING       key="name"    // write key + string value
OP_SKIP_IF_ZERO  offset=24     // check if field2 is zero
OP_KINT          key="age"     // write key + int value
OP_OBJ_CLOSE                   // write '}'
OP_RET
```


### VM Execution Model

At runtime, the Go-side `execVM()` passes the bytecode pointer and data base address to the C VM. The VM fetches, dispatches, and executes instructions in a "loop":

- **Primitive types** (`OP_BOOL`, `OP_INT`, `OP_STRING`, etc.): read the value directly from memory, format it, and write to the output buffer.
- **Structural control** (`OP_OBJ_OPEN/CLOSE`, `OP_CALL/RET`): manage JSON hierarchy and field separators.
- **Collection types** (`OP_SLICE_BEGIN/END`, `OP_MAP_STR_ITER`): handle loop iteration on the C side.
- **Conditional jumps** (`OP_SKIP_IF_ZERO`): implement `omitempty` by checking the field's zero-value tag (`ZeroCheckTag`) and skipping output when appropriate.

When the VM encounters something it can't handle — a custom `MarshalJSON` method, a non-empty interface value, or a full buffer — it yields back to Go. Go handles the special case, then lets the VM resume from where it left off. This cooperative Go↔C interaction lets the VM cover the vast majority of common cases without sacrificing any functionality.

### Iterating Swiss Maps Directly in C

Map serialization is an interesting problem. The conventional approach is to use Go's map iterator to retrieve key-value pairs, then serialize each one. But since the VM is already reading memory directly in C, why not do the same for maps?

Go 1.24 introduced swiss maps as the new map implementation. I noticed that its memory layout has remained stable from 1.24 through 1.26, and the layout itself is quite regular — the arrangement of groups, slots, and control bytes is deterministic. Directly traversing this memory structure in C is actually simpler and more stable than exporting Go's map iterator to the C side.

So Velox takes an aggressive approach: for common string-keyed map types like `map[string]string` and `map[string]int`, the VM iterates over the swiss map's internal structure directly in C, bypassing the Go runtime's iterator entirely. This brings a noticeable performance improvement, especially for maps with many entries. Even if Go's map memory layout changes in the future, we can adapt to the new layout or fall back to Go-side iteration — so this isn't a major concern.

### Three VM Variants

To get the best performance across different usage patterns, Velox compiles three VM variants:

| Variant | Condition | Characteristics |
|---------|-----------|----------------|
| **Fast** | Default (no special flags) | Fastest: no indentation, no HTML escaping, no UTF-8 validation |
| **Compact** | HTML escaping or UTF-8 validation enabled | Checks escape characters during string encoding, but no indentation |
| **Full** | Indentation enabled (`MarshalIndent`) | Full feature set: indentation + HTML escaping + UTF-8 validation |

The appropriate variant is selected automatically at runtime based on the caller's options. Most of the time it's the Fast variant — it even skips HTML escaping of `<>&` in strings, which isn't needed in many JSON use cases.

The C VM currently supports four platforms: darwin/arm64, linux/amd64, linux/arm64, and windows/amd64. On unsupported platforms, Velox automatically falls back to a pure Go marshal path — fully functional, just without native acceleration.

Build artifacts for each platform are precompiled `.syso` files that link directly into the Go binary. Users don't need a C compiler installed.


## Final Thoughts

Looking back, Velox's unmarshal and marshal took two very different paths.

**Unmarshal is input-driven.**

The parser doesn't know what the next byte will be and must make decisions byte by byte. Control flow flexibility matters here — Go's conditional branches, early returns, and error handling are a natural fit. Pre-scanning adds redundant work in binding parsing. Single-pass scanning in pure Go, combined with SWAR and carefully designed data structures, delivers solid results for most workloads.

**Marshal is type-driven.**

The output structure is entirely determined by the Go type, known at compile time. This lets us precompile "type tree traversal" into compact bytecode and execute it with a simple, efficient VM. C's advantages here are tangible: better instruction dispatch, direct memory access, and SIMD-accelerated string encoding.

Benchmark results show both paths performing well. On Linux AMD EPYC, unmarshal is 7–9× faster than `encoding/json`, marshal is 2.5–5.5× faster; numbers are higher on Apple M4 Pro. Allocation-wise, most scenarios need just 1 allocation versus 12+ for the standard library. Compared to other excellent JSON libraries in the community (sonic, go-json, easyjson, etc.), Velox shows an edge on most test datasets — detailed numbers are in the `docs/benchmarks/` directory.

Velox is still evolving. The pure Go unmarshal path can naturally benefit from Go compiler and runtime improvements; the marshal VM architecture also has plenty of room to explore — more specialized instructions, more aggressive SIMD utilization.

If you're interested in the implementation details, the code is at [github.com/velox-io/json](https://github.com/velox-io/json). Feel free to read, try it out, or open an issue.
