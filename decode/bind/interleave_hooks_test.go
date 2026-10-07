package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vopt"
)

// Hooks and panics. User code runs inside drains: a hook may return an error,
// panic, or decode reentrantly. Whatever it does, the Parser (explicit or
// pooled) must serve the next call exactly like a fresh one, and values handed
// out by earlier calls must stay intact.

type ilhHook struct{ Raw string }

func (x *ilhHook) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case `"boom"`:
		panic("ilh: hook boom")
	case `"fail"`:
		return errors.New("ilh: hook fail")
	}
	x.Raw = "H:" + string(b)
	return nil
}

type ilhText struct{ V string }

func (x *ilhText) UnmarshalText(b []byte) error {
	switch string(b) {
	case "boom":
		panic("ilh: text boom")
	case "bad":
		return errors.New("ilh: text bad")
	}
	x.V = "T:" + string(b)
	return nil
}

type ilhEmbHook struct {
	ilhHook
	E int
}

type ilhDoc struct {
	A   int
	S   string
	H   ilhHook
	HP  *ilhHook
	HS  []ilhHook
	HM  map[string]ilhHook
	HMP map[string]*ilhHook
	KM  map[int8]int
	KMH map[uint8]ilhHook
	T   ilhText
	TS  []ilhText
	TM  map[string]ilhText
	I   any
	Emb ilhEmbHook
	X   []int
	M   map[string]string
}

var ilhGood = []string{
	`{"A":1,"S":"s\n1","H":{"x":1},"HP":[1],"HS":["a\n",2,{"k":"v"}],"X":[1,2,3]}`,
	`{"HM":{"a":"v","b":2},"HMP":{"p":1,"q":null},"KM":{"1":1,"-2":2},"KMH":{"3":"hz"},"T":"abc\n","TS":["a","b"],"TM":{"m":"t"}}`,
	`{"I":{"a":[1,"s\n"]},"Emb":{"E":1},"M":{"mk":"mv\n"}}`,
}

var ilhPanics = []string{
	`{"S":"before\n","H":"boom"}`,
	`{"S":"before\n","X":[1,2],"HS":["a","boom","c"]}`,
	`{"S":"before\n","HM":{"a":"x","b":"boom"}}`,
	`{"S":"before\n","T":"boom"}`,
	`{"S":"before\n","TS":["a","boom"]}`,
	`{"S":"before\n","KM":{"1":1,"999":2},"H":"boom"}`,
	`{"S":"before\n","KMH":{"999":"h"},"HS":[1,"boom"]}`,
	`{"S":"before\n","M":{"k":"v\n"},"TM":{"a":"boom"}}`,
}

var ilhFails = []string{
	`{"H":"fail"}`,
	`{"KM":{"999":1}}`,
	`{"HS":["fail"],"KM":{"999":1}}`,
	`{"T":"bad","H":"fail"}`,
	`{"KMH":{"-1":"x"},"HM":{"a":"fail"}}`,
	`{"KM":{"1000":1,"2000":2},"X":[1,2,3}`,
}

type ilhMode int

const (
	ilhParserMode ilhMode = iota
	ilhPoolMode
)

func (m ilhMode) String() string {
	if m == ilhPoolMode {
		return "pool"
	}
	return "parser"
}

// ilhRun decodes in into a fresh T and snapshots it. A panic is recovered into
// the outcome, so the sweeps compare panics like any other error.
func ilhRun[T any](mode ilhMode, p *Parser, in string, opts []UnmarshalOption, prep func(*T)) (out ilOutcome) {
	defer func() {
		if r := recover(); r != nil {
			out = ilOutcome{err: fmt.Sprintf("PANIC: %v", r)}
		}
	}()
	v := new(T)
	if prep != nil {
		prep(v)
	}
	var err error
	if mode == ilhPoolMode {
		err = Unmarshal([]byte(in), v, opts...)
	} else {
		err = p.Unmarshal([]byte(in), v, opts...)
	}
	out.err = ilDescribeErr(err)
	js, jerr := json.Marshal(v)
	out.json = string(js)
	if jerr != nil {
		out.json += "|marshal-err:" + jerr.Error()
	}
	out.val = v
	return out
}

// ilhSweep interleaves mutants and good or panicking documents on one dirty
// Parser (or the pool) and compares each outcome with a fresh Parser.
func ilhSweep[T any](t *testing.T, mode ilhMode, docs []string, prep func(*T)) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	var mutants []string
	for _, g := range docs {
		mutants = append(mutants, ilSweepMutants(g)...)
	}
	dirty, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	fresh := func(in string, opts []UnmarshalOption) ilOutcome {
		p, _ := NewParser[T]()
		return ilhRun(ilhParserMode, p, in, opts, prep)
	}
	exactDocs := map[string]bool{}
	for _, d := range docs {
		exactDocs[d] = true
	}
	var hist []string
	var nbad int
	seen := map[string]bool{}
	check := func(in string, oi int) {
		opts := ilOptSets[oi]
		hist = append(hist, fmt.Sprintf("opts#%d %q", oi, in))
		if len(hist) > 5 {
			hist = hist[1:]
		}
		got := ilhRun(mode, dirty, in, opts, prep)
		want := fresh(in, opts)
		if ilhSame(want, got, exactDocs[in]) || nbad >= 6 {
			return
		}
		key := want.err + "|" + got.err
		if seen[key] {
			return
		}
		seen[key] = true
		nbad++
		t.Errorf("%s: history dependence\n last steps:\n   %s\n fresh: err=%s\n dirty: err=%s\n %s",
			mode, strings.Join(hist, "\n   "), want.err, got.err, ilDiff(want, got))
	}
	for i, m := range mutants {
		check(m, i%len(ilOptSets))
		check(docs[i%len(docs)], (i+1)%len(ilOptSets))
		if i%5 == 0 {
			check(ilhPanics[(i/5)%len(ilhPanics)], (i+2)%len(ilOptSets))
		}
		if i%7 == 0 {
			check(ilhFails[(i/7)%len(ilhFails)], (i+3)%len(ilOptSets))
		}
		if i%97 == 0 {
			runtime.GC()
		}
	}
}

// ilhSame is ilSame, except that a failing parse may or may not have reached a
// panicking hook before its walk error surfaced: flush timing depends on
// allocator history. Unmutated documents compare strictly.
func ilhSame(want, got ilOutcome, exact bool) bool {
	wp, gp := strings.HasPrefix(want.err, "PANIC"), strings.HasPrefix(got.err, "PANIC")
	if !exact && wp != gp && want.err != "" && got.err != "" {
		return true
	}
	return ilSame(want, got, false)
}

func ilhAllDocs() []string {
	docs := append([]string(nil), ilhGood...)
	docs = append(docs, ilhPanics...)
	docs = append(docs, ilhFails...)
	return docs
}

func TestIlhSweepDocParser(t *testing.T) { ilhSweep[ilhDoc](t, ilhParserMode, ilhAllDocs(), nil) }
func TestIlhSweepDocPool(t *testing.T)   { ilhSweep[ilhDoc](t, ilhPoolMode, ilhAllDocs(), nil) }

// A non-empty interface preset to a pointer routes records through
// bindIfaceRecord, including the sub-parser it opens for plain pointees.
type ilhIfaceDoc struct {
	U  json.Unmarshaler
	TU interface{ UnmarshalText([]byte) error }
	P  any
	PI any
	L  []any
}

var ilhIfaceGood = []string{
	`{"U":{"a":1},"TU":"txt\n","P":{"A":3,"S":"x\n"},"PI":[1,2,3]}`,
	`{"U":null,"TU":null,"P":null,"L":[null,null]}`,
}

func ilhIfacePrep(d *ilhIfaceDoc) {
	d.U = new(ilhHook)
	d.TU = new(ilhText)
	d.P = new(ilhDoc)
	d.PI = new([]int)
}

func TestIlhSweepIfaceParser(t *testing.T) {
	docs := append([]string{
		`{"U":"boom"}`, `{"U":"fail"}`, `{"TU":"boom"}`, `{"TU":"bad"}`,
		`{"P":{"H":"boom"}}`, `{"P":{"H":"fail","KM":{"999":1}}}`, `{"P":{"A":"x"}}`,
	}, ilhIfaceGood...)
	ilhSweepKeep[ilhIfaceDoc](t, ilhParserMode, docs, ilhIfacePrep)
}

func TestIlhSweepIfacePool(t *testing.T) {
	docs := append([]string{
		`{"U":"boom"}`, `{"U":"fail"}`, `{"TU":"boom"}`, `{"TU":"bad"}`,
		`{"P":{"H":"boom"}}`, `{"P":{"H":"fail","KM":{"999":1}}}`, `{"P":{"A":"x"}}`,
	}, ilhIfaceGood...)
	ilhSweepKeep[ilhIfaceDoc](t, ilhPoolMode, docs, ilhIfacePrep)
}

// ilhSweepKeep is ilhSweep without the shared panic and fail document lists,
// for document families of their own.
func ilhSweepKeep[T any](t *testing.T, mode ilhMode, docs []string, prep func(*T)) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	var mutants []string
	for _, g := range docs {
		mutants = append(mutants, ilSweepMutants(g)...)
	}
	dirty, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exactDocs := map[string]bool{}
	for _, d := range docs {
		exactDocs[d] = true
	}
	var hist []string
	var nbad int
	seen := map[string]bool{}
	check := func(in string, oi int) {
		opts := ilOptSets[oi]
		hist = append(hist, fmt.Sprintf("opts#%d %q", oi, in))
		if len(hist) > 5 {
			hist = hist[1:]
		}
		got := ilhRun(mode, dirty, in, opts, prep)
		fp, _ := NewParser[T]()
		want := ilhRun(ilhParserMode, fp, in, opts, prep)
		if ilhSame(want, got, exactDocs[in]) || nbad >= 6 {
			return
		}
		key := want.err + "|" + got.err
		if seen[key] {
			return
		}
		seen[key] = true
		nbad++
		t.Errorf("%s: history dependence\n last steps:\n   %s\n fresh: err=%s\n dirty: err=%s\n %s",
			mode, strings.Join(hist, "\n   "), want.err, got.err, ilDiff(want, got))
	}
	for i, m := range mutants {
		check(m, i%len(ilOptSets))
		check(docs[i%len(docs)], (i+1)%len(ilOptSets))
		if i%97 == 0 {
			runtime.GC()
		}
	}
}

// Retention. A destination returned by an earlier call, successful, failed, or
// panicked, is the caller's. No later call on the same Parser may rewrite it.

type ilhKept struct {
	v    *ilhDoc
	snap string
	in   string
	step int
}

func ilhSnap(v *ilhDoc) string {
	js, err := json.Marshal(v)
	if err != nil {
		return "marshal-err:" + err.Error()
	}
	return string(js)
}

func ilhRetainSweep(t *testing.T, mode ilhMode, zeroCopy, withPanics bool) {
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	docs := ilhAllDocs()
	panics := ilhPanics
	if !withPanics {
		docs = append(append([]string(nil), ilhGood...), ilhFails...)
		panics = ilhFails
	}
	var inputs []string
	for _, g := range docs {
		inputs = append(inputs, ilSweepMutants(g)...)
	}
	// Interleave the structured families with mutants so failures and panics
	// are frequent.
	var seq []string
	for i, m := range inputs {
		seq = append(seq, m, panics[i%len(panics)], docs[i%len(docs)])
	}
	p, err := NewParser[ilhDoc]()
	if err != nil {
		t.Fatal(err)
	}
	opts := []UnmarshalOption{vopt.ZeroCopy(zeroCopy)}
	var kept []ilhKept
	bad := 0
	report := func(k ilhKept, cur string, culprit string, step int) {
		if bad >= 4 {
			return
		}
		bad++
		i := 0
		for i < len(k.snap) && i < len(cur) && k.snap[i] == cur[i] {
			i++
		}
		lo := max(0, i-50)
		t.Errorf("%s zerocopy=%v: result of step %d (input %q) changed by step %d (input %q)\n at %d:\n   was ...%.120s\n   now ...%.120s",
			mode, zeroCopy, k.step, k.in, step, culprit, i, k.snap[lo:], cur[lo:])
	}
	for step, in := range seq {
		v := new(ilhDoc)
		func() {
			defer func() { _ = recover() }()
			if mode == ilhPoolMode {
				_ = Unmarshal([]byte(in), v, opts...)
			} else {
				_ = p.Unmarshal([]byte(in), v, opts...)
			}
		}()
		kept = append(kept, ilhKept{v, ilhSnap(v), in, step})
		if len(kept) > 400 {
			kept = kept[len(kept)-400:]
		}
		for _, k := range kept[max(0, len(kept)-17) : len(kept)-1] {
			if cur := ilhSnap(k.v); cur != k.snap {
				report(k, cur, in, step)
				k.snap = cur
			}
		}
		if step%200 == 0 {
			runtime.GC()
			for _, k := range kept[:len(kept)-1] {
				if cur := ilhSnap(k.v); cur != k.snap {
					report(k, cur, in+" (found at GC check)", step)
				}
			}
		}
		if bad >= 4 {
			return
		}
	}
}

func TestIlhRetainParserZeroCopyOff(t *testing.T) { ilhRetainSweep(t, ilhParserMode, false, true) }
func TestIlhRetainParserZeroCopyOn(t *testing.T)  { ilhRetainSweep(t, ilhParserMode, true, true) }
func TestIlhRetainPoolZeroCopyOff(t *testing.T)   { ilhRetainSweep(t, ilhPoolMode, false, true) }

// The same retention check without panicking documents isolates the error paths.
func TestIlhRetainErrorsParserZeroCopyOff(t *testing.T) {
	ilhRetainSweep(t, ilhParserMode, false, false)
}
func TestIlhRetainErrorsParserZeroCopyOn(t *testing.T) { ilhRetainSweep(t, ilhParserMode, true, false) }
func TestIlhRetainErrorsPoolZeroCopyOff(t *testing.T)  { ilhRetainSweep(t, ilhPoolMode, false, false) }

// A panic escaping a hook must not let the next call overwrite strings the
// panicked call already published into its destination.
func TestIlhPanicKeepsPublishedStrings(t *testing.T) {
	p, _ := NewParser[ilhDoc]()
	opts := []UnmarshalOption{vopt.ZeroCopy(false)}
	for _, tc := range []struct{ name, in string }{
		{"hook", `{"S":"AAAAAAAA\n","M":{"kk\n":"vvvvvvvv\n"},"H":"boom"}`},
		{"hook-slice-elem", `{"S":"AAAAAAAA\n","HS":["a",1,"boom"]}`},
		{"text", `{"S":"AAAAAAAA\n","T":"boom"}`},
		{"hook-after-map", `{"S":"AAAAAAAA\n","KM":{"1":1},"HM":{"a":"x\n","b":"boom"}}`},
	} {
		v := new(ilhDoc)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s: expected panic", tc.name)
				}
			}()
			_ = p.Unmarshal([]byte(tc.in), v, opts...)
		}()
		before := ilhSnap(v)
		for range 5 {
			var w ilhDoc
			_ = p.Unmarshal([]byte(`{"S":"ZZZZZZZZ\n","M":{"ZZ\n":"ZZZZZZZZ\n"},"A":7}`), &w, opts...)
		}
		if after := ilhSnap(v); after != before {
			t.Errorf("%s: input %q\n panicked call left %s\n later calls turned it into %s", tc.name, tc.in, before, after)
		}
	}
}

// Reentrancy. A hook decodes with the same package entry points while the
// outer parse is parked mid-drain.

type ilhReHook struct {
	N   int
	Sub string
}

var ilhReDepth int

func (x *ilhReHook) UnmarshalJSON(b []byte) error {
	// The hook payload is a nested document for the outer type or a marker.
	s := string(b)
	switch {
	case s == `"inner-fail"`:
		var d ilhReDoc
		return Unmarshal([]byte(`{"HS":[1,"fail-me"],"N":"notanumber"}`), &d)
	case s == `"inner-panic"`:
		defer func() { _ = recover() }()
		var d ilhReDoc
		_ = Unmarshal([]byte(`{"HS":["boom-me"]}`), &d)
		return nil
	case s == `"inner-ok"`:
		var d ilhReDoc
		if err := Unmarshal([]byte(`{"N":5,"S":"inner\n","HS":[1,2,3],"M":{"a":"b"}}`), &d); err != nil {
			return err
		}
		x.Sub = fmt.Sprintf("%d/%s/%d/%v", d.N, d.S, len(d.HS), d.M)
		return nil
	case s == `"inner-deep"` && ilhReDepth < 4:
		ilhReDepth++
		defer func() { ilhReDepth-- }()
		var d ilhReDoc
		if err := Unmarshal([]byte(`{"HS":["inner-deep","inner-ok"],"M":{"d":"x\n"}}`), &d); err != nil {
			return err
		}
		x.Sub = fmt.Sprintf("deep%d", len(d.HS))
		return nil
	}
	if s == `"fail-me"` {
		return errors.New("ilh: re fail-me")
	}
	if s == `"boom-me"` {
		panic("ilh: re boom-me")
	}
	x.N++
	return nil
}

type ilhReDoc struct {
	N  int
	S  string
	HS []ilhReHook
	M  map[string]string
	MH map[string]ilhReHook
}

// The outer document is shaped like the inner one on purpose: both borrow
// from the same shape's parser pool.
func TestIlhReentrantHookSameShape(t *testing.T) {
	filler := strings.Repeat(`"x",`, 40)
	cases := []string{
		`{"S":"outer\n","HS":["inner-ok"],"M":{"o":"p\n"}}`,
		`{"S":"outer\n","HS":["inner-fail"],"M":{"o":"p\n"}}`,
		`{"S":"outer\n","HS":["inner-panic","inner-ok"],"M":{"o":"p\n"}}`,
		`{"S":"outer\n","HS":[` + filler + `"inner-ok",` + filler + `"inner-ok"],"M":{"o":"p\n"}}`,
		`{"S":"outer\n","HS":["inner-deep"],"MH":{"a":"inner-ok","b":"inner-deep"}}`,
		`{"S":"outer\n","MH":{"a":"inner-ok","b":"inner-fail"},"HS":["inner-ok"]}`,
	}
	for _, mode := range []ilhMode{ilhParserMode, ilhPoolMode} {
		dirty, _ := NewParser[ilhReDoc]()
		for round := range 3 {
			for _, in := range cases {
				for oi := range ilOptSets {
					want := func() ilOutcome {
						fp, _ := NewParser[ilhReDoc]()
						return ilhRun[ilhReDoc](ilhParserMode, fp, in, ilOptSets[oi], nil)
					}()
					got := ilhRun[ilhReDoc](mode, dirty, in, ilOptSets[oi], nil)
					if !ilSame(want, got, false) {
						t.Errorf("%s round %d opts#%d input %q\n fresh err=%s\n dirty err=%s\n %s",
							mode, round, oi, in, want.err, got.err, ilDiff(want, got))
					}
					// Absolute expectation on the clean outer results.
					if got.err == "" && strings.Contains(in, `"HS":["inner-ok"],"M"`) &&
						!strings.Contains(got.json, "5/inner\\n/3/map[a:b]") {
						t.Errorf("%s round %d opts#%d: inner result missing from outer destination: %s", mode, round, oi, got.json)
					}
				}
			}
		}
	}
}

// An interface-held pointee with a Value field gets an options set the shape
// itself would never run with.
type ilhValHolder struct {
	V value.Value
	N int
}

type ilhHolder interface{ holder() }

func (*ilhValHolder) holder() {}

func TestIlhIfaceSubParseValueSurvivesPadBufReuse(t *testing.T) {
	type outer struct {
		P ilhHolder
		K string
	}
	p, _ := NewParser[outer]()
	var keep []outer
	for round := range 4 {
		o := outer{P: new(ilhValHolder)}
		in := fmt.Sprintf(`{"P":{"V":{"key":"value-%d","arr":["a\n",%d]},"N":%d},"K":"k"}`, round, round, round)
		if err := p.Unmarshal([]byte(in), &o); err != nil {
			t.Fatal(err)
		}
		keep = append(keep, o)
		// Reuse the shape's pooled pad buffers with unrelated bytes.
		for range 3 {
			var h ilhValHolder
			_ = Unmarshal([]byte(strings.Repeat(` `, 8)+`{"V":{"key":"XXXXXXXXXXXXXXXXXXX","arr":["Z\n",9]},"N":9}`), &h)
			var q outer
			q.P = new(ilhValHolder)
			_ = Unmarshal([]byte(`{"P":{"V":{"key":"YYYYYYYYYYYYYYYYYYY","arr":["Z\n",9]},"N":9},"K":"yyyy"}`), &q)
		}
		runtime.GC()
	}
	for round, o := range keep {
		h := o.P.(*ilhValHolder)
		js, err := json.Marshal(h)
		want := fmt.Sprintf(`{"V":{"key":"value-%d","arr":["a\n",%d]},"N":%d}`, round, round, round)
		if err != nil || string(js) != want {
			t.Errorf("round %d: iface-held Value changed after later parses:\n want %s\n got  %s (err %v)", round, want, js, err)
		}
	}
}

// Hook types whose Go representation is a single pointer word are stored
// directly in an interface's data word. A value-receiver hook on such a type
// must receive the field's current contents, as encoding/json passes them.
type ilhPtrShaped struct{ p *int }

var ilhPtrShapedSeen []*int

func (x ilhPtrShaped) UnmarshalJSON(b []byte) error {
	ilhPtrShapedSeen = append(ilhPtrShapedSeen, x.p)
	return nil
}

type ilhMapShaped map[string]int

var ilhMapShapedNil []bool

func (m ilhMapShaped) UnmarshalJSON(b []byte) error {
	ilhMapShapedNil = append(ilhMapShapedNil, m == nil)
	return nil
}

func TestIlhValueReceiverPointerShapedHook(t *testing.T) {
	type doc struct {
		PS ilhPtrShaped
		MS ilhMapShaped
	}
	anchor := new(int)
	ilhPtrShapedSeen = nil
	ilhMapShapedNil = nil
	v := doc{PS: ilhPtrShaped{anchor}}
	if err := Unmarshal([]byte(`{"PS":1,"MS":{"a":1}}`), &v); err != nil {
		t.Fatal(err)
	}
	if len(ilhPtrShapedSeen) != 1 || ilhPtrShapedSeen[0] != anchor {
		t.Errorf("struct{p *int} value receiver: want receiver holding the preset pointer %p, got %v", anchor, ilhPtrShapedSeen)
	}
	if len(ilhMapShapedNil) != 1 || !ilhMapShapedNil[0] {
		t.Errorf("map value receiver on a nil field: want nil map receiver, got nil=%v", ilhMapShapedNil)
	}
}

// ilhSnapNoArena snapshots the destination without its arena-backed strings,
// so that corruption of slices and maps shows even where string corruption
// (see TestIlhPanicKeepsPublishedStrings) would mask it.
func ilhSnapNoArena(v *ilhDoc) string {
	c := *v
	c.S = ""
	c.M = nil
	return ilhSnap(&c)
}

// ilhBigDoc builds a document whose hook slice spans several staging flushes.
// hookAt places a payload at one element index (empty for none); tail is
// appended after the slice.
func ilhBigDoc(n, hookAt int, payload, tail string) string {
	var sb strings.Builder
	sb.WriteString(`{"S":"big\n","X":[`)
	for i := range 400 {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", i)
	}
	sb.WriteString(`],"M":{"k1\n":"v1\n","k2\n":"v2\n"},"HS":[`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		if i == hookAt {
			sb.WriteString(payload)
		} else {
			fmt.Fprintf(&sb, `{"i":%d}`, i)
		}
	}
	sb.WriteString(`]`)
	sb.WriteString(tail)
	sb.WriteString(`}`)
	return sb.String()
}

// Large panicking and failing documents interleaved with small and large good
// ones. Every destination is kept; none may change after the call returned.
func TestIlhRetainBigInterleaved(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	seq := []string{
		ilhBigDoc(600, -1, "", ""),
		ilhBigDoc(600, 450, `"boom"`, ""),
		ilhGood[0],
		ilhBigDoc(600, 450, `"fail"`, `,"KM":{"999":1}`),
		ilhBigDoc(600, -1, "", ",\"A\":"),
		ilhGood[1],
		ilhBigDoc(900, 899, `"boom"`, ""),
		ilhBigDoc(700, 1, `"boom"`, `,"A":}`),
		ilhGood[2],
		ilhBigDoc(600, -1, "", ""),
		ilhBigDoc(50, 49, `"boom"`, ""),
		ilhGood[0],
	}
	for _, mode := range []ilhMode{ilhParserMode, ilhPoolMode} {
		for _, zc := range []bool{false, true} {
			p, _ := NewParser[ilhDoc]()
			opts := []UnmarshalOption{vopt.ZeroCopy(zc)}
			var kept []ilhKept
			for round := range 3 {
				for i, in := range seq {
					v := new(ilhDoc)
					func() {
						defer func() { _ = recover() }()
						if mode == ilhPoolMode {
							_ = Unmarshal([]byte(in), v, opts...)
						} else {
							_ = p.Unmarshal([]byte(in), v, opts...)
						}
					}()
					kept = append(kept, ilhKept{v, ilhSnapNoArena(v), in, round*len(seq) + i})
					runtime.GC()
					for _, k := range kept[:len(kept)-1] {
						if cur := ilhSnapNoArena(k.v); cur != k.snap {
							t.Errorf("%s zerocopy=%v: result of step %d (input %.80q...) changed by step %d (input %.80q...)\n was %.200s\n now %.200s",
								mode, zc, k.step, k.in, round*len(seq)+i, in, k.snap, cur)
							return
						}
					}
				}
			}
		}
	}
}

// Stream handlers. A stream callback runs inside serveYield, between native
// runs, and may return an error, panic, break, or decode reentrantly. The
// pooled parser must serve the next call as a fresh one would.

type ilhEv struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ilhStreamDoc struct {
	Msg    string
	Events stream.Stream[ilhEv] `json:"events"`
	After  []int
	Tail   map[string]string
}

type ilhPlainDoc struct {
	Msg   string
	After []int
}

const ilhStreamModes = 7

// ilhStreamRun decodes in with the handler behavior hm and snapshots what the
// handler saw together with the destination.
func ilhStreamRun(mode ilhMode, p *Parser, in string, hm int, opts []UnmarshalOption) (out ilOutcome) {
	defer func() {
		if r := recover(); r != nil {
			out = ilOutcome{err: fmt.Sprintf("PANIC: %v", r)}
		}
	}()
	var seen []int
	var innerNote string
	var d ilhStreamDoc
	d.Events.OnRead(func(sc stream.Scope[ilhEv]) error {
		for it := range sc.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			id := it.Target().ID
			seen = append(seen, id)
			switch {
			case hm == 1 && id == 3:
				return errors.New("ilh: handler error")
			case hm == 2 && id == 3:
				panic("ilh: handler panic")
			case hm == 3 && id == 3:
				return sc.Break()
			case hm == 4 && id == 2:
				var inner ilhPlainDoc
				if err := Unmarshal([]byte(`{"Msg":"inner\n","After":[7,8]}`), &inner); err != nil {
					return err
				}
				innerNote = fmt.Sprint(inner)
			case hm == 5 && id == 2:
				var inner ilhStreamDoc
				var innerSeen []int
				inner.Events.OnRead(func(sc2 stream.Scope[ilhEv]) error {
					for it2 := range sc2.Iter() {
						if err := it2.Decode(); err != nil {
							return err
						}
						innerSeen = append(innerSeen, it2.Target().ID)
					}
					return nil
				})
				if err := Unmarshal([]byte(`{"Msg":"in\n","events":[{"id":10,"name":"x\n"},{"id":11}],"After":[1]}`), &inner); err != nil {
					return err
				}
				innerNote = fmt.Sprint(innerSeen, inner.Msg, inner.After)
			case hm == 6 && id == 2:
				func() {
					defer func() { _ = recover() }()
					var inner ilhStreamDoc
					inner.Events.OnRead(func(sc2 stream.Scope[ilhEv]) error { panic("ilh: inner panic") })
					_ = Unmarshal([]byte(`{"events":[{"id":1}]}`), &inner)
				}()
				var inner ilhPlainDoc
				_ = Unmarshal([]byte(`{"Msg":"after-panic\n"}`), &inner)
				innerNote = inner.Msg
			}
		}
		return nil
	})
	var err error
	if mode == ilhPoolMode {
		err = Unmarshal([]byte(in), &d, opts...)
	} else {
		err = p.Unmarshal([]byte(in), &d, opts...)
	}
	out.err = ilDescribeErr(err)
	out.json = fmt.Sprintf("seen=%v inner=%q msg=%q after=%v tail=%v", seen, innerNote, d.Msg, d.After, d.Tail)
	return out
}

func ilhStreamDocs() []string {
	var big strings.Builder
	big.WriteString(`{"Msg":"big\n","events":[`)
	for i := range 150 {
		if i > 0 {
			big.WriteByte(',')
		}
		fmt.Fprintf(&big, `{"id":%d,"name":"n%d\n"}`, i+1, i)
	}
	big.WriteString(`],"After":[1,2,3],"Tail":{"k":"v\n"}}`)
	return []string{
		`{"Msg":"m\n","events":[{"id":1,"name":"a\n"},{"id":2,"name":"b"},{"id":3},{"id":4,"name":"d\n"},{"id":5}],"After":[1,2,3],"Tail":{"k":"v\n"}}`,
		`{"events":[],"Msg":"after\n"}`,
		`{"After":[9],"events":[{"id":2},{"id":3},{"id":4}],"Tail":{"a":"b"}}`,
		big.String(),
	}
}

func ilhStreamSweep(t *testing.T, mode ilhMode) {
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	docs := ilhStreamDocs()
	var mutants []string
	for _, g := range docs[:3] {
		mutants = append(mutants, ilSweepMutants(g)...)
	}
	for p := 1; p < len(docs[3]); p += 97 {
		mutants = append(mutants, docs[3][:p], docs[3][:p]+"x"+docs[3][p:])
	}
	dirty, _ := NewParser[ilhStreamDoc]()
	var hist []string
	nbad := 0
	seen := map[string]bool{}
	check := func(in string, hm, oi int) {
		opts := ilOptSets[oi]
		hist = append(hist, fmt.Sprintf("handler#%d opts#%d %q", hm, oi, in))
		if len(hist) > 5 {
			hist = hist[1:]
		}
		got := ilhStreamRun(mode, dirty, in, hm, opts)
		fp, _ := NewParser[ilhStreamDoc]()
		want := ilhStreamRun(ilhParserMode, fp, in, hm, opts)
		exact := false
		for _, d := range docs {
			exact = exact || d == in
		}
		same := ilhSame(want, got, exact)
		// A failing parse reports what the handler saw before the failure,
		// which depends on batch timing; only successes compare contents.
		if same && want.err == "" && want.json != got.json {
			same = false
		}
		if same || nbad >= 6 {
			return
		}
		key := want.err + "|" + got.err
		if seen[key] {
			return
		}
		seen[key] = true
		nbad++
		t.Errorf("%s: history dependence\n last steps:\n   %s\n fresh: err=%s\n dirty: err=%s\n %s",
			mode, strings.Join(hist, "\n   "), want.err, got.err, ilDiff(want, got))
	}
	for i, m := range mutants {
		check(m, i%ilhStreamModes, i%len(ilOptSets))
		check(docs[i%len(docs)], (i+1)%ilhStreamModes, (i+2)%len(ilOptSets))
		if i%3 == 0 {
			check(docs[(i/3)%len(docs)], (i/3)%ilhStreamModes, 0)
		}
		if i%97 == 0 {
			runtime.GC()
		}
	}
}

func TestIlhStreamHandlersParser(t *testing.T) { ilhStreamSweep(t, ilhParserMode) }
func TestIlhStreamHandlersPool(t *testing.T)   { ilhStreamSweep(t, ilhPoolMode) }

// Differential: every hook placement against encoding/json for a matrix of
// JSON values. The hooks record the exact bytes they receive.

type ilhRec struct{ Raw string }

var errIlhRec = errors.New("ilh: rec fail")

func (x *ilhRec) UnmarshalJSON(b []byte) error {
	if string(b) == `"fail"` {
		return errIlhRec
	}
	x.Raw += "[" + string(b) + "]"
	return nil
}

type ilhRecEmb struct {
	ilhRec
	X int
}

type ilhBytesHook []byte

func (x *ilhBytesHook) UnmarshalJSON(b []byte) error {
	*x = append((*x)[:0], b...)
	return nil
}

type ilhStrText string

func (x *ilhStrText) UnmarshalText(b []byte) error {
	if string(b) == "fail" {
		return errIlhRec
	}
	*x = ilhStrText("T:" + string(b))
	return nil
}

type ilhIntText int

func (x *ilhIntText) UnmarshalText(b []byte) error {
	*x = ilhIntText(len(b))
	return nil
}

type ilhStructText struct{ V string }

func (x *ilhStructText) UnmarshalText(b []byte) error {
	x.V = "S:" + string(b)
	return nil
}

type ilhSliceHook []int

func (x *ilhSliceHook) UnmarshalJSON(b []byte) error {
	*x = append((*x)[:0], len(b))
	return nil
}

type ilhMapNamedHook map[string]int

func (x *ilhMapNamedHook) UnmarshalJSON(b []byte) error {
	*x = ilhMapNamedHook{string(b): 1}
	return nil
}

type ilhMatrix struct {
	V   ilhRec
	P   *ilhRec
	PP  **ilhRec
	A   [3]ilhRec
	S   []ilhRec
	SP  []*ilhRec
	M   map[string]ilhRec
	MP  map[string]*ilhRec
	E   ilhRecEmb
	BH  ilhBytesHook
	ST  ilhStrText
	STP *ilhStrText
	STS []ilhStrText
	STM map[string]ilhStrText
	IT  ilhIntText
	SX  ilhStructText
	SXS []ilhStructText
	SH  ilhSliceHook
	MH  ilhMapNamedHook
	IU  json.Unmarshaler
}

func TestIlhHookMatrixAgainstStd(t *testing.T) {
	fields := []string{"V", "P", "PP", "A", "S", "SP", "M", "MP", "E", "BH", "ST", "STP", "STS", "STM", "IT", "SX", "SXS", "SH", "MH", "IU"}
	values := []string{`null`, `1`, `-1.5e3`, `"s"`, `"fail"`, `""`, `true`, `{}`, `{"a":1}`, `[]`, `[1,"x",null]`, `["a","fail"]`, `[{"a":1},null]`, `{"k":"v","j":null}`, `"\u00e9\n"`}
	p, _ := NewParser[ilhMatrix]()
	bad := 0
	prep := func(m *ilhMatrix) { m.IU = new(ilhRec) }
	for _, f := range fields {
		for _, val := range values {
			in := fmt.Sprintf(`{%q:%s}`, f, val)
			var want, got ilhMatrix
			prep(&want)
			prep(&got)
			serr := json.Unmarshal([]byte(in), &want)
			verr := p.Unmarshal([]byte(in), &got)
			// A failing parse leaves a partial destination; only compare
			// results when both succeed, and the error identity otherwise.
			if (serr == nil) != (verr == nil) {
				if bad++; bad <= 12 {
					t.Errorf("input %s: std err=%v, vjson err=%v", in, serr, verr)
				}
				continue
			}
			if serr != nil {
				if errors.Is(serr, errIlhRec) != errors.Is(verr, errIlhRec) {
					if bad++; bad <= 12 {
						t.Errorf("input %s: hook error identity: std %v, vjson %v", in, serr, verr)
					}
				}
				continue
			}
			wj, _ := json.Marshal(want)
			gj, _ := json.Marshal(got)
			if string(wj) != string(gj) {
				if bad++; bad <= 12 {
					t.Errorf("input %s: results differ\n std   %s\n vjson %s", in, wj, gj)
				}
			}
		}
	}
}

// A Reader is user code too. One that panics or fails after delivering part of
// a document must leave the strings the partial parse published untouched by
// later calls on the same Parser.

type ilhChunkReader struct {
	chunks []string
	i      int
	end    error // returned (or panicked) once the chunks are exhausted
	panics bool
}

func (r *ilhChunkReader) Read(b []byte) (int, error) {
	if r.i >= len(r.chunks) {
		if r.panics {
			panic("ilh: reader panic")
		}
		return 0, r.end
	}
	n := copy(b, r.chunks[r.i])
	r.chunks[r.i] = r.chunks[r.i][n:]
	if r.chunks[r.i] == "" {
		r.i++
	}
	return n, nil
}

func TestIlhReaderFailureKeepsPublishedStrings(t *testing.T) {
	type doc struct {
		S  string
		M  map[string]string
		X  []string
		HS []ilhHook
	}
	first := `{"S":"AAAAAAAA\n","M":{"kk\n":"vvvvvvvv\n"},"X":["xxxx\n","yyyy\n"],`
	for _, tc := range []struct {
		name   string
		panics bool
		end    error
	}{
		{"reader-panics", true, nil},
		{"reader-errors", false, errors.New("ilh: reader failed")},
		{"reader-eof-midvalue", false, io.EOF},
	} {
		p, _ := NewParser[doc]()
		v := new(doc)
		func() {
			defer func() { _ = recover() }()
			_ = p.UnmarshalFeed(&ilhChunkReader{chunks: []string{first}, end: tc.end, panics: tc.panics}, v)
		}()
		before, _ := json.Marshal(v)
		for range 4 {
			var w doc
			_ = p.UnmarshalFeed(strings.NewReader(`{"S":"ZZZZZZZZ\n","M":{"ZZ\n":"ZZZZZZZZ\n"},"X":["ZZZZ\n"]}`), &w)
			var w2 doc
			_ = p.Unmarshal([]byte(`{"S":"ZZZZZZZZ\n","M":{"ZZ\n":"ZZZZZZZZ\n"},"X":["ZZZZ\n"]}`), &w2, vopt.ZeroCopy(false))
		}
		if after, _ := json.Marshal(v); string(after) != string(before) {
			t.Errorf("%s: partial result changed by later calls\n before %s\n after  %s", tc.name, before, after)
		}
	}
}

// A non-empty interface holding a pointer to the root's own type: the inner
// decode borrows a parser of the same shape while the outer one is in use.
type ilhNoder interface{ node() }

type ilhNode struct {
	V    int
	S    string
	Next ilhNoder
	Kids []ilhNode
}

func (*ilhNode) node() {}

func ilhNodePrep(n *ilhNode) {
	n.Next = &ilhNode{Next: &ilhNode{Next: &ilhNode{}}}
}

var ilhNodeGood = []string{
	`{"V":1,"S":"a\n","Next":{"V":2,"S":"b\n","Next":{"V":3,"Next":{"V":4,"S":"d\n"}}}}`,
	`{"V":1,"Next":null}`,
	`{"V":1,"Kids":[{"V":2},{"V":3,"S":"k\n"}],"Next":{"V":9,"Kids":[{"V":8}]}}`,
}

func TestIlhSelfTypedIfaceMatchesStd(t *testing.T) {
	for _, in := range ilhNodeGood {
		var want, got ilhNode
		ilhNodePrep(&want)
		ilhNodePrep(&got)
		if err := json.Unmarshal([]byte(in), &want); err != nil {
			t.Fatalf("std: %v", err)
		}
		if err := Unmarshal([]byte(in), &got); err != nil {
			t.Errorf("input %s: %v", in, err)
			continue
		}
		wj, _ := json.Marshal(want)
		gj, _ := json.Marshal(got)
		if string(wj) != string(gj) {
			t.Errorf("input %s\n std   %s\n vjson %s", in, wj, gj)
		}
	}
}

func TestIlhSweepSelfTypedIfaceParser(t *testing.T) {
	ilhSweepKeep[ilhNode](t, ilhParserMode, ilhNodeGood, ilhNodePrep)
}
func TestIlhSweepSelfTypedIfacePool(t *testing.T) {
	ilhSweepKeep[ilhNode](t, ilhPoolMode, ilhNodeGood, ilhNodePrep)
}

// A type error inside a pointer held by a non-empty interface carries the
// document offset and the field path through the interface, as encoding/json
// reports it, not the coordinates of the private sub-decode.
func TestIlhIfaceSubDecodeErrorProvenance(t *testing.T) {
	for _, in := range []string{
		`{"V":1,"Next":{"V":"x"}}`,
		`{"V":1,"S":"padding padding","Next":{"V":2,"Next":{"S":5}}}`,
		`{"V":1,"Next":[1]}`,
	} {
		var a, b ilhNode
		ilhNodePrep(&a)
		ilhNodePrep(&b)
		serr := json.Unmarshal([]byte(in), &a)
		verr := Unmarshal([]byte(in), &b)
		var su *json.UnmarshalTypeError
		var vu *UnmarshalTypeError
		if !errors.As(serr, &su) || !errors.As(verr, &vu) {
			t.Errorf("input %s: std err=%v, vjson err=%v", in, serr, verr)
			continue
		}
		if su.Offset != vu.Offset || su.Field != vu.Field || su.Struct != vu.Struct {
			t.Errorf("input %s\n std   offset=%d field=%q struct=%q\n vjson offset=%d field=%q struct=%q",
				in, su.Offset, su.Field, su.Struct, vu.Offset, vu.Field, vu.Struct)
		}
	}
}

// Error precedence: a walk error beats a hook failure, which beats a map-key
// failure, whatever flush timing the parser history produced.
func TestIlhErrorPrecedenceIsHistoryFree(t *testing.T) {
	bigHooks := func(failAt int, tail string) string {
		var sb strings.Builder
		sb.WriteString(`{"KM":{"999":1},"HS":[`)
		for i := range 700 {
			if i > 0 {
				sb.WriteByte(',')
			}
			if i == failAt {
				sb.WriteString(`"fail"`)
			} else {
				fmt.Fprintf(&sb, `{"i":%d}`, i)
			}
		}
		sb.WriteString(`]`)
		sb.WriteString(tail)
		sb.WriteString(`}`)
		return sb.String()
	}
	cases := []struct{ in, class string }{
		{`{"KM":{"999":1},"HS":["fail"]}`, "hook"},
		{`{"HS":["fail"],"KM":{"999":1}}`, "hook"},
		{`{"KM":{"999":1}}`, "key"},
		{`{"HS":["fail"],"A":"x"}`, "walk"},
		{`{"A":"x","HS":["fail"]}`, "walk"},
		{`{"KM":{"999":1},"A":"x"}`, "walk"},
		{`{"HS":["fail"],"X":[1,2`, "syntax"},
		{`{"KM":{"999":1},"X":[1,2`, "syntax"},
		{bigHooks(5, ``), "hook"},
		{bigHooks(650, ``), "hook"},
		{bigHooks(5, `,"A":"x"`), "walk"},
		{bigHooks(650, `,"X":[1,2`), "syntax"},
	}
	classify := func(err error) string {
		var ute *UnmarshalTypeError
		var se *SyntaxError
		switch {
		case err == nil:
			return "none"
		case errors.As(err, &se):
			return "syntax"
		case errors.As(err, &ute):
			if ute.Field == "A" || ute.Type.Kind() == reflect.Int {
				return "walk"
			}
			return "key"
		case err.Error() == "ilh: hook fail":
			return "hook"
		}
		return "key"
	}
	noise := append(append(append([]string(nil), ilhGood...), ilhPanics...), ilhFails...)
	p, _ := NewParser[ilhDoc]()
	rng := rand.New(rand.NewSource(3))
	for round := range 400 {
		c := cases[rng.Intn(len(cases))]
		opts := ilOptSets[rng.Intn(len(ilOptSets))]
		for k := rng.Intn(4); k > 0; k-- {
			_ = ilhRun[ilhDoc](ilhParserMode, p, noise[rng.Intn(len(noise))], ilOptSets[rng.Intn(len(ilOptSets))], nil)
			_ = ilhRun[ilhDoc](ilhPoolMode, nil, ilMutants(noise[rng.Intn(len(noise))])[rng.Intn(80)], nil, nil)
		}
		var d ilhDoc
		err := p.Unmarshal([]byte(c.in), &d, opts...)
		var d2 ilhDoc
		err2 := Unmarshal([]byte(c.in), &d2, opts...)
		if got := classify(err); got != c.class {
			t.Fatalf("round %d parser: input %.100q opts %v: want %s error, got %s (%v)", round, c.in, opts, c.class, got, err)
		}
		if got := classify(err2); got != c.class {
			t.Fatalf("round %d pool: input %.100q: want %s error, got %s (%v)", round, c.in, c.class, got, err2)
		}
	}
}
