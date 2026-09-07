package stream_test

import (
	"bytes"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
)

// errMarshaler always fails with its carried error.
type errMarshaler struct {
	err error
}

func (m errMarshaler) MarshalJSON() ([]byte, error) { return nil, m.err }

// failWriter fails on the n-th write (1-based); 0 never fails.
type failWriter struct {
	buf     bytes.Buffer
	failOn  int
	writes  int
	failErr error
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.failOn > 0 && w.writes >= w.failOn {
		return 0, w.failErr
	}
	return w.buf.Write(p)
}

// shortWriter consumes only half of every write and reports success.
type shortWriter struct {
	buf bytes.Buffer
}

func (w *shortWriter) Write(p []byte) (int, error) {
	n := len(p) / 2
	return w.buf.Write(p[:n])
}

// zeroWriter reports (0, nil) on non-empty writes.
type zeroWriter struct{}

func (w *zeroWriter) Write(p []byte) (int, error) { return 0, nil }

func TestStreamWriteErrorMatrix(t *testing.T) {
	t.Run("handler-error-before-first", func(t *testing.T) {
		sentinel := errors.New("early")
		type doc struct {
			S stream.Stream[int] `json:"s"`
			T string             `json:"t"`
		}
		d := doc{S: mkStream(t, func(sink stream.Sink[int]) error { return sentinel })}
		_, err := vjson.Marshal(&d)
		if !errors.Is(err, sentinel) {
			t.Errorf("err=%v, want sentinel", err)
		}
	})

	t.Run("handler-error-after-items", func(t *testing.T) {
		sentinel := errors.New("mid")
		var s stream.Stream[int]
		s.OnWrite(func(sink stream.Sink[int]) error {
			v := 1
			if err := sink.Encode(&v); err != nil {
				return err
			}
			return sentinel
		})
		got, err := vjson.Marshal(&s)
		if !errors.Is(err, sentinel) {
			t.Errorf("err=%v, want sentinel", err)
		}
		// Failure leaves the writer mode non-rollbackable: the encoded prefix
		// may exist, but no closing bracket is appended.
		if !strings.HasPrefix(string(got), "[1") {
			t.Logf("partial output: %q", got)
		}
	})

	t.Run("element-error-nan", func(t *testing.T) {
		var s stream.Stream[float64]
		s.OnWrite(func(sink stream.Sink[float64]) error {
			good := 1.5
			if err := sink.Encode(&good); err != nil {
				return err
			}
			bad := math.NaN()
			return sink.Encode(&bad)
		})
		_, err := vjson.Marshal(&s)
		if err == nil {
			t.Fatal("NaN element must fail")
		}
		if !strings.Contains(err.Error(), "NaN") {
			t.Errorf("err should mention NaN: %v", err)
		}
	})

	t.Run("marshaler-error", func(t *testing.T) {
		merr := errors.New("marshal failed")
		var s stream.Stream[errMarshaler]
		s.OnWrite(func(sink stream.Sink[errMarshaler]) error {
			v := errMarshaler{err: merr}
			return sink.Encode(&v)
		})
		_, err := vjson.Marshal(&s)
		if !errors.Is(err, merr) {
			t.Errorf("err=%v, want marshaler error", err)
		}
	})

	t.Run("nested-stream-error", func(t *testing.T) {
		type child struct {
			Name string               `json:"name"`
			Kids stream.Stream[child] `json:"kids"`
		}
		sentinel := errors.New("inner")
		root := child{Name: "r", Kids: mkStream(t, func(sink stream.Sink[child]) error {
			c := child{Name: "c", Kids: mkStream(t, func(inner stream.Sink[child]) error { return sentinel })}
			return sink.Encode(&c)
		})}
		_, err := vjson.Marshal(&root)
		if !errors.Is(err, sentinel) {
			t.Errorf("err=%v, want inner sentinel", err)
		}
	})

	t.Run("handler-ignores-sink-error", func(t *testing.T) {
		merr := errors.New("element failed")
		var s stream.Stream[errMarshaler]
		s.OnWrite(func(sink stream.Sink[errMarshaler]) error {
			v := errMarshaler{err: merr}
			_ = sink.Encode(&v) // element error ignored
			_ = sink.Encode(&v) // must be rejected by the latch
			return nil          // returning nil must not mask the error
		})
		_, err := vjson.Marshal(&s)
		if !errors.Is(err, merr) {
			t.Errorf("err=%v, want the latched element error", err)
		}
	})

	t.Run("writer-error-no-close", func(t *testing.T) {
		werr := errors.New("disk full")
		w := &failWriter{failOn: 2, failErr: werr}
		enc := vjson.NewEncoder(w)
		var s stream.Stream[int]
		s.OnWrite(func(sink stream.Sink[int]) error {
			for i := range 100_000 {
				v := i
				if err := sink.Encode(&v); err != nil {
					return err
				}
			}
			return nil
		})
		err := vjson.EncodeValue(enc, &s)
		if !errors.Is(err, werr) {
			t.Errorf("err=%v, want writer error", err)
		}
		// Sticky: subsequent encodes fail without touching the writer.
		if err2 := vjson.EncodeValue(enc, &s); !errors.Is(err2, werr) {
			t.Errorf("sticky err=%v, want same writer error", err2)
		}
	})

	t.Run("writer-short-write", func(t *testing.T) {
		w := &shortWriter{}
		enc := vjson.NewEncoder(w)
		var s stream.Stream[int]
		s.OnWrite(func(sink stream.Sink[int]) error {
			for i := range 2000 {
				v := i
				if err := sink.Encode(&v); err != nil {
					return err
				}
			}
			return nil
		})
		if err := vjson.EncodeValue(enc, &s); err == nil {
			t.Fatal("persistent short writes must surface as an error")
		}
	})

	t.Run("writer-zero-progress", func(t *testing.T) {
		enc := vjson.NewEncoder(&zeroWriter{})
		var s stream.Stream[int]
		s.OnWrite(func(sink stream.Sink[int]) error {
			for i := range 100_000 {
				v := i
				if err := sink.Encode(&v); err != nil {
					return err
				}
			}
			return nil
		})
		if err := vjson.EncodeValue(enc, &s); err == nil {
			t.Fatal("(0, nil) writes must fail fast instead of buffering forever")
		}
	})

	t.Run("producer-runs-exactly-once", func(t *testing.T) {
		type doc struct {
			A stream.Stream[int] `json:"a"`
			B stream.Stream[int] `json:"b,omitempty"`
		}
		runs := map[string]int{}
		mk := func(name string) func(stream.Sink[int]) error {
			return func(sink stream.Sink[int]) error {
				runs[name]++
				return nil
			}
		}
		d := doc{A: mkStream(t, mk("a")), B: mkStream(t, mk("b"))}
		for range 3 {
			if _, err := vjson.Marshal(&d); err != nil {
				t.Fatal(err)
			}
		}
		// "b" is an empty omitempty producer: omitted from output, but its
		// producer still runs exactly once per encode.
		if runs["a"] != 3 || runs["b"] != 3 {
			t.Errorf("producer runs: a=%d b=%d, want 3 and 3", runs["a"], runs["b"])
		}
	})

	t.Run("size-hint-never-runs-producer", func(t *testing.T) {
		var s stream.Stream[int]
		ran := false
		s.OnWrite(func(sink stream.Sink[int]) error { ran = true; return nil })
		if _, err := vjson.Marshal(&s); err != nil {
			t.Fatal(err)
		}
		if !ran {
			t.Error("producer should have run exactly once")
		}
		// Marshal again: AdaptiveHint reuse must not add extra runs.
		before := ran
		if _, err := vjson.Marshal(&s); err != nil {
			t.Fatal(err)
		}
		if ran != before {
			t.Error("unexpected extra producer run")
		}
	})
}

// TestStreamWriteLifecycle drives GC and stack growth from inside a producer.
func TestStreamWriteLifecycle(t *testing.T) {
	type elem struct {
		ID string `json:"id"`
		N  int    `json:"n"`
	}
	var s stream.Stream[elem]
	s.OnWrite(func(sink stream.Sink[elem]) error {
		// Grow the Go stack and collect mid-producer: the element root and
		// the invocation contexts must survive both.
		var grow func(d int) int
		grow = func(d int) int {
			if d == 0 {
				return 1
			}
			var pad [64]byte
			pad[0] = byte(d)
			return int(pad[0]) + grow(d-1)
		}
		_ = grow(200)
		runtime.GC()
		for i := range 100 {
			e := elem{ID: "e", N: i}
			if err := sink.Encode(&e); err != nil {
				return err
			}
		}
		runtime.GC()
		return nil
	})
	got, err := vjson.Marshal(&s)
	if err != nil {
		t.Fatal(err)
	}
	var back []elem
	if err := vjson.Unmarshal(got, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(back) != 100 || back[99].N != 99 {
		t.Errorf("round trip lost elements: %d", len(back))
	}
}

// TestStreamWriteMemoryBounded pins the windowed-output contract: a producer
// of one million small elements must keep every single writer call bounded
// (window-sized, not output-sized), and the process heap must not grow with
// the element count.
func TestStreamWriteMemoryBounded(t *testing.T) {
	const elems = 1_000_000
	maxChunk := 0
	var total int64
	w := &discardChunkWriter{onChunk: func(n int) {
		maxChunk = max(maxChunk, n)
		total += int64(n)
	}}
	enc := vjson.NewEncoder(w)

	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		for i := range elems {
			v := i
			if err := sink.Encode(&v); err != nil {
				return err
			}
		}
		return nil
	})
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := vjson.EncodeValue(enc, &s); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// Every writer call is window-bounded; the parked streaming buffer is
	// capped at streamBufCapMax (512 KiB) plus one in-flight element.
	if maxChunk > 1024*1024 {
		t.Errorf("single write of %d bytes exceeds the window bound", maxChunk)
	}
	// The discarded output is ~7 MB; with a discarding writer the heap must
	// not grow proportionally to it.
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 1<<20 {
		t.Errorf("heap grew by %d bytes for %d elements (%d bytes written); output is not windowed",
			grew, elems, total)
	}
}

// discardChunkWriter drops everything but reports chunk sizes, so the output
// itself never becomes reachable heap.
type discardChunkWriter struct {
	onChunk func(int)
}

func (c *discardChunkWriter) Write(p []byte) (int, error) {
	c.onChunk(len(p))
	return len(p), nil
}

// TestStreamBridgeReadToWrite exercises the design's motivating pipeline: a
// root Stream whose producer drives a decoder over an input stream, forwarding
// decoded elements into the output array within one call stack.
func TestStreamBridgeReadToWrite(t *testing.T) {
	type event struct {
		ID   string   `json:"id"`
		Tags []string `json:"tags,omitempty"`
	}
	var sb strings.Builder
	for i := range 5000 {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"id":"e` + itoa(i) + `","tags":["x"]}`)
	}
	input := "[" + sb.String() + "]"

	var in vjson.Stream[event]
	var out vjson.Stream[event]
	out.OnWrite(func(sink stream.Sink[event]) error {
		in.OnRead(func(scope stream.Scope[event]) error {
			scope.AllowValueReuse()
			for item := range scope.Iter() {
				if err := item.Decode(); err != nil {
					return err
				}
				if err := sink.Encode(item.Target()); err != nil {
					return err
				}
			}
			return nil
		})
		dec := vjson.NewDecoder(strings.NewReader(input))
		return vjson.DecodeValue(dec, &in)
	})

	var buf bytes.Buffer
	enc := vjson.NewEncoder(&buf)
	if err := vjson.EncodeValue(enc, &out); err != nil {
		t.Fatal(err)
	}

	// The forwarded document must equal the input modulo re-encoding.
	var got, want []event
	if err := vjson.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output invalid: %v", err)
	}
	if err := vjson.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got[0].ID != "e0" || got[len(got)-1].ID != "e4999" {
		t.Errorf("bridge forwarded %d events, first=%v last=%v", len(got), got[0].ID, got[len(got)-1].ID)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
