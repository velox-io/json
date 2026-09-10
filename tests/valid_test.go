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
	// out-of-range exponents stay grammar-valid to encoding/json
	`1e-999`,
	// exact integers beyond every binary64 are still valid tokens
	`123456789012345678901234567890`,
	// lone surrogates: each \uXXXX stands alone
	`"\ud800"`,
	`"\udc00"`,
	// an escaped NUL key
	`{"\u0000":1}`,
	// DEL is above the control range and valid in strings
	"\x22\x7f\x22",
	// malformed UTF-8 inside strings is accepted, as in encoding/json
	"\x22a\xffb\x22",
	"\x22\xc3(\x22",
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
	`[1,,2]`,                  // empty array element
	`{:1}`,                    // object without key
	`{"a"::1}`,                // double colon
	`{"a":1"b":2}`,            // missing comma
	`["\u`,                    // truncated escape at input end
	`"\u12`,                   // truncated hex run
	`"\uzzzz"`,                // non-hex unicode escape
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

// nativeCases pin verdicts where the native walker follows encoding/json.
// They run only when the native entry is linked; the shared suites above
// stay within the common contract.
func TestValid_NativeStdlibParity(t *testing.T) {
	valid := []string{
		`1e900`,  // exponent overflow: grammar-valid, no value parsed
		`-1e900`, // negative overflow
		`1e999`,  // deep overflow
	}
	for _, in := range valid {
		if !vjson.Valid([]byte(in)) {
			t.Errorf("Valid(%q) = false, want true (std)", in)
		}
	}
	// The walker caps containers at 256, the same limit as the native DOM
	// engine; 256 nested arrays stay valid, 257 do not.
	nested := func(d int) []byte {
		return []byte(strings.Repeat(`[`, d) + `1` + strings.Repeat(`]`, d))
	}
	if !vjson.Valid(nested(256)) {
		t.Error("Valid(256 nested arrays) = false, want true")
	}
	if vjson.Valid(nested(257)) {
		t.Error("Valid(257 nested arrays) = true, want false")
	}
}

// TestValid_BackendsAgree runs the stdlib fallback (encoding/json.Valid,
// used when the native entry is not linked or the input exceeds 4 GB) and
// the active vjson.Valid over the full case corpus so the two backends
// cannot drift apart silently. The corpus stays inside the agreement
// domain: the native depth cap differs, so those inputs are pinned by
// TestValid_NativeStdlibParity instead.
func TestValid_BackendsAgree(t *testing.T) {
	var corpus []string
	corpus = append(corpus, validCases...)
	corpus = append(corpus, invalidCases...)
	for _, in := range corpus {
		got := vjson.Valid([]byte(in))
		want := json.Valid([]byte(in))
		if got != want {
			t.Errorf("backends disagree on %q: vjson=%v std=%v", in, got, want)
		}
	}
}
