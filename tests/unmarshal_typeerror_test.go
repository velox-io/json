package tests

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/decode/bind"
)

// UnmarshalTypeError must carry the same context fields encoding/json
// reports: Type is the leaf destination that rejected the value, Struct is
// the root type's name, Field is the JSON pointer with separators turned
// into dots, Offset is one past the offending token, and Value names the
// value's kind. Every case compares against stdlib as ground truth.

type uteCompare struct {
	name string
	in   string
	std  func() any
	vj   func() any
}

// assertUTEEqual decodes in with both libraries into equal fresh values and
// fails when the two UnmarshalTypeError payloads differ.
func assertUTEEqual(t *testing.T, tt uteCompare) {
	t.Helper()
	want := tt.std()
	wantErr := json.Unmarshal([]byte(tt.in), want)
	var wute *json.UnmarshalTypeError
	wCtx := "<no UTE>"
	if errors.As(wantErr, &wute) {
		wCtx = wute.Error()
	}
	got := tt.vj()
	gotErr := vjson.Unmarshal([]byte(tt.in), got)
	var gute *json.UnmarshalTypeError
	gCtx := "<no UTE>"
	if errors.As(gotErr, &gute) {
		gCtx = gute.Error()
	}
	// The bridge copies only the fields the stdlib type carried before
	// go1.27, so a stdlib that renders a wrapped Err suffix is allowed to
	// run longer; anything before that suffix must match exactly.
	if wCtx != gCtx && !strings.HasPrefix(wCtx, gCtx+": ") {
		t.Errorf("%s: message\n  stdlib: %s\n  vjson:  %s", tt.name, wCtx, gCtx)
		return
	}
	if wute != nil && gute != nil {
		if wute.Type != gute.Type {
			t.Errorf("%s: Type\n  stdlib: %v\n  vjson:  %v", tt.name, wute.Type, gute.Type)
		}
		if wute.Struct != gute.Struct || wute.Field != gute.Field {
			t.Errorf("%s: context\n  stdlib: %q.%q\n  vjson:  %q.%q", tt.name, wute.Struct, wute.Field, gute.Struct, gute.Field)
		}
		if wute.Value != gute.Value {
			t.Errorf("%s: Value\n  stdlib: %q\n  vjson:  %q", tt.name, wute.Value, gute.Value)
		}
		if wute.Offset != gute.Offset {
			t.Errorf("%s: Offset\n  stdlib: %d\n  vjson:  %d", tt.name, wute.Offset, gute.Offset)
		}
	}
}

func TestUnmarshalTypeErrorContext(t *testing.T) {
	if !stdJSONv2Context() {
		t.Skip("encoding/json predates the go1.27 UnmarshalTypeError context this test pins")
	}
	type direct struct {
		R int64 `json:",string"`
		G int64
	}
	type wrap struct {
		D direct
		S []int64
		M map[string]int64
	}
	type deep struct {
		B wrap
	}
	type embed3 struct {
		Q int64 `json:",string"`
	}
	type S5 struct {
		*embed3
		R int
	}
	type arrOf struct {
		A []direct
	}
	type boolStr struct {
		B bool `json:",string"`
	}

	pi := func() any { return new(int) }
	d1 := func() any { return &direct{} }
	w1 := func() any { return &wrap{} }
	dp := func() any { return &deep{} }
	s5 := func() any { return &S5{embed3: &embed3{}} }
	ao := func() any { return &arrOf{} }
	bs := func() any { return &boolStr{} }
	rm := func() any { return map[string]direct{} }
	rs := func() any { return &[]direct{} }
	pr := func() any { return new(*wrap) }
	pa := func() any {
		v := struct {
			M map[string]direct
		}{}
		return &v
	}

	for _, tt := range []uteCompare{
		{"root string into int", `"x"`, pi, pi},
		{"root object into int", `{"R":1}`, pi, pi},
		{"root array into int", `[1]`, pi, pi},
		{"root bool into int", `true`, pi, pi},
		{"direct ,string unquoted number", `{"R":7}`, d1, d1},
		{"direct ,string unquoted bool", `{"R":true}`, d1, d1},
		{"direct ,string quoted bad", `{"R":"x"}`, d1, d1},
		{"direct string into int", `{"G":"x"}`, d1, d1},
		{"direct bool into int", `{"G":true}`, d1, d1},
		{"direct after ok sibling", `{"G":1,"R":7}`, d1, d1},
		{"nested struct field", `{"D":{"R":7}}`, w1, w1},
		{"two levels deep", `{"B":{"D":{"R":7}}}`, dp, dp},
		{"array element", `{"S":[true]}`, w1, w1},
		{"array element after ok", `{"S":[1,true]}`, w1, w1},
		{"map value", `{"M":{"k":true}}`, w1, w1},
		{"array of structs", `{"A":[{"G":1},{"R":7}]}`, ao, ao},
		{"promoted via embedded ptr", `{"Q":1}`, s5, s5},
		{"promoted sibling ok first", `{"R":2,"Q":1}`, s5, s5},
		{"bool ,string quoted bad", `{"B":"x"}`, bs, bs},
		{"root map of structs", `{"m":{"R":7}}`, rm, rm},
		{"root slice of structs", `[{"R":7}]`, rs, rs},
		{"pointer to struct root", `{"D":{"R":7}}`, pr, pr},
		{"struct in map field", `{"M":{"key":{"R":7}}}`, pa, pa},
	} {
		assertUTEEqual(t, tt)
	}
}

// stdJSONv2Context reports whether the running encoding/json reports a
// ,string violation as UnmarshalTypeError with element and key positions in
// Field, which arrived in go1.27. Earlier toolchains answer with a plain
// invalid-use error and container-only paths, so their stdlib cannot serve
// as the reference for this context.
func stdJSONv2Context() bool {
	type direct struct {
		R int64 `json:",string"`
	}
	var d direct
	var ute *json.UnmarshalTypeError
	return errors.As(json.Unmarshal([]byte(`{"R":7}`), &d), &ute)
}

// The stream decoder must report the same value identity the contiguous path
// does. The comparison runs through the errors.As bridge because velox's own
// rendering carries the vjson prefix by design.
//
// The native streaming engine binds incrementally over sliding windows, so
// unlike the contiguous and Go-engine paths it cannot rebuild the member
// path from source bytes: Struct and Field stay empty there. Value, Type,
// and Offset are engine-independent facts recorded at the mismatch site.
func TestUnmarshalTypeErrorContextDecoder(t *testing.T) {
	if !stdJSONv2Context() {
		t.Skip("encoding/json predates the go1.27 UnmarshalTypeError context this test pins")
	}
	type direct struct {
		R int64 `json:",string"`
	}
	in := `{"R":7}`
	want := &direct{}
	wantErr := json.NewDecoder(strings.NewReader(in)).Decode(want)
	got := &direct{}
	gotErr := vjson.NewDecoder(strings.NewReader(in)).Decode(got)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("err mismatch: stdlib %v, vjson %v", wantErr, gotErr)
	}
	var wute, gute *json.UnmarshalTypeError
	if !errors.As(wantErr, &wute) || !errors.As(gotErr, &gute) {
		t.Fatalf("not a UnmarshalTypeError: stdlib %v, vjson %v", wantErr, gotErr)
	}
	if wute.Type != gute.Type || wute.Value != gute.Value || wute.Offset != gute.Offset {
		t.Errorf("identity\n  stdlib: %+v\n  vjson:  %+v", *wute, *gute)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("value\n  stdlib: %+v\n  vjson:  %+v", want, got)
	}
}

// Element and map-value sites abort rather than record, so the streaming
// entries, which cannot rebuild the mismatch site, depend on the abort
// itself naming the leaf destination. Both Decoder and UnmarshalFeed run
// over a one-byte reader so every token crosses a window edge.
func TestUnmarshalTypeErrorContextStreamingElem(t *testing.T) {
	if !stdJSONv2Context() {
		t.Skip("encoding/json predates the go1.27 UnmarshalTypeError context this test pins")
	}
	type direct struct {
		R int64
	}
	type elems struct {
		S []int64
		A [2]int64
		M map[string]int64
		P []*int64
		D []direct
	}
	e1 := func() any { return &elems{} }
	rs := func() any { return &[]int64{} }
	rm := func() any { return &map[string]bool{} }
	for _, tt := range []uteCompare{
		{"slice element", `{"S":[1,"x",2]}`, e1, e1},
		{"slice element object", `{"S":[1,{"k":[1]}]}`, e1, e1},
		{"fixed array element", `{"A":[true]}`, e1, e1},
		{"map value", `{"M":{"p":"x"}}`, e1, e1},
		{"pointer element", `{"P":["x"]}`, e1, e1},
		{"struct element", `{"D":["x"]}`, e1, e1},
		{"root slice element", `[1,[2]]`, rs, rs},
		{"root map value", `{"k":1}`, rm, rm},
	} {
		want := tt.std()
		wantErr := json.Unmarshal([]byte(tt.in), want)
		var wute *json.UnmarshalTypeError
		if !errors.As(wantErr, &wute) {
			t.Fatalf("%s: stdlib err %v is not a UnmarshalTypeError", tt.name, wantErr)
		}
		for _, entry := range []struct {
			name   string
			decode func(dst any) error
		}{
			{"Decoder", func(dst any) error {
				return vjson.NewDecoder(iotest.OneByteReader(strings.NewReader(tt.in))).Decode(dst)
			}},
			{"UnmarshalFeed", func(dst any) error {
				p, err := bind.NewParserForType(reflect.TypeOf(dst).Elem())
				if err != nil {
					return err
				}
				return p.UnmarshalFeed(iotest.OneByteReader(strings.NewReader(tt.in)), dst)
			}},
		} {
			got := tt.vj()
			gotErr := entry.decode(got)
			var gute *json.UnmarshalTypeError
			if !errors.As(gotErr, &gute) {
				t.Errorf("%s %s: err %v is not a UnmarshalTypeError", entry.name, tt.name, gotErr)
				continue
			}
			if wute.Type != gute.Type || wute.Value != gute.Value || wute.Offset != gute.Offset {
				t.Errorf("%s %s: identity\n  stdlib: Type=%v Value=%q Offset=%d\n  vjson:  Type=%v Value=%q Offset=%d",
					entry.name, tt.name, wute.Type, wute.Value, wute.Offset, gute.Type, gute.Value, gute.Offset)
			}
		}
	}
}

// An unknown member rejected inside a slice element ends the decode with
// that element partly bound and counted, as encoding/json leaves it under
// DisallowUnknownFields. The unknown member closes each document, so the
// stdlib, which keeps going after it, has nothing left to bind.
func TestUnmarshal_RejectUnknownKeepsOpenSliceElement(t *testing.T) {
	type elem struct {
		B int
		C string
	}
	type doc struct {
		S  []elem
		P  []*elem
		SS [][]elem
	}
	for _, in := range []string{
		`{"S":[{"B":1},{"C":"y","a":1}]}`,
		`{"P":[{"B":1},{"C":"y","a":1}]}`,
		`{"SS":[[{"B":1}],[{"B":2},{"C":"y","a":1}]]}`,
	} {
		var want doc
		dec := json.NewDecoder(strings.NewReader(in))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&want); err == nil {
			t.Fatalf("%s: encoding/json accepted the unknown member", in)
		}
		var got doc
		if err := vjson.Unmarshal([]byte(in), &got, vjson.RejectUnknownMembers(true)); err == nil {
			t.Errorf("%s: vjson accepted the unknown member", in)
			continue
		}
		wj, _ := json.Marshal(want)
		gj, _ := json.Marshal(got)
		if string(wj) != string(gj) {
			t.Errorf("%s: destination divergence:\n  std:   %s\n  vjson: %s", in, wj, gj)
		}
	}
}
