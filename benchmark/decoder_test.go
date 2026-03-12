package benchmark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Helpers: build NDJSON streams from existing test data
// =============================================================================

// repeatNDJSON builds a byte slice containing n copies of jsonVal separated by '\n'.
func repeatNDJSON(jsonVal []byte, n int) []byte {
	var buf bytes.Buffer
	for range n {
		buf.Write(jsonVal)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// NDJSON streams, lazily built from compact JSON data.
var (
	smallNDJSONOnce       sync.Once
	smallNDJSONData       []byte
	escapeHeavyNDJSONOnce sync.Once
	escapeHeavyNDJSONData []byte
	kubePodsNDJSONOnce    sync.Once
	kubePodsNDJSONData    []byte
	twitterNDJSONOnce     sync.Once
	twitterNDJSONData     []byte
	tinyNDJSONOnce        sync.Once
	tinyNDJSONData        []byte
)

func loadSmallNDJSON() []byte {
	smallNDJSONOnce.Do(func() { smallNDJSONData = repeatNDJSON(LoadSmallCompactJSON(), 100) })
	return smallNDJSONData
}

func loadEscapeHeavyNDJSON() []byte {
	escapeHeavyNDJSONOnce.Do(func() { escapeHeavyNDJSONData = repeatNDJSON(LoadEscapeHeavyCompactJSON(), 50) })
	return escapeHeavyNDJSONData
}

func loadKubePodsNDJSON() []byte {
	kubePodsNDJSONOnce.Do(func() { kubePodsNDJSONData = repeatNDJSON(LoadPodsCompactJSON(), 50) })
	return kubePodsNDJSONData
}

func loadTwitterNDJSON() []byte {
	twitterNDJSONOnce.Do(func() { twitterNDJSONData = repeatNDJSON(LoadTwitterCompactJSON(), 10) })
	return twitterNDJSONData
}

func loadTinyNDJSON() []byte {
	tinyNDJSONOnce.Do(func() { tinyNDJSONData = []byte(strings.Repeat("{}\n", 1000)) })
	return tinyNDJSONData
}

// =============================================================================
// Small NDJSON Stream (100 copies of SmallCompactJSON)
// =============================================================================

func Benchmark_Decoder_Small_Std(b *testing.B) {
	data := loadSmallNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var s Small
			if err := dec.Decode(&s); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Small_Sonic(b *testing.B) {
	data := loadSmallNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var s Small
			if err := dec.Decode(&s); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Small_Velox(b *testing.B) {
	data := loadSmallNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var s Small
			if err := dec.Decode(&s); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// EscapeHeavy NDJSON Stream (50 copies)
// =============================================================================

func Benchmark_Decoder_EscapeHeavy_Std(b *testing.B) {
	data := loadEscapeHeavyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p EscapeHeavyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_EscapeHeavy_Sonic(b *testing.B) {
	data := loadEscapeHeavyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var p EscapeHeavyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_EscapeHeavy_Velox(b *testing.B) {
	data := loadEscapeHeavyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var p EscapeHeavyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// KubePods NDJSON Stream (50 copies)
// =============================================================================

func Benchmark_Decoder_KubePods_Std(b *testing.B) {
	data := loadKubePodsNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var pl KubePodList
			if err := dec.Decode(&pl); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_KubePods_Sonic(b *testing.B) {
	data := loadKubePodsNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var pl KubePodList
			if err := dec.Decode(&pl); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_KubePods_Velox(b *testing.B) {
	data := loadKubePodsNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var pl KubePodList
			if err := dec.Decode(&pl); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// Twitter NDJSON Stream (10 copies — large payload ~617KB each)
// =============================================================================

func Benchmark_Decoder_Twitter_Std(b *testing.B) {
	data := loadTwitterNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var t twitter.TwitterStruct
			if err := dec.Decode(&t); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Twitter_Sonic(b *testing.B) {
	data := loadTwitterNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var t twitter.TwitterStruct
			if err := dec.Decode(&t); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Twitter_Velox(b *testing.B) {
	data := loadTwitterNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var t twitter.TwitterStruct
			if err := dec.Decode(&t); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// Single Large Value (Twitter — tests stream overhead for a single decode)
// =============================================================================

func Benchmark_Decoder_SingleLarge_Std(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		var t twitter.TwitterStruct
		if err := dec.Decode(&t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Decoder_SingleLarge_Sonic(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		var t twitter.TwitterStruct
		if err := dec.Decode(&t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Decoder_SingleLarge_Velox(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data), vjson.WithBufferSize(1<<20))
		var t twitter.TwitterStruct
		if err := dec.Decode(&t); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Tiny Values Stream: 1000 x `{}` — tests scanner + queue overhead
// =============================================================================

func Benchmark_Decoder_TinyValues_Std(b *testing.B) {
	data := loadTinyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_TinyValues_Sonic(b *testing.B) {
	data := loadTinyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_TinyValues_Velox(b *testing.B) {
	data := loadTinyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// Spiky Stream: many small values (~300B) with periodic large spikes (~2MB).
// Tests whether the decoder's buffer prediction handles size variance well.
// The gap between spikes (20 small values) exceeds the prediction window
// (average of last 2), so every spike is a cold miss.
// =============================================================================

func Benchmark_Decoder_Spiky_Std(b *testing.B) {
	data := LoadSpikyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Spiky_Sonic(b *testing.B) {
	data := LoadSpikyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Spiky_Velox(b *testing.B) {
	data := LoadSpikyNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// HalfBuf Stream: 50 values each ~65 KB — just over half the default 128 KB
// buffer. Tests buffer reuse efficiency when every value forces a new buffer.
// =============================================================================

func Benchmark_Decoder_HalfBuf_Std(b *testing.B) {
	data := LoadHalfBufNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_HalfBuf_Sonic(b *testing.B) {
	data := LoadHalfBufNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_HalfBuf_Velox(b *testing.B) {
	data := LoadHalfBufNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// ThirdBuf Stream: 50 values each ~86 KB — about one-third of the 256 KB
// buffer that maybeNewBuffer promotes to. The buffer fits 2 values but not
// 3, so switches happen every 2 values (~50% utilization).
// =============================================================================

func Benchmark_Decoder_ThirdBuf_Std(b *testing.B) {
	data := LoadThirdBufNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_ThirdBuf_Sonic(b *testing.B) {
	data := LoadThirdBufNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_ThirdBuf_Velox(b *testing.B) {
	data := LoadThirdBufNDJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(data))
		for {
			var p SpikyPayload
			if err := dec.Decode(&p); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// Log Stream: ~90K real OTEL-style log lines (~670 bytes each, NDJSON).
// Tests sustained high-count decoding with realistic small structured values.
// =============================================================================

func Benchmark_Decoder_Log_Std(b *testing.B) {
	LogNDJSON := LoadLogNDJSON()
	b.SetBytes(int64(len(LogNDJSON)))
	b.ReportAllocs()
	for b.Loop() {
		dec := json.NewDecoder(bytes.NewReader(LogNDJSON))
		for {
			var r LogRecord
			if err := dec.Decode(&r); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Log_Sonic(b *testing.B) {
	LogNDJSON := LoadLogNDJSON()
	b.SetBytes(int64(len(LogNDJSON)))
	b.ReportAllocs()
	for b.Loop() {
		dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(LogNDJSON))
		for {
			var r LogRecord
			if err := dec.Decode(&r); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Decoder_Log_Velox(b *testing.B) {
	LogNDJSON := LoadLogNDJSON()
	b.SetBytes(int64(len(LogNDJSON)))
	b.ReportAllocs()
	for b.Loop() {
		dec := vjson.NewDecoder(bytes.NewReader(LogNDJSON))
		for {
			var r LogRecord
			if err := dec.Decode(&r); err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// Memory profile: single-pass decode of the full Log stream, comparing
// heap usage across decoders.  Run with:
//
//	go test -run TestLogMemProfile -v .
// =============================================================================

func TestLogMemProfile(t *testing.T) {
	LogNDJSON := LoadLogNDJSON()
	type decoderRun struct {
		name string
		fn   func()
	}

	runs := []decoderRun{
		{"encoding/json", func() {
			dec := json.NewDecoder(bytes.NewReader(LogNDJSON))
			for {
				var r LogRecord
				if err := dec.Decode(&r); err != nil {
					if err == io.EOF {
						return
					}
					t.Fatal(err)
				}
			}
		}},
		{"sonic", func() {
			dec := sonic.ConfigDefault.NewDecoder(bytes.NewReader(LogNDJSON))
			for {
				var r LogRecord
				if err := dec.Decode(&r); err != nil {
					if err == io.EOF {
						return
					}
					t.Fatal(err)
				}
			}
		}},
		{"velox", func() {
			dec := vjson.NewDecoder(bytes.NewReader(LogNDJSON))
			for {
				var r LogRecord
				if err := dec.Decode(&r); err != nil {
					if err == io.EOF {
						return
					}
					t.Fatal(err)
				}
			}
		}},
	}

	for _, r := range runs {
		runtime.GC()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		r.fn()

		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		totalAlloc := after.TotalAlloc - before.TotalAlloc
		// HeapInuse can fluctuate due to GC; report both.
		t.Logf("%-15s  TotalAlloc=%-10s  HeapInuse=%-10s  Mallocs=%d",
			r.name,
			formatBytes(totalAlloc),
			formatBytes(after.HeapInuse),
			after.Mallocs-before.Mallocs,
		)
	}
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
