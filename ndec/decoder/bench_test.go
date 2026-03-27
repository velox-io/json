package decoder

import (
	"bytes"
	"testing"

	"github.com/velox-io/json/native/gsdec"
	"github.com/velox-io/json/native/rsdec"
	"github.com/velox-io/json/ndec"
)

// Tiny: flat struct with basic types
type BenchTiny struct {
	Bool    bool    `json:"bool"`
	Int     int     `json:"int"`
	Int64   int64   `json:"int64"`
	Float64 float64 `json:"float64"`
	String  string  `json:"string"`
}

// Small: nested struct with slices
type BenchAuthor struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type BenchBook struct {
	BookID  int           `json:"id"`
	BookIDs []int         `json:"ids"`
	Title   string        `json:"title"`
	Titles  []string      `json:"titles"`
	Price   float64       `json:"price"`
	Hot     bool          `json:"hot"`
	Author  BenchAuthor   `json:"author"`
	Authors []BenchAuthor `json:"authors"`
}

// Medium: struct with map and pointer
type BenchCoord struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type BenchMedium struct {
	Name   string         `json:"name"`
	Age    int            `json:"age"`
	Tags   []string       `json:"tags"`
	Coords []BenchCoord   `json:"coords"`
	Meta   map[string]int `json:"meta"`
	Inner  *BenchCoord    `json:"inner"`
}

var (
	tinyJSON   = []byte(`{"bool":true,"int":42,"int64":9876543210,"float64":3.14159,"string":"hello world"}`)
	smallJSON  = []byte(`{"id":1,"ids":[1,2,3],"title":"Go Programming","titles":["Go","Rust"],"price":29.99,"hot":true,"author":{"name":"Alice","age":30},"authors":[{"name":"Alice","age":30},{"name":"Bob","age":25}]}`)
	mediumJSON = []byte(`{"name":"Alice","age":30,"tags":["go","rust","json"],"coords":[{"x":1,"y":2},{"x":3,"y":4}],"meta":{"a":1,"b":2,"c":3},"inner":{"x":10,"y":20}}`)
)

// Tiny (flat struct: bool, int, float64, string)

func benchTiny(b *testing.B, drv *ndec.Driver) {
	b.SetBytes(int64(len(tinyJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s BenchTiny
		dec := New(bytes.NewReader(tinyJSON), drv)
		if err := dec.Decode(&s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Tiny_Rsdec(b *testing.B) {
	if !rsdec.D.Available {
		b.Skip()
	}
	benchTiny(b, &rsdec.D)
}

func Benchmark_Tiny_Gsdec(b *testing.B) {
	if !gsdec.D.Available {
		b.Skip()
	}
	benchTiny(b, &gsdec.D)
}

// Small (nested struct + slices)

func benchSmall(b *testing.B, drv *ndec.Driver) {
	b.SetBytes(int64(len(smallJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s BenchBook
		dec := New(bytes.NewReader(smallJSON), drv)
		if err := dec.Decode(&s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Small_Rsdec(b *testing.B) {
	if !rsdec.D.Available {
		b.Skip()
	}
	benchSmall(b, &rsdec.D)
}

func Benchmark_Small_Gsdec(b *testing.B) {
	if !gsdec.D.Available {
		b.Skip()
	}
	benchSmall(b, &gsdec.D)
}

// Medium (struct + slices + map + pointer)

func benchMedium(b *testing.B, drv *ndec.Driver) {
	b.SetBytes(int64(len(mediumJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var s BenchMedium
		dec := New(bytes.NewReader(mediumJSON), drv)
		if err := dec.Decode(&s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Medium_Rsdec(b *testing.B) {
	if !rsdec.D.Available {
		b.Skip()
	}
	benchMedium(b, &rsdec.D)
}

func Benchmark_Medium_Gsdec(b *testing.B) {
	if !gsdec.D.Available {
		b.Skip()
	}
	benchMedium(b, &gsdec.D)
}
