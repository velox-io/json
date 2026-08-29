package bind

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/velox-io/json/value"
)

// options_test covers the per-call UnmarshalOption surface. Options must take
// effect on the call that receives them and must not stick to a pooled or reused
// Parser across calls.

// --- WithUseNumber ---

// TestWithUseNumber_BoxesAsJsonNumber confirms the option routes any/interface{}
// numbers through the native BIND_OPT_USE_NUMBER path, which tags the eface
// with json.Number (vbind/build.go NumberType) instead of float64.
func TestWithUseNumber_BoxesAsJsonNumber(t *testing.T) {
	src := `{"v":123}`

	// Default: interface{} numbers decode as float64.
	var def anyField
	if err := Unmarshal([]byte(src), &def); err != nil {
		t.Fatalf("default Unmarshal: %v", err)
	}
	if f, ok := def.V.(float64); !ok || f != 123 {
		t.Fatalf("default V = %T(%v), want float64(123)", def.V, def.V)
	}

	// WithUseNumber: interface{} numbers decode as json.Number.
	var num anyField
	if err := Unmarshal([]byte(src), &num, WithUseNumber()); err != nil {
		t.Fatalf("WithUseNumber Unmarshal: %v", err)
	}
	n, ok := num.V.(json.Number)
	if !ok {
		t.Fatalf("WithUseNumber V = %T, want json.Number", num.V)
	}
	if s := n.String(); s != "123" {
		t.Fatalf("json.Number = %q, want %q", s, "123")
	}
}

// TestWithUseNumber_DoesNotStickAcrossCalls guards the pooled-Parser reset at
// the package Unmarshal entry (bind.go optFlags=0). A prior call's option must
// not leak into a later call that omits it. This is the regression that
// Parser.UseNumber() silently failed: sticky-per-call is the contract here.
func TestWithUseNumber_DoesNotStickAcrossCalls(t *testing.T) {
	src := `{"v":1}`

	var first anyField
	if err := Unmarshal([]byte(src), &first, WithUseNumber()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, ok := first.V.(json.Number); !ok {
		t.Fatalf("first V = %T, want json.Number", first.V)
	}

	// Same destination type → same pooled Parser shape. The option must not
	// survive the pool round-trip.
	var second anyField
	if err := Unmarshal([]byte(src), &second); err != nil {
		t.Fatalf("second: %v", err)
	}
	if _, ok := second.V.(json.Number); ok {
		t.Fatalf("second V = json.Number, want float64 (option leaked across calls)")
	}
}

// TestParser_WithUseNumber_PerCall confirms the Parser.Unmarshal entry also
// resets per call (bind.go optFlags=0), so a Parser reused with and without
// the option behaves per-call, not sticky.
func TestParser_WithUseNumber_PerCall(t *testing.T) {
	p, err := NewParser[anyField]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := []byte(`{"v":42}`)

	var with anyField
	if err := p.Unmarshal(src, &with, WithUseNumber()); err != nil {
		t.Fatalf("with option: %v", err)
	}
	if _, ok := with.V.(json.Number); !ok {
		t.Fatalf("with option V = %T, want json.Number", with.V)
	}

	var without anyField
	if err := p.Unmarshal(src, &without); err != nil {
		t.Fatalf("without option: %v", err)
	}
	if _, ok := without.V.(json.Number); ok {
		t.Fatalf("without option V = json.Number, want float64 (option stuck on Parser)")
	}
}

// --- WithDisallowUnknownFields ---

type disallowTarget struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// TestWithDisallowUnknownFields_RejectsUnknownField confirms the option arms
// the native BIND_OPT_DISALLOW_UNKNOWN check (bind.h), which yields
// BIND_ERR_UNKNOWN_FIELD for a JSON member with no matching Go field.
func TestWithDisallowUnknownFields_RejectsUnknownField(t *testing.T) {
	src := `{"name":"Ada","age":36,"role":"admin"}`

	// Without the option: unknown "role" is silently ignored.
	var lax disallowTarget
	if err := Unmarshal([]byte(src), &lax); err != nil {
		t.Fatalf("lax Unmarshal: %v", err)
	}
	if lax.Name != "Ada" || lax.Age != 36 {
		t.Fatalf("lax = %+v, want {Ada 36}", lax)
	}

	// With the option: unknown "role" is an error.
	var strict disallowTarget
	err := Unmarshal([]byte(src), &strict, WithDisallowUnknownFields())
	if err == nil {
		t.Fatalf("strict Unmarshal: want error for unknown field, got nil")
	}
	// The bind path surfaces this as *UnmarshalTypeError (errors.go BindErrUnknownField).
	var tee *UnmarshalTypeError
	if !errors.As(err, &tee) {
		t.Fatalf("err = %v, want assignable to *UnmarshalTypeError", err)
	}
}

// TestWithDisallowUnknownFields_AcceptsKnownFields confirms the option does
// not reject inputs whose fields all map.
func TestWithDisallowUnknownFields_AcceptsKnownFields(t *testing.T) {
	src := `{"name":"Ada","age":36}`
	var strict disallowTarget
	if err := Unmarshal([]byte(src), &strict, WithDisallowUnknownFields()); err != nil {
		t.Fatalf("strict Unmarshal: %v", err)
	}
	if strict.Name != "Ada" || strict.Age != 36 {
		t.Fatalf("strict = %+v, want {Ada 36}", strict)
	}
}

// TestWithDisallowUnknownFields_DoesNotStickAcrossCalls guards the pooled
// reset: a strict call must not make a later lax call on the same pooled
// Parser reject unknown fields.
func TestWithDisallowUnknownFields_DoesNotStickAcrossCalls(t *testing.T) {
	src := `{"name":"Ada","age":36,"extra":true}`

	var strict disallowTarget
	if err := Unmarshal([]byte(src), &strict, WithDisallowUnknownFields()); err == nil {
		t.Fatalf("strict: want error, got nil")
	}

	var lax disallowTarget
	if err := Unmarshal([]byte(src), &lax); err != nil {
		t.Fatalf("lax after strict: want nil (option leaked), got %v", err)
	}
}

// --- WithStrictScan ---

type strictScanTarget struct {
	S string `json:"s"`
}

type strictScanValueTarget struct {
	V value.Value `json:"v"`
}

func TestWithStrictScan_ValidatesRawInput(t *testing.T) {
	tests := []struct {
		name string
		src  []byte
	}{
		{name: "raw-nul", src: []byte{'{', '"', 's', '"', ':', '"', 'a', 0, 'b', '"', '}'}},
		{name: "invalid-utf8", src: []byte{'{', '"', 's', '"', ':', '"', 0xff, '"', '}'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lax strictScanTarget
			if err := Unmarshal(tt.src, &lax); err != nil {
				t.Fatalf("default scan: %v", err)
			}
			var strict strictScanTarget
			if err := Unmarshal(tt.src, &strict, WithStrictScan()); err == nil {
				t.Fatal("StrictScan accepted invalid raw input")
			}
		})
	}
}

func TestWithStrictScan_AcceptsValidStrings(t *testing.T) {
	for _, src := range []string{
		`{"s":"世界"}`,
		`{"s":"a\nb"}`,
		`{"s":"a\u0001b"}`,
	} {
		var dst strictScanTarget
		if err := Unmarshal([]byte(src), &dst, WithStrictScan()); err != nil {
			t.Fatalf("Unmarshal(%q): %v", src, err)
		}
	}
}

func TestWithStrictScan_CountedScan(t *testing.T) {
	src := []byte{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'}
	var lax strictScanValueTarget
	if err := Unmarshal(src, &lax); err != nil {
		t.Fatalf("default counted scan: %v", err)
	}
	var strict strictScanValueTarget
	if err := Unmarshal(src, &strict, WithStrictScan()); err == nil {
		t.Fatal("StrictScan counted path accepted invalid UTF-8")
	}
}

func TestWithStrictScan_DoesNotStickAcrossParserCalls(t *testing.T) {
	p, err := NewParser[strictScanTarget]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := []byte{'{', '"', 's', '"', ':', '"', 0xff, '"', '}'}
	var strict strictScanTarget
	if err := p.Unmarshal(src, &strict, WithStrictScan()); err == nil {
		t.Fatal("StrictScan accepted invalid UTF-8")
	}
	var lax strictScanTarget
	if err := p.Unmarshal(src, &lax); err != nil {
		t.Fatalf("default scan after StrictScan: %v", err)
	}
}

func TestWithStrictScan_UnmarshalPadded(t *testing.T) {
	src := Pad([]byte{'{', '"', 's', '"', ':', '"', 0xff, '"', '}'})
	var dst strictScanTarget
	if err := UnmarshalPadded(src, &dst, WithStrictScan()); err == nil {
		t.Fatal("StrictScan accepted invalid UTF-8 from padded input")
	}
}

func TestWithStrictScan_UnmarshalValueDoesNotRescan(t *testing.T) {
	src := []byte{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'}
	var doc strictScanValueTarget
	if err := Unmarshal(src, &doc); err != nil {
		t.Fatalf("build Value: %v", err)
	}
	var got string
	if err := UnmarshalValue(doc.V, &got, WithStrictScan()); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if len(got) != 1 || got[0] != 0xff {
		t.Fatalf("got %q, want raw 0xff", got)
	}
}
