package benchmark

import (
	"encoding/json"
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Helper: pre-decode test data into typed structs for marshal benchmarks
// =============================================================================

var (
	smallValue       Small
	escapeHeavyValue EscapeHeavyPayload
	podsValue        KubePodList
	twitterValue     twitter.TwitterStruct
)

func init() {
	if err := json.Unmarshal(SmallCompactJSON, &smallValue); err != nil {
		panic("init small: " + err.Error())
	}
	if err := json.Unmarshal(EscapeHeavyCompactJSON, &escapeHeavyValue); err != nil {
		panic("init escape_heavy: " + err.Error())
	}
	if err := json.Unmarshal(PodsCompactJSON, &podsValue); err != nil {
		panic("init pods: " + err.Error())
	}
	if err := json.Unmarshal(TwitterCompactJSON, &twitterValue); err != nil {
		panic("init twitter: " + err.Error())
	}
}

// =============================================================================
// Small: flat struct, 5 basic-type fields
// =============================================================================

func Benchmark_Marshal_Small_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&smallValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Small_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(&smallValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Small_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(&smallValue); err != nil {
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
		if _, err := json.Marshal(&escapeHeavyValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_EscapeHeavy_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(&escapeHeavyValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_EscapeHeavy_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(&escapeHeavyValue); err != nil {
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
		if _, err := json.Marshal(&podsValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_KubePods_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(&podsValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_KubePods_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(&podsValue); err != nil {
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
		if _, err := json.Marshal(&twitterValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Twitter_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(&twitterValue); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Twitter_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(&twitterValue); err != nil {
			b.Fatal(err)
		}
	}
}
