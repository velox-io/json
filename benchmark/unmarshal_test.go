package benchmark

import (
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	"github.com/penglei/veloxjson"
	"github.com/penglei/veloxjson/prescan"
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

func Benchmark_Small_Prescan(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := prescan.Unmarshal(SmallJSON, &s); err != nil {
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

func Benchmark_Nested_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := vjson.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Nested_Prescan(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := prescan.Unmarshal(NestedJSON, &u); err != nil {
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

func Benchmark_SliceOfStructs_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := vjson.Unmarshal(SliceJSON, &ul); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_SliceOfStructs_Prescan(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := prescan.Unmarshal(SliceJSON, &ul); err != nil {
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

func Benchmark_EscapeHeavy_Prescan(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := prescan.Unmarshal(EscapeHeavyJSON, &p); err != nil {
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

func Benchmark_KubePods_Prescan(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := prescan.Unmarshal(PodsJSON, &pl); err != nil {
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

func Benchmark_Twitter_Prescan(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := prescan.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}
