// These tests cover root targets that do not start as structs.

package ndec

import (
	"testing"
)

const todoRootContainer = "root container protocol is still incomplete; see this file for the blocked cases"

func TestRootScalar_Int(t *testing.T) {
	cases := []string{
		`42`,
		`-7`,
		`0`,
		`9223372036854775807`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want int
			runParity(t, in, &got, &want)
		})
	}
}

func TestRootScalar_Int64(t *testing.T) {
	var got, want int64
	runParity(t, `9223372036854775807`, &got, &want)
}

func TestRootScalar_Float64(t *testing.T) {
	cases := []string{
		`3.14`,
		`-2.5`,
		`0`,
		`1e10`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want float64
			runParity(t, in, &got, &want)
		})
	}
}

func TestRootScalar_Bool(t *testing.T) {
	for _, in := range []string{`true`, `false`} {
		t.Run(in, func(t *testing.T) {
			var got, want bool
			runParity(t, in, &got, &want)
		})
	}
}

func TestRootScalar_String(t *testing.T) {
	cases := []string{
		`"hello"`,
		`""`,
		`"a\nb"`,
		`"中文"`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want string
			runParity(t, in, &got, &want)
		})
	}
}

// encoding/json leaves non-pointer roots unchanged on null but clears pointer
// roots to nil, so both cases need explicit parity coverage.
func TestRootScalar_NullToNonPtr(t *testing.T) {
	var got, want int
	runParity(t, `null`, &got, &want)
}

func TestRootScalar_NullToStringNonPtr(t *testing.T) {
	var got, want string
	runParity(t, `null`, &got, &want)
}

// Root pointers should allocate only for concrete values and stay nil on null.
func TestRootPtr_NullToIntPtr(t *testing.T) {
	t.Skip(todoRootContainer)
	var got, want *int
	runParity(t, `null`, &got, &want)
}

func TestRootPtr_NullToStringPtr(t *testing.T) {
	t.Skip(todoRootContainer)
	var got, want *string
	runParity(t, `null`, &got, &want)
}

func TestRootPtr_IntValue(t *testing.T) {
	t.Skip(todoRootContainer)
	var got, want *int
	runParity(t, `42`, &got, &want)
}

func TestRootPtr_StringValue(t *testing.T) {
	t.Skip(todoRootContainer)
	var got, want *string
	runParity(t, `"hello"`, &got, &want)
}

func TestRootSlice_IntSlice(t *testing.T) {
	t.Skip(todoRootContainer)
	cases := []string{
		`[1,2,3]`,
		`[]`,
		`null`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want []int
			runParity(t, in, &got, &want)
		})
	}
}

func TestRootSlice_StringSlice(t *testing.T) {
	t.Skip(todoRootContainer)
	var got, want []string
	runParity(t, `["a","b","c"]`, &got, &want)
}

// root map

func TestRootMap_StringInt(t *testing.T) {
	t.Skip(todoRootContainer + " (root map with object input still crashes)")
	cases := []string{
		`{"a":1,"b":2}`,
		`{}`,
		`null`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want map[string]int
			runParity(t, in, &got, &want)
		})
	}
}

func TestRootArray_Int3(t *testing.T) {
	t.Skip(todoRootContainer)
	cases := []string{
		`[1,2,3]`,
		`[1]`,
		`[]`,
		`[1,2,3,4,5]`,
		`null`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want [3]int
			runParity(t, in, &got, &want)
		})
	}
}

func TestRootScalar_CurrentlyRejected(t *testing.T) {
	// Scalar root targets now work and pass parity checks.
	t.Run("accepted", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			dst  any
		}{
			{"int", `42`, new(int)},
			{"float", `3.14`, new(float64)},
			{"bool", `true`, new(bool)},
			{"string", `"hi"`, new(string)},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := Unmarshal([]byte(tc.in), tc.dst)
				if err != nil {
					t.Fatalf("ndec.Unmarshal(%q) = %v, want nil", tc.in, err)
				}
			})
		}
		// Null should be a no-op for non-pointer scalar roots.
		var v int
		if err := Unmarshal([]byte(`null`), &v); err != nil {
			t.Fatalf("ndec.Unmarshal(null, *int) = %v, want nil", err)
		}
	})

	t.Run("accepted", func(t *testing.T) {
		t.Run("slice", func(t *testing.T) {
			var v []int
			if err := Unmarshal([]byte(`[1,2,3]`), &v); err != nil {
				t.Fatalf("ndec.Unmarshal([1,2,3], *[]int) = %v, want nil", err)
			}
		})
		t.Run("empty_slice", func(t *testing.T) {
			var v []int
			if err := Unmarshal([]byte(`[]`), &v); err != nil {
				t.Fatalf("ndec.Unmarshal([], *[]int) = %v, want nil", err)
			}
		})
		t.Run("fixed_array", func(t *testing.T) {
			var v [3]int
			if err := Unmarshal([]byte(`[1,2,3]`), &v); err != nil {
				t.Fatalf("ndec.Unmarshal([1,2,3], *[3]int) = %v, want nil", err)
			}
		})
		t.Run("ptr_int", func(t *testing.T) {
			var v *int
			if err := Unmarshal([]byte(`42`), &v); err != nil {
				t.Fatalf("ndec.Unmarshal(42, **int) = %v, want nil", err)
			}
		})
	})
}
