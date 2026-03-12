package benchmark

import (
	stdjson "encoding/json"
	"fmt"
	"sync"
	"testing"

	"dev.local/benchmark/jsonbench"
	"github.com/bytedance/sonic"
	vjson "github.com/velox-io/json"
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

	jsonbenchCanadaGeometryAnyOnce sync.Once
	jsonbenchCanadaGeometryAnyVal  any

	jsonbenchSyntheaFHIRAnyOnce sync.Once
	jsonbenchSyntheaFHIRAnyVal  any

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

func loadJSONBenchCanadaGeometryAnyValue() *any {
	jsonbenchCanadaGeometryAnyOnce.Do(func() {
		data := mustLoadJSONBenchCanadaGeometryRaw()
		if err := stdjson.Unmarshal(data, &jsonbenchCanadaGeometryAnyVal); err != nil {
			panic("decode jsonbench canada_geometry as any: " + err.Error())
		}
	})
	return &jsonbenchCanadaGeometryAnyVal
}

func loadJSONBenchSyntheaFHIRAnyValue() *any {
	jsonbenchSyntheaFHIRAnyOnce.Do(func() {
		data := mustLoadJSONBenchSyntheaFHIRRaw()
		if err := stdjson.Unmarshal(data, &jsonbenchSyntheaFHIRAnyVal); err != nil {
			panic("decode jsonbench synthea_fhir as any: " + err.Error())
		}
	})
	return &jsonbenchSyntheaFHIRAnyVal
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

func Benchmark_Marshal_JSONBenchCanadaGeometry_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchCanadaGeometryValue())
}

func Benchmark_Marshal_JSONBenchCITMCatalog_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchCITMCatalogValue())
}

func Benchmark_Marshal_JSONBenchCITMCatalog_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchCITMCatalogValue())
}

func Benchmark_Marshal_JSONBenchGolangSource_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchGolangSourceValue())
}

func Benchmark_Marshal_JSONBenchGolangSource_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchGolangSourceValue())
}

func Benchmark_Marshal_JSONBenchStringUnicode_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchStringUnicodeValue())
}

func Benchmark_Marshal_JSONBenchStringUnicode_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchStringUnicodeValue())
}

func Benchmark_Marshal_JSONBenchSyntheaFHIR_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchSyntheaFHIRValue())
}

func Benchmark_Marshal_JSONBenchSyntheaFHIR_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchSyntheaFHIRValue())
}

func Benchmark_Marshal_JSONBenchTwitterStatus_Sonic(b *testing.B) {
	benchmarkJSONBenchMarshalSonic(b, loadJSONBenchTwitterStatusValue())
}

func Benchmark_Marshal_JSONBenchTwitterStatus_Velox(b *testing.B) {
	benchmarkJSONBenchMarshalVelox(b, loadJSONBenchTwitterStatusValue())
}

func Benchmark_Unmarshal_JSONBenchCanadaGeometry_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.CanadaRoot](b, mustLoadJSONBenchCanadaGeometryRaw())
}

func Benchmark_Unmarshal_JSONBenchCanadaGeometry_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.CanadaRoot](b, mustLoadJSONBenchCanadaGeometryRaw())
}

func Benchmark_Unmarshal_JSONBenchCITMCatalog_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.CITMRoot](b, mustLoadJSONBenchCITMCatalogRaw())
}

func Benchmark_Unmarshal_JSONBenchCITMCatalog_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.CITMRoot](b, mustLoadJSONBenchCITMCatalogRaw())
}

func Benchmark_Unmarshal_JSONBenchGolangSource_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.GolangRoot](b, mustLoadJSONBenchGolangSourceRaw())
}

func Benchmark_Unmarshal_JSONBenchGolangSource_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.GolangRoot](b, mustLoadJSONBenchGolangSourceRaw())
}

func Benchmark_Unmarshal_JSONBenchStringUnicode_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.StringRoot](b, mustLoadJSONBenchStringUnicodeRaw())
}

func Benchmark_Unmarshal_JSONBenchStringUnicode_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.StringRoot](b, mustLoadJSONBenchStringUnicodeRaw())
}

func Benchmark_Unmarshal_JSONBenchSyntheaFHIR_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.SyntheaRoot](b, mustLoadJSONBenchSyntheaFHIRRaw())
}

func Benchmark_Unmarshal_JSONBenchSyntheaFHIR_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.SyntheaRoot](b, mustLoadJSONBenchSyntheaFHIRRaw())
}

func Benchmark_Unmarshal_JSONBenchTwitterStatus_Sonic(b *testing.B) {
	benchmarkJSONBenchUnmarshalSonic[jsonbench.TwitterRoot](b, mustLoadJSONBenchTwitterStatusRaw())
}

func Benchmark_Unmarshal_JSONBenchTwitterStatus_Velox(b *testing.B) {
	benchmarkJSONBenchUnmarshalVelox[jsonbench.TwitterRoot](b, mustLoadJSONBenchTwitterStatusRaw())
}
