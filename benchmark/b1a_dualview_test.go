package benchmark

import (
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Dual-view (inline variant + reserve-unknown) bind benchmarks. These drive
// the merged-tape path through Unmarshal, so they sit in their own file,
// separate from the dom parse suite.

type dvHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type dvCase struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func init() {
	vbind.DefineVariantCases[dvHost, struct {
		_ dvCase `case:"c1"`
	}]()
}

var (
	dvSmall  = []byte(`{"kind":"c1","name":"bob","age":7,"u1":1,"u2":2}`)
	dvNested = []byte(`{"kind":"c1","name":"bob","age":7,"u1":{"a":[1,2,3],"b":{"c":{"d":[4,5,6]}}},"u2":{"x":[7,8,9]}}`)
	dvManyU  = []byte(`{"kind":"c1","name":"bob","age":7,"u1":1,"u2":2,"u3":3,"u4":4,"u5":5,"u6":6,"u7":7,"u8":8}`)
	// No sink content: the header is paid but nothing is dropped from B.
	dvNoUnknown = []byte(`{"kind":"c1","name":"bob","age":7}`)
	// A plain struct with no variant at all, to show the untouched path.
	dvPlainJSON = []byte(`{"kind":"c1","name":"bob","age":7}`)
)

type dvPlain struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func benchDV(b *testing.B, src []byte) {
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		var v dvHost
		if err := vjson.Unmarshal(src, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_DualView_Small(b *testing.B)       { benchDV(b, dvSmall) }
func Benchmark_DualView_Nested(b *testing.B)      { benchDV(b, dvNested) }
func Benchmark_DualView_ManyUnknown(b *testing.B) { benchDV(b, dvManyU) }
func Benchmark_DualView_NoUnknown(b *testing.B)   { benchDV(b, dvNoUnknown) }

func Benchmark_DualView_PlainControl(b *testing.B) {
	b.SetBytes(int64(len(dvPlainJSON)))
	b.ReportAllocs()
	for b.Loop() {
		var v dvPlain
		if err := vjson.Unmarshal(dvPlainJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}
