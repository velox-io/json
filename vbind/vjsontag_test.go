package vbind

import (
	"reflect"
	"strings"
	"testing"

	"github.com/velox-io/json/typ"
	"github.com/velox-io/json/value"
)

// The `vjson` tag is the single entry point for every velox-only option, so a
// malformed option set is detectable: an option nobody recognizes can be
// reported instead of ignored. These tests pin that reporting, because the whole
// reason for one key is that a typo must not silently change behavior.
//
// Field layout is not part of this vocabulary. It is spelled `json:",embed"` and
// handled during struct-field collection, so ParseVJSONTag never sees it.

func TestParseVJSONTag(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  string
		want typ.VJSONTag
	}{
		{"absent", `json:"a"`, typ.VJSONTag{}},
		{"variant", `vjson:"variant=type"`, typ.VJSONTag{Present: true, HasVariant: true, Variant: "type"}},
		{"variant with embed", `json:",embed" vjson:"variant=type"`, typ.VJSONTag{Present: true, HasVariant: true, Variant: "type"}},
		{"kindof", `vjson:"kindof"`, typ.VJSONTag{Present: true, Kindof: true}},
		{"embed alone leaves vjson absent", `json:",embed"`, typ.VJSONTag{}},
		{"empty value", `vjson:"variant="`, typ.VJSONTag{Present: true, HasVariant: true, Variant: ""}},
		{"spaces tolerated", `vjson:" variant=type "`, typ.VJSONTag{Present: true, HasVariant: true, Variant: "type"}},
		// An option that takes no value must not silently accept one, and vice
		// versa: both are reported verbatim rather than partially honored.
		{"misspelled", `vjson:"variant=type,kindoff"`, typ.VJSONTag{Present: true, HasVariant: true, Variant: "type", Unrecognized: []string{"kindoff"}}},
		{"value on flag option", `vjson:"kindof=x"`, typ.VJSONTag{Present: true, Unrecognized: []string{"kindof=x"}}},
		{"variant without value", `vjson:"variant"`, typ.VJSONTag{Present: true, Unrecognized: []string{"variant"}}},
		// The old spellings moved into the json tag, so they are now ordinary
		// unrecognized options rather than silently honored ones.
		{"retired inline", `vjson:"variant=type,inline"`, typ.VJSONTag{Present: true, HasVariant: true, Variant: "type", Unrecognized: []string{"inline"}}},
		{"retired unknown", `vjson:"unknown"`, typ.VJSONTag{Present: true, Unrecognized: []string{"unknown"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := typ.ParseVJSONTag(reflect.StructTag(tc.tag))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseVJSONTag(%q)\n got %+v\nwant %+v", tc.tag, got, tc.want)
			}
		})
	}
}

type vjBadOption struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type,inlien"`
}

type vjVariantPlusKindof struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type,kindof"`
}

// `json:",embed"` on an interface promotes the fields of whichever case the
// discriminator selects, so it needs a discriminator to select with.
type vjEmbedWithoutVariant struct {
	Data any `json:",embed"`
}

type vjUnknownPlusVariant struct {
	Type string      `json:"type"`
	Rest value.Value `json:",embed" vjson:"variant=type"`
}

// A field with no variant at all still gets its options checked, so a stray
// option cannot hide on an ordinary field.
type vjBadOptionPlainField struct {
	Name string `json:"name" vjson:"nonsense"`
}

// The retired spellings must fail loudly rather than degrade into a plain field.
type vjRetiredUnknown struct {
	Rest value.Value `json:"-" vjson:"unknown"`
}

func TestVJSONOptionErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		host reflect.Type
		want string
	}{
		{"unrecognized option", reflect.TypeFor[vjBadOption](), "unrecognized vjson option"},
		{"unrecognized on plain field", reflect.TypeFor[vjBadOptionPlainField](), "unrecognized vjson option"},
		{"variant and kindof", reflect.TypeFor[vjVariantPlusKindof](), "both variant and kindof"},
		{"embed without variant", reflect.TypeFor[vjEmbedWithoutVariant](), "without `vjson:\"variant=<disc>\"`"},
		{"embedded Value with variant", reflect.TypeFor[vjUnknownPlusVariant](), "cannot also be a polymorphic target"},
		{"retired unknown option", reflect.TypeFor[vjRetiredUnknown](), "now spelled `json:\",embed\"`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(typ.UniTypeOf(tc.host))
			if err == nil {
				t.Fatalf("Build(%s) succeeded; want error containing %q", tc.host, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Build(%s) error = %v; want it to contain %q", tc.host, err, tc.want)
			}
		})
	}
}

// A reserve-unknown field must stay in the field list so the positional lookup
// blob keeps its indices, yet must never match an input key. Both halves are
// load-bearing, so pin the name it hides behind.
func TestReserveUnknownFieldKeepsSlotUnderReservedName(t *testing.T) {
	type host struct {
		Name string      `json:"name"`
		Rest value.Value `json:",embed"`
		Tail string      `json:"tail"`
	}
	si := typ.UniTypeOf(reflect.TypeFor[host]()).Ext.(*typ.StructTypeInfo)
	var names []string
	for i := range si.Fields {
		names = append(names, si.Fields[i].JSONName)
	}
	want := []string{"name", typ.ReserveUnknownName, "tail"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("field names = %q, want %q", names, want)
	}
	if !strings.ContainsRune(typ.ReserveUnknownName, 0x7f) {
		t.Errorf("ReserveUnknownName %q must contain a byte no JSON key can carry", typ.ReserveUnknownName)
	}
	// A NUL sentinel would be equally unmatchable but vlib refuses to index it,
	// which would break lookup-blob construction for the entire struct.
	if strings.ContainsRune(typ.ReserveUnknownName, 0) {
		t.Errorf("ReserveUnknownName %q must not contain NUL; vlib cannot index such a key", typ.ReserveUnknownName)
	}
	if _, err := Build(typ.UniTypeOf(reflect.TypeFor[host]())); err != nil {
		t.Fatalf("Build: %v", err)
	}
}
