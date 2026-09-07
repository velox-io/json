package benchmark

import (
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
)

// Valid across the standard corpora: encoding/json and the native ndec
// walker behind vjson.Valid.

func Benchmark_Valid_KubePods_Std(b *testing.B) {
	b.SetBytes(int64(len(KubePodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !json.Valid(KubePodsJSON) {
			b.Fatal("invalid")
		}
	}
}

func Benchmark_Valid_KubePods_Velox(b *testing.B) {
	b.SetBytes(int64(len(KubePodsJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !vjson.Valid(KubePodsJSON) {
			b.Fatal("invalid")
		}
	}
}

func Benchmark_Valid_Twitter_Std(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !json.Valid(TwitterJSON) {
			b.Fatal("invalid")
		}
	}
}

func Benchmark_Valid_Twitter_Velox(b *testing.B) {
	b.SetBytes(int64(len(TwitterJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !vjson.Valid(TwitterJSON) {
			b.Fatal("invalid")
		}
	}
}

func Benchmark_Valid_EscapeHeavy_Std(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !json.Valid(EscapeHeavyJSON) {
			b.Fatal("invalid")
		}
	}
}

func Benchmark_Valid_EscapeHeavy_Velox(b *testing.B) {
	b.SetBytes(int64(len(EscapeHeavyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !vjson.Valid(EscapeHeavyJSON) {
			b.Fatal("invalid")
		}
	}
}

func Benchmark_Valid_Medium_Std(b *testing.B) {
	b.SetBytes(int64(len(MediumJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !json.Valid(MediumJSON) {
			b.Fatal("invalid")
		}
	}
}

func Benchmark_Valid_Medium_Velox(b *testing.B) {
	b.SetBytes(int64(len(MediumJSON)))
	b.ReportAllocs()
	for b.Loop() {
		if !vjson.Valid(MediumJSON) {
			b.Fatal("invalid")
		}
	}
}
