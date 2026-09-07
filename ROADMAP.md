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
