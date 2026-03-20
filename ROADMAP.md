# Roadmap

This file lists current high-impact improvement areas for contributors.

## Current Focus Areas

1. **Improve decoder buffer management flexibility**

   Current status: buffer strategy is usable but not flexible enough for all workloads.


2. **Length-adaptive AVX dispatch for string encoding on amd64**

   Currently the encvm uses a single ISA variant (SSE4.2 or AVX) uniformly for all string encoding operations. For most fields (short keys, small values), SSE4.2 is already optimal. However, for fields carrying large payloads — e.g. an article body or a base64 blob — AVX2's 256-bit width can provide significantly better throughput for bulk scanning (UTF-8 validation, HTML-escape detection, etc.).

   Proposed approach:
   - Inside the string encoding path, add a **runtime length threshold** (e.g. >64 or >128 bytes) to decide whether to dispatch to an AVX2 code path or stay on SSE4.2.
   - This avoids the complexity of per-field compile-time ISA tagging while making the decision based on actual data length — the factor that truly determines which ISA wins.
   - Care should be taken to issue `vzeroupper` after the AVX path and to benchmark the dispatch overhead itself to ensure it does not eat into the gains on short strings.

3. **Support sorted-key output for map serialization**

   Current status: map serialization does not support emitting keys in sorted order. Explore API shape, implementation strategy, and performance trade-offs for adding an optional sorted-keys mode without regressing the default fast path.

4. **Provide a SAX-like / non-binding parsing API**

   Current status: decoding is primarily oriented around binding into Go values. Consider a SAX-like parsing API that avoids binding, reduces Go GC pressure, and is especially useful for gateway / forwarding workloads that only need to inspect, route, or partially transform payloads.

5. **Integrate the xjb SIMD float parsing algorithm**

   Current status: float parsing is functional but still has room for throughput improvements. Evaluate how to bring in the xjb SIMD float parse algorithm, including ISA coverage, fallback paths, and benchmark impact on realistic decode workloads.

6. **Fuse whitespace skipping with next-byte classification**

   Current status: the decoder currently performs `skipWS` and then separately checks the next byte (for example `src[idx] == '{'`). Investigate a tighter scan path that combines whitespace skipping with next-character classification to reduce branch and load overhead in hot decode paths.
