# Roadmap

This file lists current high-impact improvement areas for contributors.

## Current Focus Areas

1. **Support sorted-key output for map serialization**

   Current status: map serialization does not support emitting keys in sorted order. Explore API shape, implementation strategy, and performance trade-offs for adding an optional sorted-keys mode without regressing the default fast path.

2. **Support streaming input across non-contiguous buffers**

   Velox supports streaming processing, but input must still reside in a contiguous buffer. Supporting non-contiguous input requires local rollback when a token crosses a buffer boundary and three-state EOF handling: complete, incomplete, or invalid. The existing yield mechanism already provides the control-transfer foundation.

3. **Support non-SIMD scanning for compact JSON**

   Explore a non-SIMD JSON scanning path and dispatch strategy to improve parsing performance for compact inputs while retaining SIMD scanning where it performs best. This can proceed in two stages: first implement and validate the path in the DOM (`Value`) parser, then integrate it into the general-purpose binding VM.

4. **Support JSON v2 `format` tags**

   Support the `format` struct tag semantics introduced by `encoding/json/v2`.

5. **Broaden zero-copy support**

   Extend zero-copy decoding beyond the DOM-style `Value` API.

6. **Expand documentation**

   Improve the documentation, particularly for polymorphic decoding.
