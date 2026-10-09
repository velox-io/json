package tests

import (
	"encoding/json"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
)

type tagMisspelledOmit struct {
	A int `json:"a,omit_empty"` //nolint:staticcheck // deliberate misspelling
}

type tagMisspelledCase struct {
	A int `json:"a,OmitEmpty"` //nolint:staticcheck // deliberate misspelling
}

type tagMisspelledString struct {
	A int `json:"a,STRING"` //nolint:staticcheck // deliberate misspelling
}

type tagTwoReserve struct {
	Name string      `json:"name"`
	A    value.Value `json:",embed"`
	B    value.Value `json:",embed"`
}

type tagRetiredReserve struct {
	Name string      `json:"name"`
	Rest value.Value `json:"-" vjson:"unknown"`
}

// A tag option that differs from a known one only in case or an underscore
// is a typo that would silently change the field's behavior, and a struct
// whose unknown keys have nowhere unambiguous to go has no layout. Both
// directions refuse such a type with a message naming the fix, where
// encoding/json ignores the typo.
func TestStructTag_RejectedShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		dst  any
		want string
	}{
		{"omit_empty", new(tagMisspelledOmit), "specify `omitempty` instead"},
		{"OmitEmpty", new(tagMisspelledCase), "specify `omitempty` instead"},
		{"STRING", new(tagMisspelledString), "specify `string` instead"},
		{"two reserve fields", new(tagTwoReserve), "at most one field"},
		{"retired reserve spelling", new(tagRetiredReserve), "now spelled `json:\",embed\"`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := vjson.Unmarshal([]byte(`{"a":1,"name":"n"}`), tc.dst); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Unmarshal error = %v, want it to contain %q", err, tc.want)
			}
			if _, err := vjson.Marshal(tc.dst); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Marshal error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// An option that resembles no known one is ignored, as encoding/json ignores
// it; a space before an option makes it such an option.
func TestStructTag_UnknownOptionIgnored(t *testing.T) {
	type host struct {
		A int `json:"a,unknown"`    //nolint:staticcheck // deliberate unknown option
		B int `json:"b, omitempty"` //nolint:govet // deliberate space before the option
		C int `json:"c,"`           //nolint:staticcheck // deliberate trailing comma
	}
	in := host{}
	want, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := vjson.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Marshal = %s, encoding/json %s", got, want)
	}
	var std, dec host
	doc := []byte(`{"a":1,"b":2,"c":3}`)
	if err := json.Unmarshal(doc, &std); err != nil {
		t.Fatal(err)
	}
	if err := vjson.Unmarshal(doc, &dec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dec != std {
		t.Errorf("Unmarshal = %+v, encoding/json %+v", dec, std)
	}
}
