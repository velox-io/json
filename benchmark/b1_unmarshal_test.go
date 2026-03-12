package benchmark

import (
	stdjson "encoding/json"
	"testing"

	"dev.local/benchmark/easyjson"
	"dev.local/benchmark/twitter"
	"dev.local/benchmark/twitter_typed"

	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Tiny: flat struct, 5 basic-type fields
// =============================================================================

func Benchmark_Unmarshal_Tiny_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := stdjson.Unmarshal(TinyJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Tiny_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := sonic.Unmarshal(TinyJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Tiny_GoJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := gojson.Unmarshal(TinyJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Tiny_EasyJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalTiny(TinyJSON); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Tiny_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := vjson.Unmarshal(TinyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Tiny Compact: same as Tiny but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_TinyCompact_StdJSON(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := stdjson.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TinyCompact_Sonic(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := sonic.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TinyCompact_GoJSON(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := gojson.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TinyCompact_EasyJSON(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalTiny(data); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TinyCompact_Velox(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := vjson.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Small: nested struct with slices (Sonic Book/Author)
// =============================================================================

func Benchmark_Unmarshal_Small_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := stdjson.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Small_Sonic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := sonic.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Small_GoJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := gojson.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Small_EasyJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalSmall(SmallJSON); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Small_Velox(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := vjson.Unmarshal(SmallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Small Compact: same as Small but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_SmallCompact_StdJSON(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := stdjson.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SmallCompact_Sonic(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := sonic.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SmallCompact_GoJSON(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := gojson.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SmallCompact_EasyJSON(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalSmall(data); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SmallCompact_Velox(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := vjson.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density (testdata/escape_heavy.json)
// =============================================================================

func Benchmark_Unmarshal_EscapeHeavy_StdJSON(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := stdjson.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavy_Sonic(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := sonic.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavy_GoJSON(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := gojson.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavy_EasyJSON(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalEscapeHeavy(EscapeHeavyJSON); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavy_Velox(b *testing.B) {
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

func Benchmark_Unmarshal_EscapeHeavyCompact_StdJSON(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := stdjson.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavyCompact_Sonic(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := sonic.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavyCompact_GoJSON(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := gojson.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavyCompact_EasyJSON(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalEscapeHeavy(data); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_EscapeHeavyCompact_Velox(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := vjson.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Pods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Unmarshal_KubePods_StdJSON(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := stdjson.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_Sonic(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := sonic.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_GoJSON(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := gojson.Unmarshal(PodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_EasyJSON(b *testing.B) {
	b.SetBytes(int64(len(PodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalKubePods(PodsJSON); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_Velox(b *testing.B) {
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

func Benchmark_Unmarshal_KubePodsCompact_StdJSON(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := stdjson.Unmarshal(data, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePodsCompact_Sonic(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := sonic.Unmarshal(data, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePodsCompact_GoJSON(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := gojson.Unmarshal(data, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePodsCompact_EasyJSON(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalKubePods(data); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePodsCompact_Velox(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vjson.Unmarshal(data, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Twitter: Twitter search API response (~617KB, deeply nested, many fields)
// =============================================================================

func Benchmark_Unmarshal_Twitter_StdJSON(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := stdjson.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Twitter_Sonic(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := sonic.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Twitter_GoJSON(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := gojson.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Twitter_EasyJSON(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalTwitter(TwitterJSON); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Twitter_Velox(b *testing.B) {
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

func Benchmark_Unmarshal_TwitterCompact_StdJSON(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := stdjson.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TwitterCompact_Sonic(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := sonic.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TwitterCompact_GoJSON(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := gojson.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TwitterCompact_EasyJSON(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := easyjson.UnmarshalTwitter(data); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TwitterCompact_Velox(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := vjson.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// TwitterTyped: same data, all interface{} replaced with concrete types.
// =============================================================================

func Benchmark_Unmarshal_TwitterTyped_StdJSON(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter_typed.TwitterStruct
		if err := stdjson.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TwitterTyped_Sonic(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter_typed.TwitterStruct
		if err := sonic.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TwitterTyped_GoJSON(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter_typed.TwitterStruct
		if err := gojson.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TwitterTyped_Velox(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter_typed.TwitterStruct
		if err := vjson.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}
