package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// deferred_value diff tests. Compare bind.Unmarshal against encoding/json for
// types implementing json.Unmarshaler or encoding.TextUnmarshaler. The native
// state machine captures the raw JSON byte span (UnmarshalJSON) or decodes the
// string into str_arena (TextUnmarshaler), stages an UnmarshalRecord, and drains
// at FLUSH_UNMARSHAL or document_end. Map values with val_contains_unmarshaler
// go through a SlotClass intermediate so the closure's heap writes stay
// GC-visible until the map drain copies them into the runtime map.

// --- Test types ---

// umJSONValue implements json.Unmarshaler by value receiver.
type umJSONValue struct {
	S string
	N int
}

func (u *umJSONValue) UnmarshalJSON(data []byte) error {
	var raw struct {
		S string `json:"s"`
		N int    `json:"n"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.S = raw.S
	u.N = raw.N
	return nil
}

// umJSONPtr implements json.Unmarshaler by pointer receiver.
type umJSONPtr struct {
	V string
}

func (u *umJSONPtr) UnmarshalJSON(data []byte) error {
	u.V = string(data)
	return nil
}

// umText implements encoding.TextUnmarshaler.
type umText struct {
	V string
}

func (u *umText) UnmarshalText(data []byte) error {
	u.V = string(data)
	return nil
}

// umBoth implements both interfaces; UnmarshalJSON wins.
type umBoth struct {
	FromJSON bool
	FromText bool
	Raw      string
}

func (u *umBoth) UnmarshalJSON(data []byte) error {
	u.FromJSON = true
	u.Raw = string(data)
	return nil
}

func (u *umBoth) UnmarshalText(data []byte) error {
	u.FromText = true
	u.Raw = string(data)
	return nil
}

// umErr returns an error from UnmarshalJSON.
type umErr struct{}

func (u *umErr) UnmarshalJSON(data []byte) error {
	return errors.New("umErr: always fails")
}

// --- Helpers ---

func runUmDiff[T any](t *testing.T, in string, dest func() *T) {
	t.Helper()
	jDest := dest()
	vDest := dest()
	errJ := json.Unmarshal([]byte(in), jDest)
	errV := Unmarshal([]byte(in), vDest)
	if (errJ == nil) != (errV == nil) {
		t.Errorf("input=%s: error mismatch json=%v nbind=%v", in, errJ, errV)
		return
	}
	if errJ != nil {
		return
	}
	if !reflect.DeepEqual(jDest, vDest) {
		t.Errorf("input=%s: mismatch\n  json =%+v\n  nbind=%+v", in, jDest, vDest)
	}
}

// --- UnmarshalJSON tests ---

func TestDiffUnmarshalJSON_ValueReceiver(t *testing.T) {
	runUmDiff(t, `{"s":"hello","n":42}`, func() *umJSONValue { return &umJSONValue{} })
	runUmDiff(t, `{"s":"","n":0}`, func() *umJSONValue { return &umJSONValue{} })
}

func TestDiffUnmarshalJSON_PtrReceiver(t *testing.T) {
	runUmDiff(t, `"raw bytes"`, func() *umJSONPtr { return &umJSONPtr{} })
	runUmDiff(t, `123`, func() *umJSONPtr { return &umJSONPtr{} })
	runUmDiff(t, `{"k":1}`, func() *umJSONPtr { return &umJSONPtr{} })
}

func TestDiffUnmarshalJSON_Null(t *testing.T) {
	// null: encoding/json does not call UnmarshalJSON; the slot stays zero.
	runUmDiff(t, `null`, func() *umJSONValue { return &umJSONValue{} })
	runUmDiff(t, `null`, func() *umJSONPtr { return &umJSONPtr{} })
}

func TestDiffUnmarshalJSON_BothInterfaces(t *testing.T) {
	// UnmarshalJSON wins over UnmarshalText.
	runUmDiff(t, `"data"`, func() *umBoth { return &umBoth{} })
	runUmDiff(t, `42`, func() *umBoth { return &umBoth{} })
	// Verify FromJSON is set, FromText is not.
	var v umBoth
	if err := Unmarshal([]byte(`"x"`), &v); err != nil {
		t.Fatal(err)
	}
	if !v.FromJSON || v.FromText {
		t.Errorf("UnmarshalJSON should win: %+v", v)
	}
}

func TestDiffUnmarshalJSON_ErrorPropagation(t *testing.T) {
	runUmDiff(t, `"anything"`, func() *umErr { return &umErr{} })
}

// --- TextUnmarshaler tests ---

func TestDiffUnmarshalText(t *testing.T) {
	runUmDiff(t, `"hello"`, func() *umText { return &umText{} })
	runUmDiff(t, `"with\\tescape"`, func() *umText { return &umText{} })
	runUmDiff(t, `"unicode→←"`, func() *umText { return &umText{} })
}

func TestDiffUnmarshalText_Null(t *testing.T) {
	runUmDiff(t, `null`, func() *umText { return &umText{} })
}

func TestDiffUnmarshalText_NonStringError(t *testing.T) {
	// TextUnmarshaler requires a JSON string; non-string should error.
	in := `123`
	var j, v umText
	errJ := json.Unmarshal([]byte(in), &j)
	errV := Unmarshal([]byte(in), &v)
	if (errJ == nil) != (errV == nil) {
		t.Errorf("error mismatch: json=%v nbind=%v", errJ, errV)
	}
}

// --- Container contexts ---

type structWithUmField struct {
	Name string      `json:"name"`
	Val  umJSONValue `json:"val"`
	Ptr  *umJSONPtr  `json:"ptr"`
}

func TestDiffUnmarshalJSON_StructField(t *testing.T) {
	runUmDiff(t, `{"name":"x","val":{"s":"a","n":1},"ptr":"p"}`, func() *structWithUmField {
		return &structWithUmField{}
	})
}

type sliceOfUm []umJSONValue

func TestDiffUnmarshalJSON_SliceElem(t *testing.T) {
	runUmDiff(t, `[{"s":"a","n":1},{"s":"b","n":2}]`, func() *sliceOfUm {
		return &sliceOfUm{}
	})
}

func TestDiffUnmarshalJSON_LongSlice(t *testing.T) {
	// Build a slice large enough to potentially trigger FLUSH_UNMARSHAL.
	var parts []string
	for i := range 32 {
		parts = append(parts, fmt.Sprintf(`{"s":"item%d","n":%d}`, i, i))
	}
	in := "[" + strings.Join(parts, ",") + "]"
	runUmDiff(t, in, func() *sliceOfUm { return &sliceOfUm{} })
}

type mapOfUm map[string]umJSONValue

func TestDiffUnmarshalJSON_MapValue(t *testing.T) {
	runUmDiff(t, `{"a":{"s":"x","n":1},"b":{"s":"y","n":2}}`, func() *mapOfUm {
		return &mapOfUm{}
	})
}

func TestDiffUnmarshalJSON_LongMap(t *testing.T) {
	var parts []string
	for i := range 32 {
		parts = append(parts, fmt.Sprintf(`"k%d":{"s":"v%d","n":%d}`, i, i, i))
	}
	in := "{" + strings.Join(parts, ",") + "}"
	runUmDiff(t, in, func() *mapOfUm { return &mapOfUm{} })
}

// --- Root Unmarshaler ---

func TestDiffUnmarshalJSON_Root(t *testing.T) {
	runUmDiff(t, `{"s":"root","n":99}`, func() *umJSONValue { return &umJSONValue{} })
	runUmDiff(t, `"root string"`, func() *umJSONPtr { return &umJSONPtr{} })
}

// --- Time.Time (real-world TextUnmarshaler) ---

func TestDiffUnmarshalText_Time(t *testing.T) {
	cases := []string{
		`"2024-01-15T10:30:00Z"`,
		`"2023-12-31T23:59:59+08:00"`,
		`"1970-01-01T00:00:00Z"`,
	}
	for _, in := range cases {
		var j, v time.Time
		errJ := json.Unmarshal([]byte(in), &j)
		errV := Unmarshal([]byte(in), &v)
		if (errJ == nil) != (errV == nil) {
			t.Errorf("input=%s: error mismatch json=%v nbind=%v", in, errJ, errV)
			continue
		}
		if errJ != nil {
			continue
		}
		if !j.Equal(v) {
			t.Errorf("input=%s: time mismatch json=%v nbind=%v", in, j, v)
		}
	}
}

func TestDiffUnmarshalText_TimeInStruct(t *testing.T) {
	type ts struct {
		Created time.Time `json:"created"`
		Name    string    `json:"name"`
	}
	runUmDiff(t, `{"created":"2024-06-15T12:00:00Z","name":"test"}`, func() *ts { return &ts{} })
}

func TestDiffUnmarshalText_TimeInSlice(t *testing.T) {
	type tsSlice []time.Time
	runUmDiff(t, `["2024-01-01T00:00:00Z","2024-06-15T12:30:00Z"]`, func() *tsSlice {
		return &tsSlice{}
	})
}

func TestDiffUnmarshalText_TimeInMap(t *testing.T) {
	type tsMap map[string]time.Time
	runUmDiff(t, `{"a":"2024-01-01T00:00:00Z","b":"2024-06-15T12:30:00Z"}`, func() *tsMap {
		return &tsMap{}
	})
}

func TestDiffUnmarshalText_TimeNull(t *testing.T) {
	type ts struct {
		T time.Time `json:"t"`
	}
	runUmDiff(t, `{"t":null}`, func() *ts { return &ts{} })
}

// --- Byte span fidelity ---

type umCapture struct {
	Data []byte
}

func (u *umCapture) UnmarshalJSON(data []byte) error {
	u.Data = make([]byte, len(data))
	copy(u.Data, data)
	return nil
}

func TestDiffUnmarshalJSON_ByteSpanFidelity(t *testing.T) {
	// Verify the exact bytes passed to UnmarshalJSON match encoding/json.
	cases := []string{
		`{"a":1,"b":2}`,
		`  {"a":1}  `,
		`[1,2,3]`,
		`"string with spaces"`,
		`123.456`,
		`true`,
		`null`,
	}
	for _, in := range cases {
		var j, v umCapture
		_ = json.Unmarshal([]byte(in), &j)
		_ = Unmarshal([]byte(in), &v)
		if string(j.Data) != string(v.Data) {
			t.Errorf("input=%q: byte span mismatch\n  json=%q\n  nbind=%q", in, j.Data, v.Data)
		}
	}
}

// --- Recursive Unmarshaler ---

type umRecursive struct {
	V string
}

func (u *umRecursive) UnmarshalJSON(data []byte) error {
	// Call json.Unmarshal internally, which may trigger a nested bind parse
	// for sub-values (exercises re-entrancy of the shape cache).
	var raw struct {
		V string `json:"v"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.V = raw.V
	return nil
}

func TestDiffUnmarshalJSON_Recursive(t *testing.T) {
	runUmDiff(t, `{"v":"recursive"}`, func() *umRecursive { return &umRecursive{} })
}
