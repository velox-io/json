//go:build !vdec

package tests

import (
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/decode/dom"
)

// UnmarshalValue walks a pre-built tape and binds it, and the strings it reads
// live in the same str_arena it interns new ones into. So it has to start
// appending past the content the input tape already points at.
//
// A ,string field is what makes an append happen: the tape holds the field as a
// quoted JSON literal, and binding it to a Go string unquotes the inner content
// and interns the result. If the append starts at the arena base instead of the
// document's watermark, it lands on top of the very strings the source Value
// indexes, and the caller's Value silently changes underneath them.
type quotedStringHost struct {
	Q string `json:"q,string"`
}

func TestUnmarshalValue_InterningDoesNotClobberSource(t *testing.T) {
	// The interned result is short, so the canary has to be long enough that a
	// base-relative append reaches it, and has to sit later in the arena than
	// the ,string field's own content.
	src := []byte(`{"q":"\"CANARY\"","keep":"KEEP_THIS_VALUE_INTACT_0123456789"}`)
	const want = "KEEP_THIS_VALUE_INTACT_0123456789"

	// Exercise the C-installed Value path.
	var v vjson.Value
	if err := vjson.Unmarshal(src, &v); err != nil {
		t.Fatal(err)
	}

	// Establish that the canary is intact going in, so a failure below can only
	// be the bind's doing.
	if got, ok := v.GetString("keep"); !ok || got != want {
		t.Fatalf("before UnmarshalValue: keep = %q (ok=%v), want %q", got, ok, want)
	}

	var host quotedStringHost
	if err := vjson.UnmarshalValue(v, &host); err != nil {
		t.Fatal(err)
	}
	if host.Q != "CANARY" {
		t.Errorf("Q = %q, want CANARY (the unquoted ,string content)", host.Q)
	}

	if got, ok := v.GetString("keep"); !ok || got != want {
		t.Errorf("after UnmarshalValue: keep = %q (ok=%v), want %q -- interning overwrote the source Value's strings",
			got, ok, want)
	}
}

func TestUnmarshalValue_InterningOwnsAppendRegion(t *testing.T) {
	p := dom.NewParser()
	first, err := p.Parse([]byte(`{"q":"\"FIRST\""}`))
	if err != nil {
		t.Fatal(err)
	}
	later, err := p.Parse([]byte(`{"keep":"LATER_VALUE_MUST_STAY_INTACT"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantLater := later.String()

	var host quotedStringHost
	if err := vjson.UnmarshalValue(first, &host); err != nil {
		t.Fatal(err)
	}
	if host.Q != "FIRST" {
		t.Fatalf("Q = %q, want FIRST", host.Q)
	}
	if got := later.String(); got != wantLater {
		t.Fatalf("later Value mutated: got %s, want %s", got, wantLater)
	}
}
