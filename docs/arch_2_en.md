# Velox Deserialization Architecture

The performance limitations of Go's standard JSON library have led the community to develop a large number of "high-performance" alternatives. In [Velox 0.1](./architecture.md), we implemented a serialization virtual machine in C and a recursive-descent parser in Go, achieving fast object serialization and deserialization respectively. Deserialization in Velox 0.1 reached respectable performance through a collection of isolated, sometimes inelegant techniques such as manual inlining. These optimizations were effective, but there was no holistic design for building a high-performance JSON library. In Velox 0.2, I therefore redesigned the deserialization architecture, and Velox achieved a substantial improvement in JSON parsing performance.

## Styles of JSON Libraries

I divide the many JSON libraries in the Go ecosystem into three categories according to the form of their output.

### 1. Lazy Parsing

A representative example is [jsonparser](https://github.com/buger/jsonparser). Rather than constructing the complete result up front, it locates and parses values on demand through an access API. When callers need only a small number of fields, this approach can avoid substantial work while keeping memory usage easy to control. The tradeoff is that parsing becomes intertwined with application logic. Because the JSON specification imposes no ordering on object fields, callers that need to combine multiple fields, handle nested structures, or maintain state across fields often have to add an object-binding layer themselves.

### 2. DOM Parsing

A DOM parser first converts JSON into a library-defined data structure, which is then accessed through a uniform API. Because C has no standard reflection mechanism, this design is quite common in C and C++. In C, it is both a practical necessity and a performance choice: a DOM can be written sequentially as parsing proceeds, and even when a container's position or length must be backfilled to accelerate later access, the state recorded on the parsing stack makes the location to update easy to find. simdjson and yyjson are representative implementations of this approach.

Go has a complete reflection facility, so DOM-style JSON libraries are less common. Notable examples include [simdjson-go](https://github.com/minio/simdjson-go) and [fastjson](https://github.com/valyala/fastjson). fastjson uses an internal tree structure. During parsing it recognizes only the structure, deferring number parsing and string unescaping until values are accessed. Unlike jsonparser, it does not need to parse the same JSON syntax repeatedly when accessing multiple fields, which gives it an advantage when the same JSON data is accessed independently multiple times.

Different libraries tailor their internal DOM formats to their intended use cases. For example, yyjson and fastjson support in-place modification, whereas simdjson's tape does not. fastjson is most compelling in reuse mode: a parser object is reused, and each result it produces remains valid only until the next call to `Parse`. This convention gives the DOM a very short lifetime, allowing the parser to reuse all of its internal memory and keep memory overhead during parsing extremely low. Much of fastjson's implementation is optimized around this mode of operation.

### 3. Type Binding

From an application perspective, the first two styles are closer to JSON parsing than deserialization because they do not directly construct objects in the host language. Type-binding libraries use schema information about the target object to map JSON directly into objects in the language. Callers can then work with the data using ordinary host-language syntax, making this the most common style in application code. Go's standard `encoding/json` package, Sonic, and go-json all belong to this category of parser.

Go reflection can provide all the type information required to construct an object. Deserialization therefore does not inherently have to be slower than DOM parsing. If type information is processed ahead of time, the parser can write directly into the target object according to its known layout, avoiding inefficient reflection operations on the parsing hot path and enabling high-performance parsing.

In fact, I prefer to view object binding as the fundamental primitive of JSON parsing: **transforming linear data into a graph of discrete objects**. Lazy parsing and DOM parsing differ only in the output representation and the point at which binding occurs. A DOM binds data into generic objects defined by the library, while lazy parsing defers binding until a value is accessed. From this perspective, the underlying parser need not be constrained by the usage model exposed by the upper layers. It only needs to solve the problem of constructing target objects from JSON.

Velox therefore uses object binding as the foundation of its deserialization architecture and adds support for other usage models on top of that capability.

## Codec and the Virtual Machine

In Velox 0.1, serialization first obtained type information through Go reflection, compiled it into a compact instruction stream, and then executed those instructions in a VM. The VM even had a subroutine mechanism for handling recursive types. Deserialization, however, still used a recursive-descent parser like most JSON libraries. This approach was direct and easy to understand, but from the perspective of object binding, it left serialization and deserialization without a unified architectural foundation.

Serialization transforms a discrete object graph into linear data according to object types. Deserialization performs the inverse transformation, constructing an object graph from linear data. JSON has a context-free grammar and can be parsed by a pushdown automaton. In principle, the JSON data itself can therefore be treated as a VM instruction stream: object begin, object field, array begin, scalar, container end, and so on. Parsing consists of executing these instructions to construct the object graph incrementally.

This idea is the most important premise of Velox's parsing design. JSON data provides the parsing VM's instructions, while the target type constrains how the object graph is constructed.

When Velox parses JSON into a Go type, the Go side uses reflection to construct and cache an immutable tree of type information. This tree contains object layouts, field lookup tables, memory allocators, and other descriptors. Execution flow comes from the JSON itself, while the type information only needs to specify where the result of the current "instruction" should be written. Velox can therefore support JSON parsing and object binding with the same parsing VM.

## The C VM and the Go Runtime

Once deserialization is implemented as a VM, the next question is where that VM should run. C gives us precise control over data layout, register use, and control flow while letting us take full advantage of compiler optimizations. It is therefore a natural choice for a high-performance VM. Deserialization, however, is not a computation isolated from the host language. It ultimately constructs a Go object graph. Along the way it must allocate memory, such as slice backing arrays, pointer targets, and boxed interface values; write maps; and potentially invoke a user implementation of `json.Unmarshaler`. These operations can only be performed on the Go side.

This creates a fundamental conflict. Memory allocated directly by C does not produce objects that the garbage collector can manage according to Go types. Returning to Go for every allocation or map operation, on the other hand, would let cross-language transitions overwhelm the VM's performance gains. More importantly, when the C VM writes directly to the Go heap, it bypasses the write barriers inserted by the Go compiler and may therefore compromise GC safety. The VM must also obey goroutine stack constraints. We can neither treat the C parsing VM as a parser isolated from Go nor treat the Go runtime as an ordinary function library that can be called freely from C.

High-performance implementations in the community usually avoid this problem. Pure Go parsers naturally operate within the runtime's rules. Sonic's JIT generates native code for each target type so that execution integrates directly into the Go runtime. The core purpose of Sonic's JIT is in fact to solve this integration problem, rather than to optimize through just-in-time compilation itself. There is no ready-made integration pattern in the community for continuously constructing Go objects while executing in C.

My approach in Velox is to let the C parser construct Go objects directly while consolidating operations that only the Go runtime can perform into a few kinds of yield. When the parser encounters such an operation, it saves its phase, cursors, container stack, pending arguments, and other state in the VM, then exits back to Go. After Go performs the required operation, such as allocating memory or invoking user code, execution resumes in the C VM at the corresponding phase. The entire process is independent of state preserved on the C stack.

This mechanism divides responsibility between native execution and Go runtime services. The two sides jointly advance a single parse and exchange control only at the boundary between their responsibilities. Native code handles scanning, lookup, state-machine dispatch, and object writes. Go handles type analysis, memory allocation, and runtime operations.

Yielding solves the problem of accessing the Go runtime from the C VM, but leaving the VM too frequently would still erase the benefits of native execution. Velox therefore requests resources in batches rather than one object at a time. Go prepares a block of memory, and the C VM constructs objects continuously within it, yielding only when the entire block has been consumed. Most parsing time consequently remains inside the C VM, while the Go runtime intervenes only to replenish resources or perform operations that inherently require Go.

## GC Safety

When the C VM writes directly into Go objects, the compiler does not insert write barriers for those pointer writes. Identifying every pointer write in C and explicitly invoking a barrier would amount to moving part of the Go compiler's logic into the VM, while also imposing additional overhead on the object-binding hot path. In Velox, we instead use the equivalent of executing write barriers in batches and establish GC safety over the construction of the object graph as a whole. The design relies on the following properties:

1. Go and C use compatible memory layout and alignment rules, allowing C to calculate field addresses from type information.
2. Aligned pointer writes are atomic, so the concurrent GC cannot observe a partially written pointer.
3. C writes only to managed memory or target objects, and the allocator keeps that memory reachable to the GC throughout parsing.
4. The Go GC marks underlying memory blocks. Once a block is marked live, the GC continues scanning all pointers within it according to the block's type layout.
5. The hybrid write barrier shades the referent of an overwritten pointer and, when the current stack is grey, the referent of the newly installed pointer. When parsing completes, clearing the temporary references in Go causes their former referents to be marked live.
6. `runtime.KeepAlive` ensures that the input and target objects outlive the entire parsing operation.

Given these conditions, object-graph construction and publication are designed as two phases. During parsing, temporary references on the Go side keep all relevant memory reachable. Even if object relationships newly established by C are not observed by the current GC cycle, their targets cannot be reclaimed prematurely. Once parsing completes, Go clears the temporary references, causing the hybrid write barrier to mark the referenced memory as live.

Barrier-free writes from C can affect only the GC cycle that is active at the time. By the next cycle, the object relationships are stable and will be scanned normally. As long as the construction phase preserves reachability and the publication phase ensures GC safety, the entire lifecycle is safe. The unit of GC protection therefore shifts from individual pointer writes to the memory blocks containing the object graph, keeping write barriers off the C VM's hot path.

## SIMD Pre-scan

The VM also needs a clean way to obtain instructions from raw JSON. Raw JSON interleaves whitespace, string contents, and characters that actually affect the syntax. If the VM identifies all of them while executing, string scanning and syntax processing become entangled in the same loop. Following simdjson, we therefore perform an initial scan that identifies quotes, escapes, and structural characters, extracting string starts, structural characters, and scalar starts into a structural index. In effect, this decouples instruction fetch from VM execution.

This separation carries a performance cost. The SIMD pre-scan must read the entire input once and write the structural index, which the VM then reads back, adding a substantial amount of work. When JSON contains substantial whitespace, the index can of course skip many irrelevant bytes quickly. In compact JSON, however, structural positions are already dense, so the extra scan and index traffic instead become wasteful. This cost is particularly visible in simdjson on compact inputs and is a major reason why yyjson is often faster for such data, since the state-machine execution itself cannot be parallelized.

Velox uses a SIMD pre-scan to organize JSON into clean "instructions" and give the program a clearer architecture, not because introducing SIMD necessarily makes parsing faster. While scanning the input to build the structural index, SIMD can also validate UTF-8 and escapes, greatly simplifying string handling inside the state machine.

## Summary

The central idea behind Velox 0.2 is to treat object binding as the fundamental primitive of JSON deserialization. Reflection is used ahead of time to derive immutable type information for the C VM, primarily describing memory allocation and object layout. The C VM parses JSON and constructs objects, while the Go runtime intervenes only when memory must be allocated or Go-specific logic must run. Through this architecture, the C VM can write directly into Go objects without executing GC write barriers on the hot path, enabling high-performance JSON deserialization.

Velox's memory management is similar to an arena allocator, with memory managed in blocks. A small number of live objects may therefore extend the lifetime of an entire block. Deserialized objects should not be retained in long-lived caches. When data must be cached for an extended period, the better practice is to construct independently owned domain objects rather than retain deserialized objects backed by the allocator.

The architecture described above establishes the fundamental conditions that make this approach viable. Many implementation details, including field lookup, stack management, and floating-point parsing, also affect deserialization performance, but they are not foundational to the architecture and are therefore omitted here. The greater value of this design is that it places parsing, object construction, and cooperation with the Go runtime on a unified foundation, establishing a coherent basis for further optimization and new capabilities.
