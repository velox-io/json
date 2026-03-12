package benchmark

import (
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Small: flat struct, 5 basic-type fields
// =============================================================================

func Benchmark_Small_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := sonic.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Small_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v Small
		if err := vjson.Unmarshal(SmallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Small Compact: same as Small but with whitespace stripped
// =============================================================================

func Benchmark_Small_Compact_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := sonic.Unmarshal(SmallCompactJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Small_Compact_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v Small
		if err := vjson.Unmarshal(SmallCompactJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density (testdata/escape_heavy.json)
// =============================================================================

func Benchmark_EscapeHeavy_Sonic(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := sonic.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_EscapeHeavy_Velox(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := vjson.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy Compact: same as EscapeHeavy but with whitespace stripped
// =============================================================================

func Benchmark_EscapeHeavy_Compact_Sonic(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyCompactJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := sonic.Unmarshal(EscapeHeavyCompactJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_EscapeHeavy_Compact_Velox(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyCompactJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := vjson.Unmarshal(EscapeHeavyCompactJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Pods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_KubePods_Sonic(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := sonic.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_KubePods_Velox(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vjson.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// KubePods Compact: same as KubePods but with whitespace stripped
// =============================================================================

func Benchmark_KubePods_Compact_Sonic(b *testing.B) {
	b.SetBytes(int64(len(PodsCompactJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := sonic.Unmarshal(PodsCompactJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_KubePods_Compact_Velox(b *testing.B) {
	b.SetBytes(int64(len(PodsCompactJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vjson.Unmarshal(PodsCompactJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Twitter: Twitter search API response (~617KB, deeply nested, many fields)
// =============================================================================

func Benchmark_Twitter_Sonic(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := sonic.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Twitter_Velox(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := vjson.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Twitter Compact: same as Twitter but with whitespace stripped
// =============================================================================

func Benchmark_Twitter_Compact_Sonic(b *testing.B) {
	b.SetBytes(int64(len(TwitterCompactJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := sonic.Unmarshal(TwitterCompactJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Twitter_Compact_Velox(b *testing.B) {
	b.SetBytes(int64(len(TwitterCompactJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := vjson.Unmarshal(TwitterCompactJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

