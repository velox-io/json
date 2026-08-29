# Velox Deserialization Architecture

The performance limitations of Go's standard JSON library have led the community to develop a large number of "high-performance" alternatives. In [Velox 0.1](./architecture.md), we implemented a serialization virtual machine in C and a recursive-descent parser in Go, achieving fast object serialization and deserialization respectively. Deserialization in Velox 0.1 reached respectable performance through a collection of isolated, sometimes inelegant techniques such as manual inlining. Those optimizations certainly worked, but they also revealed the absence of a coherent architectural direction for building a high-performance JSON library. In Velox 0.2, I therefore redesigned the deserialization architecture and substantially improved JSON parsing performance.

## Styles of JSON Libraries

I divide the many JSON libraries in the Go ecosystem into three categories according to the form of their output.

### 1. Lazy Parsing

A representative example is [jsonparser](https://github.com/buger/jsonparser). Rather than constructing the complete result up front, it locates and parses values on demand through an access API. When callers need only a small number of fields, this approach can avoid substantial work while keeping memory usage easy to control.

The tradeoff is that parsing becomes intertwined with application logic. JSON object fields have no semantically defined order. Once callers need to combine multiple fields, handle nested structures, or maintain state across fields, they often have to build an additional object-binding layer themselves.

### 2. DOM Parsing

A DOM parser first converts JSON into a library-defined data structure, which is then accessed through a uniform API instead of being mapped directly into objects in the host language. Because C has no standard reflection mechanism, this design is especially common in C and C++. It is both a practical necessity and a performance choice: a DOM can be written in input order, and when a container's offset or length must be backfilled, the parsing stack makes the corresponding metadata easy to locate. simdjson and yyjson are representative implementations of this approach.

Go has a complete reflection facility, so DOM-style JSON libraries are less common. Notable examples include simdjson-go and fastjson. fastjson uses a tree-shaped DOM. During parsing it recognizes only the structure, deferring number parsing and string unescaping until values are accessed. Unlike jsonparser, it does not need to scan the same JSON segment repeatedly when several fields are accessed, which gives it an advantage for this usage pattern.

The internal representation and capabilities of each DOM are also shaped by their intended use cases. simdjson and yyjson use contiguous memory, while fastjson uses a tree structure; yyjson and fastjson support in-place modification. fastjson is most compelling in reuse mode, where a parsed result remains valid only until the next call to `Parse`. This convention gives the DOM a very short lifetime, allowing the parser to reuse all of its internal memory and keep memory overhead during parsing extremely low. Much of fastjson's implementation is optimized around this mode of operation.

### 3. Type Binding

From an application perspective, the first two categories are closer to JSON parsers because they do not directly construct objects in the host language. Type binding uses type or schema information to map JSON directly into target objects. Callers can then continue working with ordinary language constructs, which is why this is also the most common approach in application code. Go's standard `encoding/json` package, Sonic, and go-json all belong to this category.

Go reflection can provide all the type information required to construct an object. Deserialization therefore does not inherently have to be slower than DOM parsing. If type information is processed ahead of time, the parser can write directly into the target object according to its known layout, without repeatedly invoking reflection on the hot path. I prefer to view object binding as the fundamental primitive of JSON parsing: **turning linear data into a graph of discrete objects**. DOM parsing and lazy parsing differ mainly in the output representation and the point at which binding occurs. A DOM binds data into generic objects defined by the library, while lazy parsing defers binding until a value is accessed. From this perspective, the underlying parser need not be constrained by how the upper layers expose the result. It only needs to solve the problem of constructing target objects from JSON.

Velox therefore uses object binding as the foundation of its deserialization architecture and builds other usage models on top of that capability.

## Codec and the Virtual Machine

In Velox 0.1, serialization first obtained type information through Go reflection, compiled it into a compact instruction stream, and then executed those instructions in a VM. The VM even had a subroutine mechanism for handling recursive types. Deserialization, however, still used a recursive-descent parser like most JSON libraries. This approach was direct and easy to understand, but it left serialization and deserialization without a unified model of object binding.

Serialization transforms a discrete object graph into linear data according to object types. Deserialization performs the inverse transformation, constructing an object graph from linear data. JSON has a context-free grammar and can be parsed by a pushdown automaton. Taking this idea one step further, the JSON data itself can be treated as the VM's instruction stream: object begin, object field, array begin, scalar, and container end are all instructions. Parsing consists of executing those instructions to construct the object graph incrementally.

This is the most important premise of the design. The target type is not a second control program; it is merely a set of constraints on how the object graph is constructed. When a Go type is first used for binding, the Go side uses reflection to construct and cache an immutable tree of type information. This tree contains object layouts, field lookup tables, slot classes, and polymorphism metadata. Execution flow then comes from the JSON itself, while the type information only answers where the result of the current instruction should be written. Parsing and object binding are thus unified into a single VM execution.

## The C VM and the Go Runtime

Once deserialization is implemented as a VM, the next question is where that VM should run. C gives us precise control over data layout, register use, and control flow while letting us take full advantage of compiler optimizations. It is therefore a natural choice for a high-performance VM. Deserialization, however, is not a computation isolated from the host language. It ultimately constructs a Go object graph. Along the way it must allocate slice backing arrays, pointer targets, and boxed interface values; write maps; and potentially invoke `json.Unmarshaler`. These operations depend on Go's type system, runtime, or user code and can only be performed on the Go side.

This creates a fundamental conflict. Memory allocated directly by C does not produce objects that the garbage collector can manage according to Go types. Returning to Go for every allocation or map operation, on the other hand, would let cross-language transitions overwhelm the VM's performance gains. More importantly, when the C VM writes directly to the Go heap, it bypasses the write barriers inserted by the Go compiler and may therefore compromise GC safety. The VM must also obey goroutine stack constraints. We can neither treat C as a parser isolated from Go nor treat the Go runtime as an ordinary function library that can be called freely from the C hot path.

High-performance implementations in the community usually avoid this boundary. Pure Go parsers naturally operate within the runtime's rules. Sonic's JIT generates native code for each target type so that execution integrates directly into the Go runtime. Its JIT primarily solves code generation and runtime integration; it is not an optimizing compiler driven by runtime profiles. There is still no ready-made integration pattern for continuously constructing Go objects from a C execution loop.

In Velox, I chose to let the C VM construct Go objects directly and consolidate the operations that require the Go runtime into a small set of yield types. When the VM encounters one of these operations, it saves its phase, cursors, container stack, pending arguments, and other state in the machine, then returns control to Go. After Go performs the required operation, such as allocating memory or invoking user code, execution resumes in the C VM at the corresponding phase. No state is preserved through the C stack.

This mechanism divides responsibility between native execution and Go runtime services. The two sides jointly advance a single parse and transfer control only at the boundary between their responsibilities. Native code handles scanning, lookup, state-machine dispatch, and object writes. Go handles type analysis, memory allocation, and runtime operations.

Yielding solves the problem of accessing the Go runtime from the C VM, but leaving the VM too frequently would still erase the benefits of native execution. Velox therefore requests resources in batches rather than one object at a time. Go prepares a block of memory, and the C VM constructs objects continuously within it, yielding only when the entire block has been consumed. Most parsing time consequently remains inside the C VM, while the Go runtime intervenes only to replenish resources or perform operations that inherently require Go.

## GC Safety

When the C VM writes directly into Go objects, the compiler does not insert write barriers for those pointer writes. Identifying every pointer write in C and explicitly invoking a barrier would amount to moving part of the Go compiler into the VM, while also imposing a per-field cost on the object-binding hot path. Instead of adding individual write barriers in C, I make object-graph construction GC-safe as a whole. The design relies on the following properties:

1. Go and C use compatible memory layout and alignment rules, allowing C to calculate field addresses from type information.
2. Aligned pointer writes are atomic, so the concurrent GC cannot observe a partially written pointer.
3. C writes only to managed memory or target objects, and the allocator keeps that memory reachable to the GC throughout parsing.
4. The Go GC marks underlying memory blocks. Once a block is marked live, the GC continues scanning all pointers within it according to the block's type layout.
5. The hybrid write barrier shades the referent of an overwritten pointer and, when the current stack is grey, the referent of the newly installed pointer. When parsing completes, clearing the temporary references in Go causes their former referents to be marked live.
6. `runtime.KeepAlive` ensures that the input and target objects outlive the entire parsing operation.

Given these conditions, I divide object-graph construction and publication into two phases. During parsing, temporary references on the Go side keep all relevant memory reachable. Even if object relationships newly established by C are not observed by the current GC cycle, their targets cannot be reclaimed prematurely. Once parsing completes, Go clears the temporary references, causing the hybrid write barrier to mark the referenced memory as live.

Barrier-free writes from C can affect only the GC cycle that is active at the time. By the next cycle, the object relationships are stable and will be scanned normally. As long as the construction phase preserves reachability and the publication phase completes marking for the current cycle, the graph remains GC-safe. The unit of GC protection therefore shifts from individual pointer writes to the memory blocks containing the object graph, keeping write barriers off the C VM's hot path.

## SIMD Pre-scan

The VM also needs a clean way to obtain instructions from raw JSON. Raw JSON interleaves whitespace, string contents, and characters that actually affect the syntax. If the VM identifies all of them while executing, string scanning and syntax processing become entangled in the same loop. Following simdjson, we therefore perform an initial scan that identifies quotes, escapes, and structural characters, extracting string starts, structural characters, and scalar starts into a structural index. In effect, this decouples instruction fetch from VM execution.

This separation is not free, the SIMD pre-scan must read the entire input once and write the structural index, which the VM then reads back. When JSON contains substantial whitespace, the index lets the VM skip many irrelevant bytes. In compact JSON, however, structural positions are already dense, so the extra scan and index traffic can become pure overhead. This cost is particularly visible in simdjson on compact inputs and is a major reason why yyjson is often faster for such data, since the state-machine execution itself cannot be parallelized.

Velox uses a SIMD pre-scan because it turns JSON into a clean instruction stream and gives the program a clearer architecture, not because adding SIMD to JSON processing is inherently faster. The same scan that produces the structural index can also validate UTF-8 and escapes, greatly simplifying string handling inside the state machine.

The SIMD pre-scan therefore separates string recognition from syntax execution and presents the VM with a clean instruction stream.

## Summary

The central idea behind Velox 0.2 is to treat object binding as the fundamental primitive of JSON deserialization. Reflection is used ahead of time to derive immutable type information for the C VM, primarily describing memory allocation and object layout. The C VM parses JSON and constructs objects, while the Go runtime intervenes only when memory must be allocated or Go-specific logic must run. Through this architecture, the C VM can write directly into Go objects without executing GC write barriers on the hot path, enabling high-performance JSON deserialization.

Velox's memory management is similar to an arena allocator, with memory managed in blocks. A small number of live objects may therefore extend the lifetime of an entire block. Deserialized objects should not be retained in long-lived caches. When data must be cached for an extended period, the better practice is to construct independently owned domain objects rather than retain deserialized objects backed by the allocator.

The architecture described above establishes the fundamental conditions that make this approach viable. Many implementation details, including field lookup, stack management, and floating-point parsing, also affect deserialization performance, but they are not foundational to the architecture and are therefore omitted here. The greater value of this design is that it places parsing, object construction, and cooperation with the Go runtime on a unified foundation, establishing a coherent basis for further optimization and new capabilities.
