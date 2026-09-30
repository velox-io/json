package vbind

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/vlib"
)

func buildLookup(tb testing.TB, keys []string) (unsafe.Pointer, uint32) {
	blob, rc := vlib.Build(keys, vlib.TiersAll)
	if rc <= 0 {
		tb.Fatalf("Build returned %d", rc)
	}
	return unsafe.Pointer(&blob[0]), uint32(rc)
}

func TestLookupFind_SingleKey(t *testing.T) {
	keys := []string{"name"}
	blob, tier := buildLookup(t, keys)
	t.Logf("tier=%d (WINDOW=%d)", tier, vlib.TierWindow)
	if idx := LookupFind(blob, "name"); idx != 0 {
		t.Errorf("LookupFind(name) = %d, want 0", idx)
	}
	if idx := LookupFind(blob, "other"); idx != -1 {
		t.Errorf("LookupFind(other) = %d, want -1", idx)
	}
	if tier == vlib.TierWindow {
		if idx := LookupFind(blob, strings.Repeat("x", 64)); idx != -1 {
			t.Errorf("LookupFind(64-byte WINDOW miss) = %d, want -1", idx)
		}
	}
}

func TestLookupFind_SmallSet(t *testing.T) {
	keys := []string{"name", "age", "email", "active", "score"}
	blob, tier := buildLookup(t, keys)
	t.Logf("tier=%d", tier)
	for i, k := range keys {
		if idx := LookupFind(blob, k); idx != i {
			t.Errorf("LookupFind(%q) = %d, want %d", k, idx, i)
		}
	}
	for _, miss := range []string{"xxx", "nam", "namee", "", "Name"} {
		if idx := LookupFind(blob, miss); idx != -1 {
			t.Errorf("LookupFind(%q) = %d, want -1", miss, idx)
		}
	}
}

func TestLookupFind_MediumSet(t *testing.T) {
	keys := []string{
		"apiVersion", "kind", "metadata", "spec", "status",
		"name", "namespace", "labels", "annotations", "creationTimestamp",
		"resourceVersion", "selfLink", "uid", "generation", "deletionGracePeriodSeconds",
		"ownerReferences", "finalizers", "clusterName", "managedFields", "conditions",
	}
	blob, tier := buildLookup(t, keys)
	t.Logf("tier=%d (n=%d)", tier, len(keys))
	for i, k := range keys {
		if idx := LookupFind(blob, k); idx != i {
			t.Errorf("LookupFind(%q) = %d, want %d", k, idx, i)
		}
	}
	if idx := LookupFind(blob, "nonexistent"); idx != -1 {
		t.Errorf("LookupFind(nonexistent) = %d, want -1", idx)
	}
}

func TestLookupFind_LargeSet(t *testing.T) {
	keys := make([]string, 100)
	for i := range keys {
		keys[i] = "field_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	blob, tier := buildLookup(t, keys)
	t.Logf("tier=%d (n=%d)", tier, len(keys))
	for i, k := range keys {
		if idx := LookupFind(blob, k); idx != i {
			t.Errorf("LookupFind(%q) = %d, want %d", k, idx, i)
		}
	}
	if idx := LookupFind(blob, "field_zzz"); idx != -1 {
		t.Errorf("LookupFind(field_zzz) = %d, want -1", idx)
	}
}

func TestLookupFind_NilBlob(t *testing.T) {
	if idx := LookupFind(nil, "anything"); idx != -1 {
		t.Errorf("LookupFind(nil, ...) = %d, want -1", idx)
	}
}

func TestLookupFind_NoneTier(t *testing.T) {
	if idx := LookupFind(unsafe.Pointer(&emptyLookupSentinel[0]), "anything"); idx != -1 {
		t.Errorf("LookupFind(NONE sentinel, ...) = %d, want -1", idx)
	}
}

func TestLookupFind_LongKeys(t *testing.T) {
	keys := []string{
		"veryLongFieldNameThatExceedsNormalExpectations_1",
		"veryLongFieldNameThatExceedsNormalExpectations_2",
		"veryLongFieldNameThatExceedsNormalExpectations_3",
	}
	blob, tier := buildLookup(t, keys)
	t.Logf("tier=%d (long keys)", tier)
	for i, k := range keys {
		if idx := LookupFind(blob, k); idx != i {
			t.Errorf("LookupFind(%q) = %d, want %d", k, idx, i)
		}
	}
}

func TestLookupFind_TableLongKeys(t *testing.T) {
	prefix := strings.Repeat("x", 64)
	keys := []string{prefix + "a", prefix + "b", prefix + "c"}
	blob, tier := buildLookup(t, keys)
	if tier != vlib.TierTable {
		t.Fatalf("tier = %d, want TABLE (%d)", tier, vlib.TierTable)
	}
	for i, k := range keys {
		if idx := LookupFind(blob, k); idx != i {
			t.Errorf("LookupFind(%q) = %d, want %d", k, idx, i)
		}
	}
	if idx := LookupFind(blob, prefix+"z"); idx != -1 {
		t.Errorf("LookupFind(TABLE miss) = %d, want -1", idx)
	}
}

func BenchmarkLookupFind_20(b *testing.B) {
	keys := []string{
		"apiVersion", "kind", "metadata", "spec", "status",
		"name", "namespace", "labels", "annotations", "creationTimestamp",
		"resourceVersion", "selfLink", "uid", "generation", "deletionGracePeriodSeconds",
		"ownerReferences", "finalizers", "clusterName", "managedFields", "conditions",
	}
	blob, _ := buildLookup(nil, keys)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = LookupFind(blob, "conditions")
	}
}

func BenchmarkLinearScan_20(b *testing.B) {
	keys := []string{
		"apiVersion", "kind", "metadata", "spec", "status",
		"name", "namespace", "labels", "annotations", "creationTimestamp",
		"resourceVersion", "selfLink", "uid", "generation", "deletionGracePeriodSeconds",
		"ownerReferences", "finalizers", "clusterName", "managedFields", "conditions",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := "conditions"
		for j, k := range keys {
			if k == target {
				_ = j
				break
			}
		}
	}
}

func TestLookupFind_TwoShortKeys(t *testing.T) {
	keys := []string{"a", "b"}
	blob, tier := buildLookup(t, keys)
	t.Logf("tier=%d (WINDOW=%d, GPERF=%d)", tier, vlib.TierWindow, vlib.TierGperf)
	for i, k := range keys {
		if idx := LookupFind(blob, k); idx != i {
			t.Errorf("LookupFind(%q) = %d, want %d", k, idx, i)
		}
	}
}
