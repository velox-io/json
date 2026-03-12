# Decoder Buffer Prediction Algorithm

The `Decoder` manages read buffers for streaming JSON decoding. Because decoded
strings may alias the buffer via `unsafe.String` (zero-copy), buffers are never
compacted in-place — each "buffer switch" allocates a fresh `[]byte`, and the
old one becomes GC-eligible once no live strings reference it.

The prediction algorithm minimizes:
1. **Retry cost** — when a value spans the buffer boundary, the decoder must
   `growAndFill` + re-parse (slow path). Fewer retries = better throughput.
2. **Memory waste** — over-sized buffers waste RAM and increase GC pressure.

## Data Flow

```
   Decode()
     │
     ├─ scanValue(buf, idx) ──► OK ──► recordValueSize(size)
     │                                        │
     │                                   remaining < predicted?
     │                                        │
     │                               ┌────────┴────────┐
     │                               │  len(buf) -     │
     │                               │  scanAt >=      │──► YES: keep buffer
     │                               │  predicted?     │
     │                               └────────┬────────┘
     │                                        │ NO
     │                                        ▼
     │                               ┌───────────────────┐
     │                               │ growBufferAndFill │
     │                               │ 1. growBuffer()   │
     │                               │    (see sizing)   │
     │                               │ 2. fill loop:     │
     │                               │    readMore()     │
     │                               │    until full     │
     │                               └─────────┬─────────┘
     │                                         │
     │                                  next Decode() finds
     │                                  pre-filled buffer
     │
     │
     └─ errUnexpectedEOF ──► retryDecode()
                                 │
                            growAndFill(valueStart)
                            (expand buf via ensureBuffer, fill to capacity)
                                 │
                            scanValue() again
                                 │
                            on success ──► growBufferAndFill()
                                           (same pre-fill for next value)
```

### Why pre-fill matters (`growBufferAndFill`)

After a successful decode, `growBuffer` allocates a correctly-sized new buffer
and copies the unscanned tail (~few KB). Without filling, the next `Decode`
call enters `ensureData`, sees `scanAt < bufLen` (the unscanned tail), skips
reading, and `scanValue` runs with insufficient data — causing an avoidable
retry. `growBufferAndFill` eliminates this by reading the new buffer full
immediately after allocation.

## Prediction: `predictedValueSize()` / `recentAvgValueSize()`

`recentAvgValueSize()` returns the rolling average of the last 2 decoded values
(falling back to `bufSize / 4` when fewer than 2 have been seen).

`predictedValueSize()` returns the expected byte size of the next JSON value:

```
predicted = max( recentAvgValueSize(),
                 maxSeenSize )
```

- **Recent average** (`recentAvgValueSize`): pure signal from actual recent values.
- **High-water mark** (`maxSeenSize`): the largest value seen so far, with
  slow exponential decay (~3% per value via `maxSeenSize -= maxSeenSize >> 5`).
  This ensures the decoder stays prepared for recurring large values (spikes)
  even across long runs of small ones.

### Decay behavior

After a 1 MB spike followed by 170-byte values:

```
  Value #    maxSeenSize
  ───────    ───────────
  spike      1,048,576   (= 1 MB, set directly)
  +1         1,015,808   (decayed 3.1%)
  +5           891,813
  +10          741,025
  +20          511,895
  +50          168,043   ← still > 170 B at gap=50
  +277             170   ← finally decays to small-value level
```

For typical spike gaps of 20, the high-water mark remains effective.

## Buffer Sizing: `growBuffer()`

When the current buffer can't hold the next predicted value (`len(buf) - scanAt
< predicted`), `growBufferAndFill` calls `growBuffer` to compute a new buffer
size through these steps:

```
┌─────────────────────────────────────────────────────┐
│ 1. Base: nextBufSize()                              │
│    = avg(lastBufSize, prevBufSize)                  │
│    fallback: bufSize (default 128 KB)               │
│                                                     │
│ 2. Floor: minGoodBufSize                            │
│    = smallest buffer that held >= 2 values           │
│    If minGoodBufSize > base, use minGoodBufSize     │
│                                                     │
│ 3. Target: needed                                   │
│    = unscanned + predicted + minReadSize             │
│    If valuesInBuf < 2 (poor fit): needed *= 2       │
│                                                     │
│ 4. Grow: for newSize < needed { newSize *= 2 }      │
│                                                     │
│ 5. Value-align (when avg-driven):                   │
│    if predicted > minReadSize && predicted == avg:   │
│      nFit = (newSize - minReadSize) / predicted     │
│      aligned = nFit * predicted + minReadSize       │
│      if aligned < needed: aligned += predicted      │
│      newSize = aligned                              │
└─────────────────────────────────────────────────────┘
```

### Fitness signal: `valuesInBuf`

Counts how many values were decoded in the current buffer's lifetime:

| valuesInBuf | Meaning | Action |
|---|---|---|
| 0 | Buffer never used (shouldn't happen) | — |
| 1 | Poor fit: value nearly filled the buffer | `needed *= 2` to jump to a better size |
| >= 2 | Good fit: buffer held multiple values | Record `len(buf)` as `minGoodBufSize` floor |

### Sticky floor: `minGoodBufSize`

Prevents `nextBufSize()` averaging from shrinking back to a poor-fit size.
Once a buffer successfully holds >= 2 values, its size becomes the minimum
for all future allocations (until an even smaller buffer proves itself).

### Value-aligned sizing

After the power-of-2 doubling, the buffer may have a tail that can't hold
another value. For example, a 512 KB buffer with 65 KB values fits 7 values
(455 KB) and wastes 57 KB (11%). Value-align trims `newSize` down to
`N * predicted + minReadSize`, eliminating this dead space:

```
  pow2 = 512K, predicted = 65K
  nFit = (512K - 512) / 65K = 7
  aligned = 7 * 65K + 512 = 455.5K   ← saves 56.5K per buffer
```

**Spike-aware guard**: value-align is only applied when `predicted == avg`
(the recent average drives prediction). When `maxSeenSize` dominates —
meaning the decoder is still "remembering" a past spike — the predicted size
doesn't reflect the actual values being decoded, so aligning to it would
trim to the wrong grid and increase both allocation count and total bytes.

| Scenario | pow2 buffer | value-aligned | Savings |
|---|---|---|---|
| 65 KB values | 512K | 455K | 11% |
| 86 KB values | 512K | 430K | 16% |
| 40 KB values | 128K | 120K | 6% |
| Spiky (300B + 2MB) | 11,541K | 11,541K | 0% (guard skips) |

### Example: 65 KB values with 128 KB initial buffer

Without the fitness signal, the buffer oscillates:

```
  Buf#   Size    Values   Next (avg)
  ────   ─────   ──────   ──────────
  1      128K    1        128K          ← only 1 value fits
  2      128K    1        128K          ← same problem
  ...    (stuck at 128K forever)
```

With the fitness signal (`needed *= 2` when `valuesInBuf < 2`):

```
  Buf#   Size    Values   Floor    Next
  ────   ─────   ──────   ─────    ─────
  1      128K    1        0        needed=131K*2=262K → 256K
  2      256K    3        256K     avg(128K,256K)=192K → floor=256K
  3      256K    3        256K     avg(256K,256K)=256K
  ...    (stable at 256K)
```

## Retry Path: `ensureBuffer()` with Prediction-Aware Sizing

When `scanValue` returns `errUnexpectedEOF`, the retry loop calls
`growAndFill(valueStart)` which uses `ensureBuffer()` to allocate larger
buffers. `ensureBuffer` uses three sizing heuristics in the `unscanned > 0`
branch:

### 1. Baseline

```
minNeeded = unscanned + minReadSize
```

This is the absolute minimum — just enough room for the existing data plus
one read.

### 2. Prediction-aware sizing

```
if predicted > minReadSize:
    needed = unscanned + predicted + minReadSize
    minNeeded = max(minNeeded, needed)
```

Incorporates the predicted value size so the new buffer is likely large enough
to hold the complete value, avoiding multiple rounds of grow-retry.

### 3. Cold-start heuristic

```
if unscanned > predicted:
    coldNeeded = unscanned * 2 + minReadSize
    minNeeded = max(minNeeded, coldNeeded)
```

When `unscanned > predicted`, the prediction is stale or in cold-start (e.g.,
Value #0 where `predicted = bufSize/4 = 32K` but the actual value is 456K).
Since the value is provably >= `unscanned` bytes (we already have that much
data and the value isn't complete), using `2 × unscanned` as the target
converges exponentially rather than linearly.

### Sizing then proceeds with:

```
for newSize < minNeeded { newSize *= 2 }

// Value-align (same spike-aware guard as growBuffer)
if predicted > minReadSize && predicted == avg:
    aligned = nFit * predicted + minReadSize
    if aligned >= minNeeded: newSize = aligned
```

### Example: 456 KB value with 128 KB initial buffer (Twitter)

Without prediction-aware sizing and cold-start heuristic:

```
  Retry#  unscanned  bufSize   minNeeded (old)    outcome
  ──────  ─────────  ───────   ───────────────    ───────
  0       128K       128K      128K + 512         scanValue fails
  1       129K       129K      129K + 512         scanValue fails
  2       130K       130K      130K + 512         scanValue fails
  3       131K       131K      131K + 512         scanValue fails (4 retries to reach 456K!)
```

With prediction-aware + cold-start:

```
  Retry#  unscanned  predicted  cold(2x)  minNeeded  newSize  outcome
  ──────  ─────────  ─────────  ────────  ─────────  ───────  ───────
  0       128K       32K        256K      257K       262144   fills 256K, scanValue fails
  1       256K       32K        512K      513K       524288   fills 512K, scanValue OK ✓
```

Retries drop from 4 to 1.

## Memory Safety

Buffers grow in response to actual value sizes — small values cannot inflate
buffers because `growBufferAndFill` only triggers when `remaining < predicted`,
and `predicted` is bounded by actual decoded sizes. The high-water mark decays
continuously, so a single large value doesn't permanently inflate the
prediction.
