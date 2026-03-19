package vjson

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// TestMarshal_StructFieldAndInterfaceRefSameType
//
// Struct A has a concrete field of type B and an interface field (any) that
// dynamically holds a B instance. Both should marshal identically to
// encoding/json.
// ---------------------------------------------------------------------------

func TestMarshal_StructFieldAndInterfaceRefSameType(t *testing.T) {
	type B struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	type A struct {
		Direct B   `json:"direct"`
		Iface  any `json:"iface"`
	}

	b := B{Name: "alice", Age: 30}

	cases := []struct {
		name string
		val  A
	}{
		{"value", A{Direct: b, Iface: b}},
		{"pointer", A{Direct: b, Iface: &b}},
		{"nil_iface", A{Direct: b, Iface: nil}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Marshal(&tc.val)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("mismatch:\n  got:  %s\n  want: %s", got, want)
			}
		})
	}
}
