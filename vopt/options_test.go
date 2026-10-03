package vopt

import "testing"

// The presence/value mask pair is the contract every entry point relies on:
// unnamed means default, named-false must be distinguishable from unnamed.
// These tests pin that tri-state through Join, the accessors, and the carried
// Indent/BufSize payloads.

func TestZeroValueNamesNothing(t *testing.T) {
	var o Options
	for _, f := range []Flag{
		FlagAllowInvalidUTF8, FlagEscapeHTML, FlagEscapeLineTerms, FlagFloatExpAuto,
		FlagUseNumber, FlagRejectUnknownMembers, FlagZeroCopy, FlagSkipLenient,
	} {
		if o.Has(f) {
			t.Fatalf("zero Options Has(%v) = true, want false", f)
		}
		if o.Enabled(f) {
			t.Fatalf("zero Options Enabled(%v) = true, want false", f)
		}
		if _, ok := o.Get(f); ok {
			t.Fatalf("zero Options Get(%v) ok = true, want false", f)
		}
	}
	if _, _, ok := o.Indent(); ok {
		t.Fatal("zero Options Indent() ok = true, want false")
	}
	if o.BufSize() != 0 {
		t.Fatalf("zero Options BufSize() = %d, want 0", o.BufSize())
	}
}

func TestConstructorsNameTheirFlag(t *testing.T) {
	cases := []struct {
		opt  Options
		flag Flag
	}{
		{AllowInvalidUTF8(true), FlagAllowInvalidUTF8},
		{AllowInvalidUTF8(false), FlagAllowInvalidUTF8},
		{EscapeHTML(true), FlagEscapeHTML},
		{EscapeLineTerms(true), FlagEscapeLineTerms},
		{FloatExpAuto(true), FlagFloatExpAuto},
		{UseNumber(true), FlagUseNumber},
		{RejectUnknownMembers(true), FlagRejectUnknownMembers},
		{ZeroCopy(true), FlagZeroCopy},
		{ZeroCopy(false), FlagZeroCopy},
		{SkipLenient(true), FlagSkipLenient},
	}
	for _, c := range cases {
		if !c.opt.Has(c.flag) {
			t.Fatalf("%#v Has(%v) = false, want true", c.opt, c.flag)
		}
		v, ok := c.opt.Get(c.flag)
		if !ok {
			t.Fatalf("%#v Get(%v) ok = false, want true", c.opt, c.flag)
		}
		if v != c.opt.Enabled(c.flag) {
			t.Fatalf("%#v Get/Enabled disagree on %v: %v vs %v", c.opt, c.flag, v, c.opt.Enabled(c.flag))
		}
	}
}

func TestJoinOverrideSemantics(t *testing.T) {
	o := Join(UseNumber(true), ZeroCopy(true))
	if !o.Enabled(FlagUseNumber) || !o.Enabled(FlagZeroCopy) {
		t.Fatalf("Join(true,true): UseNumber=%v ZeroCopy=%v, want both true",
			o.Enabled(FlagUseNumber), o.Enabled(FlagZeroCopy))
	}

	// A later explicit false overrides an earlier true, not just masks it.
	o = Join(UseNumber(true), UseNumber(false))
	if !o.Has(FlagUseNumber) {
		t.Fatal("Join(true,false): Has(FlagUseNumber) = false, want true")
	}
	if o.Enabled(FlagUseNumber) {
		t.Fatal("Join(UseNumber(true), UseNumber(false)): Enabled = true, want false")
	}

	// A later true overrides an earlier explicit false.
	o = Join(ZeroCopy(false), ZeroCopy(true))
	if !o.Enabled(FlagZeroCopy) {
		t.Fatal("Join(ZeroCopy(false), ZeroCopy(true)): Enabled = false, want true")
	}

	// Options one side leaves unnamed stay untouched by the other side.
	o = Join(UseNumber(true), EscapeHTML(true))
	if !o.Enabled(FlagUseNumber) || !o.Enabled(FlagEscapeHTML) {
		t.Fatalf("Join(UseNumber, EscapeHTML): UseNumber=%v EscapeHTML=%v, want both true",
			o.Enabled(FlagUseNumber), o.Enabled(FlagEscapeHTML))
	}

	// The empty Join names nothing.
	if o := Join(); o.set != 0 || o.val != 0 {
		t.Fatalf("Join() = set %#x val %#x, want zero masks", o.set, o.val)
	}
}

func TestJoinCarriesIndentAndBufSize(t *testing.T) {
	o := Join(Indent("", "  "), UseNumber(true))
	prefix, step, ok := o.Indent()
	if !ok || prefix != "" || step != "  " {
		t.Fatalf("Indent after Join = (%q, %q, %v), want (\"\", \"  \", true)", prefix, step, ok)
	}
	if !o.Enabled(FlagUseNumber) {
		t.Fatal("UseNumber lost across Join with Indent")
	}

	// A later Indent replaces an earlier one.
	o = Join(Indent("a", "1"), Indent("b", "2"))
	prefix, step, _ = o.Indent()
	if prefix != "b" || step != "2" {
		t.Fatalf("Indent override = (%q, %q), want (\"b\", \"2\")", prefix, step)
	}

	// A Join that does not name Indent keeps the carried indentation.
	o = Join(Indent("a", "1"), UseNumber(true))
	prefix, step, ok = o.Indent()
	if !ok || prefix != "a" || step != "1" {
		t.Fatalf("Indent retention = (%q, %q, %v), want (\"a\", \"1\", true)", prefix, step, ok)
	}

	o = Join(BufSize(4096), UseNumber(true))
	if o.BufSize() != 4096 {
		t.Fatalf("BufSize after Join = %d, want 4096", o.BufSize())
	}
	o = Join(BufSize(4096), BufSize(64))
	if o.BufSize() != 64 {
		t.Fatalf("BufSize override = %d, want 64", o.BufSize())
	}
}
