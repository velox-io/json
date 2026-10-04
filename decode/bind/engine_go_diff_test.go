//go:build vj_enginediff

package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/velox-io/json/vbind"
	"github.com/velox-io/json/vopt"

	"github.com/velox-io/json/native/ndec"
)

// The Go engine must reproduce the native binder: same destination contents,
// same error type, text, and offset. The vj_enginediff build makes the engine
// choice dynamic, so these tests run both engines in one process by toggling
// forceGoCore.

func needNativeForDiff(t *testing.T) {
	t.Helper()
	if !ndec.Available {
		t.Skip("differential run needs the native binder")
	}
}

type gcInner struct {
	B int
	C string
	D []float64
}

type GCEmbed struct {
	E1 int
	E2 string
}

type gcText struct{ v string }

func (x *gcText) UnmarshalText(b []byte) error {
	if string(b) == "bad" {
		return errors.New("gcText: bad")
	}
	x.v = "T:" + string(b)
	return nil
}

type gcHook struct{ raw string }

func (x *gcHook) UnmarshalJSON(b []byte) error {
	if string(b) == `"fail"` {
		return errors.New("gcHook: fail")
	}
	x.raw = "H:" + string(b)
	return nil
}

type gcAll struct {
	*GCEmbed
	A   int
	I8  int8
	U   uint
	U16 uint16
	F   float32
	G   float64
	S   string
	T   bool
	X   []int
	Y   [2]int
	M   map[string]int
	MI  map[int]string
	MS  map[string]gcInner
	N   gcInner
	P   *int
	PP  **gcInner
	PS  []*int
	Any any
	AL  []any
	Num json.Number
	Raw json.RawMessage
	H   gcHook
	HP  *gcHook
	HM  map[string]gcHook
	Tx  gcText
	TxS []gcText
	By  []byte
	Q   int     `json:",string"`
	QS  string  `json:",string"`
	QF  float64 `json:",string"`
	QB  bool    `json:",string"`
	E   error
	Ren int `json:"renamed"`
}

var gcDiffInputs = []string{
	`{"A":1,"I8":-5,"U":7,"U16":65535,"F":1.5,"G":-2.25e3,"S":"hi\n\u00e9\ud83d\ude00","T":true}`,
	`{"X":[1,2,3],"Y":[4,5,6],"M":{"a":1,"b":2},"MI":{"1":"x","-2":"y"},"N":{"B":9,"C":"c","D":[1.5,2]}}`,
	`{"P":5,"PP":{"B":1},"PS":[1,null,3],"Any":{"k":[1,"s",true,null,{"z":1.5}]},"AL":[1,[2],{}]}`,
	`{"Num":12.5e3,"Raw":{"a": [1, 2]} ,"H":{"x":1},"HP":[1],"HM":{"a":"v","b":2},"Tx":"abc","TxS":["a","b"],"By":"aGVsbG8="}`,
	`{"Q":"12","QS":"\"q\\n\"","QF":"1.5","QB":"true","E1":3,"E2":"e","renamed":4}`,
	`{"QF":"1.` + strings.Repeat("0", 300) + `1"}`, `{"QF":"\u0031.` + strings.Repeat("0", 300) + `1"}`,
	`{"QF":"1.` + strings.Repeat("0", 300) + `1x"}`,
	`{"MS":{"k":{"B":1,"C":"x"},"j":{"D":[]}},"Raw":null,"Num":"7"}`,
	`{"A":"x"}`, `{"A":"x","X":[1]}`, `{"X":["x"]}`, `{"M":{"a":"x"}}`, `{"N":{"B":"x"},"A":2}`,
	`{"N":{"B":"x","C":1}}`, `{"X":{"a":1}}`, `{"X":1}`, `{"N":[1]}`, `{"M":[1]}`, `{"A":1e400}`,
	`{"F":1e40}`, `{"A":1.5}`, `{"A":300000000000000000000}`, `{"A":1}x`, `{"A":1`, `{"A":tru}`,
	`{"A" 1}`, `{"A":1,}`, `{"A":[1,2}`, `{"Z":[1,2}`, `{"Z":[1,,2]}`, `{"Z":{"a":}}`, `{"Z":"\x"}`,
	`{"S":"\x"}`, "{\"S\":\"a\x01\"}", `{"P":null,"A":null,"X":null,"M":null,"Any":null,"Num":null}`,
	`null`, `{"A":"1","A":2}`, ` `, `{"Q":12}`, `{"Q":"x"}`, `{"QS":"q"}`, `{"U":-1}`, `{"U":-0}`,
	`{"I8":128}`, `{"A":01}`, `{"A":-}`, `{"A":1.}`, `{"G":.5}`, `{"H":"fail"}`, `{"Tx":"bad"}`,
	`{"Tx":1}`, `{"Tx":null}`, `{"By":"!!"}`, `{"By":[1,2]}`, `{"E":null}`, `{"E":1}`, `{"MI":{"x":"1"}}`,
	`{"Y":[1]}`, `{"Y":[1,2,3,[4,{}]]}`, `{"Y":[1,2,3,]}`, `{"Any":1e400}`, `{"AL":[1,2}`, `[1]`, `"s"`,
	`{}`, `{"A":1}{}`, `{"A":1} `, `{"HM":{"a":"fail"}}`, `{"MS":{"k":{"B":"x"}}}`, `{"A":1,"A":"x","A":3}`,
	`{"Any":{"a":1,"a":2}}`, `{"Raw":[1,{"a":2}], "A":1}`, `{"\u0041":7}`, `{"A\u0000":1}`,
	`{"PP":null}`, `{"PS":[]}`, `{"X":[]}`, `{"M":{}}`, `{"AL":[]}`, `{"N":{}}`, `{"Y":[]}`,
	// Walk errors outrank hook failures, hook failures outrank map-key
	// failures, and hooks inside a grown or reused backing land in the
	// final one.
	`{"E":{"a":1},"N":{"D":[1.5,2x}}`, `{"Tx":"bad","X":[1,2,3,4,5,6,7,8,9]x}`,
	`{"TxS":["a","b","c","d","e","f","g","h","i"]}`, `{"TxS":["a"],"TxS":["b","c","d","e","f","g"]}`,
	`{"HM":{"a":"x"},"X":[1,2,3,4,5,6],"H":"fail"}`, `{"MI":{"x":"1"},"H":"fail"}`,
	`{"H":"fail","MI":{"x":"1"}}`, `{"MI":{"x":"1"},"A":tru}`, `{"MI":{"x":"1"},"A":"s"}`,
	`{"H":"fail","A":"s"}`, `{"MI":{"x":"1","2":"b","y":"3"}}`,
	// An unclosed string anywhere outranks a walk error ahead of it.
	`{"A":tru,"S":"abc`, `{"A":1}x"`, `{"A":"x","X":[1,]"`, `{"A" 1,"S":"x`, `{"Raw":[a\"],"A":"`,
}

// gcHooks stages more hooks than a drain holds, in a fixed array and in a
// slice that grows while they are pending.
type gcHooks struct {
	Arr [20]gcText
	V   []gcText
	A   int
}

// TestGoCoreDiffHookPrecedence pins which failure surfaces whenever drains
// run. Repeated rounds move the native allocator's slice capacities, so a
// verdict that depended on drain timing would flip between them.
func TestGoCoreDiffHookPrecedence(t *testing.T) {
	needNativeForDiff(t)
	quoted := func(n int, bad int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(`"t%d"`, i)
		}
		if bad >= 0 {
			parts[bad] = `"bad"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	for range 4 {
		for _, n := range []int{2, 8, 15, 16, 17, 20} {
			for _, bad := range []int{-1, 0, n / 2, n - 1} {
				arr := quoted(n, bad)
				for _, tail := range []string{`}`, `,"A":1}`, `,"A":"x"}`, `,"A":tru}`, `,"A":1`} {
					diffEngines[gcHooks](t, []byte(`{"Arr":`+arr+tail), nil)
					diffEngines[gcHooks](t, []byte(`{"V":`+arr+tail), nil)
				}
			}
		}
	}
}

type gcOutcome struct {
	val any
	err string
}

// Poly hosts defer member keys whose binding waits for unresolved state, so
// their keys are stored beyond the walk. The inputs pair plain keys with
// escaped ones, whose transient decode reuses a buffer the stored key must
// survive, and cover missing, unknown, duplicated, and malformed
// discriminators and members.
type gcPolyUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type gcPolyProd struct {
	SKU   string   `json:"sku"`
	Price float64  `json:"price"`
	Tags  []string `json:"tags"`
}

type gcPolyHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

type gcKindofHost struct {
	Data any `json:"data" vjson:"kindof"`
}

func init() {
	vbind.DefineVariantCases[gcPolyHost, struct {
		_ gcPolyUser `case:"user"`
		_ gcPolyProd `case:"product"`
	}]()
	vbind.DefineKindofCases[gcKindofHost, struct {
		number float64
		string string
		object gcPolyUser
	}]()
}

// The deferred case fields a poly host binds in phase 2 currently lose
// their mismatch identity on the native side, which reports the fallback
// UnmarshalTypeError (zero value and offset) where the Go engine and
// encoding/json carry the leaf value and its token end, and a truncation
// right after a colon reports a plain syntax error where every other
// native path and encoding/json report the end of input. The inputs that
// hit either shape stay out of the suite until the native poly path
// catches up: {"type":"user","id":"x"}, {"type":"product","price":1e400},
// and a member whose value is cut off at the end of input.
var gcPolyInputs = []string{
	`{"type":"user","id":1,"name":"ann"}`,
	`{"type":"product","sku":"s","price":1.5,"tags":["a"]}`,
	`{"type":"user","i\u0064":1,"na\u006de":"ann"}`,
	`{"ty\u0070e":"user","na\u006de":"ann"}`,
	`{"na\u006de":"ann","type":"user"}`,
	`{"type":"user","na\u006de":"ann","ju\u006ek":1}`,
	`{"type":"user","id":1,"id":2}`,
	`{"type":"user","name":"\ud83d\ude00"}`,
	`{"type":"nope","id":1}`,
	`{"id":1,"name":"ann"}`,
	`{"type":"user"}`,
	`{}`,
	`{"type":5}`,
	`{"type":"user","id":1`,
	`{"type":"user","id":tru}`,
	`{"type":"user","id":1}x`,
	`{"data":1}`, `{"data":"s"}`, `{"data":{"id":1,"name":"x"}}`,
	`{"da\u0074a":1,"x\u0079":2}`, `{"da\u0074a":1}`,
	`{"data":true}`, `{"data":[1,2]}`, `{"data":null}`,
}

func TestGoCoreDiffPoly(t *testing.T) {
	needNativeForDiff(t)
	for _, in := range gcPolyInputs {
		b := []byte(in)
		diffEngines[gcPolyHost](t, b, nil)
		diffEngines[gcPolyHost](t, b, nil, vopt.UseNumber(true))
		diffEngines[gcPolyHost](t, b, nil, vopt.RejectUnknownMembers(true))
		diffEngines[gcPolyHost](t, b, nil, vopt.ZeroCopy(true))
		diffEngines[gcPolyHost](t, b, func(v *gcPolyHost) { v.Type, v.Data = "stale", gcPolyUser{ID: 9} })
		diffEngines[gcKindofHost](t, b, nil)
		diffEngines[gcKindofHost](t, b, nil, vopt.RejectUnknownMembers(true))
	}
}

func describeErr(err error) string {
	if err == nil {
		return ""
	}
	var ute *UnmarshalTypeError
	if errors.As(err, &ute) {
		return fmt.Sprintf("UTE{%s %v %d}", ute.Value, ute.Type, ute.Offset)
	}
	var se *SyntaxError
	if errors.As(err, &se) {
		return fmt.Sprintf("SE{%s %d eof=%v}", se.Error(), se.Offset, errors.Is(err, io.ErrUnexpectedEOF))
	}
	return fmt.Sprintf("%T:%v", err, err)
}

func runEngine[T any](goCore bool, in []byte, init func(*T), opts []UnmarshalOption) gcOutcome {
	prev := forceGoCore
	forceGoCore = goCore
	defer func() { forceGoCore = prev }()
	v := new(T)
	if init != nil {
		init(v)
	}
	err := Unmarshal(in, v, opts...)
	return gcOutcome{val: v, err: describeErr(err)}
}

func diffEngines[T any](t *testing.T, in []byte, init func(*T), opts ...UnmarshalOption) bool {
	t.Helper()
	n := runEngine(false, in, init, opts)
	g := runEngine(true, in, init, opts)
	if n.err != g.err {
		t.Errorf("input %q\n err native=%s\n err go    =%s", in, n.err, g.err)
		return false
	}
	if n.err == "" && !reflect.DeepEqual(n.val, g.val) {
		t.Errorf("input %q\n val native=%+v\n val go    =%+v", in, n.val, g.val)
		return false
	}
	return true
}

func TestGoCoreDiffCases(t *testing.T) {
	needNativeForDiff(t)
	for _, in := range gcDiffInputs {
		diffEngines[gcAll](t, []byte(in), nil)
		diffEngines[gcAll](t, []byte(in), nil, vopt.UseNumber(true))
		diffEngines[gcAll](t, []byte(in), nil, vopt.RejectUnknownMembers(true))
		diffEngines[gcAll](t, []byte(in), nil, vopt.ZeroCopy(true))
		diffEngines[gcAll](t, []byte(in), nil, vopt.SkipLenient(true))
		diffEngines[gcAll](t, []byte(in), func(v *gcAll) {
			one := 1
			v.A, v.X, v.P, v.N = 9, []int{7, 7, 7, 7}, &one, gcInner{B: 5, D: []float64{3}}
			v.GCEmbed = &GCEmbed{E1: 8}
			v.M = map[string]int{"old": 1}
		})
	}
}

func TestGoCoreDiffRoots(t *testing.T) {
	needNativeForDiff(t)
	roots := []string{`1`, `-0`, `"x"`, `true`, `null`, `[1,2]`, `{"a":1}`, `1 2`, `[1,]`, `[1,"x",3]`,
		`[[1],[2,"x"]]`, `{"1":1,"x":2}`, `{"a":[1,"x"]}`, `"`, `[`, `{`, `[1`, `1e400`, `nul`, `[]`, `{}`,
		`  [ 1 , 2 ]  `, `{"a":{"b":[1,{"c":null}]}}`, `"aGk="`, `[1] x`, `tru`, `"\ud800"`,
		`[1,2,3]`, `[1,2,[3,"x"]]`, `[1,2,1.2.3]`, `[1,2,]`}
	for _, in := range roots {
		b := []byte(in)
		for _, opts := range [][]UnmarshalOption{nil, {vopt.SkipLenient(true)}} {
			diffEngines[int](t, b, nil, opts...)
			diffEngines[*int](t, b, nil, opts...)
			diffEngines[string](t, b, nil, opts...)
			diffEngines[bool](t, b, nil, opts...)
			diffEngines[[]int](t, b, nil, opts...)
			diffEngines[[2]int](t, b, nil, opts...)
			diffEngines[[][]int](t, b, nil, opts...)
			diffEngines[map[string]int](t, b, nil, opts...)
			diffEngines[map[int]int](t, b, nil, opts...)
			diffEngines[map[string][]int](t, b, nil, opts...)
			diffEngines[any](t, b, nil, opts...)
			diffEngines[[]any](t, b, nil, opts...)
			diffEngines[map[string]any](t, b, nil, opts...)
			diffEngines[json.RawMessage](t, b, nil, opts...)
			diffEngines[[]byte](t, b, nil, opts...)
			diffEngines[json.Number](t, b, nil, opts...)
			diffEngines[gcInner](t, b, nil, opts...)
			diffEngines[*gcInner](t, b, nil, opts...)
		}
	}
}

func TestGoCoreDiffDepth(t *testing.T) {
	needNativeForDiff(t)
	for _, d := range []int{254, 255, 256, 257} {
		b := []byte(strings.Repeat("[", d) + strings.Repeat("]", d))
		diffEngines[any](t, b, nil)
		diffEngines[[]any](t, b, nil)
	}
}

// decodeSeq drains a Decoder, one value per step, through the listed target
// types (cycling). While the stream stays usable each outcome also records
// where the next value starts: the input neither the reader nor Buffered
// still holds, past whitespace, since read-ahead decides how much of it
// the window already skipped.
func decodeSeq(goCore bool, in string, oneByte bool, targets ...func() any) []gcOutcome {
	prev := forceGoCore
	forceGoCore = goCore
	defer func() { forceGoCore = prev }()
	sr := strings.NewReader(in)
	var r io.Reader = sr
	if oneByte {
		r = iotest.OneByteReader(sr)
	}
	d := NewDecoder(r, WithBufferSize(16))
	var out []gcOutcome
	for i := 0; i < 16; i++ {
		v := targets[i%len(targets)]()
		err := d.Decode(v)
		o := gcOutcome{val: v, err: describeErr(err)}
		if d.Err() == nil {
			rest, _ := io.ReadAll(d.Buffered())
			next := len(in) - sr.Len() - len(rest)
			next += feedSkipWS([]byte(in[next:]))
			o.err += fmt.Sprintf(" next=%d", next)
		}
		out = append(out, o)
		if err == io.EOF || d.Err() != nil {
			break
		}
	}
	return out
}

// TestGoCoreDiffDecoder covers the value-per-call driver: error offsets past
// the first value, which errors leave the stream usable, and where the next
// value starts after a non-sticky error, across target type switches.
func TestGoCoreDiffDecoder(t *testing.T) {
	needNativeForDiff(t)
	all := func() any { return new(gcAll) }
	iface := func() any { return new(error) }
	num := func() any { return new(int) }
	cases := []struct {
		in      string
		targets []func() any
	}{
		{`{"A":1} {"A":"x"} {"A":2}`, []func() any{all}},
		{`{"A":1} {"A":tru} {"A":2}`, []func() any{all}},
		{`{"E":1} {"A":3}`, []func() any{all}},
		{`1  2 {"E":3}`, []func() any{iface, iface, all}},
		{`"x" 5 6`, []func() any{num}},
		{`{"A":"x"} 7`, []func() any{all, num}},
		{`{"H":"fail"} {"A":1}`, []func() any{all}},
		{`{"MI":{"x":"1"}} {"A":1}`, []func() any{all}},
		{`{"A":1} {"A":`, []func() any{all}},
		{`{"A":1}` + "\n" + `{"X":[1,2,3],"S":"` + strings.Repeat("s", 40) + `"}` + "\n", []func() any{all}},
	}
	for _, tc := range cases {
		for _, oneByte := range []bool{false, true} {
			n := decodeSeq(false, tc.in, oneByte, tc.targets...)
			g := decodeSeq(true, tc.in, oneByte, tc.targets...)
			if len(n) != len(g) {
				t.Errorf("input %q oneByte=%v: steps native=%d go=%d\n native=%v\n go    =%v", tc.in, oneByte, len(n), len(g), n, g)
				continue
			}
			for i := range n {
				if n[i].err != g[i].err {
					t.Errorf("input %q oneByte=%v step %d:\n err native=%s\n err go    =%s", tc.in, oneByte, i, n[i].err, g[i].err)
				} else if !reflect.DeepEqual(n[i].val, g[i].val) {
					t.Errorf("input %q oneByte=%v step %d:\n val native=%+v\n val go    =%+v", tc.in, oneByte, i, n[i].val, g[i].val)
				}
			}
		}
	}
}

// TestGoCoreDiffMutations perturbs valid documents: byte flips, insertions,
// deletions, and truncations.
func TestGoCoreDiffMutations(t *testing.T) {
	needNativeForDiff(t)
	r := rand.New(rand.NewPCG(1, uint64(time.Now().UnixNano())))
	alphabet := []byte(`{}[],:"\ 0123456789.eE+-truefalsnxu`)
	fails := 0
	// Poly documents stay out of the sweep for now: a truncation lands on
	// the native poly path's end-of-input divergence documented at
	// gcPolyInputs, which random cuts hit too often to tolerate.
	for i := 0; i < 60000 && fails < 10; i++ {
		base := []byte(gcDiffInputs[r.IntN(len(gcDiffInputs))])
		b := append([]byte(nil), base...)
		for k := r.IntN(3) + 1; k > 0 && len(b) > 0; k-- {
			p := r.IntN(len(b))
			switch r.IntN(4) {
			case 0:
				b[p] = alphabet[r.IntN(len(alphabet))]
			case 1:
				b = append(b[:p], append([]byte{alphabet[r.IntN(len(alphabet))]}, b[p:]...)...)
			case 2:
				b = append(b[:p], b[p+1:]...)
			case 3:
				b = b[:p]
			}
		}
		if len(b) == 0 {
			continue
		}
		var opts []UnmarshalOption
		switch i % 4 {
		case 1:
			opts = []UnmarshalOption{vopt.UseNumber(true)}
		case 2:
			opts = []UnmarshalOption{vopt.ZeroCopy(true), vopt.AllowInvalidUTF8(false)}
		case 3:
			opts = []UnmarshalOption{vopt.RejectUnknownMembers(true)}
		}
		if !diffEngines[gcAll](t, b, nil, opts...) {
			fails++
		}
	}
}

// TestGoCoreGrowPointerFreeUnderGC grows small pointer-free slices, whose
// backings come from the tiny allocator unaligned to a word, while the GC
// keeps its write barrier on. A barriered copy of such a backing throws.
func TestGoCoreGrowPointerFreeUnderGC(t *testing.T) {
	prev := forceGoCore
	forceGoCore = true
	defer func() { forceGoCore = prev }()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	var v struct {
		B []byte
		H []int16
	}
	for i := range 20000 {
		in := []byte(`{"B":[1,2,3],"H":[1,2,3]}`)
		if i%2 == 0 {
			in = []byte(`{"B":[1],"H":[1]}`)
		}
		v.B, v.H = nil, nil
		if err := Unmarshal(in, &v); err != nil {
			t.Fatal(err)
		}
	}
}

type scanSealRoot struct {
	X []int  `json:"x"`
	S string `json:"s"`
}

// TestScanFailureSealsNothing pins that a parse failing its structural scan
// leaves every slot cursor where it was. Such a parse binds nothing, but its
// error yield runs the seal, which reads the machine's live container: it
// must be the fresh root rather than the slice an earlier parse died in.
//
// The earlier parse's spill survives only while no other native parse runs,
// so the Go engine writes the tail in between, past the abandoned region.
func TestScanFailureSealsNothing(t *testing.T) {
	needNativeForDiff(t)
	p, err := NewParser[scanSealRoot]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	parse := func(goCore bool, in string, wantErr bool) {
		t.Helper()
		prev := forceGoCore
		forceGoCore = goCore
		defer func() { forceGoCore = prev }()
		var v scanSealRoot
		if err := p.Unmarshal([]byte(in), &v); (err != nil) != wantErr {
			t.Fatalf("goCore=%v %q: err = %v", goCore, in, err)
		}
	}
	// Room past the first slice, so the next ones borrow the same block.
	parse(false, `{"x":[1,2,3,4,5]}`, false)
	parse(false, `{"x":[1,2,x`, true)
	parse(true, `{"x":[1,2,3,4,5]}`, false)
	before := make([]uint32, len(p.alloc.Slots))
	for i := range p.alloc.Slots {
		before[i] = p.alloc.Slots[i].Offset
	}
	parse(false, `{"s":"`, true)
	for i := range p.alloc.Slots {
		if sc := &p.alloc.Slots[i]; sc.Offset != before[i] {
			t.Fatalf("slot %d cursor moved from %d to %d across a scan failure", i, sc.Offset, before[i])
		}
	}
}
