package main

import (
	stdjson "encoding/json"
	"testing"

	json "github.com/velox-io/json"
)

func Benchmark_Marshal_Velox(b *testing.B) {
	testUser := NewTestUser()
	// warm up: ensure Blueprint + iface cache are compiled
	json.Marshal(testUser)

	b.ReportAllocs()

	for b.Loop() {
		_, err := json.Marshal(testUser)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_std(b *testing.B) {
	testUser := NewTestUser()
	b.ReportAllocs()

	for b.Loop() {
		_, err := stdjson.Marshal(&testUser)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_MarshalIndent_Velox(b *testing.B) {
	testUser := NewTestUser()
	json.MarshalIndent(testUser, "", "  ")

	b.ReportAllocs()

	for b.Loop() {
		_, err := json.MarshalIndent(testUser, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_MarshalIndent_std(b *testing.B) {
	testUser := NewTestUser()
	b.ReportAllocs()

	for b.Loop() {
		_, err := stdjson.MarshalIndent(&testUser, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
	}
}
