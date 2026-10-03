package venc

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/velox-io/json/native/encvm"
)

// A time wrapper is a struct which embeds a time.Time at offset 0 and takes
// MarshalJSON from it, as github.Timestamp and go-github's Timestamp do. The
// embedded value leads the layout, so the native time op encodes it; a wrapper
// which declares its own MarshalJSON, as metav1.Time does, must stay on the
// fallback that calls the method.

type twWrap struct{ time.Time }

type twExtra struct { // extra fields are ignored by the promoted method
	time.Time
	Note string
}

type twNested struct{ twWrap } // the time.Time is one embed deeper: not taken

type twPtrEmbed struct{ *time.Time } // pointer embed: not taken

type twNotEmbed struct {
	Time time.Time // named field, not an embed
	Note string
}

type twOverride struct{ time.Time }
type twOverridePtr struct{ time.Time }

func (twOverride) MarshalJSON() ([]byte, error)     { return []byte(`"value"`), nil }
func (*twOverridePtr) MarshalJSON() ([]byte, error) { return []byte(`"ptr"`), nil }

// twMetaV1 is the shape of metav1.Time: the zero time encodes as null and the
// rest in microsecond precision, which the native time op would get wrong.
type twMetaV1 struct{ time.Time }

func (t twMetaV1) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte(`null`), nil
	}
	return json.Marshal(t.UTC().Format("2006-01-02T15:04:05.000000Z07:00"))
}

func TestIsTimeWrapperPredicate(t *testing.T) {
	cases := []struct {
		name string
		rt   reflect.Type
		want bool
	}{
		{"wrapper", reflect.TypeFor[twWrap](), true},
		{"wrapper with extra fields", reflect.TypeFor[twExtra](), true},
		{"one embed deeper", reflect.TypeFor[twNested](), false},
		{"pointer embed", reflect.TypeFor[twPtrEmbed](), false},
		{"named field, no embed", reflect.TypeFor[twNotEmbed](), false},
		{"override on value", reflect.TypeFor[twOverride](), false},
		{"override on pointer", reflect.TypeFor[twOverridePtr](), false},
		{"time.Time itself", reflect.TypeFor[time.Time](), false},
		{"not a struct", reflect.TypeFor[int](), false},
	}
	for _, c := range cases {
		if got := isTimeWrapper(c.rt); got != c.want {
			t.Errorf("%s: isTimeWrapper = %v, want %v", c.name, got, c.want)
		}
	}
}

// blueprintHasOp reports whether the compiled blueprint contains the opcode.
func blueprintHasOp(t *testing.T, rt reflect.Type, op uint16) bool {
	t.Helper()
	bp := EncTypeInfoOf(rt).getBlueprint()
	if bp == nil {
		t.Fatalf("%s: no blueprint", rt)
	}
	for pc := int32(0); pc < int32(len(bp.Ops)); {
		hdr := opHdrAt(bp.Ops, pc)
		if hdr.OpType == op {
			return true
		}
		if isLongOp(hdr.OpType) {
			pc += 16
		} else {
			pc += 8
		}
	}
	return false
}

// The engagement guard: a wrapper must compile to the native time op, an
// override to the fallback, or the parity tests would pass on either path.
func TestTimeWrapperBlueprintOp(t *testing.T) {
	if !blueprintHasOp(t, reflect.TypeFor[twWrap](), opTime) {
		t.Error("twWrap: blueprint has no TIME op")
	}
	if !blueprintHasOp(t, reflect.TypeFor[twExtra](), opTime) {
		t.Error("twExtra: blueprint has no TIME op")
	}
	if !blueprintHasOp(t, reflect.TypeFor[twOverride](), opFallback) {
		t.Error("twOverride: blueprint has no FALLBACK op")
	}
	if blueprintHasOp(t, reflect.TypeFor[twOverride](), opTime) {
		t.Error("twOverride: blueprint must not take the TIME op")
	}
	if !blueprintHasOp(t, reflect.TypeFor[twMetaV1](), opFallback) {
		t.Error("twMetaV1: blueprint has no FALLBACK op")
	}
}

type twDoc struct {
	Created twWrap    `json:"created"`
	Closed  *twWrap   `json:"closed"`
	Notes   *twWrap   `json:"notes"` // nil pointer: null without a call
	Extra   twExtra   `json:"extra"`
	Items   []twWrap  `json:"items"`
	Ptrs    []*twWrap `json:"ptrs"`          // one nil element: null
	When    twWrap    `json:"when,omitzero"` // zero: elided
	Seen    twWrap    `json:"seen,omitzero"` // non-zero: kept
}

func TestTimeWrapperEncodeParity(t *testing.T) {
	utc := time.Date(2026, 9, 24, 12, 30, 5, 123456789, time.UTC)
	whole := time.Date(2026, 9, 24, 12, 30, 5, 0, time.UTC)
	shanghai := utc.In(time.FixedZone("CST", 8*3600))
	wrap := func(t time.Time) *twWrap { return &twWrap{Time: t} }

	v := twDoc{
		Created: twWrap{utc},
		Closed:  wrap(shanghai),
		Notes:   nil,
		Extra:   twExtra{Time: whole, Note: "ignored by the promoted method"},
		Items:   []twWrap{{whole}, {shanghai}},
		Ptrs:    []*twWrap{wrap(whole), nil},
		When:    twWrap{},
		Seen:    twWrap{whole},
	}
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("doc:\n got:  %s\n want: %s", got, want)
	}

	// root positions
	if got, err = Marshal(twWrap{utc}); err != nil {
		t.Fatal(err)
	}
	if want, err = json.Marshal(twWrap{utc}); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("root value: got %s, want %s", got, want)
	}
	var nilWrap *twWrap
	if got, err = Marshal(nilWrap); err != nil {
		t.Fatal(err)
	}
	if want, err = json.Marshal(nilWrap); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("root nil: got %s, want %s", got, want)
	}
}

// A complex timezone (with DST) yields the native time op to Go in both the
// time.Time path and the wrapper path; the output must hold either way.
func TestTimeWrapperDSTZoneParity(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata not available")
	}
	type doc struct {
		A twWrap    `json:"a"`
		B time.Time `json:"b"`
	}
	v := doc{
		A: twWrap{time.Date(2026, 7, 4, 12, 0, 0, 42, loc)},
		B: time.Date(2026, 1, 4, 12, 0, 0, 42, loc),
	}
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("dst:\n got:  %s\n want: %s", got, want)
	}
}

func TestTimeWrapperOverrideStaysCustom(t *testing.T) {
	utc := time.Date(2026, 9, 24, 12, 30, 5, 0, time.UTC)
	type doc struct {
		A twOverride     `json:"a"`
		B *twOverridePtr `json:"b"`
		C twMetaV1       `json:"c"` // zero: null, not "0001-01-01T00:00:00Z"
		D twMetaV1       `json:"d"`
	}
	v := doc{A: twOverride{utc}, B: &twOverridePtr{utc}, C: twMetaV1{}, D: twMetaV1{utc}}
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("override:\n got:  %s\n want: %s", got, want)
	}
	if string(got) != `{"a":"value","b":"ptr","c":null,"d":"2026-09-24T12:30:05.000000Z"}` {
		t.Fatalf("override: the declared methods did not run: %s", got)
	}
}

// The native encoder writes the wrapper without an allocation, as it does a
// time.Time: no MarshalJSON call with its intermediate buffer.
func TestTimeWrapperNativeAllocs(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	utc := time.Date(2026, 9, 24, 12, 30, 5, 123456789, time.UTC)
	type doc struct {
		A twWrap `json:"a"`
		B twWrap `json:"b"`
	}
	v := doc{A: twWrap{utc}, B: twWrap{utc}}
	if n := testing.AllocsPerRun(100, func() {
		if _, err := Marshal(v); err != nil {
			t.Fatal(err)
		}
	}); n > 1 {
		t.Errorf("wrapper marshal allocs = %v, want <= 1 (buffer only)", n)
	}
}
