package tests

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// validCases are inputs whose validity is the same for any JSON implementation.
// Each is checked against both encoding/json and vjson.
var validCases = []string{
	// atoms
	`true`,
	`false`,
	`null`,
	`0`,
	`-0`,
	`123`,
	`-123`,
	`1.5`,
	`-1.5`,
	`1e10`,
	`1E10`,
	`1.5e+10`,
	`1.5e-10`,
	`3.141592653589793`,
	`""`,
	`"hello"`,
	`"hello\u0020world"`,
	`"\"\\\/\b\f\n\r\t"`,
	`"日本語"`,
	`"emoji \ud83d\ude00"`,
	`{}`,
	`[]`,
	`{"a":1}`,
	`[1,2,3]`,
	`{"a":[1,2,3],"b":{"c":"d"}}`,
	`  true  `,
	"\t\n\r true \n",
	`{"": ""}`,
	`{"a":null,"b":true,"c":false,"d":1.5,"e":"s","f":[],"g":{}}`,
	// deeply nested but reasonable
	strings.Repeat(`[`, 100) + `1` + strings.Repeat(`]`, 100),
	// large string with many escapes
	`"` + strings.Repeat(`\u00e9`, 50) + `"`,
}

// invalidCases are inputs that must be rejected.
var invalidCases = []string{
	``,
	`   `,
	"\n\t\r",
	`tru`,
	`truex`,
	`fals`,
	`nul`,
	`NULL`,
	`True`,
	`False`,
	`01`,   // leading zero
	`1.`,   // trailing dot
	`1.e5`, // empty fraction
	`+1`,   // explicit plus
	`--1`,  // double minus
	`1e`,   // empty exponent
	`1e+`,  // empty exponent sign
	`.5`,   // missing leading digit
	`Infinity`,
	`NaN`,
	`'single'`,                // wrong quote
	`"unterminated`,           // unterminated string
	`"bad\u00"`,               // short unicode escape
	`"bad\u00zz"`,             // non-hex unicode escape
	`"bad\x00"`,               // invalid escape
	`"control` + "\x00" + `"`, // raw control char in string
	`{`,                       // unclosed object
	`}`,                       // unopened object
	`[`,                       // unclosed array
	`]`,                       // unopened array
	`[1,]`,                    // trailing comma (rejected by std)
	`{,}`,                     // leading comma
	`{"a"}`,                   // missing colon
	`{"a":}`,                  // missing value
	`{"a":1,}`,                // trailing comma in object
	`[1 2]`,                   // missing comma
	`{"a":1 "b":2}`,           // missing comma in object
	`{}{}`,                    // two values
	`true false`,              // two values
	`true,`,                   // trailing junk
	`1 2`,                     // two numbers
	`undefined`,               // js literal
	`{a:1}`,                   // unquoted key
	`[01]`,                    // leading zero in array
	`["a" "b"]`,               // missing comma in array
	// U+2028/U+2029 are valid inside JSON strings per RFC 8259.
	// The case below lets std set the baseline so we just check parity.
	`"` + string(rune(0x2028)) + `"`,
}

func TestValid_MatchesStd(t *testing.T) {
	for _, in := range validCases {
		std := json.Valid([]byte(in))
		got := vjson.Valid([]byte(in))
		if !std {
			t.Errorf("validCases: encoding/json rejected %q (fix test data)", in)
			continue
		}
		if got != std {
			t.Errorf("Valid(%q) = %v, want %v (std)", in, got, std)
		}
	}
	for _, in := range invalidCases {
		std := json.Valid([]byte(in))
		got := vjson.Valid([]byte(in))
		if got != std {
			t.Errorf("Valid(%q) = %v, want %v (std)", in, got, std)
		}
	}
}

func TestValid_Randomized(t *testing.T) {
	// Round-trip: any value produced by json.Marshal must be Valid.
	values := []any{
		nil,
		true,
		false,
		42,
		-42,
		3.14,
		math.Pi,
		math.MaxFloat64,
		math.SmallestNonzeroFloat64,
		"hello",
		"",
		"with \"quotes\" and \\ backslash",
		[]any{1, "two", true, nil},
		map[string]any{"a": 1, "b": []any{2, 3}, "c": nil},
		map[string]any{"nested": map[string]any{"deep": map[string]any{"deeper": 7}}},
	}
	for _, v := range values {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%#v): %v", v, err)
		}
		if !vjson.Valid(data) {
			t.Errorf("Valid(Marshal(%#v)) = false, want true; data=%s", v, data)
		}
	}
}

func TestValid_LeadingWhitespace(t *testing.T) {
	prefixes := []string{"", " ", "\t", "\n", "\r", " \t\n\r "}
	for _, p := range prefixes {
		in := p + `true`
		if !vjson.Valid([]byte(in)) {
			t.Errorf("Valid(%q) = false, want true", in)
		}
	}
}

func TestValid_TrailingJunk(t *testing.T) {
	cases := []string{
		`true x`,
		`1 "a"`,
		`{}  false`,
		`"a" "b"`,
	}
	for _, in := range cases {
		if vjson.Valid([]byte(in)) {
			t.Errorf("Valid(%q) = true, want false", in)
		}
	}
}
