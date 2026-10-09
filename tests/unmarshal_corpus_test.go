package tests

// A binding corpus run against encoding/json. Each case decodes one document
// into a fresh destination through every decode entry point: Unmarshal, a
// Decoder over chunked reads, and, for destinations the tape walk supports,
// Parse followed by UnmarshalValue. Every entry must agree with
// encoding/json on whether the document fails, on the error's class, and on
// a success's decoded value. The make test matrix runs the corpus under the
// native engine, the Go engine (vj_nondec), and the engine diff build, so
// one table pins all three.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

// corpusCase is one document and the destination it decodes into. uv marks
// destinations the tape walk binds; json.Number and the hook kinds are
// outside it.
type corpusCase struct {
	name string
	in   string
	mk   func() any
	uv   bool
}

// errClass names the encoding/json error family an error belongs to, which
// is what callers branch on. A truncated document is a syntax error through
// Unmarshal and io.ErrUnexpectedEOF through a Decoder, so both read as
// "syntax".
func errClass(err error) string {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.As(err, &te):
		return "type"
	case errors.As(err, &se), errors.Is(err, io.ErrUnexpectedEOF):
		return "syntax"
	}
	return fmt.Sprintf("other(%T)", err)
}

// assertParity decodes c through every entry point and compares each with
// encoding/json.
func assertParity(t *testing.T, c corpusCase) {
	t.Helper()
	data := []byte(c.in)
	std := c.mk()
	stdErr := json.Unmarshal(data, std)
	stdDec := c.mk()
	stdDecErr := json.NewDecoder(strings.NewReader(c.in)).Decode(stdDec)

	check := func(leg string, want error, wantV any, got error, gotV any) {
		t.Helper()
		if errClass(got) != errClass(want) {
			t.Errorf("%s/%s: error %s (%v), encoding/json %s (%v)", c.name, leg, errClass(got), got, errClass(want), want)
			return
		}
		if want == nil && !reflect.DeepEqual(gotV, wantV) {
			t.Errorf("%s/%s: decoded %s, encoding/json %s", c.name, leg, dbgAny(gotV), dbgAny(wantV))
		}
	}

	v := c.mk()
	check("Unmarshal", stdErr, std, vjson.Unmarshal(data, v), v)
	for _, chunk := range []int{1, 7, len(data) + 1} {
		v = c.mk()
		err := vjson.NewDecoder(&chunkReader{data: data, size: chunk}).Decode(v)
		check(fmt.Sprintf("Decoder(chunk=%d)", chunk), stdDecErr, stdDec, err, v)
	}
	if !c.uv {
		return
	}
	v = c.mk()
	pv, err := vjson.Parse(data)
	if err == nil {
		err = vjson.UnmarshalValue(pv, v)
	}
	check("UnmarshalValue", stdErr, std, err, v)
}

type cpScalars struct {
	S   string  `json:"s"`
	B   bool    `json:"b"`
	I   int     `json:"i"`
	I8  int8    `json:"i8"`
	I16 int16   `json:"i16"`
	I32 int32   `json:"i32"`
	I64 int64   `json:"i64"`
	U   uint    `json:"u"`
	U8  uint8   `json:"u8"`
	U16 uint16  `json:"u16"`
	U32 uint32  `json:"u32"`
	U64 uint64  `json:"u64"`
	F32 float32 `json:"f32"`
	F64 float64 `json:"f64"`
}

type cpStr struct {
	S string `json:"s"`
}

type cpInts struct {
	I   int64  `json:"i"`
	U   uint64 `json:"u"`
	I8  int8   `json:"i8"`
	I16 int16  `json:"i16"`
	I32 int32  `json:"i32"`
	U8  uint8  `json:"u8"`
	U16 uint16 `json:"u16"`
	U32 uint32 `json:"u32"`
}

type cpFloats struct {
	F32 float32 `json:"f32"`
	F64 float64 `json:"f64"`
}

type cpNumber struct {
	N json.Number `json:"n"`
}

type cpQuoted struct {
	B bool    `json:"b,string"`
	I int     `json:"i,string"`
	U uint16  `json:"u,string"`
	F float64 `json:"f,string"`
	S string  `json:"s,string"`
	P *int    `json:"p,string"`
}

type cpPtrs struct {
	P  *int
	PP **string
	S  *cpStr
}

type cpChainLeaf int

type cpChain struct {
	D ************cpChainLeaf `json:"d"`
}

type cpLeaf struct {
	Name string `json:"name"`
	Tag  int    `json:"tag"`
}

type cpNested struct {
	List []cpLeaf            `json:"list"`
	Idx  map[string][]cpLeaf `json:"idx"`
	Grid [][]int             `json:"grid"`
	Arr  [2]cpLeaf           `json:"arr"`
}

type cpEmbBase struct {
	X int `json:"x"`
}

type CpEmbExported struct {
	Z int `json:"z"`
}

type cpEmb struct {
	*CpEmbExported
	cpEmbBase
	Y int `json:"y"`
}

type cpAB struct {
	A int `json:"a"`
	B int `json:"b"`
}

func intList(n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", i)
	}
	b.WriteByte(']')
	return b.String()
}

func keyedObject(n int) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"k%d":%d`, i, i)
	}
	b.WriteByte('}')
	return b.String()
}

func TestUnmarshalCorpus_Scalars(t *testing.T) {
	cases := []corpusCase{
		{"every scalar kind", `{"s":"a\tb\u00e9\ud83d\ude00","b":true,"i":-5,` +
			`"i8":-128,"i16":-32768,"i32":-2147483648,"i64":-9223372036854775808,` +
			`"u":7,"u8":255,"u16":65535,"u32":4294967295,"u64":18446744073709551615,` +
			`"f32":0.5,"f64":1.5e3}`, newOf[cpScalars](), true},
		{"scalar kind mismatch", `{"b":1}`, newOf[cpScalars](), true},
		{"string for bool", `{"b":"true"}`, newOf[cpScalars](), true},
		{"bool for string", `{"s":false}`, newOf[cpScalars](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_Strings(t *testing.T) {
	long := strings.Repeat("x", 4096)
	cases := []corpusCase{
		{"empty", `{"s":""}`, newOf[cpStr](), true},
		{"plain", `{"s":"hello"}`, newOf[cpStr](), true},
		{"short escapes", `{"s":"a\nb\tc\\d\"e\/f\b\f\r"}`, newOf[cpStr](), true},
		{"unicode escapes", `{"s":"\u0041\u00e9\u4e2d\ud83d\ude00"}`, newOf[cpStr](), true},
		{"lone high surrogate", `{"s":"a\ud83db"}`, newOf[cpStr](), true},
		{"lone low surrogate", `{"s":"\udc00"}`, newOf[cpStr](), true},
		{"high surrogate then plain escape", `{"s":"\ud83d\n"}`, newOf[cpStr](), true},
		{"long plain", `{"s":"` + long + `"}`, newOf[cpStr](), true},
		{"long escaped", `{"s":"` + strings.Repeat(`\n`, 2048) + `"}`, newOf[cpStr](), true},
		{"long mixed", `{"s":"` + strings.Repeat(`ab\u00e9cd\t`, 500) + `"}`, newOf[cpStr](), true},
		{"unterminated", `{"s":"abc`, newOf[cpStr](), true},
		{"unknown escape", `{"s":"a\qb"}`, newOf[cpStr](), true},
		{"short unicode escape", `{"s":"\u00"}`, newOf[cpStr](), true},
		{"non-hex unicode escape", `{"s":"\uZZZZ"}`, newOf[cpStr](), true},
		{"root string", `"root"`, newOf[string](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_Integers(t *testing.T) {
	cases := []corpusCase{
		{"int64 max", `{"i":9223372036854775807}`, newOf[cpInts](), true},
		{"int64 min", `{"i":-9223372036854775808}`, newOf[cpInts](), true},
		{"uint64 max", `{"u":18446744073709551615}`, newOf[cpInts](), true},
		{"int64 overflow", `{"i":9223372036854775808}`, newOf[cpInts](), true},
		{"int64 underflow", `{"i":-9223372036854775809}`, newOf[cpInts](), true},
		{"uint64 overflow", `{"u":18446744073709551616}`, newOf[cpInts](), true},
		{"long digit run", `{"u":123456789012345678901234567890}`, newOf[cpInts](), true},
		{"negative unsigned", `{"u":-1}`, newOf[cpInts](), true},
		{"fraction", `{"i":1.5}`, newOf[cpInts](), true},
		{"integral fraction", `{"i":1.0}`, newOf[cpInts](), true},
		{"exponent", `{"i":1e3}`, newOf[cpInts](), true},
		{"int8 overflow", `{"i8":128}`, newOf[cpInts](), true},
		{"int8 min", `{"i8":-128}`, newOf[cpInts](), true},
		{"int16 overflow", `{"i16":32768}`, newOf[cpInts](), true},
		{"int32 overflow", `{"i32":2147483648}`, newOf[cpInts](), true},
		{"int32 underflow", `{"i32":-2147483649}`, newOf[cpInts](), true},
		{"uint8 overflow", `{"u8":256}`, newOf[cpInts](), true},
		{"uint16 overflow", `{"u16":65536}`, newOf[cpInts](), true},
		{"uint32 overflow", `{"u32":4294967296}`, newOf[cpInts](), true},
		{"leading zero", `{"i":01}`, newOf[cpInts](), true},
		{"trailing garbage", `{"i":12x}`, newOf[cpInts](), true},
		{"bare minus", `{"i":-}`, newOf[cpInts](), true},
		{"plus sign", `{"i":+1}`, newOf[cpInts](), true},
		{"root int", `42`, newOf[int](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_Floats(t *testing.T) {
	cases := []corpusCase{
		{"short fractions", `{"f32":0.1,"f64":0.1}`, newOf[cpFloats](), true},
		{"exponents", `{"f32":1E+3,"f64":-1.5e-3}`, newOf[cpFloats](), true},
		{"long mantissa", `{"f64":12345678901234567890123}`, newOf[cpFloats](), true},
		{"long fraction", `{"f64":0.1000000000000000055511151231257827}`, newOf[cpFloats](), true},
		{"smallest subnormal", `{"f64":5e-324}`, newOf[cpFloats](), true},
		{"underflow to zero", `{"f64":1e-400}`, newOf[cpFloats](), true},
		{"float64 overflow", `{"f64":1e999}`, newOf[cpFloats](), true},
		{"float32 overflow", `{"f32":1e50}`, newOf[cpFloats](), true},
		{"dot without fraction", `{"f64":1.}`, newOf[cpFloats](), true},
		{"fraction without integer", `{"f64":.5}`, newOf[cpFloats](), true},
		{"exponent without digits", `{"f64":1e}`, newOf[cpFloats](), true},
		{"signed exponent without digits", `{"f64":1e+}`, newOf[cpFloats](), true},
		{"two dots", `{"f64":1.2.3}`, newOf[cpFloats](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

// DeepEqual does not tell -0 from +0, so the sign is checked directly.
func TestUnmarshalCorpus_NegativeZero(t *testing.T) {
	for _, in := range []string{`-0.0`, `-0`, `-0e5`} {
		var f float64
		if err := vjson.Unmarshal([]byte(in), &f); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var std float64
		if err := json.Unmarshal([]byte(in), &std); err != nil {
			t.Fatalf("%s: encoding/json: %v", in, err)
		}
		if f != 0 || math.Signbit(f) != math.Signbit(std) {
			t.Errorf("%s: decoded %v (signbit %v), encoding/json signbit %v", in, f, math.Signbit(f), math.Signbit(std))
		}
	}
}

func TestUnmarshalCorpus_Number(t *testing.T) {
	cases := []corpusCase{
		{"integer text", `{"n":123}`, newOf[cpNumber](), false},
		{"exponent text", `{"n":1.5e3}`, newOf[cpNumber](), false},
		{"negative text", `{"n":-0.25}`, newOf[cpNumber](), false},
		{"overflowing text", `{"n":1e999}`, newOf[cpNumber](), false},
		{"quoted number", `{"n":"12"}`, newOf[cpNumber](), false},
		{"bool", `{"n":true}`, newOf[cpNumber](), false},
		{"array", `{"n":[1]}`, newOf[cpNumber](), false},
		{"root", `7.5`, newOf[json.Number](), false},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_QuotedFields(t *testing.T) {
	cases := []corpusCase{
		{"every quoted kind", `{"b":"true","i":"-5","u":"7","f":"1.5","s":"\"hi\"","p":"9"}`, newOf[cpQuoted](), true},
		{"quoted false", `{"b":"false"}`, newOf[cpQuoted](), true},
		{"float trailing garbage", `{"f":"1.5x"}`, newOf[cpQuoted](), true},
		{"uint16 overflow", `{"u":"65536"}`, newOf[cpQuoted](), true},
		{"null spelled in quotes", `{"i":"null","s":"null"}`, newOf[cpQuoted](), true},
		// The native tape walk gives a quoted pointer a zero pointee for any
		// null spelling, so this one stays off UnmarshalValue.
		{"null spelled in quotes for pointer", `{"p":"null"}`, newOf[cpQuoted](), false},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_Pointers(t *testing.T) {
	cases := []corpusCase{
		{"pointer fields", `{"P":5,"PP":"x","S":{"s":"y"}}`, newOf[cpPtrs](), true},
		{"null pointer fields", `{"P":null,"PP":null,"S":null}`, newOf[cpPtrs](), true},
		{"root pointer to struct", `{"s":"z"}`, newOf[*cpStr](), true},
		{"root pointer null", `null`, newOf[*int](), true},
		{"twelve-layer chain", `{"d":1}`, newOf[cpChain](), true},
		{"twelve-layer chain null", `{"d":null}`, newOf[cpChain](), true},
		{"pointer element slice", `[1,null,3]`, newOf[[]*int](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_Containers(t *testing.T) {
	cases := []corpusCase{
		{"empty slice", `[]`, newOf[[]int](), true},
		{"short slice", `[1,2,3]`, newOf[[]int](), true},
		{"slice growth", intList(100), newOf[[]int](), true},
		{"long slice growth", intList(5000), newOf[[]int](), true},
		{"null element", `[1,null,3]`, newOf[[]int](), true},
		{"nested slices", `[[1,2],[],[3]]`, newOf[[][]int](), true},
		{"slice element mismatch", `[1,"x",3]`, newOf[[]int](), true},
		{"fixed array exact", `[1,2,3]`, newOf[[3]int](), true},
		{"fixed array surplus", `[1,2,3,4,"x",{"y":[5]}]`, newOf[[3]int](), true},
		{"empty map", `{}`, newOf[map[string]int](), true},
		{"duplicate keys", `{"a":1,"b":2,"a":3}`, newOf[map[string]int](), true},
		{"many keys", keyedObject(100), newOf[map[string]int](), true},
		{"escaped keys", `{"a\"b":1,"\u00e9":2,"":3}`, newOf[map[string]int](), true},
		{"integer keys", `{"1":"a","-2":"b"}`, newOf[map[int]string](), true},
		{"unsigned keys", `{"0":"a","255":"b"}`, newOf[map[uint8]string](), true},
		{"map value mismatch", `{"a":"x"}`, newOf[map[string]int](), true},
		{"map of slices", `{"a":[1],"b":[],"c":null}`, newOf[map[string][]int](), true},
		{"nested containers", `{"list":[{"name":"a","tag":1},{"name":"b"}],` +
			`"idx":{"g":[{"name":"c","tag":3}]},"grid":[[1],[2,3]],"arr":[{"name":"d"}]}`, newOf[cpNested](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

func TestUnmarshalCorpus_Structs(t *testing.T) {
	cases := []corpusCase{
		{"promoted through embeds", `{"x":1,"y":2,"z":3}`, newOf[cpEmb](), true},
		{"unknown fields skip", `{"a":1,"u":{"nested":[1,2]},"v":1e3,"w":"s","x":null,"y":[]}`, newOf[cpAB](), true},
		{"invalid skipped value", `{"a":1,"u":[1,}`, newOf[cpAB](), true},
		{"missing colon", `{"a" 1}`, newOf[cpAB](), true},
		{"missing comma", `{"a":1 "b":2}`, newOf[cpAB](), true},
		{"wrong close", `{"a":1]}`, newOf[cpAB](), true},
		{"bare key", `{a:1}`, newOf[cpAB](), true},
		{"trailing comma", `{"a":1,}`, newOf[cpAB](), true},
		{"truncated after value", `{"a":1`, newOf[cpAB](), true},
		{"truncated open", `{`, newOf[cpAB](), true},
		{"unterminated skipped string", `{"a":1,"u":"abc`, newOf[cpAB](), true},
		{"garbage", `x`, newOf[cpAB](), true},
		// Parse reports an empty document as decode.ErrEmptyInput.
		{"empty document", ``, newOf[cpAB](), false},
		{"whitespace document", " \t\r\n", newOf[cpAB](), true},
		{"trailing value", `{"a":1} {"b":2}`, newOf[cpAB](), true},
		{"trailing garbage", `{"a":1} x`, newOf[cpAB](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

// A root that cannot hold the document's value consumes it whole and
// reports the mismatch. A mismatch is reported ahead of a syntax error later
// in the input, which encoding/json finds first by validating the whole
// document up front, so those inputs are not in the table.
func TestUnmarshalCorpus_RootMismatch(t *testing.T) {
	cases := []corpusCase{
		{"object into int", `{"a":1}`, newOf[int](), true},
		{"array into string", `[1]`, newOf[string](), true},
		{"number into struct", `5`, newOf[cpAB](), true},
		{"string into struct", `"s"`, newOf[cpAB](), true},
		{"bool into struct", `true`, newOf[cpAB](), true},
		{"object into slice", `{"a":[1]}`, newOf[[]int](), true},
		{"array into map", `[{"a":1}]`, newOf[map[string]int](), true},
		{"invalid mismatched root", `{"a":}`, newOf[int](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

// The tape walk does not bind an any root, so only the map case runs
// through UnmarshalValue.
func TestUnmarshalCorpus_Any(t *testing.T) {
	cases := []corpusCase{
		{"string", `"s"`, newOf[any](), false},
		{"integer", `3`, newOf[any](), false},
		{"fraction", `0.1`, newOf[any](), false},
		{"true", `true`, newOf[any](), false},
		{"false", `false`, newOf[any](), false},
		{"null", `null`, newOf[any](), false},
		{"containers", `{"k":[1,"a",null,true],"m":{"x":2,"y":{}}}`, newOf[any](), false},
		{"any values", `{"a":[{"b":null}],"b":1}`, newOf[map[string]any](), true},
		{"overflow", `1e999`, newOf[any](), false},
		{"broken literal", `tru`, newOf[any](), false},
		{"nil spelling", `nil`, newOf[any](), false},
		{"bad token", `+`, newOf[any](), false},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
	// The option reaches json.Number on every entry point; encoding/json
	// sets it on the Decoder.
	for _, in := range []string{`1.5e3`, `{"a":[1,2]}`} {
		var std any
		d := json.NewDecoder(strings.NewReader(in))
		d.UseNumber()
		if err := d.Decode(&std); err != nil {
			t.Fatal(err)
		}
		var got any
		if err := vjson.Unmarshal([]byte(in), &got, vjson.UseNumber(true)); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !reflect.DeepEqual(got, std) {
			t.Errorf("%s: UseNumber decoded %#v, encoding/json %#v", in, got, std)
		}
	}
}
