package bind

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/decode/option"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// strPtr returns the data pointer of a Go string header.
func strPtr(s string) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&s))
}

// inSpan reports whether p falls inside buf's [0, len) region.
func inSpan(p unsafe.Pointer, buf []byte) bool {
	base := uintptr(unsafe.Pointer(unsafe.SliceData(buf)))
	dp := uintptr(p)
	return dp >= base && dp < base+uintptr(len(buf))
}

// zcDoc exercises every zero-copy producer site: typed strings, json.Number,
// any strings and numbers, map keys, nested pointers, and RawMessage.
type zcDoc struct {
	Clean   string            `json:"clean"`
	Escaped string            `json:"escaped"`
	Uni     string            `json:"uni"`
	Surro   string            `json:"surro"`
	Empty   string            `json:"empty"`
	High    string            `json:"high"`
	Num     json.Number       `json:"num"`
	Quoted  json.Number       `json:"quoted,string"`
	Any     any               `json:"any"`
	AnyStr  any               `json:"anystr"`
	AnyNum  any               `json:"anynum"`
	M       map[string]string `json:"m"`
	Arr     []string          `json:"arr"`
	Ptr     *zcNested         `json:"ptr"`
	Raw     json.RawMessage   `json:"raw"`
}

type zcNested struct {
	S string `json:"s"`
}

// zcDocBytes builds the corpus with byte concatenation so raw high-bit bytes
// stay raw instead of becoming backtick escapes.
func zcDocBytes() []byte {
	doc := []byte(`{"clean":"hello","escaped":"a\nb\tc","uni":"\u00e9\u4e2d","surro":"\ud800x",` +
		`"empty":"","high":"`)
	doc = append(doc, 0xC3, 0xA9, 0xFF)
	doc = append(doc, []byte(`","num":3.14,"quoted":"42",`+
		`"any":{"k":"v"},"anystr":"s","anynum":7,`+
		`"m":{"mk":"mv","esc\u0061ped":"v2"},"arr":["x","y\nz"],`+
		`"ptr":{"s":"deep"},"raw":{"r":[1,2]}}`)...)
	return doc
}

func zcWant() zcDoc {
	return zcDoc{
		Clean:   "hello",
		Escaped: "a\nb\tc",
		Uni:     "é中",
		Surro:   "\uFFFDx",
		Empty:   "",
		High:    "\xC3\xA9\xFF",
		Num:     "3.14",
		Quoted:  "42",
		Any:     map[string]any{"k": "v"},
		AnyStr:  "s",
		AnyNum:  float64(7),
		M:       map[string]string{"mk": "mv", "escaped": "v2"},
		Arr:     []string{"x", "y\nz"},
		Ptr:     &zcNested{S: "deep"},
		Raw:     json.RawMessage(`{"r":[1,2]}`),
	}
}

// TestZeroCopyEquivalence pins byte-for-byte output parity between the
// explicit copying parse and the zero-copy default across every string
// producer, including Unmarshal's delta-rebased aliases over the internal
// padded copy.
func TestZeroCopyEquivalence(t *testing.T) {
	doc := zcDocBytes()
	for _, opts := range [][]UnmarshalOption{nil, {WithUseNumber()}} {
		copyOpts := append(slices.Clone(opts), WithZeroCopy(false))
		var want, got, gotU zcDoc
		if err := Unmarshal(doc, &want, copyOpts...); err != nil {
			t.Fatalf("opts=%v copy: %v", opts, err)
		}
		if err := UnmarshalPadded(Pad(doc), &got, opts...); err != nil {
			t.Fatalf("opts=%v zero-copy default: %v", opts, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("opts=%v mode divergence:\nwant %+v\ngot  %+v", opts, want, got)
		}
		if err := Unmarshal(doc, &gotU, opts...); err != nil {
			t.Fatalf("opts=%v unmarshal default: %v", opts, err)
		}
		if !reflect.DeepEqual(want, gotU) {
			t.Fatalf("opts=%v unmarshal divergence:\nwant %+v\ngot  %+v", opts, want, gotU)
		}
		wantAny, gotAny := zcWant(), got
		if opts != nil {
			wantAny.AnyNum = json.Number("7")
		}
		if !reflect.DeepEqual(wantAny, gotAny) {
			t.Fatalf("opts=%v expected-value divergence:\nwant %+v\ngot  %+v", opts, wantAny, gotAny)
		}
	}
}

// TestZeroCopyAliasBounds asserts which strings alias the padded source:
// escape-free bodies, clean map keys, number tokens, and RawMessage spans do;
// escaped bodies decode through the string arena instead.
func TestZeroCopyAliasBounds(t *testing.T) {
	padded := Pad(zcDocBytes())
	var got zcDoc
	if err := UnmarshalPadded(padded, &got, WithZeroCopy(true)); err != nil {
		t.Fatalf("zero-copy: %v", err)
	}
	aliased := map[string]string{
		"clean":       got.Clean,
		"high":        got.High,
		"num":         string(got.Num),
		"quoted":      string(got.Quoted),
		"anystr":      got.AnyStr.(string),
		"arr[0]":      got.Arr[0],
		"ptr.s":       got.Ptr.S,
		"map key mk":  firstKeyContaining(got.M, "mk"),
		"map val mk":  got.M["mk"],
		"map val esc": got.M["escaped"],
	}
	for name, s := range aliased {
		if s == "" {
			t.Fatalf("%s: empty probe string", name)
		}
		if !inSpan(strPtr(s), padded) {
			t.Errorf("%s: %q does not alias the padded source", name, s)
		}
	}
	decoded := map[string]string{
		"escaped":     got.Escaped,
		"uni":         got.Uni,
		"surro":       got.Surro,
		"arr[1]":      got.Arr[1],
		"map key esc": firstKeyContaining(got.M, "escaped"),
	}
	for name, s := range decoded {
		if inSpan(strPtr(s), padded) {
			t.Errorf("%s: %q unexpectedly aliases the padded source", name, s)
		}
	}
	if !inSpan(unsafe.Pointer(unsafe.SliceData(got.Raw)), padded) {
		t.Errorf("RawMessage %s does not alias the padded source", got.Raw)
	}
	if c := cap(got.Raw); c != len(got.Raw) {
		t.Errorf("RawMessage cap %d exceeds len %d; appends would reach the caller's buffer", c, len(got.Raw))
	}
}

func firstKeyContaining(m map[string]string, sub string) string {
	for k := range m {
		if strings.Contains(k, sub) {
			return k
		}
	}
	return ""
}

// TestZeroCopy24BitBoundary pins the zc scan cap: a body of exactly 0xFFFFFF
// bytes still aliases, one byte more falls back to the copying parse.
func TestZeroCopy24BitBoundary(t *testing.T) {
	for _, tc := range []struct {
		n     int
		alias bool
	}{
		{0xFFFFFF, true},
		{0x1000000, false},
	} {
		body := strings.Repeat("a", tc.n)
		doc := Pad([]byte(`{"s":"` + body + `"}`))
		var got struct {
			S string `json:"s"`
		}
		if err := UnmarshalPadded(doc, &got, WithZeroCopy(true)); err != nil {
			t.Fatalf("n=%#x: %v", tc.n, err)
		}
		if len(got.S) != tc.n || got.S[0] != 'a' || got.S[tc.n-1] != 'a' {
			t.Fatalf("n=%#x: body corrupted", tc.n)
		}
		if alias := inSpan(strPtr(got.S), doc); alias != tc.alias {
			t.Fatalf("n=%#x: alias=%v want %v", tc.n, alias, tc.alias)
		}
	}
}

// TestZeroCopyGates pins the input-model and shape gates: contiguous drives
// alias by default and copy under WithZeroCopy(false); trees carrying
// value.Value or poly fields fall back to the copying parse under the default
// and reject an explicit demand; entries whose input relocates across windows
// or reads a Value doc reject the explicit demand only.
func TestZeroCopyGates(t *testing.T) {
	var plain struct {
		S string `json:"s"`
	}
	if err := Unmarshal(zeroCopyGateInput, &plain); err != nil {
		t.Errorf("Unmarshal plain tree: %v", err)
	}
	if plain.S != "x" || !inSpan(strPtr(plain.S), zeroCopyGateInput) {
		t.Errorf("Unmarshal plain tree: S=%q aliased=%v; the default must alias the caller's data",
			plain.S, inSpan(strPtr(plain.S), zeroCopyGateInput))
	}
	var optOut struct {
		S string `json:"s"`
	}
	if err := Unmarshal(zeroCopyGateInput, &optOut, WithZeroCopy(false)); err != nil {
		t.Errorf("Unmarshal opt-out: %v", err)
	}
	if optOut.S != "x" || inSpan(strPtr(optOut.S), zeroCopyGateInput) {
		t.Errorf("Unmarshal opt-out: S=%q aliased=%v; WithZeroCopy(false) must copy",
			optOut.S, inSpan(strPtr(optOut.S), zeroCopyGateInput))
	}
	p, err := NewParser[struct {
		S string `json:"s"`
	}]()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Unmarshal(zeroCopyGateInput, &plain); err != nil {
		t.Errorf("Parser.Unmarshal plain tree: %v", err)
	}
	if plain.S != "x" || !inSpan(strPtr(plain.S), zeroCopyGateInput) {
		t.Errorf("Parser.Unmarshal plain tree: S=%q; the default must alias the caller's data", plain.S)
	}
	if err = p.UnmarshalFeed(strings.NewReader(`{"s":"x"}`), &plain); err != nil {
		t.Errorf("UnmarshalFeed default: %v", err)
	}
	if plain.S != "x" {
		t.Errorf("UnmarshalFeed default: S=%q", plain.S)
	}
	if err = p.UnmarshalFeed(strings.NewReader(`{"s":"x"}`), &plain, WithZeroCopy(true)); !errors.Is(err, option.ErrZeroCopyUnsupported) {
		t.Errorf("UnmarshalFeed: got %v, want ErrZeroCopyUnsupported", err)
	}
	v, err := dom.Parse([]byte(`{"s":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = UnmarshalValue(v, &plain, WithZeroCopy(true)); !errors.Is(err, option.ErrZeroCopyUnsupported) {
		t.Errorf("UnmarshalValue: got %v, want ErrZeroCopyUnsupported", err)
	}

	var vh zcValueHost
	pv, err := NewParser[zcValueHost]()
	if err != nil {
		t.Fatal(err)
	}
	valueRaw := []byte(`{"v":{"x":1},"s":"a"}`)
	valueDoc := Pad(valueRaw)
	// The default falls back to the copying parse on value.Value trees.
	if err = pv.UnmarshalPadded(valueDoc, &vh); err != nil {
		t.Fatalf("value tree default: %v", err)
	}
	xv := (&vh.V).Get("x")
	if x, ok := xv.Int(); !ok || x != 1 || vh.S != "a" {
		t.Fatalf("value tree default bound wrong: %+v", vh)
	}
	if inSpan(strPtr(vh.S), valueDoc) {
		t.Error("value tree default: S aliases the source; the fallback must copy")
	}
	if err = pv.UnmarshalPadded(valueDoc, &vh, WithZeroCopy(true)); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("value tree: got %v, want ErrZeroCopyTypedTree", err)
	}
	if err = UnmarshalPadded(valueDoc, &vh, WithZeroCopy(true)); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("package value tree: got %v, want ErrZeroCopyTypedTree", err)
	}
	if err = Unmarshal(valueRaw, &vh, WithZeroCopy(true)); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("unmarshal value tree: got %v, want ErrZeroCopyTypedTree", err)
	}

	var ph zcVariantHost
	pp, err := NewParser[zcVariantHost]()
	if err != nil {
		t.Fatal(err)
	}
	polyRaw := []byte(`{"kind":"c1","greet":"hi"}`)
	polyDoc := Pad(polyRaw)
	if err = pp.UnmarshalPadded(polyDoc, &ph); err != nil {
		t.Fatalf("poly tree default: %v", err)
	}
	if c, ok := ph.Data.(zcVariantCase); !ok || ph.Kind != "c1" || c.Greet != "hi" {
		t.Fatalf("poly tree default bound wrong: %+v", ph)
	}
	if inSpan(strPtr(ph.Kind), polyDoc) {
		t.Error("poly tree default: Kind aliases the source; the fallback must copy")
	}
	if err = pp.UnmarshalPadded(polyDoc, &ph, WithZeroCopy(true)); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("poly tree: got %v, want ErrZeroCopyTypedTree", err)
	}
	if err = Unmarshal(polyRaw, &ph, WithZeroCopy(true)); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("unmarshal poly tree: got %v, want ErrZeroCopyTypedTree", err)
	}
}

// zeroCopyGateInput is the caller-owned backing the default-alias assertions
// probe; package-level so the compiler cannot stack-allocate per-call copies
// that would defeat inSpan.
var zeroCopyGateInput = []byte(`{"s":"x"}`)

type zcValueHost struct {
	V value.Value `json:"v"`
	S string      `json:"s"`
}

type zcVariantHost struct {
	Kind string `json:"kind"`
	Data any    `json:",embed" vjson:"variant=kind"`
}

type zcVariantCase struct {
	Greet string `json:"greet"`
}

func init() {
	vbind.DefineVariantCases[zcVariantHost, struct {
		_ zcVariantCase `case:"c1"`
	}]()
}

// TestZeroCopyParserReuse decodes twice through one Parser with distinct
// padded buffers: clean strings keep pointing at their own caller buffer and
// escaped strings survive the second parse's arena writes.
func TestZeroCopyParserReuse(t *testing.T) {
	p, err := NewParser[zcDoc]()
	if err != nil {
		t.Fatal(err)
	}
	var first, second zcDoc
	if err := p.UnmarshalPadded(Pad([]byte(`{"clean":"one","escaped":"a\nb"}`)), &first, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if err := p.UnmarshalPadded(Pad([]byte(`{"clean":"two","escaped":"c\nd"}`)), &second, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if first.Clean != "one" || first.Escaped != "a\nb" {
		t.Fatalf("first parse corrupted by reuse: %+v", first)
	}
	if second.Clean != "two" || second.Escaped != "c\nd" {
		t.Fatalf("second parse: %+v", second)
	}
}

// TestZeroCopySubParseAlias drives unmarshalRawInto directly: the sub-parse
// owns its aliasSrc for the input span, so its strings alias that span's
// backing and survive the next sub-parse's pad-buffer reuse.
func TestZeroCopySubParseAlias(t *testing.T) {
	p, err := NewParser[zcNested]()
	if err != nil {
		t.Fatal(err)
	}
	p.optFlags = ndec.BindOptZeroCopyStr
	var first, second zcNested
	rt := reflect.TypeFor[zcNested]()
	in1 := []byte(`{"s":"first"}`)
	in2 := []byte(`{"s":"third"}`)
	if err := unmarshalRawInto(p, in1, rt, unsafe.Pointer(&first)); err != nil {
		t.Fatal(err)
	}
	// Equal input length keeps the sub-parser's pad buffer reused rather than
	// regrown, so an alias left pointing at the pad buffer would corrupt first.S.
	if err := unmarshalRawInto(p, in2, rt, unsafe.Pointer(&second)); err != nil {
		t.Fatal(err)
	}
	if first.S != "first" {
		t.Fatalf("first sub-parse corrupted by pad-buffer reuse: %q", first.S)
	}
	if second.S != "third" {
		t.Fatalf("second sub-parse: %q", second.S)
	}
	if !inSpan(strPtr(first.S), in1) {
		t.Errorf("first.S %q does not alias the first input's backing", first.S)
	}
	if !inSpan(strPtr(second.S), in2) {
		t.Errorf("second.S %q does not alias the second input's backing", second.S)
	}
}

// TestZeroCopyIfaceSubDecode covers the end-to-end interface sub-decode path
// under zero-copy over padded input: pointee recovery re-parses the value
// span, whose strings alias the caller's padded buffer.
type zcIfaceTarget struct {
	S string `json:"s"`
}

func (t *zcIfaceTarget) ZCMark() {}

type zcIface interface{ ZCMark() }

func TestZeroCopyIfaceSubDecode(t *testing.T) {
	type host struct {
		Sub zcIface `json:"sub"`
	}
	p, err := NewParser[host]()
	if err != nil {
		t.Fatal(err)
	}
	var h1, h2 host
	h1.Sub = &zcIfaceTarget{}
	h2.Sub = &zcIfaceTarget{}
	if err := p.UnmarshalPadded(Pad([]byte(`{"sub":{"s":"first"}}`)), &h1, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	// Equal document lengths keep the pooled sub-parser's pad buffer reused,
	// so a propagated zero-copy bit would corrupt the first sub-decode.
	if err := p.UnmarshalPadded(Pad([]byte(`{"sub":{"s":"third"}}`)), &h2, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if s := h1.Sub.(*zcIfaceTarget).S; s != "first" {
		t.Fatalf("first sub-decode corrupted: %q", s)
	}
	if s := h2.Sub.(*zcIfaceTarget).S; s != "third" {
		t.Fatalf("second sub-decode: %q", s)
	}
}

// TestZeroCopyStreamHost smoke-tests stream activation over contiguous padded
// input under zero-copy: element strings pin the caller's buffer.
func TestZeroCopyStreamHost(t *testing.T) {
	type host struct {
		Items stream.Stream[zcNested] `json:"items"`
		Name  string                  `json:"name"`
	}
	var ids []string
	var h host
	h.Items.OnRead(func(s stream.Scope[zcNested]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			ids = append(ids, it.Target().S)
		}
		return nil
	})
	padded := Pad([]byte(`{"items":[{"s":"a"},{"s":"b"}],"name":"n"}`))
	if err := UnmarshalPadded(padded, &h, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids=%v", ids)
	}
	if h.Name != "n" {
		t.Fatalf("name=%q", h.Name)
	}
	for i, id := range ids {
		if !inSpan(strPtr(id), padded) {
			t.Errorf("ids[%d]=%q does not alias the padded source", i, id)
		}
	}
}

// TestZeroCopyUnmarshalAliasBounds asserts the delta rebase: Unmarshal scans
// an internal padded copy, and every aliased span must land in the caller's
// original data buffer, never the pooled copy.
func TestZeroCopyUnmarshalAliasBounds(t *testing.T) {
	data := zcDocBytes()
	var got zcDoc
	if err := Unmarshal(data, &got, WithZeroCopy(true)); err != nil {
		t.Fatalf("zero-copy: %v", err)
	}
	aliased := map[string]string{
		"clean":       got.Clean,
		"high":        got.High,
		"num":         string(got.Num),
		"quoted":      string(got.Quoted),
		"anystr":      got.AnyStr.(string),
		"arr[0]":      got.Arr[0],
		"ptr.s":       got.Ptr.S,
		"map key mk":  firstKeyContaining(got.M, "mk"),
		"map val mk":  got.M["mk"],
		"map val esc": got.M["escaped"],
	}
	for name, s := range aliased {
		if s == "" {
			t.Fatalf("%s: empty probe string", name)
		}
		if !inSpan(strPtr(s), data) {
			t.Errorf("%s: %q does not alias the caller's data buffer", name, s)
		}
	}
	if !inSpan(unsafe.Pointer(unsafe.SliceData(got.Raw)), data) {
		t.Errorf("RawMessage %s does not alias the caller's data buffer", got.Raw)
	}
	if c := cap(got.Raw); c != len(got.Raw) {
		t.Errorf("RawMessage cap %d exceeds len %d; appends would reach the caller's buffer", c, len(got.Raw))
	}
}

// zcRecorder retains the borrowed span the deferred drain hands to
// json.Unmarshaler, pinning which backing the record was sliced from.
type zcRecorder struct {
	Span []byte
}

func (r *zcRecorder) UnmarshalJSON(b []byte) error {
	r.Span = b
	return nil
}

type zcChurnDoc struct {
	S   string          `json:"s"`
	U   zcRecorder      `json:"u"`
	Raw json.RawMessage `json:"raw"`
}

// TestZeroCopyUnmarshalPadChurn decodes through one Parser's reused pad
// buffer: published strings, RawMessage spans, and Unmarshaler spans alias
// each call's caller-owned data, so later parses cannot corrupt earlier
// results. The second document keeps the pad buffer reused in place and the
// third forces a regrow.
func TestZeroCopyUnmarshalPadChurn(t *testing.T) {
	p, err := NewParser[zcChurnDoc]()
	if err != nil {
		t.Fatal(err)
	}
	doc1 := []byte(`{"s":"first","u":{"a":1},"raw":{"z":2}}`)
	doc2 := []byte(`{"s":"secnd","u":{"a":9},"raw":{"z":8}}`)
	doc3 := []byte(`{"s":"third-with-padding-to-regrow","u":{"a":7},"raw":{"z":9}}`)
	var one, two, three zcChurnDoc
	for i, d := range [][]byte{doc1, doc2, doc3} {
		switch i {
		case 0:
			err = p.Unmarshal(d, &one, WithZeroCopy(true))
		case 1:
			err = p.Unmarshal(d, &two, WithZeroCopy(true))
		case 2:
			err = p.Unmarshal(d, &three, WithZeroCopy(true))
		}
		if err != nil {
			t.Fatalf("parse %d: %v", i+1, err)
		}
	}
	if one.S != "first" || string(one.U.Span) != `{"a":1}` || string(one.Raw) != `{"z":2}` {
		t.Fatalf("first parse corrupted by pad churn: %q %s %s", one.S, one.U.Span, one.Raw)
	}
	if two.S != "secnd" || string(two.U.Span) != `{"a":9}` || string(two.Raw) != `{"z":8}` {
		t.Fatalf("second parse: %q %s %s", two.S, two.U.Span, two.Raw)
	}
	if three.S != "third-with-padding-to-regrow" {
		t.Fatalf("third parse: %q", three.S)
	}
	if !inSpan(strPtr(one.S), doc1) || !inSpan(unsafe.Pointer(unsafe.SliceData(one.Raw)), doc1) ||
		!inSpan(unsafe.Pointer(unsafe.SliceData(one.U.Span)), doc1) {
		t.Error("first parse spans do not alias doc1's backing")
	}
	if !inSpan(strPtr(two.S), doc2) || !inSpan(strPtr(three.S), doc3) {
		t.Error("later parses do not alias their own backing")
	}
}

// strUsed reports the string-arena bytes the parser's last call committed.
func strUsed(p *Parser) uint64 {
	m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
	return m.Core.StrUsed
}

// TestZeroCopyQuotedBorrow pins `,string` behavior under the body borrow: an
// escape-free outer body feeds the quoted scalar walk straight from the
// source span, an escaped body still decodes through the arena, and a
// string-target content that is not a quoted JSON string fails identically
// in both modes.
func TestZeroCopyQuotedBorrow(t *testing.T) {
	type doc struct {
		Q int32  `json:"q,string"`
		S string `json:"s,string"`
	}
	pz, err := NewParser[doc]()
	if err != nil {
		t.Fatal(err)
	}
	pc, err := NewParser[doc]()
	if err != nil {
		t.Fatal(err)
	}
	clean := []byte(`{"q":"42","s":"\"hi\""}`)
	esc := []byte(`{"q":"4\u0032","s":"\"hi\""}`)
	bad := []byte(`{"q":"42","s":"hi"}`)
	var zc, cp doc
	if err := pz.Unmarshal(clean, &zc, WithZeroCopy(true)); err != nil {
		t.Fatalf("clean zero-copy: %v", err)
	}
	if err := pc.Unmarshal(clean, &cp, WithZeroCopy(false)); err != nil {
		t.Fatalf("clean copy: %v", err)
	}
	if zc.Q != 42 || zc.S != "hi" || cp.Q != 42 || cp.S != "hi" {
		t.Fatalf("values: zero-copy %+v copy %+v", zc, cp)
	}
	var zcEsc doc
	if err := pz.Unmarshal(esc, &zcEsc, WithZeroCopy(true)); err != nil {
		t.Fatalf("escaped zero-copy: %v", err)
	}
	if zcEsc.Q != 42 || zcEsc.S != "hi" {
		t.Fatalf("escaped values: %+v", zcEsc)
	}
	if u := strUsed(pz); u == 0 {
		t.Error("escaped zero-copy committed no arena bytes; the escaped body must decode through str_arena")
	}
	errZC := pz.Unmarshal(bad, &zc, WithZeroCopy(true))
	errCP := pc.Unmarshal(bad, &cp, WithZeroCopy(false))
	if errZC == nil || errCP == nil {
		t.Fatalf("unquoted string content accepted: zero-copy err=%v copy err=%v", errZC, errCP)
	}
}

// zcTextRec retains the byte slice the deferred drain hands to
// encoding.TextUnmarshaler, pinning which backing the record sliced from.
type zcTextRec struct {
	Span []byte
}

func (r *zcTextRec) UnmarshalText(b []byte) error {
	r.Span = b
	return nil
}

// TestZeroCopyTextBorrow pins the TextUnmarshaler and base64 record borrows:
// an escape-free body reaches the hook as a source span aliasing the caller's
// data, while an escaped body stays arena-backed. The base64 record borrows
// the same way, visible through the untouched arena.
func TestZeroCopyTextBorrow(t *testing.T) {
	type doc struct {
		T   zcTextRec `json:"t"`
		Esc zcTextRec `json:"esc"`
		B   []byte    `json:"b"`
	}
	p, err := NewParser[doc]()
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"t":"hello","esc":"a\nb","b":"aGVsbG8="}`)
	var got doc
	if err := p.Unmarshal(data, &got, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if string(got.T.Span) != "hello" {
		t.Fatalf("text span: %q", got.T.Span)
	}
	if !inSpan(unsafe.Pointer(unsafe.SliceData(got.T.Span)), data) {
		t.Errorf("text span %q does not alias the caller's data buffer", got.T.Span)
	}
	if string(got.Esc.Span) != "a\nb" {
		t.Fatalf("escaped text span: %q", got.Esc.Span)
	}
	if inSpan(unsafe.Pointer(unsafe.SliceData(got.Esc.Span)), data) {
		t.Errorf("escaped text span %q unexpectedly aliases the caller's data buffer", got.Esc.Span)
	}
	if string(got.B) != "hello" {
		t.Fatalf("base64 value: %q", got.B)
	}
	if u := strUsed(p); u == 0 {
		t.Error("the escaped body must still intern into the arena")
	}
	// A second same-length parse reuses the pad buffer, so a span left
	// pointing at it would corrupt the first result.
	data2 := []byte(`{"t":"world","esc":"c\nd","b":"d29ybGQ="}`)
	var got2 doc
	if err := p.Unmarshal(data2, &got2, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if string(got.T.Span) != "hello" {
		t.Fatalf("first text span corrupted by pad churn: %q", got.T.Span)
	}
	if string(got2.T.Span) != "world" || string(got2.B) != "world" {
		t.Fatalf("second parse: %q %q", got2.T.Span, got2.B)
	}
}

// TestZeroCopyUnmarshalIfaceSubDecode covers the interface sub-decode over
// Unmarshal input: the rebased raw span feeds the sub-parser, whose strings
// alias the caller's data through the propagated zero-copy bit.
func TestZeroCopyUnmarshalIfaceSubDecode(t *testing.T) {
	type host struct {
		Sub zcIface `json:"sub"`
	}
	p, err := NewParser[host]()
	if err != nil {
		t.Fatal(err)
	}
	var h1, h2 host
	h1.Sub = &zcIfaceTarget{}
	h2.Sub = &zcIfaceTarget{}
	doc1 := []byte(`{"sub":{"s":"first"}}`)
	doc2 := []byte(`{"sub":{"s":"third"}}`)
	if err := p.Unmarshal(doc1, &h1, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if err := p.Unmarshal(doc2, &h2, WithZeroCopy(true)); err != nil {
		t.Fatal(err)
	}
	if s := h1.Sub.(*zcIfaceTarget).S; s != "first" {
		t.Fatalf("first sub-decode corrupted: %q", s)
	} else if !inSpan(strPtr(s), doc1) {
		t.Errorf("first sub-decode %q does not alias doc1's backing", s)
	}
	if s := h2.Sub.(*zcIfaceTarget).S; s != "third" {
		t.Fatalf("second sub-decode: %q", s)
	} else if !inSpan(strPtr(s), doc2) {
		t.Errorf("second sub-decode %q does not alias doc2's backing", s)
	}
}
