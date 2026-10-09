package tests

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	vjson "github.com/velox-io/json"
)

// A pointer map key with its own MarshalText is named by that method, and ""
// when nil. Any other pointer key is named by the value at the end of its
// pointer chain, matching go1.27's encoding/json; earlier versions reject such
// key types, so those cases live in marshal_ptrkey_go127_test.go. A nil
// pointer on the chain has no name, and neither has a value which cannot be a
// key.

type ptrKeyVal struct{ S string }

func (t ptrKeyVal) MarshalText() ([]byte, error) { return []byte("v:" + t.S), nil }

type ptrKeyRecv struct{ S string }

func (t *ptrKeyRecv) MarshalText() ([]byte, error) { return []byte("p:" + t.S), nil }

// numberKeySelf is a type of pointers which points to itself: the dereference
// chain never reaches a base kind. The standard library stack-overflows here
// instead of erroring.
type numberKeySelf *numberKeySelf

type ptrKeyCase struct {
	name string
	v    any
}

// checkPtrKeyCases compares each case against encoding/json, compact and
// indented.
func checkPtrKeyCases(t *testing.T, cases []ptrKeyCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			want, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatalf("stdlib marshal: %v", err)
			}
			got, err := vjson.Marshal(tt.v)
			if err != nil {
				t.Fatalf("vjson.Marshal error: %v", err)
			}
			if !mapsEqual(got, want) {
				t.Fatalf("vjson.Marshal = %s, want %s", got, want)
			}
			want, err = json.MarshalIndent(tt.v, "", " ")
			if err != nil {
				t.Fatalf("stdlib marshal indent: %v", err)
			}
			got, err = vjson.MarshalIndent(tt.v, "", " ")
			if err != nil {
				t.Fatalf("vjson.MarshalIndent error: %v", err)
			}
			if !mapsEqual(got, want) {
				t.Fatalf("vjson.MarshalIndent = %s, want %s", got, want)
			}
		})
	}
}

// TestMarshalPointerMapKeys covers the keys every supported encoding/json
// version names.
func TestMarshalPointerMapKeys(t *testing.T) {
	tv := ptrKeyVal{"a"}
	tp := ptrKeyRecv{"b"}
	tm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	checkPtrKeyCases(t, []ptrKeyCase{
		{"json.Number", map[json.Number]int{"1.5": 1, "10": 2, "2": 3}},
		{"json.Number not a number", map[json.Number]int{"": 1, "x": 2}},
		{"pointer to text marshaler", map[*ptrKeyVal]int{&tv: 1}},
		{"pointer to ptr-receiver text marshaler", map[*ptrKeyRecv]int{&tp: 1}},
		{"pointer to time.Time", map[*time.Time]int{&tm: 1}},
		{"nil pointer to text marshaler", map[*ptrKeyVal]int{nil: 1}},
		{"nil pointer to ptr-receiver text marshaler", map[*ptrKeyRecv]int{nil: 1}},
	})
}

func TestMarshalPointerMapKeyErrors(t *testing.T) {
	yes := true
	checkPtrKeyErrors(t, []ptrKeyCase{
		{"nil *int key", map[*int]int{nil: 1}},
		{"nil *string key", map[*string]int{nil: 1}},
		{"nil *json.Number key", map[*json.Number]int{nil: 1}},
		{"nil hop in **int key", map[**int]int{new(*int): 1}},
		{"nil hop in **json.Number key", map[**json.Number]int{new(*json.Number): 1}},
		{"nil hop to text marshaler in **ptrKeyVal key", map[**ptrKeyVal]int{new(*ptrKeyVal): 1}},
		{"pointer to bool", map[*bool]int{&yes: 1}},
		{"pointer to struct", map[*struct{ A int }]int{{1}: 1}},
	})
}

// checkPtrKeyErrors asserts that encoding/json and vjson both fail each case.
func checkPtrKeyErrors(t *testing.T, cases []ptrKeyCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if b, err := json.Marshal(tt.v); err == nil {
				t.Fatalf("stdlib marshal = %s, want an error", b)
			}
			if b, err := vjson.Marshal(tt.v); err == nil {
				t.Fatalf("vjson.Marshal = %s, want an error", b)
			}
		})
	}
}

// TestMarshalPointerMapKeyErrorNamesKeyType checks that a key whose chain ends
// at a value which cannot be a key reports the map's own key type.
func TestMarshalPointerMapKeyErrorNamesKeyType(t *testing.T) {
	yes := true
	py := &yes
	_, err := vjson.Marshal(map[**bool]int{&py: 1})
	var ute *vjson.UnsupportedTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("vjson.Marshal error = %v, want *UnsupportedTypeError", err)
	}
	if want := reflect.TypeFor[**bool](); ute.Type != want {
		t.Fatalf("UnsupportedTypeError.Type = %v, want %v", ute.Type, want)
	}
}

// TestMarshalSelfPointingMapKey covers a key type whose pointer chain cycles
// before any base kind. Its entries fail like those of any key which cannot be
// named, and a map without entries has nothing to name. The standard library
// infinitely recurses on the same input, so it is not consulted.
func TestMarshalSelfPointingMapKey(t *testing.T) {
	var self numberKeySelf
	self = &self
	if b, err := vjson.Marshal(map[numberKeySelf]int{self: 1}); err == nil {
		t.Fatalf("vjson.Marshal = %s, want an error", b)
	}
	if b, err := vjson.MarshalIndent(map[numberKeySelf]int{self: 1}, "", " "); err == nil {
		t.Fatalf("vjson.MarshalIndent = %s, want an error", b)
	}
	if b, err := vjson.Marshal(map[numberKeySelf]int{}); err != nil || string(b) != "{}" {
		t.Fatalf("vjson.Marshal(empty) = %s, %v, want {}", b, err)
	}
	if b, err := vjson.Marshal(map[numberKeySelf]int(nil)); err != nil || string(b) != "null" {
		t.Fatalf("vjson.Marshal(nil) = %s, %v, want null", b, err)
	}
}
