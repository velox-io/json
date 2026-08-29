package benchmark

import (
	"testing"

	vjson "github.com/velox-io/json"
	gdreflect "golang.design/x/reflect"
)

// =============================================================================
// Deep-copy benchmarks: vcopy vs golang-design/x/reflect
//
// golang-design/x/reflect is the external implementation of the Go proposal
// go.dev/issue/51520 ("DeepCopy"). It is a generic, reflect-driven deep copy
// that handles cycles, shared pointers, and unexported fields via unsafe, a
// close semantic match to vcopy and a fair baseline.
//
// Workloads span four sizes, all loaded once via the existing fixtures:
//   - Tiny:        flat scalar struct
//   - Small/Book:  nested structs + slices of scalars/structs
//   - KubePodList: deep nesting, slices of structs, maps
//   - Twitter:     large real-world JSON-derived struct tree
// =============================================================================

// --- Tiny ---

func BenchmarkDeepCopy_Tiny_vcopy(b *testing.B) {
	src := *loadTinyValue()

	b.ReportAllocs()
	for b.Loop() {
		_, _ = vjson.DeepCopy(src)
	}
}

func BenchmarkDeepCopy_Tiny_gdesign(b *testing.B) {
	src := *loadTinyValue()

	b.ReportAllocs()
	for b.Loop() {
		_ = gdreflect.DeepCopy(src)
	}
}

// --- Small (Book) ---

func BenchmarkDeepCopy_Small_vcopy(b *testing.B) {
	src := *loadSmallValue()

	b.ReportAllocs()
	for b.Loop() {
		_, _ = vjson.DeepCopy(src)
	}
}

func BenchmarkDeepCopy_Small_gdesign(b *testing.B) {
	src := *loadSmallValue()

	b.ReportAllocs()
	for b.Loop() {
		_ = gdreflect.DeepCopy(src)
	}
}

// --- KubePodList ---

func BenchmarkDeepCopy_Pods_vcopy(b *testing.B) {
	src := *loadPodsValue()

	b.ReportAllocs()
	for b.Loop() {
		_, _ = vjson.DeepCopy(src)
	}
}

func BenchmarkDeepCopy_Pods_gdesign(b *testing.B) {
	src := *loadPodsValue()

	b.ReportAllocs()
	for b.Loop() {
		_ = gdreflect.DeepCopy(src)
	}
}

// --- Twitter ---

func BenchmarkDeepCopy_Twitter_vcopy(b *testing.B) {
	src := *loadTwitterValue()

	b.ReportAllocs()
	for b.Loop() {
		_, _ = vjson.DeepCopy(src)
	}
}

func BenchmarkDeepCopy_Twitter_gdesign(b *testing.B) {
	src := *loadTwitterValue()

	b.ReportAllocs()
	for b.Loop() {
		_ = gdreflect.DeepCopy(src)
	}
}
