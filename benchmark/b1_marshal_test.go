package benchmark

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"sync"
	"testing"

	"dev.local/benchmark/twitter"
	"dev.local/benchmark/twitter_typed"
	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Helper: pre-decode test data into typed structs for marshal benchmarks
// =============================================================================

var (
	tinyValueOnce sync.Once
	tinyValue     Tiny

	smallValueOnce sync.Once
	smallValue     Book

	escapeHeavyValueOnce sync.Once
	escapeHeavyValue     EscapeHeavyPayload

	podsValueOnce sync.Once
	podsValue     KubePodList

	twitterValueOnce sync.Once
	twitterValue     twitter.TwitterStruct
)

func loadTinyValue() *Tiny {
	tinyValueOnce.Do(func() {
		if err := json.Unmarshal(LoadTinyCompactJSON(), &tinyValue); err != nil {
			panic("load tiny: " + err.Error())
		}
	})
	return &tinyValue
}

func loadSmallValue() *Book {
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

// marshalSize returns the JSON output size for SetBytes throughput reporting.
func marshalSize(v any) int64 {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return int64(len(data))
}

// =============================================================================
// Tiny: flat struct, 5 basic-type fields
// =============================================================================

func Benchmark_Marshal_Tiny_Sonic(b *testing.B) {
	b.SetBytes(marshalSize(loadTinyValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadTinyValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Tiny_GoJSON(b *testing.B) {
	b.SetBytes(marshalSize(loadTinyValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := gojson.Marshal(loadTinyValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_Tiny_JSONv2(b *testing.B) {
	b.SetBytes(marshalSize(loadTinyValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jsonv2.Marshal(loadTinyValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_Tiny_Velox(b *testing.B) {
	b.SetBytes(marshalSize(loadTinyValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadTinyValue()); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Small: nested struct with slices (Sonic Book/Author)
// =============================================================================

func Benchmark_Marshal_Small_Sonic(b *testing.B) {
	b.SetBytes(marshalSize(loadSmallValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadSmallValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Small_GoJSON(b *testing.B) {
	b.SetBytes(marshalSize(loadSmallValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := gojson.Marshal(loadSmallValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_Small_JSONv2(b *testing.B) {
	b.SetBytes(marshalSize(loadSmallValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jsonv2.Marshal(loadSmallValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_Small_Velox(b *testing.B) {
	b.SetBytes(marshalSize(loadSmallValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadSmallValue()); err != nil {
			b.Fatal(err)
		}
	}
}

var (
	mediumValueOnce sync.Once
	mediumValue     MediumPayload
)

func loadMediumValue() *MediumPayload {
	mediumValueOnce.Do(func() {
		if err := json.Unmarshal(LoadMediumCompactJSON(), &mediumValue); err != nil {
			panic("load medium: " + err.Error())
		}
	})
	return &mediumValue
}

// =============================================================================
// Medium: 2.3 KB FullContact-style person-enrichment record. Same
// fixture as b1_unmarshal_test.go; struct defined in benchmark/schema.go.
// =============================================================================

func Benchmark_Marshal_Medium_Sonic(b *testing.B) {
	b.SetBytes(marshalSize(loadMediumValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadMediumValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Medium_GoJSON(b *testing.B) {
	b.SetBytes(marshalSize(loadMediumValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := gojson.Marshal(loadMediumValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Medium_JSONv2(b *testing.B) {
	b.SetBytes(marshalSize(loadMediumValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jsonv2.Marshal(loadMediumValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Medium_Velox(b *testing.B) {
	b.SetBytes(marshalSize(loadMediumValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadMediumValue()); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density
// =============================================================================

func Benchmark_Marshal_EscapeHeavy_Sonic(b *testing.B) {
	b.SetBytes(marshalSize(loadEscapeHeavyValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadEscapeHeavyValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_EscapeHeavy_GoJSON(b *testing.B) {
	b.SetBytes(marshalSize(loadEscapeHeavyValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := gojson.Marshal(loadEscapeHeavyValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_EscapeHeavy_JSONv2(b *testing.B) {
	b.SetBytes(marshalSize(loadEscapeHeavyValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jsonv2.Marshal(loadEscapeHeavyValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_EscapeHeavy_Velox(b *testing.B) {
	b.SetBytes(marshalSize(loadEscapeHeavyValue()))
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

func Benchmark_Marshal_KubePods_Sonic(b *testing.B) {
	b.SetBytes(marshalSize(loadPodsValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadPodsValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_KubePods_GoJSON(b *testing.B) {
	b.SetBytes(marshalSize(loadPodsValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := gojson.Marshal(loadPodsValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_KubePods_JSONv2(b *testing.B) {
	b.SetBytes(marshalSize(loadPodsValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jsonv2.Marshal(loadPodsValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_KubePods_Velox(b *testing.B) {
	b.SetBytes(marshalSize(loadPodsValue()))
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

func Benchmark_Marshal_Twitter_Sonic(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadTwitterValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_Twitter_GoJSON(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := gojson.Marshal(loadTwitterValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_Twitter_JSONv2(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jsonv2.Marshal(loadTwitterValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_Twitter_Velox(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadTwitterValue()); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// TwitterTyped: same data, all interface{} replaced with concrete types.
// Zero-yield benchmark: the C VM runs the entire struct without yielding.
// =============================================================================

var (
	twitterTypedValueOnce sync.Once
	twitterTypedValue     twitter_typed.TwitterStruct
)

func loadTwitterTypedValue() *twitter_typed.TwitterStruct {
	twitterTypedValueOnce.Do(func() {
		if err := json.Unmarshal(LoadTwitterCompactJSON(), &twitterTypedValue); err != nil {
			panic("load twitter_typed: " + err.Error())
		}
	})
	return &twitterTypedValue
}
func Benchmark_Marshal_TwitterTyped_Sonic(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterTypedValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sonic.Marshal(loadTwitterTypedValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_TwitterTyped_GoJSON(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterTypedValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := gojson.Marshal(loadTwitterTypedValue()); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_TwitterTyped_JSONv2(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterTypedValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := jsonv2.Marshal(loadTwitterTypedValue()); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_TwitterTyped_Velox(b *testing.B) {
	b.SetBytes(marshalSize(loadTwitterTypedValue()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(loadTwitterTypedValue()); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// MapAny: map[string]any – exercises encodeAnyMap / encodeAnyVal path
// Uses KubePods JSON decoded into map[string]any for realistic nested data.
// =============================================================================

var (
	mapAnyValueOnce sync.Once
	mapAnyValue     map[string]any
)

func loadMapAnyValue() *map[string]any {
	mapAnyValueOnce.Do(func() {
		if err := json.Unmarshal(LoadPodsCompactJSON(), &mapAnyValue); err != nil {
			panic("load map[string]any: " + err.Error())
		}
	})
	return &mapAnyValue
}
func Benchmark_Marshal_MapAny_Sonic(b *testing.B) {
	v := loadMapAnyValue()
	b.SetBytes(marshalSize(v))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := sonic.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_MapAny_GoJSON(b *testing.B) {
	v := loadMapAnyValue()
	b.SetBytes(marshalSize(v))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := gojson.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}
func Benchmark_Marshal_MapAny_JSONv2(b *testing.B) {
	v := loadMapAnyValue()
	b.SetBytes(marshalSize(v))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := jsonv2.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_MapAny_Velox(b *testing.B) {
	v := loadMapAnyValue()
	b.SetBytes(marshalSize(v))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := vjson.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}
