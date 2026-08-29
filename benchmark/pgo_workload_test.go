package benchmark

import (
	"testing"

	vjson "github.com/velox-io/json"
)

// PGO-only marshal workloads for the encvm full/compact VM modes.
//
// The comparison suite (Benchmark_Marshal_*_Velox) only drives the FAST VM:
// plain vjson.Marshal sets no indent and no escape flags, which dispatches to
// vj_vm_exec_fast (venc/vm_exec.go). The other two encvm builds need their own
// counter collection:
//
//	full    <- MarshalIndent: indentString != "" dispatches to vj_vm_exec_full
//	compact <- Marshal + WithStdCompat: escape flags dispatch to vj_vm_exec_compact
//
// Naming: these entries deliberately carry NO library suffix
// (_Velox/_Sonic/_GoJSON/_JSONv2). scripts/bench.sh sweeps construct
// `Benchmark_<filter>.*_<lib>$` regexes, so these never appear in comparison
// runs; they are selected only by scripts/pgo-collect-instr.sh's per-mode
// default PGO_BENCH_FILTER:
//
//	MODES=full    -> ^Benchmark_PGOWorkload_Full$
//	MODES=compact -> ^Benchmark_PGOWorkload_Compact$
//
// Each entry sweeps all Benchmark_Marshal_*_Velox datasets per iteration, at
// their concrete types (identical call shapes to the comparison suite, so the
// VM dispatch mirrors production). Std-compat escaping is on in both workloads:
// MarshalIndent is the encoding/json drop-in (HTML escaping on by default),
// and it keeps the full VM's escape-handling blocks warm in the profile.

// pgoStep adapts one typed dataset marshal into a workload step. The value
// crosses at its concrete type so each vjson call site matches the
// corresponding Benchmark_Marshal_*_Velox entry.
func pgoStep[T any](v T, marshal func(T) ([]byte, error)) func() error {
	return func() error {
		_, err := marshal(v)
		return err
	}
}

func pgoMarshalFull[T any](v T) ([]byte, error) {
	return vjson.MarshalIndent(v, "", "  ", vjson.WithStdCompat())
}

func pgoMarshalCompact[T any](v T) ([]byte, error) {
	return vjson.Marshal(v, vjson.WithStdCompat())
}

func pgoRunWorkload(b *testing.B, steps []func() error) {
	b.ReportAllocs()
	for b.Loop() {
		for _, step := range steps {
			if err := step(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func Benchmark_PGOWorkload_Full(b *testing.B) {
	pgoRunWorkload(b, []func() error{
		pgoStep(loadTinyValue(), pgoMarshalFull),
		pgoStep(loadSmallValue(), pgoMarshalFull),
		pgoStep(loadMediumValue(), pgoMarshalFull),
		pgoStep(loadEscapeHeavyValue(), pgoMarshalFull),
		pgoStep(loadPodsValue(), pgoMarshalFull),
		pgoStep(loadTwitterValue(), pgoMarshalFull),
		pgoStep(loadTwitterTypedValue(), pgoMarshalFull),
		pgoStep(loadMapAnyValue(), pgoMarshalFull),
		pgoStep(loadJSONBenchCanadaGeometryValue(), pgoMarshalFull),
		pgoStep(loadJSONBenchCITMCatalogValue(), pgoMarshalFull),
		pgoStep(loadJSONBenchGolangSourceValue(), pgoMarshalFull),
		pgoStep(loadJSONBenchStringUnicodeValue(), pgoMarshalFull),
		pgoStep(loadJSONBenchSyntheaFHIRValue(), pgoMarshalFull),
		pgoStep(loadJSONBenchTwitterStatusValue(), pgoMarshalFull),
	})
}

func Benchmark_PGOWorkload_Compact(b *testing.B) {
	pgoRunWorkload(b, []func() error{
		pgoStep(loadTinyValue(), pgoMarshalCompact),
		pgoStep(loadSmallValue(), pgoMarshalCompact),
		pgoStep(loadMediumValue(), pgoMarshalCompact),
		pgoStep(loadEscapeHeavyValue(), pgoMarshalCompact),
		pgoStep(loadPodsValue(), pgoMarshalCompact),
		pgoStep(loadTwitterValue(), pgoMarshalCompact),
		pgoStep(loadTwitterTypedValue(), pgoMarshalCompact),
		pgoStep(loadMapAnyValue(), pgoMarshalCompact),
		pgoStep(loadJSONBenchCanadaGeometryValue(), pgoMarshalCompact),
		pgoStep(loadJSONBenchCITMCatalogValue(), pgoMarshalCompact),
		pgoStep(loadJSONBenchGolangSourceValue(), pgoMarshalCompact),
		pgoStep(loadJSONBenchStringUnicodeValue(), pgoMarshalCompact),
		pgoStep(loadJSONBenchSyntheaFHIRValue(), pgoMarshalCompact),
		pgoStep(loadJSONBenchTwitterStatusValue(), pgoMarshalCompact),
	})
}
