//go:build go1.27

package tests

import (
	"encoding/json"
	"testing"
)

// go1.27's encoding/json names a pointer map key without its own MarshalText
// by the value at the end of its pointer chain; earlier versions reject the
// key type.

func TestMarshalDerefMapKeys(t *testing.T) {
	three, i, u, s := json.Number("3"), 4, uint8(5), "a"
	pi, pthree := &i, &three
	tv := ptrKeyVal{"a"}
	tvInner := &tv
	checkPtrKeyCases(t, []ptrKeyCase{
		{"pointer to json.Number", map[*json.Number]int{&three: 1}},
		{"pointer to pointer to json.Number", map[**json.Number]int{&pthree: 1}},
		{"pointer to int", map[*int]int{&i: 1}},
		{"pointer to pointer to int", map[**int]int{&pi: 1}},
		{"pointer to uint8", map[*uint8]int{&u: 1}},
		{"pointer to string", map[*string]int{&s: 1}},
		{"pointer to pointer to text marshaler", map[**ptrKeyVal]int{&tvInner: 1}},
	})
}

type wrapPtrKeyMap struct {
	M map[*int]string `json:"m"`
}

// TestMarshalDerefMapKeysInStruct drives the same key naming through a struct
// field, the opMap route the native VM yields to Go from.
func TestMarshalDerefMapKeysInStruct(t *testing.T) {
	i := 7
	checkPtrKeyCases(t, []ptrKeyCase{
		{"struct field", wrapPtrKeyMap{M: map[*int]string{&i: "a"}}},
	})
}

// keyChainK dereferences through keyChainA to keyChainX, the struct holding
// the map, and keyChainL likewise to keyChainY. keyChainX names itself through
// MarshalText, so its map's keys do too; keyChainY has no name, so its map's
// entries fail. Each family is first built from its pointer type, which meets
// that type again while it is still under construction: the keys must be
// named as if the map had been built on its own.
type keyChainA *keyChainX
type keyChainX struct {
	M map[keyChainK]int
}
type keyChainK *keyChainA

func (keyChainX) MarshalText() ([]byte, error) { return []byte("x"), nil }

type keyChainB *keyChainY
type keyChainY struct {
	M map[keyChainL]int
}
type keyChainL *keyChainB

func TestMarshalDerefMapKeyRecursiveBuild(t *testing.T) {
	a := keyChainA(&keyChainX{})
	b := keyChainB(&keyChainY{M: map[keyChainL]int{}})
	// Order matters: the slices are the first to build each family.
	cases := []ptrKeyCase{
		{"first build from keyChainA", []keyChainA{a}},
		{"named key", map[keyChainK]int{&a: 1}},
		{"first build from keyChainB", []keyChainB{b}},
	}
	checkPtrKeyCases(t, cases)
	checkPtrKeyErrors(t, []ptrKeyCase{
		{"unnamed key", map[keyChainL]int{&b: 1}},
	})
}
