package venc

import (
	"testing"

	"github.com/velox-io/json/decode/bind"
	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

type uvUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type uvProduct struct {
	SKU   string   `json:"sku"`
	Price float64  `json:"price"`
	Tags  []string `json:"tags"`
}

type uvEmpty struct{}

type uvAllOmit struct {
	X int `json:"x,omitempty"`
}

// inlHost mirrors decode/bind/variant_inline_test.go's inlHost: the Data
// field is an inline variant target; decode selects the case by the Type
// discriminator and binds its fields into Data.
type inlHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[inlHost, struct {
		_ uvUser    `case:"user"`
		_ uvProduct `case:"product"`
		_ uvEmpty   `case:"empty"`
	}]()
}

func unmarshalInl(t *testing.T, src string) inlHost {
	t.Helper()
	var h inlHost
	if err := bind.Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("Unmarshal(%s): %v", src, err)
	}
	return h
}

// TestUnfoldRoundTrip pins the inline variant encode: the case struct's
// fields unfold into the host object with no key of their own, and the
// discriminator field round-trips as an ordinary string field.
func TestUnfoldRoundTrip(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	cases := []struct {
		in   string
		want string
	}{
		{`{"type":"user","id":7,"name":"ann"}`, `{"type":"user","id":7,"name":"ann"}`},
		{`{"type":"product","sku":"s1","price":9.5,"tags":["a","b"]}`, `{"type":"product","sku":"s1","price":9.5,"tags":["a","b"]}`},
		{`{"type":"empty"}`, `{"type":"empty"}`},
	}
	for _, c := range cases {
		h := unmarshalInl(t, c.in)
		got, err := Marshal(h)
		if err != nil {
			t.Fatalf("Marshal(%s): %v", c.in, err)
		}
		if string(got) != c.want {
			t.Errorf("round trip %s: got %s, want %s", c.in, got, c.want)
		}
	}
}

// TestUnfoldEmptyBodyLatch pins the first-latch invariant: a case body that
// writes nothing must not corrupt the host's comma state.
func TestUnfoldEmptyBodyLatch(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	// The empty case struct has no fields: the host carries only Type.
	h := unmarshalInl(t, `{"type":"empty"}`)
	got, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"type":"empty"}` {
		t.Errorf("empty case: got %s", got)
	}

	// All-omitempty case with zero fields writes nothing mid-object: the
	// neighbors must sit next to each other with no stray comma.
	type omitHost struct {
		A    string `json:"a"`
		Body any    `json:",embed" vjson:"variant=kind"`
		B    string `json:"b"`
	}
	bh := omitHost{A: "x", Body: uvAllOmit{}, B: "y"}
	got, err = Marshal(bh)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":"x","b":"y"}` {
		t.Errorf("all-omitempty body: got %s, want {\"a\":\"x\",\"b\":\"y\"}", got)
	}
}

// TestUnfoldNilAndIndent covers the nil case (nothing emitted) and indent
// mode (unfolded fields at the host level).
func TestUnfoldNilAndIndent(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	var h inlHost
	got, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"type":""}` {
		t.Errorf("nil case: got %s, want {\"type\":\"\"}", got)
	}

	h = unmarshalInl(t, `{"type":"user","id":1,"name":"n"}`)
	got, err = MarshalIndent(h, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"type\": \"user\",\n  \"id\": 1,\n  \"name\": \"n\"\n}"
	if string(got) != want {
		t.Errorf("indent:\ngot  %q\nwant %q", got, want)
	}
}

// TestUnfoldPointerCase covers a pointer case target constructed directly:
// decode refuses non-struct inline cases, but a programmatically built host
// can still hold one, and the data-word deref addresses the pointee's fields.
func TestUnfoldPointerCase(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type ptrHost struct {
		Kind string `json:"kind"`
		Obj  any    `json:",embed" vjson:"variant=kind"`
	}
	h := ptrHost{Kind: "user", Obj: &uvUser{ID: 3, Name: "p"}}
	got, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"kind":"user","id":3,"name":"p"}` {
		t.Errorf("pointer case: got %s", got)
	}
}

// TestUnfoldNonStructMisuse pins the deterministic error for a non-struct
// stored in an unfold field.
func TestUnfoldNonStructMisuse(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	h := inlHost{Type: "user", Data: "not a struct"}
	if _, err := Marshal(h); err == nil {
		t.Fatal("expected error for non-struct unfold payload")
	}
}

// TestUnfoldBufFullSweep forces window-full exits inside a body blueprint
// reached through unfold.
func TestUnfoldBufFullSweep(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	h := unmarshalInl(t, `{"type":"product","sku":"a longer sku string here","price":19.99,"tags":["t1","t2","t3"]}`)
	want, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	for n := 8; n <= len(want)+16; n++ {
		got, err := Marshal(h, WithBufSize(n))
		if err != nil {
			t.Fatalf("bufsize %d: %v", n, err)
		}
		if string(got) != string(want) {
			t.Fatalf("bufsize %d: got %s, want %s", n, got, want)
		}
	}
}

// TestUnfoldNestedValue covers an unfold body that itself contains a
// value.Value field, mixing the two new ops.
func TestUnfoldNestedValue(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encvm unavailable")
	}
	type metaHost struct {
		Kind string `json:"kind"`
		Body any    `json:",embed" vjson:"variant=kind"`
	}
	type metaCase struct {
		Label string      `json:"label"`
		Extra value.Value `json:"extra"`
	}
	vbind.DefineVariantCases[metaHost, struct {
		_ metaCase `case:"meta"`
	}]()

	v, err := dom.Parse([]byte(`{"k":[1,2],"s":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	h := metaHost{Kind: "meta", Body: metaCase{Label: "L", Extra: v}}
	got, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"meta","label":"L","extra":{"k":[1,2],"s":"x"}}`
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
