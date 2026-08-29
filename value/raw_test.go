package value

import (
	"encoding/json"
	"testing"
)

// val builds a Raw from a string literal for tests. The Raw aliases the
// converted bytes; tests do not require a defensive copy.
func val(s string) Raw { return Raw([]byte(s)) }

func TestType(t *testing.T) {
	tests := []struct {
		in   string
		want Kind
	}{
		{`{"a":1}`, KindObject},
		{`[1,2]`, KindArray},
		{`"s"`, KindString},
		{`123`, KindNumber},
		{`-1.5e3`, KindNumber},
		{`true`, KindBool},
		{`false`, KindBool},
		{`null`, KindNull},
		{`  {"a":1}  `, KindObject},
		{``, KindInvalid},
		{`   `, KindInvalid},
		{`xyz`, KindInvalid},
	}
	for _, tc := range tests {
		if got := val(tc.in).Type(); got != tc.want {
			t.Errorf("val(%q).Type() = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGetNested(t *testing.T) {
	v := val(`{"a":{"b":{"c":"deep"}},"n":42}`)

	if got, ok := v.GetString("a", "b", "c"); !ok || got != "deep" {
		t.Errorf(`GetString("a","b","c") = %q, %v; want "deep", true`, got, ok)
	}
	if got, ok := v.GetInt("n"); !ok || got != 42 {
		t.Errorf(`GetInt("n") = %d, %v; want 42, true`, got, ok)
	}
	if v.Get("a", "missing").Exists() {
		t.Error(`Get("a","missing") should not exist`)
	}
	if v.Get("n", "b").Exists() {
		t.Error(`Get("n","b") should not exist: n is not an object`)
	}
	if got := v.Get("a", "b"); got.String() != `{"c":"deep"}` {
		t.Errorf(`Get("a","b") = %s`, got)
	}
}

// Whitespace inside a captured span is preserved by the decoder, so every
// reader must skip it. See TestValue_WhitespacePreserved in tests/.
func TestReadersSkipWhitespace(t *testing.T) {
	v := val(`{ "x" : [ 1 , 2 , 3 ] , "y" : "z" }`)

	if n := v.Len(); n != 2 {
		t.Errorf("Len() = %d, want 2", n)
	}
	arr := v.Get("x")
	if n := arr.Len(); n != 3 {
		t.Errorf(`Get("x").Len() = %d, want 3`, n)
	}
	if got, ok := arr.Index(1).Int(); !ok || got != 2 {
		t.Errorf("Index(1).Int() = %d, %v; want 2, true", got, ok)
	}
	if got, ok := v.GetString("y"); !ok || got != "z" {
		t.Errorf(`GetString("y") = %q, %v; want "z", true`, got, ok)
	}

	var keys []string
	v.ForEachKey(func(k string, _ Raw) bool { keys = append(keys, k); return true })
	if len(keys) != 2 || keys[0] != "x" || keys[1] != "y" {
		t.Errorf("ForEachKey keys = %v, want [x y]", keys)
	}

	var sum int64
	arr.ForEachElem(func(_ int, e Raw) bool {
		n, _ := e.Int()
		sum += n
		return true
	})
	if sum != 6 {
		t.Errorf("ForEachElem sum = %d, want 6", sum)
	}
}

func TestIndex(t *testing.T) {
	v := val(`["a","b","c"]`)
	for i, want := range []string{"a", "b", "c"} {
		if got, ok := v.Index(i).Str(); !ok || got != want {
			t.Errorf("Index(%d) = %q, %v; want %q", i, got, ok, want)
		}
	}
	if got, ok := v.Index(-1).Str(); !ok || got != "c" {
		t.Errorf("Index(-1) = %q, %v; want c", got, ok)
	}
	if v.Index(3).Exists() {
		t.Error("Index(3) should not exist")
	}
	if v.Index(-4).Exists() {
		t.Error("Index(-4) should not exist")
	}
	if val(`{"a":1}`).Index(0).Exists() {
		t.Error("Index on an object should not exist")
	}
}

func TestEmptyContainers(t *testing.T) {
	for _, in := range []string{`{}`, `[]`, `{ }`, `[ ]`} {
		v := val(in)
		if n := v.Len(); n != 0 {
			t.Errorf("val(%q).Len() = %d, want 0", in, n)
		}
		called := false
		v.ForEachKey(func(string, Raw) bool { called = true; return true })
		v.ForEachElem(func(int, Raw) bool { called = true; return true })
		if called {
			t.Errorf("val(%q): callback ran on empty container", in)
		}
		if !v.Valid() {
			t.Errorf("val(%q).Valid() = false, want true", in)
		}
	}
}

func TestForEachEarlyStop(t *testing.T) {
	n := 0
	val(`[1,2,3,4,5]`).ForEachElem(func(int, Raw) bool { n++; return n < 2 })
	if n != 2 {
		t.Errorf("ForEachElem ran %d times, want 2", n)
	}

	n = 0
	val(`{"a":1,"b":2,"c":3}`).ForEachKey(func(string, Raw) bool { n++; return false })
	if n != 1 {
		t.Errorf("ForEachKey ran %d times, want 1", n)
	}
}

func TestScalars(t *testing.T) {
	if got, ok := val(`3`).Int(); !ok || got != 3 {
		t.Errorf("Int() = %d, %v", got, ok)
	}
	// Integral floats are accepted by Int.
	if got, ok := val(`1e3`).Int(); !ok || got != 1000 {
		t.Errorf("val(1e3).Int() = %d, %v; want 1000, true", got, ok)
	}
	if got, ok := val(`2.0`).Int(); !ok || got != 2 {
		t.Errorf("val(2.0).Int() = %d, %v; want 2, true", got, ok)
	}
	if _, ok := val(`1.5`).Int(); ok {
		t.Error("val(1.5).Int() should fail")
	}
	if got, ok := val(`-1.5e2`).Float(); !ok || got != -150 {
		t.Errorf("Float() = %v, %v; want -150", got, ok)
	}
	if got, ok := val(`true`).Bool(); !ok || !got {
		t.Errorf("Bool() = %v, %v", got, ok)
	}
	if got, ok := val(` false `).Bool(); !ok || got {
		t.Errorf("Bool() = %v, %v; want false, true", got, ok)
	}
	if !val(`null`).IsNull() {
		t.Error("IsNull() = false")
	}
	// Wrong kinds must not succeed.
	if _, ok := val(`"5"`).Int(); ok {
		t.Error(`val("5").Int() should fail: it is a string`)
	}
	if _, ok := val(`{"a":1}`).Str(); ok {
		t.Error("Str() on an object should fail")
	}
	if _, ok := val(`truex`).Bool(); ok {
		t.Error("val(truex).Bool() should fail")
	}
}

func TestStrEscapes(t *testing.T) {
	tests := []struct{ in, want string }{
		{`"plain"`, "plain"},
		{`""`, ""},
		{`"a\"b"`, `a"b`},
		{`"a\\b"`, `a\b`},
		{`"a\/b"`, "a/b"},
		{`"\b\f\n\r\t"`, "\b\f\n\r\t"},
		{`"A"`, "A"},
		{`"tab\there"`, "tab\there"},
		{`"é"`, "é"},
		// Surrogate pair for U+1F600.
		{`"😀"`, "😀"},
		{`"pre😀post"`, "pre😀post"},
		// Unpaired surrogates become U+FFFD, matching encoding/json.
		{`"\ud83d"`, "�"},
		{`"\ude00"`, "�"},
	}
	for _, tc := range tests {
		got, ok := val(tc.in).Str()
		if !ok {
			t.Errorf("val(%s).Str() failed", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("val(%s).Str() = %q, want %q", tc.in, got, tc.want)
		}
		// Cross check against the standard library.
		var std string
		if err := json.Unmarshal([]byte(tc.in), &std); err != nil {
			t.Errorf("stdlib rejected %s: %v", tc.in, err)
			continue
		}
		if got != std {
			t.Errorf("val(%s).Str() = %q, stdlib = %q", tc.in, got, std)
		}
	}
}

func TestEscapedKeys(t *testing.T) {
	v := val(`{"a\"b":1,"c":2}`)
	if got, ok := v.GetInt(`a"b`); !ok || got != 1 {
		t.Errorf(`GetInt("a\"b") = %d, %v; want 1`, got, ok)
	}
	if got, ok := v.GetInt("c"); !ok || got != 2 {
		t.Errorf(`GetInt("c") = %d, %v; want 2`, got, ok)
	}
}

// Malformed input must never panic, and must report absence rather than a
// wrong answer.
func TestMalformedNoPanic(t *testing.T) {
	inputs := []string{
		``, `   `, `{`, `[`, `"`, `"unterminated`,
		`{"a"`, `{"a":`, `{"a":}`, `{"a":1`, `{"a":1,}`, `{,}`, `{"a" 1}`,
		`[1,`, `[1,]`, `[,]`, `[1 2]`,
		`{"a":tru}`, `tru`, `nul`, `fals`,
		`01`, `-`, `1.`, `.5`, `1e`, `1e+`, `--1`, `1.2.3`,
		`"\x"`, `"\u12"`, `"\uZZZZ"`, `"a` + "\x01" + `b"`,
		`{"a":{"b":}}`, `[[[`, `}`, `]`, `:`, `,`,
	}
	for _, in := range inputs {
		v := val(in)
		// None of these may panic.
		_ = v.Type()
		_ = v.Len()
		_ = v.Exists()
		_, _ = v.Str()
		_, _ = v.Int()
		_, _ = v.Float()
		_, _ = v.Bool()
		_ = v.Get("a")
		_ = v.Get("a", "b")
		_ = v.Index(0)
		v.ForEachKey(func(string, Raw) bool { return true })
		v.ForEachElem(func(int, Raw) bool { return true })

		if v.Valid() {
			t.Errorf("val(%q).Valid() = true, want false", in)
		}
	}
}

// The decoder locates a value by matching brackets and does not check scalar
// syntax inside the span, so a captured Raw can be bracket balanced yet
// malformed. Valid is the way to detect that.
func TestValidCatchesBalancedButMalformed(t *testing.T) {
	v := val(`{"a":tru}`)
	if v.Valid() {
		t.Error("Valid() = true for {\"a\":tru}, want false")
	}
	// Type only looks at the first byte, so it still reports object.
	if got := v.Type(); got != KindObject {
		t.Errorf("Type() = %v, want object", got)
	}
	// The malformed member is not readable.
	if v.Get("a").Exists() {
		t.Error(`Get("a") should not exist in a malformed object`)
	}
}

func TestValid(t *testing.T) {
	valid := []string{
		`{}`, `[]`, `null`, `true`, `false`, `0`, `-0`, `1.5`, `1e10`, `1E+10`, `-1.5e-10`,
		`"s"`, `"A"`, `"😀"`,
		`{"a":[1,{"b":null}]}`, `  [ 1 , 2 ]  `, `[[],{}]`,
	}
	for _, in := range valid {
		if !val(in).Valid() {
			t.Errorf("val(%q).Valid() = false, want true", in)
		}
		if !json.Valid([]byte(in)) {
			t.Errorf("stdlib disagrees: json.Valid(%q) = false", in)
		}
	}

	invalid := []string{`{}{}`, `[] x`, `1 2`, `nulll`, `+1`, `0x1`, `'s'`, `[1,2,]`}
	for _, in := range invalid {
		if val(in).Valid() {
			t.Errorf("val(%q).Valid() = true, want false", in)
		}
		if json.Valid([]byte(in)) {
			t.Errorf("stdlib disagrees: json.Valid(%q) = true", in)
		}
	}
}

func TestExists(t *testing.T) {
	if (Raw{}).Exists() {
		t.Error("nil Raw should not exist")
	}
	if val(``).Exists() {
		t.Error("empty Raw should not exist")
	}
	if val(`  `).Exists() {
		t.Error("whitespace only Raw should not exist")
	}
	if !val(`null`).Exists() {
		t.Error("JSON null is present, so Exists should be true")
	}
	if !val(`0`).Exists() {
		t.Error("val(0) should exist")
	}
}

func TestMarshalUnmarshalJSON(t *testing.T) {
	// Empty marshals as null.
	got, err := (Raw{}).MarshalJSON()
	if err != nil || string(got) != "null" {
		t.Errorf("(Raw{}).MarshalJSON() = %s, %v; want null", got, err)
	}
	// Non empty passes through verbatim, whitespace and all.
	raw := `{ "a" : 1 }`
	got, err = val(raw).MarshalJSON()
	if err != nil || string(got) != raw {
		t.Errorf("MarshalJSON() = %s, %v; want %s", got, err, raw)
	}

	// UnmarshalJSON copies, so the source may be reused afterwards.
	src := []byte(`{"a":1}`)
	var v Raw
	if uerr := v.UnmarshalJSON(src); uerr != nil {
		t.Fatal(uerr)
	}
	src[2] = 'X'
	if v.String() != `{"a":1}` {
		t.Errorf("Raw aliased its input: got %s", v)
	}

	// Raw works through encoding/json in both directions.
	type S struct {
		D Raw `json:"d"`
	}
	var s S
	if jerr := json.Unmarshal([]byte(`{"d":{"k":[1,2]}}`), &s); jerr != nil {
		t.Fatal(jerr)
	}
	if s.D.String() != `{"k":[1,2]}` {
		t.Errorf("stdlib decode gave %s", s.D)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"d":{"k":[1,2]}}` {
		t.Errorf("stdlib encode gave %s", out)
	}
}

func TestKindString(t *testing.T) {
	tests := []struct {
		k    Kind
		want string
	}{
		{KindInvalid, "invalid"},
		{KindNull, "null"},
		{KindBool, "bool"},
		{KindNumber, "number"},
		{KindString, "string"},
		{KindArray, "array"},
		{KindObject, "object"},
	}
	for _, tc := range tests {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

// Nested values handed to callbacks must be independently readable.
func TestNestedIteration(t *testing.T) {
	v := val(`{"users":[{"id":1,"name":"a"},{"id":2,"name":"b"}]}`)
	type user struct {
		id   int64
		name string
	}
	var got []user
	v.Get("users").ForEachElem(func(_ int, e Raw) bool {
		id, _ := e.GetInt("id")
		name, _ := e.GetString("name")
		got = append(got, user{id, name})
		return true
	})
	want := []user{{1, "a"}, {2, "b"}}
	if len(got) != len(want) {
		t.Fatalf("got %d users, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("user %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDeeplyNested(t *testing.T) {
	// Deep nesting must not blow up or misreport.
	const depth = 200
	buf := make([]byte, 0, depth*2+2)
	for range depth {
		buf = append(buf, '[')
	}
	buf = append(buf, '1')
	for range depth {
		buf = append(buf, ']')
	}
	v := Raw(buf)
	if !v.Valid() {
		t.Error("Valid() = false for deeply nested array")
	}
	cur := v
	for range depth {
		cur = cur.Index(0)
	}
	if got, ok := cur.Int(); !ok || got != 1 {
		t.Errorf("innermost = %d, %v; want 1", got, ok)
	}
}

func FuzzRawReaders(f *testing.F) {
	seeds := []string{
		`{"a":1}`, `[1,2,3]`, `"s"`, `null`, `{ "x" : [ 1 ] }`,
		`{"a":tru}`, `{`, ``, `"😀"`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v := Raw(data)
		// No reader may panic on arbitrary input.
		_ = v.Type()
		_ = v.Len()
		_ = v.Exists()
		_, _ = v.Str()
		_, _ = v.Int()
		_, _ = v.Float()
		_, _ = v.Bool()
		_ = v.Get("a", "b")
		_ = v.Index(0)
		v.ForEachKey(func(_ string, e Raw) bool { _ = e.Type(); return true })
		v.ForEachElem(func(_ int, e Raw) bool { _ = e.Type(); return true })

		// Valid must agree with the standard library.
		if got, want := v.Valid(), json.Valid(data); got != want {
			t.Errorf("Valid() = %v, json.Valid() = %v for %q", got, want, data)
		}
	})
}
