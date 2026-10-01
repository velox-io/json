# Roadmap

This file lists current high-impact improvement areas for contributors.

## Current Focus Areas

1. **Support sorted-key output for map serialization**

   Current status: map serialization does not support emitting keys in sorted order.
   Explore API shape, implementation strategy, and performance trade-offs for adding
   an optional sorted-keys mode without regressing the default fast path.

2. **Match `encoding/json` handling of custom `MarshalJSON` output**

   Custom `MarshalJSON` output is currently appended verbatim: it is not
   re-indented under indent mode and not HTML-escaped under EscapeHTML mode.
   Post-process the output so indent and EscapeHTML behave like `encoding/json`
   without regressing the default fast path (a scan-only pass when no
   transformation is needed). Validity checking stays out of scope: the
   implementer is responsible for producing valid JSON. `MarshalText` output
   already goes through the normal string escaper and is unaffected.

3. **Expand documentation**

   Improve the documentation, particularly for polymorphic decoding.

4. **Support JSON v2 `format` tag**

