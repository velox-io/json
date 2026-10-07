package bind

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/stream"
)

// chunkReader releases data in fixed-size reads so the feed driver sees one
// window per chunk.
type chunkReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data)-r.pos {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	if r.pos >= len(r.data) {
		return n, io.EOF
	}
	return n, nil
}

type feedInner struct {
	A int     `json:"a"`
	B string  `json:"b"`
	C float64 `json:"c"`
}

type feedDoc struct {
	I      int               `json:"i"`
	F      float64           `json:"f"`
	S      string            `json:"s"`
	Q      []int             `json:"q"`
	SS     []string          `json:"ss"`
	M      map[string]int    `json:"m"`
	P      *int              `json:"p"`
	A      any               `json:"a"`
	Nested []feedInner       `json:"nested"`
	Arr    [3]int            `json:"arr"`
	O      map[string]string `json:"o"`
}

var feedValidDocs = []string{
	"{}",
	`{"i":1}`,
	`{"i":1,"f":2.5,"s":"text","q":[1,2,3],"ss":["x","y"],"m":{"k":1},"p":null,"a":true,"nested":[{"a":1,"b":"z","c":0.5}],"arr":[7,8,9],"o":{"k":"v"}}`,
	`{"i":1,"unknown_field":{"deep":[1,{"x":"y"},2.5,null]}}`,
	`{"nested":[{"a":1},{"a":2,"b":"two"},{"a":3,"c":3.5}]}`,
	`{"a":{"deep":{"deeper":{"deepest":[1,[2,[3]]]}}}}`,
	`{"ss":["日本語","émoji","\"quoted\"","back\\slash"]}`,
	`{"q":[],"ss":[],"m":{},"o":{},"arr":[],"nested":[{},{}],"a":[[],{}]}`,
	`{"arr":[1,2,3,4,[5,{"x":6}],"seven",{"a":[8]}],"i":9}`,
}

var feedInvalidDocs = []string{
	`{"i":1`,
	`{"i":1,`,
	`{"i":1,}`,
	`{"i":1,} 5`,
	"[1,2",
	`{"s":"abc`,
	`{"m":{"k":1`,
	"[true",
	"{",
	`{"i":1} trailing`,
	`{"i":1}[]`,
	`[1] garbage`,
	`{"i":"str"`,
	`{"q":[1,2`,
	`{"q":[1,"str",null,3.5,true,{"k":1},[2]]}`,
	`{"m":{"":""}}`,
	`{"s":[1]}`,
	`{"arr":{}}`,
	`{"nested":[1]}`,
	`{"p":[1]}`,
	`{"o":{"k":[1]}}`,
	`{"m":{"k":{}}}`,
	`{"q":[1,]}`,
	`{"q":[,]}`,
	`{"m":{"k":1,}}`,
	`{"m":{,}}`,
	`{"arr":[1,2,3,4,]}`,
	`{"unknown_field":[1,]}`,
	`{"unknown_field":{"a":1,}}`,
	`{"unknown_field":[1,,2]}`,
	`{"arr":[1,2,3,[4,]]}`,
}

var feedChunkSizes = []int{1, 2, 3, 7, 31, 32, 63, 64, 65, 4096}

func feedUnmarshal[T any](t *testing.T, data []byte) (T, error) {
	t.Helper()
	p, err := NewParser[T]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var out T
	err = p.Unmarshal(data, &out)
	return out, err
}

func feedRun[T any](t *testing.T, data []byte, chunk int) (T, error) {
	t.Helper()
	p, err := NewParser[T]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var out T
	err = p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &out)
	return out, err
}

// feedErrKind flattens an error into a comparable (kind, offset) pair.
func feedErrKind(t *testing.T, err error) (string, int64) {
	t.Helper()
	if err == nil {
		return "", -1
	}
	switch e := err.(type) {
	case *jerr.SyntaxError:
		return "syntax", e.Offset
	case *UnmarshalTypeError:
		return "type", e.Offset
	default:
		return err.Error(), -2
	}
}

func TestFeedSplitParityValid(t *testing.T) {
	for _, doc := range feedValidDocs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[feedDoc](t, data)
		if wantErr != nil {
			t.Fatalf("contiguous parse of %q failed: %v", doc, wantErr)
		}
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[feedDoc](t, data, chunk)
			if err != nil {
				t.Fatalf("feed chunk=%d doc=%q: %v", chunk, doc, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("feed chunk=%d doc=%q: got %+v want %+v", chunk, doc, got, want)
			}
		}
	}
}

func TestFeedSplitParityInvalid(t *testing.T) {
	for _, doc := range feedInvalidDocs {
		data := []byte(doc)
		_, wantErr := feedUnmarshal[feedDoc](t, data)
		if wantErr == nil {
			t.Fatalf("contiguous parse of %q unexpectedly succeeded", doc)
		}
		wantKind, wantOff := feedErrKind(t, wantErr)
		for _, chunk := range feedChunkSizes {
			_, err := feedRun[feedDoc](t, data, chunk)
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

// A missing colon whose follower is a scalar exercises the colon checks after
// a fetched key: the scanner can withhold that follower as an unterminated
// scalar, and a check reading the window sentinel instead reports the edge as
// truncation, not the offending token. Every chunking must report exactly the
// contiguous verdict.
func TestFeedColonEdgeParity(t *testing.T) {
	t.Run("struct", func(t *testing.T) {
		feedColonParity[feedDoc](t)
	})
	t.Run("map", func(t *testing.T) {
		feedColonParity[map[string]int](t)
	})
	t.Run("anyMap", func(t *testing.T) {
		feedColonParity[map[string]any](t)
	})
	t.Run("any", func(t *testing.T) {
		feedColonParity[any](t)
	})
}

func feedColonParity[T any](t *testing.T) {
	t.Helper()
	for _, doc := range []string{
		`{"i" 1}`,        // a number standing in the ':' place
		`{"i" 1`,         // and truncated right after it
		`{"i" tru}`,      // a keyword prefix whose letters may still arrive
		`{"i":1,"i" 2}`,  // a later member
		`{"i":1,"zz" 2}`, // an unknown key skipped before its colon
	} {
		data := []byte(doc)
		_, wantErr := feedUnmarshal[T](t, data)
		if wantErr == nil {
			t.Fatalf("contiguous parse of %q unexpectedly succeeded", doc)
		}
		wantKind, wantOff := feedErrKind(t, wantErr)
		for _, chunk := range feedChunkSizes {
			_, err := feedRun[T](t, data, chunk)
			if err == nil {
				t.Fatalf("feed chunk=%d doc=%q: unexpectedly succeeded", chunk, doc)
			}
			kind, off := feedErrKind(t, err)
			if kind != wantKind || off != wantOff {
				t.Fatalf("feed chunk=%d doc=%q: error (%s,%d), contiguous (%s,%d)", chunk, doc, kind, off, wantKind, wantOff)
			}
		}
	}
}

// A mismatch token landing at a non-final window edge exercises the
// advance-then-compare dispatch sites (root brackets, array and map element
// dispatch). The token must produce the same definite error as the contiguous
// engine, never an input yield that drops it.
func TestFeedRootMismatchParity(t *testing.T) {
	t.Run("int", func(t *testing.T) {
		feedMismatchParity[int](t, []string{`{"i":1}`, `[1,2]`, `"x"`, `true`, `1.5`})
	})
	t.Run("string", func(t *testing.T) {
		feedMismatchParity[string](t, []string{`123`, `true`, `[1]`, `{}`})
	})
	t.Run("bool", func(t *testing.T) {
		feedMismatchParity[bool](t, []string{`{"b":true}`, `[true]`, `1`, `"x"`})
	})
	t.Run("slice", func(t *testing.T) {
		feedMismatchParity[[]int](t, []string{`{"i":1}`, `"x"`, `1`, `true`})
	})
	t.Run("map", func(t *testing.T) {
		feedMismatchParity[map[string]int](t, []string{`[1]`, `42`, `"x"`, `true`})
	})
	t.Run("struct", func(t *testing.T) {
		feedMismatchParity[feedDoc](t, []string{`[1,2,3]`, `42`, `"x"`, `true`})
	})
}

func feedMismatchParity[T any](t *testing.T, docs []string) {
	t.Helper()
	for _, doc := range docs {
		data := []byte(doc)
		_, wantErr := feedUnmarshal[T](t, data)
		if wantErr == nil {
			t.Fatalf("contiguous parse of %q unexpectedly succeeded", doc)
		}
		wantKind, wantOff := feedErrKind(t, wantErr)
		for _, chunk := range feedChunkSizes {
			_, err := feedRun[T](t, data, chunk)
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

func TestFeedScalarRoots(t *testing.T) {
	docs := []string{"123", "-1.5e3", `"text"`, "true", "false", "null", `"日本語"`}
	for _, doc := range docs {
		want, wantErr := feedUnmarshal[string](t, []byte(doc))
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[string](t, []byte(doc), chunk)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("doc=%q chunk=%d: err=%v contiguous=%v", doc, chunk, err, wantErr)
			}
			if err == nil && got != want {
				t.Fatalf("doc=%q chunk=%d: got %q want %q", doc, chunk, got, want)
			}
		}
	}
}

func TestFeedSliceRoots(t *testing.T) {
	doc := `[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]`
	want, err := feedUnmarshal[[]int](t, []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range feedChunkSizes {
		got, err := feedRun[[]int](t, []byte(doc), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk=%d: got %v want %v", chunk, got, want)
		}
	}
}

func TestFeedMapRoot(t *testing.T) {
	doc := `{"alpha":1,"beta":2,"gamma":3,"日本":4,"long key with spaces":5}`
	want, err := feedUnmarshal[map[string]int](t, []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range feedChunkSizes {
		got, err := feedRun[map[string]int](t, []byte(doc), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk=%d: got %v want %v", chunk, got, want)
		}
	}
}

func TestFeedEmptyInput(t *testing.T) {
	_, wantErr := feedUnmarshal[feedDoc](t, []byte(""))
	if wantErr == nil {
		t.Fatal("contiguous empty input unexpectedly succeeded")
	}
	wantKind, _ := feedErrKind(t, wantErr)
	for _, chunk := range []int{1, 64} {
		_, err := feedRun[feedDoc](t, []byte(""), chunk)
		if err == nil {
			t.Fatalf("chunk=%d: unexpectedly succeeded", chunk)
		}
		kind, _ := feedErrKind(t, err)
		if kind != wantKind {
			t.Fatalf("chunk=%d: kind %q want %q (%v)", chunk, kind, wantKind, err)
		}
	}
}

func TestFeedWhitespaceOnly(t *testing.T) {
	_, wantErr := feedUnmarshal[feedDoc](t, []byte("   \n\t"))
	if wantErr == nil {
		t.Fatal("contiguous whitespace input unexpectedly succeeded")
	}
	wantKind, _ := feedErrKind(t, wantErr)
	_, err := feedRun[feedDoc](t, []byte("   \n\t"), 2)
	if err == nil {
		t.Fatal("feed whitespace input unexpectedly succeeded")
	}
	kind, _ := feedErrKind(t, err)
	if kind != wantKind {
		t.Fatalf("kind %q want %q (%v)", kind, wantKind, err)
	}
}

// TestFeedStreamHostParity covers stream hosts under the streaming input
// engine: the window-aware engine re-enters the stream scope machinery across
// window edges and must decode identically to the contiguous path.
type feedStreamHost struct {
	Items stream.Stream[feedStreamElem] `json:"items"`
	Name  string                        `json:"name"`
}

type feedStreamElem struct {
	ID string `json:"id"`
	N  int    `json:"n"`
}

func TestFeedStreamHostParity(t *testing.T) {
	doc := `{"items":[{"id":"a","n":1},{"id":"b","n":2}],"name":"s"}`
	for _, chunk := range []int{1, 7, 4096} {
		var gotIDs, wantIDs []string
		run := func(feed bool) string {
			p, err := NewParser[feedStreamHost]()
			if err != nil {
				t.Fatal(err)
			}
			var h feedStreamHost
			ids := &gotIDs
			if !feed {
				ids = &wantIDs
			}
			h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
					*ids = append(*ids, it.Target().ID)
				}
				return nil
			})
			if feed {
				if err := p.UnmarshalFeed(&chunkReader{data: []byte(doc), chunk: chunk}, &h); err != nil {
					t.Fatalf("chunk=%d: %v", chunk, err)
				}
			} else {
				if err := p.Unmarshal([]byte(doc), &h); err != nil {
					t.Fatalf("chunk=%d: contiguous: %v", chunk, err)
				}
			}
			return h.Name
		}
		if gotName, wantName := run(true), run(false); gotName != wantName || gotName != "s" {
			t.Fatalf("chunk=%d: name %q want %q", chunk, gotName, wantName)
		}
		if len(gotIDs) != 2 || gotIDs[0] != "a" || gotIDs[1] != "b" {
			t.Fatalf("chunk=%d: ids=%v", chunk, gotIDs)
		}
		if len(wantIDs) != 2 || wantIDs[0] != "a" || wantIDs[1] != "b" {
			t.Fatalf("chunk=%d: contiguous ids=%v", chunk, wantIDs)
		}
	}
}

// feedArrGuard brackets a fixed array with sentinel fields: a surplus element
// bound past the array end lands in Tail or Pad instead of being skipped.
type feedArrGuard struct {
	Arr  [2]int `json:"arr"`
	Tail int    `json:"tail"`
	Pad  [4]int `json:"pad"`
}

// TestFeedFixedArraySurplusSkip splits a fixed array's surplus elements at
// every offset. A skip interrupted by a window edge must resume as the skip,
// not as an element bind at the slot past the array end.
func TestFeedFixedArraySurplusSkip(t *testing.T) {
	data := []byte(`{"arr":[1,2,3,[4,{"x":5}],6,7,8,9],"tail":-1}`)
	want := feedArrGuard{Arr: [2]int{1, 2}, Tail: -1}
	for chunk := 1; chunk <= len(data); chunk++ {
		got, err := feedRun[feedArrGuard](t, data, chunk)
		if err != nil || got != want {
			t.Fatalf("chunk=%d: got %+v err=%v, want %+v", chunk, got, err, want)
		}
	}
}

type feedMapHost struct {
	M map[string]int `json:"m"`
}

type feedIntStreamHost struct {
	Items stream.Stream[int] `json:"items"`
}

// TestFeedTrailingCommaAtResetBoundary places a trailing comma where the
// container's staging state looks freshly opened: right after a map region
// flush (32 entries) and right after a stream batch rotation. Only the phase
// that follows the opening bracket may accept a closing bracket.
func TestFeedTrailingCommaAtResetBoundary(t *testing.T) {
	var mb strings.Builder
	mb.WriteString(`{"m":{`)
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&mb, `"k%d":%d,`, i, i)
	}
	mb.WriteString(`}}`)
	for _, chunk := range feedChunkSizes {
		if _, err := feedRun[feedMapHost](t, []byte(mb.String()), chunk); err == nil {
			t.Fatalf("map chunk=%d: trailing comma after region flush accepted", chunk)
		}
	}

	for n := 1; n <= 64; n++ {
		var sb strings.Builder
		sb.WriteString(`{"items":[`)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, `%d,`, i)
		}
		sb.WriteString(`]}`)
		for _, chunk := range []int{1, 7, 4096} {
			p, err := NewParser[feedIntStreamHost]()
			if err != nil {
				t.Fatal(err)
			}
			var h feedIntStreamHost
			h.Items.OnRead(func(s stream.Scope[int]) error {
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
				}
				return nil
			})
			if err := p.UnmarshalFeed(&chunkReader{data: []byte(sb.String()), chunk: chunk}, &h); err == nil {
				t.Fatalf("stream n=%d chunk=%d: trailing comma after batch rotation accepted", n, chunk)
			}
		}
	}
}

// TestFeedEmptyStreamAcrossEdge splits an empty stream between its brackets.
// The handler activation the open site performs for a visible ']' must also
// happen when the ']' arrives in a later window.
func TestFeedEmptyStreamAcrossEdge(t *testing.T) {
	data := []byte(`{"items":[],"name":"s"}`)
	run := func(feed bool, chunk int) (int, string) {
		p, err := NewParser[feedStreamHost]()
		if err != nil {
			t.Fatal(err)
		}
		var h feedStreamHost
		calls := 0
		h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
			calls++
			for it := range s.Iter() {
				if derr := it.Decode(); derr != nil {
					return derr
				}
			}
			return nil
		})
		if feed {
			err = p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h)
		} else {
			err = p.Unmarshal(data, &h)
		}
		if err != nil {
			t.Fatalf("feed=%v chunk=%d: %v", feed, chunk, err)
		}
		return calls, h.Name
	}
	wantCalls, wantName := run(false, 0)
	for chunk := 1; chunk <= len(data); chunk++ {
		if calls, name := run(true, chunk); calls != wantCalls || name != wantName {
			t.Fatalf("chunk=%d: calls=%d name=%q, contiguous calls=%d name=%q", chunk, calls, name, wantCalls, wantName)
		}
	}
}

// feedTextTok records whether the TextUnmarshaler hook ran so null handling
// can distinguish a skipped callback from an empty-string call.
type feedTextTok struct {
	Text   string
	Called bool
}

func (t *feedTextTok) UnmarshalText(b []byte) error {
	t.Called = true
	t.Text = string(b)
	return nil
}

// feedUnmValue counts hook invocations; the counter is package-level because
// the hook receives only the receiver pointer.
type feedUnmValue struct {
	Payload string
}

var feedUnmCalls int

// feedUnmPayload, when non-nil, observes the exact bytes each hook call
// receives; the clamp test reads it back.
var feedUnmPayload *string

func (v *feedUnmValue) UnmarshalJSON(b []byte) error {
	feedUnmCalls++
	v.Payload = string(b)
	if feedUnmPayload != nil {
		*feedUnmPayload = string(b)
	}
	return nil
}

type feedRawDoc struct {
	I   int                        `json:"i"`
	R   json.RawMessage            `json:"r"`
	Rs  []json.RawMessage          `json:"rs"`
	Rm  map[string]json.RawMessage `json:"rm"`
	Ra  [2]json.RawMessage         `json:"ra"`
	T   feedTextTok                `json:"t"`
	U   feedUnmValue               `json:"u"`
	Ptr *json.RawMessage           `json:"ptr"`
}

var feedRawValidDocs = []string{
	`{"i":1,"r":{"x":[1,2]},"rs":[{"a":1},"str",null,42],"rm":{"k":{"deep":[1,{"z":2}]}},"ra":[true,false],"t":"tok","u":{"u":1},"ptr":[1,{"p":2}]}`,
	`{"i":2,"r":null,"rs":null,"t":null,"u":null,"ptr":null}`,
	`{"i":3,"r":123.45e10,"t":"日本語 \"q\" back\\slash é","u":[1,{"deep":[2]}]}`,
	`{"r":{"nested":{"deep":{"x":[1,2,3,{"y":"z"}]}}},"t":"edge"}`,
	`{"rm":{"a":{"v":1},"b":[1,2],"c":"s","d":null}}`,
}

// TestFeedDeferredSplitParity runs the deferred kinds through every split and
// compares against the contiguous oracle: values byte-for-byte, errors by kind
// and absolute offset.
func TestFeedDeferredSplitParity(t *testing.T) {
	for _, doc := range feedRawValidDocs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[feedRawDoc](t, data)
		if wantErr != nil {
			t.Fatalf("contiguous parse of %q failed: %v", doc, wantErr)
		}
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[feedRawDoc](t, data, chunk)
			if err != nil {
				t.Fatalf("feed chunk=%d doc=%q: %v", chunk, doc, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("feed chunk=%d doc=%q: got %+v want %+v", chunk, doc, got, want)
			}
		}
	}
}

// TestFeedDeferredRootRaw binds a root-level RawMessage, the atom and the
// container form, and compares the exact raw bytes.
func TestFeedDeferredRootRaw(t *testing.T) {
	docs := []string{
		` {"x":[1,2],"s":"v"} `,
		`[1,{"k":2},null,true]`,
		`"just a string"`,
		`42`,
		`null`,
		`true`,
	}
	for _, doc := range docs {
		data := []byte(doc)
		want, wantErr := feedUnmarshal[json.RawMessage](t, data)
		if wantErr != nil {
			t.Fatalf("contiguous parse of %q failed: %v", doc, wantErr)
		}
		for _, chunk := range feedChunkSizes {
			got, err := feedRun[json.RawMessage](t, data, chunk)
			if err != nil {
				t.Fatalf("feed chunk=%d doc=%q: %v", chunk, doc, err)
			}
			if string(got) != string(want) {
				t.Fatalf("feed chunk=%d doc=%q: got %q want %q", chunk, doc, got, want)
			}
		}
	}
}

var feedRawInvalidDocs = []string{
	`{"r":{"x":`,
	`{"r":{"x":[1,`,
	`{"r":`,
	`{"r":"unterminated`,
	`{"t":42}`,
	`{"r":{"x":1},"i":2} trailing`,
}

// TestFeedDeferredSplitParityInvalid compares error kind and absolute offset
// against the contiguous oracle for truncated and mismatched deferred values.
func TestFeedDeferredSplitParityInvalid(t *testing.T) {
	for _, doc := range feedRawInvalidDocs {
		data := []byte(doc)
		_, wantErr := feedUnmarshal[feedRawDoc](t, data)
		if wantErr == nil {
			t.Fatalf("contiguous parse of %q unexpectedly succeeded", doc)
		}
		wantKind, wantOff := feedErrKind(t, wantErr)
		for _, chunk := range feedChunkSizes {
			_, err := feedRun[feedRawDoc](t, data, chunk)
			if err == nil {
				t.Fatalf("feed chunk=%d doc=%q: unexpected success", chunk, doc)
			}
			kind, off := feedErrKind(t, err)
			if kind != wantKind || off != wantOff {
				t.Fatalf("feed chunk=%d doc=%q: got (%q,%d) want (%q,%d) (%v)", chunk, doc, kind, off, wantKind, wantOff, err)
			}
		}
	}
}

// TestFeedDeferredHandlerOnce counts UnmarshalJSON invocations across every
// split: each value must call its hook exactly once no matter how many windows
// it crossed.
func TestFeedDeferredHandlerOnce(t *testing.T) {
	doc := `{"u":{"a":[1,2,{"b":"c"}]},"i":1}`
	for _, chunk := range feedChunkSizes {
		feedUnmCalls = 0
		if _, err := feedRun[feedUnmValue](t, []byte(doc), chunk); err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if feedUnmCalls != 1 {
			t.Fatalf("chunk=%d: UnmarshalJSON called %d times, want 1", chunk, feedUnmCalls)
		}
	}
}

// TestFeedDeferredNullSemantics pins the null contract: TextUnmarshaler keeps
// its zero value without a call, RawMessage records the literal null.
func TestFeedDeferredNullSemantics(t *testing.T) {
	type nullDoc struct {
		T feedTextTok     `json:"t"`
		R json.RawMessage `json:"r"`
	}
	for _, chunk := range feedChunkSizes {
		got, err := feedRun[nullDoc](t, []byte(`{"t":null,"r":null}`), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if got.T.Called {
			t.Fatalf("chunk=%d: TextUnmarshaler called for null", chunk)
		}
		if string(got.R) != "null" {
			t.Fatalf("chunk=%d: RawMessage = %q, want null", chunk, got.R)
		}
	}
}

// TestFeedDeferredClampSpan pins the stable-end clamp: when a raw value's
// closing bracket is the last published structural and the withheld tail
// begins right after, the recorded span must stop at the stable prefix. The
// drain runs at the following input yield, before the missing-comma error,
// so the hook observes the span directly.
func TestFeedDeferredClampSpan(t *testing.T) {
	needNativeBinder(t)
	type clampDoc struct {
		U feedUnmValue `json:"u"`
	}
	// The quote after the value's closing bracket is the withheld tail for
	// these chunk sizes, so the span end reads the window sentinel. The drain
	// runs at the input yield before the error surfaces; a single-window feed
	// errors first and never drains.
	for _, chunk := range []int{14, 15, 16} {
		var got string
		feedUnmPayload = &got
		p, err := NewParser[clampDoc]()
		if err != nil {
			t.Fatal(err)
		}
		var out clampDoc
		err = p.UnmarshalFeed(&chunkReader{data: []byte(`{"u":{"a":1} "x"}`), chunk: chunk}, &out)
		feedUnmPayload = nil
		if err == nil {
			t.Fatalf("chunk=%d: expected missing-comma error", chunk)
		}
		if got != `{"a":1}` {
			t.Fatalf("chunk=%d: hook received %q, want %q", chunk, got, `{"a":1}`)
		}
	}
}

// TestFeedDeferredMapFlushCrossing forces map-region flushes interleaved with
// input yields: more values than one region holds, each a raw object wide
// enough to cross window edges.
func TestFeedDeferredMapFlushCrossing(t *testing.T) {
	type mapDoc struct {
		M map[string]json.RawMessage `json:"m"`
	}
	var sb strings.Builder
	sb.WriteString(`{"m":{`)
	for i := 0; i < 24; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%02d":{"nested":[%d,{"x":"y"}]}`, i, i)
	}
	sb.WriteString(`}}`)
	data := []byte(sb.String())
	want, wantErr := feedUnmarshal[mapDoc](t, data)
	if wantErr != nil {
		t.Fatalf("contiguous parse failed: %v", wantErr)
	}
	for _, chunk := range []int{1, 3, 31, 64, 4096} {
		got, err := feedRun[mapDoc](t, data, chunk)
		if err != nil {
			t.Fatalf("feed chunk=%d: %v", chunk, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("feed chunk=%d: got %+v want %+v", chunk, got, want)
		}
	}
}
