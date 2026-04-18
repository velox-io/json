package bench

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/ndec"
)

type Tiny struct {
	Bool    bool    `json:"bool"`
	Int     int     `json:"int"`
	Int64   int64   `json:"int64"`
	Float64 float64 `json:"float64"`
	String  string  `json:"string"`
}

var tinyJSON = []byte(
	"{" +
		"\"bool\":true," +
		"\"int\":42," +
		"\"int64\":9223372036854775807," +
		"\"float64\":3.14159265358979," +
		"\"string\":\"hello world benchmark\"" +
		"}",
)

type Book struct {
	BookID  int       `json:"id"`
	BookIDs []int     `json:"ids"`
	Title   string    `json:"title"`
	Titles  []string  `json:"titles"`
	Price   float64   `json:"price"`
	Prices  []float64 `json:"prices"`
	Hot     bool      `json:"hot"`
	Hots    []bool    `json:"hots"`
	Author  Author    `json:"author"`
	Authors []Author  `json:"authors"`
	Weights []int     `json:"weights"`
}

type Author struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
	Male bool   `json:"male"`
}

var smallJSON = []byte(
	"{" +
		"\"id\":12125925," +
		"\"ids\":[-2147483648,2147483647]," +
		"\"title\":\"未来简史-从智人到智神\"," +
		"\"titles\":[\"hello\",\"world\"]," +
		"\"price\":40.8," +
		"\"prices\":[-0.1,0.1]," +
		"\"hot\":true," +
		"\"hots\":[true,true,true]," +
		"\"author\":{\"name\":\"json\",\"age\":99,\"male\":true}," +
		"\"authors\":[{\"name\":\"alice\",\"age\":30,\"male\":false},{\"name\":\"bob\",\"age\":40,\"male\":true}]," +
		"\"weights\":[]" +
		"}",
)

func Benchmark_Unmarshal_Tiny_Ndec(b *testing.B) {
	b.SetBytes(int64(len(tinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := ndec.Unmarshal(tinyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Tiny_Velox(b *testing.B) {
	b.SetBytes(int64(len(tinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := vjson.Unmarshal(tinyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_Tiny_StdJSON(b *testing.B) {
	b.SetBytes(int64(len(tinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Tiny
		if err := json.Unmarshal(tinyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// SliceSmall keeps a mixed nested payload that exercises the common slice and
// nested-struct fast paths without being dominated by growth costs.
func Benchmark_Unmarshal_SliceSmall_Ndec(b *testing.B) {
	b.SetBytes(int64(len(smallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := ndec.Unmarshal(smallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SliceSmall_Velox(b *testing.B) {
	b.SetBytes(int64(len(smallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := vjson.Unmarshal(smallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SliceSmall_StdJSON(b *testing.B) {
	b.SetBytes(int64(len(smallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Book
		if err := json.Unmarshal(smallJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// SliceMid is sized to force repeated slice growth so the benchmark captures
// slab reuse versus fallback allocation once earlier growth steps have consumed space.
type Mid struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Tags   []string `json:"tags"`
	Coords []int64  `json:"coords"` // 16 elem * 8B = 128 B
	Scores []int64  `json:"scores"` // 16 elem * 8B = 128 B
	Flags  []bool   `json:"flags"`  // 16 elem * 1B = 16 B
	Sums   []int64  `json:"sums"`   // 16 elem * 8B = 128 B
	Author Author   `json:"author"`
	Notes  []string `json:"notes"`
}

func makeMidJSON() []byte {
	var sb strings.Builder
	sb.Grow(2048)
	sb.WriteString(`{`)
	sb.WriteString(`"id":7777777777,`)
	sb.WriteString(`"name":"middle-record-payload",`)
	sb.WriteString(`"tags":["alpha","beta","gamma","delta","epsilon","zeta","eta","theta"],`)
	// Sixteen elements are enough to force repeated grow steps on these slices.
	sb.WriteString(`"coords":[`)
	for i := 0; i < 16; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", -8000000000+int64(i)*1000)
	}
	sb.WriteString(`],"scores":[`)
	for i := 0; i < 16; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", int64(i*7)+100)
	}
	sb.WriteString(`],"flags":[`)
	for i := 0; i < 16; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		if i%3 == 0 {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	}
	sb.WriteString(`],"sums":[`)
	for i := 0; i < 16; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", int64(i)*97)
	}
	sb.WriteString(`],"author":{"name":"middle author","age":42,"male":true},`)
	sb.WriteString(`"notes":["n1","n2","n3","n4"]`)
	sb.WriteString(`}`)
	return []byte(sb.String())
}

var midJSON = makeMidJSON()

func Benchmark_Unmarshal_SliceMid_Ndec(b *testing.B) {
	b.SetBytes(int64(len(midJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Mid
		if err := ndec.Unmarshal(midJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SliceMid_Velox(b *testing.B) {
	b.SetBytes(int64(len(midJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Mid
		if err := vjson.Unmarshal(midJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SliceMid_StdJSON(b *testing.B) {
	b.SetBytes(int64(len(midJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Mid
		if err := json.Unmarshal(midJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// SliceLarge shifts the cost center to SLICE<struct> growth so the benchmark
// measures typed allocation, GROW_SLICE_STRUCT yields, and inner fallback growth together.
type LargeRecord struct {
	ID     int64    `json:"id"`
	Code   string   `json:"code"`
	Score  float64  `json:"score"`
	Active bool     `json:"active"`
	Tags   []string `json:"tags"`
	Vals   []int64  `json:"vals"`
}

type Large struct {
	Total   int64         `json:"total"`
	Title   string        `json:"title"`
	Records []LargeRecord `json:"records"`
}

func makeLargeJSON(n int) []byte {
	var sb strings.Builder
	sb.Grow(n * 200)
	sb.WriteString(`{"total":`)
	fmt.Fprintf(&sb, "%d", n)
	sb.WriteString(`,"title":"large record list payload",`)
	sb.WriteString(`"records":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb,
			`{"id":%d,"code":"rec-%d","score":%d.%d,"active":%t,"tags":["t%da","t%db","t%dc"],"vals":[%d,%d,%d,%d,%d]}`,
			int64(i)*100+1, i, i, i%100, i%2 == 0,
			i, i, i,
			i, i+1, i+2, i+3, i+4,
		)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

var largeJSON = makeLargeJSON(150)

// Guard the larger benchmark payloads with parity checks so a failed decode is
// never misread as a faster benchmark result.
func TestSliceMidLargeParity(t *testing.T) {
	t.Helper()
	{
		var nd, std Mid
		if err := ndec.Unmarshal(midJSON, &nd); err != nil {
			t.Fatalf("ndec Mid: %v", err)
		}
		if err := json.Unmarshal(midJSON, &std); err != nil {
			t.Fatalf("stdlib Mid: %v", err)
		}
		if fmt.Sprintf("%#v", nd) != fmt.Sprintf("%#v", std) {
			t.Fatalf("Mid mismatch:\nndec=%#v\nstd=%#v", nd, std)
		}
	}
	{
		var nd, std Large
		if err := ndec.Unmarshal(largeJSON, &nd); err != nil {
			t.Fatalf("ndec Large: %v", err)
		}
		if err := json.Unmarshal(largeJSON, &std); err != nil {
			t.Fatalf("stdlib Large: %v", err)
		}
		if len(nd.Records) != len(std.Records) {
			t.Fatalf("Large records len: ndec=%d std=%d", len(nd.Records), len(std.Records))
		}
		if fmt.Sprintf("%#v", nd) != fmt.Sprintf("%#v", std) {
			t.Fatalf("Large mismatch")
		}
	}
}

func Benchmark_Unmarshal_SliceLarge_Ndec(b *testing.B) {
	b.SetBytes(int64(len(largeJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Large
		if err := ndec.Unmarshal(largeJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SliceLarge_Velox(b *testing.B) {
	b.SetBytes(int64(len(largeJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Large
		if err := vjson.Unmarshal(largeJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_SliceLarge_StdJSON(b *testing.B) {
	b.SetBytes(int64(len(largeJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v Large
		if err := json.Unmarshal(largeJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// The map benchmarks compare all libraries on identical payloads instead of
// exposing ndec-only buffer-size knobs that would skew cross-library comparisons.
type MapInner struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type MapStringStringBox struct {
	M map[string]string `json:"m"`
}

type MapStringIntBox struct {
	M map[string]int `json:"m"`
}

type MapStringInnerBox struct {
	M map[string]MapInner `json:"m"`
}

func makeMapStringStringJSON(n int) []byte {
	var sb strings.Builder
	sb.Grow(n * 24)
	sb.WriteString(`{"m":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%d":"v%d"`, i, i)
	}
	sb.WriteString(`}}`)
	return []byte(sb.String())
}

func makeMapStringIntJSON(n int) []byte {
	var sb strings.Builder
	sb.Grow(n * 20)
	sb.WriteString(`{"m":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%d":%d`, i, i*17-9)
	}
	sb.WriteString(`}}`)
	return []byte(sb.String())
}

func makeMapStringInnerJSON(n int) []byte {
	var sb strings.Builder
	sb.Grow(n * 48)
	sb.WriteString(`{"m":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%d":{"name":"name_%d","age":%d}`, i, i, i*3)
	}
	sb.WriteString(`}}`)
	return []byte(sb.String())
}

var (
	mapStringStringJSON = makeMapStringStringJSON(65)
	mapStringIntJSON    = makeMapStringIntJSON(65)
	mapStringInnerJSON  = makeMapStringInnerJSON(65)
)

func benchmarkMapUnmarshal[T any](b *testing.B, data []byte, unmarshal func([]byte, *T) error) {
	var probe T
	if err := unmarshal(data, &probe); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var v T
		if err := unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkMapParity[T any](t *testing.T, data []byte) {
	t.Helper()
	var got, want T
	if err := ndec.Unmarshal(data, &got); err != nil {
		t.Fatalf("ndec.Unmarshal: %v", err)
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parity drift:\nndec=%#v\nstdlib=%#v", got, want)
	}
}

// Sample several map sizes around the flush threshold so the shared benchmark
// payloads also cover both closing-only and buffer-full first-flush behavior.
func TestMapParity(t *testing.T) {
	for _, n := range []int{1, 8, 15, 16, 17, 32, 33, 65} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			benchmarkMapParity[MapStringStringBox](t, makeMapStringStringJSON(n))
			benchmarkMapParity[MapStringIntBox](t, makeMapStringIntJSON(n))
			benchmarkMapParity[MapStringInnerBox](t, makeMapStringInnerJSON(n))
		})
	}
}

func Benchmark_Unmarshal_MapStringString_Ndec(b *testing.B) {
	benchmarkMapUnmarshal[MapStringStringBox](b, mapStringStringJSON,
		func(data []byte, v *MapStringStringBox) error { return ndec.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringString_Velox(b *testing.B) {
	benchmarkMapUnmarshal[MapStringStringBox](b, mapStringStringJSON,
		func(data []byte, v *MapStringStringBox) error { return vjson.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringString_StdJSON(b *testing.B) {
	benchmarkMapUnmarshal[MapStringStringBox](b, mapStringStringJSON,
		func(data []byte, v *MapStringStringBox) error { return json.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringInt_Ndec(b *testing.B) {
	benchmarkMapUnmarshal[MapStringIntBox](b, mapStringIntJSON,
		func(data []byte, v *MapStringIntBox) error { return ndec.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringInt_Velox(b *testing.B) {
	benchmarkMapUnmarshal[MapStringIntBox](b, mapStringIntJSON,
		func(data []byte, v *MapStringIntBox) error { return vjson.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringInt_StdJSON(b *testing.B) {
	benchmarkMapUnmarshal[MapStringIntBox](b, mapStringIntJSON,
		func(data []byte, v *MapStringIntBox) error { return json.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringInner_Ndec(b *testing.B) {
	benchmarkMapUnmarshal[MapStringInnerBox](b, mapStringInnerJSON,
		func(data []byte, v *MapStringInnerBox) error { return ndec.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringInner_Velox(b *testing.B) {
	benchmarkMapUnmarshal[MapStringInnerBox](b, mapStringInnerJSON,
		func(data []byte, v *MapStringInnerBox) error { return vjson.Unmarshal(data, v) })
}

func Benchmark_Unmarshal_MapStringInner_StdJSON(b *testing.B) {
	benchmarkMapUnmarshal[MapStringInnerBox](b, mapStringInnerJSON,
		func(data []byte, v *MapStringInnerBox) error { return json.Unmarshal(data, v) })
}
