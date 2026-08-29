package benchmark

import (
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
)

func valueMarshalSize(b *testing.B, v any) int64 {
	data, err := vjson.Marshal(v)
	if err != nil {
		b.Fatal(err)
	}
	return int64(len(data))
}

// Root value.Value marshal: pure-Go tape walk (venc/tape_walk.go).
func Benchmark_Marshal_KubePods_VeloxValue(b *testing.B) {
	var val vjson.Value
	if err := vjson.Unmarshal(LoadPodsCompactJSON(), &val); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(valueMarshalSize(b, val))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := vjson.Marshal(val); err != nil {
			b.Fatal(err)
		}
	}
}

// Same output, host struct with an embedded Value: the field compiles to
// opValueSpread and the tape walk runs in the native VM (tapewalk.h).
type kubePodsValueHost struct {
	Doc value.Value `json:",embed"`
}

func Benchmark_Marshal_KubePods_VeloxValueEmbed(b *testing.B) {
	var host kubePodsValueHost
	if err := vjson.Unmarshal(LoadPodsCompactJSON(), &host.Doc); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(valueMarshalSize(b, host))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := vjson.Marshal(host); err != nil {
			b.Fatal(err)
		}
	}
}
