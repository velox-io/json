package benchmark

import (
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Helpers: countWriter counts bytes written without buffering, simulating a
// real IO sink (net.Conn, bufio.Writer, etc.) where the destination does not
// need to grow a contiguous buffer.
// =============================================================================

// countWriter counts bytes written without buffering.
type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// =============================================================================
// KubePodsStream: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// Encode 50 copies as NDJSON stream, reusing one Encoder per iteration.
// =============================================================================

const kubePodsEncodeCount = 50

func Benchmark_Encoder_KubePodsStream_StdJSON(b *testing.B) {
	v := loadPodsValue()
	var cw countWriter
	enc := json.NewEncoder(&cw)
	for range kubePodsEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := json.NewEncoder(&countWriter{})
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_KubePodsStream_Sonic(b *testing.B) {
	v := loadPodsValue()
	var cw countWriter
	enc := sonic.ConfigDefault.NewEncoder(&cw)
	for range kubePodsEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := sonic.ConfigDefault.NewEncoder(&countWriter{})
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_KubePodsStream_GoJSON(b *testing.B) {
	v := loadPodsValue()
	var cw countWriter
	enc := gojson.NewEncoder(&cw)
	for range kubePodsEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := gojson.NewEncoder(&countWriter{})
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_KubePodsStream_Velox(b *testing.B) {
	v := loadPodsValue()
	var cw countWriter
	enc := vjson.NewEncoder(&cw)
	for range kubePodsEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := vjson.NewEncoder(&countWriter{})
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// TwitterStream: Twitter search API response (~617KB, deeply nested, many fields)
// Encode 10 copies as NDJSON stream, reusing one Encoder per iteration.
// =============================================================================

const twitterEncodeCount = 10

func Benchmark_Encoder_TwitterStream_StdJSON(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	enc := json.NewEncoder(&cw)
	for range twitterEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := json.NewEncoder(&countWriter{})
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_TwitterStream_Sonic(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	enc := sonic.ConfigDefault.NewEncoder(&cw)
	for range twitterEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := sonic.ConfigDefault.NewEncoder(&countWriter{})
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_TwitterStream_GoJSON(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	enc := gojson.NewEncoder(&cw)
	for range twitterEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := gojson.NewEncoder(&countWriter{})
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_TwitterStream_Velox(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	enc := vjson.NewEncoder(&cw)
	for range twitterEncodeCount {
		_ = enc.Encode(v)
	}
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		enc := vjson.NewEncoder(&countWriter{})
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// TwitterSingle: encode one Twitter value per iteration to measure per-call
// overhead (vs TwitterStream which encodes 10 copies reusing one Encoder).
// =============================================================================

func Benchmark_Encoder_TwitterSingle_StdJSON(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = json.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := json.NewEncoder(&countWriter{}).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Encoder_TwitterSingle_Sonic(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = sonic.ConfigDefault.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := sonic.ConfigDefault.NewEncoder(&countWriter{}).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Encoder_TwitterSingle_GoJSON(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = gojson.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := gojson.NewEncoder(&countWriter{}).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Encoder_TwitterSingle_Velox(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = vjson.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := vjson.NewEncoder(&countWriter{}).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}
