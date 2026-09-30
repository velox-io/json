package gdec

import (
	"strings"
	"testing"
)

func validUTF8Cases() []string {
	return []string{
		"",
		"a",
		"\x7f",
		"\xc2\x80",
		"\xdf\xbf",
		"\xe0\xa0\x80",
		"\xe1\x80\x80",
		"\xec\xbf\xbf",
		"\xed\x9f\xbf",
		"\xee\x80\x80",
		"\xef\xbf\xbf",
		"\xf0\x90\x80\x80",
		"\xf1\x80\x80\x80",
		"\xf3\xbf\xbf\xbf",
		"\xf4\x8f\xbf\xbf",
		"a\xc2\x80b\xf0\x90\x80\x80c",
		strings.Repeat("\xe4\xb8\x96\xe7\x95\x8c", 40),
	}
}

func invalidUTF8Cases() []string {
	return []string{
		"\x80",                 // lone continuation
		"\xbf",                 // lone continuation
		"\xc0\x80",             // overlong NUL
		"\xc1\xbf",             // overlong
		"\xc2",                 // truncated lead
		"\xe0\x80\x80",         // overlong 3-byte
		"\xe0\x9f\xbf",         // overlong 3-byte
		"\xed\xa0\x80",         // surrogate D800
		"\xed\xbf\xbf",         // surrogate DFFF
		"\xee\x80",             // truncated 3-byte
		"\xf0\x80\x80\x80",     // overlong 4-byte
		"\xf0\x8f\xbf\xbf",     // overlong 4-byte
		"\xf4\x90\x80\x80",     // past U+10FFFF
		"\xf5\x80\x80\x80",     // invalid lead
		"\xf8\x80\x80\x80\x80", // 5-byte lead
		"\xff",                 // invalid lead
		"\xe4\xb8",             // truncated CJK
		"\xe4\xb8\x96\xe7",     // valid then truncated
		"a\x80b",               // continuation between ASCII
	}
}

// TestValidateBodyUTF8 checks ValidateBody against utf8.Valid over spans,
// padded with escape bytes and stop bytes the body walk passes around.
func TestValidateBodyUTF8(t *testing.T) {
	for _, s := range validUTF8Cases() {
		if !ValidateBody([]byte(s), 0, len(s)) {
			t.Errorf("ValidateBody(%q) = false, want true", s)
		}
	}
	for _, s := range invalidUTF8Cases() {
		if ValidateBody([]byte(s), 0, len(s)) {
			t.Errorf("ValidateBody(%q) = true, want false", s)
		}
	}
}

// TestValidateBodyCtl rejects every raw byte below 0x20 alone, and accepts
// them escaped.
func TestValidateBodyCtl(t *testing.T) {
	for b := 0; b < 0x20; b++ {
		src := []byte{'a', byte(b), 'b'}
		if ValidateBody(src, 0, len(src)) {
			t.Errorf("ValidateBody accepted raw byte %d", b)
		}
	}
	if !ValidateBody([]byte("a\\nb\\tc\\x"), 0, 9) {
		t.Error("ValidateBody rejected escape bytes")
	}
}

// TestValidateBodySpanSplits runs every case at every split of an interior
// span, so word boundaries and the two loops both cover it.
func TestValidateBodySpanSplits(t *testing.T) {
	for _, s := range append(validUTF8Cases(), invalidUTF8Cases()...) {
		src := []byte(`0123456789` + s + `abcdefgh`)
		for i := range len(s) + 1 {
			lo, hi := 10, 10+i
			got := ValidateBody(src, lo, hi)
			want := ValidateBody([]byte(s), 0, i)
			if got != want {
				t.Errorf("ValidateBody(%q, %d, %d) = %v, want %v", s, lo, hi, got, want)
			}
		}
	}
}

// TestValidateBodyBoundarySplits slides a multibyte sequence across 8-byte
// word boundaries at every offset.
func TestValidateBodyBoundarySplits(t *testing.T) {
	for _, seq := range []string{"\xc2\x80", "\xe4\xb8\x96", "\xf0\x90\x80\x80"} {
		for off := 0; off < 24; off++ {
			for _, bad := range []bool{false, true} {
				s := append([]byte(strings.Repeat("x", off)), seq...)
				if bad {
					s = append(s, 0x80) // stray continuation after the sequence
				}
				want := !bad
				if got := ValidateBody(s, 0, len(s)); got != want {
					t.Errorf("ValidateBody(off=%d, bad=%v) = %v, want %v", off, bad, got, want)
				}
			}
		}
	}
}

// TestQuoteOrEscapeBodyStops checks the fused stop against QuoteOrEscape
// and its verdict against ValidateBody over the span walked.
func TestQuoteOrEscapeBodyStops(t *testing.T) {
	inputs := []string{
		``,
		`abc`,
		`ab"c`,
		`ab\nc`,
		`\`,
		`\"`,
		"\xc2\x80",
		"\xc2\x80\"x",
		"a\xc2\x80b\\\"c",
		"a\x01b\"c",
		"\xe4\xb8\x96\xe7\x95\x8c",
		"\xed\xa0\x80x",
		"a\xed\xa0\x80b",
		"\xff",
		`aaa\xff"tail`,
		"\xe4\xb8",
	}
	for _, in := range inputs {
		src := []byte(in)
		stop := QuoteOrEscapeBody(src, 0)
		want := QuoteOrEscape(src, 0)
		if stop >= 0 {
			if stop != want {
				t.Errorf("QuoteOrEscapeBody(%q) = %d, want stop %d", in, stop, want)
				continue
			}
			if ok := ValidateBody(src, 0, stop); ok != (stop >= 0) {
				t.Errorf("stop %d of %q disagrees with ValidateBody", stop, in)
			}
		}
	}
}

// TestQuoteOrEscapeBodyPastStop checks that a violation past the stop
// leaves the span before it valid, as the bytes past a stop belong to the
// next token.
func TestQuoteOrEscapeBodyPastStop(t *testing.T) {
	src := []byte("ab\"c\xff")
	if stop := QuoteOrEscapeBody(src, 0); stop != 2 {
		t.Fatalf("stop = %d, want 2", stop)
	}
	if bad := []byte("a\xff\"b\x01"); QuoteOrEscapeBody(bad, 0) != -1 {
		t.Fatal("violation before the stop was not reported")
	}
}
