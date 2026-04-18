package ndec

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type structFlat struct {
	A int32  `json:"a"`
	B string `json:"b"`
}

type structInner struct {
	X int32  `json:"x"`
	Y string `json:"y"`
}

type structNested struct {
	Tag   string      `json:"tag"`
	Inner structInner `json:"inner"`
	N     int32       `json:"n"`
}

type structDoubleNested struct {
	A int32        `json:"a"`
	B structNested `json:"b"`
}

// Sixteen fields keep this shape on the PERFECT lookup path instead of the
// smaller BITMAP8 strategy.
type structWide16 struct {
	F00 int32  `json:"f00"`
	F01 int32  `json:"f01"`
	F02 int32  `json:"f02"`
	F03 int32  `json:"f03"`
	F04 int32  `json:"f04"`
	F05 int32  `json:"f05"`
	F06 int32  `json:"f06"`
	F07 int32  `json:"f07"`
	F08 string `json:"f08"`
	F09 string `json:"f09"`
	F10 string `json:"f10"`
	F11 string `json:"f11"`
	F12 string `json:"f12"`
	F13 string `json:"f13"`
	F14 string `json:"f14"`
	F15 string `json:"f15"`
}

// Thirty two fields hits the upper bound of the PERFECT lookup tier.
type structWide32 struct {
	A  int32 `json:"a"`
	B  int32 `json:"b"`
	C  int32 `json:"c"`
	D  int32 `json:"d"`
	E  int32 `json:"e"`
	F  int32 `json:"f"`
	G  int32 `json:"g"`
	H  int32 `json:"h"`
	I  int32 `json:"i"`
	J  int32 `json:"j"`
	K  int32 `json:"k"`
	L  int32 `json:"l"`
	M  int32 `json:"m"`
	N  int32 `json:"n"`
	O  int32 `json:"o"`
	P  int32 `json:"p"`
	Q  int32 `json:"q"`
	R  int32 `json:"r"`
	S  int32 `json:"s"`
	T  int32 `json:"t"`
	U  int32 `json:"u"`
	V  int32 `json:"v"`
	W  int32 `json:"w"`
	X  int32 `json:"x"`
	Y  int32 `json:"y"`
	Z  int32 `json:"z"`
	A1 int32 `json:"a1"`
	B1 int32 `json:"b1"`
	C1 int32 `json:"c1"`
	D1 int32 `json:"d1"`
	E1 int32 `json:"e1"`
	F1 int32 `json:"f1"`
}

// Fifty fields force lookup construction onto the MAP tier.
type structMap50 struct {
	F00 int32 `json:"f00"`
	F01 int32 `json:"f01"`
	F02 int32 `json:"f02"`
	F03 int32 `json:"f03"`
	F04 int32 `json:"f04"`
	F05 int32 `json:"f05"`
	F06 int32 `json:"f06"`
	F07 int32 `json:"f07"`
	F08 int32 `json:"f08"`
	F09 int32 `json:"f09"`
	F10 int32 `json:"f10"`
	F11 int32 `json:"f11"`
	F12 int32 `json:"f12"`
	F13 int32 `json:"f13"`
	F14 int32 `json:"f14"`
	F15 int32 `json:"f15"`
	F16 int32 `json:"f16"`
	F17 int32 `json:"f17"`
	F18 int32 `json:"f18"`
	F19 int32 `json:"f19"`
	F20 int32 `json:"f20"`
	F21 int32 `json:"f21"`
	F22 int32 `json:"f22"`
	F23 int32 `json:"f23"`
	F24 int32 `json:"f24"`
	F25 int32 `json:"f25"`
	F26 int32 `json:"f26"`
	F27 int32 `json:"f27"`
	F28 int32 `json:"f28"`
	F29 int32 `json:"f29"`
	F30 int32 `json:"f30"`
	F31 int32 `json:"f31"`
	F32 int32 `json:"f32"`
	F33 int32 `json:"f33"`
	F34 int32 `json:"f34"`
	F35 int32 `json:"f35"`
	F36 int32 `json:"f36"`
	F37 int32 `json:"f37"`
	F38 int32 `json:"f38"`
	F39 int32 `json:"f39"`
	F40 int32 `json:"f40"`
	F41 int32 `json:"f41"`
	F42 int32 `json:"f42"`
	F43 int32 `json:"f43"`
	F44 int32 `json:"f44"`
	F45 int32 `json:"f45"`
	F46 int32 `json:"f46"`
	F47 int32 `json:"f47"`
	F48 int32 `json:"f48"`
	F49 int32 `json:"f49"`
}

func TestStructFlat(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  structFlat
	}{
		{"both_present", `{"a":42,"b":"hi"}`, structFlat{A: 42, B: "hi"}},
		{"reverse_order", `{"b":"world","a":-1}`, structFlat{A: -1, B: "world"}},
		{"empty_string", `{"a":0,"b":""}`, structFlat{A: 0, B: ""}},
		{"missing_a", `{"b":"only"}`, structFlat{A: 0, B: "only"}},
		{"missing_b", `{"a":7}`, structFlat{A: 7, B: ""}},
		{"empty_object", `{}`, structFlat{A: 0, B: ""}},
		{"int32_max", `{"a":2147483647,"b":""}`, structFlat{A: 2147483647, B: ""}},
		{"int32_min", `{"a":-2147483648,"b":""}`, structFlat{A: -2147483648, B: ""}},
		{"null_a_string_keeps", `{"a":null,"b":"x"}`, structFlat{A: 0, B: "x"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got structFlat
			if err := Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatalf("ndec.Unmarshal: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			var oracle structFlat
			if err := json.Unmarshal([]byte(tc.input), &oracle); err != nil {
				t.Fatalf("encoding/json.Unmarshal: %v", err)
			}
			if got != oracle {
				t.Fatalf("parity drift:\n ndec   = %+v\n stdlib = %+v", got, oracle)
			}
		})
	}
}

func TestStructNested(t *testing.T) {
	cases := []string{
		`{"tag":"hello","inner":{"x":1,"y":"a"},"n":42}`,
		`{"tag":"","inner":{"x":-1,"y":""},"n":0}`,
		`{"inner":{"x":99,"y":"only inner"}}`,
		`{"tag":"first","n":7,"inner":{"y":"reversed","x":3}}`,
		`{"tag":"with-null","inner":null,"n":1}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want structNested
			runParity(t, in, &got, &want)
		})
	}
}

func TestStructDoubleNested(t *testing.T) {
	cases := []string{
		`{"a":1,"b":{"tag":"t","inner":{"x":2,"y":"yy"},"n":3}}`,
		`{"a":-1,"b":{"tag":"","inner":{"x":0,"y":""},"n":0}}`,
		`{"b":{"inner":{"x":7,"y":"deep"}}}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want structDoubleNested
			runParity(t, in, &got, &want)
		})
	}
}

func TestStructWide16(t *testing.T) {
	cases := []string{
		`{"f00":1,"f01":2,"f02":3,"f03":4,"f04":5,"f05":6,"f06":7,"f07":8,` +
			`"f08":"a","f09":"b","f10":"c","f11":"d","f12":"e","f13":"f","f14":"g","f15":"h"}`,
		`{"f15":"hh","f14":"gg","f13":"ff","f12":"ee","f11":"dd","f10":"cc","f09":"bb","f08":"aa",` +
			`"f07":80,"f06":70,"f05":60,"f04":50,"f03":40,"f02":30,"f01":20,"f00":10}`,
		`{"f00":42,"f15":"only"}`,
		`{"f00":1,"unknown":"ignored","f10":"hello"}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in[:min(40, len(in))], func(t *testing.T) {
			var got, want structWide16
			runParity(t, in, &got, &want)
		})
	}
}

func TestStructWide32(t *testing.T) {
	cases := []string{
		`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,` +
			`"i":9,"j":10,"k":11,"l":12,"m":13,"n":14,"o":15,"p":16,` +
			`"q":17,"r":18,"s":19,"t":20,"u":21,"v":22,"w":23,"x":24,` +
			`"y":25,"z":26,"a1":27,"b1":28,"c1":29,"d1":30,"e1":31,"f1":32}`,
		`{"f1":99,"a":-1,"z":0}`,
	}
	for _, in := range cases {
		t.Run(in[:min(40, len(in))], func(t *testing.T) {
			var got, want structWide32
			runParity(t, in, &got, &want)
		})
	}
}

func TestStructMap50AllFields(t *testing.T) {
	var sb strings.Builder
	sb.WriteByte('{')
	for i := range 50 {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"f%02d":%d`, i, i*7)
	}
	sb.WriteByte('}')
	in := sb.String()

	var got, want structMap50
	runParity(t, in, &got, &want)
}

func TestStructMap50PartialFields(t *testing.T) {
	cases := []string{
		`{"f00":1,"f49":99}`,
		`{"f25":42}`,
		`{"f10":10,"unknown":"ignored","f30":30}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want structMap50
			runParity(t, in, &got, &want)
		})
	}
}

func TestStructMap50ReverseOrder(t *testing.T) {
	var sb strings.Builder
	sb.WriteByte('{')
	for i := 49; i >= 0; i-- {
		if i < 49 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"f%02d":%d`, i, i*3)
	}
	sb.WriteByte('}')
	in := sb.String()

	var got, want structMap50
	runParity(t, in, &got, &want)
}

// These cases pin the stdlib contract for ,string fields on scalar types. The
// invalid string,string combination is intentionally excluded because stdlib rejects it.
type tagStringType struct {
	Int   int     `json:"int,string"`
	Int8  int8    `json:"int8,string"`
	Int64 int64   `json:"int64,string"`
	Uint  uint    `json:"uint,string"`
	Float float64 `json:"float,string"`
	Bool  bool    `json:"bool,string"`
}

func TestTagString_Flat(t *testing.T) {
	cases := []struct {
		in string
	}{
		{`{"int":"42","int8":"-7","int64":"9223372036854775807","uint":"255","float":"3.14","bool":"true"}`},
		{`{"int":"0","int8":"0","int64":"0","uint":"0","float":"0","bool":"false"}`},
		{`{"int":"-1","int8":"127","int64":"0","uint":"0","float":"1e308","bool":"true"}`},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			var got, want tagStringType
			runParity(t, c.in, &got, &want)
		})
	}
}
