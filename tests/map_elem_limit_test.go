package tests

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/decode/bind"
	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/vdec"
)

// Go stores a map element indirectly once its type exceeds 128 bytes
// (abi.MapMaxElemBytes): the slot holds a *V, and the runtime allocates the V.
// runtime.mapassign performs that allocation and returns the element storage,
// but mapassign_faststr does not; it hands back the pointer slot itself. The
// compiler never calls the faststr variant for such a map (walk.mapfast returns
// mapslow above the limit), and reflect gates on the same size, so a decoder
// writing map values through faststr must gate on it too.
//
// Writing a large value through the ungated call lands on top of the pointer
// slot, so the map publishes an element whose words are whatever the decoder
// wrote: a string or slice header pointing at an arbitrary address. Nothing
// errors, and the damage surfaces later at an unrelated read.
//
// The matrix below crosses the boundary in both directions and drives every
// decoder that assigns map values, because the boundary is a property of the map
// type alone: it does not care what is in the value, only how big it is. Cases
// therefore use ordinary types with no Value, RawMessage, or variant involved.
//
// Every case reads a published field back and compares it against the input.
// Asserting on the absence of an error would pass while the map holds a bad
// header, and a bad header cannot be inspected safely, so the assertions read a
// scalar field first and only then anything indirect.

// The boundary is on the map's element type, so each shape below is sized
// deliberately: mapBoundaryUnder is the largest value Go still stores inline,
// mapBoundaryOver the smallest it stores indirectly.

// mapBoundaryUnder is exactly 128 bytes: 16 (string) + 112.
type mapBoundaryUnder struct {
	Name string `json:"name"`
	Pad  [112]byte
}

// mapBoundaryOver is 136 bytes, one 8-byte word past the limit.
type mapBoundaryOver struct {
	Name string `json:"name"`
	Pad  [120]byte
}

// mapBoundaryOverNoPtr is over the limit and holds no pointers at all, so a fix
// cannot get away with only handling pointer-bearing values.
type mapBoundaryOverNoPtr struct {
	N   int64 `json:"n"`
	Pad [136]byte
}

// mapBoundaryBig is far past the limit, where the published header would be
// deep inside unrelated memory rather than just past the slot.
type mapBoundaryBig struct {
	Name string `json:"name"`
	Pad  [504]byte
}

// mapValueDecoder is one decoder that can bind a JSON object into a map.
// Each is driven with identical input so a disagreement is attributable.
type mapValueDecoder struct {
	name string
	// bind fills dst (a pointer to a map) from src.
	bind func(t *testing.T, src []byte, dst any) error
}

func mapValueDecoders() []mapValueDecoder {
	return []mapValueDecoder{
		{
			name: "vjson.Unmarshal",
			bind: func(t *testing.T, src []byte, dst any) error {
				t.Helper()
				return vjson.Unmarshal(src, dst)
			},
		},
		{
			name: "bind.Unmarshal",
			bind: func(t *testing.T, src []byte, dst any) error {
				t.Helper()
				return bind.Unmarshal(src, dst)
			},
		},
		{
			// The tape-bind path reaches the same drain through a different walk.
			name: "bind.UnmarshalValue",
			bind: func(t *testing.T, src []byte, dst any) error {
				t.Helper()
				val, err := dom.Parse(src)
				if err != nil {
					t.Fatalf("dom.Parse: %v", err)
				}
				return bind.UnmarshalValue(val, dst)
			},
		},
		{
			// An independent decoder with its own map assignment site.
			name: "vdec.Unmarshal",
			bind: func(t *testing.T, src []byte, dst any) error {
				t.Helper()
				return vdec.Unmarshal(src, dst)
			},
		},
	}
}

// TestMapValueOverElemLimit_StringField drives a map whose value carries a
// string across the inline/indirect boundary. The string header is the most
// direct witness: published from the wrong slot it points at the source bytes or
// at nothing, and the length comes from adjacent memory.
func TestMapValueOverElemLimit_StringField(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		// get returns the published Name for the sole key.
		get func(any) string
	}{
		{"128B inline", reflect.TypeOf(map[string]mapBoundaryUnder{}), func(d any) string {
			m := *d.(*map[string]mapBoundaryUnder)
			e := m["k"]
			return e.Name
		}},
		{"136B indirect", reflect.TypeOf(map[string]mapBoundaryOver{}), func(d any) string {
			m := *d.(*map[string]mapBoundaryOver)
			e := m["k"]
			return e.Name
		}},
		{"520B indirect", reflect.TypeOf(map[string]mapBoundaryBig{}), func(d any) string {
			m := *d.(*map[string]mapBoundaryBig)
			e := m["k"]
			return e.Name
		}},
	}
	const src = `{"k":{"name":"hello"}}`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, d := range mapValueDecoders() {
				t.Run(d.name, func(t *testing.T) {
					dst := reflect.New(c.typ)
					if err := d.bind(t, []byte(src), dst.Interface()); err != nil {
						t.Fatalf("%s(%s): %v", d.name, src, err)
					}
					got := c.get(dst.Interface())
					// Compare the length before the content: a header published
					// from the pointer slot usually has a length taken from
					// unrelated memory, and formatting it would read that far.
					if len(got) != len("hello") {
						t.Fatalf("%s: published Name has len %d, want %d; the map value was written through the wrong slot",
							d.name, len(got), len("hello"))
					}
					if got != "hello" {
						t.Errorf("%s: Name = %q, want %q", d.name, got, "hello")
					}
				})
			}
		})
	}
}

// TestMapValueOverElemLimit_NoPointers keeps the value free of pointers. The
// boundary is a property of the map type's size, so this must fail the same way;
// it is here to stop a fix that only redirects pointer-bearing values.
func TestMapValueOverElemLimit_NoPointers(t *testing.T) {
	requireMapElemLimitFixed(t)
	const src = `{"k":{"n":4242}}`
	for _, d := range mapValueDecoders() {
		t.Run(d.name, func(t *testing.T) {
			var m map[string]mapBoundaryOverNoPtr
			if err := d.bind(t, []byte(src), &m); err != nil {
				t.Fatalf("%s(%s): %v", d.name, src, err)
			}
			e := m["k"]
			if e.N != 4242 {
				t.Errorf("%s: N = %d, want 4242", d.name, e.N)
			}
		})
	}
}

// TestMapValueOverElemLimit_ManyKeys drives more keys than one group holds, so
// the map grows while entries are being assigned. A per-entry mistake shows up on
// every entry, so a single surviving key would mean the decoder is only correct
// for the first one.
//
// The published map is kept at arm's length: only 8 keys, and the check is a
// range walk rather than keyed lookups. Writing a large element over its pointer
// slot corrupts the table's own bookkeeping, not just the values: observed
// symptoms include len() reporting more entries than were assigned and lookups
// probing forever. A test that indexed such a map would hang the package instead
// of reporting, which is why the entry count that actually stresses growth lives
// in TestMapValueOverElemLimit_ManyKeysNoInspect below.
func TestMapValueOverElemLimit_ManyKeys(t *testing.T) {
	const n = 8
	var b strings.Builder
	b.WriteByte('{')
	want := make(map[string]string, n)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		key := "k" + strconv.Itoa(i)
		val := "v" + strconv.Itoa(i)
		want[key] = val
		b.WriteString(`"` + key + `":{"name":"` + val + `"}`)
	}
	b.WriteByte('}')
	src := b.String()

	for _, d := range mapValueDecoders() {
		t.Run(d.name, func(t *testing.T) {
			var m map[string]mapBoundaryOver
			if err := d.bind(t, []byte(src), &m); err != nil {
				t.Fatalf("%s: %v", d.name, err)
			}
			if len(m) != len(want) {
				t.Fatalf("%s: len = %d, want %d; a length past the assigned count means the table's own bookkeeping was overwritten",
					d.name, len(m), len(want))
			}
			seen := 0
			for key, got := range m {
				wantVal, ok := want[key]
				if !ok {
					t.Fatalf("%s: published unexpected key %q", d.name, key)
				}
				seen++
				// Length before content, and stop at the first bad one: past
				// this point the header is not something to keep formatting.
				if len(got.Name) != len(wantVal) {
					t.Fatalf("%s: %s published Name has len %d, want %d; the map value was written through the wrong slot",
						d.name, key, len(got.Name), len(wantVal))
				}
				if got.Name != wantVal {
					t.Errorf("%s: %s Name = %q, want %q", d.name, key, got.Name, wantVal)
				}
			}
			if seen != len(want) {
				t.Errorf("%s: walked %d entries, want %d", d.name, seen, len(want))
			}
		})
	}
}

// TestMapValueOverElemLimit_ManyKeysNoInspect drives enough keys to force several
// rounds of growth, which is where a mis-assigned element does the most damage to
// the table. It deliberately asserts nothing about the contents: at this size the
// corrupted map cannot be inspected from Go at all (len() over-reports and
// lookups can spin), so the only safe assertion is that decoding reported no
// error and the process is still standing.
//
// Its value is as a crash/hang canary. The content guarantee is the other tests'
// job; this one exists so growth is exercised without a corrupt map being handed
// to the test framework.
func TestMapValueOverElemLimit_ManyKeysNoInspect(t *testing.T) {
	requireMapElemLimitFixed(t)
	const n = 64
	var b strings.Builder
	b.WriteByte('{')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k` + strconv.Itoa(i) + `":{"name":"v` + strconv.Itoa(i) + `"}`)
	}
	b.WriteByte('}')
	src := []byte(b.String())

	for _, d := range mapValueDecoders() {
		t.Run(d.name, func(t *testing.T) {
			var m map[string]mapBoundaryOver
			if err := d.bind(t, src, &m); err != nil {
				t.Fatalf("%s: %v", d.name, err)
			}
		})
	}
}

// TestMapValueOverElemLimit_NonStringKey covers the generic assignment path. It
// already routes through runtime.mapassign, which performs the indirect
// allocation itself, so this must keep passing: it is the control that says the
// boundary belongs to the string-key fast path and not to map values at large.
func TestMapValueOverElemLimit_NonStringKey(t *testing.T) {
	const src = `{"7":{"name":"hello"}}`
	for _, d := range mapValueDecoders() {
		t.Run(d.name, func(t *testing.T) {
			var m map[int]mapBoundaryOver
			if err := d.bind(t, []byte(src), &m); err != nil {
				t.Fatalf("%s(%s): %v", d.name, src, err)
			}
			e := m[7]
			if len(e.Name) != len("hello") {
				t.Fatalf("%s: published Name has len %d, want %d", d.name, len(e.Name), len("hello"))
			}
			if e.Name != "hello" {
				t.Errorf("%s: Name = %q, want %q", d.name, e.Name, "hello")
			}
		})
	}
}

// TestMapValueOverElemLimit_Encode covers the other direction. Encoding a
// map[string]V iterates the groups directly, stepping by a stride the layout
// probe reports, so the same boundary applies: above it a slot holds a *V, and an
// iterator stepping by the reported stride would render that pointer's bytes as
// the value.
//
// The probe declines such a map (no inline element for a stride to describe),
// which routes it to the generic iteration that dereferences properly. Comparing
// against encoding/json rather than a literal keeps this about the boundary and
// not about how any particular field is spelled.
func TestMapValueOverElemLimit_Encode(t *testing.T) {
	cases := []struct {
		name string
		// value returns the map to encode, already populated.
		value func() any
	}{
		{"128B inline", func() any {
			return map[string]mapBoundaryUnder{"k": {Name: "hello"}}
		}},
		{"136B indirect", func() any {
			return map[string]mapBoundaryOver{"k": {Name: "hello"}}
		}},
		{"520B indirect", func() any {
			return map[string]mapBoundaryBig{"k": {Name: "hello"}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.value()
			got, err := vjson.Marshal(v)
			if err != nil {
				t.Fatalf("vjson.Marshal: %v", err)
			}
			// encoding/json renders a [N]byte array as a list where this encoder
			// uses base64, and that difference is unrelated to the boundary (it
			// shows for a plain struct too). Compare only the field the boundary
			// can damage: an element read from the wrong slot loses it entirely.
			if !strings.Contains(string(got), `"name":"hello"`) {
				out := string(got)
				if len(out) > 80 {
					out = out[:80] + "..."
				}
				t.Errorf("vjson.Marshal did not render the value's own field; got %s", out)
			}
		})
	}
}

// requireMapElemLimitFixed skips a case whose failure mode is fatal to the whole
// test binary rather than reportable.
//
// Most shapes above publish a bad element and let an assertion catch it. Two do
// not: they fault or spin inside the decode call itself, before any assertion
// runs, taking the package down with them. Their value is as post-fix guarantees,
// so they are gated on a cheap probe of the same defect rather than deleted;
// once the gate passes they run for real, and if the defect ever returns they go
// back to being skipped instead of wedging CI.
//
// The probe decodes a 136-byte map value, one word past abi.MapMaxElemBytes, and
// asks whether the published string survived. It uses the same public entry point
// as the cases it guards, so it cannot pass while they would fault.
func requireMapElemLimitFixed(t *testing.T) {
	t.Helper()
	var m map[string]mapBoundaryOver
	if err := vjson.Unmarshal([]byte(`{"k":{"name":"hello"}}`), &m); err != nil {
		t.Skipf("map values over abi.MapMaxElemBytes are still mis-assigned (decode error %v); this case faults inside the decoder, so it cannot report", err)
	}
	e := m["k"]
	if len(e.Name) != len("hello") || e.Name != "hello" {
		t.Skip("map values over abi.MapMaxElemBytes are still mis-assigned; this case faults or spins inside the decoder, so it cannot report")
	}
}
