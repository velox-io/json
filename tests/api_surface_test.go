package tests

// The root package's thin entry points: buffer padding, the padded decode
// entries, deep copy, Decoder sizing options, kindof registration, and the
// encoding/json aliases. Each is checked against its documented contract
// through the root package rather than the package it forwards to.

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

func TestPad(t *testing.T) {
	src := []byte(`{"name":"al","age":3}`)
	padded := vjson.Pad(src)
	if len(padded) != len(src) || cap(padded) != len(src)+vjson.PaddingSize {
		t.Fatalf("Pad len/cap = %d/%d, want %d/%d", len(padded), cap(padded), len(src), len(src)+vjson.PaddingSize)
	}
	if string(padded) != string(src) {
		t.Fatalf("Pad changed the content: %s", padded)
	}
	for i, b := range padded[len(src):cap(padded)] {
		if b != ' ' {
			t.Fatalf("pad byte %d = %#x, want 0x20", i, b)
		}
	}
	if &padded[0] == &src[0] {
		t.Error("Pad wrote into a buffer with no spare capacity")
	}

	// Spare capacity is reused in place.
	roomy := make([]byte, len(src), len(src)+vjson.PaddingSize+8)
	copy(roomy, src)
	if again := vjson.Pad(roomy); &again[0] != &roomy[0] || len(again) != len(src) {
		t.Error("Pad copied a buffer that had room for the padding")
	}
}

func TestPaddedEntriesAgree(t *testing.T) {
	type user struct {
		Name string   `json:"name"`
		Age  int      `json:"age"`
		Tags []string `json:"tags"`
	}
	doc := []byte(`{"name":"al\u00e9","age":3,"tags":["a","b\"c"]}`)
	var want, got user
	if err := json.Unmarshal(doc, &want); err != nil {
		t.Fatal(err)
	}
	if err := vjson.UnmarshalPadded(vjson.Pad(doc), &got); err != nil {
		t.Fatalf("UnmarshalPadded: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnmarshalPadded = %+v, want %+v", got, want)
	}
	v, err := vjson.ParsePadded(vjson.Pad(doc))
	if err != nil {
		t.Fatalf("ParsePadded: %v", err)
	}
	got = user{}
	if err := vjson.UnmarshalValue(v, &got); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePadded+UnmarshalValue = %+v, want %+v", got, want)
	}
	var syn *json.SyntaxError
	if err := vjson.UnmarshalPadded(vjson.Pad([]byte(`{"name":`)), &got); !errors.As(err, &syn) {
		t.Errorf("UnmarshalPadded of a truncated document = %v, want a SyntaxError", err)
	}
}

type copyNode struct {
	V    int
	Next *copyNode
}

func TestDeepCopyAndCopyInto(t *testing.T) {
	type leaf struct{ T []string }
	type host struct {
		Name string
		Tags []string
		Idx  map[string]int
		In   leaf
		P    *leaf
	}
	orig := host{
		Name: "n", Tags: []string{"a", "b"}, Idx: map[string]int{"k": 1},
		In: leaf{T: []string{"x"}}, P: &leaf{T: []string{"p"}},
	}
	cp, err := vjson.DeepCopy(orig)
	if err != nil {
		t.Fatalf("DeepCopy: %v", err)
	}
	if !reflect.DeepEqual(cp, orig) {
		t.Fatalf("DeepCopy = %+v, want %+v", cp, orig)
	}
	cp.Tags[0], cp.Idx["k"], cp.In.T[0], cp.P.T[0] = "z", 9, "y", "q"
	if orig.Tags[0] != "a" || orig.Idx["k"] != 1 || orig.In.T[0] != "x" || orig.P.T[0] != "p" {
		t.Error("DeepCopy shares storage with the original")
	}

	// CopyInto replaces the destination's contents rather than merging.
	dst := host{Tags: []string{"old", "old", "old"}, Idx: map[string]int{"old": 1}}
	if err = vjson.CopyInto(orig, &dst); err != nil {
		t.Fatalf("CopyInto: %v", err)
	}
	if !reflect.DeepEqual(dst, orig) {
		t.Errorf("CopyInto = %+v, want %+v", dst, orig)
	}

	// A cycle copies into the same cycle over new nodes.
	a := &copyNode{V: 1}
	a.Next = &copyNode{V: 2, Next: a}
	c, err := vjson.DeepCopy(a)
	if err != nil {
		t.Fatalf("DeepCopy(cycle): %v", err)
	}
	if c == a || c.Next == a.Next || c.Next.Next != c || c.V != 1 || c.Next.V != 2 {
		t.Errorf("DeepCopy(cycle) did not reproduce the cycle over new nodes")
	}
}

// The sizing options change only the Decoder's buffering: a run of values,
// some larger than the starting buffer, decodes as encoding/json decodes it.
func TestDecoderSizingOptions(t *testing.T) {
	type rec struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	var b strings.Builder
	for i := range 20 {
		b.WriteString(`{"id":`)
		b.WriteString(strings.Repeat("1", 1+i%3))
		b.WriteString(`,"body":"`)
		b.WriteString(strings.Repeat("x", i*37))
		b.WriteString(`"}` + "\n")
	}
	in := b.String()
	var want []rec
	sd := json.NewDecoder(strings.NewReader(in))
	for {
		var r rec
		if err := sd.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		want = append(want, r)
	}
	for name, opt := range map[string]vjson.DecoderOption{
		"small buffer":   vjson.WithBufferSize(16),
		"large buffer":   vjson.WithBufferSize(1 << 16),
		"expected small": vjson.WithExpectedSize(8),
		"expected large": vjson.WithExpectedSize(4096),
	} {
		d := vjson.NewDecoder(strings.NewReader(in), opt)
		var got []rec
		for {
			var r rec
			if err := d.Decode(&r); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s: Decode: %v", name, err)
			}
			got = append(got, r)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: decoded %d records, encoding/json %d, or their contents differ", name, len(got), len(want))
		}
	}
}

type apiKindofHost struct {
	Val any `json:"val" vjson:"kindof"`
}

type apiKindofObject struct {
	Name string `json:"name"`
}

func TestDefineKindofCases(t *testing.T) {
	vjson.DefineKindofCases[apiKindofHost, struct {
		number int
		string string
		object apiKindofObject
		array  []string
	}]()
	for _, tc := range []struct {
		in   string
		want any
	}{
		{`{"val":7}`, 7},
		{`{"val":"s"}`, "s"},
		{`{"val":{"name":"n"}}`, apiKindofObject{Name: "n"}},
		{`{"val":["a","b"]}`, []string{"a", "b"}},
	} {
		var h apiKindofHost
		if err := vjson.Unmarshal([]byte(tc.in), &h); err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if !reflect.DeepEqual(h.Val, tc.want) {
			t.Errorf("%s: Val = %#v, want %#v", tc.in, h.Val, tc.want)
		}
	}
	var h apiKindofHost
	if err := vjson.Unmarshal([]byte(`{"val":true}`), &h); err == nil {
		t.Errorf("a kind with no case decoded into %#v", h.Val)
	}
}

// The root aliases are the encoding/json types themselves, so values cross
// the two packages without conversion.
func TestAliases(t *testing.T) {
	for _, pair := range [][2]reflect.Type{
		{reflect.TypeFor[vjson.RawMessage](), reflect.TypeFor[json.RawMessage]()},
		{reflect.TypeFor[vjson.Number](), reflect.TypeFor[json.Number]()},
		{reflect.TypeFor[vjson.Marshaler](), reflect.TypeFor[json.Marshaler]()},
		{reflect.TypeFor[vjson.Unmarshaler](), reflect.TypeFor[json.Unmarshaler]()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%v is not %v", pair[0], pair[1])
		}
	}
}
