# Roadmap

This file lists current high-impact improvement areas for contributors.

## Current Focus Areas

1. **Support sorted-key output for map serialization**

   Current status: map serialization does not support emitting keys in sorted order. Explore API shape, implementation strategy, and performance trade-offs for adding an optional sorted-keys mode without regressing the default fast path.
