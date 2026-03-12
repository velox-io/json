package benchmark

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Helpers: discardWriter avoids io.Discard which is just an int and doesn't
// exercise real Write paths. countWriter tracks total bytes for b.SetBytes.
// =============================================================================

// countWriter counts bytes written without buffering.
type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// =============================================================================
// KubePods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// Encode 50 copies as NDJSON stream.
// =============================================================================

const kubePodsEncodeCount = 50

func Benchmark_Encoder_KubePods_StdJSON(b *testing.B) {
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
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_KubePods_Sonic(b *testing.B) {
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
		var buf bytes.Buffer
		enc := sonic.ConfigDefault.NewEncoder(&buf)
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_KubePods_GoJSON(b *testing.B) {
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
		var buf bytes.Buffer
		enc := gojson.NewEncoder(&buf)
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_KubePods_Velox(b *testing.B) {
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
		var buf bytes.Buffer
		enc := vjson.NewEncoder(&buf)
		for range kubePodsEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// Twitter: Twitter search API response (~617KB, deeply nested, many fields)
// Encode 10 copies as NDJSON stream.
// =============================================================================

const twitterEncodeCount = 10

func Benchmark_Encoder_Twitter_StdJSON(b *testing.B) {
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
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_Twitter_Sonic(b *testing.B) {
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
		var buf bytes.Buffer
		enc := sonic.ConfigDefault.NewEncoder(&buf)
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_Twitter_GoJSON(b *testing.B) {
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
		var buf bytes.Buffer
		enc := gojson.NewEncoder(&buf)
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_Encoder_Twitter_Velox(b *testing.B) {
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
		var buf bytes.Buffer
		enc := vjson.NewEncoder(&buf)
		for range twitterEncodeCount {
			if err := enc.Encode(v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// =============================================================================
// SingleLarge: encode one large Twitter value to measure per-call overhead.
// =============================================================================

func Benchmark_Encoder_SingleLarge_StdJSON(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = json.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Encoder_SingleLarge_Sonic(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = sonic.ConfigDefault.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		if err := sonic.ConfigDefault.NewEncoder(&buf).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Encoder_SingleLarge_GoJSON(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = gojson.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		if err := gojson.NewEncoder(&buf).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Encoder_SingleLarge_Velox(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = vjson.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		if err := vjson.NewEncoder(&buf).Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Discard: encode to a no-op writer to isolate encoding speed from
// buffer allocation / memory copy overhead.
// =============================================================================

func Benchmark_EncoderDiscard_KubePods_StdJSON(b *testing.B) {
	v := loadPodsValue()
	var cw countWriter
	_ = json.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n * kubePodsEncodeCount)
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

func Benchmark_EncoderDiscard_KubePods_Velox(b *testing.B) {
	v := loadPodsValue()
	var cw countWriter
	_ = vjson.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n * kubePodsEncodeCount)
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

func Benchmark_EncoderDiscard_Twitter_StdJSON(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = json.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n * twitterEncodeCount)
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

func Benchmark_EncoderDiscard_Twitter_Velox(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	_ = vjson.NewEncoder(&cw).Encode(v)
	b.SetBytes(cw.n * twitterEncodeCount)
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
// EncodeValue (generic) benchmark — measures the zero-alloc typed path.
// =============================================================================

func Benchmark_EncodeValue_KubePods_Velox(b *testing.B) {
	v := loadPodsValue()
	var cw countWriter
	enc := vjson.NewEncoder(&cw)
	_ = vjson.EncodeValue(enc, v)
	b.SetBytes(cw.n * kubePodsEncodeCount)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		enc := vjson.NewEncoder(&buf)
		for range kubePodsEncodeCount {
			if err := vjson.EncodeValue(enc, v); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_EncodeValue_Twitter_Velox(b *testing.B) {
	v := loadTwitterValue()
	var cw countWriter
	enc := vjson.NewEncoder(&cw)
	_ = vjson.EncodeValue(enc, v)
	b.SetBytes(cw.n * twitterEncodeCount)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		enc := vjson.NewEncoder(&buf)
		for range twitterEncodeCount {
			if err := vjson.EncodeValue(enc, v); err != nil {
				b.Fatal(err)
			}
		}
	}
}
