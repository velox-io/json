package bind

import (
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
)

// An empty JSON object is a struct with zero fields, so its close owes exactly
// what any other close of that type owes: an inline-variant host must settle its
// variant (with no discriminator, that means selecting no case and leaving the
// eface nil), a reserve-unknown host must publish an empty-object Value. Nothing
// about the input being `{}` changes that.
//
// The close is reached from four dispatch sites per path (root, struct field,
// array element, map value) across two paths (JSON bind, tape-bind), and each
// site decides on its own whether to descend. A site that shortcuts `{}` past the
// close silently produces a nil eface or an invalid Value instead, so the matrix
// below drives every site rather than trusting one of them to stand for the rest.
//
// A nil eface is the CORRECT outcome for the inline-variant host here, which is
// why that test cannot detect a skipped close on its own; the reserve-unknown
// host and the empty-discriminator variant carry that weight.

// emptyCloseReserveHost is a reserve-unknown host with all-optional fields, so
// `{}` is valid input and the surviving assertion is about Exts, not an error.
type emptyCloseReserveHost struct {
	Name string      `json:"name"`
	Exts value.Value `json:",embed"`
}

// The four dispatch sites, as (JSON, destination) pairs. Each wraps the host
// under test so the empty object arrives at a different descend site.
type emptyCloseSite struct {
	name string
	src  string
	// dst returns a fresh destination plus a getter for the host instance
	// reached through it, so one table covers both host types.
	inlineDst  func() (any, func(any) inlHost)
	reserveDst func() (any, func(any) emptyCloseReserveHost)
}

func emptyCloseSites() []emptyCloseSite {
	return []emptyCloseSite{
		{
			name: "root",
			src:  `{}`,
			inlineDst: func() (any, func(any) inlHost) {
				return new(inlHost), func(d any) inlHost { return *d.(*inlHost) }
			},
			reserveDst: func() (any, func(any) emptyCloseReserveHost) {
				return new(emptyCloseReserveHost), func(d any) emptyCloseReserveHost {
					return *d.(*emptyCloseReserveHost)
				}
			},
		},
		{
			// BIND_DESCEND_STRUCT / TAPE_BIND_DESCEND_STRUCT
			name: "struct field",
			src:  `{"h":{}}`,
			inlineDst: func() (any, func(any) inlHost) {
				type w struct {
					H inlHost `json:"h"`
				}
				return new(w), func(d any) inlHost { return d.(*w).H }
			},
			reserveDst: func() (any, func(any) emptyCloseReserveHost) {
				type w struct {
					H emptyCloseReserveHost `json:"h"`
				}
				return new(w), func(d any) emptyCloseReserveHost { return d.(*w).H }
			},
		},
		{
			// array_value's STRUCT arm / t_array_value's STRUCT arm
			name: "array element",
			src:  `[{}]`,
			inlineDst: func() (any, func(any) inlHost) {
				return new([]inlHost), func(d any) inlHost { return (*d.(*[]inlHost))[0] }
			},
			reserveDst: func() (any, func(any) emptyCloseReserveHost) {
				return new([]emptyCloseReserveHost), func(d any) emptyCloseReserveHost {
					return (*d.(*[]emptyCloseReserveHost))[0]
				}
			},
		},
		{
			// map_value's STRUCT arm / t_map_value's STRUCT arm
			name: "map value",
			src:  `{"k":{}}`,
			inlineDst: func() (any, func(any) inlHost) {
				return new(map[string]inlHost), func(d any) inlHost { return (*d.(*map[string]inlHost))["k"] }
			},
			reserveDst: func() (any, func(any) emptyCloseReserveHost) {
				return new(map[string]emptyCloseReserveHost), func(d any) emptyCloseReserveHost {
					return (*d.(*map[string]emptyCloseReserveHost))["k"]
				}
			},
		},
	}
}

// TestEmptyObject_InlineVariantSelectsNothing: `{}` names no case, so every site
// must leave the eface nil and return no error. An embedded variant is a layout
// for the selected case's fields, not a requirement that a case be selected.
//
// Asserting Data == nil and not merely err == nil is the point: before the close
// was made unconditional, these sites walked off the end of the object and failed
// with "syntax error" / "unexpected end of input". Those are still errors, so
// err == nil is what proves the close is reached; Data == nil is what proves it
// settled correctly once there. The reserve-unknown half of this matrix
// (TestEmptyObject_ReserveUnknownPublishesEmptyObject) covers the same four sites
// with a host that DOES owe something at close, so a site that skipped the close
// entirely would fail there.
func TestEmptyObject_InlineVariantSelectsNothing(t *testing.T) {
	for _, site := range emptyCloseSites() {
		t.Run(site.name, func(t *testing.T) {
			t.Run("json", func(t *testing.T) {
				dst, get := site.inlineDst()
				if err := Unmarshal([]byte(site.src), dst); err != nil {
					t.Fatalf("Unmarshal(%s): %v", site.src, err)
				}
				if h := get(dst); h.Data != nil {
					t.Errorf("Unmarshal(%s): Data = %v, want nil", site.src, h.Data)
				}
			})
			t.Run("tape", func(t *testing.T) {
				val, err := dom.Parse([]byte(site.src))
				if err != nil {
					t.Fatalf("dom.Parse(%s): %v", site.src, err)
				}
				dst, get := site.inlineDst()
				if err := UnmarshalValue(val, dst); err != nil {
					t.Fatalf("UnmarshalValue(%s): %v", site.src, err)
				}
				if h := get(dst); h.Data != nil {
					t.Errorf("UnmarshalValue(%s): Data = %v, want nil", site.src, h.Data)
				}
			})
		})
	}
}

// TestEmptyObject_InlineVariantEmptyDiscReportsMissingDisc drives the same four
// sites with a discriminator that is present but names no case. Each site must
// report, which is what keeps the test above from passing merely because a site
// stopped resolving the variant at all.
func TestEmptyObject_InlineVariantEmptyDiscReportsMissingDisc(t *testing.T) {
	for _, site := range emptyCloseSites() {
		t.Run(site.name, func(t *testing.T) {
			src := strings.Replace(site.src, `{}`, `{"type":""}`, 1)
			t.Run("json", func(t *testing.T) {
				dst, _ := site.inlineDst()
				err := Unmarshal([]byte(src), dst)
				if err == nil {
					t.Fatalf("Unmarshal(%s): want missing-discriminator error, got nil", src)
				}
				if !strings.Contains(err.Error(), "missing discriminator") {
					t.Errorf("Unmarshal(%s) error = %q, want it to name the missing discriminator", src, err)
				}
			})
			t.Run("tape", func(t *testing.T) {
				val, err := dom.Parse([]byte(src))
				if err != nil {
					t.Fatalf("dom.Parse(%s): %v", src, err)
				}
				dst, _ := site.inlineDst()
				err = UnmarshalValue(val, dst)
				if err == nil {
					t.Fatalf("UnmarshalValue(%s): want missing-discriminator error, got nil", src)
				}
				if !strings.Contains(err.Error(), "missing discriminator") {
					t.Errorf("UnmarshalValue(%s) error = %q, want it to name the missing discriminator", src, err)
				}
			})
		})
	}
}

// TestEmptyObject_ReserveUnknownPublishesEmptyObject: `{}` is valid input for an
// all-optional reserve-unknown host, and the Value it publishes must be an empty
// object rather than invalid. TestReserveUnknownEmpty already pins this for an
// object carrying known keys; the two must not disagree just because this one
// carries none.
func TestEmptyObject_ReserveUnknownPublishesEmptyObject(t *testing.T) {
	check := func(t *testing.T, what string, h emptyCloseReserveHost) {
		if !h.Exts.Valid() {
			t.Fatalf("%s: Exts invalid; want a valid empty object", what)
		}
		if h.Exts.Type() != value.KindObject {
			t.Fatalf("%s: Exts.Type = %v, want KindObject", what, h.Exts.Type())
		}
		if h.Exts.Len() != 0 {
			t.Errorf("%s: Exts.Len = %d, want 0", what, h.Exts.Len())
		}
	}
	for _, site := range emptyCloseSites() {
		t.Run(site.name, func(t *testing.T) {
			t.Run("json", func(t *testing.T) {
				dst, get := site.reserveDst()
				if err := Unmarshal([]byte(site.src), dst); err != nil {
					t.Fatalf("Unmarshal(%s): %v", site.src, err)
				}
				check(t, "Unmarshal", get(dst))
			})
			t.Run("tape", func(t *testing.T) {
				val, err := dom.Parse([]byte(site.src))
				if err != nil {
					t.Fatalf("dom.Parse(%s): %v", site.src, err)
				}
				dst, get := site.reserveDst()
				if err := UnmarshalValue(val, dst); err != nil {
					t.Fatalf("UnmarshalValue(%s): %v", site.src, err)
				}
				check(t, "UnmarshalValue", get(dst))
			})
		})
	}
}

// TestEmptyObject_PlainStructStillShortcuts guards the other direction. A struct
// with nothing to settle at close must keep leaving without one; routing it
// through the close would be correct but would put the merged-tape machinery on
// the path of every ordinary `{}`. An unexpected error here means the
// MAY_PHASE2 screen stopped dismissing plain structs.
func TestEmptyObject_PlainStructStillShortcuts(t *testing.T) {
	type plain struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	cases := []struct {
		name string
		src  string
		dst  func() any
	}{
		{"root", `{}`, func() any { return new(plain) }},
		{"struct field", `{"p":{}}`, func() any {
			return new(struct {
				P plain `json:"p"`
			})
		}},
		{"array element", `[{}]`, func() any { return new([]plain) }},
		{"map value", `{"k":{}}`, func() any { return new(map[string]plain) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Unmarshal([]byte(c.src), c.dst()); err != nil {
				t.Errorf("Unmarshal(%s): %v", c.src, err)
			}
			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse(%s): %v", c.src, err)
			}
			if err := UnmarshalValue(val, c.dst()); err != nil {
				t.Errorf("UnmarshalValue(%s): %v", c.src, err)
			}
		})
	}
}
