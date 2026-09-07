package bind

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Scoped arena views: a stream scope's str/tape bytes must follow the
// remaining input instead of accumulating for the whole parse, and rotation
// must not corrupt what earlier batches published.

// scopeValueElem adds a Value field so rotations exercise per-generation
// documents: a Value bound in generation G keeps resolving against the
// backing its offsets were written into.
type scopeValueElem struct {
	ID string      `json:"id"`
	V  value.Value `json:"v"`
}

type scopeValueHost struct {
	Items stream.Stream[scopeValueElem] `json:"items"`
}

func scopeValueJSON(n int, pad int) []byte {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"id-%d-%s","v":{"k":"v-%d","n":%d}}`, i, strings.Repeat("x", pad), i, i)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// TestScopeViewRotationBounded asserts the geometric shrink: the scoped str
// view is installed at the remaining-source bound and replaced by a halved
// view each time consumption crosses half of it. Without scoped views the
// single arena holds every element's bytes until the parse ends.
func TestScopeViewRotationBounded(t *testing.T) {
	const n = 20000
	data := scopeValueJSON(n, 40)

	p, err := NewParser[scopeValueHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var h scopeValueHost
	var caps []int
	consumed := 0
	h.Items.OnRead(func(s stream.Scope[scopeValueElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			consumed++
			// Sampled per element: the view only changes at batch settles.
			strCap, _ := p.alloc.ArenaViewCaps()
			if len(caps) == 0 || caps[len(caps)-1] != strCap {
				caps = append(caps, strCap)
			}
		}
		return nil
	})
	if err := p.Unmarshal(data, &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if consumed != n {
		t.Fatalf("consumed %d, want %d", consumed, n)
	}
	if len(caps) < 3 {
		t.Fatalf("expected at least three distinct view sizes across the parse, got %v", caps)
	}
	for i := 1; i < len(caps); i++ {
		if caps[i] >= caps[i-1] {
			t.Fatalf("view capacity must never grow across a parse: %v", caps)
		}
	}
	if last := caps[len(caps)-1]; last > caps[0]/2 {
		t.Fatalf("final view %d never halved from the initial %d", last, caps[0])
	}
	if caps[0] > len(data)+64 {
		t.Fatalf("initial view %d exceeds the remaining-source bound for a %d-byte document", caps[0], len(data))
	}
}

// TestScopeViewValueFieldsSurviveRotations holds elements across many
// generations and reads their Values after the parse. Each Value must
// resolve against its own generation's document, not the final one.
func TestScopeViewValueFieldsSurviveRotations(t *testing.T) {
	const n = 20000
	const keepEvery = 200
	data := scopeValueJSON(n, 0)

	var h scopeValueHost
	var held []*scopeValueElem
	var heldIdx []int
	consumed := 0
	h.Items.OnRead(func(s stream.Scope[scopeValueElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			if consumed%keepEvery == 0 {
				held = append(held, it.Target())
				heldIdx = append(heldIdx, consumed)
			}
			consumed++
		}
		return nil
	})
	if err := Unmarshal(data, &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(held) != (n+keepEvery-1)/keepEvery {
		t.Fatalf("held %d elements", len(held))
	}
	for k, e := range held {
		i := heldIdx[k]
		if got, want := e.ID, fmt.Sprintf("id-%d-", i); got != want {
			t.Fatalf("held[%d].ID = %q, want %q", k, got, want)
		}
		kv := e.V.Get("k")
		s, ok := kv.Str()
		if !ok || s != fmt.Sprintf("v-%d", i) {
			t.Fatalf("held[%d].V.k = %q,%v, want %q", k, s, ok, fmt.Sprintf("v-%d", i))
		}
		nv := e.V.Get("n")
		got, ok := nv.Int()
		if !ok || got != int64(i) {
			t.Fatalf("held[%d].V.n = %d,%v, want %d", k, got, ok, i)
		}
	}
}

// TestScopeViewValueReadablePerBatch reads every element's Value during
// iteration, before later batches, rotations, or the scope exit could
// publish anything more. SettleBatch must publish the generation's doc per
// batch for these reads to resolve; a rotation-only publication leaves every
// batch but the one whose settle triggered the rotation reading empty.
func TestScopeViewValueReadablePerBatch(t *testing.T) {
	const n = 300
	data := scopeValueJSON(n, 0)

	var h scopeValueHost
	i := 0
	h.Items.OnRead(func(s stream.Scope[scopeValueElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			kvVal := it.Target().V.Get("k")
			kv, ok := kvVal.Str()
			if !ok || kv != fmt.Sprintf("v-%d", i) {
				t.Errorf("element %d: V.k = %q,%v", i, kv, ok)
			}
			i++
		}
		return nil
	})
	if err := Unmarshal(data, &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if i != n {
		t.Fatalf("iterated %d of %d elements", i, n)
	}
}

// TestScopeViewValuePinIsBatchSized asserts a retained Value pins an
// exact-fit snapshot of its own batch's tape words, independent of the view
// bound and the document size. A generation-cumulative publication pins the
// first held element to a view sized for the remaining document, and a view
// into the scratch backing carries that bound-sized capacity.
func TestScopeViewValuePinIsBatchSized(t *testing.T) {
	run := func(n int) (first, max int, exactFit bool) {
		data := scopeValueJSON(n, 0)
		var h scopeValueHost
		var held []value.Value
		h.Items.OnRead(func(s stream.Scope[scopeValueElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				held = append(held, it.Target().V)
			}
			return nil
		})
		if err := Unmarshal(data, &h); err != nil {
			t.Fatalf("Unmarshal(%d): %v", n, err)
		}
		exactFit = true
		for i := range held {
			desc := valueabi.Load(unsafe.Pointer(&held[i]))
			if cap(desc.Doc.Tape) != len(desc.Doc.Tape) {
				exactFit = false
			}
			if l := len(desc.Doc.Tape); l > max {
				max = l
			}
			if i == 0 {
				first = len(desc.Doc.Tape)
			}
		}
		return first, max, exactFit
	}
	// A one-element document measures the per-element tape words.
	perElem, _, ok := run(1)
	if perElem == 0 || !ok {
		t.Fatalf("probe element: tape len %d, exactFit %v", perElem, ok)
	}
	// The leaf batch is the stream SlotClass capacity (4 elements), so every
	// held snapshot must stay at that bound however long the stream runs.
	const leafBatch = 4
	first, max, exactFit := run(2000)
	if limit := leafBatch * perElem; first > limit || max > limit {
		t.Fatalf("held tape lens first=%d max=%d, want <= batch bound %d (per-elem %d)",
			first, max, limit, perElem)
	}
	if !exactFit {
		t.Fatal("held snapshots are not exact-fit")
	}
}

// pinNestedElem is a non-leaf element whose settles run per element, so each
// retained Value pins a single element's snapshot.
type pinNestedInner struct {
	V string `json:"v"`
}

type pinNestedElem struct {
	V  value.Value                   `json:"v"`
	In stream.Stream[pinNestedInner] `json:"in"`
}

type pinNestedHost struct {
	Items stream.Stream[pinNestedElem] `json:"items"`
}

// TestScopeViewValuePinPerElement: the non-leaf batch is one element, so
// per-element settles make every snapshot exactly one Value. A generation
// spanning elements would publish growing cumulative lengths.
func TestScopeViewValuePinPerElement(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"v":{"k":"v-%d"},"in":[{"v":"x"}]}`, i)
	}
	b.WriteString(`]}`)

	var h pinNestedHost
	var held []value.Value
	h.Items.OnRead(func(s stream.Scope[pinNestedElem]) error {
		for it := range s.Iter() {
			it.Target().In.OnRead(func(is stream.Scope[pinNestedInner]) error {
				for iit := range is.Iter() {
					if err := iit.Decode(); err != nil {
						return err
					}
				}
				return nil
			})
			if err := it.Decode(); err != nil {
				return err
			}
			held = append(held, it.Target().V)
		}
		return nil
	})
	if err := Unmarshal([]byte(b.String()), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(held) != 50 {
		t.Fatalf("held %d elements", len(held))
	}
	min, max := 1<<30, 0
	exactFit := true
	for i := range held {
		desc := valueabi.Load(unsafe.Pointer(&held[i]))
		if cap(desc.Doc.Tape) != len(desc.Doc.Tape) {
			exactFit = false
		}
		l := len(desc.Doc.Tape)
		if l < min {
			min = l
		}
		if l > max {
			max = l
		}
	}
	// {"k":"v-N"} is four tape words; every snapshot must be that one Value,
	// exact-fit, regardless of where capacity rotations fall.
	if min != max || min > 8 || !exactFit {
		t.Fatalf("held tape lens min=%d max=%d exactFit=%v, want equal, <= 8, exact-fit", min, max, exactFit)
	}
}

// pinInnerElem gives the nested scope its own tape generation: inner Values
// publish inner snapshots while the outer element's generation is mid-flight.
type pinInnerElem struct {
	V value.Value `json:"v"`
}

type pinBothElem struct {
	V  value.Value                 `json:"v"`
	In stream.Stream[pinInnerElem] `json:"in"`
}

type pinBothHost struct {
	Items stream.Stream[pinBothElem] `json:"items"`
}

// TestScopeViewNestedValueDocs runs a Value-publishing scope nested inside a
// Value-publishing element. Field order alternates so the outer Value binds
// both before the inner scope activates and after it restores, covering both
// ValueDoc switch directions. Every held Value must resolve against its own
// scope's doc, mid-handler reads must resolve, and both levels' snapshots
// stay exact-fit at their own granularity.
func TestScopeViewNestedValueDocs(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 12; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		if i%2 == 0 {
			fmt.Fprintf(&b, `{"v":{"ok":"o-%d"},"in":[`, i)
		} else {
			b.WriteString(`{"in":[`)
		}
		for j := 0; j < 9; j++ {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"v":{"ik":"i-%d-%d"}}`, i, j)
		}
		if i%2 == 0 {
			b.WriteString(`]}`)
		} else {
			fmt.Fprintf(&b, `],"v":{"ok":"o-%d"}}`, i)
		}
	}
	b.WriteString(`]}`)

	var h pinBothHost
	var outerHeld, innerHeld []value.Value
	innerMidOK := 0
	h.Items.OnRead(func(s stream.Scope[pinBothElem]) error {
		for it := range s.Iter() {
			it.Target().In.OnRead(func(is stream.Scope[pinInnerElem]) error {
				for iit := range is.Iter() {
					if err := iit.Decode(); err != nil {
						return err
					}
					ik := iit.Target().V.Get("ik")
					if s2, ok := ik.Str(); ok && s2 != "" {
						innerMidOK++
					}
					innerHeld = append(innerHeld, iit.Target().V)
				}
				return nil
			})
			if err := it.Decode(); err != nil {
				return err
			}
			outerHeld = append(outerHeld, it.Target().V)
		}
		return nil
	})
	if err := Unmarshal([]byte(b.String()), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Mid-handler reads resolve on the inner level as well.
	if innerMidOK != 12*9 {
		t.Fatalf("inner mid-handler reads: %d/%d", innerMidOK, 12*9)
	}
	// Every held Value resolves against its own scope's doc after the parse.
	for i := range outerHeld {
		okv := outerHeld[i].Get("ok")
		if s, ok := okv.Str(); !ok || s != fmt.Sprintf("o-%d", i) {
			t.Fatalf("outer[%d].ok = %q,%v", i, s, ok)
		}
	}
	for k := 0; k < len(innerHeld); k++ {
		i, j := k/9, k%9
		ikv := innerHeld[k].Get("ik")
		if s, ok := ikv.Str(); !ok || s != fmt.Sprintf("i-%d-%d", i, j) {
			t.Fatalf("inner[%d].ik = %q,%v", k, s, ok)
		}
	}
	// The outer level is per-element (one Value), the inner per-batch (four).
	for i := range outerHeld {
		desc := valueabi.Load(unsafe.Pointer(&outerHeld[i]))
		if cap(desc.Doc.Tape) != len(desc.Doc.Tape) || len(desc.Doc.Tape) > 8 {
			t.Fatalf("outer[%d] tape len=%d cap=%d", i, len(desc.Doc.Tape), cap(desc.Doc.Tape))
		}
	}
	for i := range innerHeld {
		desc := valueabi.Load(unsafe.Pointer(&innerHeld[i]))
		if cap(desc.Doc.Tape) != len(desc.Doc.Tape) || len(desc.Doc.Tape) > 16 {
			t.Fatalf("inner[%d] tape len=%d cap=%d", i, len(desc.Doc.Tape), cap(desc.Doc.Tape))
		}
	}
}

// Non-leaf elements pin the outer view across a long inner stream: the outer
// host's own fields bind before and after the inner batches, and the inner
// scope rotates freely underneath.
type scopeNestedInner struct {
	V string `json:"v"`
}

type scopeNestedElem struct {
	ID    string                          `json:"id"`
	Inner stream.Stream[scopeNestedInner] `json:"inner"`
}

type scopeNestedHost struct {
	Items stream.Stream[scopeNestedElem] `json:"items"`
}

func TestScopeViewNestedStreamsBoundAndValid(t *testing.T) {
	// The first outer element's inner stream dominates the document, so the
	// inner scope's views rotate within that single element while the outer
	// view stays quiescent.
	innerCounts := []int{60000, 100, 100}
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i, inner := range innerCounts {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"o-%d","inner":[`, i)
		for j := 0; j < inner; j++ {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"v":"%d-%d-%s"}`, i, j, strings.Repeat("y", 20))
		}
		b.WriteString(`]}`)
	}
	b.WriteString(`]}`)
	data := []byte(b.String())

	p, err := NewParser[scopeNestedHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var h scopeNestedHost
	var firstElemCaps []int
	var capturedFirst []int
	outerIDs := make([]string, 0, len(innerCounts))
	var innerVs []string
	h.Items.OnRead(func(s stream.Scope[scopeNestedElem]) error {
		for it := range s.Iter() {
			// The outer view must stay live and quiescent while the inner
			// scope rotates underneath.
			it.Target().Inner.OnRead(func(is stream.Scope[scopeNestedInner]) error {
				for iit := range is.Iter() {
					if err := iit.Decode(); err != nil {
						return err
					}
					innerVs = append(innerVs, iit.Target().V)
					if c, _ := p.alloc.ArenaViewCaps(); len(firstElemCaps) == 0 || firstElemCaps[len(firstElemCaps)-1] != c {
						firstElemCaps = append(firstElemCaps, c)
					}
				}
				return nil
			})
			if err := it.Decode(); err != nil {
				return err
			}
			outerIDs = append(outerIDs, it.Target().ID)
			// Only the first element's inner stream is large enough to
			// rotate its own views; later elements would only see fresh
			// activation installs.
			if capturedFirst == nil {
				capturedFirst = firstElemCaps
			}
			firstElemCaps = nil
		}
		return nil
	})
	if err := p.Unmarshal(data, &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	total := 0
	for _, c := range innerCounts {
		total += c
	}
	if len(outerIDs) != len(innerCounts) || len(innerVs) != total {
		t.Fatalf("outer=%d inner=%d, want %d/%d", len(outerIDs), len(innerVs), len(innerCounts), total)
	}
	for i, id := range outerIDs {
		if id != fmt.Sprintf("o-%d", i) {
			t.Fatalf("outerIDs[%d] = %q", i, id)
		}
	}
	off := 0
	for i, c := range innerCounts {
		for j := 0; j < c; j++ {
			want := fmt.Sprintf("%d-%d-%s", i, j, strings.Repeat("y", 20))
			if got := innerVs[off+j]; got != want {
				t.Fatalf("innerVs[%d] = %q, want %q", off+j, got, want)
			}
		}
		off += c
	}
	// The first element's inner scope must rotate its own views.
	if len(capturedFirst) < 3 {
		t.Fatalf("inner views never rotated within the first element: %v", capturedFirst)
	}
}

// Provenance matrix: a named variant field whose discriminator is an ordinary
// typed string. The disc and the case sit on opposite sides of a long inner
// stream, so the check spans the inner scope's view rotations.
type scopePolyInner struct {
	V string `json:"v"`
}

type scopePolyCase struct {
	Tag string `json:"tag"`
	N   int    `json:"n"`
}

type scopePolyElem struct {
	D     string                        `json:"d"`
	Inner stream.Stream[scopePolyInner] `json:"inner"`
	Cased any                           `json:"cased" vjson:"variant=d"`
}

type scopePolyHost struct {
	Items stream.Stream[scopePolyElem] `json:"items"`
}

func init() {
	vbind.DefineVariantCases[scopePolyElem, struct {
		_ scopePolyCase `case:"a"`
	}]()
}

func TestScopeViewDiscAcrossInnerStream(t *testing.T) {
	// Both orderings: the discriminator before and after the inner stream
	// that rotates views between the disc bind and the case resolution.
	innerLong := scopePolyInnerList(600)
	for _, tc := range []struct {
		name string
		json string
	}{
		{"discBeforeInner", `{"d":"a","inner":[` + innerLong + `],"cased":{"tag":"t","n":1}}`},
		{"discAfterInner", `{"inner":[` + innerLong + `],"d":"a","cased":{"tag":"t","n":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := `{"items":[` + tc.json + `]}`
			var h scopePolyHost
			var got scopePolyCase
			var haveCase bool
			h.Items.OnRead(func(s stream.Scope[scopePolyElem]) error {
				for it := range s.Iter() {
					it.Target().Inner.OnRead(func(is stream.Scope[scopePolyInner]) error {
						for iit := range is.Iter() {
							return iit.Decode()
						}
						return nil
					})
					if err := it.Decode(); err != nil {
						return err
					}
					got, haveCase = it.Target().Cased.(scopePolyCase)
				}
				return nil
			})
			if err := Unmarshal([]byte(doc), &h); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !haveCase || got.Tag != "t" || got.N != 1 {
				t.Fatalf("case = %+v (have %v), want tag t n 1", got, haveCase)
			}
		})
	}
}

func scopePolyInnerList(n int) string {
	var b strings.Builder
	for j := 0; j < n; j++ {
		if j > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"v":"i-%d-%s"}`, j, strings.Repeat("z", 20))
	}
	return b.String()
}

// A pending TextUnmarshaler record carries a str_arena offset, so the drain
// at every settle must resolve against the generation the text was decoded
// into, before any rotation installs a fresh view.
type scopeTextValue struct {
	V string
}

func (t *scopeTextValue) UnmarshalText(data []byte) error {
	t.V = "text:" + string(data)
	return nil
}

type scopeTextElem struct {
	ID string         `json:"id"`
	T  scopeTextValue `json:"t"`
}

type scopeTextHost struct {
	Items stream.Stream[scopeTextElem] `json:"items"`
}

func TestScopeViewTextUnmarshalerAcrossRotations(t *testing.T) {
	const n = 20000
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"id-%d-%s","t":"val-%d-%s"}`, i, strings.Repeat("x", 30), i, strings.Repeat("y", 10))
	}
	b.WriteString(`]}`)

	var h scopeTextHost
	consumed := 0
	h.Items.OnRead(func(s stream.Scope[scopeTextElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			// Read in batch: the settle drained this batch's records against
			// the generation their offsets were written into.
			e := it.Target()
			want := fmt.Sprintf("text:val-%d-%s", consumed, strings.Repeat("y", 10))
			if e.T.V != want {
				return fmt.Errorf("elem %d T = %q, want %q", consumed, e.T.V, want)
			}
			consumed++
		}
		return nil
	})
	if err := Unmarshal([]byte(b.String()), &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if consumed != n {
		t.Fatalf("consumed %d, want %d", consumed, n)
	}
}

// TestScopeViewStaleDiscIsMissing pins the deliberate semantics change: a
// non-leaf host reuses element memory, so element N+1 whose JSON omits the
// discriminator must not inherit element N's disc through the reused field.
// The provenance floor advanced at every element settle makes the stale disc
// read as missing, matching plain Unmarshal on fresh host memory.
func TestScopeViewStaleDiscIsMissing(t *testing.T) {
	// Element 2 omits the discriminator while presenting case content, so the
	// host's reused memory carries element 1's disc pointer. Element 2 is far
	// larger than element 1, so no view rotation separates the two settles and
	// the provenance floor is the only rejector.
	elem1 := `{"d":"a","inner":[` + scopePolyInnerList(8) + `],"cased":{"tag":"t","n":1}}`
	elem2 := `{"inner":[` + scopePolyInnerList(3000) + `],"cased":{"tag":"u","n":2}}`
	doc := `{"items":[` + elem1 + `,` + elem2 + `]}`

	var h scopePolyHost
	var first scopePolyCase
	h.Items.OnRead(func(s stream.Scope[scopePolyElem]) error {
		for it := range s.Iter() {
			it.Target().Inner.OnRead(func(is stream.Scope[scopePolyInner]) error {
				for iit := range is.Iter() {
					return iit.Decode()
				}
				return nil
			})
			if err := it.Decode(); err != nil {
				return err
			}
			first, _ = it.Target().Cased.(scopePolyCase)
		}
		return nil
	})
	err := Unmarshal([]byte(doc), &h)
	if err == nil {
		t.Fatal("element 2 inherited the stale discriminator; want a missing-discriminator error")
	}
	var ve *VariantError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *VariantError", err)
	}
	if !strings.Contains(ve.Error(), "missing") {
		t.Fatalf("err = %v, want missing-discriminator semantics", err)
	}
	if first.Tag != "t" || first.N != 1 {
		t.Fatalf("element 1 case = %+v, want tag t n 1", first)
	}
}
