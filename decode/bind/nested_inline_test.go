package bind

import (
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Nested inline-variant host tests via UnmarshalValue (tape-bind). The inline
// intercept (TAPE_BIND_INLINE_INTERCEPT) fires at struct-field, array-element,
// and map-value struct dispatch sites, running pass-1/pass-2 on the child's
// tape sub-span. Each case compares UnmarshalValue (tape-bind) against
// Unmarshal (JSON path, control).

// --- deep nested (2-level struct field) ---

type nestedMiddle struct {
	Inner inlHost `json:"inner"`
}
type nestedOuter struct {
	Middle nestedMiddle `json:"middle"`
}

func TestNestedInline_DeepStruct(t *testing.T) {
	src := `{"middle":{"inner":{"type":"user","name":"Alice","role":"admin"}}}`
	var u nestedOuter
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv nestedOuter
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []nestedOuter{u, uv} {
		m, ok := h.Middle.Inner.Data.(inlUser)
		if !ok {
			t.Fatalf("Middle.Inner.Data = %T, want inlUser", h.Middle.Inner.Data)
		}
		if m.Name != "Alice" || m.Role != "admin" {
			t.Errorf("Middle.Inner.Data = %+v, want {Alice admin}", m)
		}
	}
}

// --- inline host as slice element ---

type nestedSliceHost struct {
	Items []inlHost `json:"items"`
}

func TestNestedInline_SliceElement(t *testing.T) {
	src := `{"items":[{"type":"user","name":"Alice","role":"admin"},{"type":"product","title":"Widget","price":99}]}`
	var u nestedSliceHost
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv nestedSliceHost
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []nestedSliceHost{u, uv} {
		if len(h.Items) != 2 {
			t.Fatalf("len(Items) = %d, want 2", len(h.Items))
		}
		u0, ok := h.Items[0].Data.(inlUser)
		if !ok || u0.Name != "Alice" || u0.Role != "admin" {
			t.Errorf("Items[0].Data = %+v (%T), want inlUser{Alice admin}", h.Items[0].Data, h.Items[0].Data)
		}
		p1, ok := h.Items[1].Data.(inlProduct)
		if !ok || p1.Title != "Widget" || p1.Price != 99 {
			t.Errorf("Items[1].Data = %+v (%T), want inlProduct{Widget 99}", h.Items[1].Data, h.Items[1].Data)
		}
	}
}

// --- inline host as map value ---

type nestedMapHost struct {
	Items map[string]inlHost `json:"items"`
}

func TestNestedInline_MapValue(t *testing.T) {
	src := `{"items":{"a":{"type":"user","name":"Alice","role":"admin"},"b":{"type":"product","title":"Widget","price":99}}}`
	var u nestedMapHost
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv nestedMapHost
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []nestedMapHost{u, uv} {
		if len(h.Items) != 2 {
			t.Fatalf("len(Items) = %d, want 2", len(h.Items))
		}
		ua, ok := h.Items["a"].Data.(inlUser)
		if !ok || ua.Name != "Alice" || ua.Role != "admin" {
			t.Errorf("Items[a].Data = %+v, want inlUser{Alice admin}", h.Items["a"].Data)
		}
		pb, ok := h.Items["b"].Data.(inlProduct)
		if !ok || pb.Title != "Widget" || pb.Price != 99 {
			t.Errorf("Items[b].Data = %+v, want inlProduct{Widget 99}", h.Items["b"].Data)
		}
	}
}

// --- nested + value.Value case ---

type nestedValueOuter struct {
	Inner coldCaseInlineValue `json:"inner"`
}

func TestNestedInline_ValueCase(t *testing.T) {
	src := `{"inner":{"type":"raw","extra":{"a":1}}}`
	var u nestedValueOuter
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv nestedValueOuter
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []nestedValueOuter{u, uv} {
		vv, ok := h.Inner.Data.(value.Value)
		if !ok {
			t.Fatalf("Inner.Data = %T, want value.Value", h.Inner.Data)
		}
		typ := vv.Get("type")
		if s, ok := typ.Str(); !ok || s != "raw" {
			t.Errorf("Inner.Data.type = %q, want %q", s, "raw")
		}
		extra := vv.Get("extra")
		a := extra.Get("a")
		if n, ok := a.Int(); !ok || n != 1 {
			t.Errorf("Inner.Data.extra.a = %d, want 1", n)
		}
	}
}

// --- nested + out-of-order disc ---

func TestNestedInline_OutOfOrder(t *testing.T) {
	src := `{"middle":{"inner":{"name":"Alice","role":"admin","type":"user"}}}`
	var u nestedOuter
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv nestedOuter
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []nestedOuter{u, uv} {
		m, ok := h.Middle.Inner.Data.(inlUser)
		if !ok || m.Name != "Alice" || m.Role != "admin" {
			t.Errorf("Middle.Inner.Data = %+v, want inlUser{Alice admin}", h.Middle.Inner.Data)
		}
	}
}

// --- multi-field: inline host in the middle, fields after must bind ---

type nestedMultiHost struct {
	Before string  `json:"before"`
	Inner  inlHost `json:"inner"`
	After  string  `json:"after"`
}

func TestNestedInline_MultiField(t *testing.T) {
	src := `{"before":"x","inner":{"type":"user","name":"Alice","role":"admin"},"after":"y"}`
	var u nestedMultiHost
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv nestedMultiHost
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []nestedMultiHost{u, uv} {
		if h.Before != "x" {
			t.Errorf("Before = %q, want %q", h.Before, "x")
		}
		if h.After != "y" {
			t.Errorf("After = %q, want %q", h.After, "y")
		}
		m, ok := h.Inner.Data.(inlUser)
		if !ok || m.Name != "Alice" || m.Role != "admin" {
			t.Errorf("Inner.Data = %+v, want inlUser{Alice admin}", h.Inner.Data)
		}
	}
}

// --- inline case whose struct field is itself an inline-variant host ---
//
// The case type does not host an inline variant itself (so the build's
// diamond check at variant.go:319 passes), but one of its fields is an
// inline-variant host. The field boundary isolates the two inline dispatches
// (the inner host is a separate struct instance with its own Data), so this
// is composition, not nested inheritance. Both Unmarshal and UnmarshalValue
// must dispatch both layers: the outer inline selects the case type, and the
// Inner field's inline variant selects inlUser.
//
// The nested intercept saves/restores m->rebind_inline_eface_target across
// the inner pass-1/pass-2 (t_inline_post_walk overwrites it for the inner
// pass-2 close; without save/restore the outer eface would be written to the
// inner's target, leaving host.Data nil).

type inlineCaseWithNestedInlineField struct {
	Label string  `json:"label"`
	Inner inlHost `json:"inner"`
}

type hostWithNestedInlineCase struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[hostWithNestedInlineCase, struct {
		_ inlineCaseWithNestedInlineField `case:"nested"`
	}]()
}

func TestNestedInline_InlineCaseWithNestedInlineField(t *testing.T) {
	src := `{"type":"nested","label":"x","inner":{"type":"user","name":"Alice","role":"admin"}}`
	var u hostWithNestedInlineCase
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv hostWithNestedInlineCase
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []hostWithNestedInlineCase{u, uv} {
		c, ok := h.Data.(inlineCaseWithNestedInlineField)
		if !ok {
			t.Fatalf("Data = %T, want inlineCaseWithNestedInlineField", h.Data)
		}
		if c.Label != "x" {
			t.Errorf("Label = %q, want %q", c.Label, "x")
		}
		m, ok := c.Inner.Data.(inlUser)
		if !ok {
			t.Fatalf("Inner.Data = %T, want inlUser (nested inline must dispatch)", c.Inner.Data)
		}
		if m.Name != "Alice" || m.Role != "admin" {
			t.Errorf("Inner.Data = %+v, want inlUser{Alice admin}", m)
		}
	}
}

// --- three-level isolated inline variant ---
//
// level1Host (inline) → case level1Case → field level2Host (inline)
//   → case level2Case → field level3Host (inline) → case level3Case.
// Each layer's intercept saves/restores rebind_inline_eface_target via the
// nested_walk_save LIFO stack (depth 4). Pins the stack discipline.

type level3Case struct {
	Name string `json:"name"`
}
type level3Host struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[level3Host, struct {
		_ level3Case `case:"c3"`
	}]()
}

type level2Case struct {
	Label string     `json:"label"`
	Inner level3Host `json:"inner"`
}
type level2Host struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[level2Host, struct {
		_ level2Case `case:"c2"`
	}]()
}

type level1Case struct {
	Tag   string     `json:"tag"`
	Inner level2Host `json:"inner"`
}
type level1Host struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[level1Host, struct {
		_ level1Case `case:"c1"`
	}]()
}

func TestNestedInline_ThreeLevels(t *testing.T) {
	src := `{"type":"c1","tag":"t","inner":{"type":"c2","label":"l","inner":{"type":"c3","name":"n"}}}`
	var u level1Host
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, _ := dom.Parse([]byte(src))
	var uv level1Host
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []level1Host{u, uv} {
		c1, ok := h.Data.(level1Case)
		if !ok {
			t.Fatalf("L1 Data = %T, want level1Case", h.Data)
		}
		if c1.Tag != "t" {
			t.Errorf("L1 Tag = %q, want %q", c1.Tag, "t")
		}
		c2, ok := c1.Inner.Data.(level2Case)
		if !ok {
			t.Fatalf("L2 Data = %T, want level2Case", c1.Inner.Data)
		}
		if c2.Label != "l" {
			t.Errorf("L2 Label = %q, want %q", c2.Label, "l")
		}
		c3, ok := c2.Inner.Data.(level3Case)
		if !ok {
			t.Fatalf("L3 Data = %T, want level3Case", c2.Inner.Data)
		}
		if c3.Name != "n" {
			t.Errorf("L3 Name = %q, want %q", c3.Name, "n")
		}
	}
}
