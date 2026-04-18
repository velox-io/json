# Strategy Playground


## field lookup

This directory is for experimenting with field lookup strategy choices. The final conclusion is a three tier split by field count `n`: bitmap8k / perfect hash / hashmap.

### Build and run

```
make bench        # full 2D matrix (queries=200000, reps=5)
make quick        # quick run (queries=50000, reps=3)
make collision    # empirical bitmap8k false positive check
```

### Final choice

```
n == 0          empty        (degenerate, never hits)
1 <= n <= 8     bitmap8k     (K=8 prefix bitmap + memcmp verification)
9 <= n <= 32    perfect hash (simple mixer; fall back to fnv1a if build fails)
n >= 33         hashmap      (FNV-1a + linear probing, load < 0.5)
```

Practical reasons:

* **bitmap8k (`n <= 8`)**: the bitmaps for 8 fields fit into a single `u64`, so the hit path needs only one mask, one popcount, and at most one `memcmp`; the miss path is around 1 ns, which no hash based scheme reaches.
* **perfect hash (`9 <= n <= 32`)**: once a brute force search over `(seed, shift)` finds a collision free 2N slot layout, each lookup is only one mixer plus one `memcmp`, with a much smaller constant cost than hashmap probing.
* **hashmap (`n >= 33`)**: the build cost for perfect hash grows too unpredictably as `n` increases, so hashmap is the stable choice once `n` becomes large.

Representative measured `ns/op` for auto pick at 100% hit rate: `n=4` is 4.59 ns, `n=8` is 5.43 ns, `n=16` is 5.73 ns, `n=32` is 6.90 ns, `n=64` is 9.45 ns, and `n=256` is 11.28 ns.

### Performance matrix summary (hit=100%, `make quick`)

```
     n |   bitmap8k  ph-simple     ph-fnv    hashmap
-------+--------------------------------------------
     1 |      2.53ns      6.17ns      5.23ns      5.23ns
     2 |      6.38ns      7.21ns      7.93ns      7.97ns
     4 |      6.26ns      6.78ns      7.34ns      7.47ns
     8 |      6.39ns      6.36ns      7.59ns      8.75ns
     9 |          -      6.63ns      8.08ns      7.26ns
    12 |          -      7.21ns      7.83ns      8.88ns
    16 |          -      6.48ns      7.43ns      9.93ns
    24 |          -      7.09ns      7.75ns     10.47ns
    32 |          - build-fail      7.17ns      9.83ns
    33 |          -          - build-fail      8.30ns
    64 |          -          - build-fail      9.39ns
   256 |          -          - build-fail     11.41ns
  4096 |          -          -          -     14.38ns
```

In miss heavy scenarios the bitmap8k advantage grows further: for `n <= 8`, the 0% hit rate column stays around 0.89 ns, because one mask is enough to reject the key, far below hashmap's 9 ns to 19 ns.

### bitmap8k design highlights

* The bitmap width is 8192 bits, or 1 KiB. Together with field metadata and the byte mask table, total memory is about 2 KB and does not depend on field count.
* Each field uses a prefix capped at `K=8`: take the first `min(klen, K)` bytes of the key, mix them, and set only one bit.
* `klen <= K`: the prefix is the full key, so the prefix bitmap is an exact filter. A hit is a true hit and needs no `memcmp`, which makes it false positive free.
* `klen > K`: a bit hit must be verified with `memcmp`. Empirically, with `make collision` across 9 real field sets, average `memcmps/query` ranges from 0.0 to 0.6 and the maximum is 2. The prefix heavy case with 8 `customer*` prefixes is the worst case.

### perfect hash design highlights

* Build: brute force over `(seed, shift)` and require every field to land in a distinct slot of a 2N table. Once found, the layout is fixed.
* The simple mixer emits a 4 byte fingerprint, which is enough to separate 9 to 32 fields. If that build fails, the code falls back to the fnv1a version.
* The lookup path is only one mixer, one `memcmp`, and one pointer load.

### Explored alternatives

* **SIMD list scan (`lens + first_byte` prefilter)**: only useful for partial parsing or existence checks. In general unmarshal workloads where all fields are hit, it loses to bitmap8k.
* **bitmap8c (compact distinct char)**: the SWAR variable shift creates a serial dependency chain and ends up 1 to 2 times slower than bitmap8.
* **bitmap8t (transpose + NEON)**: the `fmov` crossing from SIMD to GPR costs 3 to 5 cycles, so throughput still loses to the scalar bitmap8 variant.
* **naive bitmap8 (no `K` truncation)**: when `maxlen=28`, the bitmap grows to about 7 KB and performance regresses to 8 ns.
* **word/overlap/CRC32 mixer tuning**: lookup time is dominated by `memcmp` and pointer load, around 60% to 70%, while the mixer is only 10% to 15%, so tuning brings no real gain. CRC also needs architecture specific handling and is not worth it.
* **singleton/pair (special casing `n=1` and `n=2`)**: memory can drop by 30 times to 90 times, but `ns/op` is not meaningfully better than bitmap8k. The extra strategy branch is not worth it, and one bitmap8k path stays simpler.
