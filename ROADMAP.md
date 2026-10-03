# Roadmap

This file lists current high-impact improvement areas for contributors.

## Current Focus Areas

1. **Support sorted-key output for map serialization**

   Current status: map serialization does not support emitting keys in sorted order.
   Explore API shape, implementation strategy, and performance trade-offs for adding
   an optional sorted-keys mode without regressing the default fast path.

2. **Support JSON v2 `format` tag**


3. **First-class custom encoding and decoding**

   A JSON library must let users define their own encode/decode logic, and velox lacks an elegant mechanism for it.
   The only extension point today is `MarshalJSON() ([]byte, error)` / `UnmarshalJSON([]byte) error`, whose bytes-in/bytes-out shape isolates custom types from the library:
   Indent, `EscapeHTML` and `FloatExpAuto` have no effect on their output, and `UseNumber`, `RejectUnknownMembers` and `ZeroCopy` cannot reach them on decode.
   Define extension interfaces in velox: custom logic reads and writes through the velox encoder/decoder, which carries the Options and enforces formatting, and nested values recurse back into velox.
   A custom codec describes only the structure of a value while Options decide the format, keeping the two orthogonal. Consider supporting the v2 interfaces as a bridge.
