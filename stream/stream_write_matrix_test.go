package stream_test

import (
	"bytes"
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
)

// produce returns an OnWrite handler that submits the given items in order.
func produce[T any](items ...T) func(stream.Sink[T]) error {
	return func(sink stream.Sink[T]) error {
		for i := range items {
			if err := sink.Encode(&items[i]); err != nil {
				return err
			}
		}
		return nil
	}
}

// parityCase drives one structural case through every engine and entry point,
// comparing against the equivalent slice-based value.
type parityCase struct {
	name       string
	stream     any // value whose type tree contains Stream fields
	equivalent any // same value with []T slices instead of Streams
}

func assertStreamParity(t *testing.T, cases []parityCase) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			engines := []struct {
				name          string
				prefix, ident string
			}{
				{"compact", "", ""},
				{"native-indent", "", "  "},
				{"interp-indent", "", "--"}, // non-simple indent forces the interpreter
			}
			for _, eng := range engines {
				got, err := vjson.MarshalIndent(c.stream, eng.prefix, eng.ident)
				if err != nil {
					t.Fatalf("%s: %v", eng.name, err)
				}
				// Compact output is byte-comparable with encoding/json. The
				// indent engines compare against the equivalent slice value
				// encoded by vjson itself: RawMessage and Marshaler elements
				// are appended verbatim, while encoding/json re-indents them.
				var want []byte
				if eng.ident == "" {
					want, err = json.Marshal(c.equivalent)
				} else {
					want, err = vjson.MarshalIndent(c.equivalent, eng.prefix, eng.ident)
				}
				if err != nil {
					t.Fatalf("%s equiv: %v", eng.name, err)
				}
				if string(got) != string(want) {
					t.Errorf("%s Marshal:\n got %s\nwant %s", eng.name, got, want)
				}
			}

			// Encoder (stream mode) must agree with Marshal byte-for-byte.
			var buf bytes.Buffer
			enc := vjson.NewEncoder(&buf)
			if err := vjson.EncodeValue(enc, c.stream); err != nil {
				t.Fatalf("EncodeValue: %v", err)
			}
			want, _ := vjson.Marshal(c.stream)
			if buf.String() != string(want)+"\n" {
				t.Errorf("Encoder:\n got %q\nwant %q", buf.String(), string(want)+"\n")
			}
		})
	}
}

type mInner struct {
	Name string         `json:"name"`
	Tags []string       `json:"tags,omitempty"`
	Sub  map[string]int `json:"sub,omitempty"`
}

type mMarshaler struct{ N int }

func (m mMarshaler) MarshalJSON() ([]byte, error) { return []byte(`{"m":1}`), nil }

// mAnyHolder's X field holds an any whose payload is a pointer: the iface
// cache stores no blueprint for pointer kinds, so every encode of X yields to
// Go mid-run.
type mAnyHolder struct {
	Name string `json:"name"`
	X    any    `json:"x"`
}

type mLabeler interface{ Label() string }

func (m *mAnyHolder) Label() string { return m.Name }

func TestStreamPositionMatrix(t *testing.T) {
	items := []mInner{
		{Name: "a", Tags: []string{"x"}, Sub: map[string]int{"k": 1}},
		{Name: "b"},
		{},
	}

	t.Run("field-position", func(t *testing.T) {
		type sDoc struct {
			Head stream.Stream[mInner] `json:"head"`
			Mid  string                `json:"mid"`
			Tail stream.Stream[mInner] `json:"tail"`
		}
		type eDoc struct {
			Head []mInner `json:"head"`
			Mid  string   `json:"mid"`
			Tail []mInner `json:"tail"`
		}
		assertStreamParity(t, []parityCase{
			{
				"first-and-last",
				sDoc{Head: mkStream(t, produce(items...)), Mid: "m", Tail: mkStream(t, produce(items[:1]...))},
				eDoc{Head: items, Mid: "m", Tail: items[:1]},
			},
			{
				"empty-head",
				sDoc{Head: mkStream(t, produce[mInner]()), Mid: "m", Tail: mkStream(t, produce(items...))},
				eDoc{Head: []mInner{}, Mid: "m", Tail: items},
			},
		})
	})

	t.Run("omitempty", func(t *testing.T) {
		type sDoc struct {
			A stream.Stream[mInner] `json:"a,omitempty"`
			B stream.Stream[mInner] `json:"b,omitempty"`
			C int                   `json:"c"`
		}
		type eDoc struct {
			A []mInner `json:"a,omitempty"`
			B []mInner `json:"b,omitempty"`
			C int      `json:"c"`
		}
		assertStreamParity(t, []parityCase{
			{
				"both-empty",
				sDoc{A: mkStream(t, produce[mInner]()), B: mkStream(t, produce[mInner]())},
				eDoc{A: []mInner{}, B: []mInner{}},
			},
			{
				"b-filled",
				sDoc{B: mkStream(t, produce(items[:1]...))},
				eDoc{B: items[:1]},
			},
		})
	})

	t.Run("sibling", func(t *testing.T) {
		type sDoc struct {
			X stream.Stream[int]    `json:"x"`
			Y stream.Stream[string] `json:"y"`
		}
		type eDoc struct {
			X []int    `json:"x"`
			Y []string `json:"y"`
		}
		assertStreamParity(t, []parityCase{
			{
				"two-streams",
				sDoc{X: mkStream(t, produce(1, 2, 3)), Y: mkStream(t, produce("p", "q"))},
				eDoc{X: []int{1, 2, 3}, Y: []string{"p", "q"}},
			},
		})
	})

	t.Run("pointer", func(t *testing.T) {
		type sDoc struct {
			P *stream.Stream[int] `json:"p"`
		}
		type eDoc struct {
			P *[]int `json:"p"`
		}
		ps := mkStream(t, produce(7, 8))
		ref := []int{7, 8}
		assertStreamParity(t, []parityCase{
			{"non-nil", sDoc{P: &ps}, eDoc{P: &ref}},
			{"nil", sDoc{}, eDoc{}},
		})
	})

	t.Run("slice-and-array", func(t *testing.T) {
		type sDoc struct {
			S []stream.Stream[int]  `json:"s"`
			A [2]stream.Stream[int] `json:"a"`
		}
		type eDoc struct {
			S [][]int  `json:"s"`
			A [2][]int `json:"a"`
		}
		assertStreamParity(t, []parityCase{
			{
				"of-streams",
				sDoc{
					S: []stream.Stream[int]{mkStream(t, produce(1)), mkStream(t, produce[int]())},
					A: [2]stream.Stream[int]{mkStream(t, produce(2, 3)), mkStream(t, produce(4))},
				},
				eDoc{
					S: [][]int{{1}, {}},
					A: [2][]int{{2, 3}, {4}},
				},
			},
		})
	})

	t.Run("map-value", func(t *testing.T) {
		type sDoc struct {
			M map[string]stream.Stream[int] `json:"m"`
		}
		type eDoc struct {
			M map[string][]int `json:"m"`
		}
		assertStreamParity(t, []parityCase{
			{
				"single-entry",
				sDoc{M: map[string]stream.Stream[int]{"k": mkStream(t, produce(5, 6))}},
				eDoc{M: map[string][]int{"k": {5, 6}}},
			},
		})
	})

	t.Run("any", func(t *testing.T) {
		type sDoc struct {
			X any `json:"x"`
		}
		type eDoc struct {
			X any `json:"x"`
		}
		assertStreamParity(t, []parityCase{
			{
				"dynamic-stream",
				sDoc{X: mkStream(t, produce(9, 10))},
				eDoc{X: []int{9, 10}},
			},
		})
	})

	t.Run("root", func(t *testing.T) {
		assertStreamParity(t, []parityCase{
			{"root-int", mkStream(t, produce(1, 2)), []int{1, 2}},
			{"root-empty", mkStream(t, produce[int]()), []int{}},
			{"root-struct", mkStream(t, produce(items...)), items},
		})
	})
}

func TestStreamElementKindMatrix(t *testing.T) {
	t.Run("value-raw-marshaler-elements", func(t *testing.T) {
		type sDoc struct {
			V stream.Stream[value.Value]     `json:"v"`
			R stream.Stream[json.RawMessage] `json:"r"`
			M stream.Stream[mMarshaler]      `json:"m"`
		}
		type eDoc struct {
			V []value.Value     `json:"v"`
			R []json.RawMessage `json:"r"`
			M []mMarshaler      `json:"m"`
		}
		assertStreamParity(t, []parityCase{
			{
				"mixed-elements",
				sDoc{
					V: mkStream(t, produce(parseValue(t, `{"a":[1,{"b":true}]}`))),
					R: mkStream(t, produce(json.RawMessage(`{"z":2}`))),
					M: mkStream(t, produce(mMarshaler{})),
				},
				eDoc{
					V: []value.Value{parseValue(t, `{"a":[1,{"b":true}]}`)},
					R: []json.RawMessage{json.RawMessage(`{"z":2}`)},
					M: []mMarshaler{{}},
				},
			},
		})
	})

	t.Run("element-kinds", func(t *testing.T) {
		type sDoc struct {
			P stream.Stream[*mInner]           `json:"p"`
			L stream.Stream[[]string]          `json:"l"`
			F stream.Stream[[2]int]            `json:"f"`
			G stream.Stream[map[string]string] `json:"g"`
			N stream.Stream[*int]              `json:"n"`
			B stream.Stream[bool]              `json:"b"`
			A stream.Stream[any]               `json:"a"`
			I stream.Stream[mLabeler]          `json:"i"`
		}
		type eDoc struct {
			P []*mInner           `json:"p"`
			L [][]string          `json:"l"`
			F [][2]int            `json:"f"`
			G []map[string]string `json:"g"`
			N []*int              `json:"n"`
			B []bool              `json:"b"`
			A []any               `json:"a"`
			I []mLabeler          `json:"i"`
		}
		five := 5
		assertStreamParity(t, []parityCase{
			{
				"kinds",
				sDoc{
					P: mkStream(t, produce(&mInner{Name: "p"}, (*mInner)(nil))),
					L: mkStream(t, produce([]string{"a"}, []string{})),
					F: mkStream(t, produce([2]int{1, 2})),
					G: mkStream(t, produce(map[string]string{"k": "v"})),
					N: mkStream(t, produce(&five, (*int)(nil))),
					B: mkStream(t, produce(true, false, true)),
					A: mkStream(t, produce[any](&mAnyHolder{Name: "a", X: &mInner{Name: "x"}}, nil)),
					I: mkStream(t, produce[mLabeler](&mAnyHolder{Name: "i", X: &mInner{Name: "y"}})),
				},
				eDoc{
					P: []*mInner{{Name: "p"}, nil},
					L: [][]string{{"a"}, {}},
					F: [][2]int{{1, 2}},
					G: []map[string]string{{"k": "v"}},
					N: []*int{&five, nil},
					B: []bool{true, false, true},
					A: []any{&mAnyHolder{Name: "a", X: &mInner{Name: "x"}}, nil},
					I: []mLabeler{&mAnyHolder{Name: "i", X: &mInner{Name: "y"}}},
				},
			},
		})
	})
}

// TestStreamRootAnyElementDepthBound reproduces the per-element indent-depth
// leak. A root Stream of any elements holding pointers encodes each pointee
// through an outermost exec; the pointee's any-pointer field yields to Go on
// every encode, and the yield synced es.indentDepth up from ctx.IndentDepth
// without a matching restore at run exit. The depth compounded per element
// until writeIndent sliced past the 768-byte indent template
// (slice bounds out of range [:769] with length 768).
func TestStreamRootAnyElementDepthBound(t *testing.T) {
	n := 400
	items := make([]any, n)
	for i := range items {
		items[i] = &mAnyHolder{Name: "e", X: &mInner{Name: "x"}}
	}
	root := mkStream(t, produce(items...))
	for _, ident := range []string{"  ", "--"} {
		got, err := vjson.MarshalIndent(root, "", ident)
		if err != nil {
			t.Fatalf("MarshalIndent(%q): %v", ident, err)
		}
		want, err := vjson.MarshalIndent(items, "", ident)
		if err != nil {
			t.Fatalf("oracle MarshalIndent(%q): %v", ident, err)
		}
		if string(got) != string(want) {
			g, w := tail(got), tail(want)
			t.Errorf("root any elements MarshalIndent(%q):\n got %s\nwant %s", ident, g, w)
		}
	}
}

func TestStreamNestedMatrix(t *testing.T) {
	type sChild struct {
		Name string                `json:"name"`
		Kids stream.Stream[sChild] `json:"kids"`
	}
	// leaf builds a child whose own Kids producer is empty; every level must
	// configure a producer because kids is not omitempty.
	leaf := func(name string) sChild {
		return sChild{Name: name, Kids: mkStream(t, produce[sChild]())}
	}

	// Two levels of nested producers inside level-1 elements.
	root := sChild{Name: "root", Kids: mkStream(t, produce(
		sChild{Name: "k1", Kids: mkStream(t, produce(leaf("g1"), leaf("g2")))},
		leaf("k2"),
	))}

	// The oracle is the same value re-encoded as a slice tree: unmarshal the
	// compact form into the equivalent shape and compare all engines against
	// the same library encoding of that shape.
	type eChild struct {
		Name string   `json:"name"`
		Kids []eChild `json:"kids"`
	}
	compact, err := vjson.Marshal(&root)
	if err != nil {
		t.Fatal(err)
	}
	var round eChild
	if err = json.Unmarshal(compact, &round); err != nil {
		t.Fatalf("compact output is not valid JSON: %v\n%s", err, compact)
	}

	var got, want []byte
	for _, ident := range []string{"  ", "--"} {
		got, err = vjson.MarshalIndent(&root, "", ident)
		if err != nil {
			t.Fatal(err)
		}
		want, err = vjson.MarshalIndent(&round, "", ident)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("nested MarshalIndent(%q):\n got %s\nwant %s", ident, got, want)
		}
	}

	// Level 3: a producer nested two activations deep.
	deep := sChild{Name: "l0", Kids: mkStream(t, func(sink stream.Sink[sChild]) error {
		l1 := sChild{Name: "l1", Kids: mkStream(t, produce(leaf("l2")))}
		return sink.Encode(&l1)
	})}
	gotDeep, err := vjson.Marshal(&deep)
	if err != nil {
		t.Fatal(err)
	}
	wantDeep := `{"name":"l0","kids":[{"name":"l1","kids":[{"name":"l2","kids":[]}]}]}`
	if string(gotDeep) != wantDeep {
		t.Errorf("3-level Marshal:\n got %s\nwant %s", gotDeep, wantDeep)
	}
	gotDeepInd, err := vjson.MarshalIndent(&deep, "", "--")
	if err != nil {
		t.Fatal(err)
	}
	var roundDeep eChild
	if err = json.Unmarshal(gotDeep, &roundDeep); err != nil {
		t.Fatal(err)
	}
	wantDeepInd, err := vjson.MarshalIndent(&roundDeep, "", "--")
	if err != nil {
		t.Fatal(err)
	}
	if string(gotDeepInd) != string(wantDeepInd) {
		t.Errorf("3-level MarshalIndent:\n got %s\nwant %s", gotDeepInd, wantDeepInd)
	}
}

// mkStream returns a Stream configured with the given producer. It fails the
// test if handle is nil, catching fixture mistakes.
func mkStream[T any](t *testing.T, handle func(stream.Sink[T]) error) stream.Stream[T] {
	t.Helper()
	if handle == nil {
		t.Fatal("nil producer in fixture")
	}
	var s stream.Stream[T]
	s.OnWrite(handle)
	return s
}

// parseValue returns a value.Value parsed from src, failing the test on
// invalid input.
func parseValue(t *testing.T, src string) value.Value {
	t.Helper()
	v, err := vjson.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	return v
}

// tail returns the last 60 bytes of b (or all of it), keeping mismatch
// diagnostics on large streaming payloads readable.
func tail(b []byte) []byte {
	if len(b) > 60 {
		return b[len(b)-60:]
	}
	return b
}
