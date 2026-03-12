package benchmark

import (
	"encoding/json"
	"testing"

	"dev.local/benchmark/twitter"

	"github.com/bytedance/sonic"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/penglei/pjson"
)

// =============================================================================
// Small: flat struct, 5 basic-type fields
// =============================================================================

func Benchmark_Sonic_Small(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := sonic.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Pjson_Small(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := pjson.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV1_Small(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := json.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV2_Small(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Small
		if err := jsonv2.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Nested: User + Address (2-level struct)
// =============================================================================

func Benchmark_Sonic_Nested(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := sonic.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Pjson_Nested(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := pjson.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV1_Nested(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := json.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV2_Nested(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var u User
		if err := jsonv2.Unmarshal(NestedJSON, &u); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// SliceOfStructs: 5 Users in an array
// =============================================================================

func Benchmark_Sonic_SliceOfStructs(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := sonic.Unmarshal(SliceJSON, &ul); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Pjson_SliceOfStructs(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := pjson.Unmarshal(SliceJSON, &ul); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV1_SliceOfStructs(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := json.Unmarshal(SliceJSON, &ul); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV2_SliceOfStructs(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var ul UserList
		if err := jsonv2.Unmarshal(SliceJSON, &ul); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density (testdata/escape_heavy.json)
// =============================================================================

func Benchmark_Sonic_EscapeHeavy(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := sonic.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Pjson_EscapeHeavy(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := pjson.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV1_EscapeHeavy(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := json.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV2_EscapeHeavy(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := jsonv2.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Pods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Sonic_KubePods(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := sonic.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Pjson_KubePods(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := pjson.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV1_KubePods(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := json.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV2_KubePods(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := jsonv2.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Twitter: Twitter search API response (~617KB, deeply nested, many fields)
// =============================================================================

func Benchmark_Sonic_Twitter(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := sonic.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Pjson_Twitter(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := pjson.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV1_Twitter(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := json.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_JsonV2_Twitter(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := jsonv2.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}
