package benchmark

import (
	"runtime"
	"testing"

	"github.com/valyala/fastjson"
	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/decode/dom"
)

// Per-implementation drivers. Every corpus section below composes the same
// set, so a missing entry is visible at a glance:
//   VeloxParse       pooled steady state (the shape production traffic takes)
//   VeloxUnpooled    a new Parser per parse: the one-shot scenario (CLI tools,
//                    startup config), expected to trail VeloxParse by the
//                    cost of allocating every buffer from scratch. B/op is
//                    the full first-parse reservation, the regression guard
//                    for the counted sizing bound and the seed.
//   VeloxZeroCopy    padded input with escape-free strings aliasing the source
//   VeloxValue       root Value through Unmarshal (bind-path tape build)
//   VeloxValueStrict VeloxValue with the strict scan armed
//   FastJson_*       fastjson controls, reusing and owning the parser

func benchDom(b *testing.B, data []byte) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := dom.Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

func benchDomUnpooled(b *testing.B, data []byte) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p := dom.NewParser()
		if _, err := p.Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

func benchDomZeroCopy(b *testing.B, data []byte) {
	padded := dom.Pad(data)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := dom.ParsePadded(padded, dom.WithZeroCopy()); err != nil {
			b.Fatal(err)
		}
	}
}

func benchDomValue(b *testing.B, data []byte) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var val vjson.Value
		if err := vjson.Unmarshal(data, &val); err != nil {
			b.Fatal(err)
		}
	}
}

// benchDomValueStrict arms the strict scan on the Value unmarshal path.
func benchDomValueStrict(b *testing.B, data []byte) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var val vjson.Value
		if err := vjson.Unmarshal(data, &val, vjson.WithStrictScan()); err != nil {
			b.Fatal(err)
		}
	}
}

func benchFastJSONReuse(b *testing.B, data []byte) {
	s := string(data)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	b.ResetTimer()
	var parser fastjson.Parser
	for b.Loop() {
		r, err := parser.Parse(s)
		if err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(r)
	}
}

func benchFastJSONOwned(b *testing.B, data []byte) {
	s := string(data)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var parser fastjson.Parser
		r, err := parser.Parse(s)
		if err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(r)
	}
}

// --- KubePodsCompact: dense small fields, the separator-heavy shape ---

func Benchmark_Dom_KubePodsCompact_VeloxParse(b *testing.B) { benchDom(b, LoadPodsCompactJSON()) }
func Benchmark_Dom_KubePodsCompact_VeloxUnpooled(b *testing.B) {
	benchDomUnpooled(b, LoadPodsCompactJSON())
}
func Benchmark_Dom_KubePodsCompact_VeloxZeroCopy(b *testing.B) {
	benchDomZeroCopy(b, LoadPodsCompactJSON())
}
func Benchmark_Dom_KubePodsCompact_VeloxValue(b *testing.B) { benchDomValue(b, LoadPodsCompactJSON()) }
func Benchmark_Dom_KubePodsCompact_VeloxValueStrict(b *testing.B) {
	benchDomValueStrict(b, LoadPodsCompactJSON())
}
func Benchmark_Dom_KubePodsCompact_FastJson_Reuse(b *testing.B) {
	benchFastJSONReuse(b, LoadPodsCompactJSON())
}
func Benchmark_Dom_KubePodsCompact_FastJson_Owned(b *testing.B) {
	benchFastJSONOwned(b, LoadPodsCompactJSON())
}

// --- Medium: small mixed document, the per-parse fixed-cost regime ---

func Benchmark_Dom_Medium_VeloxParse(b *testing.B)       { benchDom(b, MediumJSON) }
func Benchmark_Dom_Medium_VeloxUnpooled(b *testing.B)    { benchDomUnpooled(b, MediumJSON) }
func Benchmark_Dom_Medium_VeloxZeroCopy(b *testing.B)    { benchDomZeroCopy(b, MediumJSON) }
func Benchmark_Dom_Medium_VeloxValue(b *testing.B)       { benchDomValue(b, MediumJSON) }
func Benchmark_Dom_Medium_VeloxValueStrict(b *testing.B) { benchDomValueStrict(b, MediumJSON) }
func Benchmark_Dom_Medium_FastJson_Reuse(b *testing.B)   { benchFastJSONReuse(b, MediumJSON) }
func Benchmark_Dom_Medium_FastJson_Owned(b *testing.B)   { benchFastJSONOwned(b, MediumJSON) }

// --- Twitter: large string-heavy document, the arena-reservation regime ---

func Benchmark_Dom_Twitter_VeloxParse(b *testing.B)       { benchDom(b, TwitterJSON) }
func Benchmark_Dom_Twitter_VeloxUnpooled(b *testing.B)    { benchDomUnpooled(b, TwitterJSON) }
func Benchmark_Dom_Twitter_VeloxZeroCopy(b *testing.B)    { benchDomZeroCopy(b, TwitterJSON) }
func Benchmark_Dom_Twitter_VeloxValue(b *testing.B)       { benchDomValue(b, TwitterJSON) }
func Benchmark_Dom_Twitter_VeloxValueStrict(b *testing.B) { benchDomValueStrict(b, TwitterJSON) }
func Benchmark_Dom_Twitter_FastJson_Reuse(b *testing.B)   { benchFastJSONReuse(b, TwitterJSON) }
func Benchmark_Dom_Twitter_FastJson_Owned(b *testing.B)   { benchFastJSONOwned(b, TwitterJSON) }

// --- EscapeHeavyCompact: string decoding and the ZC escape boundary ---

func Benchmark_Dom_EscapeHeavyCompact_VeloxParse(b *testing.B) {
	benchDom(b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Dom_EscapeHeavyCompact_VeloxUnpooled(b *testing.B) {
	benchDomUnpooled(b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Dom_EscapeHeavyCompact_VeloxZeroCopy(b *testing.B) {
	benchDomZeroCopy(b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Dom_EscapeHeavyCompact_VeloxValue(b *testing.B) {
	benchDomValue(b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Dom_EscapeHeavyCompact_VeloxValueStrict(b *testing.B) {
	benchDomValueStrict(b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Dom_EscapeHeavyCompact_FastJson_Reuse(b *testing.B) {
	benchFastJSONReuse(b, LoadEscapeHeavyCompactJSON())
}
func Benchmark_Dom_EscapeHeavyCompact_FastJson_Owned(b *testing.B) {
	benchFastJSONOwned(b, LoadEscapeHeavyCompactJSON())
}
