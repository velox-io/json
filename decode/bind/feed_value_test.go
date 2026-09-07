package bind

import (
	"strings"
	"testing"

	"github.com/velox-io/json/value"
)

// feedValueDocs covers the shapes the vd submachine emits: scalar roots,
// nesting, empty containers, escapes, unicode, and a 21-digit number whose
// exact text must survive as a raw tape entry.
var feedValueDocs = []string{
	"null",
	"true",
	"false",
	"123",
	"-1.5e-3",
	"12345678901234567890123",
	`"text"`,
	`"日本語"`,
	`"\"quoted\""`,
	`"back\\slash\ttab"`,
	"{}",
	"[]",
	"[1,2,3]",
	`{"a":1,"b":"two","c":null}`,
	`{"nested":{"deep":{"deeper":[{"x":[[]]},"y"]}}}`,
	`{"objs":[{},{ "k":[] },{"kk":[{},{}]}],"arr":[[[]],[[1]]]}`,
	`{"unicode":"émoji 🎌 surrogate 𐍈","esc":"\u0041\u00e9"}`,
}

var feedValueInvalidDocs = []string{
	"{",
	"[",
	`{"a":1`,
	"[1,2",
	`{"a":`,
	`{"a":1,}`,
	"[1,]",
	`{"a" 1}`,
	`{"a":"unclosed`,
	"tru",
	"[1 true]",
	`{"a":1} extra`,
}

func TestFeedValueRootSplitParity(t *testing.T) {
	for _, doc := range feedValueDocs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[value.Value](t, data)
		if wantErr != nil {
			t.Fatalf("contiguous parse of %q failed: %v", doc, wantErr)
		}
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[value.Value](t, data, chunk)
			if err != nil {
				t.Fatalf("feed chunk=%d doc=%q: %v", chunk, doc, err)
			}
			if gs, ws := got.String(), want.String(); gs != ws {
				t.Fatalf("feed chunk=%d doc=%q: got %s want %s\ntape:\n%s\nwant tape:\n%s",
					chunk, doc, gs, ws, got.TapeDiagram(), want.TapeDiagram())
			}
		}
	}
}

func TestFeedValueRootSplitParityInvalid(t *testing.T) {
	for _, doc := range feedValueInvalidDocs {
		data := []byte(doc)
		_, wantErr := feedUnmarshal[value.Value](t, data)
		if wantErr == nil {
			t.Fatalf("contiguous parse of %q unexpectedly succeeded", doc)
		}
		wantKind, wantOff := feedErrKind(t, wantErr)
		for _, chunk := range feedChunkSizes {
			_, err := feedRun[value.Value](t, data, chunk)
			if err == nil {
				t.Fatalf("feed chunk=%d doc=%q: unexpectedly succeeded", chunk, doc)
			}
			kind, off := feedErrKind(t, err)
			if kind != wantKind {
				t.Fatalf("feed chunk=%d doc=%q: error kind %q, contiguous %q (%v vs %v)", chunk, doc, kind, wantKind, err, wantErr)
			}
			if off != wantOff {
				t.Fatalf("feed chunk=%d doc=%q: error offset %d, contiguous %d", chunk, doc, off, wantOff)
			}
		}
	}
}

type feedValueHost struct {
	V    value.Value   `json:"v"`
	W    *value.Value  `json:"w"`
	List []value.Value `json:"list"`
	Name string        `json:"name"`
}

func TestFeedValueFieldSplitParity(t *testing.T) {
	docs := []string{
		`{"v":{"x":[1,2]},"name":"n"}`,
		`{"v":123,"name":"scalar"}`,
		`{"v":null,"name":"null value"}`,
		`{"v":[],"w":{},"name":"empties"}`,
		`{"list":[1,"two",null,[3],{"f":4}],"name":"mixed list"}`,
		`{"v":{"deep":{"n":[[{"k":"v"}]]}},"name":"deep"}`,
	}
	for _, doc := range docs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[feedValueHost](t, data)
		if wantErr != nil {
			t.Fatalf("contiguous parse of %q failed: %v", doc, wantErr)
		}
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[feedValueHost](t, data, chunk)
			if err != nil {
				t.Fatalf("feed chunk=%d doc=%q: %v", chunk, doc, err)
			}
			if got.Name != want.Name {
				t.Fatalf("chunk=%d doc=%q: name %q want %q", chunk, doc, got.Name, want.Name)
			}
			if gs, ws := got.V.String(), want.V.String(); gs != ws {
				t.Fatalf("chunk=%d doc=%q: V got %s want %s", chunk, doc, gs, ws)
			}
			if (got.W == nil) != (want.W == nil) {
				t.Fatalf("chunk=%d doc=%q: W nil-ness differs", chunk, doc)
			}
			if len(got.List) != len(want.List) {
				t.Fatalf("chunk=%d doc=%q: list len %d want %d", chunk, doc, len(got.List), len(want.List))
			}
			for i := range got.List {
				if gs, ws := got.List[i].String(), want.List[i].String(); gs != ws {
					t.Fatalf("chunk=%d doc=%q: list[%d] got %s want %s", chunk, doc, i, gs, ws)
				}
			}
		}
	}
}

// TestFeedValueArenaGrowth drives many windows and multiple arena growths past
// the initial window size, then verifies the published Values against the
// contiguous parse. Values completed before a growth must keep their bytes and
// tape coordinates valid in the final backing.
func TestFeedValueArenaGrowth(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"rows":[`)
	for i := 0; i < 400; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"id":1234567890123456789012,"text":"row number 日本語 🎌 with escapes \"q\"","tags":["a","bb","ccc"]}`)
	}
	sb.WriteString(`],"v":{"count":400,"big":123456789012345678901234567890}}`)
	data := []byte(sb.String())

	want, wantErr := feedUnmarshal[feedValueHost2](t, data)
	if wantErr != nil {
		t.Fatalf("contiguous parse failed: %v", wantErr)
	}
	// chunk=1 crosses every window edge; the document is far larger than the
	// initial window, so both arenas grow several times mid-parse.
	for _, chunk := range []int{1, 7, 64, 4096} {
		got, err := feedRun[feedValueHost2](t, data, chunk)
		if err != nil {
			t.Fatalf("feed chunk=%d: %v", chunk, err)
		}
		if gs, ws := got.V.String(), want.V.String(); gs != ws {
			t.Fatalf("chunk=%d: V got %s want %s", chunk, gs, ws)
		}
		if len(got.Rows) != len(want.Rows) {
			t.Fatalf("chunk=%d: rows len %d want %d", chunk, len(got.Rows), len(want.Rows))
		}
		for i := range got.Rows {
			if gs, ws := got.Rows[i].String(), want.Rows[i].String(); gs != ws {
				t.Fatalf("chunk=%d: rows[%d] got %s want %s", chunk, i, gs, ws)
			}
		}
	}
}

type feedValueHost2 struct {
	Rows []value.Value `json:"rows"`
	V    value.Value   `json:"v"`
}

// TestFeedValueDenseBudget drives documents whose tape-word density per source
// byte is maximal (bracket ladders and single-digit pairs reach one word per
// byte), so the per-window reserve of two words per byte plus slack is the only
// bound between the vd bump writes and the arena edge.
func TestFeedValueDenseBudget(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 4000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("[[1],[[2]]]")
	}
	b.WriteByte(']')
	data := []byte(b.String())

	want, wantErr := feedUnmarshal[value.Value](t, data)
	if wantErr != nil {
		t.Fatalf("contiguous parse failed: %v", wantErr)
	}
	for _, chunk := range []int{1, 64, 4096} {
		p, err := NewParser[value.Value]()
		if err != nil {
			t.Fatal(err)
		}
		var got value.Value
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &got); err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if got.String() != want.String() {
			t.Fatalf("chunk=%d: output mismatch", chunk)
		}
		m := machineOf(p)
		if int(m.Alloc.TapeUsed) > int(m.Alloc.TapeArenaCap) {
			t.Fatalf("chunk=%d: tape used %d exceeds arena cap %d", chunk, m.Alloc.TapeUsed, m.Alloc.TapeArenaCap)
		}
		if int(m.Core.StrUsed) > int(m.Alloc.StrArenaCap) {
			t.Fatalf("chunk=%d: str used %d exceeds arena cap %d", chunk, m.Core.StrUsed, m.Alloc.StrArenaCap)
		}
	}
}

// TestFeedValueNestedDocAliasing verifies that a Value produced by the feed
// keeps working after the parser is reused for another parse: the published
// Doc views must not be overwritten by later traffic in the pooled arenas.
func TestFeedValueNestedDocAliasing(t *testing.T) {
	data := []byte(`{"v":{"x":[1,2,{"deep":"yes"}]}}`)
	p, err := NewParser[feedValueHost]()
	if err != nil {
		t.Fatal(err)
	}
	var first feedValueHost
	if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: 3}, &first); err != nil {
		t.Fatal(err)
	}
	want := first.V.String()

	// A second, larger parse forces arena growth over the first Doc's span.
	var sb strings.Builder
	sb.WriteString(`{"v":{`)
	for i := 0; i < 500; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`"k` + itoa(i) + `":"value number ` + itoa(i) + `"`)
	}
	sb.WriteString(`}}`)
	var second feedValueHost
	if err := p.UnmarshalFeed(&chunkReader{data: []byte(sb.String()), chunk: 5}, &second); err != nil {
		t.Fatal(err)
	}

	if got := first.V.String(); got != want {
		t.Fatalf("first Value changed after reuse: got %s want %s", got, want)
	}
	deep := first.V.Get("x")
	elem := deep.Index(2)
	if s, ok := elem.GetString("deep"); !ok || s != "yes" {
		t.Fatalf("deep access after reuse: got (%q, %v), want (%q, true)", s, ok, "yes")
	}
}
