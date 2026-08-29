package bind

import (
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
)

// Tests for Unmarshal into value.Value. The bind path produces a tape-backed
// value.Value identical in shape to what dom.Parse emits; the cold path
// (variant/kindof tape rebind) also funnels through this code via
// serveTapeBindValue. These tests cover root value.Value, struct fields,
// slice/map elements, pointer fields, and the parsing invariants
// (depth, aliasing, error propagation) that the value.Value kind participates
// in. The depth boundary itself is pinned in value_depth_test.go.

// mustUnmarshalValue unmarshals in into a fresh value.Value, failing t on
// error. Used by the root-level tests below.
func mustUnmarshalValue(t *testing.T, in string) value.Value {
	t.Helper()
	var v value.Value
	if err := Unmarshal([]byte(in), &v); err != nil {
		t.Fatalf("Unmarshal(%q): %v", in, err)
	}
	return v
}

// mustParseDom parses in via dom.Parse, failing t on error. dom.Parse is the
// reference tape producer; bind's value.Value must serialize identically.
func mustParseDom(t *testing.T, in string) value.Value {
	t.Helper()
	v, err := dom.Parse([]byte(in))
	if err != nil {
		t.Fatalf("dom.Parse(%q): %v", in, err)
	}
	return v
}

// firstDiff returns the byte offset of the first divergence between a and b,
// or -1 if they share a common prefix and have equal length. Used by the
// fixture round-trip tests so failures point at the divergence instead of
// dumping megabytes of JSON.
func firstDiff(a, b string) int {
	n := min(len(b), len(a))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) == len(b) {
		return -1
	}
	return n
}

// --- root: all JSON kinds ---

func TestValueRoot_AllKinds(t *testing.T) {
	cases := []struct {
		in   string
		want value.Kind
	}{
		{`null`, value.KindNull},
		{`true`, value.KindBool},
		{`false`, value.KindBool},
		{`42`, value.KindNumber},
		{`3.14`, value.KindNumber},
		{`"hello"`, value.KindString},
		{`""`, value.KindString},
		{`[]`, value.KindArray},
		{`[1,2,3]`, value.KindArray},
		{`{}`, value.KindObject},
		{`{"a":1}`, value.KindObject},
	}
	for _, c := range cases {
		v := mustUnmarshalValue(t, c.in)
		if v.Type() != c.want {
			t.Errorf("input=%s: Type()=%v want %v", c.in, v.Type(), c.want)
		}
		if !v.Exists() {
			t.Errorf("input=%s: Exists()=false", c.in)
		}
		if !v.Valid() {
			t.Errorf("input=%s: Valid()=false", c.in)
		}
	}
}

// --- root: scalar accessors ---

func TestValueRoot_BoolNullString(t *testing.T) {
	v := mustUnmarshalValue(t, `true`)
	if b, ok := v.Bool(); !ok || !b {
		t.Errorf("true: Bool()=%v,%v want true,true", b, ok)
	}
	v = mustUnmarshalValue(t, `false`)
	if b, ok := v.Bool(); !ok || b {
		t.Errorf("false: Bool()=%v,%v want false,true", b, ok)
	}
	v = mustUnmarshalValue(t, `null`)
	if !v.IsNull() {
		t.Errorf("null: IsNull()=false")
	}
	// Empty string round-trips without degenerating into a missing value.
	v = mustUnmarshalValue(t, `""`)
	if s, ok := v.Str(); !ok || s != "" {
		t.Errorf(`"": Str()=%q,%v want "",true`, s, ok)
	}
	// Escapes resolve in the str_arena path; Str returns the decoded form.
	v = mustUnmarshalValue(t, `"hello\nworld\t\"q\""`)
	if s, ok := v.Str(); !ok || s != "hello\nworld\t\"q\"" {
		t.Errorf("escapes: Str()=%q,%v", s, ok)
	}
	// Multibyte unicode survives the str_arena copy.
	v = mustUnmarshalValue(t, `"→←中文🎉"`)
	if s, ok := v.Str(); !ok || s != "→←中文🎉" {
		t.Errorf("unicode: Str()=%q,%v", s, ok)
	}
}

func TestValueRoot_NumberAccessors(t *testing.T) {
	cases := []struct {
		in  string
		i   int64
		iok bool
		u   uint64
		uok bool
		f   float64
		fok bool
	}{
		{`0`, 0, true, 0, true, 0, true},
		{`-1`, -1, true, 0, false, -1, true},
		{`9223372036854775807`, 1<<63 - 1, true, 1<<63 - 1, true, 9.223372036854776e+18, true},
		{`9223372036854775808`, 0, false, 1 << 63, true, 9.223372036854776e+18, true},
		{`18446744073709551615`, 0, false, 1<<64 - 1, true, 1.8446744073709552e+19, true},
		{`1.5`, 0, false, 0, false, 1.5, true},
		{`1e10`, 10000000000, true, 10000000000, true, 1e10, true},
		{`-3.14`, 0, false, 0, false, -3.14, true},
		// Integral floats are accepted by Int/Uint (matches value.Value's
		// documented semantics for TagDouble).
		{`1.0`, 1, true, 1, true, 1, true},
		{`1e3`, 1000, true, 1000, true, 1000, true},
	}
	for _, c := range cases {
		v := mustUnmarshalValue(t, c.in)
		if i, iok := v.Int(); iok != c.iok || (iok && i != c.i) {
			t.Errorf("%s: Int()=%d,%v want %d,%v", c.in, i, iok, c.i, c.iok)
		}
		if u, uok := v.Uint(); uok != c.uok || (uok && u != c.u) {
			t.Errorf("%s: Uint()=%d,%v want %d,%v", c.in, u, uok, c.u, c.uok)
		}
		if f, fok := v.Float(); fok != c.fok || (fok && f != c.f) {
			t.Errorf("%s: Float()=%v,%v want %v,%v", c.in, f, fok, c.f, c.fok)
		}
	}
}

// --- root: navigation ---

func TestValueRoot_Navigation(t *testing.T) {
	v := mustUnmarshalValue(t, `{"id":"abc","n":7,"ok":true,"tags":["a","b"],"miss":null,"nested":{"k":"v"}}`)

	// Scalar path accessors.
	if s, ok := v.GetString("id"); !ok || s != "abc" {
		t.Errorf(`GetString("id")=%q,%v want "abc",true`, s, ok)
	}
	if i, ok := v.GetInt("n"); !ok || i != 7 {
		t.Errorf(`GetInt("n")=%d,%v want 7,true`, i, ok)
	}
	if b, ok := v.GetBool("ok"); !ok || !b {
		t.Errorf(`GetBool("ok")=%v,%v want true,true`, b, ok)
	}

	// Missing key returns an empty Value (chained lookups must be safe).
	if miss := v.Get("missing"); miss.Exists() {
		t.Error(`Get("missing").Exists()=true`)
	}
	if s, ok := v.GetString("missing"); ok {
		t.Errorf(`GetString("missing")=%q,%v want "",false`, s, ok)
	}

	// Nested Get and chained GetString.
	if s, ok := v.GetString("nested", "k"); !ok || s != "v" {
		t.Errorf(`GetString("nested","k")=%q,%v want "v",true`, s, ok)
	}

	// Index on array; out-of-range returns an empty Value.
	tags := v.Get("tags")
	if got := tags.Len(); got != 2 {
		t.Errorf("tags.Len()=%d want 2", got)
	}
	if e := tags.Index(0); e.Exists() {
		if s, ok := e.Str(); !ok || s != "a" {
			t.Errorf(`tags.Index(0).Str()=%q,%v want "a",true`, s, ok)
		}
	} else {
		t.Error("tags.Index(0).Exists()=false")
	}
	if e := tags.Index(10); e.Exists() {
		t.Error("tags.Index(10).Exists()=true for 2-elem array")
	}
	// Negative Index counts from the end.
	if e := tags.Index(-1); e.Exists() {
		if s, ok := e.Str(); !ok || s != "b" {
			t.Errorf(`tags.Index(-1).Str()=%q,%v want "b",true`, s, ok)
		}
	}

	// ForEachKey yields keys in document order (tape preserves source order).
	var keys []string
	v.ForEachKey(func(k string, _ value.Value) bool { keys = append(keys, k); return true })
	want := []string{"id", "n", "ok", "tags", "miss", "nested"}
	if len(keys) != len(want) {
		t.Fatalf("ForEachKey yielded %d keys, want %d: %v", len(keys), len(want), keys)
	}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("ForEachKey[%d]=%q want %q", i, keys[i], k)
		}
	}

	// ForEachElem walks array elements in order.
	var elems []string
	tags.ForEachElem(func(_ int, e value.Value) bool {
		s, _ := e.Str()
		elems = append(elems, s)
		return true
	})
	if len(elems) != 2 || elems[0] != "a" || elems[1] != "b" {
		t.Errorf("ForEachElem=%v want [a b]", elems)
	}

	// Early stop: returning false halts iteration.
	n := 0
	v.ForEachKey(func(string, value.Value) bool { n++; return n < 2 })
	if n != 2 {
		t.Errorf("ForEachKey early-stop ran %d times, want 2", n)
	}
}

// --- root: round-trip vs dom.Parse ---
//
// bind's value.Value and dom.Parse's value.Value serialize identically
// because both walk the same tape format. This is the strongest invariant:
// any divergence in tag selection, str_arena layout, or container-end
// bookkeeping surfaces as a String() mismatch.

func TestValueRoot_RoundTripVsDom(t *testing.T) {
	cases := []string{
		`null`,
		`true`,
		`false`,
		`42`,
		`3.14`,
		`"hello"`,
		`"with escape\n\t\"quote\""`,
		`"unicode → ← 中文 🎉"`,
		`[]`,
		`[1,2,3]`,
		`{}`,
		`{"a":1,"b":"two","c":true}`,
		`{"nested":{"deep":{"arr":[1,[2,[3]]]}}}`,
		`{"mixed":[1,"two",3.14,true,null,{"k":"v"},[1,2,3]]}`,
		`   {"ws":"around"}   `,
		`{"empty_arr":[],"empty_obj":{}}`,
	}
	for _, in := range cases {
		bindV := mustUnmarshalValue(t, in)
		domV := mustParseDom(t, in)
		if got, want := bindV.String(), domV.String(); got != want {
			t.Errorf("input=%s\n  bind=%s\n  dom =%s", in, got, want)
		}
	}
}

// --- root: input mutation safety ---
//
// The bind path writes escape-free strings as TagStrFree (str_arena verbatim
// copy) and escaped strings as TagString (decoded into str_arena). Both are
// independent of the caller's src buffer, so mutating src after parse must not
// affect the returned Value. This is the bind-path equivalent of TestBindRawMessage_BytesAreCopied.

func TestValueRoot_InputMutationSafe(t *testing.T) {
	src := []byte(`{"key":"hello world","nested":{"inner":"value"},"arr":["a","b"]}`)
	var v value.Value
	if err := Unmarshal(src, &v); err != nil {
		t.Fatal(err)
	}
	s1, _ := v.GetString("key")
	s2, _ := v.GetString("nested", "inner")
	arr := v.Get("arr")
	arrFirst := arr.Index(0)
	arr0, _ := arrFirst.Str()

	for i := range src {
		src[i] = 'X'
	}

	if s1b, _ := v.GetString("key"); s1 != s1b {
		t.Errorf("key string mutated: was %q, now %q", s1, s1b)
	}
	if s2b, _ := v.GetString("nested", "inner"); s2 != s2b {
		t.Errorf("nested.inner string mutated: was %q, now %q", s2, s2b)
	}
	arrFirst2 := arr.Index(0)
	if s2b, _ := arrFirst2.Str(); arr0 != s2b {
		t.Errorf("arr[0] string mutated: was %q, now %q", arr0, s2b)
	}
}

// --- root: parser reuse ---
//
// NewParser[value.Value]() borrows a Parser whose shape (and tape_arena) is
// reused across calls. Each call must produce a Value equivalent to what
// dom.Parse emits for the same input; the StrArena/TapeArena monotonically
// advance, so prior Values stay valid as long as the arena doesn't grow.

func TestValueRoot_ParserReuse(t *testing.T) {
	p, err := NewParser[value.Value]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	inputs := []string{
		`{"a":1}`,
		`[1,2,3]`,
		`"hello"`,
		`42`,
		`null`,
		`true`,
		`{"nested":{"deep":{"arr":[1,2,3]}}}`,
	}
	for i, in := range inputs {
		var v value.Value
		if err := p.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		domV := mustParseDom(t, in)
		if v.String() != domV.String() {
			t.Errorf("call %d: got=%s want=%s", i, v.String(), domV.String())
		}
	}
}

// --- root: error cases ---

func TestValueRoot_ErrorCases(t *testing.T) {
	cases := []string{
		``,
		`{`,
		`[`,
		`{"a"}`,
		`"unclosed`,
		`tru`,
		`nul`,
		`[1,]`,
		`{"a":}`,
		`xx`,
		`123abc`,
	}
	for _, in := range cases {
		var v value.Value
		err := Unmarshal([]byte(in), &v)
		if err == nil {
			t.Errorf("input=%q: expected error, got nil (v=%s)", in, v.String())
		}
	}
}

// TestValueField_UnpublishedAfterError pins the state of a value.Value that a
// failed parse left in the destination.
//
// C installs the Doc pointer into the destination as it parses, but the
// doc's buffer views are filled in by publishDoc only after the parse
// succeeds. An error return therefore leaves a reachable Value whose doc is
// non-nil and whose Tape is empty. Publishing on the error path is not the fix:
// the arena goes back to the pool immediately after, so those views would alias
// memory the next parse overwrites.
//
// So the guard is Valid/Exists reporting false, which makes such a Value
// indistinguishable from the zero Value. Asserting that every accessor returns
// its zero rather than merely that Valid() is false is the point: the accessors
// reach the tape through unsafe.SliceData, so a stale predicate does not return
// a wrong answer, it segfaults.
func TestValueField_UnpublishedAfterError(t *testing.T) {
	type host struct {
		M1 value.Value `json:"m1"`
		N  int         `json:"n"`
	}
	// Each input binds m1 successfully, then fails: type mismatch, truncation,
	// and trailing data respectively, so the error is raised at three different
	// points after the Value was written.
	for _, in := range []string{
		`{"m1":1,"n":"bad"}`,
		`{"m1":{"a":1},"n":`,
		`{"m1":[1,2]} xx`,
		`{"m1":{"a":{"b":[1,2,3]}},"n":"bad"}`,
	} {
		t.Run(in, func(t *testing.T) {
			var h host
			if err := Unmarshal([]byte(in), &h); err == nil {
				t.Fatalf("Unmarshal(%s): want error, got nil", in)
			}
			v := h.M1
			if v.Valid() {
				t.Error("Valid() = true for a Value left by a failed parse")
			}
			if v.Exists() {
				t.Error("Exists() = true for a Value left by a failed parse")
			}
			// Unguarded reads: these must not fault.
			if got := v.Type(); got != value.KindInvalid {
				t.Errorf("Type() = %v, want KindInvalid", got)
			}
			if got := v.Len(); got != 0 {
				t.Errorf("Len() = %d, want 0", got)
			}
			if got := v.String(); got != "" {
				t.Errorf("String() = %q, want empty", got)
			}
			if s, ok := v.Str(); ok || s != "" {
				t.Errorf("Str() = (%q, %v), want (\"\", false)", s, ok)
			}
			if v.IsNull() {
				t.Error("IsNull() = true, want false")
			}
			got := v.Get("a")
			idx := v.Index(0)
			if got.Valid() || idx.Valid() {
				t.Error("navigation off an unpublished Value produced a valid Value")
			}
			v.ForEachKey(func(k string, _ value.Value) bool {
				t.Errorf("ForEachKey visited %q on an unpublished Value", k)
				return false
			})
			v.ForEachElem(func(i int, _ value.Value) bool {
				t.Errorf("ForEachElem visited %d on an unpublished Value", i)
				return false
			})
			// MarshalJSON has its own doc check, so it needs its own assertion.
			js, err := v.MarshalJSON()
			if err != nil {
				t.Errorf("MarshalJSON: %v", err)
			}
			if string(js) != "null" {
				t.Errorf("MarshalJSON = %s, want null", js)
			}
		})
	}
}

// --- root: fixture round-trip ---
//
// Reference fixtures from the shared corpus: each must round-trip through
// bind's value.Value and produce the same String() as dom.Parse over the
// same input. The largest datasets are omitted to keep the test under a
// second.

func TestValueRoot_FixtureRoundTrip(t *testing.T) {
	fixtures := []string{
		"canada_geometry",
		"citm_catalog",
		"kube_pod",
		"escape_heavy",
		"tiny",
		"small",
	}
	for _, name := range fixtures {
		data, err := loadCorpus(name)
		if err != nil {
			t.Fatalf("loadCorpus %s: %v", name, err)
		}
		var v value.Value
		if uerr := Unmarshal(data, &v); uerr != nil {
			t.Fatalf("Unmarshal %s: %v", name, uerr)
		}
		domV, err := dom.Parse(data)
		if err != nil {
			t.Fatalf("dom.Parse %s: %v", name, err)
		}
		got, want := v.String(), domV.String()
		if diff := firstDiff(got, want); diff >= 0 {
			lo := max(diff-40, 0)
			hi := min(min(diff+40, len(got)), len(want))
			t.Errorf("%s: first diff at %d\n  got =...%q\n  want=...%q",
				name, diff, got[lo:hi], want[lo:hi])
		}
	}
}

// --- struct field ---

type valueFieldHost struct {
	Name string      `json:"name"`
	Raw  value.Value `json:"raw"`
}

func TestValueField_BasicAccess(t *testing.T) {
	src := `{"name":"test","raw":{"nested":{"deep":true,"arr":[1,2,3]}}}`
	var f valueFieldHost
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.Name != "test" {
		t.Errorf("Name=%q want test", f.Name)
	}
	if f.Raw.Type() != value.KindObject {
		t.Errorf("Raw.Type()=%v want object", f.Raw.Type())
	}
	if deep, ok := f.Raw.GetBool("nested", "deep"); !ok || !deep {
		t.Errorf(`GetBool("nested","deep")=%v,%v want true,true`, deep, ok)
	}
	arr := f.Raw.Get("nested", "arr")
	if arr.Type() != value.KindArray {
		t.Fatalf(`Get("nested","arr").Type()=%v want array`, arr.Type())
	}
	if n := arr.Len(); n != 3 {
		t.Errorf("arr.Len()=%d want 3", n)
	}
	// String() on the field round-trips the captured sub-tree.
	if got, want := f.Raw.String(), `{"nested":{"deep":true,"arr":[1,2,3]}}`; got != want {
		t.Errorf("Raw.String()=%s want %s", got, want)
	}
}

func TestValueField_NullAtField(t *testing.T) {
	// JSON null into a value.Value field: the field is set (Exists reports
	// true) and IsNull reports true. Matches value.Value's contract that an
	// explicit null is a captured value, not a missing one.
	src := `{"name":"test","raw":null}`
	var f valueFieldHost
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if !f.Raw.Exists() {
		t.Error("Raw.Exists()=false for explicit null; null is a value")
	}
	if !f.Raw.IsNull() {
		t.Error("Raw.IsNull()=false")
	}
	if got := f.Raw.Type(); got != value.KindNull {
		t.Errorf("Raw.Type()=%v want null", got)
	}
}

func TestValueField_MissingField(t *testing.T) {
	// A missing field leaves the value.Value zero: Exists reports false and
	// Type reports KindInvalid. This matches encoding/json's "leave the
	// destination at its zero value" semantics.
	src := `{"name":"test"}`
	var f valueFieldHost
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.Raw.Exists() {
		t.Error("Raw.Exists()=true for missing field; expected zero Value")
	}
	if got := f.Raw.Type(); got != value.KindInvalid {
		t.Errorf("Raw.Type()=%v want invalid", got)
	}
}

func TestValueField_AllKinds(t *testing.T) {
	// Every JSON kind captured as a value.Value field round-trips through
	// String() matching the dom.Parse of the same sub-document.
	cases := []string{
		`{"raw":null}`,
		`{"raw":true}`,
		`{"raw":false}`,
		`{"raw":42}`,
		`{"raw":3.14}`,
		`{"raw":"hello"}`,
		`{"raw":[]}`,
		`{"raw":[1,2,3]}`,
		`{"raw":{}}`,
		`{"raw":{"a":1,"b":"two"}}`,
		`{"raw":[1,"two",3.14,true,null,{"k":"v"},[1,2,3]]}`,
	}
	for _, in := range cases {
		var f valueFieldHost
		if err := Unmarshal([]byte(in), &f); err != nil {
			t.Fatalf("Unmarshal(%s): %v", in, err)
		}
		// Re-parse the captured sub-tree via dom to get the reference form,
		// then compare. dom.Parse on the full document and SliceData on the
		// "raw" key would also work; the simpler check is to compare the
		// bind String() against a dom.Parse of just the sub-document.
		sub := strings.TrimPrefix(in, `{"raw":`)
		sub = strings.TrimSuffix(sub, `}`)
		domV := mustParseDom(t, sub)
		if f.Raw.String() != domV.String() {
			t.Errorf("input=%s: Raw.String()=%s want %s", in, f.Raw.String(), domV.String())
		}
	}
}

// Nested struct: value.Value field lives inside a non-root struct reached via
// BIND_DESCEND_STRUCT. Confirms the value path fires outside of root entry.

type valueNestedInner struct {
	V value.Value `json:"v"`
}

type valueNestedOuter struct {
	Label string           `json:"label"`
	Inner valueNestedInner `json:"inner"`
}

func TestValueField_NestedStruct(t *testing.T) {
	src := `{"label":"outer","inner":{"v":{"k":"v","arr":[1,2,3]}}}`
	var o valueNestedOuter
	if err := Unmarshal([]byte(src), &o); err != nil {
		t.Fatal(err)
	}
	if o.Label != "outer" {
		t.Errorf("Label=%q", o.Label)
	}
	if s, ok := o.Inner.V.GetString("k"); !ok || s != "v" {
		t.Errorf(`Inner.V.GetString("k")=%q,%v`, s, ok)
	}
	if arr := o.Inner.V.Get("arr"); arr.Len() != 3 {
		t.Errorf(`Inner.V.Get("arr").Len()=%d want 3`, arr.Len())
	}
}

// --- slice field ---

type valueSliceField struct {
	Items []value.Value `json:"items"`
}

func TestValueSliceField_MixedKinds(t *testing.T) {
	src := `{"items":[{"a":1},"hello",42,null,[1,2]]}`
	var f valueSliceField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 5 {
		t.Fatalf("len(Items)=%d want 5", len(f.Items))
	}
	wantKinds := []value.Kind{
		value.KindObject,
		value.KindString,
		value.KindNumber,
		value.KindNull,
		value.KindArray,
	}
	for i, k := range wantKinds {
		if f.Items[i].Type() != k {
			t.Errorf("Items[%d].Type()=%v want %v", i, f.Items[i].Type(), k)
		}
	}
	// Spot-check element content.
	if n, ok := f.Items[2].Int(); !ok || n != 42 {
		t.Errorf("Items[2].Int()=%d,%v want 42,true", n, ok)
	}
	if !f.Items[3].IsNull() {
		t.Errorf("Items[3].IsNull()=false")
	}
	if n := f.Items[4].Len(); n != 2 {
		t.Errorf("Items[4].Len()=%d want 2", n)
	}
}

func TestValueSliceField_Empty(t *testing.T) {
	src := `{"items":[]}`
	var f valueSliceField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 0 {
		t.Errorf("len(Items)=%d want 0", len(f.Items))
	}
}

func TestValueSliceField_Null(t *testing.T) {
	// JSON null into a []Value field leaves the slice nil (matches
	// encoding/json's behavior for null into a slice destination).
	src := `{"items":null}`
	var f valueSliceField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.Items != nil {
		t.Errorf("Items=%v want nil", f.Items)
	}
}

func TestValueSliceField_Missing(t *testing.T) {
	src := `{}`
	var f valueSliceField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.Items != nil {
		t.Errorf("Items=%v want nil for missing field", f.Items)
	}
}

// Root-level []value.Value.

func TestValueSliceRoot_MixedKinds(t *testing.T) {
	src := `[{"a":1},"hello",42,null,[1,2]]`
	var items []value.Value
	if err := Unmarshal([]byte(src), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("len=%d want 5", len(items))
	}
	wantKinds := []value.Kind{
		value.KindObject,
		value.KindString,
		value.KindNumber,
		value.KindNull,
		value.KindArray,
	}
	for i, k := range wantKinds {
		if items[i].Type() != k {
			t.Errorf("items[%d].Type()=%v want %v", i, items[i].Type(), k)
		}
	}
	// String() on each element matches dom.Parse of the same element.
	for i, sub := range []string{`{"a":1}`, `"hello"`, `42`, `null`, `[1,2]`} {
		domV := mustParseDom(t, sub)
		if items[i].String() != domV.String() {
			t.Errorf("items[%d]: got=%s want=%s", i, items[i].String(), domV.String())
		}
	}
}

// --- map field ---

type valueMapField struct {
	Items map[string]value.Value `json:"items"`
}

func TestValueMapField_MixedKinds(t *testing.T) {
	src := `{"items":{"a":{"x":1},"b":"hi","c":99,"d":null,"e":[1,2]}}`
	var f valueMapField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 5 {
		t.Fatalf("len(Items)=%d want 5", len(f.Items))
	}
	if v := f.Items["a"]; v.Type() != value.KindObject {
		t.Errorf(`Items["a"].Type()=%v want object`, v.Type())
	}
	if v := f.Items["b"]; v.Type() != value.KindString {
		t.Errorf(`Items["b"].Type()=%v want string`, v.Type())
	}
	if v := f.Items["c"]; v.Type() != value.KindNumber {
		t.Errorf(`Items["c"].Type()=%v want number`, v.Type())
	}
	if v := f.Items["d"]; !v.IsNull() {
		t.Errorf(`Items["d"].IsNull()=false`)
	}
	if v := f.Items["e"]; v.Type() != value.KindArray || v.Len() != 2 {
		t.Errorf(`Items["e"].Type()=%v Len=%d want array,2`, v.Type(), v.Len())
	}
	// Spot-check content navigation.
	aVal := f.Items["a"]
	if i, ok := aVal.GetInt("x"); !ok || i != 1 {
		t.Errorf(`Items["a"].GetInt("x")=%d,%v want 1,true`, i, ok)
	}
	bVal := f.Items["b"]
	if s, ok := bVal.Str(); !ok || s != "hi" {
		t.Errorf(`Items["b"].Str()=%q,%v want "hi",true`, s, ok)
	}
}

func TestValueMapField_Empty(t *testing.T) {
	src := `{"items":{}}`
	var f valueMapField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.Items == nil || len(f.Items) != 0 {
		t.Errorf("Items=%v want non-nil empty map", f.Items)
	}
}

func TestValueMapField_Null(t *testing.T) {
	src := `{"items":null}`
	var f valueMapField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.Items != nil {
		t.Errorf("Items=%v want nil for null", f.Items)
	}
}

// Root-level map[string]value.Value.

func TestValueMapRoot_MixedKinds(t *testing.T) {
	src := `{"a":{"x":1},"b":"hi","c":99}`
	var m map[string]value.Value
	if err := Unmarshal([]byte(src), &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("len=%d want 3", len(m))
	}
	if v, ok := m["a"]; !ok || v.Type() != value.KindObject {
		t.Errorf(`m["a"]=%v ok=%v`, v, ok)
	}
	if v, ok := m["b"]; !ok || v.Type() != value.KindString {
		t.Errorf(`m["b"]=%v ok=%v`, v, ok)
	}
	cVal := m["c"]
	if i, ok := cVal.Int(); !ok || i != 99 {
		t.Errorf(`m["c"].Int()=%d,%v want 99,true`, i, ok)
	}
}

// --- pointer field ---

type valuePtrField struct {
	V *value.Value `json:"v"`
}

func TestValuePtrField_NonNull(t *testing.T) {
	src := `{"v":{"k":"v","arr":[1,2,3]}}`
	var f valuePtrField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.V == nil {
		t.Fatal("V is nil")
	}
	if f.V.Type() != value.KindObject {
		t.Errorf("V.Type()=%v want object", f.V.Type())
	}
	if s, ok := f.V.GetString("k"); !ok || s != "v" {
		t.Errorf(`V.GetString("k")=%q,%v`, s, ok)
	}
	arrVal := f.V.Get("arr")
	if n := arrVal.Len(); n != 3 {
		t.Errorf(`V.Get("arr").Len()=%d want 3`, n)
	}
}

func TestValuePtrField_Null(t *testing.T) {
	src := `{"v":null}`
	var f valuePtrField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.V != nil {
		t.Errorf("V=%v want nil for JSON null", f.V)
	}
}

func TestValuePtrField_Missing(t *testing.T) {
	// A missing field leaves the pointer nil, matching encoding/json.
	src := `{}`
	var f valuePtrField
	if err := Unmarshal([]byte(src), &f); err != nil {
		t.Fatal(err)
	}
	if f.V != nil {
		t.Errorf("V=%v want nil for missing field", f.V)
	}
}

func TestValuePtrField_AllKinds(t *testing.T) {
	// Each JSON kind captured via *value.Value round-trips String() against
	// dom.Parse of the same sub-document.
	cases := []string{
		`{"v":null}`,
		`{"v":true}`,
		`{"v":42}`,
		`{"v":3.14}`,
		`{"v":"hello"}`,
		`{"v":[1,2,3]}`,
		`{"v":{"k":"v"}}`,
	}
	for _, in := range cases {
		var f valuePtrField
		if err := Unmarshal([]byte(in), &f); err != nil {
			t.Fatalf("Unmarshal(%s): %v", in, err)
		}
		if in == `{"v":null}` {
			if f.V != nil {
				t.Errorf("%s: V=%v want nil for null pointer", in, f.V)
			}
			continue
		}
		if f.V == nil {
			t.Errorf("%s: V is nil for non-null input", in)
			continue
		}
		sub := strings.TrimSuffix(strings.TrimPrefix(in, `{"v":`), `}`)
		domV := mustParseDom(t, sub)
		if f.V.String() != domV.String() {
			t.Errorf("input=%s: V.String()=%s want %s", in, f.V.String(), domV.String())
		}
	}
}
