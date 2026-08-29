package bind

import (
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/vbind"
)

type quotedDiscUser struct {
	Name string `json:"name"`
}

type quotedDiscSibling struct {
	Kind string `json:"kind,string"`
	Data any    `json:"data" vjson:"variant=kind"`
}

type quotedDiscInline struct {
	Kind string `json:"kind,string"`
	Data any    `json:",embed" vjson:"variant=kind"`
}

func init() {
	vbind.DefineVariantCases[quotedDiscSibling, struct {
		_ quotedDiscUser `case:"user"`
	}]()
	vbind.DefineVariantCases[quotedDiscInline, struct {
		_ quotedDiscUser `case:"user"`
	}]()
}

func TestVariantQuotedDiscriminator(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		new  func() any
		get  func(any) (string, any)
	}{
		{
			"sibling discriminator first",
			`{"kind":"\"u\\u0073er\"","data":{"name":"A"}}`,
			func() any { return new(quotedDiscSibling) },
			func(v any) (string, any) { h := v.(*quotedDiscSibling); return h.Kind, h.Data },
		},
		{
			"sibling payload first",
			`{"data":{"name":"A"},"kind":"\"u\\u0073er\""}`,
			func() any { return &quotedDiscSibling{Kind: "stale"} },
			func(v any) (string, any) { h := v.(*quotedDiscSibling); return h.Kind, h.Data },
		},
		{
			"inline",
			`{"name":"A","kind":"\"u\\u0073er\""}`,
			func() any { return &quotedDiscInline{Kind: "stale"} },
			func(v any) (string, any) { h := v.(*quotedDiscInline); return h.Kind, h.Data },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []struct {
				name string
				run  func(any) error
			}{
				{"Unmarshal", func(dst any) error { return Unmarshal([]byte(tc.src), dst) }},
				{"UnmarshalValue", func(dst any) error {
					v, err := dom.Parse([]byte(tc.src))
					if err != nil {
						return err
					}
					return UnmarshalValue(v, dst)
				}},
			} {
				t.Run(path.name, func(t *testing.T) {
					dst := tc.new()
					if err := path.run(dst); err != nil {
						t.Fatalf("decode: %v", err)
					}
					kind, data := tc.get(dst)
					if kind != "user" {
						t.Fatalf("Kind = %q, want user", kind)
					}
					if got, ok := data.(quotedDiscUser); !ok || got.Name != "A" {
						t.Fatalf("Data = %#v, want quotedDiscUser{Name:A}", data)
					}
				})
			}
		})
	}
}
