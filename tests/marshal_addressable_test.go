package tests

// Pointer-receiver marshal methods run on every value, as in json v2. Where
// the value is addressable (behind a pointer, a slice element, the encoder's
// own copy of a root) the method runs on it in place. Where it is not (an
// interface payload, a map key or value) the method runs on a copy, so what
// it writes reaches neither the caller's storage, nor storage an interface
// shares with its copies, nor memory the runtime keeps for zero and small
// values. encoding/json under its v1 defaults skips such methods instead, so
// these tests check the properties directly rather than against it.

import (
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/vbind"
)

// adMut's MarshalJSON writes its receiver, as a method caching its output
// would.
type adMut struct{ N int }

func (m *adMut) MarshalJSON() ([]byte, error) {
	m.N = -1
	return []byte(`"m"`), nil
}

// adByte is one byte wide: an interface holding a small one points into the
// runtime's table of small integers.
type adByte uint8

func (b *adByte) MarshalJSON() ([]byte, error) {
	*b = 99
	return []byte(`"b"`), nil
}

// adKey writes its receiver from MarshalText.
type adKey struct{ S string }

func (k *adKey) MarshalText() ([]byte, error) {
	out := "k:" + k.S
	k.S = "written"
	return []byte(out), nil
}

// adZero writes its receiver from IsZero.
type adZero struct{ N int }

func (z *adZero) IsZero() bool {
	z.N = -1
	return false
}

type adHost struct {
	M adMut  `json:"m"`
	Z adZero `json:"z,omitzero"`
}

// adOmit's only method is its field's IsZero.
type adOmit struct {
	Z adZero `json:"z,omitzero"`
	N int    `json:"n"`
}

type adNested struct {
	H   adHost   `json:"h"`
	Arr [2]adMut `json:"arr"`
	P   *adMut   `json:"p"`
	S   []adMut  `json:"s"`
	Box any      `json:"box"`
	Opt adZero   `json:"opt,omitzero"`
}

type adAnyHost struct {
	I any   `json:"i"`
	S []any `json:"s"`
}

type adCaseHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[adCaseHost, struct {
		_ adHost `case:"host"`
	}]()
}

//go:noinline
func adN(n int) int { return n }

func adMarshal(t *testing.T, name string, v any, want string) {
	t.Helper()
	got, err := vjson.Marshal(v)
	if err != nil {
		t.Errorf("%s: Marshal: %v", name, err)
		return
	}
	if string(got) != want {
		t.Errorf("%s: Marshal = %s, want %s", name, got, want)
	}
	var ind strings.Builder
	enc := vjson.NewEncoder(&ind)
	if err := enc.Encode(v); err != nil {
		t.Errorf("%s: Encoder: %v", name, err)
	} else if strings.TrimSuffix(ind.String(), "\n") != want {
		t.Errorf("%s: Encoder = %s, want %s", name, ind.String(), want)
	}
}

// An interface payload is not addressable: the method runs on a copy, and
// every copy of the interface keeps the value.
func TestMarshalAddressable_InterfacePayload(t *testing.T) {
	// IsZero writes the copy the field then encodes from.
	const hostOut = `{"m":"m","z":{"N":-1}}`
	for _, c := range []struct {
		name string
		wrap func(a any) any
		want func(inner string) string
	}{
		{"root", func(a any) any { return a }, func(s string) string { return s }},
		{"field", func(a any) any { return adAnyHost{I: a} }, func(s string) string { return `{"i":` + s + `,"s":null}` }},
		{"slice field", func(a any) any { return adAnyHost{S: []any{a, a}} }, func(s string) string { return `{"i":null,"s":[` + s + `,` + s + `]}` }},
		{"slice", func(a any) any { return []any{a} }, func(s string) string { return `[` + s + `]` }},
		{"map", func(a any) any { return map[string]any{"k": a} }, func(s string) string { return `{"k":` + s + `}` }},
		{"pointer to field host", func(a any) any { return &adAnyHost{I: a} }, func(s string) string { return `{"i":` + s + `,"s":null}` }},
	} {
		mut := any(adMut{N: adN(1000)})
		adMarshal(t, c.name+"/hooked", c.wrap(mut), c.want(`"m"`))
		if got := mut.(adMut).N; got != 1000 {
			t.Errorf("%s/hooked: the interface's payload now holds N=%d", c.name, got)
		}

		host := any(adHost{M: adMut{N: adN(5)}, Z: adZero{N: adN(7)}})
		adMarshal(t, c.name+"/struct", c.wrap(host), c.want(hostOut))
		if got := host.(adHost); got.M.N != 5 || got.Z.N != 7 {
			t.Errorf("%s/struct: the interface's payload now holds %+v", c.name, got)
		}

		omit := any(adOmit{Z: adZero{N: adN(8)}, N: 1})
		adMarshal(t, c.name+"/omitzero", c.wrap(omit), c.want(`{"z":{"N":-1},"n":1}`))
		if got := omit.(adOmit); got.Z.N != 8 {
			t.Errorf("%s/omitzero: the interface's payload now holds %+v", c.name, got)
		}

		arr := any([2]adMut{{N: adN(3)}, {N: adN(4)}})
		adMarshal(t, c.name+"/array", c.wrap(arr), c.want(`["m","m"]`))
		if got := arr.([2]adMut); got[0].N != 3 || got[1].N != 4 {
			t.Errorf("%s/array: the interface's payload now holds %+v", c.name, got)
		}
	}
}

// A zero value or a small integer in an interface points at memory the
// runtime shares process-wide; a method writing its receiver must not reach
// it.
func TestMarshalAddressable_RuntimeSharedPayload(t *testing.T) {
	var missing map[string]adMut
	adMarshal(t, "zero struct", adAnyHost{I: adMut{}}, `{"i":"m","s":null}`)
	adMarshal(t, "zero struct in slice", []any{adMut{}}, `["m"]`)
	if got := missing["absent"].N; got != 0 {
		t.Errorf("a missing map key now reads N=%d: the runtime's zero value was written", got)
	}

	adMarshal(t, "small integer", adAnyHost{I: adByte(adN(2))}, `{"i":"b","s":null}`)
	if got := any(uint8(adN(2))).(uint8); got != 2 {
		t.Errorf("any(uint8(2)) now reads %d: the runtime's small integer table was written", got)
	}

	// A constant composite literal boxes into read-only data.
	adMarshal(t, "constant literal", adAnyHost{I: adMut{N: 1}}, `{"i":"m","s":null}`)
	adMarshal(t, "constant literal in slice", []any{adMut{N: 1}}, `["m"]`)
}

// A map entry is not addressable: methods run on a copy, and the map keeps
// its keys and values.
func TestMarshalAddressable_MapEntries(t *testing.T) {
	vals := map[string]adMut{"k": {N: 5}}
	adMarshal(t, "value", vals, `{"k":"m"}`)
	adMarshal(t, "value in field", struct {
		M map[string]adMut `json:"m"`
	}{vals}, `{"m":{"k":"m"}}`)
	if got := vals["k"].N; got != 5 {
		t.Errorf("value: the map now holds N=%d", got)
	}

	ints := map[int]adMut{1: {N: 6}}
	adMarshal(t, "value under int key", ints, `{"1":"m"}`)
	if got := ints[1].N; got != 6 {
		t.Errorf("value under int key: the map now holds N=%d", got)
	}

	hosts := map[string]adHost{"k": {M: adMut{N: 2}, Z: adZero{N: 3}}}
	adMarshal(t, "struct value", hosts, `{"k":{"m":"m","z":{"N":-1}}}`)
	if got := hosts["k"]; got.M.N != 2 || got.Z.N != 3 {
		t.Errorf("struct value: the map now holds %+v", got)
	}

	omits := map[string]adOmit{"k": {Z: adZero{N: 4}, N: 1}}
	adMarshal(t, "omitzero value", omits, `{"k":{"z":{"N":-1},"n":1}}`)
	if got := omits["k"]; got.Z.N != 4 {
		t.Errorf("omitzero value: the map now holds %+v", got)
	}

	arrs := map[string][2]adMut{"k": {{N: 8}, {N: 9}}}
	adMarshal(t, "array value", arrs, `{"k":["m","m"]}`)
	if got := arrs["k"]; got[0].N != 8 || got[1].N != 9 {
		t.Errorf("array value: the map now holds %+v", got)
	}

	keys := map[adKey]int{{S: "a"}: 1}
	adMarshal(t, "text key", keys, `{"k:a":1}`)
	if _, ok := keys[adKey{S: "a"}]; !ok || len(keys) != 1 {
		t.Errorf("text key: the map's key was written: %+v", keys)
	}
}

// An inline variant case held by value is an interface payload too.
func TestMarshalAddressable_InlineCase(t *testing.T) {
	data := any(adHost{M: adMut{N: adN(4)}, Z: adZero{N: adN(6)}})
	h := adCaseHost{Type: "host", Data: data}
	adMarshal(t, "value case", h, `{"type":"host","m":"m","z":{"N":-1}}`)
	adMarshal(t, "value case in slice", []adCaseHost{h}, `[{"type":"host","m":"m","z":{"N":-1}}]`)
	if got := data.(adHost); got.M.N != 4 || got.Z.N != 6 {
		t.Errorf("value case: the interface's payload now holds %+v", got)
	}
}

// An addressable value runs the method in place, as encoding/json does; a
// root passed by value is the encoder's own copy.
func TestMarshalAddressable_InPlace(t *testing.T) {
	elems := []adMut{{N: 1}, {N: 2}}
	adMarshal(t, "slice elements", elems, `["m","m"]`)
	if elems[0].N != -1 || elems[1].N != -1 {
		t.Errorf("slice elements: the method did not run in place: %+v", elems)
	}

	n := &adNested{H: adHost{M: adMut{N: 1}, Z: adZero{N: 2}}, P: &adMut{N: 3}}
	adMarshal(t, "through a pointer", n,
		`{"h":{"m":"m","z":{"N":-1}},"arr":["m","m"],"p":"m","s":null,"box":null,"opt":{"N":-1}}`)
	if n.H.M.N != -1 || n.H.Z.N != -1 || n.Arr[0].N != -1 || n.P.N != -1 {
		t.Errorf("through a pointer: the method did not run in place: %+v", n)
	}

	root := adHost{M: adMut{N: 1}, Z: adZero{N: 2}}
	if got, err := vjson.Marshal(root); err != nil || string(got) != `{"m":"m","z":{"N":-1}}` {
		t.Errorf("root by value: Marshal = %s, %v", got, err)
	}
	if root.M.N != 1 || root.Z.N != 2 {
		t.Errorf("root by value: the caller's value now holds %+v", root)
	}
}
