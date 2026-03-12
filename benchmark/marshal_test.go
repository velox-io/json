package benchmark

import (
	"encoding/json"
	"sync"
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Helper: pre-decode test data into typed structs for marshal benchmarks
// =============================================================================

var (
	smallValueOnce sync.Once
	smallValue     Small

	escapeHeavyValueOnce sync.Once
	escapeHeavyValue     EscapeHeavyPayload

	podsValueOnce sync.Once
	podsValue     KubePodList

	twitterValueOnce sync.Once
	twitterValue     twitter.TwitterStruct
)

func loadSmallValue() *Small {
	smallValueOnce.Do(func() {
		if err := json.Unmarshal(LoadSmallCompactJSON(), &smallValue); err != nil {
			panic("load small: " + err.Error())
		}
	})
	return &smallValue
}

func loadEscapeHeavyValue() *EscapeHeavyPayload {
	escapeHeavyValueOnce.Do(func() {
		if err := json.Unmarshal(LoadEscapeHeavyCompactJSON(), &escapeHeavyValue); err != nil {
			panic("load escape_heavy: " + err.Error())
		}
	})
	return &escapeHeavyValue
}

func loadPodsValue() *KubePodList {
	podsValueOnce.Do(func() {
		if err := json.Unmarshal(LoadPodsCompactJSON(), &podsValue); err != nil {
			panic("load pods: " + err.Error())
		}
	})
	return &podsValue
}

func loadTwitterValue() *twitter.TwitterStruct {
	twitterValueOnce.Do(func() {
		if err := json.Unmarshal(LoadTwitterCompactJSON(), &twitterValue); err != nil {
			panic("load twitter: " + err.Error())
		}
	})
	return &twitterValue
}

// =============================================================================
// Small: flat struct, 5 basic-type fields
// =============================================================================

func Benchmark_Marshal_Small_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(loadSmallValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Small_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadSmallValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Small_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadSmallValue()); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density
// =============================================================================

func Benchmark_Marshal_EscapeHeavy_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(loadEscapeHeavyValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_EscapeHeavy_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadEscapeHeavyValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_EscapeHeavy_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadEscapeHeavyValue()); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// KubePods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Marshal_KubePods_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(loadPodsValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_KubePods_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadPodsValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_KubePods_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadPodsValue()); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Twitter: Twitter search API response (~617KB, deeply nested, many fields)
// =============================================================================

func Benchmark_Marshal_Twitter_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(loadTwitterValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Twitter_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadTwitterValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Twitter_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadTwitterValue()); err != nil {
			b.Fatal(err)
		}
	}
}
