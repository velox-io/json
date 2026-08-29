package bind

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/decode/option"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/vbind"
)

// Numbers no binary form represents faithfully reach the tape as TagNumRaw: the
// source text, rather than a rounded double. These tests pin the two things that
// buys, and the one thing that must not change.

// Tokens past 19 significant digits or past float64's range. Each is a value
// float64 cannot hold, which is the whole condition for the tag.
var numRawTokens = []string{
	`123456789012345678901234567890`,
	`-123456789012345678901234567890`,
	`1.2345678901234567890123456789`,
	`12345678901234567890123456789012345678901234567890`,
	`1e400`,
	`-1e400`,
	`123456789012345678901234567890e400`,
}

// Ordinary numbers, which must keep taking the binary tags. Listed here so the
// no-regression test below and the tag test cannot drift apart.
var numBinaryTokens = []string{
	`0`, `1`, `-1`, `100`, `0.1`, `3.14`, `-2.5`, `1e2`, `1E+2`, `-0.0`,
	`18446744073709551615`,   // UINT64_MAX, exactly representable as 'u'
	`-9223372036854775808`,   // INT64_MIN, exactly representable as 'l'
	`1e-400`,                 // underflows to signed zero, which IS faithful
	`0.00000000000000000123`, // 21 chars but 3 significant digits
}

// A Value holding a number this large re-serializes to the original bytes. That
// is the point of the tag: a rounded double could not, and before the tag these
// same inputs came back as 1.2345678901234568e+29 or were rejected outright.
func TestNumRaw_ReforwardsSourceBytes(t *testing.T) {
	for _, tok := range numRawTokens {
		for _, src := range []string{
			tok,
			`{"a":` + tok + `}`,
			`[` + tok + `]`,
			// Followed by a sibling, so a wrong word count for the one-word tag
			// would land the walk mid-entry rather than on the next value.
			`[` + tok + `,7,"x"]`,
			`{"a":` + tok + `,"b":7}`,
		} {
			v, err := dom.Parse([]byte(src))
			if err != nil {
				t.Errorf("%s: Parse: %v", src, err)
				continue
			}
			if got := v.String(); got != src {
				t.Errorf("reforward mismatch\n  src=%s\n  got=%s", src, got)
			}
		}
	}
}

// The tag is emitted only where a binary form would be unfaithful. Ordinary
// numbers keep their binary tags, so the hot path is untouched; asserting the tag
// (not just the value) is what makes that visible.
func TestNumRaw_TagBoundary(t *testing.T) {
	check := func(tok string, wantRaw bool) {
		v, err := dom.Parse([]byte(tok))
		if err != nil {
			t.Errorf("%s: Parse: %v", tok, err)
			return
		}
		tag := valueDescriptor(&v).TagAt(0)
		isRaw := tag == valueabi.TagNumRaw
		if isRaw != wantRaw {
			t.Errorf("%s: tag=%q rawText=%v, want rawText=%v", tok, tag, isRaw, wantRaw)
			return
		}
		if !isRaw && tag != valueabi.TagInt64 && tag != valueabi.TagUint64 && tag != valueabi.TagDouble {
			t.Errorf("%s: tag=%q is not a number tag", tok, tag)
		}
	}
	for _, tok := range numRawTokens {
		check(tok, true)
	}
	for _, tok := range numBinaryTokens {
		check(tok, false)
	}
}

// TagNumRaw is one word where the binary tags are two. Skip must therefore step
// by one, or every sibling after such a number is read from the wrong offset.
// Checked positionally rather than through re-serialization so a failure names
// the offset instead of a mangled string.
func TestNumRaw_SkipIsOneWord(t *testing.T) {
	v, err := dom.Parse([]byte(`[1e400,42]`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// A dom-built tape is contiguous (no seams), so the first element sits at 1.
	first := 1
	desc := valueDescriptor(&v)
	if tag := desc.TagAt(first); tag != valueabi.TagNumRaw {
		t.Fatalf("element 0 tag=%q, want %q", tag, valueabi.TagNumRaw)
	}
	second := desc.Skip(first)
	if second != first+1 {
		t.Errorf("Skip advanced %d words, want 1 (TagNumRaw carries no value word)", second-first)
	}
	if tag := desc.TagAt(second); tag != valueabi.TagInt64 {
		t.Errorf("element 1 tag=%q, want %q; the skip landed in the wrong place", tag, valueabi.TagInt64)
	}
}

// Numeric accessors read the text. A float target rounds, and reports failure
// past its range rather than silently yielding Inf.
func TestNumRaw_Accessors(t *testing.T) {
	cases := []struct {
		tok     string
		wantF   float64
		okFloat bool
		wantI   int64
		okInt   bool
	}{
		{`123456789012345678901234567890`, 1.2345678901234568e29, true, 0, false},
		{`1e400`, 0, false, 0, false},
		{`-1e400`, 0, false, 0, false},
	}
	for _, c := range cases {
		v, err := dom.Parse([]byte(c.tok))
		if err != nil {
			t.Errorf("%s: Parse: %v", c.tok, err)
			continue
		}
		if got, ok := v.Float(); ok != c.okFloat || (ok && got != c.wantF) {
			t.Errorf("%s: Float = (%v, %v), want (%v, %v)", c.tok, got, ok, c.wantF, c.okFloat)
		}
		if got, ok := v.Int(); ok != c.okInt || (ok && got != c.wantI) {
			t.Errorf("%s: Int = (%v, %v), want (%v, %v)", c.tok, got, ok, c.wantI, c.okInt)
		}
		if raw := string(valueDescriptor(&v).NumRawAt(0)); raw != c.tok {
			t.Errorf("%s: NumRawAt = %q", c.tok, raw)
		}
	}
}

type numRawTargets struct {
	F float64 `json:"f"`
	A any     `json:"a"`
}

// Integer targets must refuse these tokens rather than round through a double.
// The tag is only emitted when no integer type holds the value, so accepting one
// could only mean silently storing a different number than the source named.
//
// Both paths are checked: the tape path (variant case with the discriminator
// last) has its own conversion, and encoding/json is the reference for both.
type numRawIntTarget struct {
	I int64  `json:"i"`
	U uint64 `json:"u"`
	S int32  `json:"s"`
}

type numRawIntCase struct {
	I int64 `json:"i"`
}

func TestNumRaw_IntegerTargetsRefuse(t *testing.T) {
	for _, tok := range numRawTokens {
		for _, field := range []string{"i", "u", "s"} {
			src := `{"` + field + `":` + tok + `}`
			var got numRawIntTarget
			err := Unmarshal([]byte(src), &got)
			if err == nil {
				t.Errorf("%s: accepted into an integer target as %+v; no integer type holds this value", src, got)
			}
			var ref numRawIntTarget
			if json.Unmarshal([]byte(src), &ref) == nil {
				t.Errorf("%s: encoding/json accepts this; the reference changed", src)
			}
		}
	}
	// Same through the tape path.
	for _, tok := range numRawTokens {
		src := `{"data":{"i":` + tok + `},"type":"ic"}`
		var h numRawIntHost
		if err := Unmarshal([]byte(src), &h); err == nil {
			t.Errorf("%s: tape path accepted into int64 as %+v", src, h.Data)
		}
	}
}

type numRawIntHost struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

// encoding/json, the JSON bind path, and the tape-bind path must agree, including
// on which inputs fail. A float64 target cannot hold these values, so the
// interesting part is that all three refuse or accept together.
func TestNumRaw_Parity3(t *testing.T) {
	for i, tok := range numRawTokens {
		parity3[numRawTargets](t, fmt.Sprintf("f%d", i), `{"f":`+tok+`}`)
		parity3[numRawTargets](t, fmt.Sprintf("a%d", i), `{"a":`+tok+`}`)
	}
	for i, tok := range numBinaryTokens {
		parity3[numRawTargets](t, fmt.Sprintf("bin_f%d", i), `{"f":`+tok+`}`)
		parity3[numRawTargets](t, fmt.Sprintf("bin_a%d", i), `{"a":`+tok+`}`)
	}
}

// A variant case is bound from JSON when the discriminator comes first and from
// the tape when it comes last, so the same document takes different paths purely
// on key order. Those paths must not disagree.
type numPathCase struct {
	A any     `json:"a"`
	F float64 `json:"f"`
}

type numPathHost struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[numPathHost, struct {
		_ numPathCase `case:"c"`
	}]()
	vbind.DefineVariantCases[numRawIntHost, struct {
		_ numRawIntCase `case:"ic"`
	}]()
}

// Before TagNumRaw the tape path silently degraded: with UseNumber, a 30-digit
// number bound through JSON became json.Number "123..." and through the tape
// became float64 1.23e+29. Key order alone decided which, which is the bug this
// pins shut.
//
// Scope is the TagNumRaw tokens plus ordinary numbers WITHOUT UseNumber. Ordinary
// numbers under UseNumber still differ by path, because l/u/d keep no text for
// json.Number to hold; that gap predates this tag and is what
// TestNumRaw_UseNumberBinaryTagsStillDiffer records.
func TestNumRaw_KeyOrderDoesNotChangeResult(t *testing.T) {
	// Renders the outcome as a comparable string: either the error class or the
	// bound value plus the dynamic type of the any field, since json.Number and
	// float64 can print identically.
	render := func(src string, opts ...option.Option) string {
		var h numPathHost
		if err := Unmarshal([]byte(src), &h, opts...); err != nil {
			return "error"
		}
		c, _ := h.Data.(numPathCase)
		out, err := json.Marshal(c)
		if err != nil {
			return "marshal-error"
		}
		return fmt.Sprintf("%s|%T", out, c.A)
	}
	compare := func(t *testing.T, body string, opts ...option.Option) {
		t.Helper()
		fromJSON := render(`{"type":"c","data":`+body+`}`, opts...)
		fromTape := render(`{"data":`+body+`,"type":"c"}`, opts...)
		if fromJSON != fromTape {
			t.Errorf("%s: path disagreement\n  json=%s\n  tape=%s", body, fromJSON, fromTape)
		}
	}

	for _, tok := range numRawTokens {
		for _, body := range []string{`{"a":` + tok + `}`, `{"f":` + tok + `}`} {
			compare(t, body)
			compare(t, body, option.WithUseNumber())
		}
	}
	// Ordinary numbers: paths must agree on the value. UseNumber is excluded here
	// for the reason above.
	for _, tok := range numBinaryTokens {
		compare(t, `{"a":`+tok+`}`)
		compare(t, `{"f":`+tok+`}`)
	}
}

// UseNumber cannot be honored uniformly for binary tags. Signed and unsigned
// integer tags retain no source spelling, while applying it only to double tags
// would make the result depend on the producer's tag choice. The tape path
// therefore falls back to float64 unless the value is TagNumRaw.
//
// Same reason vbind rejects a json.Number field on the tape-bind path outright:
// see tapeBindNestedUnsupportedReason.
func TestNumRaw_UseNumberBinaryTagsStillDiffer(t *testing.T) {
	get := func(src string) any {
		var h numPathHost
		if err := Unmarshal([]byte(src), &h, option.WithUseNumber()); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		c, _ := h.Data.(numPathCase)
		return c.A
	}
	// 1e2 is exactly representable, so it takes a binary tag.
	if got := get(`{"type":"c","data":{"a":1e2}}`); got != json.Number("1e2") {
		t.Errorf("JSON path: A = %#v, want json.Number(\"1e2\")", got)
	}
	if got := get(`{"data":{"a":1e2},"type":"c"}`); got != float64(100) {
		t.Errorf("tape path: A = %#v, want float64(100); if this now yields "+
			"json.Number the limit was lifted and the note above is stale", got)
	}
}

// TagNumRaw always carries exact source text, so UseNumber can be honored
// independently of a binary tag choice.
func TestNumRaw_UseNumberOverTape(t *testing.T) {
	for _, tok := range numRawTokens {
		src := `{"data":{"a":` + tok + `},"type":"c"}` // disc last: tape path
		var h numPathHost
		if err := Unmarshal([]byte(src), &h, option.WithUseNumber()); err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		c, ok := h.Data.(numPathCase)
		if !ok {
			t.Errorf("%s: Data = %T", src, h.Data)
			continue
		}
		num, ok := c.A.(json.Number)
		if !ok {
			t.Errorf("%s: A = %T, want json.Number", src, c.A)
			continue
		}
		if string(num) != tok {
			t.Errorf("%s: A = %q, want %q", src, num, tok)
		}
	}
}

// str_arena holds the text, so a document of these numbers must not exhaust it.
// Every byte written is a byte of some token and tokens do not overlap, so the
// total is bounded by srcLen, which both arenas are sized past. A long run is the
// shape that would break an accounting error.
func TestNumRaw_ArenaHoldsManyTokens(t *testing.T) {
	const n = 2000
	var b strings.Builder
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "1234567890123456789012345678%02d", i%100)
	}
	b.WriteByte(']')
	src := b.String()

	v, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := v.String(); got != src {
		t.Errorf("reforward mismatch at length %d", len(src))
	}
}
