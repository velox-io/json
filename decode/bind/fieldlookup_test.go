package bind

import "testing"

// These exercise the perfect-hash struct-field lookup path in the native
// binder: unescaped keys (raw-src fast path), escaped keys (decode-then-find),
// long field names that force the TABLE tier, and unknown-field skipping.

func TestFieldLookupUnescapedKeys(t *testing.T) {
	type S struct {
		Alpha string `json:"alpha"`
		Beta  int    `json:"beta"`
		Gamma string `json:"gamma"`
	}
	parity3[S](t, "unescaped", `{"alpha":"x","beta":7,"gamma":"z"}`)
	parity3[S](t, "reordered", `{"gamma":"z","alpha":"x","beta":7}`)
}

func TestFieldLookupEscapedKey(t *testing.T) {
	type S struct {
		Name string `json:"name"`
		Val  int    `json:"val"`
	}
	// The key "name" written with a \u escape must decode and still resolve.
	parity3[S](t, "escaped-key", `{"\u006eame":"hi","val":3}`)
	// Escaped key that does not match any field -> unknown, skipped.
	parity3[S](t, "escaped-unknown", `{"\u0071q":1,"name":"hi","val":3}`)
}

func TestFieldLookupLongNamesTable(t *testing.T) {
	// Field names > 63 bytes force the TABLE tier (perfect-hash tiers cap at 63).
	type S struct {
		A string `json:"this_is_a_deliberately_long_json_field_name_exceeding_sixty_three_bytes_aaa"`
		B int    `json:"another_long_field_name_that_also_exceeds_the_sixty_three_byte_threshold_bbb"`
	}
	in := `{"this_is_a_deliberately_long_json_field_name_exceeding_sixty_three_bytes_aaa":"v",` +
		`"another_long_field_name_that_also_exceeds_the_sixty_three_byte_threshold_bbb":9}`
	parity3[S](t, "long-table", in)
}

func TestFieldLookupManyFields(t *testing.T) {
	// A wider field set exercises the GPERF/HAND tiers rather than WINDOW.
	type S struct {
		F1 int    `json:"apiVersion"`
		F2 string `json:"kind"`
		F3 string `json:"namespace"`
		F4 int    `json:"generation"`
		F5 string `json:"resourceVersion"`
		F6 bool   `json:"deleted"`
		F7 string `json:"selfLink"`
		F8 int    `json:"replicas"`
	}
	in := `{"apiVersion":1,"kind":"Pod","namespace":"default","generation":3,` +
		`"resourceVersion":"12345","deleted":true,"selfLink":"/api/v1","replicas":5}`
	parity3[S](t, "many", in)
}

func TestFieldLookupSIMDBoundaries(t *testing.T) {
	type S struct {
		K15 string `json:"aaaaaaaaaaaaaaa"`
		K16 string `json:"bbbbbbbbbbbbbbbb"`
		K31 string `json:"ccccccccccccccccccccccccccccccc"`
		K32 string `json:"dddddddddddddddddddddddddddddddd"`
		K47 string `json:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"`
		K48 string `json:"ffffffffffffffffffffffffffffffffffffffffffffffff"`
		K63 string `json:"ggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"`
		K64 string `json:"hhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhh"`
	}
	in := `{"aaaaaaaaaaaaaaa":"15","bbbbbbbbbbbbbbbb":"16",` +
		`"ccccccccccccccccccccccccccccccc":"31","dddddddddddddddddddddddddddddddd":"32",` +
		`"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee":"47",` +
		`"ffffffffffffffffffffffffffffffffffffffffffffffff":"48",` +
		`"ggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg":"63",` +
		`"hhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhh":"64"}`
	parity3[S](t, "simd-boundaries", in)
}

func TestFieldLookupUnknownField(t *testing.T) {
	type S struct {
		Keep string `json:"keep"`
	}
	parity3[S](t, "unknown-scalar", `{"drop":123,"keep":"ok"}`)
	parity3[S](t, "unknown-object", `{"drop":{"a":1,"b":[1,2,3]},"keep":"ok"}`)
	parity3[S](t, "unknown-array", `{"drop":[1,{"x":1}],"keep":"ok"}`)
}
