package benchmark

import (
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Parallel Unmarshal EscapeHeavy: real-world ~4KB JSON with ~40% escape density
// =============================================================================

func Benchmark_Parallel_Unmarshal_EscapeHeavy_Sonic(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var p EscapeHeavyPayload
			if err := sonic.Unmarshal(EscapeHeavyJSON, &p); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Unmarshal_EscapeHeavy_Velox(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var p EscapeHeavyPayload
			if err := vjson.Unmarshal(EscapeHeavyJSON, &p); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// =============================================================================
// Parallel Unmarshal KubePods: Kubernetes Pod List (~25KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Parallel_Unmarshal_KubePods_Sonic(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var pl KubePodList
			if err := sonic.Unmarshal(PodsJSON, &pl); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Unmarshal_KubePods_Velox(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var pl KubePodList
			if err := vjson.Unmarshal(PodsJSON, &pl); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// =============================================================================
// Parallel Unmarshal Twitter: Twitter search API response (~617KB, deeply nested)
// =============================================================================

func Benchmark_Parallel_Unmarshal_Twitter_Sonic(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var t twitter.TwitterStruct
			if err := sonic.Unmarshal(TwitterJSON, &t); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Unmarshal_Twitter_Velox(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var t twitter.TwitterStruct
			if err := vjson.Unmarshal(TwitterJSON, &t); err != nil {
				b.Fatal(err)
			}
		}
	})
}

