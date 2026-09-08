package bind

import (
	"encoding/json"
	"errors"
	"reflect"
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

// TestZeroCopyEquivalence pins byte-for-byte output parity between copy and
// zero-copy modes across every string producer.
func TestZeroCopyEquivalence(t *testing.T) {
	doc := zcDocBytes()
	for _, opts := range [][]UnmarshalOption{nil, {WithUseNumber()}} {
		var want, got zcDoc
		if err := Unmarshal(doc, &want, opts...); err != nil {
			t.Fatalf("opts=%v copy: %v", opts, err)
		}
		if err := UnmarshalPadded(Pad(doc), &got, append(opts, WithZeroCopy())...); err != nil {
			t.Fatalf("opts=%v zero-copy: %v", opts, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("opts=%v mode divergence:\nwant %+v\ngot  %+v", opts, want, got)
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
	if err := UnmarshalPadded(padded, &got, WithZeroCopy()); err != nil {
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
		if err := UnmarshalPadded(doc, &got, WithZeroCopy()); err != nil {
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

// TestZeroCopyGates pins the input-model and shape gates: every entry whose
// input is copied or relocated rejects WithZeroCopy, and padded entry points
// reject trees carrying value.Value or poly fields.
func TestZeroCopyGates(t *testing.T) {
	var plain struct {
		S string `json:"s"`
	}
	if err := Unmarshal([]byte(`{"s":"x"}`), &plain, WithZeroCopy()); !errors.Is(err, option.ErrZeroCopyNeedsPadded) {
		t.Errorf("Unmarshal: got %v, want ErrZeroCopyNeedsPadded", err)
	}
	p, err := NewParser[struct {
		S string `json:"s"`
	}]()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Unmarshal([]byte(`{"s":"x"}`), &plain, WithZeroCopy()); !errors.Is(err, option.ErrZeroCopyNeedsPadded) {
		t.Errorf("Parser.Unmarshal: got %v, want ErrZeroCopyNeedsPadded", err)
	}
	if err = p.UnmarshalFeed(strings.NewReader(`{"s":"x"}`), &plain, WithZeroCopy()); !errors.Is(err, option.ErrZeroCopyNeedsPadded) {
		t.Errorf("UnmarshalFeed: got %v, want ErrZeroCopyNeedsPadded", err)
	}
	v, err := dom.Parse([]byte(`{"s":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = UnmarshalValue(v, &plain, WithZeroCopy()); !errors.Is(err, option.ErrZeroCopyNeedsPadded) {
		t.Errorf("UnmarshalValue: got %v, want ErrZeroCopyNeedsPadded", err)
	}

	var vh zcValueHost
	pv, err := NewParser[zcValueHost]()
	if err != nil {
		t.Fatal(err)
	}
	valueDoc := Pad([]byte(`{"v":{"x":1},"s":"a"}`))
	if err = pv.UnmarshalPadded(valueDoc, &vh); err != nil {
		t.Fatalf("value tree without zero-copy: %v", err)
	}
	if err = pv.UnmarshalPadded(valueDoc, &vh, WithZeroCopy()); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("value tree: got %v, want ErrZeroCopyTypedTree", err)
	}
	if err = UnmarshalPadded(valueDoc, &vh, WithZeroCopy()); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("package value tree: got %v, want ErrZeroCopyTypedTree", err)
	}

	var ph zcVariantHost
	pp, err := NewParser[zcVariantHost]()
	if err != nil {
		t.Fatal(err)
	}
	polyDoc := Pad([]byte(`{"kind":"c1","data":{"greet":"hi"}}`))
	if err = pp.UnmarshalPadded(polyDoc, &ph); err != nil {
		t.Fatalf("poly tree without zero-copy: %v", err)
	}
	if err = pp.UnmarshalPadded(polyDoc, &ph, WithZeroCopy()); !errors.Is(err, ErrZeroCopyTypedTree) {
		t.Errorf("poly tree: got %v, want ErrZeroCopyTypedTree", err)
	}
}

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
	if err := p.UnmarshalPadded(Pad([]byte(`{"clean":"one","escaped":"a\nb"}`)), &first, WithZeroCopy()); err != nil {
		t.Fatal(err)
	}
	if err := p.UnmarshalPadded(Pad([]byte(`{"clean":"two","escaped":"c\nd"}`)), &second, WithZeroCopy()); err != nil {
		t.Fatal(err)
	}
	if first.Clean != "one" || first.Escaped != "a\nb" {
		t.Fatalf("first parse corrupted by reuse: %+v", first)
	}
	if second.Clean != "two" || second.Escaped != "c\nd" {
		t.Fatalf("second parse: %+v", second)
	}
}

// TestZeroCopySubParseMask drives unmarshalRawInto directly: the sub-parse
// copies its input through a reusable pad buffer, so the zero-copy bit must
// not propagate and the first result must survive the second sub-parse.
func TestZeroCopySubParseMask(t *testing.T) {
	p, err := NewParser[zcNested]()
	if err != nil {
		t.Fatal(err)
	}
	p.optFlags = ndec.BindOptZeroCopyStr
	var first, second zcNested
	rt := reflect.TypeFor[zcNested]()
	if err := unmarshalRawInto(p, []byte(`{"s":"first"}`), rt, unsafe.Pointer(&first)); err != nil {
		t.Fatal(err)
	}
	// Equal input length keeps the sub-parser's pad buffer reused rather than
	// regrown, so a propagated zero-copy bit would corrupt first.S.
	if err := unmarshalRawInto(p, []byte(`{"s":"third"}`), rt, unsafe.Pointer(&second)); err != nil {
		t.Fatal(err)
	}
	if first.S != "first" {
		t.Fatalf("first sub-parse corrupted by pad-buffer reuse: %q", first.S)
	}
	if second.S != "third" {
		t.Fatalf("second sub-parse: %q", second.S)
	}
}

// TestZeroCopyIfaceSubDecode covers the end-to-end interface sub-decode path
// under zero-copy: pointee recovery re-parses the value span with the
// zero-copy bit masked off.
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
	if err := p.UnmarshalPadded(Pad([]byte(`{"sub":{"s":"first"}}`)), &h1, WithZeroCopy()); err != nil {
		t.Fatal(err)
	}
	// Equal document lengths keep the pooled sub-parser's pad buffer reused,
	// so a propagated zero-copy bit would corrupt the first sub-decode.
	if err := p.UnmarshalPadded(Pad([]byte(`{"sub":{"s":"third"}}`)), &h2, WithZeroCopy()); err != nil {
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
	if err := UnmarshalPadded(padded, &h, WithZeroCopy()); err != nil {
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
