package tests

import (
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/native/encvm"
)

type strTagMarshaler int

func (strTagMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"m"`), nil }

type strTagTextMarshaler int

func (strTagTextMarshaler) MarshalText() ([]byte, error) { return []byte("t"), nil }

// strTagCase mixes `,string` on quotable kinds with `,string` on types that
// marshal themselves, which encoding/json leaves alone.
type strTagCase struct {
	N  int                  `json:"n,string"`
	S  string               `json:"s,string"`
	M  strTagMarshaler      `json:"m,string"`
	P  *strTagMarshaler     `json:"p,string"`
	T  strTagTextMarshaler  `json:"t,string"`
	TP *strTagTextMarshaler `json:"tp,string"`
}

// strTagHot keeps the embed field within the native offset limit, so the
// case body compiles into the encoding plan.
type strTagHot struct {
	Kind string `json:"kind"`
	Body any    `json:",embed" vjson:"variant=kind"`
}

// strTagCold pads the embed field past the native offset limit, so the case
// body is unfolded in Go.
type strTagCold struct {
	Pad  [70000]byte `json:"-"`
	Kind string      `json:"kind"`
	Body any         `json:",embed" vjson:"variant=kind"`
}

func init() {
	vjson.DefineVariantCases[strTagHot, struct {
		_ strTagCase `case:"c"`
	}]()
	vjson.DefineVariantCases[strTagCold, struct {
		_ strTagCase `case:"c"`
	}]()
}

func newStrTagCase() strTagCase {
	m, tm := strTagMarshaler(2), strTagTextMarshaler(2)
	return strTagCase{N: 5, S: "x", M: 1, P: &m, T: 1, TP: &tm}
}

func TestMarshal_StringTagMatchesStd(t *testing.T) {
	c := newStrTagCase()
	want, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := vjson.Marshal(&c); err != nil || string(got) != string(want) {
		t.Fatalf("Marshal = %s (err %v), want %s", got, err, want)
	}
}

// TestMarshal_StringTagInlineVariant pins that an inline variant case applies
// `,string` the same way whether its body is compiled or unfolded in Go.
func TestMarshal_StringTagInlineVariant(t *testing.T) {
	if !encvm.Available {
		t.Skip("the interpreter cannot yet unfold an inline case that holds a Go-encoded field")
	}
	c := newStrTagCase()
	body, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"c",` + string(body[1:])
	for name, v := range map[string]any{
		"compiled": &strTagHot{Kind: "c", Body: c},
		"unfold":   &strTagCold{Kind: "c", Body: c},
	} {
		if got, err := vjson.Marshal(v); err != nil || string(got) != want {
			t.Errorf("%s: Marshal = %s (err %v), want %s", name, got, err, want)
		}
	}
}
