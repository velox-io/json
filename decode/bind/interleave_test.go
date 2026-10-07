package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vopt"
)

// History independence. A Parser is reusable state: machine, allocator, key
// memo, staged drains, arenas. Whatever one call leaves behind, the next call
// must behave exactly as it would on a fresh Parser. The sweeps below run one
// long-lived "dirty" Parser through failing mutants of valid documents,
// interleaved with valid documents, and compare every outcome (error identity
// and destination contents) against a fresh Parser run on the same input.

type ilHook struct{ raw string }

func (x *ilHook) UnmarshalJSON(b []byte) error {
	if string(b) == `"fail"` {
		return errors.New("ilHook: fail")
	}
	x.raw = "H:" + string(b)
	return nil
}

type ilText struct{ v string }

func (x *ilText) UnmarshalText(b []byte) error {
	if string(b) == "bad" {
		return errors.New("ilText: bad")
	}
	x.v = "T:" + string(b)
	return nil
}

type ilInner struct {
	B int
	C string
	D []float64
}

type IlEmbed struct {
	E1 int
	E2 string
}

type ilAll struct {
	*IlEmbed
	A   int
	I8  int8
	U   uint
	F   float32
	G   float64
	S   string
	T   bool
	X   []int
	Y   [2]int
	M   map[string]int
	MI  map[int]string
	MS  map[string]ilInner
	MP  map[string]*ilInner
	N   ilInner
	P   *int
	PP  **ilInner
	PS  []*int
	SS  []string
	SI  []ilInner
	Any any
	AL  []any
	Num json.Number
	Raw json.RawMessage
	H   ilHook
	HP  *ilHook
	HM  map[string]ilHook
	HS  []ilHook
	Tx  ilText
	TxS []ilText
	By  []byte
	Q   int     `json:",string"`
	QS  string  `json:",string"`
	QF  float64 `json:",string"`
	Ren int     `json:"renamed"`
}

var ilAllGood = []string{
	`{"A":1,"I8":-5,"U":7,"F":1.5,"G":-2.25e3,"S":"hi\n\u00e9\ud83d\ude00","T":true}`,
	`{"X":[1,2,3],"Y":[4,5],"M":{"a":1,"b":2},"MI":{"1":"x","-2":"y"},"N":{"B":9,"C":"c","D":[1.5,2]}}`,
	`{"P":5,"PP":{"B":1},"PS":[1,null,3],"Any":{"k":[1,"s",true,null,{"z":1.5}]},"AL":[1,[2],{}]}`,
	`{"Num":12.5e3,"Raw":{"a": [1, 2]} ,"H":{"x":1},"HP":[1],"HM":{"a":"v","b":2},"HS":[1,"x"],"Tx":"abc","TxS":["a","b"],"By":"aGVsbG8="}`,
	`{"Q":"12","QS":"\"q\\n\"","QF":"1.5","E1":3,"E2":"e","renamed":4}`,
	`{"MS":{"k":{"B":1,"C":"x"},"j":{"D":[]}},"MP":{"p":{"B":2},"q":null},"SS":["a","bb","ccc"],"SI":[{"B":1},{"C":"z","D":[1]}]}`,
}

type ilVal struct {
	V value.Value
	N int
	S []string
}

var ilValGood = []string{
	`{"V":{"a":[1,2,{"b":null}],"c":"s"},"N":1,"S":["x","y"]}`,
	`{"V":[1,2,3.5e10,"str",true,false,null],"N":2}`,
	`{"N":3,"V":"plain","S":[]}`,
	`{"V":{"deep":{"deeper":{"deepest":[[],[[]],{}]}}}}`,
}

type ilItem struct {
	ID   int               `json:"id"`
	Name string            `json:"name"`
	Tags []string          `json:"tags"`
	Meta map[string]string `json:"meta"`
	Next *ilItem           `json:"next"`
}

var ilItemsGood = []string{
	`[{"id":1,"name":"a","tags":["x","y"],"meta":{"k":"v"},"next":{"id":2}},{"id":3,"tags":[]}]`,
	`[]`,
	`[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5},{"id":6},{"id":7},{"id":8},{"id":9}]`,
}

var ilMapGood = []string{
	`{"a":[1,2,3],"b":[],"c":[4]}`,
	`{}`,
	`{"k1":[1],"k2":[2],"k3":[3],"k4":[4],"k5":[5],"k6":[6],"k7":[7],"k8":[8],"k9":[9]}`,
}

var ilAnyGood = []string{
	`{"a":[1,"s",{"b":null}],"c":1.5}`,
	`[1,[2,[3,[4]]]]`,
	`"str"`, `12`, `null`, `true`,
}

func ilMutants(doc string) []string {
	out, _ := ilMutantsPick(doc, ilPickAll)
	return out
}

func ilPickAll(int) bool { return true }

// ilMutantsPick builds the mutants of doc whose index in ilMutants order
// keep accepts, and returns them with the total count. A document's mutant
// set is quadratic in its length, so a sample of a large one must not
// materialize the rest.
func ilMutantsPick(doc string, keep func(int) bool) (out []string, n int) {
	const rep = "{}[]\",:\\ 0-.etnx\xff\xc3\x00\x80"
	pick := func(build func() string) {
		if keep(n) {
			out = append(out, build())
		}
		n++
	}
	for p := range len(doc) {
		pick(func() string { return doc[:p] })
		pick(func() string { return doc[:p] + doc[p+1:] })
		for k := range 6 {
			c := rep[(p*7+k*3)%len(rep)]
			if c == doc[p] {
				continue
			}
			pick(func() string { return doc[:p] + string(c) + doc[p+1:] })
		}
		ins := rep[(p*5)%len(rep)]
		pick(func() string { return doc[:p] + string(ins) + doc[p:] })
	}
	pick(func() string { return doc + "x" })
	pick(func() string { return doc + "{" })
	pick(func() string { return doc + " 1" })
	return out, n
}

// ilSweepEvery thins the interleave sweeps where every parse costs several
// times what it costs in a plain build: under the race detector, and under
// the self-checking collector scripts/gc-stress.sh runs. The plain legs of
// the test matrix keep the full sweeps.
var ilSweepEvery = func() int {
	if raceEnabled || strings.Contains(os.Getenv("GODEBUG"), "gccheckmark=1") {
		return 8
	}
	return 1
}()

// ilSweepMutants is the part of ilMutants(doc) a sweep iterates: every
// ilSweepEvery-th mutant.
func ilSweepMutants(doc string) []string {
	out, _ := ilMutantsPick(doc, func(i int) bool { return i%ilSweepEvery == 0 })
	return out
}

func ilDescribeErr(err error) string {
	if err == nil {
		return ""
	}
	var ute *UnmarshalTypeError
	if errors.As(err, &ute) {
		return fmt.Sprintf("UTE{%s %v %d struct=%q field=%q}", ute.Value, ute.Type, ute.Offset, ute.Struct, ute.Field)
	}
	var se *SyntaxError
	if errors.As(err, &se) {
		return fmt.Sprintf("SE{%s %d eof=%v}", se.Error(), se.Offset, errors.Is(err, io.ErrUnexpectedEOF))
	}
	return fmt.Sprintf("%T:%v", err, err)
}

type ilOutcome struct {
	err  string
	json string
	val  any
}

// ilRun decodes in into a zero T through p and snapshots the result. The
// snapshot walks the whole destination through encoding/json, which touches
// every pointer, map, and string a failed parse may have left half built.
func ilRun[T any](p *Parser, in string, opts []UnmarshalOption) (out ilOutcome) {
	defer func() {
		if r := recover(); r != nil {
			out = ilOutcome{err: fmt.Sprintf("PANIC: %v", r)}
		}
	}()
	v := new(T)
	err := p.Unmarshal([]byte(in), v, opts...)
	out.err = ilDescribeErr(err)
	js, jerr := json.Marshal(v)
	out.json = string(js)
	if jerr != nil {
		out.json += "|marshal-err:" + jerr.Error()
	}
	out.val = v
	return out
}

func ilDiff(a, b ilOutcome) string {
	if a.json != b.json {
		i := 0
		for i < len(a.json) && i < len(b.json) && a.json[i] == b.json[i] {
			i++
		}
		lo := max(0, i-60)
		return fmt.Sprintf("json differs at %d:\n   fresh ...%.140s\n   dirty ...%.140s", i, a.json[lo:], b.json[lo:])
	}
	return "json equal; DeepEqual differs"
}

// ilStrictFail makes the sweeps compare destination contents after a failed
// parse too. Off by default: a failed parse leaves a partial destination, and
// which deferred hook or raw records ran before the failure depends on
// allocator history (see TestInterleaveHookPartialDependsOnHistory).
var ilStrictFail = false

func ilSame(a, b ilOutcome, deep bool) bool {
	if a.err != b.err {
		return false
	}
	if a.err != "" && !ilStrictFail {
		return true
	}
	if a.json != b.json {
		return false
	}
	return !deep || reflect.DeepEqual(a.val, b.val)
}

var ilOptSets = [][]UnmarshalOption{
	nil,
	{vopt.UseNumber(true)},
	{vopt.RejectUnknownMembers(true)},
	{vopt.AllowInvalidUTF8(false)},
	{vopt.SkipLenient(true)},
	{vopt.ZeroCopy(false)},
}

type ilStep struct {
	in string
	oi int
}

type ilFail struct {
	chain     []ilStep // minimal replay that reproduces, the failing step last
	want, got ilOutcome
}

// ilMinimize shrinks a failing history to a short suffix that still diverges
// from the fresh outcome when replayed on a new Parser.
func ilMinimize[T any](hist []ilStep, deep bool) []ilStep {
	last := hist[len(hist)-1]
	diverges := func(pre []ilStep) bool {
		p, _ := NewParser[T]()
		for _, s := range pre {
			ilRun[T](p, s.in, ilOptSets[s.oi])
		}
		got := ilRun[T](p, last.in, ilOptSets[last.oi])
		f, _ := NewParser[T]()
		return !ilSame(ilRun[T](f, last.in, ilOptSets[last.oi]), got, deep)
	}
	pre := hist[:len(hist)-1]
	if len(pre) > 64 {
		pre = pre[len(pre)-64:]
	}
	if !diverges(pre) {
		return nil // depends on state beyond the replayed inputs (GC, pool)
	}
	// Shrink the suffix, then drop single steps while the divergence persists.
	for k := 1; k < len(pre); k *= 2 {
		if diverges(pre[len(pre)-k:]) {
			pre = pre[len(pre)-k:]
			break
		}
	}
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(pre); i++ {
			cand := append(append([]ilStep(nil), pre[:i]...), pre[i+1:]...)
			if diverges(cand) {
				pre = cand
				changed = true
				i--
			}
		}
	}
	return append(append([]ilStep(nil), pre...), last)
}

// ilSweep runs the dirty-versus-fresh comparison for one root type. deep adds
// reflect.DeepEqual on top of the marshaled comparison; it is off for types
// whose destinations embed arena-relative descriptors.
func ilSweep[T any](t *testing.T, good []string, deep bool) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	var mutants []string
	for _, g := range good {
		mutants = append(mutants, ilSweepMutants(g)...)
	}
	dirty, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	fresh := func(in string, opts []UnmarshalOption) ilOutcome {
		p, _ := NewParser[T]()
		return ilRun[T](p, in, opts)
	}
	var fails []ilFail
	seen := map[string]bool{}
	var hist []ilStep
	check := func(in string, oi int) {
		opts := ilOptSets[oi]
		hist = append(hist, ilStep{in, oi})
		if len(hist) > 400 {
			hist = hist[len(hist)-400:]
		}
		got := ilRun[T](dirty, in, opts)
		want := fresh(in, opts)
		if !ilSame(want, got, deep) {
			key := want.err + "|" + got.err
			if seen[key] {
				return
			}
			seen[key] = true
			fails = append(fails, ilFail{ilMinimize[T](hist, deep), want, got})
		}
	}
	for i, m := range mutants {
		if len(fails) >= 6 {
			break
		}
		check(m, i%len(ilOptSets))
		check(good[i%len(good)], (i+1)%len(ilOptSets))
		if i%3 == 0 {
			check(good[(i*7+1)%len(good)], (i+2)%len(ilOptSets))
		}
		if i%97 == 0 {
			runtime.GC()
		}
	}
	for _, f := range fails {
		var sb strings.Builder
		for i, s := range f.chain {
			fmt.Fprintf(&sb, "\n   step%d opts#%d %q", i, s.oi, s.in)
		}
		if f.chain == nil {
			sb.WriteString("\n   (not reproducible by replay: GC- or pool-dependent)")
		}
		t.Errorf("history dependence, minimal chain:%s\n fresh: err=%s\n dirty: err=%s\n %s",
			sb.String(), f.want.err, f.got.err, ilDiff(f.want, f.got))
	}
}

// ilPlain has no deferred records (hooks, raw spans), so its destination after
// a failed parse is compared too.
type ilPlain struct {
	*IlEmbed
	A   int
	I8  int8
	U   uint
	F   float32
	G   float64
	S   string
	T   bool
	X   []int
	Y   [2]int
	M   map[string]int
	MI  map[int]string
	MS  map[string]ilInner
	MP  map[string]*ilInner
	N   ilInner
	P   *int
	PP  **ilInner
	PS  []*int
	SS  []string
	SI  []ilInner
	Any any
	AL  []any
	Num json.Number
	By  []byte
	Q   int     `json:",string"`
	QS  string  `json:",string"`
	QF  float64 `json:",string"`
	Ren int     `json:"renamed"`
}

var ilPlainGood = []string{ilAllGood[0], ilAllGood[1], ilAllGood[2],
	`{"Num":12.5e3,"By":"aGVsbG8=","Q":"12","QS":"\"q\\n\"","QF":"1.5","E1":3,"E2":"e","renamed":4}`, ilAllGood[5]}

func TestInterleaveSweepPlainStrict(t *testing.T) {
	ilStrictFail = true
	defer func() { ilStrictFail = false }()
	ilSweep[ilPlain](t, ilPlainGood, true)
}

func TestInterleaveSweepAll(t *testing.T)   { ilSweep[ilAll](t, ilAllGood, true) }
func TestInterleaveSweepValue(t *testing.T) { ilSweep[ilVal](t, ilValGood, false) }
func TestInterleaveSweepItems(t *testing.T) { ilSweep[[]ilItem](t, ilItemsGood, true) }
func TestInterleaveSweepMap(t *testing.T)   { ilSweep[map[string][]int](t, ilMapGood, true) }
func TestInterleaveSweepAny(t *testing.T)   { ilSweep[any](t, ilAnyGood, true) }

func TestInterleaveSweepVariant(t *testing.T) {
	ilSweep[variantEnvelopeSibling](t, siblingVariantInputs, false)
	ilSweep[variantEnvelopeNonStruct](t, nonStructVariantInputs, false)
	ilSweep[variantEnvelopePtrMap](t, ptrMapVariantInputs, false)
	ilSweep[variantEnvelopeOuter](t, outerVariantInputs, false)
}

func TestInterleaveSweepKindof(t *testing.T) {
	ilSweep[kindofEnvelopeMixed](t, kindofInputs, false)
	ilSweep[kindofEnvelopePointer](t, []string{`{"data":{"name":"a","role":"b"}}`, `{"data":null}`}, false)
}

// ilBigShape is a destination whose parse crosses every refill and flush
// boundary: slice growth, slot block refills, map flushes, string arena use.
type ilBigShape struct {
	X  []int
	SI []ilInner
	M  map[string]int
	MS map[string]ilInner
	SS []string
	PS []*ilInner
	TS []ilText
}

func ilBigShapeDoc(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"X":[`)
	for i := 0; i < n*8; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", i)
	}
	sb.WriteString(`],"SI":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"B":%d,"C":"c%d","D":[1,2]}`, i, i)
	}
	sb.WriteString(`],"M":{`)
	for i := 0; i < n*2; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%d":%d`, i, i)
	}
	sb.WriteString(`},"MS":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"s%d":{"B":%d}`, i, i)
	}
	sb.WriteString(`},"SS":[`)
	for i := 0; i < n*2; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"str%d"`, i)
	}
	sb.WriteString(`],"PS":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"B":%d}`, i)
	}
	sb.WriteString(`],"TS":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"t%d"`, i)
	}
	sb.WriteString(`]}`)
	return sb.String()
}

// TestInterleaveSweepBig mutates every byte position of documents sized to
// cross refill and flush boundaries, so each failure lands right after some
// resume point, then parses a good document on the same Parser.
func TestInterleaveSweepBig(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	for _, n := range []int{5, 14, 40} {
		doc := ilBigShapeDoc(n)
		dirty, _ := NewParser[ilBigShape]()
		var bad []string
		step := ilSweepEvery
		if len(doc) > 4000 {
			step *= 3
		}
		for p := 1; p < len(doc) && len(bad) < 6; p += step {
			for k, m := range []string{doc[:p], doc[:p] + "x" + doc[p:], doc[:p] + `"` + doc[p+1:], doc[:p] + `[` + doc[p+1:]} {
				opts := ilOptSets[(p+k)%len(ilOptSets)]
				got := ilRun[ilBigShape](dirty, m, opts)
				pf, _ := NewParser[ilBigShape]()
				want := ilRun[ilBigShape](pf, m, opts)
				if !ilSame(want, got, true) {
					bad = append(bad, fmt.Sprintf("n=%d mutant@%d/%d: fresh=%s dirty=%s\n%s", n, p, k, want.err, got.err, ilDiff(want, got)))
				}
				got = ilRun[ilBigShape](dirty, doc, nil)
				if p%7 == 0 || len(bad) > 0 {
					pf, _ = NewParser[ilBigShape]()
					want = ilRun[ilBigShape](pf, doc, nil)
					if !ilSame(want, got, true) {
						bad = append(bad, fmt.Sprintf("n=%d good doc after mutant@%d/%d (err %s): fresh=%s dirty=%s\n%s", n, p, k, got.err, want.err, got.err, ilDiff(want, got)))
					}
				}
			}
		}
		for _, b := range bad {
			t.Error(b)
		}
	}
}

// ilValueSpans returns the [start,end) span of every JSON value in a
// well-formed doc, children before their parent.
func ilValueSpans(doc string) [][2]int {
	var spans [][2]int
	i := 0
	skipWS := func() {
		for i < len(doc) && (doc[i] == ' ' || doc[i] == '\n' || doc[i] == '\t' || doc[i] == '\r') {
			i++
		}
	}
	str := func() {
		i++
		for doc[i] != '"' {
			if doc[i] == '\\' {
				i++
			}
			i++
		}
		i++
	}
	var val func()
	val = func() {
		skipWS()
		st := i
		switch doc[i] {
		case '{':
			i++
			skipWS()
			for doc[i] != '}' {
				skipWS()
				str()
				skipWS()
				i++ // the colon
				val()
				skipWS()
				if doc[i] == ',' {
					i++
				}
			}
			i++
		case '[':
			i++
			skipWS()
			for doc[i] != ']' {
				val()
				skipWS()
				if doc[i] == ',' {
					i++
				}
			}
			i++
		case '"':
			str()
		default:
			for i < len(doc) && !strings.ContainsRune(",}] \n\t\r", rune(doc[i])) {
				i++
			}
		}
		spans = append(spans, [2]int{st, i})
	}
	val()
	return spans
}

var ilSwapVals = []string{`"x"`, `7`, `true`, `null`, `[]`, `{}`, `-1.5`, `1e999`, `[1,"a"]`, `{"B":"x","a":[1]}`, `""`, `"7"`}

// ilTypeMutants replaces each value of a well-formed doc by values of other
// kinds, producing well-formed documents that mismatch the destination.
func ilTypeMutants(doc string) []string {
	out, _ := ilTypeMutantsPick(doc, ilPickAll)
	return out
}

// ilTypeMutantsPick is ilMutantsPick for the ilTypeMutants order.
func ilTypeMutantsPick(doc string, keep func(int) bool) (out []string, n int) {
	for _, sp := range ilValueSpans(doc) {
		for _, sw := range ilSwapVals {
			if doc[sp[0]:sp[1]] == sw {
				continue
			}
			if keep(n) {
				out = append(out, doc[:sp[0]]+sw+doc[sp[1]:])
			}
			n++
		}
	}
	return out, n
}

type ilDeepNode struct {
	V    int         `json:"v"`
	Next *ilDeepNode `json:"next"`
	Kids []ilDeepNode
	M    map[string]*ilDeepNode
	A    any
}

func ilNest(open, mid, close string, depth int) string {
	return strings.Repeat(open, depth) + mid + strings.Repeat(close, depth)
}

// Depth overflow fails from deep inside nested frames. Each overflowing
// document is followed by documents just under the limit and by shallow ones on
// the same Parser; all outcomes must match a fresh Parser.
func TestInterleaveSweepDepth(t *testing.T) {
	depths := []int{1, 2, 17, 64, 100, 128, 200, 250, 253, 254, 255, 256, 257, 300, 600, 5000}
	nodeChain := func(d int) string {
		return strings.Repeat(`{"v":1,"next":`, d) + `null` + strings.Repeat(`}`, d)
	}
	kidsChain := func(d int) string {
		return strings.Repeat(`{"Kids":[`, d) + `{"v":2}` + strings.Repeat(`]}`, d)
	}
	mapChain := func(d int) string {
		return strings.Repeat(`{"M":{"k":`, d) + `{"v":3}` + strings.Repeat(`}}`, d)
	}
	anyChain := func(d int) string {
		return strings.Repeat(`{"A":`, d) + `1` + strings.Repeat(`}`, d)
	}
	var docs []string
	for _, d := range depths {
		docs = append(docs, nodeChain(d), kidsChain(d), mapChain(d), anyChain(d))
	}
	// The ordered list interleaves limit-crossing documents with shallow ones.
	small := []string{nodeChain(3), kidsChain(3), mapChain(3), anyChain(3)}
	dirty, _ := NewParser[ilDeepNode]()
	var bad []string
	for i, d := range docs {
		for k, in := range []string{d, small[i%len(small)], d, nodeChain(250), small[(i+1)%len(small)]} {
			opts := ilOptSets[(i+k)%len(ilOptSets)]
			got := ilRun[ilDeepNode](dirty, in, opts)
			p, _ := NewParser[ilDeepNode]()
			want := ilRun[ilDeepNode](p, in, opts)
			if !ilSame(want, got, true) {
				bad = append(bad, fmt.Sprintf("doc #%d step %d (len %d): fresh=%s dirty=%s\n%s", i, k, len(in), want.err, got.err, ilDiff(want, got)))
			}
		}
	}
	for _, b := range bad[:min(len(bad), 6)] {
		t.Error(b)
	}
	// Generic roots too.
	for _, d := range depths {
		for mi, mk := range []func(int) string{
			func(d int) string { return ilNest("[", "1", "]", d) },
			func(d int) string { return ilNest(`{"a":`, "1", "}", d) },
			func(d int) string { return ilNest(`[{"a":`, "null", "}]", d) },
		} {
			in := mk(d)
			eff := d
			if mi == 2 {
				eff = 2 * d
			}
			dirtyAny, _ := NewParser[any]()
			ilRun[any](dirtyAny, in, nil)
			good := `{"a":[1,2,{"b":null}]}`
			got := ilRun[any](dirtyAny, good, nil)
			p, _ := NewParser[any]()
			want := ilRun[any](p, good, nil)
			if !ilSame(want, got, true) {
				t.Errorf("any root: good doc after depth %d failure differs: fresh=%s dirty=%s", d, want.err, got.err)
			}
			// The depth verdict itself: std accepts up to 10000, vjson caps at 255.
			var s any
			p2, _ := NewParser[any]()
			err := p2.Unmarshal([]byte(in), &s)
			if eff <= 250 && err != nil {
				t.Errorf("any root depth %d (%.20q...) rejected: %v", d, in, err)
			}
			if eff >= 500 && err == nil {
				t.Errorf("any root depth %d accepted past the documented limit", d)
			}
		}
	}
}
