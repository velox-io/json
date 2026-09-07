# Roadmap

This file lists current high-impact improvement areas for contributors.

## Current Focus Areas

1. **Support sorted-key output for map serialization**

   Current status: map serialization does not support emitting keys in sorted order. Explore API shape, implementation strategy, and performance trade-offs for adding an optional sorted-keys mode without regressing the default fast path.

2. **Support JSON v2 `format` tags**

   Support the `format` struct tag semantics introduced by `encoding/json/v2`.

3. **Broaden zero-copy support**

   Extend zero-copy decoding beyond the DOM-style `Value` API.

4. **Expand documentation**

   Improve the documentation, particularly for polymorphic decoding.

5. **Speed up the `interface{}` type cache in encvm**

   Current status: OP_INTERFACE and OP_UNFOLD resolve every non-nil interface
   value through a global snapshot: a 32-byte-per-entry array sorted by type
   pointer, searched with an inline binary search per value. On a miss the VM
   yields to Go, which compiles the Blueprint and republishes the snapshot as
   a full array copy plus re-sort. For any-heavy workloads with many distinct
   concrete types the branchy O(log n) lookup sits on the hot path, and
   warmup pays repeated snapshot churn. Explore: an open-addressed hash table
   keyed by the type pointer (one dependent load on hit instead of log n
   branches), a per-ExecCtx last-hit memo for monomorphic sites, and a
   cheaper insert (binary-search position plus memmove into a
   capacity-reserved array). Any redesign must keep the immutable-snapshot
   publication that lets concurrent VMs read lock-free, and be validated on
   any-heavy benchmarks with 10s-level benchtime.
