package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"testing"

	vjson "github.com/velox-io/json"
)

// Public API under interleaved failure. Failing and succeeding calls alternate
// on the pooled parsers behind Unmarshal, UnmarshalPadded, UnmarshalValue and
// NewDecoder. Whatever a failure leaves in a pooled parser, the next call must
// match what it would produce alone, options must not stick, and values
// handed out earlier must stay intact.

type ilpInner struct {
	B int
	C string
	D []float64
}

type ilpDoc struct {
	A   int
	S   string
	X   []int
	M   map[string]int
	MS  map[string]string
	N   ilpInner
	SI  []ilpInner
	P   *int
	Any any
	AL  []any
	Num vjson.Number
	Raw vjson.RawMessage
	By  []byte
}

var ilpDocGood = []string{
	`{"A":1,"S":"hi\n\u00e9\ud83d\ude00","X":[1,2,3],"M":{"a":1,"b":2},"MS":{"k\n":"v\n"},"N":{"B":9,"C":"c\n","D":[1.5,2]}}`,
	`{"SI":[{"B":1,"C":"x\n"},{"D":[1,2,3]}],"P":5,"Any":{"k":[1,"s\n",true,null,{"z":1.5}]},"AL":[1,[2],{}]}`,
	`{"Num":12.5e3,"Raw":{"a": [1, 2]} ,"By":"aGVsbG8=","S":"plain"}`,
}

type ilpItem struct {
	ID   int               `json:"id"`
	Name string            `json:"name"`
	Tags []string          `json:"tags"`
	Meta map[string]string `json:"meta"`
	Next *ilpItem          `json:"next"`
}

var ilpItemsGood = []string{
	`[{"id":1,"name":"a\n","tags":["x","y"],"meta":{"k":"v\n"},"next":{"id":2}},{"id":3,"tags":[]}]`,
	`[]`,
	`[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5},{"id":6},{"id":7},{"id":8},{"id":9}]`,
}

var ilpMapGood = []string{`{"a":[1,2,3],"b":[],"c":[4]}`, `{}`, `{"k1":[1],"k2":[2],"k3":[3],"k4":[4],"k5":[5],"k6":[6],"k7":[7]}`}

var ilpAnyGood = []string{`{"a":[1,"s\n",{"b":null}],"c":1.5}`, `[1,[2,[3,[4]]]]`, `"str\n"`, `12`, `null`, `true`}

func ilpMutants(doc string) []string {
	const rep = "{}[]\",:\\ 0-.etnx"
	var out []string
	b := []byte(doc)
	for p := range b {
		out = append(out, string(b[:p]), string(b[:p])+string(b[p+1:]))
		for k := range 4 {
			c := rep[(p*7+k*3)%len(rep)]
			if c != b[p] {
				out = append(out, string(b[:p])+string(c)+string(b[p+1:]))
			}
		}
		out = append(out, string(b[:p])+string(rep[(p*5)%len(rep)])+string(b[p:]))
	}
	return append(out, doc+"x", doc+"{", doc+" 1")
}

func ilpDescribe(err error) string {
	if err == nil {
		return ""
	}
	var ute *vjson.UnmarshalTypeError
	if errors.As(err, &ute) {
		return fmt.Sprintf("UTE{%s %v %d %q}", ute.Value, ute.Type, ute.Offset, ute.Field)
	}
	return fmt.Sprintf("%T:%v", err, err)
}

type ilpOut struct{ err, js string }

// ilpOpts is the option palette. Index 0 is "no options".
var ilpOpts = [][]vjson.Option{
	nil,
	{vjson.UseNumber(true)},
	{vjson.RejectUnknownMembers(true)},
	{vjson.AllowInvalidUTF8(false)},
	{vjson.SkipLenient(true)},
	{vjson.ZeroCopy(false)},
	{vjson.ZeroCopy(true)},
	{vjson.UseNumber(true), vjson.RejectUnknownMembers(true), vjson.ZeroCopy(false)},
}

func ilpRun[T any](in []byte, oi int) (out ilpOut) {
	defer func() {
		if r := recover(); r != nil {
			out = ilpOut{err: fmt.Sprintf("PANIC: %v", r)}
		}
	}()
	v := new(T)
	out.err = ilpDescribe(vjson.Unmarshal(in, v, ilpOpts[oi]...))
	js, err := json.Marshal(v)
	if err != nil {
		js = []byte("marshal-err:" + err.Error())
	}
	out.js = string(js)
	return out
}

// ilpHistory runs the long-lived pool through failing mutants interleaved with
// good documents, twice, and requires every outcome to repeat (the bind
// package sweeps compare against fresh Parsers).
func ilpHistory[T any](t *testing.T, good []string) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	var mutants []string
	for _, g := range good {
		mutants = append(mutants, ilpMutants(g)...)
	}
	// The first pass records each input's outcome and the second must repeat
	// it, with unrelated good calls interleaved between all of them.
	first := map[string]ilpOut{}
	var bad []string
	for round := range 2 {
		for i, m := range mutants {
			oi := i % len(ilpOpts)
			if oi == 6 {
				oi = 0 // an explicit ZeroCopy(true) demand may be rejected by design
			}
			key := fmt.Sprintf("%d|%s", oi, m)
			got := ilpRun[T]([]byte(m), oi)
			// Interleave: a good document under another option set.
			ilpRun[T]([]byte(good[i%len(good)]), (i+3)%len(ilpOpts))
			if round == 0 {
				first[key] = got
				continue
			}
			want := first[key]
			if want.err != got.err || (want.err == "" && want.js != got.js) {
				if len(bad) < 5 {
					bad = append(bad, fmt.Sprintf("opts#%d input %q\n first: err=%q js=%.150s\n later: err=%q js=%.150s", oi, m, want.err, want.js, got.err, got.js))
				}
			}
		}
	}
	for _, b := range bad {
		t.Error(b)
	}
}

func TestIlpHistoryDoc(t *testing.T)   { ilpHistory[ilpDoc](t, ilpDocGood) }
func TestIlpHistoryItems(t *testing.T) { ilpHistory[[]ilpItem](t, ilpItemsGood) }
func TestIlpHistoryMap(t *testing.T)   { ilpHistory[map[string][]int](t, ilpMapGood) }
func TestIlpHistoryAny(t *testing.T)   { ilpHistory[any](t, ilpAnyGood) }

// Options are per call. The same pooled shape runs one input under every
// option combination in turn, with failing calls between, and each result
// must equal a hand-derived expectation that depends on the options alone.
func TestIlpOptionsDoNotStick(t *testing.T) {
	type doc struct {
		N any
		S string
		K int
	}
	goodNum := `{"N":1.5,"S":"a\n","K":1}`
	unknown := `{"N":1,"S":"x","K":1,"zzz":[1,2]}`
	badUTF8 := "{\"N\":1,\"S\":\"\xff\xfe\",\"K\":1}"
	failing := []string{`{"N":`, `{"K":"x"}`, `{"N":1,"S":"x\n","K":1,"zzz":}`, `[1`, ``, `{"N":1} x`}
	opts := []struct {
		name string
		o    []vjson.Option
	}{
		{"none", nil},
		{"usenumber", []vjson.Option{vjson.UseNumber(true)}},
		{"reject", []vjson.Option{vjson.RejectUnknownMembers(true)}},
		{"strictutf8", []vjson.Option{vjson.AllowInvalidUTF8(false)}},
		{"zcfalse", []vjson.Option{vjson.ZeroCopy(false)}},
		{"all", []vjson.Option{vjson.UseNumber(true), vjson.RejectUnknownMembers(true), vjson.AllowInvalidUTF8(false), vjson.ZeroCopy(false)}},
		{"joined", []vjson.Option{vjson.Join(vjson.UseNumber(true), vjson.RejectUnknownMembers(true))}},
		{"override", []vjson.Option{vjson.UseNumber(true), vjson.UseNumber(false)}},
	}
	// Expected behavior per option set: whether numbers are json.Number,
	// unknown members reject, invalid UTF-8 rejects.
	type exp struct{ number, reject, strict bool }
	want := map[string]exp{
		"none": {}, "usenumber": {number: true}, "reject": {reject: true}, "strictutf8": {strict: true},
		"zcfalse": {}, "all": {true, true, true}, "joined": {number: true, reject: true}, "override": {},
	}
	rng := rand.New(rand.NewSource(1))
	for round := range 40 {
		oi := rng.Intn(len(opts))
		o, e := opts[oi].o, want[opts[oi].name]
		// A failing call under random options right before the checked one.
		fo := opts[rng.Intn(len(opts))].o
		var junk doc
		_ = vjson.Unmarshal([]byte(failing[rng.Intn(len(failing))]), &junk, fo...)

		var d doc
		if err := vjson.Unmarshal([]byte(goodNum), &d, o...); err != nil {
			t.Fatalf("round %d %s: %v", round, opts[oi].name, err)
		}
		_, isNum := d.N.(vjson.Number)
		_, isF := d.N.(float64)
		if e.number && !isNum || !e.number && !isF {
			t.Errorf("round %d opts %s: N is %T, want number=%v", round, opts[oi].name, d.N, e.number)
		}
		var d2 doc
		err := vjson.Unmarshal([]byte(unknown), &d2, o...)
		if e.reject != (err != nil) {
			t.Errorf("round %d opts %s: unknown member err=%v, want reject=%v", round, opts[oi].name, err, e.reject)
		}
		var d3 doc
		err = vjson.Unmarshal([]byte(badUTF8), &d3, o...)
		if e.strict != (err != nil) {
			t.Errorf("round %d opts %s: invalid UTF-8 err=%v, want strict=%v", round, opts[oi].name, err, e.strict)
		}
	}
}

// UnmarshalPadded with a broken padding contract returns an error and leaves
// the pooled parser serving the next good call.
func TestIlpPaddedErrorsBetweenGoodCalls(t *testing.T) {
	good := []byte(ilpDocGood[0])
	var want ilpDoc
	if err := json.Unmarshal(good, &want); err != nil {
		t.Fatal(err)
	}
	wantJS, _ := json.Marshal(want)
	check := func(step string) {
		t.Helper()
		var got ilpDoc
		padded := vjson.Pad(append([]byte(nil), good...))
		if err := vjson.UnmarshalPadded(padded, &got); err != nil {
			t.Fatalf("%s: good padded call failed: %v", step, err)
		}
		if js, _ := json.Marshal(got); string(js) != string(wantJS) {
			t.Fatalf("%s: good padded call result\n got  %s\n want %s", step, js, wantJS)
		}
		var got2 ilpDoc
		if err := vjson.Unmarshal(good, &got2); err != nil {
			t.Fatalf("%s: good plain call failed: %v", step, err)
		}
		if js, _ := json.Marshal(got2); string(js) != string(wantJS) {
			t.Fatalf("%s: good plain call result\n got  %s\n want %s", step, js, wantJS)
		}
	}
	check("start")
	bad := []struct {
		name string
		buf  func() []byte
	}{
		{"nocap", func() []byte { return append([]byte(nil), good...)[:len(good):len(good)] }},
		{"shortcap", func() []byte {
			b := make([]byte, len(good), len(good)+vjson.PaddingSize-1)
			copy(b, good)
			return b
		}},
		{"dirtytail", func() []byte {
			b := vjson.Pad(append([]byte(nil), good...))
			b = b[:len(good)+vjson.PaddingSize]
			b[len(good)+vjson.PaddingSize-1] = 'x'
			return b[:len(good)]
		}},
		{"dirtyfirst", func() []byte {
			b := vjson.Pad(append([]byte(nil), good...))
			b = b[:len(good)+vjson.PaddingSize]
			b[len(good)] = '\t'
			return b[:len(good)]
		}},
		{"empty", func() []byte { return vjson.Pad(nil)[:0] }},
		{"nil", func() []byte { return nil }},
	}
	for round := range 3 {
		for _, b := range bad {
			var d ilpDoc
			err := vjson.UnmarshalPadded(b.buf(), &d)
			if err == nil {
				t.Errorf("round %d %s: bad padding accepted", round, b.name)
			}
			check(fmt.Sprintf("round %d after %s", round, b.name))
		}
		// A padded buffer carrying a syntactically broken document.
		for _, m := range ilpMutants(ilpDocGood[1])[:50] {
			var d ilpDoc
			_ = vjson.UnmarshalPadded(vjson.Pad([]byte(m)), &d)
		}
		check(fmt.Sprintf("round %d after mutants", round))
	}
}

// Invalid targets and unsupported types are rejected without disturbing the
// parser the same type later uses.
func TestIlpInvalidTargetsBetweenGoodCalls(t *testing.T) {
	good := []byte(ilpDocGood[1])
	var want ilpDoc
	_ = json.Unmarshal(good, &want)
	wantJS, _ := json.Marshal(want)
	ok := func(step string) {
		t.Helper()
		var d ilpDoc
		if err := vjson.Unmarshal(good, &d); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if js, _ := json.Marshal(d); string(js) != string(wantJS) {
			t.Fatalf("%s: result\n got  %s\n want %s", step, js, wantJS)
		}
	}
	var nilDoc *ilpDoc
	var nilAny any
	var typedNilInAny any = nilDoc
	ch := make(chan int)
	fn := func() {}
	var pp *ilpDoc
	steps := []struct {
		name string
		call func() error
	}{
		{"nil-ptr", func() error { return vjson.Unmarshal(good, nilDoc) }},
		{"non-ptr", func() error { return vjson.Unmarshal(good, ilpDoc{}) }},
		{"nil-any", func() error { return vjson.Unmarshal(good, nilAny) }},
		{"typed-nil-in-any", func() error { return vjson.Unmarshal(good, typedNilInAny) }},
		{"nonptr-in-any", func() error { return vjson.Unmarshal[any](good, ilpDoc{}) }},
		{"map-nonptr", func() error { return vjson.Unmarshal(good, map[string]int{}) }},
		{"chan", func() error { return vjson.Unmarshal(good, &ch) }},
		{"func", func() error { return vjson.Unmarshal(good, &fn) }},
		{"ptr-to-ptr", func() error { return vjson.Unmarshal(good, &pp) }},
		{"empty", func() error { var d ilpDoc; return vjson.Unmarshal(nil, &d) }},
		{"blank", func() error { var d ilpDoc; return vjson.Unmarshal([]byte("  \n "), &d) }},
		{"padded-nonptr", func() error { return vjson.UnmarshalPadded(vjson.Pad(good), ilpDoc{}) }},
		{"padded-nilptr", func() error { return vjson.UnmarshalPadded(vjson.Pad(good), nilDoc) }},
		{"zc-typed-tree", func() error {
			var v struct{ V vjson.Value }
			return vjson.Unmarshal(good, &v, vjson.ZeroCopy(true))
		}},
	}
	ok("start")
	for round := range 3 {
		for _, s := range steps {
			err := func() (err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("PANIC %v", r)
					}
				}()
				return s.call()
			}()
			if err == nil && s.name != "ptr-to-ptr" {
				t.Errorf("round %d %s: no error", round, s.name)
			}
			if err != nil && strings.HasPrefix(err.Error(), "PANIC") {
				t.Errorf("round %d %s: %v", round, s.name, err)
			}
			ok(fmt.Sprintf("round %d after %s", round, s.name))
		}
	}
	// A pointer to a pointer is allocated and filled, like encoding/json.
	var d *ilpDoc
	if err := vjson.Unmarshal(good, &d); err != nil || d == nil || d.P == nil || *d.P != 5 {
		t.Errorf("pointer to pointer target: err=%v d=%+v", err, d)
	}
}

// Retention. Results of earlier calls (strings, slices, maps, any values,
// RawMessage, Number, byte slices) stay intact however many failing and
// succeeding calls follow on the same pooled parsers.

type ilpKept[T any] struct {
	v    *T
	snap string
	in   string
	step int
}

func ilpSnap[T any](v *T) string {
	js, err := json.Marshal(v)
	if err != nil {
		return "marshal-err:" + err.Error()
	}
	return string(js)
}

// ilpCall picks the entry point and options for step i.
type ilpCall func(i int, in []byte, v any) error

var ilpCalls = map[string]ilpCall{
	"default": func(i int, in []byte, v any) error { return vjson.Unmarshal(in, v) },
	"zcfalse": func(i int, in []byte, v any) error { return vjson.Unmarshal(in, v, vjson.ZeroCopy(false)) },
	"zctrue":  func(i int, in []byte, v any) error { return vjson.Unmarshal(in, v, vjson.ZeroCopy(true)) },
	"usenum":  func(i int, in []byte, v any) error { return vjson.Unmarshal(in, v, vjson.UseNumber(true)) },
	"padded": func(i int, in []byte, v any) error {
		return vjson.UnmarshalPadded(vjson.Pad(append([]byte(nil), in...)), v)
	},
	"padd-zcf": func(i int, in []byte, v any) error {
		return vjson.UnmarshalPadded(vjson.Pad(append([]byte(nil), in...)), v, vjson.ZeroCopy(false))
	},
	"cycle": func(i int, in []byte, v any) error {
		return vjson.Unmarshal(in, v, ilpOpts[i%len(ilpOpts)]...)
	},
}

func ilpRetain[T any](t *testing.T, callName string, good []string, extra []string) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	call := ilpCalls[callName]
	var seq []string
	for i, g := range good {
		for j, m := range ilpMutants(g) {
			seq = append(seq, m)
			if j%3 == 0 {
				seq = append(seq, good[(i+j)%len(good)])
			}
		}
	}
	for i, e := range extra {
		seq = append(seq, e, good[i%len(good)])
	}
	var kept []ilpKept[T]
	bad := 0
	verify := func(ks []ilpKept[T], culprit string, step int) {
		for _, k := range ks {
			if cur := ilpSnap(k.v); cur != k.snap && bad < 4 {
				bad++
				i := 0
				for i < len(cur) && i < len(k.snap) && cur[i] == k.snap[i] {
					i++
				}
				lo := max(0, i-40)
				t.Errorf("%s: result of step %d (input %.90q) changed by step %d (input %.90q)\n was ...%.100s\n now ...%.100s",
					callName, k.step, k.in, step, culprit, k.snap[lo:], cur[lo:])
			}
		}
	}
	for step, in := range seq {
		v := new(T)
		func() {
			defer func() { _ = recover() }()
			_ = call(step, []byte(in), v)
		}()
		kept = append(kept, ilpKept[T]{v, ilpSnap(v), in, step})
		if len(kept) > 300 {
			kept = kept[len(kept)-300:]
		}
		verify(kept[max(0, len(kept)-9):len(kept)-1], in, step)
		if step%150 == 0 {
			runtime.GC()
			verify(kept[:len(kept)-1], in+" (GC check)", step)
		}
		if bad >= 4 {
			return
		}
	}
	runtime.GC()
	verify(kept, "(end)", len(seq))
}

func TestIlpRetainDoc(t *testing.T) {
	for _, name := range []string{"default", "zcfalse", "zctrue", "usenum", "padded", "padd-zcf", "cycle"} {
		t.Run(name, func(t *testing.T) { ilpRetain[ilpDoc](t, name, ilpDocGood, nil) })
	}
}

func TestIlpRetainAny(t *testing.T) {
	for _, name := range []string{"default", "zcfalse", "usenum", "padded"} {
		t.Run(name, func(t *testing.T) { ilpRetain[any](t, name, ilpAnyGood, nil) })
	}
}

func TestIlpRetainItems(t *testing.T) {
	for _, name := range []string{"default", "zcfalse", "padded", "cycle"} {
		t.Run(name, func(t *testing.T) { ilpRetain[[]ilpItem](t, name, ilpItemsGood, nil) })
	}
}

func TestIlpRetainMap(t *testing.T) {
	for _, name := range []string{"default", "zcfalse", "cycle"} {
		t.Run(name, func(t *testing.T) { ilpRetain[map[string][]int](t, name, ilpMapGood, nil) })
	}
}

// ilpBig builds a document with a long escaped-string body, a large slice and
// a map, optionally broken at the tail.
func ilpBig(nInts int, strLen int, tail string) string {
	var sb strings.Builder
	sb.WriteString(`{"S":"`)
	for range strLen {
		sb.WriteString(`ab\n`)
	}
	sb.WriteString(`","X":[`)
	for i := range nInts {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", i)
	}
	sb.WriteString(`],"M":{`)
	for i := range 200 {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"key%d\n":%d`, i, i)
	}
	sb.WriteString(`},"SI":[`)
	for i := range 300 {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"B":%d,"C":"c%d\n","D":[1,2,3]}`, i, i)
	}
	sb.WriteString(`]`)
	sb.WriteString(tail)
	return sb.String()
}

// Large documents failing near their end, then small good ones, and the other
// way round, with every destination kept and rechecked after each step.
func TestIlpRetainLargeInterleaved(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	small := ilpDocGood
	bigGood := ilpBig(100000, 5000, `}`)
	seq := []string{
		small[0],
		bigGood,
		ilpBig(100000, 5000, `,"A":}`),
		small[1],
		ilpBig(100000, 5000, `,"N":{"B":"x"}}`),
		small[2],
		ilpBig(100000, 5000, `,"A"`),
		small[0],
		ilpBig(100, 20000, `,"X":[1,2,}`),
		bigGood,
		small[1],
		ilpBig(100000, 10, ``),
		small[2],
		ilpBig(50, 5, `}`),
		small[0],
	}
	for _, name := range []string{"default", "zcfalse", "zctrue", "padded"} {
		call := ilpCalls[name]
		var kept []ilpKept[ilpDoc]
		for step, in := range seq {
			v := new(ilpDoc)
			func() {
				defer func() { _ = recover() }()
				_ = call(step, []byte(in), v)
			}()
			kept = append(kept, ilpKept[ilpDoc]{v, ilpSnap(v), in, step})
			runtime.GC()
			for _, k := range kept[:len(kept)-1] {
				if cur := ilpSnap(k.v); cur != k.snap {
					i := 0
					for i < len(cur) && i < len(k.snap) && cur[i] == k.snap[i] {
						i++
					}
					t.Errorf("%s: result of step %d (input len %d) changed by step %d (input len %d) at byte %d\n was ...%.100s\n now ...%.100s",
						name, k.step, len(k.in), step, len(in), i, k.snap[max(0, i-30):], cur[max(0, i-30):])
					return
				}
			}
		}
	}
}

// Value trees: a Parse result and the typed results of UnmarshalValue stay
// intact across failing parses and failing walks.
func TestIlpValueRetention(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	type target struct {
		A int
		S string
		X []int
	}
	docs := []string{
		`{"A":1,"S":"a\n","X":[1,2,3],"extra":{"deep":["z\n"]}}`,
		`{"A":2,"S":"b\n","X":[4],"extra":[1,2,{"q":"r\n"}]}`,
		`{"A":3,"S":"c\n","X":[],"extra":null}`,
	}
	type held struct {
		val  vjson.Value
		js   string
		typ  *target
		tjs  string
		step int
	}
	var keep []held
	for step := range 90 {
		in := docs[step%len(docs)]
		// A failing parse and a failing walk first.
		for _, m := range ilpMutants(in)[step%7 : step%7+12] {
			if v, err := vjson.Parse([]byte(m)); err == nil {
				var junk target
				_ = vjson.UnmarshalValue(v, &junk)
			}
			var junk2 struct{ A []string }
			if v, err := vjson.Parse([]byte(in)); err == nil {
				_ = vjson.UnmarshalValue(v, &junk2)
			}
		}
		val, err := vjson.Parse([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		js, err := val.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		tg := new(target)
		if err := vjson.UnmarshalValue(val, tg); err != nil {
			t.Fatalf("step %d: UnmarshalValue: %v", step, err)
		}
		tjs, _ := json.Marshal(tg)
		keep = append(keep, held{val, string(js), tg, string(tjs), step})
		if step%10 == 0 {
			runtime.GC()
		}
		for _, h := range keep {
			js, err := h.val.MarshalJSON()
			if err != nil || string(js) != h.js {
				t.Fatalf("step %d: Value from step %d changed:\n was %s\n now %s (err %v)", step, h.step, h.js, js, err)
			}
			if tjs, _ := json.Marshal(h.typ); string(tjs) != h.tjs {
				t.Fatalf("step %d: UnmarshalValue result from step %d changed:\n was %s\n now %s", step, h.step, h.tjs, tjs)
			}
		}
	}
}

// Concurrency. Goroutines hammer the pooled entry points with failing and
// succeeding inputs of several types. Each outcome is compared with a serial
// oracle; copy-mode results must not alias the caller's buffer, and nothing
// a goroutine does to its own buffers may reach another goroutine's result.

type ilpCase struct {
	name string
	run  func(in []byte, oi int, scribble bool) ilpOut
	ins  []string
}

// ilpRunScribble decodes in, then (when scribble is set) overwrites the input
// buffer before snapshotting, which a copying parse must survive.
func ilpRunScribble[T any](in []byte, oi int, scribble bool) (out ilpOut) {
	defer func() {
		if r := recover(); r != nil {
			out = ilpOut{err: fmt.Sprintf("PANIC: %v", r)}
		}
	}()
	v := new(T)
	err := vjson.Unmarshal(in, v, ilpOpts[oi]...)
	out.err = ilpDescribe(err)
	if scribble {
		for i := range in {
			in[i] = 0xAA
		}
	}
	js, jerr := json.Marshal(v)
	if jerr != nil {
		js = []byte("marshal-err:" + jerr.Error())
	}
	out.js = string(js)
	return out
}

func ilpSample(good []string, per int) []string {
	var all []string
	for _, g := range good {
		all = append(all, g)
		all = append(all, ilpMutants(g)...)
	}
	var out []string
	for i := 0; i < len(all); i += max(1, len(all)/per) {
		out = append(out, all[i])
	}
	return append(out, good...)
}

// ilpBigInputs are large documents, good and broken at several depths, whose
// parsers outgrow the hot slot and travel through the pool instead.
func ilpBigInputs() []string {
	good := ilpBig(3000, 400, `}`)
	out := []string{good, ilpDocGood[0]}
	for _, cut := range []int{len(good) / 7, len(good) / 3, len(good) / 2, len(good) - 9, len(good) - 1} {
		out = append(out, good[:cut])
	}
	for _, tail := range []string{`,"A":}`, `,"N":{"B":"x"}}`, `,"X":[1,}`, `,"A"`} {
		out = append(out, ilpBig(3000, 400, tail))
	}
	return out
}

// ilpCopyOpt reports option sets that select a copying parse.
func ilpCopyOpt(oi int) bool { return oi == 5 || oi == 7 }

func TestIlpConcurrentInterleave(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	cases := []ilpCase{
		{"doc", ilpRunScribble[ilpDoc], ilpSample(ilpDocGood, 250)},
		{"items", ilpRunScribble[[]ilpItem], ilpSample(ilpItemsGood, 150)},
		{"map", ilpRunScribble[map[string][]int], ilpSample(ilpMapGood, 150)},
		{"any", ilpRunScribble[any], ilpSample(ilpAnyGood, 150)},
		{"big", ilpRunScribble[ilpDoc], ilpBigInputs()},
	}
	// Serial oracle: two passes in different orders, unstable entries dropped.
	type key struct{ c, i, o int }
	oracle := map[key]ilpOut{}
	for pass := range 2 {
		for ci, c := range cases {
			order := rand.New(rand.NewSource(int64(pass))).Perm(len(c.ins))
			for _, ii := range order {
				for oi := range ilpOpts {
					if oi == 6 {
						continue
					}
					got := c.run([]byte(c.ins[ii]), oi, false)
					k := key{ci, ii, oi}
					if pass == 0 {
						oracle[k] = got
					} else if old, ok := oracle[k]; ok && (old.err != got.err || (old.err == "" && old.js != got.js)) {
						delete(oracle, k)
					}
				}
			}
		}
	}
	keys := make([]key, 0, len(oracle))
	for k := range oracle {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		x, y := keys[a], keys[b]
		if x.c != y.c {
			return x.c < y.c
		}
		if x.i != y.i {
			return x.i < y.i
		}
		return x.o < y.o
	})
	workers := runtime.GOMAXPROCS(0) * 2
	const iters = 3000
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	report := func(s string) {
		mu.Lock()
		if len(failures) < 8 {
			failures = append(failures, s)
		}
		mu.Unlock()
	}
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) + 100))
			for n := range iters {
				k := keys[rng.Intn(len(keys))]
				c := cases[k.c]
				want := oracle[k]
				buf := []byte(c.ins[k.i])
				scribble := ilpCopyOpt(k.o) && rng.Intn(2) == 0
				got := c.run(buf, k.o, scribble)
				if got.err != want.err || (want.err == "" && got.js != want.js) {
					report(fmt.Sprintf("worker %d type %s opts#%d scribble=%v input %q\n want err=%q js=%.140s\n got  err=%q js=%.140s",
						w, c.name, k.o, scribble, c.ins[k.i], want.err, want.js, got.err, got.js))
				}
				if n%500 == 0 {
					runtime.GC()
				}
			}
		}(w)
	}
	wg.Wait()
	for _, f := range failures {
		t.Error(f)
	}
}

// Results kept across goroutines: each goroutine keeps its default-mode (zero
// copy) and copy-mode results, other goroutines keep failing and succeeding,
// and the kept results are rechecked at the end after GC.
func TestIlpConcurrentKeptResults(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	inputs := ilpSample(ilpDocGood, 200)
	workers := runtime.GOMAXPROCS(0) * 2
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) + 7))
			var kept []ilpKept[ilpDoc]
			for n := range 1500 {
				in := inputs[rng.Intn(len(inputs))]
				v := new(ilpDoc)
				var opts []vjson.Option
				switch rng.Intn(3) {
				case 1:
					opts = []vjson.Option{vjson.ZeroCopy(false)}
				case 2:
					opts = []vjson.Option{vjson.UseNumber(true)}
				}
				buf := []byte(in)
				_ = vjson.Unmarshal(buf, v, opts...)
				kept = append(kept, ilpKept[ilpDoc]{v, ilpSnap(v), in, n})
				if len(kept) > 60 {
					kept = kept[1:]
				}
				if n%300 == 0 {
					runtime.GC()
				}
			}
			runtime.GC()
			for _, k := range kept {
				if cur := ilpSnap(k.v); cur != k.snap {
					mu.Lock()
					if len(failures) < 4 {
						failures = append(failures, fmt.Sprintf("worker %d: result of step %d (input %.100q) changed\n was %.160s\n now %.160s", w, k.step, k.in, k.snap, cur))
					}
					mu.Unlock()
					return
				}
			}
		}(w)
	}
	wg.Wait()
	for _, f := range failures {
		t.Error(f)
	}
}

// Decoders, one per goroutine, over streams that mix broken and good lines.
// Skipped lines must not disturb the lines that follow or other decoders.
func TestIlpConcurrentDecoders(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	lines := ilpSample(ilpDocGood, 120)
	var oneLine []string
	for _, l := range lines {
		oneLine = append(oneLine, strings.NewReplacer("\n", " ", "\r", " ").Replace(l))
	}
	workers := runtime.GOMAXPROCS(0) * 2
	// Serial oracle for the whole stream per seed.
	run := func(seed int64) (outs []string) {
		rng := rand.New(rand.NewSource(seed))
		var sb bytes.Buffer
		for range 400 {
			sb.WriteString(oneLine[rng.Intn(len(oneLine))])
			sb.WriteByte('\n')
		}
		skipped := 0
		dec := vjson.NewDecoder(&sb, vjson.WithBufferSize(256), vjson.WithSkipErrors(func(err error) bool { skipped++; return true }))
		for {
			var d ilpDoc
			err := dec.Decode(&d)
			if err == io.EOF {
				break
			}
			if err != nil {
				outs = append(outs, "ERR:"+ilpDescribe(err))
				break
			}
			js, _ := json.Marshal(d)
			outs = append(outs, string(js))
		}
		return append(outs, fmt.Sprintf("skipped=%d", skipped))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := range 4 {
				seed := int64(w%4*10 + r)
				want := run(seed)
				got := run(seed)
				ok := len(want) == len(got)
				for i := 0; ok && i < len(want); i++ {
					ok = want[i] == got[i]
				}
				if !ok {
					mu.Lock()
					if len(failures) < 3 {
						failures = append(failures, fmt.Sprintf("seed %d: decoded streams differ between two runs (%d vs %d results)", seed, len(want), len(got)))
					}
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	for _, f := range failures {
		t.Error(f)
	}
}

// Value-receiver hooks on types stored directly in an interface word. The
// hook must see the field's current contents, as with encoding/json.
type ilpPtrShaped struct{ p *int }

var ilpPtrShapedSeen []*int

func (x ilpPtrShaped) UnmarshalJSON([]byte) error {
	ilpPtrShapedSeen = append(ilpPtrShapedSeen, x.p)
	return nil
}

type ilpMapShaped map[string]int

var ilpMapShapedNil []bool

func (m ilpMapShaped) UnmarshalJSON([]byte) error {
	ilpMapShapedNil = append(ilpMapShapedNil, m == nil)
	return nil
}

func TestIlpValueReceiverPointerShapedHook(t *testing.T) {
	type doc struct {
		PS ilpPtrShaped
		MS ilpMapShaped
	}
	anchor := new(int)
	ilpPtrShapedSeen, ilpMapShapedNil = nil, nil
	v := doc{PS: ilpPtrShaped{anchor}}
	if err := vjson.Unmarshal([]byte(`{"PS":1,"MS":{"a":1}}`), &v); err != nil {
		t.Fatal(err)
	}
	if len(ilpPtrShapedSeen) != 1 || ilpPtrShapedSeen[0] != anchor {
		t.Errorf("struct{p *int} value receiver: want preset pointer %p, hook saw %v", anchor, ilpPtrShapedSeen)
	}
	if len(ilpMapShapedNil) != 1 || !ilpMapShapedNil[0] {
		t.Errorf("map value receiver on a nil field: want nil receiver, got nil=%v", ilpMapShapedNil)
	}
}

// ilpVDoc is ilpDoc without the fields tape-bind rejects (json.Number,
// RawMessage, []byte).
type ilpVDoc struct {
	A  int
	S  string
	X  []int
	M  map[string]int
	MS map[string]string
	N  ilpInner
	SI []ilpInner
	P  *int
	AL []any
}

// Parse plus UnmarshalValue must agree with Unmarshal on the same bytes, for
// every input and option set, however many failures came before.
func TestIlpParseUnmarshalValueAgreement(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(20))
	var inputs []string
	for _, g := range ilpDocGood {
		inputs = append(inputs, g)
		inputs = append(inputs, ilpMutants(g)...)
	}
	parseOpts := [][]vjson.Option{nil, {vjson.UseNumber(true)}, {vjson.ZeroCopy(false)}, {vjson.AllowInvalidUTF8(false)}}
	bad := 0
	for i, in := range inputs {
		po := parseOpts[i%len(parseOpts)]
		// Break the pool state first with a failing call of each entry.
		var junk ilpVDoc
		_ = vjson.Unmarshal([]byte(ilpMutants(ilpDocGood[i%len(ilpDocGood)])[i%97]), &junk, po...)
		_, _ = vjson.Parse([]byte(ilpMutants(ilpDocGood[(i+1)%len(ilpDocGood)])[i%89]), po...)

		var direct any
		derr := vjson.Unmarshal([]byte(in), &direct, po...)
		val, perr := vjson.Parse([]byte(in), po...)
		if (derr == nil) != (perr == nil) {
			if bad++; bad <= 6 {
				t.Errorf("opts#%d input %q: Unmarshal err=%v, Parse err=%v", i%len(parseOpts), in, derr, perr)
			}
			continue
		}
		if perr != nil {
			continue
		}
		var typed, viaTyped ilpVDoc
		terr := vjson.Unmarshal([]byte(in), &typed, po...)
		verr := vjson.UnmarshalValue(val, &viaTyped, po...)
		if (terr == nil) != (verr == nil) {
			if bad++; bad <= 6 {
				t.Errorf("opts#%d input %q: typed Unmarshal err=%v, UnmarshalValue err=%v", i%len(parseOpts), in, terr, verr)
			}
			continue
		}
		if terr == nil {
			tj, _ := json.Marshal(typed)
			tvj, _ := json.Marshal(viaTyped)
			if string(tj) != string(tvj) {
				if bad++; bad <= 6 {
					t.Errorf("opts#%d input %q:\n typed Unmarshal      %s\n typed UnmarshalValue %s", i%len(parseOpts), in, tj, tvj)
				}
			}
		}
	}
}

// A navigation-only Value (Parse with ZeroCopy(true)) is rejected by
// UnmarshalValue, and the rejection leaves the walker usable.
func TestIlpZeroCopyValueRejectionThenGood(t *testing.T) {
	src := []byte(ilpDocGood[0])
	var want ilpVDoc
	_ = json.Unmarshal(src, &want)
	wantJS, _ := json.Marshal(want)
	for round := range 5 {
		zc, err := vjson.ParsePadded(vjson.Pad(append([]byte(nil), src...)), vjson.ZeroCopy(true))
		if err != nil {
			t.Fatalf("round %d: Parse zero-copy: %v", round, err)
		}
		var d ilpVDoc
		if err := vjson.UnmarshalValue(zc, &d); !errors.Is(err, vjson.ErrZeroCopyValue) {
			t.Errorf("round %d: zero-copy Value: want ErrZeroCopyValue, got %v", round, err)
		}
		var junk struct{ A []string }
		_ = vjson.UnmarshalValue(ilpMustParse(t, src), &junk)
		var good ilpVDoc
		if err := vjson.UnmarshalValue(ilpMustParse(t, src), &good); err != nil {
			t.Fatalf("round %d: good UnmarshalValue: %v", round, err)
		}
		if js, _ := json.Marshal(good); string(js) != string(wantJS) {
			t.Errorf("round %d: UnmarshalValue after rejection\n got  %s\n want %s", round, js, wantJS)
		}
	}
}

func ilpMustParse(t *testing.T, src []byte) vjson.Value {
	t.Helper()
	v, err := vjson.Parse(append([]byte(nil), src...))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Pointer-rich destinations decoded while another goroutine forces collections
// back to back: map flushes, slice growth and failed parses all publish
// pointers the collector must find.
type ilpPtrDoc struct {
	MP  map[string]*ilpInner
	MS  map[string][]string
	MI  map[int]ilpInner
	MA  map[string]any
	PS  []*ilpInner
	SS  [][]string
	AL  []any
	Any any
	PP  **ilpInner
}

func ilpPtrDocInput(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"MP":{`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"p%d\n":{"B":%d,"C":"c%d\n","D":[%d.5]}`, i, i, i, i)
	}
	sb.WriteString(`},"MS":{`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"s%d":["a%d\n","b%d"]`, i, i, i)
	}
	sb.WriteString(`},"MI":{`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"%d":{"B":%d,"C":"i%d\n"}`, i, i, i)
	}
	sb.WriteString(`},"MA":{`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"a%d":{"k":["v%d\n",%d,null]}`, i, i, i)
	}
	sb.WriteString(`},"PS":[`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"B":%d,"C":"q%d\n"}`, i, i)
	}
	sb.WriteString(`],"SS":[["x\n"],["y","z\n"],[]],"AL":[1,"two\n",[3],{"four":4}],"Any":{"deep":[{"er":"s\n"}]},"PP":{"B":7}}`)
	return sb.String()
}

func TestIlpConcurrentGCStress(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(1))
	good := []string{ilpPtrDocInput(3), ilpPtrDocInput(40), ilpPtrDocInput(200)}
	want := make([]string, len(good))
	for i, g := range good {
		var d ilpPtrDoc
		if err := json.Unmarshal([]byte(g), &d); err != nil {
			t.Fatal(err)
		}
		js, _ := json.Marshal(d)
		want[i] = string(js)
	}
	var mutants []string
	for _, g := range good[:2] {
		mutants = append(mutants, ilpMutants(g)...)
	}
	stop := make(chan struct{})
	var gcWG sync.WaitGroup
	gcWG.Add(1)
	go func() {
		defer gcWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	for w := range 4 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) + 50))
			var keep []*ilpPtrDoc
			var keepIdx []int
			for range 250 {
				for range 3 {
					m := mutants[rng.Intn(len(mutants))]
					var junk ilpPtrDoc
					_ = vjson.Unmarshal([]byte(m), &junk, ilpOpts[rng.Intn(6)]...)
					_, _ = json.Marshal(&junk)
				}
				gi := rng.Intn(len(good))
				d := new(ilpPtrDoc)
				if err := vjson.Unmarshal([]byte(good[gi]), d); err != nil {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("worker %d: good document %d failed: %v", w, gi, err))
					mu.Unlock()
					return
				}
				keep, keepIdx = append(keep, d), append(keepIdx, gi)
			}
			for i, d := range keep {
				if js, _ := json.Marshal(d); string(js) != want[keepIdx[i]] {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("worker %d: kept result %d (doc %d) differs from encoding/json\n got  %.200s\n want %.200s", w, i, keepIdx[i], js, want[keepIdx[i]]))
					mu.Unlock()
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	gcWG.Wait()
	for _, f := range failures {
		t.Error(f)
	}
}

// The marshal side of the same representation rule: a value-receiver
// MarshalJSON or MarshalText on a map or single-pointer-struct type must see
// the field's contents, not its address.
type ilpMarshalMap map[string]int

func (m ilpMarshalMap) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"len=%d"`, len(m))), nil
}

type ilpMarshalPtr struct{ p *int }

func (m ilpMarshalPtr) MarshalJSON() ([]byte, error) {
	if m.p == nil {
		return []byte(`"nil"`), nil
	}
	return []byte(fmt.Sprintf(`"v=%d"`, *m.p)), nil
}

type ilpMarshalTextMap map[string]int

func (m ilpMarshalTextMap) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("len=%d", len(m))), nil
}

func TestIlpValueReceiverPointerShapedMarshal(t *testing.T) {
	x := 5
	v := struct {
		A ilpMarshalMap
		B ilpMarshalPtr
		C ilpMarshalTextMap
		D *ilpMarshalPtr
	}{ilpMarshalMap{"a": 1, "b": 2}, ilpMarshalPtr{&x}, ilpMarshalTextMap{"z": 1}, &ilpMarshalPtr{&x}}
	want, _ := json.Marshal(v)
	got, err := vjson.Marshal(v)
	if err != nil || string(got) != string(want) {
		t.Errorf("value-receiver marshaler on pointer-shaped type\n std   %s\n vjson %s (err %v)", want, got, err)
	}
}
