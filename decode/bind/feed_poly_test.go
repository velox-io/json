package bind

import (
	"reflect"
	"strings"
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Streaming coverage for poly output: variant, kindof, reserve-unknown, dual
// view, and nested Value content. The merged-tape phase 2 walk runs inside one
// native invocation; the surrounding field loop spans windows, so every key
// write waits until its value token is visible before consuming the key.

// feedVariantHost mixes a variant field, a Value field, and the discriminator.
type feedVariantHost struct {
	Name string      `json:"name"`
	Data any         `json:",embed" vjson:"variant=name"`
	Any  value.Value `json:"any"`
}

type feedVariantCase struct {
	Greet string `json:"greet"`
	Big   []int  `json:"big"`
}

func init() {
	vbind.DefineVariantCases[feedVariantHost, struct {
		_ feedVariantCase `case:"bob"`
	}]()
}

// feedKindofHost mixes a deferred poly field with ordinary typed fields.
type feedKindofHost struct {
	Name string `json:"name"`
	Data any    `json:"data" vjson:"kindof"`
}

type feedKindofUser struct {
	Role string `json:"role"`
}

func init() {
	vbind.DefineKindofCases[feedKindofHost, struct {
		bool   bool
		number float64
		string string
		array  []feedKindofUser
		object feedKindofUser
	}]()
}

// feedDualHost carries an inline variant and a reserve-unknown sink: one
// merged tape serves both consumers through two logical views.
type feedDualHost struct {
	Kind string      `json:"kind"`
	Case any         `json:",embed" vjson:"variant=kind"`
	Rest value.Value `json:",embed"`
}

type feedDualCase struct {
	Name string      `json:"name"`
	Age  int         `json:"age"`
	Note value.Value `json:"note"`
}

func init() {
	vbind.DefineVariantCases[feedDualHost, struct {
		_ feedDualCase `case:"c1"`
	}]()
}

// feedCaseValueHost selects a case whose content itself holds a Value field,
// exercising the tape alias published from case content.
type feedCaseValueHost struct {
	Shape string `json:"shape"`
	Data  any    `json:",embed" vjson:"variant=shape"`
}

type feedCaseValueCase struct {
	Payload value.Value `json:"payload"`
	N       int         `json:"n"`
}

func init() {
	vbind.DefineVariantCases[feedCaseValueHost, struct {
		_ feedCaseValueCase `case:"box"`
	}]()
}

func TestFeedPolyVariantSplitParity(t *testing.T) {
	// disc-before-value and disc-after-value orderings.
	docs := []string{
		`{"name":"bob","greet":"hello","big":[1,2,3],"any":{"k":[1]}}`,
		`{"greet":"hello","big":[1,2,3],"any":{"k":[1]},"name":"bob"}`,
		`{"any":{"k":[1]},"greet":"x","name":"bob","big":[9]}`,
	}
	for _, doc := range docs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[feedVariantHost](t, data)
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[feedVariantHost](t, data, chunk)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("chunk=%d doc=%q: err=%v contiguous=%v", chunk, doc, err, wantErr)
			}
			if err != nil {
				continue
			}
			if got.Name != want.Name || got.Any.String() != want.Any.String() {
				t.Fatalf("chunk=%d doc=%q: got %+v want %+v", chunk, doc, got, want)
			}
			gc, gok := got.Data.(feedVariantCase)
			wc, wok := want.Data.(feedVariantCase)
			if gok != wok {
				t.Fatalf("chunk=%d doc=%q: case type %T want %T", chunk, doc, got.Data, want.Data)
			}
			if gok && (gc.Greet != wc.Greet || len(gc.Big) != len(wc.Big)) {
				t.Fatalf("chunk=%d doc=%q: case got %+v want %+v", chunk, doc, gc, wc)
			}
		}
	}
}

func TestFeedPolyKindofSplitParity(t *testing.T) {
	docs := []string{
		`{"name":"n","data":true}`,
		`{"name":"n","data":42}`,
		`{"name":"n","data":"text"}`,
		`{"name":"n","data":[{"role":"a"},{"role":"b"}]}`,
		`{"name":"n","data":{"role":"r"}}`,
		`{"name":"n","data":{"role":"r"},"extra":1}`,
	}
	for _, doc := range docs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[feedKindofHost](t, data)
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[feedKindofHost](t, data, chunk)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("chunk=%d doc=%q: err=%v contiguous=%v", chunk, doc, err, wantErr)
			}
			if err != nil {
				continue
			}
			if got.Name != want.Name || !reflect.DeepEqual(got.Data, want.Data) {
				t.Fatalf("chunk=%d doc=%q: got %+v want %+v", chunk, doc, got, want)
			}
		}
	}
}

func TestFeedPolyDualViewSplitParity(t *testing.T) {
	docs := []string{
		`{"kind":"c1","name":"n","age":1}`,
		`{"kind":"c1","name":"n","age":2,"left1":{"deep":[1,2]},"left2":"s"}`,
		`{"kind":"c1","note":{"inner":[null,true]},"age":3,"name":"x","left":1}`,
		`{"kind":"other","anything":[1,2,3]}`,
	}
	for _, doc := range docs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[feedDualHost](t, data)
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[feedDualHost](t, data, chunk)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("chunk=%d doc=%q: err=%v contiguous=%v", chunk, doc, err, wantErr)
			}
			if err != nil {
				continue
			}
			if got.Rest.String() != want.Rest.String() {
				t.Fatalf("chunk=%d doc=%q: rest got %s want %s", chunk, doc, got.Rest.String(), want.Rest.String())
			}
			gc, gok := got.Case.(feedDualCase)
			wc, wok := want.Case.(feedDualCase)
			if gok != wok {
				t.Fatalf("chunk=%d doc=%q: case %T want %T", chunk, doc, got.Case, want.Case)
			}
			if gok && (gc.Name != wc.Name || gc.Age != wc.Age || gc.Note.String() != wc.Note.String()) {
				t.Fatalf("chunk=%d doc=%q: case got %+v want %+v", chunk, doc, gc, wc)
			}
		}
	}
}

func TestFeedPolyCaseValueContent(t *testing.T) {
	doc := `{"shape":"box","payload":{"deep":{"list":[1,{"x":null}],"s":"日"}},"n":7}`
	data := []byte(doc)
	want, wantErr := feedUnmarshal[feedCaseValueHost](t, data)
	if wantErr != nil {
		t.Fatalf("contiguous: %v", wantErr)
	}
	for _, chunk := range feedChunkSizes {
		got, err := feedRun[feedCaseValueHost](t, data, chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		gc, ok := got.Data.(feedCaseValueCase)
		if !ok {
			t.Fatalf("chunk=%d: case type %T", chunk, got.Data)
		}
		wc := want.Data.(feedCaseValueCase)
		if gc.N != wc.N || gc.Payload.String() != wc.Payload.String() {
			t.Fatalf("chunk=%d: got %+v want %+v", chunk, gc, wc)
		}
	}
}

// TestFeedPolyStaleDiscWithGrowth forces the string arena to grow after the
// discriminator is bound but before a later field consults it. A declared
// variant field binds its disc as a plain typed string early; the growth then
// leaves that string in a retired backing, and provenance must recognize it
// through the recorded generation interval instead of the current arena range.
type feedVarFieldHost struct {
	Name string `json:"name"`
	Data any    `json:"data" vjson:"variant=name"`
}

type feedVarFieldCase struct {
	N int `json:"n"`
}

func init() {
	vbind.DefineVariantCases[feedVarFieldHost, struct {
		_ feedVarFieldCase `case:"bob"`
	}]()
}

func TestFeedPolyStaleDiscWithGrowth(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"name":"bob","pad":"`)
	sb.WriteString(strings.Repeat("x", 24000))
	sb.WriteString(`","data":{"n":7}}`)
	data := []byte(sb.String())

	want, wantErr := feedUnmarshal[feedVarFieldHost](t, data)
	if wantErr != nil {
		t.Fatalf("contiguous: %v", wantErr)
	}
	for _, chunk := range []int{1, 64, 1024} {
		got, err := feedRun[feedVarFieldHost](t, data, chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		gc, ok := got.Data.(feedVarFieldCase)
		if !ok {
			t.Fatalf("chunk=%d: case type %T, want bound case (stale disc rejected)", chunk, got.Data)
		}
		if gc.N != 7 || got.Name != want.Name {
			t.Fatalf("chunk=%d: got %+v name=%q", chunk, gc, got.Name)
		}
	}
}

// TestFeedPolyDiscMissingAndUnknown covers the error families: a missing
// discriminator and an unmatched one must match the contiguous error.
func TestFeedPolyDiscErrors(t *testing.T) {
	docs := []string{
		`{"greet":"hello"}`,
		`{"name":"nobody","greet":"hello"}`,
		`{"name":"bob"`,
		`{"name":"bob",}`,
	}
	for _, doc := range docs {
		data := []byte(doc)
		_, wantErr := feedUnmarshal[feedVariantHost](t, data)
		for _, chunk := range feedChunkSizes {
			_, err := feedRun[feedVariantHost](t, data, chunk)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("chunk=%d doc=%q: err=%v contiguous=%v", chunk, doc, err, wantErr)
			}
			if err != nil && err.Error() != wantErr.Error() {
				t.Fatalf("chunk=%d doc=%q: err=%q contiguous=%q", chunk, doc, err, wantErr)
			}
		}
	}
}
