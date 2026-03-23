package vjson

import (
	"encoding/json"
	"testing"
)

// Benchmark scenarios for MAP_STR_ITER: C-native Swiss Map key
// iteration with VM-dispatched value body vs Go-driven MAP_BEGIN.
//
// Scenarios cover:
//  1. map[string]struct — the primary use case for MAP_STR_ITER
//  2. map[string][]string — nested slice values
//  3. map[string]struct with many entries — large map iteration
//  4. Struct with mixed fields including map — realistic struct

// Scenario 1: map[string]struct{...} (small, 3 entries)

type benchContact struct {
	Phone string `json:"phone"`
	Email string `json:"email"`
}

type benchWithContactMap struct {
	ID       int                     `json:"id"`
	Name     string                  `json:"name"`
	Contacts map[string]benchContact `json:"contacts"`
}

var contactMapVal = benchWithContactMap{
	ID:   1,
	Name: "alice",
	Contacts: map[string]benchContact{
		"home":   {Phone: "+1-555-0100", Email: "alice@home.com"},
		"work":   {Phone: "+1-555-0200", Email: "alice@work.com"},
		"mobile": {Phone: "+1-555-0300", Email: "alice@mobile.com"},
	},
}

func BenchmarkMarshal_MapStrStruct_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(contactMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrStruct_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&contactMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrStruct_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&contactMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Scenario 2: map[string][]string (slice values)

type benchWithSliceMap struct {
	ID   int                 `json:"id"`
	Tags map[string][]string `json:"tags"`
}

var sliceMapVal = benchWithSliceMap{
	ID: 42,
	Tags: map[string][]string{
		"languages":  {"Go", "C", "Rust", "Python"},
		"frameworks": {"gin", "echo", "fiber"},
		"tools":      {"git", "docker", "k8s"},
	},
}

func BenchmarkMarshal_MapStrSlice_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(sliceMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrSlice_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&sliceMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrSlice_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&sliceMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Scenario 3: map[string]struct with 50 entries (large map)

type benchLargeMapItem struct {
	Value  int    `json:"value"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
}

type benchLargeMap struct {
	Data map[string]benchLargeMapItem `json:"data"`
}

var largeMapVal = func() benchLargeMap {
	m := make(map[string]benchLargeMapItem, 50)
	for i := range 50 {
		key := "key_" + smallInts[i]
		m[key] = benchLargeMapItem{
			Value:  i * 100,
			Label:  "item-" + smallInts[i],
			Active: i%2 == 0,
		}
	}
	return benchLargeMap{Data: m}
}()

func BenchmarkMarshal_MapStrStruct50_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(largeMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrStruct50_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&largeMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrStruct50_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&largeMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Scenario 4: Realistic mixed struct (like the User example)

type benchAddr struct {
	City   string `json:"city"`
	Street string `json:"street"`
	Zip    string `json:"zip"`
}

type benchProfile struct {
	ID       int64                `json:"id"`
	Name     string               `json:"name"`
	Age      int                  `json:"age"`
	Addr     benchAddr            `json:"addr"`
	Tags     []string             `json:"tags"`
	Meta     map[string]string    `json:"meta"`
	Contacts map[string]benchAddr `json:"contacts"`
	Score    float64              `json:"score"`
}

var profileVal = benchProfile{
	ID:   12345,
	Name: "alice",
	Age:  30,
	Addr: benchAddr{City: "Beijing", Street: "Chang'an Ave", Zip: "100000"},
	Tags: []string{"admin", "developer", "reviewer"},
	Meta: map[string]string{
		"dept":   "engineering",
		"level":  "senior",
		"region": "asia-pacific",
	},
	Contacts: map[string]benchAddr{
		"home":   {City: "Beijing", Street: "Home St", Zip: "100001"},
		"work":   {City: "Shanghai", Street: "Nanjing Rd", Zip: "200000"},
		"parent": {City: "Hangzhou", Street: "West Lake Ave", Zip: "310000"},
	},
	Score: 95.5,
}

func BenchmarkMarshal_MixedProfile_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(profileVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MixedProfile_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&profileVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MixedProfile_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&profileVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Scenario 5: map[string]struct with deep nesting

type benchDeepValue struct {
	Inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
		Z struct {
			A bool   `json:"a"`
			B string `json:"b"`
		} `json:"z"`
	} `json:"inner"`
}

type benchDeepMap struct {
	Items map[string]benchDeepValue `json:"items"`
}

var deepMapVal = benchDeepMap{
	Items: map[string]benchDeepValue{
		"alpha": {Inner: struct {
			X int    `json:"x"`
			Y string `json:"y"`
			Z struct {
				A bool   `json:"a"`
				B string `json:"b"`
			} `json:"z"`
		}{X: 1, Y: "first", Z: struct {
			A bool   `json:"a"`
			B string `json:"b"`
		}{A: true, B: "nested-a"}}},
		"beta": {Inner: struct {
			X int    `json:"x"`
			Y string `json:"y"`
			Z struct {
				A bool   `json:"a"`
				B string `json:"b"`
			} `json:"z"`
		}{X: 2, Y: "second", Z: struct {
			A bool   `json:"a"`
			B string `json:"b"`
		}{A: false, B: "nested-b"}}},
	},
}

func BenchmarkMarshal_MapDeepStruct_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(deepMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapDeepStruct_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&deepMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapDeepStruct_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&deepMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

// Scenario 6: map[string]*struct (pointer values)

type benchPtrMapEntry struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type benchWithPtrMap struct {
	Name  string                       `json:"name"`
	Items map[string]*benchPtrMapEntry `json:"items"`
}

var ptrMapVal = benchWithPtrMap{
	Name: "pointer-test",
	Items: map[string]*benchPtrMapEntry{
		"a":   {ID: 1, Label: "alpha"},
		"b":   {ID: 2, Label: "bravo"},
		"c":   {ID: 3, Label: "charlie"},
		"d":   {ID: 4, Label: "delta"},
		"nil": nil,
	},
}

func BenchmarkMarshal_MapStrPtrStruct_Native(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(ptrMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrPtrStruct_GoOnly(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalGoOnly(&ptrMapVal); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_MapStrPtrStruct_StdJSON(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(&ptrMapVal); err != nil {
			b.Fatal(err)
		}
	}
}
