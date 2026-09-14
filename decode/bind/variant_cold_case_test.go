package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Cold-kind variant/kindof case types are rejected by the tape-bind
// sub-routine. The fast path's inline dispatch only covers concrete kinds
// (struct/slice/map/scalar/array); cold-kind cases (value.Value,
// json.RawMessage, Unmarshaler, ...) fall back to the cold path. An any-target
// case is the exception: the field binds with the default any boxing, so it
// works on every path (see the passthrough tests below). At build time,
// checkVariantCaseTypes marks the type tree TapeBindUnsupported so
// UnmarshalValue fails fast with TapeBindUnsupportedError before entering C;
// Unmarshal reaches the same gate at runtime via BIND_ERR_KINDOF_COLD_CASE /
// BIND_ERR_VARIANT_COLD_CASE. Inline variants add a build-time struct
// requirement on each case type.
//
// These tests pin the gates so a future change that lifts the cold-kind
// restriction (e.g. a reserve-unknown value.Value case) deliberately updates
// or removes them.

// --- sibling variant, value.Value case (the natural reserve-unknown pattern) ---

type coldCaseSiblingValue struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[coldCaseSiblingValue, struct {
		_ value.Value `case:"raw"`
	}]()
}

// --- sibling variant, json.RawMessage case ---

type coldCaseSiblingRaw struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[coldCaseSiblingRaw, struct {
		_ json.RawMessage `case:"raw"`
	}]()
}

// --- kindof, value.Value object case ---

type coldCaseKindofValue struct {
	Data any `json:"data" vjson:"kindof"`
}

func init() {
	vbind.DefineKindofCases[coldCaseKindofValue, struct {
		object value.Value
	}]()
}

// --- inline variant, json.RawMessage case (non-struct, build-time reject) ---

type coldCaseInlineRaw struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[coldCaseInlineRaw, struct {
		_ json.RawMessage `case:"raw"`
	}]()
}

// --- inline variant, value.Value case (struct kind, cold-kind reject) ---
//
// value.Value is reflect.Struct, so it passes the inline-case struct
// requirement; its StructMeta.InlineVariantIdx sentinel is stamped at collect
// (KindValue branch) so buildOneVariantTable does not misreport it as "hosts an
// inline variant". Rejection then falls through to checkVariantCaseTypes, the
// same cold-kind gate as sibling/kindof.

type coldCaseInlineValue struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[coldCaseInlineValue, struct {
		_ value.Value `case:"raw"`
	}]()
}

// assertTapeBindUnsupported verifies the error is TapeBindUnsupportedError
// with the cold-kind reason and a path that names the variant/kindof field.
func assertTapeBindUnsupported(t *testing.T, err error, wantReasonSub, wantPathSub string) {
	t.Helper()
	var uerr *TapeBindUnsupportedError
	if !errors.As(err, &uerr) {
		t.Fatalf("want *TapeBindUnsupportedError, got %T: %v", err, err)
	}
	if !strings.Contains(uerr.Pos.Reason, wantReasonSub) {
		t.Errorf("Reason=%q, want substring %q", uerr.Pos.Reason, wantReasonSub)
	}
	if !strings.Contains(uerr.Pos.Path, wantPathSub) {
		t.Errorf("Path=%q, want substring %q", uerr.Pos.Path, wantPathSub)
	}
}

// assertRuntimeUnsupported verifies the JSON bind path rejects the cold-kind
// case at runtime with the dedicated cold-case error naming the target-type
// restriction.
func assertRuntimeUnsupported(t *testing.T, err error, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected runtime unsupported error, got nil", label)
	}
	if !strings.Contains(err.Error(), "target type not supported") {
		t.Errorf("%s: err=%q, want substring %q", label, err, "target type not supported")
	}
}

func TestColdCaseSiblingValue_Supported(t *testing.T) {
	src := `{"type":"raw","data":{"a":1,"b":2}}`

	// Unmarshal (JSON bind) and UnmarshalValue (tape-bind) both bind the
	// value.Value case by aliasing the "data" sub-tree into the case slot.
	var u coldCaseSiblingValue
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv coldCaseSiblingValue
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []coldCaseSiblingValue{u, uv} {
		vv, ok := h.Data.(value.Value)
		if !ok {
			t.Fatalf("Data = %T, want value.Value", h.Data)
		}
		if vv.Type() != value.KindObject {
			t.Fatalf("Data.Type = %v, want Object", vv.Type())
		}
		a := vv.Get("a")
		if n, ok := a.Int(); !ok || n != 1 {
			t.Errorf("Data.a = %d (ok=%v), want 1", n, ok)
		}
		b := vv.Get("b")
		if n, ok := b.Int(); !ok || n != 2 {
			t.Errorf("Data.b = %d (ok=%v), want 2", n, ok)
		}
	}
}

func TestColdCaseSiblingRaw_Rejected(t *testing.T) {
	src := `{"type":"raw","data":{"a":1}}`

	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv coldCaseSiblingRaw
	err = UnmarshalValue(val, &uv)
	assertTapeBindUnsupported(t, err, "variant case has unsupported cold kind", ".data")

	var u coldCaseSiblingRaw
	err = Unmarshal([]byte(src), &u)
	assertRuntimeUnsupported(t, err, "sibling RawMessage")
}

func TestColdCaseKindofValue_Supported(t *testing.T) {
	src := `{"data":{"a":1}}`

	// kindof object case binds a JSON object as value.Value via both paths.
	var u coldCaseKindofValue
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv coldCaseKindofValue
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []coldCaseKindofValue{u, uv} {
		vv, ok := h.Data.(value.Value)
		if !ok {
			t.Fatalf("Data = %T, want value.Value", h.Data)
		}
		if vv.Type() != value.KindObject {
			t.Fatalf("Data.Type = %v, want Object", vv.Type())
		}
		a := vv.Get("a")
		if n, ok := a.Int(); !ok || n != 1 {
			t.Errorf("Data.a = %d (ok=%v), want 1", n, ok)
		}
	}
}

// TestColdCaseInlineRaw_BuildRejected verifies the inline-variant build-time
// gate: inline cases must be structs (the case unfolds into the host layout),
// so a json.RawMessage case (slice kind) is rejected at NewParser time.
func TestColdCaseInlineRaw_BuildRejected(t *testing.T) {
	_, err := NewParser[coldCaseInlineRaw]()
	if err == nil {
		t.Fatal("NewParser: expected build error for non-struct inline case, got nil")
	}
	if !strings.Contains(err.Error(), "must be a struct") {
		t.Errorf("err=%q, want substring %q", err, "must be a struct")
	}
}

// TestColdCaseInlineValue_Supported verifies an inline value.Value case binds
// via both Unmarshal (JSON path) and UnmarshalValue (tape-bind cold-start).
// The tape-bind t_document_start inline-variant intercept runs pass-1 (walk
// as host type) → pass-2 (walk as case type, aliasing the buffered host-struct
// tape into the case slot) → t_inline_pass2_close (write eface). The resulting
// value.Value captures the whole host struct.
func TestColdCaseInlineValue_Supported(t *testing.T) {
	// Inline variant unfolds case fields into the host; value.Value has no
	// fields, so pass-1 binds only the disc and pass-2 aliases the whole
	// buffered host-struct tape into the case slot. Extra keys ("extra") are
	// not host fields, so pass-1 skips them; pass-2 captures the whole tape.
	src := `{"type":"raw","extra":{"a":1}}`

	var u coldCaseInlineValue
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv coldCaseInlineValue
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []coldCaseInlineValue{u, uv} {
		vv, ok := h.Data.(value.Value)
		if !ok {
			t.Fatalf("Data = %T, want value.Value", h.Data)
		}
		if vv.Type() != value.KindObject {
			t.Fatalf("Data.Type = %v, want Object", vv.Type())
		}
		// Inline pass-2 re-walks the whole buffered host struct tape as the
		// case type, so the Value captures the disc plus the extra field.
		typ := vv.Get("type")
		if s, ok := typ.Str(); !ok || s != "raw" {
			t.Errorf("Data.type = %q (ok=%v), want %q", s, ok, "raw")
		}
		extra := vv.Get("extra")
		a := extra.Get("a")
		if n, ok := a.Int(); !ok || n != 1 {
			t.Errorf("Data.extra.a = %d (ok=%v), want 1", n, ok)
		}
	}
}

// TestColdCaseInlineValue_OutOfOrder verifies the inline intercept's pass-1
// handles a disc that arrives after the case content (pass-1 walks the whole
// host struct before t_inline_post_walk reads the disc, so ordering is free).
func TestColdCaseInlineValue_OutOfOrder(t *testing.T) {
	src := `{"extra":{"a":1},"type":"raw"}`
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv coldCaseInlineValue
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	vv, ok := uv.Data.(value.Value)
	if !ok {
		t.Fatalf("Data = %T, want value.Value", uv.Data)
	}
	typ := vv.Get("type")
	if s, ok := typ.Str(); !ok || s != "raw" {
		t.Errorf("Data.type = %q (ok=%v), want %q", s, ok, "raw")
	}
	extra := vv.Get("extra")
	a := extra.Get("a")
	if n, ok := a.Int(); !ok || n != 1 {
		t.Errorf("Data.extra.a = %d (ok=%v), want 1", n, ok)
	}
}

// TestColdCaseInlineValue_DiscErrors verifies the inline intercept routes
// unknown/unresolvable discriminators through t_inline_post_walk's error yield,
// producing a VariantError (not a panic or silent nil Data) via UnmarshalValue.
//
// Both inputs carry a discriminator key, which is what makes them errors: an
// absent key selects nothing and is quiet (TestColdCaseInlineValue_AbsentDisc).
func TestColdCaseInlineValue_DiscErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"unknown-disc", `{"type":"nope","extra":1}`, "unknown discriminator"},
		{"empty-disc", `{"type":"","extra":1}`, "missing discriminator"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var uv coldCaseInlineValue
			err = UnmarshalValue(val, &uv)
			if err == nil {
				t.Fatalf("expected error, got nil (Data=%v)", uv.Data)
			}
			var verr *VariantError
			if !errors.As(err, &verr) {
				t.Fatalf("want *VariantError, got %T: %v", err, err)
			}
			if !strings.Contains(verr.Error(), c.want) {
				t.Errorf("err=%q, want substring %q", verr, c.want)
			}
		})
	}
}

// TestColdCaseInlineValue_AbsentDisc: with no discriminator key the intercept
// selects no case and leaves Data nil, on the tape path as on the JSON path.
func TestColdCaseInlineValue_AbsentDisc(t *testing.T) {
	val, err := dom.Parse([]byte(`{"extra":1}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv coldCaseInlineValue
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if uv.Data != nil {
		t.Errorf("Data = %v, want nil", uv.Data)
	}
}

// TestColdCasePointerCaseStillSupported is the control: a Pointer case
// (cold-kind but exempt via the PTR chain) must still bind through both paths.
// Pins that the cold-kind rejection does not over-reach to Pointer cases.
func TestColdCasePointerCaseStillSupported(t *testing.T) {
	// Reuse the existing ptrMap envelope which has a *variantUser case.
	src := `{"type":"ptruser","data":{"name":"Grace","role":"admin"}}`
	var u variantEnvelopePtrMap
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal pointer case: %v", err)
	}
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv variantEnvelopePtrMap
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue pointer case: %v", err)
	}
}

// TestUnmarshalValueRootValue verifies UnmarshalValue binds a root value.Value
// by aliasing the whole input tape (tidx=0). Covers object root and scalar root
// (non-container tape exercises the root branch without object/array dispatch).
func TestUnmarshalValueRootValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kind value.Kind
	}{
		{"object", `{"a":1,"b":[2,3],"c":"hi"}`, value.KindObject},
		{"scalar-string", `"hello"`, value.KindString},
		{"scalar-number", `42`, value.KindNumber},
		{"array", `[1,2,3]`, value.KindArray},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var out value.Value
			if err := UnmarshalValue(val, &out); err != nil {
				t.Fatalf("UnmarshalValue: %v", err)
			}
			if !out.Valid() {
				t.Fatal("result not valid")
			}
			if out.Type() != c.kind {
				t.Fatalf("Type = %v, want %v", out.Type(), c.kind)
			}
			// Round-trip: the aliased tape must serialize back to the input.
			if got := out.String(); got != c.src {
				t.Errorf("String() = %q, want %q", got, c.src)
			}
		})
	}
}

// TestUnmarshalValueRootValue_Null verifies a null root produces an invalid
// (zero) value.Value rather than a panic or stale alias.
func TestUnmarshalValueRootValue_Null(t *testing.T) {
	val, err := dom.Parse([]byte(`null`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var out value.Value
	if err := UnmarshalValue(val, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if out.Valid() {
		t.Errorf("null root should produce invalid Value, got valid (%+v)", out)
	}
}

// --- any-target case passthrough ---

type kindofAnyCaseHost struct {
	Data any `json:"data" vjson:"kindof"`
}

func init() {
	vbind.DefineKindofCases[kindofAnyCaseHost, struct {
		bool   any
		number any
		string any
		array  any
		object any
	}]()
}

type variantAnyCaseHost struct {
	Kind string `json:"kind"`
	Data any    `json:"data" vjson:"variant=kind"`
}

func init() {
	vbind.DefineVariantCases[variantAnyCaseHost, struct {
		list []string
	}]()
}

type variantAnyDefaultHost struct {
	Kind string `json:"kind"`
	Data any    `json:"data" vjson:"variant=kind"`
}

func init() {
	vbind.DefineVariantCases[variantAnyDefaultHost, struct {
		list []string
		_    any
	}]()
}

// TestKindofAnyCasePassthrough verifies an any-target case binds the field
// with the default any boxing on both engines: each JSON kind produces the
// same concrete representation a plain any field would.
func TestKindofAnyCasePassthrough(t *testing.T) {
	cases := []struct {
		src  string
		want any
	}{
		{`{"data":true}`, true},
		{`{"data":1}`, float64(1)},
		{`{"data":"s"}`, "s"},
		{`{"data":[1,"a"]}`, []any{float64(1), "a"}},
		{`{"data":{"k":1}}`, map[string]any{"k": float64(1)}},
		{`{"data":null}`, nil},
	}
	for _, c := range cases {
		var u kindofAnyCaseHost
		if err := Unmarshal([]byte(c.src), &u); err != nil {
			t.Fatalf("Unmarshal(%s): %v", c.src, err)
		}
		val, err := dom.Parse([]byte(c.src))
		if err != nil {
			t.Fatalf("dom.Parse(%s): %v", c.src, err)
		}
		var uv kindofAnyCaseHost
		if err := UnmarshalValue(val, &uv); err != nil {
			t.Fatalf("UnmarshalValue(%s): %v", c.src, err)
		}
		for i, h := range []kindofAnyCaseHost{u, uv} {
			if fmt.Sprintf("%T:%v", h.Data, h.Data) != fmt.Sprintf("%T:%v", c.want, c.want) {
				t.Errorf("path %d %s: Data = %T(%v), want %T(%v)", i, c.src, h.Data, h.Data, c.want, c.want)
			}
		}
	}
}

// TestVariantAnyCasePassthrough exercises an any-target variant case through
// the immediate dispatch (disc first), the phase2 descent (value before disc),
// and the tape walk (UnmarshalValue).
func TestVariantAnyCasePassthrough(t *testing.T) {
	// The any target must be registered as a case to be selected; reuse a
	// dedicated host so the lookup resolves to any rather than []string.
	type host struct {
		Kind string `json:"kind"`
		Data any    `json:"data" vjson:"variant=kind"`
	}
	// Registration is process-wide and keyed by host type, so a local type is
	// safe here.
	vbind.DefineVariantCases[host, struct {
		fallback any
	}]()

	for _, src := range []string{
		`{"kind":"fallback","data":{"x":1}}`,
		`{"data":[9],"kind":"fallback"}`,
		`{"data":"str","kind":"fallback"}`,
	} {
		var u host
		if err := Unmarshal([]byte(src), &u); err != nil {
			t.Fatalf("Unmarshal(%s): %v", src, err)
		}
		val, err := dom.Parse([]byte(src))
		if err != nil {
			t.Fatalf("dom.Parse(%s): %v", src, err)
		}
		var uv host
		if err := UnmarshalValue(val, &uv); err != nil {
			t.Fatalf("UnmarshalValue(%s): %v", src, err)
		}
		for i, h := range []host{u, uv} {
			var want any
			switch {
			case strings.Contains(src, `"x":1`):
				want = map[string]any{"x": float64(1)}
			case strings.Contains(src, `[9]`):
				want = []any{float64(9)}
			default:
				want = "str"
			}
			if fmt.Sprintf("%T:%v", h.Data, h.Data) != fmt.Sprintf("%T:%v", want, want) {
				t.Errorf("path %d %s: Data = %T(%v), want %T(%v)", i, src, h.Data, h.Data, want, want)
			}
			if h.Kind != "fallback" {
				t.Errorf("%s: Kind = %q, want %q", src, h.Kind, "fallback")
			}
		}
	}
}

// TestVariantAnyDefaultPassthrough verifies a default case typed any falls
// back to the default any boxing for unmatched discriminator values.
func TestVariantAnyDefaultPassthrough(t *testing.T) {
	var u variantAnyDefaultHost
	if err := Unmarshal([]byte(`{"kind":"nope","data":[7]}`), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, ok := u.Data.([]any); !ok || len(got) != 1 || got[0] != float64(7) {
		t.Errorf("Data = %T(%v), want []interface{}([7])", u.Data, u.Data)
	}
	val, err := dom.Parse([]byte(`{"data":{"n":1},"kind":"nope"}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv variantAnyDefaultHost
	if err := UnmarshalValue(val, &uv); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if got, ok := uv.Data.(map[string]any); !ok || got["n"] != float64(1) {
		t.Errorf("Data = %T(%v), want map[string]interface{}{n:1}", uv.Data, uv.Data)
	}
}

// TestVariantAnyCaseErrorParity verifies a mismatch inside an any-target case
// descent reports the case type rather than a cascading syntax error. The tape
// path's value name stays "json" (tape offsets carry no source byte), so only
// the error kind and type are compared.
func TestVariantAnyCaseErrorParity(t *testing.T) {
	var u variantAnyCaseHost
	uerr := Unmarshal([]byte(`{"data":[1],"kind":"list"}`), &u)
	var typErr *UnmarshalTypeError
	if !errors.As(uerr, &typErr) || typErr.Type == nil || typErr.Type.String() != "[]string" {
		t.Fatalf("Unmarshal err = %v, want *UnmarshalTypeError with type []string", uerr)
	}
	val, err := dom.Parse([]byte(`{"data":[1],"kind":"list"}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv variantAnyCaseHost
	verr := UnmarshalValue(val, &uv)
	if !errors.As(verr, &typErr) || typErr.Type == nil || typErr.Type.String() != "[]string" {
		t.Fatalf("UnmarshalValue err = %v, want *UnmarshalTypeError with type []string", verr)
	}
}
