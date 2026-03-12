package benchmark

import (
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	"github.com/penglei/pjson"
	"github.com/penglei/pjson/s2s"
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

func Benchmark_Small_S2S(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v Small
		if err := s2s.Unmarshal(SmallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Small_Pjson(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := pjson.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Nested: User + Address (2-level struct)
// =============================================================================

func Benchmark_Nested_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := sonic.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Nested_S2S(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := s2s.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Nested_Pjson(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := pjson.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// SliceOfStructs: 5 Users in an array
// =============================================================================

func Benchmark_SliceOfStructs_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := sonic.Unmarshal(SliceJSON, &ul); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_SliceOfStructs_S2S(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := s2s.Unmarshal(SliceJSON, &ul); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_SliceOfStructs_Pjson(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := pjson.Unmarshal(SliceJSON, &ul); err != nil {
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

func Benchmark_EscapeHeavy_S2S(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := s2s.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_EscapeHeavy_Pjson(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := pjson.Unmarshal(EscapeHeavyJSON, &p); err != nil {
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

func Benchmark_KubePods_S2S(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := s2s.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_KubePods_Pjson(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := pjson.Unmarshal(PodsJSON, &pl); err != nil {
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

func Benchmark_Twitter_S2S(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := s2s.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Twitter_Pjson(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := pjson.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}
