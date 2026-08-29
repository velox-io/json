package bind

import (
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// An embedded variant unfolds its case's fields into the host's JSON object, so
// the host's fields and the case's share one key space. A key is leftover only if
// *neither* side declared it, and that is what a sink (a value.Value field marked
// `json:",embed"`) collects. Classifying against one side alone is wrong in both
// directions: it hands a declared key to a sink, and it drops a genuine leftover.
//
// The tests below pin the case-side sink, which is the half that used to be
// classified against the case's field table only. Its mirror, a host-side sink
// with a case that has none, is variant_inline_coexist_test.go.
//
// See docs/embed_key_space.md for the full shape matrix.

// caseSinkCase carries a sink of its own. Reused by both hosts below so the only
// difference between them is whether the host also declares one.
type caseSinkCase struct {
	Greet string      `json:"greet"`
	Sink  value.Value `json:",embed"`
}

// caseSinkOnlyHost declines leftovers: no sink of its own.
type caseSinkOnlyHost struct {
	Name string `json:"name"`
	Data any    `json:",embed" vjson:"variant=name"`
}

// bothSinksHost declares a sink at depth 0, shallower than the case's.
type bothSinksHost struct {
	Name  string      `json:"name"`
	Data  any         `json:",embed" vjson:"variant=name"`
	HSink value.Value `json:",embed"`
}

func init() {
	vbind.DefineVariantCases[caseSinkOnlyHost, struct {
		_ caseSinkCase `case:"bob"`
	}]()
	vbind.DefineVariantCases[bothSinksHost, struct {
		_ caseSinkCase `case:"bob"`
	}]()
}

// TestCaseSink_CollectsOnlyMutuallyUndeclared is the core case. The host declares
// "name" (its discriminator) and the case declares "greet"; only "a" and "b" are
// leftover. The discriminator is the interesting one: phase1 puts it on the merged
// tape so a value.Value case can still see it, and the case's own field table does
// not declare it, so leaving it there would have the case's sink collect it.
func TestCaseSink_CollectsOnlyMutuallyUndeclared(t *testing.T) {
	var h caseSinkOnlyHost
	if err := Unmarshal([]byte(`{"name":"bob","greet":"hello","a":1,"b":2}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Name != "bob" {
		t.Errorf("Name = %q, want %q", h.Name, "bob")
	}
	c, ok := h.Data.(caseSinkCase)
	if !ok {
		t.Fatalf("Data = %T, want caseSinkCase", h.Data)
	}
	if c.Greet != "hello" {
		t.Errorf("Greet = %q, want %q", c.Greet, "hello")
	}
	if c.Sink.Type() != value.KindObject {
		t.Fatalf("Sink.Type = %v, want KindObject", c.Sink.Type())
	}
	if c.Sink.Len() != 2 {
		t.Fatalf("Sink.Len = %d, want 2 (a, b); got %v", c.Sink.Len(), c.Sink)
	}
	if v := c.Sink.Get("name"); v.Valid() {
		t.Error("Sink collected the host's discriminator; it is declared, not leftover")
	}
	if v := c.Sink.Get("greet"); v.Valid() {
		t.Error("Sink collected a key the case itself declares")
	}
	for _, k := range []string{"a", "b"} {
		got := c.Sink.Get(k)
		if n, ok := got.Int(); !ok || (k == "a" && n != 1) || (k == "b" && n != 2) {
			t.Errorf("Sink.%s = %d (ok=%v), want the leftover value", k, n, ok)
		}
	}
}

// TestCaseSink_LosesToShallowerHostSink pins depth precedence. Two sinks compete
// for the same leftovers; the host's is shallower and takes all of them, exactly as
// a shallower field shadows a deeper same-named one. The case's sink must still
// read as an empty object rather than as invalid.
func TestCaseSink_LosesToShallowerHostSink(t *testing.T) {
	var h bothSinksHost
	if err := Unmarshal([]byte(`{"name":"bob","greet":"hello","a":1,"b":2}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	c, ok := h.Data.(caseSinkCase)
	if !ok {
		t.Fatalf("Data = %T, want caseSinkCase", h.Data)
	}
	if c.Greet != "hello" {
		t.Errorf("Greet = %q, want %q", c.Greet, "hello")
	}
	if h.HSink.Len() != 2 {
		t.Errorf("HSink.Len = %d, want 2 (a, b); the shallower sink takes every leftover", h.HSink.Len())
	}
	if c.Sink.Type() != value.KindObject {
		t.Fatalf("case Sink.Type = %v, want KindObject (empty, not invalid)", c.Sink.Type())
	}
	if c.Sink.Len() != 0 {
		t.Errorf("case Sink.Len = %d, want 0; the host's sink is shallower and won", c.Sink.Len())
	}
}

// TestCaseSink_NoLeftover distinguishes "collected nothing" from "collected the
// wrong thing". Every key is declared by one side or the other, so the case's sink
// is empty for lack of input rather than for losing a contest.
func TestCaseSink_NoLeftover(t *testing.T) {
	var h caseSinkOnlyHost
	if err := Unmarshal([]byte(`{"name":"bob","greet":"hello"}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	c, ok := h.Data.(caseSinkCase)
	if !ok {
		t.Fatalf("Data = %T, want caseSinkCase", h.Data)
	}
	if c.Sink.Type() != value.KindObject {
		t.Fatalf("Sink.Type = %v, want KindObject (empty, not invalid)", c.Sink.Type())
	}
	if c.Sink.Len() != 0 {
		t.Errorf("Sink.Len = %d, want 0", c.Sink.Len())
	}
}

// TestCaseSink_DiscLast is the ordering that forces the classification to happen at
// struct close. With the discriminator last, no member can be classified as it
// arrives: the case is unknown, so "greet" cannot be recognized as case content nor
// "a"/"b" as leftover. Everything is taped and split afterwards.
func TestCaseSink_DiscLast(t *testing.T) {
	var h caseSinkOnlyHost
	if err := Unmarshal([]byte(`{"greet":"hello","a":1,"b":2,"name":"bob"}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	c, ok := h.Data.(caseSinkCase)
	if !ok {
		t.Fatalf("Data = %T, want caseSinkCase", h.Data)
	}
	if c.Greet != "hello" {
		t.Errorf("Greet = %q, want %q", c.Greet, "hello")
	}
	if c.Sink.Len() != 2 {
		t.Errorf("Sink.Len = %d, want 2 (a, b); got %v", c.Sink.Len(), c.Sink)
	}
}

// TestCaseSink_StrictMode: a case-side sink volunteers to carry unmatched keys, so
// strict mode must stay inert. The reject only fires when nothing would place the
// key, and the host declining it is not the same as no one wanting it.
func TestCaseSink_StrictMode(t *testing.T) {
	var h caseSinkOnlyHost
	err := Unmarshal([]byte(`{"name":"bob","greet":"hello","surprise":true}`), &h, WithDisallowUnknownFields())
	if err != nil {
		t.Fatalf("Unmarshal: %v; a case-side sink places the key, so strict mode must not reject", err)
	}
	c, ok := h.Data.(caseSinkCase)
	if !ok {
		t.Fatalf("Data = %T, want caseSinkCase", h.Data)
	}
	if c.Sink.Len() != 1 {
		t.Errorf("Sink.Len = %d, want 1 (surprise collected, not rejected)", c.Sink.Len())
	}
}

// TestCaseSink_NestedContainers pins that a container reaching a case-side sink
// survives the trip. Its entries carry paired indices relative to the tape it was
// written on, and the sink publishes a sub-span of that tape, so a stale base shows
// up as a wrong child rather than as an error.
func TestCaseSink_NestedContainers(t *testing.T) {
	var h caseSinkOnlyHost
	src := `{"a":{"deep":[1,2]},"name":"bob","greet":"hello","b":[{"k":true}]}`
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	c, ok := h.Data.(caseSinkCase)
	if !ok {
		t.Fatalf("Data = %T, want caseSinkCase", h.Data)
	}
	if c.Sink.Len() != 2 {
		t.Fatalf("Sink.Len = %d, want 2", c.Sink.Len())
	}
	ca := c.Sink.Get("a")
	deep := ca.Get("deep")
	if deep.Type() != value.KindArray || deep.Len() != 2 {
		t.Fatalf("Sink.a.deep = %v len %d, want array of 2", deep.Type(), deep.Len())
	}
	d1 := deep.Index(1)
	if n, ok := d1.Int(); !ok || n != 2 {
		t.Errorf("Sink.a.deep[1] = %d (ok=%v), want 2", n, ok)
	}
	cb := c.Sink.Get("b")
	cb0 := cb.Index(0)
	cbk := cb0.Get("k")
	if b, ok := cbk.Bool(); !ok || !b {
		t.Errorf("Sink.b[0].k = %v (ok=%v), want true", b, ok)
	}
}

// TestCaseSink_Repeated reuses one Parser so the tape arena and slot-class cursors
// advance across parses. A split that assumed a zero base, or an entry excised from
// the wrong tape, drifts into failure only after the cursors have moved.
func TestCaseSink_Repeated(t *testing.T) {
	p, err := NewParser[caseSinkOnlyHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := []byte(`{"name":"bob","greet":"hello","a":{"x":1},"b":2}`)
	for i := range 8 {
		var h caseSinkOnlyHost
		if err := p.Unmarshal(src, &h); err != nil {
			t.Fatalf("iter %d: Unmarshal: %v", i, err)
		}
		c, ok := h.Data.(caseSinkCase)
		if !ok {
			t.Fatalf("iter %d: Data = %T, want caseSinkCase", i, h.Data)
		}
		if c.Greet != "hello" {
			t.Fatalf("iter %d: Greet = %q, want %q", i, c.Greet, "hello")
		}
		if c.Sink.Len() != 2 {
			t.Fatalf("iter %d: Sink.Len = %d, want 2", i, c.Sink.Len())
		}
		sa := c.Sink.Get("a")
		sax := sa.Get("x")
		if n, ok := sax.Int(); !ok || n != 1 {
			t.Fatalf("iter %d: Sink.a.x = %d (ok=%v), want 1", i, n, ok)
		}
	}
}
