package tests

import (
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
)

// A reserve-unknown field exists to collect keys the struct does not declare,
// so it is a decode-side construct with no JSON name of its own. Encoding
// spreads the collected members back inline at the field's position, so an
// Unmarshal/Marshal round trip reproduces the input's full key set. It used
// to be spelled `json:"+"`, which made "+" a real key in both directions;
// `json:",embed"` now keeps the field out of velox's key space entirely.
type reserveUnknownHost struct {
	Name string      `json:"name"`
	Rest value.Value `json:",embed"`
	Tail int         `json:"tail"`
}

func TestReserveUnknownSpreadEncoded(t *testing.T) {
	var h reserveUnknownHost
	if err := vjson.Unmarshal([]byte(`{"name":"a","zzz":1,"nested":{"k":[2,3]},"tail":7}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Name != "a" || h.Tail != 7 {
		t.Fatalf("declared fields not bound: %+v", h)
	}
	if !h.Rest.Valid() {
		t.Fatal("reserve-unknown Value not populated")
	}

	got, err := vjson.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"name":"a","zzz":1,"nested":{"k":[2,3]},"tail":7}`
	if string(got) != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}

	// Indent mode keeps the members at the host level: no braces of their own.
	got, err = vjson.MarshalIndent(h, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	wantIndent := "{\n  \"name\": \"a\",\n  \"zzz\": 1,\n  \"nested\": {\n    \"k\": [\n      2,\n      3\n    ]\n  },\n  \"tail\": 7\n}"
	if string(got) != wantIndent {
		t.Errorf("MarshalIndent:\ngot  %q\nwant %q", got, wantIndent)
	}
}

// An empty collection spreads nothing: the host object carries no trace of
// the field, and no stray comma appears between the declared neighbors.
func TestReserveUnknownSpreadEmpty(t *testing.T) {
	var h reserveUnknownHost
	if err := vjson.Unmarshal([]byte(`{"name":"a","tail":7}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, err := vjson.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"name":"a","tail":7}`
	if string(got) != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

// The collected keys must be exactly the ones the struct did not declare.
func TestReserveUnknownCollectsOnlyUndeclared(t *testing.T) {
	var h reserveUnknownHost
	if err := vjson.Unmarshal([]byte(`{"name":"a","zzz":1,"nested":{"k":[2,3]},"tail":7}`), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	rest, err := vjson.Marshal(h.Rest)
	if err != nil {
		t.Fatalf("Marshal(Rest): %v", err)
	}
	const want = `{"zzz":1,"nested":{"k":[2,3]}}`
	if string(rest) != want {
		t.Errorf("Rest = %s, want %s", rest, want)
	}
}
