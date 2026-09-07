package benchmark

import (
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
)

// Stream-vs-slice encode benchmarks: the producer path must stay within a
// small fixed overhead of encoding an already-materialized []T, because the
// OnWrite pipeline trades element storage for per-element handoff.

type benchEvent struct {
	ID    string   `json:"id"`
	N     int      `json:"n"`
	Tags  []string `json:"tags,omitempty"`
	Score float64  `json:"score"`
}

type benchDoc struct {
	Events stream.Stream[benchEvent] `json:"events"`
	Name   string                    `json:"name"`
}

type benchDocSlice struct {
	Events []benchEvent `json:"events"`
	Name   string       `json:"name"`
}

const benchStreamElems = 1000

func benchEvents() []benchEvent {
	ev := make([]benchEvent, benchStreamElems)
	for i := range ev {
		ev[i] = benchEvent{ID: "event-42", N: i, Tags: []string{"a", "b"}, Score: 3.14}
	}
	return ev
}

func benchProducer(ev []benchEvent) func(stream.Sink[benchEvent]) error {
	return func(sink stream.Sink[benchEvent]) error {
		for i := range ev {
			if err := sink.Encode(&ev[i]); err != nil {
				return err
			}
		}
		return nil
	}
}

func benchStreamDoc(ev []benchEvent) *benchDoc {
	d := &benchDoc{Name: "bench"}
	d.Events.OnWrite(benchProducer(ev))
	return d
}

func Benchmark_Marshal_StreamEvents(b *testing.B) {
	ev := benchEvents()
	d := benchStreamDoc(ev)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(d); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_SliceEvents(b *testing.B) {
	d := &benchDocSlice{Events: benchEvents(), Name: "bench"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(d); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_StreamRootInts(b *testing.B) {
	ev := make([]int, benchStreamElems)
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		for i := range ev {
			if err := sink.Encode(&ev[i]); err != nil {
				return err
			}
		}
		return nil
	})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(&s); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_SliceRootInts(b *testing.B) {
	ev := make([]int, benchStreamElems)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.Marshal(ev); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_StreamEvents_Indent(b *testing.B) {
	ev := benchEvents()
	d := benchStreamDoc(ev)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.MarshalIndent(d, "", "  "); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_SliceEvents_Indent(b *testing.B) {
	d := &benchDocSlice{Events: benchEvents(), Name: "bench"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vjson.MarshalIndent(d, "", "  "); err != nil {
			b.Fatal(err)
		}
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func Benchmark_Encoder_StreamEvents(b *testing.B) {
	ev := benchEvents()
	d := benchStreamDoc(ev)
	b.ReportAllocs()
	for b.Loop() {
		enc := vjson.NewEncoder(discardWriter{})
		if err := vjson.EncodeValue(enc, d); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Encoder_SliceEvents(b *testing.B) {
	d := &benchDocSlice{Events: benchEvents(), Name: "bench"}
	b.ReportAllocs()
	for b.Loop() {
		enc := vjson.NewEncoder(discardWriter{})
		if err := vjson.EncodeValue(enc, d); err != nil {
			b.Fatal(err)
		}
	}
}
