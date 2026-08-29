package benchmark

import (
	jsonv2 "encoding/json/v2"
	"testing"

	"dev.local/benchmark/twitter"
	"dev.local/benchmark/twitter_typed"
	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/vdec"
)

// =============================================================================
// Tiny: flat struct, 5 basic-type fields
// =============================================================================

func Benchmark_Unmarshal_Tiny_Sonic(b *testing.B) {
	b.SetBytes(int64(len(TinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := sonic.Unmarshal(TinyJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_Tiny_GoJSON(b *testing.B) {
	b.SetBytes(int64(len(TinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := gojson.Unmarshal(TinyJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_Tiny_JSONv2(b *testing.B) {
	b.SetBytes(int64(len(TinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := jsonv2.Unmarshal(TinyJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_Tiny_Velox(b *testing.B) {
	b.SetBytes(int64(len(TinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := vjson.Unmarshal(TinyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Tiny_VeloxGo(b *testing.B) {
	b.SetBytes(int64(len(TinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := vdec.Unmarshal(TinyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Tiny Compact: same as Tiny but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_TinyCompact_Sonic(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.SetBytes(int64(len(data)))
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
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := gojson.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_TinyCompact_JSONv2(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var s Tiny
		if err := jsonv2.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_TinyCompact_Velox(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := vjson.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_TinyCompact_VeloxGo(b *testing.B) {
	data := LoadTinyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := vdec.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Small: nested struct with slices (Sonic Book/Author)
// =============================================================================

func Benchmark_Unmarshal_Small_Sonic(b *testing.B) {
	b.SetBytes(int64(len(SmallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := sonic.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_Small_GoJSON(b *testing.B) {
	b.SetBytes(int64(len(SmallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := gojson.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_Small_JSONv2(b *testing.B) {
	b.SetBytes(int64(len(SmallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := jsonv2.Unmarshal(SmallJSON, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_Small_Velox(b *testing.B) {
	b.SetBytes(int64(len(SmallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := vjson.Unmarshal(SmallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Small_VeloxGo(b *testing.B) {
	b.SetBytes(int64(len(SmallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := vdec.Unmarshal(SmallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Small Compact: same as Small but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_SmallCompact_Sonic(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.SetBytes(int64(len(data)))
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
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := gojson.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_SmallCompact_JSONv2(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var s Book
		if err := jsonv2.Unmarshal(data, &s); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_SmallCompact_Velox(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := vjson.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SmallCompact_VeloxGo(b *testing.B) {
	data := LoadSmallCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := vdec.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Medium: 2.3 KB FullContact-style person-enrichment record. Same
// fixture as b7_rawextract_test.go; struct defined in benchmark/schema.go.
// =============================================================================

func Benchmark_Unmarshal_Medium_Sonic(b *testing.B) {
	data := MediumJSON
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := sonic.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_Medium_GoJSON(b *testing.B) {
	data := MediumJSON
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := gojson.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Medium_JSONv2(b *testing.B) {
	data := MediumJSON
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := jsonv2.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Medium_Velox(b *testing.B) {
	data := MediumJSON
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := vjson.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Medium_VeloxGo(b *testing.B) {
	data := MediumJSON
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := vdec.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Medium Compact: same as Medium but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_MediumCompact_Sonic(b *testing.B) {
	data := LoadMediumCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := sonic.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_MediumCompact_GoJSON(b *testing.B) {
	data := LoadMediumCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := gojson.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_MediumCompact_JSONv2(b *testing.B) {
	data := LoadMediumCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := jsonv2.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_MediumCompact_Velox(b *testing.B) {
	data := LoadMediumCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := vjson.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_MediumCompact_VeloxGo(b *testing.B) {
	data := LoadMediumCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p MediumPayload
		if err := vdec.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density (corpus escape_heavy)
// =============================================================================

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
func Benchmark_Unmarshal_EscapeHeavy_JSONv2(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := jsonv2.Unmarshal(EscapeHeavyJSON, &p); err != nil {
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
func Benchmark_Unmarshal_EscapeHeavy_VeloxGo(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := vdec.Unmarshal(EscapeHeavyJSON, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy Compact: same as EscapeHeavy but with whitespace stripped
// =============================================================================

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
func Benchmark_Unmarshal_EscapeHeavyCompact_JSONv2(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := jsonv2.Unmarshal(data, &p); err != nil {
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

func Benchmark_Unmarshal_EscapeHeavyCompact_VeloxGo(b *testing.B) {
	data := LoadEscapeHeavyCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var p EscapeHeavyPayload
		if err := vdec.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Pods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Unmarshal_KubePods_Sonic(b *testing.B) {
	b.SetBytes(int64(len(KubePodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := sonic.Unmarshal(KubePodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_KubePods_GoJSON(b *testing.B) {
	b.SetBytes(int64(len(KubePodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := gojson.Unmarshal(KubePodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Unmarshal_KubePods_JSONv2(b *testing.B) {
	b.SetBytes(int64(len(KubePodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := jsonv2.Unmarshal(KubePodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_Velox(b *testing.B) {
	b.SetBytes(int64(len(KubePodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vjson.Unmarshal(KubePodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_VeloxGo(b *testing.B) {
	b.SetBytes(int64(len(KubePodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vdec.Unmarshal(KubePodsJSON, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// KubePods Compact: same as KubePods but with whitespace stripped
// =============================================================================

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
func Benchmark_Unmarshal_KubePodsCompact_JSONv2(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := jsonv2.Unmarshal(data, &pl); err != nil {
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

func Benchmark_Unmarshal_KubePodsCompact_VeloxGo(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vdec.Unmarshal(data, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// KubePods Padded: caller-padded buffer via UnmarshalPadded. Exercises the
// UnmarshalPadded entry point (currently still copies through padBuf; the
// contract is in place for future zero-copy work). Same payload as KubePods
// and KubePodsCompact, pre-padded once outside the loop.
// =============================================================================

var kubePodsPadded = vjson.Pad(KubePodsJSON)
var kubePodsCompactPadded = vjson.Pad(LoadPodsCompactJSON())

func Benchmark_Unmarshal_KubePods_Velox_Padded(b *testing.B) {
	b.SetBytes(int64(len(kubePodsPadded)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vjson.UnmarshalPadded(kubePodsPadded, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePodsCompact_Velox_Padded(b *testing.B) {
	b.SetBytes(int64(len(kubePodsCompactPadded)))
	b.ReportAllocs()
	for b.Loop() {
		var pl KubePodList
		if err := vjson.UnmarshalPadded(kubePodsCompactPadded, &pl); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePodsCompact_Velox_Padded_StrictScan(b *testing.B) {
	b.SetBytes(int64(len(kubePodsCompactPadded)))
	b.ReportAllocs()
	strictScan := vjson.WithStrictScan()
	for b.Loop() {
		var pl KubePodList
		if err := vjson.UnmarshalPadded(kubePodsCompactPadded, &pl, strictScan); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Twitter: Twitter search API response (~617KB, deeply nested, many fields)
// =============================================================================

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

func Benchmark_Unmarshal_Twitter_JSONv2(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := jsonv2.Unmarshal(TwitterJSON, &t); err != nil {
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

func Benchmark_Unmarshal_Twitter_VeloxGo(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := vdec.Unmarshal(TwitterJSON, &t); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Twitter Compact: same as Twitter but with whitespace stripped
// =============================================================================

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
func Benchmark_Unmarshal_TwitterCompact_JSONv2(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := jsonv2.Unmarshal(data, &t); err != nil {
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

func Benchmark_Unmarshal_TwitterCompact_VeloxGo(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter.TwitterStruct
		if err := vdec.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// TwitterTyped: same data, all interface{} replaced with concrete types.
// =============================================================================

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
func Benchmark_Unmarshal_TwitterTyped_JSONv2(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter_typed.TwitterStruct
		if err := jsonv2.Unmarshal(data, &t); err != nil {
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

func Benchmark_Unmarshal_TwitterTyped_VeloxGo(b *testing.B) {
	data := LoadTwitterCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var t twitter_typed.TwitterStruct
		if err := vdec.Unmarshal(data, &t); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// MapAny: map[string]any – exercises the decodeAnyMap / decodeAnyVal path
// (unmarshal counterpart of marshal's MapAny). Decodes KubePods JSON into
// map[string]any for realistic nested data.
// =============================================================================

func Benchmark_Unmarshal_MapAny_Sonic(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v map[string]any
		if err := sonic.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_MapAny_GoJSON(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v map[string]any
		if err := gojson.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_MapAny_JSONv2(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v map[string]any
		if err := jsonv2.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_MapAny_Velox(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v map[string]any
		if err := vjson.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_MapAny_VeloxGo(b *testing.B) {
	data := LoadPodsCompactJSON()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var v map[string]any
		if err := vdec.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}
