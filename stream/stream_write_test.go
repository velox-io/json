package stream_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
)

type wEvent struct {
	ID   string   `json:"id"`
	N    int      `json:"n"`
	Tags []string `json:"tags,omitempty"`
}

type wResponse struct {
	Events  stream.Stream[wEvent] `json:"events"`
	Message string                `json:"message"`
}

func TestOnWriteSmokeField(t *testing.T) {
	var resp wResponse
	resp.Events.OnWrite(func(sink stream.Sink[wEvent]) error {
		for i := range 3 {
			e := wEvent{ID: "e", N: i, Tags: []string{"x"}}
			if err := sink.Encode(&e); err != nil {
				return err
			}
		}
		return nil
	})
	resp.Message = "done"

	got, err := vjson.Marshal(&resp)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"events":[{"id":"e","n":0,"tags":["x"]},{"id":"e","n":1,"tags":["x"]},{"id":"e","n":2,"tags":["x"]}],"message":"done"}`
	if string(got) != want {
		t.Errorf("Marshal:\n got %s\nwant %s", got, want)
	}

	gotInd, err := vjson.MarshalIndent(&resp, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wantInd := `{
  "events": [
    {
      "id": "e",
      "n": 0,
      "tags": [
        "x"
      ]
    },
    {
      "id": "e",
      "n": 1,
      "tags": [
        "x"
      ]
    },
    {
      "id": "e",
      "n": 2,
      "tags": [
        "x"
      ]
    }
  ],
  "message": "done"
}`
	if string(gotInd) != wantInd {
		t.Errorf("MarshalIndent:\n got %s\nwant %s", gotInd, wantInd)
	}
}

func TestOnWriteSmokeRoot(t *testing.T) {
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		for i := range 3 {
			v := (i + 1) * 10
			if err := sink.Encode(&v); err != nil {
				return err
			}
		}
		return nil
	})

	got, err := vjson.Marshal(&s)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `[10,20,30]` {
		t.Errorf("root Marshal: %s", got)
	}

	var buf bytes.Buffer
	enc := vjson.NewEncoder(&buf)
	if err := vjson.EncodeValue(enc, &s); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "[10,20,30]\n" {
		t.Errorf("root EncodeValue: %q", buf.String())
	}
}

func TestOnWriteSmokeEmptyAndOmit(t *testing.T) {
	type doc struct {
		Empty stream.Stream[int] `json:"empty"`
		Omit  stream.Stream[int] `json:"omit,omitempty"`
		Full  stream.Stream[int] `json:"full,omitempty"`
	}
	var d doc
	produce := func(sink stream.Sink[int]) error { return nil }
	d.Empty.OnWrite(produce)
	d.Omit.OnWrite(produce)
	d.Full.OnWrite(func(sink stream.Sink[int]) error {
		v := 1
		return sink.Encode(&v)
	})

	got, err := vjson.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"empty":[],"full":[1]}`
	if string(got) != want {
		t.Errorf("Marshal:\n got %s\nwant %s", got, want)
	}
}

func TestOnWriteSmokeUnconfigured(t *testing.T) {
	type doc struct {
		S stream.Stream[int] `json:"s"`
	}
	_, err := vjson.Marshal(&doc{})
	if err == nil {
		t.Fatal("unconfigured stream field must be an error")
	}
	if !strings.Contains(err.Error(), "OnWrite") {
		t.Errorf("error should mention OnWrite: %v", err)
	}

	type docOmit struct {
		S stream.Stream[int] `json:"s,omitempty"`
	}
	got, err := vjson.Marshal(&docOmit{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{}` {
		t.Errorf("unconfigured omitempty stream: %s", got)
	}
}

func TestOnWriteSmokeEncoderStreaming(t *testing.T) {
	// A producer that would overflow the buffer window must keep the writer
	// receiving data before the handler returns.
	counting := &countWriter{w: &bytes.Buffer{}}
	enc := vjson.NewEncoder(counting)

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
	if err := vjson.EncodeValue(enc, &s); err != nil {
		t.Fatal(err)
	}
	if counting.flushes == 0 {
		t.Error("writer never received data during the handler")
	}
	out := counting.w.String()
	if !strings.HasPrefix(out, "[0,1,2,") || !strings.HasSuffix(out, "99999]\n") {
		t.Errorf("unexpected output shape: %q...", out[:min(len(out), 32)])
	}
	var dec []int
	if err := json.Unmarshal([]byte(strings.TrimSuffix(out, "\n")), &dec); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if len(dec) != 100_000 || dec[99_999] != 99_999 {
		t.Errorf("decoded %d elements, last=%v", len(dec), dec[99_999])
	}
}

type countWriter struct {
	w       *bytes.Buffer
	flushes int
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.flushes++
	return c.w.Write(p)
}

// A stream held by an interface encodes as its array wherever the interface
// sits, and a producer's error fails the encode unchanged.
func TestOnWriteThroughAnyAndErrors(t *testing.T) {
	events := func() *stream.Stream[wEvent] {
		s := &stream.Stream[wEvent]{}
		s.OnWrite(func(sink stream.Sink[wEvent]) error {
			for i := range 2 {
				if err := sink.Encode(&wEvent{ID: "e", N: i}); err != nil {
					return err
				}
			}
			return nil
		})
		return s
	}
	const arr = `[{"id":"e","n":0},{"id":"e","n":1}]`
	type anyHost struct {
		A any    `json:"a"`
		M string `json:"m"`
	}
	for _, tc := range []struct {
		name string
		val  func() any
		want string
	}{
		{"root any", func() any { return events() }, arr},
		{"struct any field", func() any { return &anyHost{A: events(), M: "x"} }, `{"a":` + arr + `,"m":"x"}`},
		{"slice element", func() any { return []any{1, events(), nil} }, `[1,` + arr + `,null]`},
		{"map value", func() any { return map[string]any{"s": events()} }, `{"s":` + arr + `}`},
	} {
		got, err := vjson.Marshal(tc.val())
		if err != nil {
			t.Fatalf("%s: Marshal: %v", tc.name, err)
		}
		if string(got) != tc.want {
			t.Errorf("%s: Marshal = %s, want %s", tc.name, got, tc.want)
		}
		var want bytes.Buffer
		if ierr := json.Indent(&want, []byte(tc.want), "> ", "\t"); ierr != nil {
			t.Fatal(ierr)
		}
		got, err = vjson.MarshalIndent(tc.val(), "> ", "\t")
		if err != nil {
			t.Fatalf("%s: MarshalIndent: %v", tc.name, err)
		}
		if string(got) != want.String() {
			t.Errorf("%s: MarshalIndent =\n%s\nwant\n%s", tc.name, got, want.String())
		}
	}

	sentinel := errors.New("producer failed")
	fail := func(s *stream.Stream[wEvent]) *stream.Stream[wEvent] {
		s.OnWrite(func(sink stream.Sink[wEvent]) error {
			if err := sink.Encode(&wEvent{ID: "e"}); err != nil {
				return err
			}
			return sentinel
		})
		return s
	}
	for name, val := range map[string]func() any{
		"field":        func() any { r := &wResponse{}; fail(&r.Events); return r },
		"root":         func() any { return fail(&stream.Stream[wEvent]{}) },
		"slice of any": func() any { return []any{fail(&stream.Stream[wEvent]{})} },
	} {
		if _, err := vjson.Marshal(val()); !errors.Is(err, sentinel) {
			t.Errorf("%s: Marshal error = %v, want the producer's error", name, err)
		}
		var buf bytes.Buffer
		if err := vjson.NewEncoder(&buf).Encode(val()); !errors.Is(err, sentinel) {
			t.Errorf("%s: Encoder error = %v, want the producer's error", name, err)
		}
	}
}
