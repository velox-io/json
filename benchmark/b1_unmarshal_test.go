package benchmark

import (
	"encoding/json"
	"reflect"
	"testing"

	"dev.local/benchmark/twitter"
	"dev.local/benchmark/twitter_typed"
	vjson "github.com/velox-io/json"
)

// =============================================================================
// Small: nested struct with slices (Sonic Book/Author)
// =============================================================================

func Benchmark_Unmarshal_Small_Sonic(b *testing.B)  { benchUnmarshalSonic[Book](b, SmallJSON) }
func Benchmark_Unmarshal_Small_GoJSON(b *testing.B) { benchUnmarshalGoJSON[Book](b, SmallJSON) }
func Benchmark_Unmarshal_Small_JSONv2(b *testing.B) { benchUnmarshalJSONv2[Book](b, SmallJSON) }
func Benchmark_Unmarshal_Small_Velox(b *testing.B)  { benchUnmarshalVelox[Book](b, SmallJSON) }

// =============================================================================
// Small Compact: same as Small but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_SmallCompact_Sonic(b *testing.B) {
	benchUnmarshalSonic[Book](b, LoadSmallCompactJSON())
}
func Benchmark_Unmarshal_SmallCompact_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[Book](b, LoadSmallCompactJSON())
}
func Benchmark_Unmarshal_SmallCompact_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[Book](b, LoadSmallCompactJSON())
}
func Benchmark_Unmarshal_SmallCompact_Velox(b *testing.B) {
	benchUnmarshalVelox[Book](b, LoadSmallCompactJSON())
}

// =============================================================================
// Medium: 2.3 KB FullContact-style person-enrichment record. Same
// fixture as b7_rawextract_test.go; struct defined in benchmark/schema.go.
// =============================================================================

func Benchmark_Unmarshal_Medium_Sonic(b *testing.B) {
	benchUnmarshalSonic[MediumPayload](b, MediumJSON)
}
func Benchmark_Unmarshal_Medium_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[MediumPayload](b, MediumJSON)
}
func Benchmark_Unmarshal_Medium_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[MediumPayload](b, MediumJSON)
}
func Benchmark_Unmarshal_Medium_Velox(b *testing.B) {
	benchUnmarshalVelox[MediumPayload](b, MediumJSON)
}

// =============================================================================
// Medium Compact: same as Medium but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_MediumCompact_Sonic(b *testing.B) {
	benchUnmarshalSonic[MediumPayload](b, LoadMediumCompactJSON())
}
func Benchmark_Unmarshal_MediumCompact_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[MediumPayload](b, LoadMediumCompactJSON())
}
func Benchmark_Unmarshal_MediumCompact_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[MediumPayload](b, LoadMediumCompactJSON())
}
func Benchmark_Unmarshal_MediumCompact_Velox(b *testing.B) {
	benchUnmarshalVelox[MediumPayload](b, LoadMediumCompactJSON())
}

// =============================================================================
// EscapeHeavy: real-world ~4KB JSON with ~40% escape density (corpus escape_heavy)
// =============================================================================

func Benchmark_Unmarshal_EscapeHeavy_Sonic(b *testing.B) {
	benchUnmarshalSonic[EscapeHeavyPayload](b, EscapeHeavyJSON)
}
func Benchmark_Unmarshal_EscapeHeavy_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[EscapeHeavyPayload](b, EscapeHeavyJSON)
}
func Benchmark_Unmarshal_EscapeHeavy_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[EscapeHeavyPayload](b, EscapeHeavyJSON)
}
func Benchmark_Unmarshal_EscapeHeavy_Velox(b *testing.B) {
	benchUnmarshalVelox[EscapeHeavyPayload](b, EscapeHeavyJSON)
}

// =============================================================================
// EscapeHeavy Compact: same as EscapeHeavy but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_EscapeHeavyCompact_Sonic(b *testing.B) {
	benchUnmarshalSonic[EscapeHeavyPayload](b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Unmarshal_EscapeHeavyCompact_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[EscapeHeavyPayload](b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Unmarshal_EscapeHeavyCompact_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[EscapeHeavyPayload](b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Unmarshal_EscapeHeavyCompact_Velox(b *testing.B) {
	benchUnmarshalVelox[EscapeHeavyPayload](b, LoadEscapeHeavyCompactJSON())
}

// =============================================================================
// Pods: Kubernetes Pod List (~4.6KB, deeply nested, 3 pods)
// =============================================================================

func Benchmark_Unmarshal_KubePods_Sonic(b *testing.B) {
	benchUnmarshalSonic[KubePodList](b, KubePodsJSON)
}
func Benchmark_Unmarshal_KubePods_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[KubePodList](b, KubePodsJSON)
}
func Benchmark_Unmarshal_KubePods_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[KubePodList](b, KubePodsJSON)
}
func Benchmark_Unmarshal_KubePods_Velox(b *testing.B) {
	benchUnmarshalVelox[KubePodList](b, KubePodsJSON)
}

// =============================================================================
// KubePods Compact: same as KubePods but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_KubePodsCompact_Sonic(b *testing.B) {
	benchUnmarshalSonic[KubePodList](b, LoadPodsCompactJSON())
}
func Benchmark_Unmarshal_KubePodsCompact_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[KubePodList](b, LoadPodsCompactJSON())
}
func Benchmark_Unmarshal_KubePodsCompact_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[KubePodList](b, LoadPodsCompactJSON())
}
func Benchmark_Unmarshal_KubePodsCompact_Velox(b *testing.B) {
	benchUnmarshalVelox[KubePodList](b, LoadPodsCompactJSON())
}

// =============================================================================
// KubePods Padded: caller-padded buffer via UnmarshalPadded. Both entries
// alias escape-free strings into the caller's buffer by default. Same payload
// as KubePods and KubePodsCompact, pre-padded once outside the loop.
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
	benchUnmarshalSonic[twitter.TwitterStruct](b, TwitterJSON)
}
func Benchmark_Unmarshal_Twitter_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[twitter.TwitterStruct](b, TwitterJSON)
}
func Benchmark_Unmarshal_Twitter_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[twitter.TwitterStruct](b, TwitterJSON)
}
func Benchmark_Unmarshal_Twitter_Velox(b *testing.B) {
	benchUnmarshalVelox[twitter.TwitterStruct](b, TwitterJSON)
}

// =============================================================================
// Twitter Compact: same as Twitter but with whitespace stripped
// =============================================================================

func Benchmark_Unmarshal_TwitterCompact_Sonic(b *testing.B) {
	benchUnmarshalSonic[twitter.TwitterStruct](b, LoadTwitterCompactJSON())
}
func Benchmark_Unmarshal_TwitterCompact_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[twitter.TwitterStruct](b, LoadTwitterCompactJSON())
}
func Benchmark_Unmarshal_TwitterCompact_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[twitter.TwitterStruct](b, LoadTwitterCompactJSON())
}
func Benchmark_Unmarshal_TwitterCompact_Velox(b *testing.B) {
	benchUnmarshalVelox[twitter.TwitterStruct](b, LoadTwitterCompactJSON())
}

// =============================================================================
// TwitterTyped: same data, all interface{} replaced with concrete types.
// =============================================================================

func Benchmark_Unmarshal_TwitterTyped_Sonic(b *testing.B) {
	benchUnmarshalSonic[twitter_typed.TwitterStruct](b, LoadTwitterCompactJSON())
}
func Benchmark_Unmarshal_TwitterTyped_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[twitter_typed.TwitterStruct](b, LoadTwitterCompactJSON())
}
func Benchmark_Unmarshal_TwitterTyped_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[twitter_typed.TwitterStruct](b, LoadTwitterCompactJSON())
}
func Benchmark_Unmarshal_TwitterTyped_Velox(b *testing.B) {
	benchUnmarshalVelox[twitter_typed.TwitterStruct](b, LoadTwitterCompactJSON())
}

// =============================================================================
// GitHubIssues: GitHub REST API issues (~186KB, 30 issues). Pointer-heavy
// go-github types with a custom UnmarshalJSON timestamp. The response keys
// have not the field order of the types, and some match no field, so the
// key prediction mostly misses and the full lookup path runs.
// =============================================================================

func Benchmark_Unmarshal_GitHubIssues_Sonic(b *testing.B) {
	benchUnmarshalSonic[[]*GitHubIssue](b, LoadGitHubIssuesJSON())
}
func Benchmark_Unmarshal_GitHubIssues_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[[]*GitHubIssue](b, LoadGitHubIssuesJSON())
}
func Benchmark_Unmarshal_GitHubIssues_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[[]*GitHubIssue](b, LoadGitHubIssuesJSON())
}
func Benchmark_Unmarshal_GitHubIssues_Velox(b *testing.B) {
	benchUnmarshalVelox[[]*GitHubIssue](b, LoadGitHubIssuesJSON())
}

// TestGitHubIssuesPayload checks that velox decodes the payload exactly as
// encoding/json does: the pointer fields, the times by UnmarshalJSON, the
// empty arrays, and the response keys which no field matches.
func TestGitHubIssuesPayload(t *testing.T) {
	data := LoadGitHubIssuesJSON()
	var want, got []*GitHubIssue
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if err := vjson.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("the payload decodes differently")
	}
	if len(got) != 30 || got[0].Title == nil || got[0].User == nil || got[0].User.Login == nil {
		t.Fatal("the payload has no issues")
	}
}

// =============================================================================
// SmallMapAny: small flat map[string]any: one database audit log line
// (453B, 24 keys, mixed scalars plus one string array). The small-input
// counterpart of MapAny: fixed per-call costs dominate over per-byte scan.
// =============================================================================

func Benchmark_Unmarshal_SmallMapAny_Sonic(b *testing.B) {
	benchUnmarshalSonic[map[string]any](b, SmallMapAnyBytes)
}
func Benchmark_Unmarshal_SmallMapAny_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[map[string]any](b, SmallMapAnyBytes)
}
func Benchmark_Unmarshal_SmallMapAny_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[map[string]any](b, SmallMapAnyBytes)
}
func Benchmark_Unmarshal_SmallMapAny_Velox(b *testing.B) {
	benchUnmarshalVelox[map[string]any](b, SmallMapAnyBytes)
}

// =============================================================================
// MapAny: map[string]any – exercises the decodeAnyMap / decodeAnyVal path
// (unmarshal counterpart of marshal's MapAny). Decodes KubePods JSON into
// map[string]any for realistic nested data.
// =============================================================================

func Benchmark_Unmarshal_MapAny_Sonic(b *testing.B) {
	benchUnmarshalSonic[map[string]any](b, LoadPodsCompactJSON())
}
func Benchmark_Unmarshal_MapAny_GoJSON(b *testing.B) {
	benchUnmarshalGoJSON[map[string]any](b, LoadPodsCompactJSON())
}
func Benchmark_Unmarshal_MapAny_JSONv2(b *testing.B) {
	benchUnmarshalJSONv2[map[string]any](b, LoadPodsCompactJSON())
}
func Benchmark_Unmarshal_MapAny_Velox(b *testing.B) {
	benchUnmarshalVelox[map[string]any](b, LoadPodsCompactJSON())
}
