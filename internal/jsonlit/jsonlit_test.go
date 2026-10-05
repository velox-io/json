package jsonlit

import (
	"testing"
	"unsafe"
)

func TestUnquote(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`""`, "", true},
		{`"plain"`, "plain", true},
		{`"a\"b\\c\/d\b\f\n\r\t"`, "a\"b\\c/d\b\f\n\r\t", true},
		{`"é€"`, "é€", true},
		{`"😀"`, "😀", true},
		{`"\ud83d x"`, "� x", true}, // unpaired surrogate
		{`"\x"`, "", false},
		{`"\u12"`, "", false},
		{`"tail\"`, "", false},
		{`plain`, "", false},
		{`"`, "", false},
	}
	for _, tc := range cases {
		got, ok := Unquote([]byte(tc.in))
		gotS, okS := UnquoteString([]byte(tc.in))
		if ok != tc.ok || okS != tc.ok || (tc.ok && (string(got) != tc.want || gotS != tc.want)) {
			t.Errorf("Unquote(%s) = %q %v, UnquoteString = %q %v; want %q %v", tc.in, got, ok, gotS, okS, tc.want, tc.ok)
		}
	}
}

func TestUnquoteAliasesEscapeFreeInput(t *testing.T) {
	q := []byte(`"abc"`)
	got, _ := Unquote(q)
	if unsafe.SliceData(got) != &q[1] {
		t.Fatal("escape-free Unquote copied its input")
	}
}

func TestIsNumber(t *testing.T) {
	for in, want := range map[string]bool{
		"0": true, "-0": true, "12.5e-3": true, "1E+9": true,
		"": false, "-": false, "01": false, "1.": false, ".5": false, "1e": false, "+1": false, "1 ": false, "0x1": false,
	} {
		if got := IsNumber([]byte(in)); got != want {
			t.Errorf("IsNumber(%q) = %v, want %v", in, got, want)
		}
	}
}
