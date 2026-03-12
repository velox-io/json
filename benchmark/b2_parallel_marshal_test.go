package benchmark

import (
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Parallel Marshal EscapeHeavy: real-world ~4KB JSON with ~40% escape density
// =============================================================================

func Benchmark_Parallel_Marshal_EscapeHeavy_StdJSON(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := json.Marshal(&escapeHeavyValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Marshal_EscapeHeavy_Sonic(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := sonic.Marshal(&escapeHeavyValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Marshal_EscapeHeavy_Velox(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := vjson.Marshal(&escapeHeavyValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// =============================================================================
// Parallel Marshal KubePods: Kubernetes Pod List (~25KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Parallel_Marshal_KubePods_StdJSON(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := json.Marshal(&podsValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Marshal_KubePods_Sonic(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := sonic.Marshal(&podsValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Marshal_KubePods_Velox(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := vjson.Marshal(&podsValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// =============================================================================
// Parallel Marshal Twitter: Twitter search API response (~617KB, deeply nested)
// =============================================================================

func Benchmark_Parallel_Marshal_Twitter_StdJSON(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := json.Marshal(&twitterValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Marshal_Twitter_Sonic(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := sonic.Marshal(&twitterValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func Benchmark_Parallel_Marshal_Twitter_Velox(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := vjson.Marshal(&twitterValue); err != nil {
				b.Fatal(err)
			}
		}
	})
}
