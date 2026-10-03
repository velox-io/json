package benchmark

import (
	"encoding/json"
	"sync"
	"testing"

	"dev.local/benchmark/twitter"
	"dev.local/benchmark/twitter_typed"
)

// =============================================================================
// Helper: pre-decode test data into typed structs for marshal benchmarks
// =============================================================================

var (
	smallValueOnce sync.Once
	smallValue     Book

	escapeHeavyValueOnce sync.Once
	escapeHeavyValue     EscapeHeavyPayload

	podsValueOnce sync.Once
	podsValue     KubePodList

	twitterValueOnce sync.Once
	twitterValue     twitter.TwitterStruct
)

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

// =============================================================================
// Small: nested struct with slices (Sonic Book/Author)
// =============================================================================

func Benchmark_Marshal_Small_Sonic(b *testing.B)  { benchMarshalSonic(b, loadSmallValue()) }
func Benchmark_Marshal_Small_GoJSON(b *testing.B) { benchMarshalGoJSON(b, loadSmallValue()) }
func Benchmark_Marshal_Small_JSONv2(b *testing.B) { benchMarshalJSONv2(b, loadSmallValue()) }
func Benchmark_Marshal_Small_Velox(b *testing.B)  { benchMarshalVelox(b, loadSmallValue()) }

// =============================================================================
// Medium: 2.3 KB FullContact-style person-enrichment record. Same
// fixture as b1_unmarshal_test.go; struct defined in benchmark/schema.go.
// =============================================================================

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

func Benchmark_Marshal_Medium_Sonic(b *testing.B)  { benchMarshalSonic(b, loadMediumValue()) }
func Benchmark_Marshal_Medium_GoJSON(b *testing.B) { benchMarshalGoJSON(b, loadMediumValue()) }
func Benchmark_Marshal_Medium_JSONv2(b *testing.B) { benchMarshalJSONv2(b, loadMediumValue()) }
func Benchmark_Marshal_Medium_Velox(b *testing.B)  { benchMarshalVelox(b, loadMediumValue()) }

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density
// =============================================================================

func Benchmark_Marshal_EscapeHeavy_Sonic(b *testing.B) { benchMarshalSonic(b, loadEscapeHeavyValue()) }
func Benchmark_Marshal_EscapeHeavy_GoJSON(b *testing.B) {
	benchMarshalGoJSON(b, loadEscapeHeavyValue())
}
func Benchmark_Marshal_EscapeHeavy_JSONv2(b *testing.B) {
	benchMarshalJSONv2(b, loadEscapeHeavyValue())
}
func Benchmark_Marshal_EscapeHeavy_Velox(b *testing.B) { benchMarshalVelox(b, loadEscapeHeavyValue()) }

// =============================================================================
// KubePods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Marshal_KubePods_Sonic(b *testing.B)  { benchMarshalSonic(b, loadPodsValue()) }
func Benchmark_Marshal_KubePods_GoJSON(b *testing.B) { benchMarshalGoJSON(b, loadPodsValue()) }
func Benchmark_Marshal_KubePods_JSONv2(b *testing.B) { benchMarshalJSONv2(b, loadPodsValue()) }
func Benchmark_Marshal_KubePods_Velox(b *testing.B)  { benchMarshalVelox(b, loadPodsValue()) }

// =============================================================================
// Twitter: Twitter search API response (~617KB, deeply nested, many fields)
// =============================================================================

func Benchmark_Marshal_Twitter_Sonic(b *testing.B)  { benchMarshalSonic(b, loadTwitterValue()) }
func Benchmark_Marshal_Twitter_GoJSON(b *testing.B) { benchMarshalGoJSON(b, loadTwitterValue()) }
func Benchmark_Marshal_Twitter_JSONv2(b *testing.B) { benchMarshalJSONv2(b, loadTwitterValue()) }
func Benchmark_Marshal_Twitter_Velox(b *testing.B)  { benchMarshalVelox(b, loadTwitterValue()) }

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
	benchMarshalSonic(b, loadTwitterTypedValue())
}
func Benchmark_Marshal_TwitterTyped_GoJSON(b *testing.B) {
	benchMarshalGoJSON(b, loadTwitterTypedValue())
}
func Benchmark_Marshal_TwitterTyped_JSONv2(b *testing.B) {
	benchMarshalJSONv2(b, loadTwitterTypedValue())
}
func Benchmark_Marshal_TwitterTyped_Velox(b *testing.B) {
	benchMarshalVelox(b, loadTwitterTypedValue())
}

// =============================================================================
// GitHubIssues: GitHub REST API issues (~186KB, 30 issues). Pointer-heavy
// go-github types; every field has omitempty, and the times marshal by the
// embedded time.Time.
// =============================================================================

var (
	githubIssuesValueOnce sync.Once
	githubIssuesValue     []*GitHubIssue
)

func loadGitHubIssuesValue() []*GitHubIssue {
	githubIssuesValueOnce.Do(func() {
		if err := json.Unmarshal(LoadGitHubIssuesJSON(), &githubIssuesValue); err != nil {
			panic("load github issues: " + err.Error())
		}
	})
	return githubIssuesValue
}

func Benchmark_Marshal_GitHubIssues_Sonic(b *testing.B) {
	benchMarshalSonic(b, loadGitHubIssuesValue())
}
func Benchmark_Marshal_GitHubIssues_GoJSON(b *testing.B) {
	benchMarshalGoJSON(b, loadGitHubIssuesValue())
}
func Benchmark_Marshal_GitHubIssues_JSONv2(b *testing.B) {
	benchMarshalJSONv2(b, loadGitHubIssuesValue())
}
func Benchmark_Marshal_GitHubIssues_Velox(b *testing.B) {
	benchMarshalVelox(b, loadGitHubIssuesValue())
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

func Benchmark_Marshal_MapAny_Sonic(b *testing.B)  { benchMarshalSonic(b, loadMapAnyValue()) }
func Benchmark_Marshal_MapAny_GoJSON(b *testing.B) { benchMarshalGoJSON(b, loadMapAnyValue()) }
func Benchmark_Marshal_MapAny_JSONv2(b *testing.B) { benchMarshalJSONv2(b, loadMapAnyValue()) }
func Benchmark_Marshal_MapAny_Velox(b *testing.B)  { benchMarshalVelox(b, loadMapAnyValue()) }

// =============================================================================
// SmallMapAny: small flat map[string]any (database audit log line, 24 keys).
// Marshal counterpart of Unmarshal SmallMapAny: fixed per-call costs dominate
// over per-byte encoding.
// =============================================================================

var (
	smallMapAnyValueOnce sync.Once
	smallMapAnyValue     map[string]any
)

func loadSmallMapAnyValue() *map[string]any {
	smallMapAnyValueOnce.Do(func() {
		if err := json.Unmarshal(SmallMapAnyBytes, &smallMapAnyValue); err != nil {
			panic("load small map[string]any: " + err.Error())
		}
	})
	return &smallMapAnyValue
}

func Benchmark_Marshal_SmallMapAny_Sonic(b *testing.B) { benchMarshalSonic(b, loadSmallMapAnyValue()) }
func Benchmark_Marshal_SmallMapAny_GoJSON(b *testing.B) {
	benchMarshalGoJSON(b, loadSmallMapAnyValue())
}
func Benchmark_Marshal_SmallMapAny_JSONv2(b *testing.B) {
	benchMarshalJSONv2(b, loadSmallMapAnyValue())
}
func Benchmark_Marshal_SmallMapAny_Velox(b *testing.B) { benchMarshalVelox(b, loadSmallMapAnyValue()) }
