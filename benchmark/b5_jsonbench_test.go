package benchmark

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"dev.local/benchmark/jsonbench"
	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/decode/bind"
)

func benchmarkJSONBenchMarshal[T any](b *testing.B, v *T, marshal func(*T) ([]byte, error)) {
	out, err := marshal(v)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkJSONBenchUnmarshal[T any](b *testing.B, data []byte, unmarshal func([]byte, *T) error) {
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

func benchmarkJSONBenchMarshalSonic[T any](b *testing.B, v *T) {
	benchmarkJSONBenchMarshal(b, v, func(v *T) ([]byte, error) {
		return sonic.Marshal(v)
	})
}

func benchmarkJSONBenchMarshalVelox[T any](b *testing.B, v *T) {
	probe, err := safeVeloxMarshal(v)
	if err != nil {
		b.Fatalf("velox marshal probe failed: %v", err)
	}

	b.SetBytes(int64(len(probe)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := vjson.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

func safeVeloxMarshal[T any](v *T) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	out, err = vjson.Marshal(v)
	return out, err
}

func benchmarkJSONBenchUnmarshalSonic[T any](b *testing.B, data []byte) {
	benchmarkJSONBenchUnmarshal(b, data, func(data []byte, dst *T) error {
		return sonic.Unmarshal(data, dst)
	})
}

func benchmarkJSONBenchMarshalGoJSON[T any](b *testing.B, v *T) {
	benchmarkJSONBenchMarshal(b, v, func(v *T) ([]byte, error) {
		return gojson.Marshal(v)
	})
}

func benchmarkJSONBenchMarshalJSONv2[T any](b *testing.B, v *T) {
	benchmarkJSONBenchMarshal(b, v, func(v *T) ([]byte, error) {
		return jsonv2.Marshal(v)
	})
}

func benchmarkJSONBenchUnmarshalJSONv2[T any](b *testing.B, data []byte) {
	benchmarkJSONBenchUnmarshal(b, data, func(data []byte, dst *T) error {
		return jsonv2.Unmarshal(data, dst)
	})
}

func benchmarkJSONBenchUnmarshalGoJSON[T any](b *testing.B, data []byte) {
	benchmarkJSONBenchUnmarshal(b, data, func(data []byte, dst *T) error {
		return gojson.Unmarshal(data, dst)
	})
}

func benchmarkJSONBenchUnmarshalVelox[T any](b *testing.B, data []byte) {
	benchmarkJSONBenchUnmarshal(b, data, func(data []byte, dst *T) error {
		return vjson.Unmarshal(data, dst)
	})
}

var (
	jsonbenchCanadaGeometryOnce sync.Once
	jsonbenchCanadaGeometryVal  *jsonbench.CanadaRoot

	jsonbenchCITMCatalogOnce sync.Once
	jsonbenchCITMCatalogVal  *jsonbench.CITMRoot

	jsonbenchGolangSourceOnce sync.Once
	jsonbenchGolangSourceVal  *jsonbench.GolangRoot

	jsonbenchStringUnicodeOnce sync.Once
	jsonbenchStringUnicodeVal  *jsonbench.StringRoot

	jsonbenchSyntheaFHIROnce sync.Once
	jsonbenchSyntheaFHIRVal  *jsonbench.SyntheaRoot

	jsonbenchTwitterStatusOnce sync.Once
	jsonbenchTwitterStatusVal  *jsonbench.TwitterRoot
)

func loadJSONBenchCanadaGeometryValue() *jsonbench.CanadaRoot {
	jsonbenchCanadaGeometryOnce.Do(func() {
		v, err := jsonbench.LoadCanadaGeometry()
		if err != nil {
			panic("load jsonbench canada_geometry: " + err.Error())
		}
		jsonbenchCanadaGeometryVal = v
	})
	return jsonbenchCanadaGeometryVal
}

func loadJSONBenchCITMCatalogValue() *jsonbench.CITMRoot {
	jsonbenchCITMCatalogOnce.Do(func() {
		v, err := jsonbench.LoadCITMCatalog()
		if err != nil {
			panic("load jsonbench citm_catalog: " + err.Error())
		}
		jsonbenchCITMCatalogVal = v
	})
	return jsonbenchCITMCatalogVal
}

func loadJSONBenchGolangSourceValue() *jsonbench.GolangRoot {
	jsonbenchGolangSourceOnce.Do(func() {
		v, err := jsonbench.LoadGolangSource()
		if err != nil {
			panic("load jsonbench golang_source: " + err.Error())
		}
		jsonbenchGolangSourceVal = v
	})
	return jsonbenchGolangSourceVal
}

func loadJSONBenchStringUnicodeValue() *jsonbench.StringRoot {
	jsonbenchStringUnicodeOnce.Do(func() {
		v, err := jsonbench.LoadStringUnicode()
		if err != nil {
			panic("load jsonbench string_unicode: " + err.Error())
		}
		jsonbenchStringUnicodeVal = v
	})
	return jsonbenchStringUnicodeVal
}

func loadJSONBenchSyntheaFHIRValue() *jsonbench.SyntheaRoot {
	jsonbenchSyntheaFHIROnce.Do(func() {
		v, err := jsonbench.LoadSyntheaFHIR()
		if err != nil {
			panic("load jsonbench synthea_fhir: " + err.Error())
		}
		jsonbenchSyntheaFHIRVal = v
	})
	return jsonbenchSyntheaFHIRVal
}

func loadJSONBenchTwitterStatusValue() *jsonbench.TwitterRoot {
	jsonbenchTwitterStatusOnce.Do(func() {
		v, err := jsonbench.LoadTwitterStatus()
		if err != nil {
			panic("load jsonbench twitter_status: " + err.Error())
		}
		jsonbenchTwitterStatusVal = v
	})
	return jsonbenchTwitterStatusVal
}

func mustLoadJSONBenchCanadaGeometryRaw() []byte {
	data, err := jsonbench.LoadCanadaGeometryJSON()
	if err != nil {
		panic("load jsonbench canada_geometry raw: " + err.Error())
	}
	return data
}

func mustLoadJSONBenchCITMCatalogRaw() []byte {
	data, err := jsonbench.LoadCITMCatalogJSON()
	if err != nil {
		panic("load jsonbench citm_catalog raw: " + err.Error())
	}
	return data
}

func mustLoadJSONBenchGolangSourceRaw() []byte {
	data, err := jsonbench.LoadGolangSourceJSON()
	if err != nil {
		panic("load jsonbench golang_source raw: " + err.Error())
	}
	return data
}

func mustLoadJSONBenchStringUnicodeRaw() []byte {
	data, err := jsonbench.LoadStringUnicodeJSON()
	if err != nil {
		panic("load jsonbench string_unicode raw: " + err.Error())
	}
	return data
}

func mustLoadJSONBenchSyntheaFHIRRaw() []byte {
	data, err := jsonbench.LoadSyntheaFHIRJSON()
	if err != nil {
		panic("load jsonbench synthea_fhir raw: " + err.Error())
	}
	return data
}

func mustLoadJSONBenchTwitterStatusRaw() []byte {
	data, err := jsonbench.LoadTwitterStatusJSON()
	if err != nil {
		panic("load jsonbench twitter_status raw: " + err.Error())
	}
	return data
}

func Benchmark_Marshal_JSONBenchCanadaGeometry_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchCanadaGeometryValue())
}
func Benchmark_Marshal_JSONBenchCanadaGeometry_JSONv2(b *testing.B) {
	benchmarkJSONBenchMarshalJSONv2(b, loadJSONBenchCanadaGeometryValue())
}

func Benchmark_Marshal_JSONBenchCanadaGeometry_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchCanadaGeometryValue())
}
func Benchmark_Marshal_JSONBenchCanadaGeometry_GoJSON(b *testing.B) {
	benchmarkJSONBenchMarshalGoJSON(b, loadJSONBenchCanadaGeometryValue())
}

func Benchmark_Marshal_JSONBenchCITMCatalog_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchCITMCatalogValue())
}
func Benchmark_Marshal_JSONBenchCITMCatalog_JSONv2(b *testing.B) {
	benchmarkJSONBenchMarshalJSONv2(b, loadJSONBenchCITMCatalogValue())
}

func Benchmark_Marshal_JSONBenchCITMCatalog_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchCITMCatalogValue())
}
func Benchmark_Marshal_JSONBenchCITMCatalog_GoJSON(b *testing.B) {
	benchmarkJSONBenchMarshalGoJSON(b, loadJSONBenchCITMCatalogValue())
}

func Benchmark_Marshal_JSONBenchGolangSource_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchGolangSourceValue())
}
func Benchmark_Marshal_JSONBenchGolangSource_JSONv2(b *testing.B) {
	benchmarkJSONBenchMarshalJSONv2(b, loadJSONBenchGolangSourceValue())
}

func Benchmark_Marshal_JSONBenchGolangSource_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchGolangSourceValue())
}
func Benchmark_Marshal_JSONBenchGolangSource_GoJSON(b *testing.B) {
	benchmarkJSONBenchMarshalGoJSON(b, loadJSONBenchGolangSourceValue())
}

func Benchmark_Marshal_JSONBenchStringUnicode_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchStringUnicodeValue())
}
func Benchmark_Marshal_JSONBenchStringUnicode_JSONv2(b *testing.B) {
	benchmarkJSONBenchMarshalJSONv2(b, loadJSONBenchStringUnicodeValue())
}

func Benchmark_Marshal_JSONBenchStringUnicode_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchStringUnicodeValue())
}
func Benchmark_Marshal_JSONBenchStringUnicode_GoJSON(b *testing.B) {
	benchmarkJSONBenchMarshalGoJSON(b, loadJSONBenchStringUnicodeValue())
}

func Benchmark_Marshal_JSONBenchSyntheaFHIR_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchSyntheaFHIRValue())
}
func Benchmark_Marshal_JSONBenchSyntheaFHIR_JSONv2(b *testing.B) {
	benchmarkJSONBenchMarshalJSONv2(b, loadJSONBenchSyntheaFHIRValue())
}

func Benchmark_Marshal_JSONBenchSyntheaFHIR_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchSyntheaFHIRValue())
}
func Benchmark_Marshal_JSONBenchSyntheaFHIR_GoJSON(b *testing.B) {
	benchmarkJSONBenchMarshalGoJSON(b, loadJSONBenchSyntheaFHIRValue())
}

func Benchmark_Marshal_JSONBenchTwitterStatus_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchTwitterStatusValue())
}
func Benchmark_Marshal_JSONBenchTwitterStatus_JSONv2(b *testing.B) {
	benchmarkJSONBenchMarshalJSONv2(b, loadJSONBenchTwitterStatusValue())
}

func Benchmark_Marshal_JSONBenchTwitterStatus_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchTwitterStatusValue())
}
func Benchmark_Marshal_JSONBenchTwitterStatus_GoJSON(b *testing.B) {
	benchmarkJSONBenchMarshalGoJSON(b, loadJSONBenchTwitterStatusValue())
}

func Benchmark_Unmarshal_JSONBenchCanadaGeometry_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.CanadaRoot](b, mustLoadJSONBenchCanadaGeometryRaw())
}
func Benchmark_Unmarshal_JSONBenchCanadaGeometry_JSONv2(b *testing.B) {
	benchmarkJSONBenchUnmarshalJSONv2[jsonbench.CanadaRoot](b, mustLoadJSONBenchCanadaGeometryRaw())
}

func Benchmark_Unmarshal_JSONBenchCanadaGeometry_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.CanadaRoot](b, mustLoadJSONBenchCanadaGeometryRaw())
}
func Benchmark_Unmarshal_JSONBenchCanadaGeometry_GoJSON(b *testing.B) {
	benchmarkJSONBenchUnmarshalGoJSON[jsonbench.CanadaRoot](b, mustLoadJSONBenchCanadaGeometryRaw())
}

func Benchmark_Unmarshal_JSONBenchCITMCatalog_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.CITMRoot](b, mustLoadJSONBenchCITMCatalogRaw())
}
func Benchmark_Unmarshal_JSONBenchCITMCatalog_JSONv2(b *testing.B) {
	benchmarkJSONBenchUnmarshalJSONv2[jsonbench.CITMRoot](b, mustLoadJSONBenchCITMCatalogRaw())
}

func Benchmark_Unmarshal_JSONBenchCITMCatalog_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.CITMRoot](b, mustLoadJSONBenchCITMCatalogRaw())
}
func Benchmark_Unmarshal_JSONBenchCITMCatalog_GoJSON(b *testing.B) {
	benchmarkJSONBenchUnmarshalGoJSON[jsonbench.CITMRoot](b, mustLoadJSONBenchCITMCatalogRaw())
}

func Benchmark_Unmarshal_JSONBenchGolangSource_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.GolangRoot](b, mustLoadJSONBenchGolangSourceRaw())
}
func Benchmark_Unmarshal_JSONBenchGolangSource_JSONv2(b *testing.B) {
	benchmarkJSONBenchUnmarshalJSONv2[jsonbench.GolangRoot](b, mustLoadJSONBenchGolangSourceRaw())
}

func Benchmark_Unmarshal_JSONBenchGolangSource_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.GolangRoot](b, mustLoadJSONBenchGolangSourceRaw())
}
func Benchmark_Unmarshal_JSONBenchGolangSource_GoJSON(b *testing.B) {
	benchmarkJSONBenchUnmarshalGoJSON[jsonbench.GolangRoot](b, mustLoadJSONBenchGolangSourceRaw())
}

func Benchmark_Unmarshal_JSONBenchStringUnicode_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.StringRoot](b, mustLoadJSONBenchStringUnicodeRaw())
}
func Benchmark_Unmarshal_JSONBenchStringUnicode_JSONv2(b *testing.B) {
	benchmarkJSONBenchUnmarshalJSONv2[jsonbench.StringRoot](b, mustLoadJSONBenchStringUnicodeRaw())
}

func Benchmark_Unmarshal_JSONBenchStringUnicode_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.StringRoot](b, mustLoadJSONBenchStringUnicodeRaw())
}
func Benchmark_Unmarshal_JSONBenchStringUnicode_GoJSON(b *testing.B) {
	benchmarkJSONBenchUnmarshalGoJSON[jsonbench.StringRoot](b, mustLoadJSONBenchStringUnicodeRaw())
}

func Benchmark_Unmarshal_JSONBenchSyntheaFHIR_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.SyntheaRoot](b, mustLoadJSONBenchSyntheaFHIRRaw())
}
func Benchmark_Unmarshal_JSONBenchSyntheaFHIR_JSONv2(b *testing.B) {
	benchmarkJSONBenchUnmarshalJSONv2[jsonbench.SyntheaRoot](b, mustLoadJSONBenchSyntheaFHIRRaw())
}

func Benchmark_Unmarshal_JSONBenchSyntheaFHIR_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.SyntheaRoot](b, mustLoadJSONBenchSyntheaFHIRRaw())
}
func Benchmark_Unmarshal_JSONBenchSyntheaFHIR_GoJSON(b *testing.B) {
	benchmarkJSONBenchUnmarshalGoJSON[jsonbench.SyntheaRoot](b, mustLoadJSONBenchSyntheaFHIRRaw())
}

func Benchmark_Unmarshal_JSONBenchTwitterStatus_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.TwitterRoot](b, mustLoadJSONBenchTwitterStatusRaw())
}
func Benchmark_Unmarshal_JSONBenchTwitterStatus_JSONv2(b *testing.B) {
	benchmarkJSONBenchUnmarshalJSONv2[jsonbench.TwitterRoot](b, mustLoadJSONBenchTwitterStatusRaw())
}

func Benchmark_Unmarshal_JSONBenchTwitterStatus_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.TwitterRoot](b, mustLoadJSONBenchTwitterStatusRaw())
}
func Benchmark_Unmarshal_JSONBenchTwitterStatus_GoJSON(b *testing.B) {
	benchmarkJSONBenchUnmarshalGoJSON[jsonbench.TwitterRoot](b, mustLoadJSONBenchTwitterStatusRaw())
}

// TestGolangSource_NoMemoryGrowth verifies that unmarshaling the recursive
// GolangNode tree repeatedly does not grow the heap without bound.
//
// Before the SlabPool fix, recursive []GolangNode backings were sliced out of
// a shared SlotClass bump block. A single live slice header rooted the whole
// block, so GC could not reclaim sibling backings and heap grew every parse.
// With SlabPool each backing is its own GC object, so overwriting the result
// frees it on the next GC.
//
// The test drives a single *bind.Parser directly. Package Unmarshal pools
// Parsers via sync.Pool, whose victim cache lets a Parser (and its arena
// backings) survive one GC before being collected. That nondeterminism crosses
// the baseline/final measurement: the parser may be live at one reading and
// evicted at the other, producing a spurious 5x ratio. Holding one Parser for
// the whole test keeps slab pool and arenas at a steady state across both
// measurements, so the ratio reflects only unbounded growth from backings.
func TestGolangSource_NoMemoryGrowth(t *testing.T) {
	data := mustLoadJSONBenchGolangSourceRaw()
	parser, err := bind.NewParser[jsonbench.GolangRoot]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	// Warm up slab free lists and EWMA block predictor to steady state.
	for i := range 20 {
		var v jsonbench.GolangRoot
		if err := parser.Unmarshal(data, &v); err != nil {
			t.Fatalf("warmup %d: %v", i, err)
		}
	}

	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	const n = 50
	for i := range n {
		var v jsonbench.GolangRoot
		if err := parser.Unmarshal(data, &v); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}

	runtime.GC()
	var final runtime.MemStats
	runtime.ReadMemStats(&final)

	// Tolerate 2x for slab pool filling and GC jitter. Before the fix the
	// heap grew without bound across parses.
	if final.HeapAlloc > baseline.HeapAlloc*2 {
		t.Errorf("heap grew from %d to %d bytes (%.1fx) over %d parses; expected steady state",
			baseline.HeapAlloc, final.HeapAlloc,
			float64(final.HeapAlloc)/float64(max(baseline.HeapAlloc, 1)), n)
	}
}
