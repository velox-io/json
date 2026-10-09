package tests

import (
	"bytes"
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/vbind"
)

// Round trips through the root package API for the polymorphic hosts the
// decode side exercises: sibling variant, inline variant, and kindof. The
// encode side needs no variant tables: the stored concrete type drives the
// output, and inline fields unfold their case structs.

type polyUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type polyProduct struct {
	SKU   string   `json:"sku"`
	Price float64  `json:"price"`
	Tags  []string `json:"tags"`
}

type polyEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

type polyInlineHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

// polyRef holds a single pointer, so an interface stores it in its data
// word rather than behind a pointer.
type polyRef struct {
	P *int `json:"p"`
}

type polyRefHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
	N    int    `json:"n"`
}

// polyRich's fields leave the native ops for Go: a marshal method, a map
// with integer keys, and an interface.
type polyRich struct {
	Status polyStatus     `json:"status"`
	Counts map[int]string `json:"counts"`
	Extra  any            `json:"extra"`
}

type polyStatus int

func (s polyStatus) MarshalJSON() ([]byte, error) { return []byte(`"on"`), nil }

type polyRichHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
	N    int    `json:"n"`
}

type polyKindofEnvelope struct {
	Data any `json:"data" vjson:"kindof"`
}

func init() {
	vbind.DefineVariantCases[polyEnvelope, struct {
		_ polyUser    `case:"user"`
		_ polyProduct `case:"product"`
	}]()
	vbind.DefineVariantCases[polyInlineHost, struct {
		_ polyUser    `case:"user"`
		_ polyProduct `case:"product"`
	}]()
	vbind.DefineVariantCases[polyRefHost, struct {
		_ polyUser `case:"user"`
		_ polyRef  `case:"ref"`
	}]()
	vbind.DefineVariantCases[polyRichHost, struct {
		_ polyRich `case:"rich"`
	}]()
	vbind.DefineKindofCases[polyKindofEnvelope, struct {
		bool   bool
		number float64
		string string
		array  []polyUser
		object polyUser
	}]()
}

func TestPolyEncodeRoundTrip(t *testing.T) {
	siblingCases := []string{
		`{"type":"user","data":{"id":1,"name":"ann"}}`,
		`{"type":"product","data":{"sku":"s","price":1.5,"tags":["a"]}}`,
	}
	for _, in := range siblingCases {
		var env polyEnvelope
		if err := vjson.Unmarshal([]byte(in), &env); err != nil {
			t.Fatalf("sibling Unmarshal(%s): %v", in, err)
		}
		got, err := vjson.Marshal(env)
		if err != nil {
			t.Fatalf("sibling Marshal: %v", err)
		}
		if string(got) != in {
			t.Errorf("sibling round trip: got %s, want %s", got, in)
		}
	}

	inlineCases := []string{
		`{"type":"user","id":1,"name":"ann"}`,
		`{"type":"product","sku":"s","price":1.5,"tags":["a"]}`,
	}
	for _, in := range inlineCases {
		var env polyInlineHost
		if err := vjson.Unmarshal([]byte(in), &env); err != nil {
			t.Fatalf("inline Unmarshal(%s): %v", in, err)
		}
		got, err := vjson.Marshal(env)
		if err != nil {
			t.Fatalf("inline Marshal: %v", err)
		}
		if string(got) != in {
			t.Errorf("inline round trip: got %s, want %s", got, in)
		}
	}
}

// An inline case unfolds the same whether the interface holds it boxed,
// behind a pointer, or in its data word.
func TestPolyEncodeInlineCaseStorage(t *testing.T) {
	x := 7
	for _, c := range []struct {
		name string
		typ  string
		data any
		want string
	}{
		{"boxed", "user", polyUser{ID: 1, Name: "a"}, `{"type":"user","id":1,"name":"a","n":2}`},
		{"pointer", "user", &polyUser{ID: 1, Name: "a"}, `{"type":"user","id":1,"name":"a","n":2}`},
		{"data word", "ref", polyRef{P: &x}, `{"type":"ref","p":7,"n":2}`},
		{"data word nil field", "ref", polyRef{}, `{"type":"ref","p":null,"n":2}`},
		{"pointer to data-word type", "ref", &polyRef{P: &x}, `{"type":"ref","p":7,"n":2}`},
	} {
		h := polyRefHost{Type: c.typ, Data: c.data, N: 2}
		check := func(leg string, got []byte, err error, want string) {
			t.Helper()
			if err != nil {
				t.Errorf("%s/%s: %v", c.name, leg, err)
			} else if string(got) != want {
				t.Errorf("%s/%s: got %s, want %s", c.name, leg, got, want)
			}
		}
		got, err := vjson.Marshal(h)
		check("Marshal", got, err, c.want)
		got, err = vjson.Marshal([]polyRefHost{h, h})
		check("slice", got, err, "["+c.want+","+c.want+"]")
		var ind bytes.Buffer
		if ierr := json.Indent(&ind, []byte(c.want), "", "  "); ierr != nil {
			t.Fatal(ierr)
		}
		got, err = vjson.MarshalIndent(h, "", "  ")
		check("MarshalIndent", got, err, ind.String())
	}
}

// An inline case unfolds the struct at the end of a pointer chain, and a nil
// anywhere along the chain holds no case: nothing is written and the host's
// fields stay put, the same as a nil interface.
func TestPolyEncodeInlineCasePointerChain(t *testing.T) {
	u := &polyUser{ID: 1, Name: "a"}
	var nu *polyUser
	const full = `{"type":"user","id":1,"name":"a","n":2}`
	const none = `{"type":"user","n":2}`
	// A non-nil *polyUser ahead of the case in the same call caches its
	// type's body, so a typed nil of that type unfolds from a cache hit.
	prime := polyRefHost{Type: "user", Data: u, N: 2}
	for _, c := range []struct {
		name string
		data any
		want string
	}{
		{"nil interface", nil, none},
		{"typed nil pointer", (*polyUser)(nil), none},
		{"typed nil non-struct pointer", (*int)(nil), none},
		{"pointer chain", &u, full},
		{"pointer chain inner nil", &nu, none},
		{"typed nil pointer chain", (**polyUser)(nil), none},
	} {
		h := polyRefHost{Type: "user", Data: c.data, N: 2}
		check := func(leg string, got []byte, err error, want string) {
			t.Helper()
			if err != nil {
				t.Errorf("%s/%s: %v", c.name, leg, err)
			} else if string(got) != want {
				t.Errorf("%s/%s: got %s, want %s", c.name, leg, got, want)
			}
		}
		got, err := vjson.Marshal(h)
		check("Marshal", got, err, c.want)
		got, err = vjson.Marshal([]polyRefHost{prime, h, h})
		check("after cached", got, err, "["+full+","+c.want+","+c.want+"]")
		var ind bytes.Buffer
		if ierr := json.Indent(&ind, []byte(c.want), "", "  "); ierr != nil {
			t.Fatal(ierr)
		}
		got, err = vjson.MarshalIndent(h, "", "  ")
		check("MarshalIndent", got, err, ind.String())
	}
}

// An inline case whose fields encode in Go unfolds like any other, and the
// host's fields after it continue.
func TestPolyEncodeInlineCaseGoFields(t *testing.T) {
	const want = `{"type":"rich","status":"on","counts":{"1":"a"},"extra":[true],"n":2}`
	var ind bytes.Buffer
	if err := json.Indent(&ind, []byte(want), "", "  "); err != nil {
		t.Fatal(err)
	}
	for _, data := range []any{
		polyRich{Status: 1, Counts: map[int]string{1: "a"}, Extra: []any{true}},
		&polyRich{Status: 1, Counts: map[int]string{1: "a"}, Extra: []any{true}},
	} {
		h := polyRichHost{Type: "rich", Data: data, N: 2}
		if got, err := vjson.Marshal(h); err != nil || string(got) != want {
			t.Errorf("%T: Marshal = %s, %v; want %s", data, got, err, want)
		}
		if got, err := vjson.MarshalIndent(h, "", "  "); err != nil || string(got) != ind.String() {
			t.Errorf("%T: MarshalIndent = %s, %v; want %s", data, got, err, ind.String())
		}
	}
}

// kindof fields encode naturally through concrete-type dispatch; no
// machinery on the encode side, and the round trip is byte identical.
func TestPolyEncodeKindofRoundTrip(t *testing.T) {
	cases := []string{
		`{"data":true}`,
		`{"data":42}`,
		`{"data":"s"}`,
		`{"data":[{"id":1,"name":"a"}]}`,
		`{"data":{"id":1,"name":"a"}}`,
	}
	for _, in := range cases {
		var env polyKindofEnvelope
		if err := vjson.Unmarshal([]byte(in), &env); err != nil {
			t.Fatalf("kindof Unmarshal(%s): %v", in, err)
		}
		got, err := vjson.Marshal(env)
		if err != nil {
			t.Fatalf("kindof Marshal: %v", err)
		}
		if string(got) != in {
			t.Errorf("kindof round trip: got %s, want %s", got, in)
		}
	}
}

// A K8sObject-shaped host combines an inline variant, a sibling variant,
// and ordinary fields: the axes must compose in one round trip.
func TestPolyEncodeMixedAxes(t *testing.T) {
	type k8sObject struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
		Object     any    `json:",embed" vjson:"variant=kind"`
		Observer   string `json:"observer"`
		Report     any    `json:"report" vjson:"variant=observer"`
	}
	type podSpec struct {
		Replicas int    `json:"replicas"`
		Image    string `json:"image"`
	}
	type svcSpec struct {
		Port int    `json:"port"`
		Name string `json:"name"`
	}
	vbind.DefineVariantCasesAt[k8sObject, struct {
		_ podSpec `case:"Pod"`
		_ svcSpec `case:"Service"`
	}]("Object")
	vbind.DefineVariantCasesAt[k8sObject, struct {
		_ svcSpec `case:"ops"`
	}]("Report")

	in := `{"kind":"Pod","apiVersion":"v1","replicas":2,"image":"nginx:1","observer":"ops","report":{"port":80,"name":"web"}}`
	var h k8sObject
	if err := vjson.Unmarshal([]byte(in), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, err := vjson.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != in {
		t.Errorf("mixed axes round trip: got %s, want %s", got, in)
	}
}
