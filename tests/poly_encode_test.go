package tests

import (
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
