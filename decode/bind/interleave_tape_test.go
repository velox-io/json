package bind

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
	"github.com/velox-io/json/vopt"
)

// Interleaving failing and succeeding operations on tape-backed paths: Value
// fields, UnmarshalValue (tape-bind) and poly fields (variant, kindof) whose
// cold cases defer through the tape and rebind in phase 2. One long-lived
// Parser runs mutated (failing) documents interleaved with good documents.
// Oracles: (1) dirty and fresh Parsers agree on error identity and on the
// destination of every successful parse; (2) earlier successful results stay
// intact (re-marshaled after GC) while later parses reuse the Parser; (3)
// encoding/json agrees on success where semantics match.

// --- destination types ---

type iltValHost struct {
	Pre  int                    `json:"pre"`
	V    value.Value            `json:"v"`
	Mid  string                 `json:"mid"`
	W    []value.Value          `json:"w"`
	M    map[string]value.Value `json:"m"`
	Post []int                  `json:"post"`
}

// iltValHostStd mirrors iltValHost with json.RawMessage for the std oracle.
type iltValHostStd struct {
	Pre  int                        `json:"pre"`
	V    json.RawMessage            `json:"v"`
	Mid  string                     `json:"mid"`
	W    []json.RawMessage          `json:"w"`
	M    map[string]json.RawMessage `json:"m"`
	Post []int                      `json:"post"`
}

// iltFlat has no slice field, keeping the sweeps below independent of the
// slice-in-slice-of-struct tape-bind defect pinned by
// TestIltUVSliceFieldInSliceOfStruct.
type iltFlat struct {
	B int
	C string
}

type iltBig struct {
	X    []int                     `json:"x"`
	M    map[string]int            `json:"m"`
	MM   map[string]map[string]int `json:"mm"`
	SI   []iltFlat                 `json:"si"`
	MP   map[string]*iltFlat       `json:"mp"`
	PP   **iltFlat                 `json:"pp"`
	SP   []*iltFlat                `json:"sp"`
	Any  any                       `json:"any"`
	Tail string                    `json:"tail"`
}

type iltInnerCase struct {
	Tag string                 `json:"tag"`
	Env variantEnvelopeSibling `json:"env"`
	N   []int                  `json:"n"`
}

type iltValCase struct {
	ID int         `json:"id"`
	V  value.Value `json:"v"`
}

type iltEnv struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[iltEnv, struct {
		_ iltInnerCase               `case:"inner"`
		_ []variantEnvelopeSibling   `case:"list"`
		_ map[string]variantUser     `case:"users"`
		_ string                     `case:"str"`
		_ iltValCase                 `case:"val"`
		_ []variantEnvelopeNonStruct `case:"nlist"`
	}]()
}

type iltPolyHost struct {
	Pre  int                      `json:"pre"`
	S    []variantEnvelopeSibling `json:"s"`
	NS   variantEnvelopeNonStruct `json:"ns"`
	PM   []variantEnvelopePtrMap  `json:"pm"`
	O    variantEnvelopeOuter     `json:"o"`
	K    []kindofEnvelopeMixed    `json:"k"`
	E    iltEnv                   `json:"e"`
	Post string                   `json:"post"`
}

// --- corpora ---

func iltRepeat(n int, f func(i int) string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = f(i)
	}
	return strings.Join(parts, ",")
}

var iltValHostGood = []string{
	`{"pre":1,"v":{"a":[1,2,{"b":null}],"c":"s"},"mid":"m","w":[1,"x",[2],{"k":3}],"m":{"a":{"x":1},"b":[1,2]},"post":[1,2,3]}`,
	`{"post":[9],"m":{},"w":[],"v":[[],{}],"mid":"","pre":-3}`,
	`{"v":"str\u00e9\n","w":[null,true,1.5e300,"s"],"m":{"z":null}}`,
	`{"v":{"deep":{"deeper":[[[{"x":1}]]]}},"w":[[[]],{"a":{"b":{}}}],"post":[]}`,
}

func iltValHostBig() string {
	return `{"pre":7,"v":{"k":[` + iltRepeat(60, func(i int) string { return fmt.Sprintf(`{"i":%d,"s":"s%d"}`, i, i) }) +
		`]},"mid":"mid","w":[` + iltRepeat(120, func(i int) string { return fmt.Sprintf(`{"w":[%d,"%d"]}`, i, i) }) +
		`],"m":{` + iltRepeat(40, func(i int) string { return fmt.Sprintf(`"k%d":{"a":[%d]}`, i, i) }) +
		`},"post":[` + iltRepeat(200, func(i int) string { return fmt.Sprint(i) }) + `]}`
}

func iltNest(n int, leaf string) string {
	return strings.Repeat("[", n) + leaf + strings.Repeat("]", n)
}

// iltDepthDocs places deeply nested arrays inside Value positions, with
// siblings before and after, around the depth cap.
func iltDepthDocs() []string {
	var out []string
	for _, n := range []int{240, 250, 252, 253, 254, 255, 256, 300} {
		d := iltNest(n, "1")
		out = append(out,
			`{"pre":1,"v":`+d+`,"mid":"m","post":[1]}`,
			`{"pre":1,"w":[1,`+d+`],"mid":"m","post":[1]}`,
			`{"pre":1,"m":{"a":1,"b":`+d+`},"mid":"m","post":[1]}`,
			`{"v":`+d+`,"post":"bad"}`,
			`{"post":"bad","v":`+d+`}`,
		)
	}
	return out
}

var iltBigDocs = func() []string {
	big := `{"x":[` + iltRepeat(300, func(i int) string { return fmt.Sprint(i) }) +
		`],"m":{` + iltRepeat(70, func(i int) string { return fmt.Sprintf(`"k%d":%d`, i, i) }) +
		`},"mm":{` + iltRepeat(40, func(i int) string {
		return fmt.Sprintf(`"o%d":{`, i) + iltRepeat(4, func(j int) string { return fmt.Sprintf(`"i%d":%d`, j, i*j) }) + `}`
	}) +
		`},"si":[` + iltRepeat(40, func(i int) string { return fmt.Sprintf(`{"B":%d,"C":"c%d"}`, i, i) }) +
		`],"mp":{` + iltRepeat(36, func(i int) string {
		if i%7 == 3 {
			return fmt.Sprintf(`"p%d":null`, i)
		}
		return fmt.Sprintf(`"p%d":{"B":%d}`, i, i)
	}) +
		`},"pp":{"B":5,"C":"p"},"sp":[` + iltRepeat(30, func(i int) string { return fmt.Sprintf(`{"B":%d}`, i) }) +
		`],"any":{"a":[1,2,{"b":"c"}]},"tail":"t"}`
	return []string{
		big,
		`{"x":[1,2],"m":{"a":1},"mm":{"o":{"i":1}},"si":[{"B":1}],"mp":{"p":{"B":2}},"pp":null,"sp":[null,{"B":1}],"any":[1],"tail":"t"}`,
		`{"tail":"only"}`,
	}
}()

var iltPolyGood = []string{
	`{"pre":1,"s":[{"type":"user","data":{"name":"A","role":"r"}},{"data":{"title":"T","price":1},"type":"product"}],"post":"p"}`,
	`{"ns":{"type":"ints","data":[1,2,3]},"pm":[{"type":"counts","data":{"a":1}},{"data":{"name":"G","role":"x"},"type":"ptruser"}],"post":"p"}`,
	`{"o":{"type":"wrap","data":{"type":"user","data":{"name":"C","role":"o"}}},"k":[{"data":true},{"data":{"name":"B","role":"u"}},{"data":[{"name":"L","role":"m"}]},{"data":"s"},{"data":1.5}]}`,
	`{"e":{"type":"inner","data":{"tag":"t","env":{"type":"user","data":{"name":"Z","role":"q"}},"n":[1,2]}},"post":"p"}`,
	`{"e":{"data":[{"type":"user","data":{"name":"A","role":"r"}},{"data":null,"type":"user"}],"type":"list"},"pre":2}`,
	`{"e":{"type":"users","data":{"a":{"name":"A","role":"r"},"b":{"name":"B"}}}}`,
	`{"e":{"data":"hello","type":"str"},"post":"p"}`,
	`{"e":{"type":"val","data":{"id":4,"v":{"x":[1,2,{"y":null}]}}},"post":"p"}`,
	`{"e":{"data":{"v":[1,"a"],"id":9},"type":"val"},"post":"p"}`,
	`{"e":{"type":"nlist","data":[{"type":"ints","data":[1]},{"data":{"counts":{"a":1},"label":"l"},"type":"mapstruct"}]}}`,
}

func iltPolyBig() string {
	return `{"s":[` + iltRepeat(90, func(i int) string {
		if i%2 == 0 {
			return fmt.Sprintf(`{"type":"user","data":{"name":"n%d","role":"r"}}`, i)
		}
		return fmt.Sprintf(`{"data":{"title":"t%d","price":%d},"type":"product"}`, i, i)
	}) + `],"ns":{"type":"slicestruct","data":{"items":[` + iltRepeat(400, func(i int) string { return fmt.Sprint(i) }) +
		`],"tags":[` + iltRepeat(60, func(i int) string { return fmt.Sprintf(`"t%d"`, i) }) + `]}},"pm":[` +
		iltRepeat(50, func(i int) string {
			if i%2 == 0 {
				return `{"type":"counts","data":{` + iltRepeat(40, func(j int) string { return fmt.Sprintf(`"c%d":%d`, j, j) }) + `}}`
			}
			return fmt.Sprintf(`{"data":{"name":"g%d","role":"x"},"type":"ptruser"}`, i)
		}) + `],"k":[` + iltRepeat(60, func(i int) string {
		switch i % 3 {
		case 0:
			return fmt.Sprintf(`{"data":{"name":"k%d","role":"r"}}`, i)
		case 1:
			return `{"data":[{"name":"a","role":"b"},{"name":"c","role":"d"}]}`
		}
		return fmt.Sprintf(`{"data":%d}`, i)
	}) + `],"e":{"type":"list","data":[` + iltRepeat(70, func(i int) string {
		return fmt.Sprintf(`{"type":"user","data":{"name":"e%d","role":"r"}}`, i)
	}) + `]},"post":"p"}`
}

// iltPolyExtra holds hand-written envelope failures: unknown, missing,
// wrongly typed, duplicated discriminators and cold-case body errors.
var iltPolyExtra = []string{
	`{"s":[{"type":"nope","data":{"name":"A"}}]}`,
	`{"s":[{"data":{"name":"A"},"type":"nope"}]}`,
	`{"s":[{"data":{"name":"A"}}]}`,
	`{"s":[{"type":1,"data":{"name":"A"}}]}`,
	`{"s":[{"data":{"name":"A"},"type":1}]}`,
	`{"s":[{"type":null,"data":{"name":"A"}}]}`,
	`{"s":[{"type":["user"],"data":{"name":"A"}}]}`,
	`{"s":[{"type":"user","type":"product","data":{"title":"x"}}]}`,
	`{"s":[{"type":"user","data":{"name":1}}]}`,
	`{"s":[{"data":{"name":1},"type":"user"}]}`,
	`{"s":[{"type":"user","data":{"name":"A","role":}}]}`,
	`{"s":[{"data":{"name":"A","role":},"type":"user"}]}`,
	`{"s":[{"type":"user","data":[1,2]}]}`,
	`{"s":[{"data":"x","type":"product"}]}`,
	`{"s":[{"type":"user","data":{"name":"A"}},{"type":"nope","data":1}]}`,
	`{"ns":{"type":"ints","data":[1,"x",3]}}`,
	`{"ns":{"data":[1,"x",3],"type":"ints"}}`,
	`{"ns":{"data":[1,2,3,,],"type":"ints"}}`,
	`{"ns":{"type":"mapstruct","data":{"counts":{"a":"x"},"label":"l"}}}`,
	`{"ns":{"data":{"counts":{"a":1,"b":[]},"label":"l"},"type":"mapstruct"}}`,
	`{"pm":[{"type":"counts","data":{"a":1,"b":true}}]}`,
	`{"pm":[{"data":{"a":1,"b":true},"type":"counts"}]}`,
	`{"pm":[{"type":"ptruser","data":{"name":[]}}]}`,
	`{"pm":[{"data":{"name":[]},"type":"ptruser"}]}`,
	`{"o":{"type":"wrap","data":{"type":"nope","data":{}}}}`,
	`{"o":{"type":"wrap","data":{"data":{"name":1},"type":"user"}}}`,
	`{"o":{"data":{"type":"user","data":{"name":1}},"type":"wrap"}}`,
	`{"k":[{"data":{"name":1}}]}`,
	`{"k":[{"data":[{"name":"a"},{"name":2}]}]}`,
	`{"k":[{"data":[{"name":"a"},1]}]}`,
	`{"k":[{"data":null}]}`,
	`{"e":{"type":"inner","data":{"tag":"t","env":{"type":"nope"},"n":[1]}}}`,
	`{"e":{"type":"inner","data":{"tag":1,"env":{"type":"user","data":{}},"n":[1]}}}`,
	`{"e":{"data":{"tag":"t","env":{"data":{"name":2},"type":"user"},"n":[1]},"type":"inner"}}`,
	`{"e":{"type":"list","data":[{"type":"user","data":{"name":"A"}},{"type":"user","data":{"name":5}}]}}`,
	`{"e":{"data":[{"type":"user","data":{"name":"A"}},{"data":{"name":5},"type":"user"}],"type":"list"}}`,
	`{"e":{"type":"users","data":{"a":{"name":"A"},"b":{"name":3}}}}`,
	`{"e":{"type":"val","data":{"id":"x","v":{"a":1}}}}`,
	`{"e":{"data":{"v":{"a":[1,2,,]},"id":1},"type":"val"}}`,
	`{"e":{"type":"val","data":{"id":1,"v":` + iltNest(300, "1") + `}}}`,
	`{"e":{"type":"nlist","data":[{"type":"ints","data":[1]},{"type":"ints","data":["a"]}]}}`,
	`{"e":{"type":"str","data":5}}`,
	`{"e":{"data":5,"type":"str"}}`,
	`{"e":{"type":"inner","data":` + strings.Repeat(`{"tag":"t","env":`, 1) + `{"type":"user","data":{"name":"x"}}` + `,"n":[1}}}`,
}

// --- sweep machinery ---

type iltRetained struct {
	in  string
	out ilOutcome
}

// iltSample picks n of total indices spread evenly, or all of them when
// total is at most n.
func iltSample(total, n int) func(int) bool {
	if total <= n {
		return ilPickAll
	}
	keep := make(map[int]bool, n)
	stride := float64(total) / float64(n)
	for i := range n {
		keep[int(float64(i)*stride)] = true
	}
	return func(i int) bool { return keep[i] }
}

// iltMutantsOf samples perDoc of each good document's mutants followed by
// its type mutants, building only the sampled ones.
func iltMutantsOf(good []string, perDoc int) []string {
	if m, err := strconv.Atoi(os.Getenv("ILT_MULT")); err == nil && m > 1 {
		perDoc *= m
	}
	perDoc = max(1, perDoc/ilSweepEvery)
	none := func(int) bool { return false }
	var out []string
	for _, g := range good {
		_, nm := ilMutantsPick(g, none)
		_, nt := ilTypeMutantsPick(g, none)
		keep := iltSample(nm+nt, perDoc)
		ms, _ := ilMutantsPick(g, keep)
		ts, _ := ilTypeMutantsPick(g, func(i int) bool { return keep(nm + i) })
		out = append(append(out, ms...), ts...)
	}
	return out
}

// iltAt shows a and b around their first difference.
func iltAt(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(0, i-40)
	return fmt.Sprintf("first diff at byte %d (len %d vs %d)\n   A ...%.100s\n   B ...%.100s", i, len(a), len(b), a[lo:], b[lo:])
}

// iltRetainedCheck re-marshals every retained destination. A later parse that
// clobbers an arena, string, or tape of an earlier successful result shows up
// as a changed marshal output (or a crash).
func iltRetainedCheck(t *testing.T, kept []iltRetained, when string) bool {
	t.Helper()
	for i, r := range kept {
		js, _ := json.Marshal(r.out.val)
		if string(js) != r.out.json {
			t.Errorf("%s: retained result #%d corrupted by later parses\n input: %.200q\n %s",
				when, i, r.in, iltAt(r.out.json, string(js)))
			return false
		}
	}
	return true
}

// iltSweep runs mutants and good documents through one dirty Parser and
// compares each outcome to a fresh Parser. Successful outcomes are retained
// and re-verified after GC. std, when set, is an encoding/json oracle for
// successful parses.
func iltSweep[T any](t *testing.T, good, extra []string, perDoc int, std func(in string, got any) string) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	mutants := append(iltMutantsOf(good, perDoc), extra...)
	dirty, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	var kept []iltRetained
	var fails []ilFail
	seen := map[string]bool{}
	var hist []ilStep
	stdSeen := map[string]bool{}
	check := func(in string, oi int) {
		opts := ilOptSets[oi]
		hist = append(hist, ilStep{in, oi})
		if len(hist) > 300 {
			hist = hist[len(hist)-300:]
		}
		got := ilRun[T](dirty, in, opts)
		fp, _ := NewParser[T]()
		want := ilRun[T](fp, in, opts)
		if !ilSame(want, got, false) {
			key := want.err + "|" + got.err
			if !seen[key] {
				seen[key] = true
				fails = append(fails, ilFail{ilMinimize[T](hist, false), want, got})
			}
		}
		if got.err == "" && !strings.Contains(got.json, "|marshal-err") {
			kept = append(kept, iltRetained{in, got})
			if len(kept) > 250 {
				kept = kept[1:]
			}
			if std != nil && oi == 0 {
				if d := std(in, got.val); d != "" && !stdSeen[d[:min(len(d), 40)]] {
					stdSeen[d[:min(len(d), 40)]] = true
					t.Errorf("std differential on success: %s\n input: %.300q", d, in)
				}
			}
		}
	}
	for i, m := range mutants {
		if len(fails) >= 5 {
			break
		}
		check(m, i%len(ilOptSets))
		check(good[i%len(good)], (i+1)%len(ilOptSets))
		if i%5 == 0 {
			check(good[(i*7+1)%len(good)], (i+2)%len(ilOptSets))
		}
		if i%61 == 0 {
			runtime.GC()
			if !iltRetainedCheck(t, kept, fmt.Sprintf("after mutant #%d", i)) {
				break
			}
		}
	}
	runtime.GC()
	iltRetainedCheck(t, kept, "final")
	for _, f := range fails {
		var sb strings.Builder
		for i, s := range f.chain {
			fmt.Fprintf(&sb, "\n   step%d opts#%d %.300q", i, s.oi, s.in)
		}
		if f.chain == nil {
			sb.WriteString("\n   (not reproducible by replay: GC- or pool-dependent)")
		}
		t.Errorf("history dependence, minimal chain:%s\n fresh: err=%s\n dirty: err=%s\n %s",
			sb.String(), f.want.err, f.got.err, ilDiff(f.want, f.got))
	}
}

func iltAnyOf(b []byte) any {
	var a any
	d := json.NewDecoder(strings.NewReader(string(b)))
	if err := d.Decode(&a); err != nil {
		return "undecodable:" + err.Error()
	}
	return a
}

func iltValHostStdCheck(in string, got any) string {
	// Control characters inside strings are accepted by design.
	if strings.IndexFunc(in, func(r rune) bool { return r < 0x20 && r != '\n' && r != ' ' }) >= 0 {
		return ""
	}
	var s iltValHostStd
	if err := json.Unmarshal([]byte(in), &s); err != nil {
		return fmt.Sprintf("std rejects but vjson accepts: %v", err)
	}
	sj, _ := json.Marshal(&s)
	gj, _ := json.Marshal(got)
	if !reflect.DeepEqual(iltAnyOf(sj), iltAnyOf(gj)) {
		return fmt.Sprintf("content differs\n  std:   %.300s\n  vjson: %.300s", sj, gj)
	}
	return ""
}

func TestIltValueHostSweep(t *testing.T) {
	good := append(append([]string{}, iltValHostGood...), iltValHostBig())
	iltSweep[iltValHost](t, good, iltDepthDocs(), 160, iltValHostStdCheck)
}

func TestIltBigSweep(t *testing.T) {
	iltSweep[iltBig](t, iltBigDocs, nil, 220, nil)
}

func TestIltPolyHostSweep(t *testing.T) {
	good := append(append([]string{}, iltPolyGood...), iltPolyBig())
	iltSweep[iltPolyHost](t, good, iltPolyExtra, 120, nil)
}

func TestIltPolyEnvelopeSweeps(t *testing.T) {
	big := iltPolyBig()
	_ = big
	t.Run("sibling", func(t *testing.T) {
		iltSweep[variantEnvelopeSibling](t, siblingVariantInputs, []string{
			`{"type":"nope","data":{}}`, `{"data":{},"type":"nope"}`, `{"type":1,"data":{}}`, `{"data":{},"type":1}`,
			`{"type":null,"data":{"name":"A"}}`, `{"data":{"name":"A"},"type":null}`, `{"data":{"name":"A"}}`, `{"type":"user"}`,
		}, 400, nil)
	})
	t.Run("nonstruct", func(t *testing.T) { iltSweep[variantEnvelopeNonStruct](t, nonStructVariantInputs, nil, 400, nil) })
	t.Run("ptrmap", func(t *testing.T) { iltSweep[variantEnvelopePtrMap](t, ptrMapVariantInputs, nil, 400, nil) })
	t.Run("outer", func(t *testing.T) { iltSweep[variantEnvelopeOuter](t, outerVariantInputs, nil, 400, nil) })
	t.Run("kindof", func(t *testing.T) { iltSweep[kindofEnvelopeMixed](t, kindofInputs, nil, 400, nil) })
	t.Run("kindofptr", func(t *testing.T) {
		iltSweep[kindofEnvelopePointer](t, []string{`{"data":{"name":"a","role":"b"}}`, `{"data":null}`}, nil, 400, nil)
	})
	t.Run("env", func(t *testing.T) { iltSweep[iltEnv](t, []string{iltPolyGood[3], iltPolyGood[4]}, nil, 400, nil) })
}

// --- UnmarshalValue (tape-bind) sweeps ---

type iltUVRun struct {
	err string
	js  string
	val any
}

func iltUV[T any](p *Parser, v value.Value, opts []UnmarshalOption) (out iltUVRun) {
	defer func() {
		if r := recover(); r != nil {
			out = iltUVRun{err: fmt.Sprintf("PANIC: %v", r)}
		}
	}()
	d := new(T)
	err := p.UnmarshalValue(v, d, opts...)
	out.err = ilDescribeErr(err)
	js, jerr := json.Marshal(d)
	out.js = string(js)
	if jerr != nil {
		out.js += "|marshal-err:" + jerr.Error()
	}
	out.val = d
	return out
}

var iltUVOpts = [][]UnmarshalOption{
	nil,
	{vopt.UseNumber(true)},
	{vopt.ZeroCopy(false)},
	{vopt.SkipLenient(true)},
}

// iltUVSweep parses each mutant with dom.Parse, binds it through a dirty
// Parser alternating UnmarshalValue and Unmarshal, and compares with a fresh
// Parser. The error class must also agree with the contiguous Unmarshal path.
func iltUVSweep[T any](t *testing.T, good, extra []string, perDoc int) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	mutants := append(iltMutantsOf(good, perDoc), extra...)
	dirty, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	type keptUV struct {
		in  string
		out iltUVRun
	}
	var kept []keptUV
	reported := map[string]int{}
	report := func(kind, in string, args ...any) {
		reported[kind]++
		if reported[kind] <= 2 {
			t.Errorf("%s: input=%.300q\n %s", kind, in, fmt.Sprint(args...))
		}
	}
	step := 0
	bindOne := func(in string, oi int) {
		step++
		opts := iltUVOpts[oi]
		// Control characters inside strings are accepted by design.
		if strings.IndexFunc(in, func(r rune) bool { return r < 0x20 && r != '\n' && r != '\t' && r != '\r' }) >= 0 {
			return
		}
		val, perr := dom.Parse([]byte(in))
		if perr != nil {
			return
		}
		iltLogStep(in, oi)
		got := iltUV[T](dirty, val, opts)
		fp, _ := NewParser[T]()
		want := iltUV[T](fp, val, opts)
		if want.err != got.err || (want.err == "" && want.js != got.js) {
			report("UnmarshalValue history dependence", in, fmt.Sprintf("opts#%d step %d fresh err=%q dirty err=%q\n   A=fresh B=dirty %s", oi, step, want.err, got.err, iltAt(want.js, got.js)))
		}
		// Class agreement with the contiguous path on the same dirty Parser.
		cont := ilRun[T](dirty, in, opts)
		if ilClass(cont.err) != ilClass(got.err) && !strings.HasPrefix(got.err, "*bind.TapeBindUnsupported") && !strings.Contains(got.err, "unsupported target type") {
			report("tape-bind vs contiguous class", in, fmt.Sprintf("opts#%d\n Unmarshal: %s\n UnmarshalValue: %s", oi, cont.err, got.err))
		} else if cont.err == "" && got.err == "" && cont.json != got.js {
			report("tape-bind vs contiguous result", in, fmt.Sprintf("opts#%d\n   A=Unmarshal B=UnmarshalValue %s", oi, iltAt(cont.json, got.js)))
		}
		if got.err == "" && !strings.Contains(got.js, "|marshal-err") {
			kept = append(kept, keptUV{in, got})
			if len(kept) > 200 {
				kept = kept[1:]
			}
		}
	}
	for i, m := range mutants {
		bindOne(m, i%len(iltUVOpts))
		bindOne(good[i%len(good)], (i+1)%len(iltUVOpts))
		if i%53 == 0 {
			runtime.GC()
			for j, k := range kept {
				js, _ := json.Marshal(k.out.val)
				if string(js) != k.out.js {
					t.Errorf("retained UnmarshalValue result #%d corrupted (after mutant #%d)\n input: %.200q\n %s", j, i, k.in, iltAt(k.out.js, string(js)))
					return
				}
			}
		}
	}
}

func TestIltUnmarshalValueBigSweep(t *testing.T) {
	iltUVSweep[iltBig](t, iltBigDocs, nil, 200)
}

// iltPlain mirrors ilPlain with iltFlat in place of ilInner.
type iltPlain struct {
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
	MS  map[string]iltFlat
	MP  map[string]*iltFlat
	N   iltFlat
	P   *int
	PP  **iltFlat
	PS  []*int
	SS  []string
	SI  []iltFlat
	Any any
	AL  []any
	Num json.Number
	By  []byte
	Q   int     `json:",string"`
	QS  string  `json:",string"`
	QF  float64 `json:",string"`
	Ren int     `json:"renamed"`
}

var iltDField = regexp.MustCompile(`,"D":\[[^\]]*\]|"D":\[[^\]]*\],?`)

func iltPlainDocs() []string {
	var out []string
	for _, d := range ilPlainGood {
		out = append(out, iltDField.ReplaceAllString(d, ""))
	}
	return out
}

func TestIltUnmarshalValuePlainSweep(t *testing.T) {
	iltUVSweep[iltPlain](t, iltPlainDocs(), nil, 200)
}

func TestIltUnmarshalValueValueHostSweep(t *testing.T) {
	good := append(append([]string{}, iltValHostGood...), iltValHostBig())
	iltUVSweep[iltValHost](t, good, nil, 120)
}

// iltPolyHostU avoids slices nested in slice elements, which the tape-bind
// walk binds incorrectly (see TestIltUVSliceFieldInSliceOfStruct and
// TestIltUVKindofArrayCaseInSlice).
type iltPolyHostU struct {
	Pre  int                      `json:"pre"`
	S    []variantEnvelopeSibling `json:"s"`
	NS   variantEnvelopeNonStruct `json:"ns"`
	PM   variantEnvelopePtrMap    `json:"pm"`
	O    variantEnvelopeOuter     `json:"o"`
	K    kindofEnvelopeMixed      `json:"k"`
	E    iltEnv                   `json:"e"`
	Post string                   `json:"post"`
}

var iltPolyUVGood = []string{
	iltPolyGood[0],
	`{"ns":{"type":"ints","data":[1,2,3]},"pm":{"type":"counts","data":{"a":1}},"post":"p"}`,
	`{"pm":{"data":{"name":"G","role":"x"},"type":"ptruser"},"post":"p"}`,
	`{"o":{"type":"wrap","data":{"type":"user","data":{"name":"C","role":"o"}}},"k":{"data":[{"name":"L","role":"m"}]}}`,
	`{"k":{"data":true}}`, `{"k":{"data":{"name":"B","role":"u"}}}`,
	iltPolyGood[3], iltPolyGood[4][:0] + `{"e":{"data":[{"type":"user","data":{"name":"A","role":"r"}},{"data":null,"type":"user"}],"type":"list"},"pre":2}`,
	iltPolyGood[5], iltPolyGood[6],
}

func TestIltUnmarshalValuePolySweep(t *testing.T) {
	iltUVSweep[iltPolyHostU](t, iltPolyUVGood, nil, 100)
}

func TestIltUVKindofArrayCaseInSlice(t *testing.T) {
	for n := 1; n <= 8; n++ {
		doc := "[" + iltRepeat(n, func(int) string { return `{"data":[{"name":"a","role":"b"},{"name":"c","role":"d"}]}` }) + "]"
		var want, got []kindofEnvelopeMixed
		if err := Unmarshal([]byte(doc), &want); err != nil {
			t.Fatal(err)
		}
		val, _ := dom.Parse([]byte(doc))
		if err := UnmarshalValue(val, &got); err != nil {
			t.Fatal(err)
		}
		for i := range want {
			if !reflect.DeepEqual(want[i], got[i]) {
				w := want[i].Data.([]kindofUser)
				g := got[i].Data.([]kindofUser)
				t.Errorf("%d elements: element %d array case: Unmarshal has len %d, UnmarshalValue has len %d", n, i, len(w), len(g))
				return
			}
		}
	}
}

// --- shape rotation through the package-level pooled entry points ---

type iltRot struct {
	name  string
	good  []string
	run   func(in string) ilOutcome
	fresh func(in string) ilOutcome
}

func iltRotOf[T any](name string, good []string) iltRot {
	return iltRot{
		name: name,
		good: good,
		run: func(in string) (out ilOutcome) {
			defer func() {
				if r := recover(); r != nil {
					out = ilOutcome{err: fmt.Sprintf("PANIC: %v", r)}
				}
			}()
			v := new(T)
			err := Unmarshal([]byte(in), v)
			out.err = ilDescribeErr(err)
			js, _ := json.Marshal(v)
			out.json = string(js)
			out.val = v
			return out
		},
		fresh: func(in string) ilOutcome {
			p, _ := NewParser[T]()
			return ilRun[T](p, in, nil)
		},
	}
}

// TestIltShapeRotation rotates several shapes through package-level
// Unmarshal, each fed failing mutants and good documents, and compares each
// result with a fresh Parser. Earlier successes are re-verified after GC.
func TestIltShapeRotation(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	rots := []iltRot{
		iltRotOf[iltValHost]("valhost", append(append([]string{}, iltValHostGood...), iltValHostBig())),
		iltRotOf[iltBig]("big", iltBigDocs),
		iltRotOf[iltPolyHost]("poly", append(append([]string{}, iltPolyGood...), iltPolyBig())),
		iltRotOf[iltPlain]("plain", iltPlainDocs()),
		iltRotOf[[]ilItem]("items", ilItemsGood),
		iltRotOf[variantEnvelopeNonStruct]("nonstruct", nonStructVariantInputs),
		iltRotOf[kindofEnvelopeMixed]("kindof", kindofInputs),
	}
	muts := make([][]string, len(rots))
	for i, r := range rots {
		muts[i] = iltMutantsOf(r.good, 90)
	}
	var kept []iltRetained
	bad := 0
	for round := 0; round < 90 && bad < 5; round++ {
		for ri, r := range rots {
			m := muts[ri][(round*7+ri)%len(muts[ri])]
			for _, in := range []string{m, r.good[round%len(r.good)]} {
				got, want := r.run(in), r.fresh(in)
				if !ilSame(want, got, false) {
					bad++
					t.Errorf("rotation %s round %d: fresh=%s dirty=%s\n input: %.300q", r.name, round, want.err, got.err, in)
				} else if got.err == "" && !strings.Contains(got.json, "|marshal-err") {
					kept = append(kept, iltRetained{in, got})
				}
			}
		}
		if round%6 == 0 {
			runtime.GC()
			if !iltRetainedCheck(t, kept, fmt.Sprintf("rotation round %d", round)) {
				return
			}
			if len(kept) > 300 {
				kept = kept[len(kept)-300:]
			}
		}
	}
}

// --- targeted scenarios ---

// TestIltValueSurvivesFailingParses keeps the Value of a good parse alive and
// navigates it after many failing and succeeding parses on the same Parser.
func TestIltValueSurvivesFailingParses(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	p, err := NewParser[iltValHost]()
	if err != nil {
		t.Fatal(err)
	}
	var firsts []*iltValHost
	var snaps []string
	for _, g := range iltValHostGood {
		v := new(iltValHost)
		if err := p.Unmarshal([]byte(g), v); err != nil {
			t.Fatalf("good doc failed: %v\n %s", err, g)
		}
		js, _ := json.Marshal(v)
		firsts = append(firsts, v)
		snaps = append(snaps, string(js))
	}
	bigDoc := iltValHostBig()
	for round := range 40 {
		for _, in := range []string{
			`{"pre":1,"v":{"a":[1,2,` + strings.Repeat("x", round*3),
			`{"pre":1,"v":{"a":[1,2,3]},"mid":5}`,
			`{"pre":1,"v":` + iltNest(255+round, "1") + `}`,
			`{"pre":"x","v":{"q":"` + strings.Repeat("s", 50+round*17) + `"},"w":[1,2,` + iltNest(300, "0") + `]}`,
			bigDoc[:len(bigDoc)/2+round*13],
			`{"v":{"a":1},"post":[1,"x"]}`,
		} {
			_ = p.Unmarshal([]byte(in), new(iltValHost))
			g := new(iltValHost)
			if err := p.Unmarshal([]byte(bigDoc), g); err != nil {
				t.Fatalf("round %d: good big doc failed after failing parse %.80q: %v", round, in, err)
			}
		}
		runtime.GC()
		for i, v := range firsts {
			js, _ := json.Marshal(v)
			if string(js) != snaps[i] {
				t.Fatalf("round %d: result of good doc #%d corrupted\n  was: %.200s\n  now: %.200s", round, i, snaps[i], js)
			}
		}
	}
}

// --- native crash probes ---

// iltProbe runs body in a child test process so that a native fault is
// reported as one failing test instead of ending the whole package run.
func iltProbe(t *testing.T, body func()) {
	t.Helper()
	if os.Getenv("ILT_PROBE") == t.Name() {
		body()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ILT_PROBE="+t.Name())
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	var keep []string
	for l := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(l, "fatal error") || strings.HasPrefix(l, "panic") || strings.HasPrefix(l, "[signal") ||
			strings.Contains(l, "/vbind/") || strings.Contains(l, "/decode/bind/") && !strings.Contains(l, "_test.go") {
			keep = append(keep, strings.TrimSpace(l))
		}
		if len(keep) >= 8 {
			break
		}
	}
	t.Errorf("child process died: %v\n%s", err, strings.Join(keep, "\n"))
}

type iltMapSlice struct {
	MM map[string]map[string]int `json:"mm"`
	MS map[string][]string       `json:"ms"`
}

func iltMapSliceDoc(nmm, nms int) string {
	return `{"mm":{` + iltRepeat(nmm, func(i int) string {
		return fmt.Sprintf(`"o%d":{`, i) + iltRepeat(4, func(j int) string { return fmt.Sprintf(`"i%d":%d`, j, i*j) }) + `}`
	}) + `},"ms":{` + iltRepeat(nms, func(i int) string { return fmt.Sprintf(`"s%d":["a","b%d"]`, i, i) }) + `}}`
}

// A tiny successful UnmarshalValue followed by a large one on the same Parser
// must behave like the large one alone. The native tape-bind walk faults in
// ServeSliceGrow when the larger document binds a 34-entry map[string][]string
// after a map[string][]string of one entry.
func TestIltUVMapOfSliceAfterSmallParseCrash(t *testing.T) {
	iltProbe(t, func() {
		p, _ := NewParser[iltMapSlice]()
		small, _ := dom.Parse([]byte(`{"ms":{"s":["a"]}}`))
		big, _ := dom.Parse([]byte(iltMapSliceDoc(34, 34)))
		var a, b iltMapSlice
		if err := p.UnmarshalValue(small, &a); err != nil {
			t.Fatal(err)
		}
		if err := p.UnmarshalValue(big, &b); err != nil {
			t.Fatal(err)
		}
		if len(b.MS) != 34 {
			t.Fatalf("len(MS)=%d", len(b.MS))
		}
	})
}

// Slice fields of consecutive struct elements bound through the tape-bind
// walk must keep their own length.
func TestIltUVSliceFieldInSliceOfStruct(t *testing.T) {
	type elem struct{ D []float64 }
	for n := 1; n <= 8; n++ {
		doc := "[" + iltRepeat(n, func(int) string { return `{"D":[1,2]}` }) + "]"
		var want []elem
		if err := json.Unmarshal([]byte(doc), &want); err != nil {
			t.Fatal(err)
		}
		val, err := dom.Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		var got []elem
		if err := UnmarshalValue(val, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("UnmarshalValue of %s\n want %v\n  got %v", doc, want, got)
			return
		}
	}
}

var iltLog *os.File

func iltLogStep(in string, oi int) {
	if p := os.Getenv("ILT_LOG"); p != "" {
		if iltLog == nil {
			iltLog, _ = os.Create(p)
		}
		b, _ := json.Marshal([]any{in, oi})
		iltLog.Write(append(b, '\n'))
	}
}

type iltCaseHost struct {
	E iltEnv                 `json:"e"`
	S variantEnvelopeSibling `json:"s"`
}

// A Value bound inside a variant case by UnmarshalValue is a result the
// caller keeps. Later parses on the same Parser must leave it intact.
func TestIltUVValueInCaseSurvivesLaterParse(t *testing.T) {
	docs := []string{
		`{"e":{"data":{"v":[1,"a"],"id":9},"type":"val"}}`,
		`{"e":{"type":"val","data":{"id":9,"v":[1,"a"]}}}`,
	}
	later := []string{
		`{"s":{"data":{"price":1},"type":"product"}}`,
		`{"s":{"type":"user","data":{"name":"x"}}}`,
		`{"e":{"type":"str","data":"zzzzzzzz"}}`,
	}
	for _, d1 := range docs {
		for _, d2 := range later {
			for _, laterViaTape := range []bool{false, true} {
				p, _ := NewParser[iltCaseHost]()
				v1, _ := dom.Parse([]byte(d1))
				var a, b iltCaseHost
				if err := p.UnmarshalValue(v1, &a); err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(&a)
				if laterViaTape {
					v2, _ := dom.Parse([]byte(d2))
					if err := p.UnmarshalValue(v2, &b); err != nil {
						t.Fatal(err)
					}
				} else if err := p.Unmarshal([]byte(d2), &b); err != nil {
					t.Fatal(err)
				}
				after, _ := json.Marshal(&a)
				if string(before) != string(after) {
					t.Errorf("first=UnmarshalValue(%s) then %s (tape=%v)\n  before: %s\n  after:  %s", d1, d2, laterViaTape, before, after)
				}
			}
		}
	}
}

// UnmarshalValue reports unknown members under RejectUnknownMembers like
// Unmarshal does.
func TestIltUVRejectUnknownMembers(t *testing.T) {
	type T struct {
		A int
		N struct{ B int }
		L []struct{ C int }
	}
	for _, doc := range []string{`{"A":1,"zz":2}`, `{"N":{"B":1,"zz":2}}`, `{"L":[{"C":1,"zz":2}]}`} {
		var a, b T
		errC := Unmarshal([]byte(doc), &a, vopt.RejectUnknownMembers(true))
		val, _ := dom.Parse([]byte(doc))
		errV := UnmarshalValue(val, &b, vopt.RejectUnknownMembers(true))
		if (errC == nil) != (errV == nil) {
			t.Errorf("%s with RejectUnknownMembers: Unmarshal err=%v, UnmarshalValue err=%v", doc, errC, errV)
		}
	}
}

// A document whose root kind does not fit the destination is a type mismatch
// for UnmarshalValue as it is for Unmarshal.
func TestIltUVRootKindMismatchIdentity(t *testing.T) {
	type T struct{ A int }
	for _, doc := range []string{`"x"`, `7`, `true`} {
		var a, b T
		errC := Unmarshal([]byte(doc), &a)
		val, _ := dom.Parse([]byte(doc))
		errV := UnmarshalValue(val, &b)
		if ilClass(ilDescribeErr(errC)) != ilClass(ilDescribeErr(errV)) {
			t.Errorf("%s into struct: Unmarshal gives %s, UnmarshalValue gives %s", doc, ilDescribeErr(errC), ilDescribeErr(errV))
		}
	}
}

// --- failures positioned inside long poly arrays ---

func iltNonStructElem(i int, discFirst bool) string {
	var typ, data string
	switch i % 3 {
	case 0:
		typ, data = "ints", `[`+iltRepeat(20+i%7, func(j int) string { return fmt.Sprint(j) })+`]`
	case 1:
		typ, data = "slicestruct", `{"items":[`+iltRepeat(30, func(j int) string { return fmt.Sprint(j * i) })+`],"tags":["a","bb","ccc"]}`
	default:
		typ, data = "mapstruct", `{"counts":{`+iltRepeat(12, func(j int) string { return fmt.Sprintf(`"c%d":%d`, j, j) })+`},"label":"l"}`
	}
	if discFirst {
		return fmt.Sprintf(`{"type":%q,"data":%s}`, typ, data)
	}
	return fmt.Sprintf(`{"data":%s,"type":%q}`, data, typ)
}

func iltNonStructArray(n int, bad map[int]string) string {
	return "[" + iltRepeat(n, func(i int) string {
		if b, ok := bad[i]; ok {
			return b
		}
		return iltNonStructElem(i, i%4 < 2)
	}) + "]"
}

func TestIltPolyArrayFailureThenBlockFull(t *testing.T) {
	const n = 90
	faults := []string{
		`{"type":"nope","data":{"a":1}}`,
		`{"data":{"a":1},"type":"nope"}`,
		`{"type":"slicestruct","data":{"items":[1,"x"],"tags":[]}}`,
		`{"data":{"items":[1,"x"],"tags":[]},"type":"slicestruct"}`,
		`{"type":"slicestruct","data":{"items":[1,,2]}}`,
		`{"data":{"items":[1,,2]},"type":"slicestruct"}`,
		`{"type":"mapstruct","data":{"counts":{"a":"x"}}}`,
		`{"data":{"counts":{"a":1,"b":}},"type":"mapstruct"}`,
		`{"data":{"counts":{"a":1}}}`,
		`{"type":7,"data":[1]}`,
		`{"data":[1],"type":7}`,
		`{"type":"ints","data":{"a":1}}`,
	}
	var extra []string
	for _, k := range []int{0, 1, 2, 5, 17, 33, 34, 64, 89} {
		for _, f := range faults {
			extra = append(extra, iltNonStructArray(n, map[int]string{k: f}))
		}
	}
	good := iltNonStructArray(n, nil)
	extra = append(extra, good[:len(good)/2], good[:len(good)-1], good[:len(good)*3/4]+"x")
	iltSweep[[]variantEnvelopeNonStruct](t, []string{good, iltNonStructArray(3, nil), `[]`}, extra, 0, nil)
}

func TestIltUVPolyArrayFailureThenBlockFull(t *testing.T) {
	const n = 90
	extra := []string{}
	for _, k := range []int{0, 2, 17, 34, 64, 89} {
		for _, f := range []string{
			`{"type":"slicestruct","data":{"items":[1,"x"],"tags":[]}}`,
			`{"data":{"items":[1,"x"],"tags":[]},"type":"slicestruct"}`,
			`{"type":"mapstruct","data":{"counts":{"a":"x"}}}`,
			`{"type":"nope","data":1}`,
			`{"data":1}`,
		} {
			extra = append(extra, iltNonStructArray(n, map[int]string{k: f}))
		}
	}
	good := iltNonStructArray(n, nil)
	iltUVSweep[[]variantEnvelopeNonStruct](t, []string{good, iltNonStructArray(3, nil), `[]`}, extra, 0)
}

// --- Value elements in long slices and maps ---

type iltVE struct {
	ID   int         `json:"id"`
	V    value.Value `json:"v"`
	Tags []string    `json:"tags"`
}

type iltVPtr struct {
	P  *iltVE       `json:"p"`
	PV *value.Value `json:"pv"`
	L  []*iltVE     `json:"l"`
}

func iltVEDoc(i int) string {
	return fmt.Sprintf(`{"id":%d,"v":{"a":[%d,"s%d",{"b":null}],"c":"x%d"},"tags":["t%d","u"]}`, i, i, i, i, i)
}

func TestIltValueElemSweeps(t *testing.T) {
	slice := "[" + iltRepeat(70, iltVEDoc) + "]"
	m := "{" + iltRepeat(40, func(i int) string { return fmt.Sprintf(`"k%d":%s`, i, iltVEDoc(i)) }) + "}"
	t.Run("slice", func(t *testing.T) {
		iltSweep[[]iltVE](t, []string{slice, "[" + iltVEDoc(1) + "," + iltVEDoc(2) + "]", `[]`}, nil, 250, nil)
	})
	t.Run("map", func(t *testing.T) {
		iltSweep[map[string]iltVE](t, []string{m, `{"a":` + iltVEDoc(1) + `}`, `{}`}, nil, 250, nil)
	})
	t.Run("ptr", func(t *testing.T) {
		iltSweep[iltVPtr](t, []string{
			`{"p":` + iltVEDoc(1) + `,"pv":{"x":[1,2]},"l":[` + iltRepeat(40, iltVEDoc) + `]}`,
			`{"pv":null,"p":null,"l":[null,` + iltVEDoc(3) + `]}`, `{"pv":"s"}`,
		}, nil, 250, nil)
	})
}

// --- child Values bound through UnmarshalValue ---

type iltChild struct {
	B int
	C string
	M map[string]int
}

// iltChildren lists the sub-Values of v reachable through a few paths.
func iltChildren(v value.Value) []value.Value {
	var out []value.Value
	if a := v.Get("a"); a.Exists() {
		for i := 0; i < a.Len(); i++ {
			out = append(out, a.Index(i))
		}
	}
	if m := v.Get("m"); m.Exists() {
		m.ForEachKey(func(_ string, c value.Value) bool { out = append(out, c); return true })
	}
	return out
}

// TestIltUVChildValues binds sub-Values (selected by base, root and extent
// inside a shared tape) into typed destinations. Well-formed and mismatching
// children alternate on one Parser; each outcome is compared with the
// contiguous path on the child's own JSON text.
func TestIltUVChildValues(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	var docs []string
	var els []string
	for i := range 50 {
		switch i % 5 {
		case 0:
			els = append(els, fmt.Sprintf(`{"B":%d,"C":"c%d","M":{"x":%d}}`, i, i, i))
		case 1:
			els = append(els, fmt.Sprintf(`{"B":"bad%d","C":"c"}`, i))
		case 2:
			els = append(els, fmt.Sprintf(`{"B":%d,"M":{%s}}`, i, iltRepeat(40, func(j int) string { return fmt.Sprintf(`"k%d":%d`, j, j) })))
		case 3:
			els = append(els, fmt.Sprintf(`{"M":{"a":1,"b":"x%d"}}`, i))
		default:
			els = append(els, fmt.Sprintf(`[%d]`, i))
		}
	}
	docs = append(docs, `{"a":[`+strings.Join(els, ",")+`],"m":{"p":`+els[0]+`,"q":`+els[1]+`,"r":`+els[2]+`}}`)
	docs = append(docs, `{"a":[`+els[0]+`,`+els[0]+`],"m":{}}`)

	dirty, _ := NewParser[iltChild]()
	var kept []iltRetained
	for di, doc := range docs {
		root, err := dom.Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		for ci, ch := range iltChildren(root) {
			txt, _ := ch.MarshalJSON()
			fp, _ := NewParser[iltChild]()
			want := ilRun[iltChild](fp, string(txt), nil)
			got := iltUV[iltChild](dirty, ch, nil)
			wantC := ilClass(want.err)
			if wantC != ilClass(got.err) || (got.err == "" && want.json != got.js) {
				t.Errorf("doc#%d child#%d %.120q\n  Unmarshal(text): err=%s %.150s\n  UnmarshalValue:  err=%s %.150s", di, ci, txt, want.err, want.json, got.err, got.js)
				return
			}
			if got.err == "" {
				kept = append(kept, iltRetained{string(txt), ilOutcome{json: got.js, val: got.val}})
			}
			// A good, small parse right after each child keeps the Parser cycling.
			good, _ := dom.Parse([]byte(`{"B":1,"C":"g","M":{"z":2}}`))
			iltUV[iltChild](dirty, good, nil)
			if ci%9 == 0 {
				runtime.GC()
				if !iltRetainedCheck(t, kept, fmt.Sprintf("doc#%d child#%d", di, ci)) {
					return
				}
			}
		}
	}
}

// A Value held in a struct field after Unmarshal is itself a tape view. Bind
// it into typed destinations through UnmarshalValue.
func TestIltUVOfValueFieldFromUnmarshal(t *testing.T) {
	type host struct {
		V value.Value `json:"v"`
		N int         `json:"n"`
	}
	p, _ := NewParser[host]()
	d, _ := NewParser[iltChild]()
	var kept []iltRetained
	for round := range 60 {
		in := fmt.Sprintf(`{"n":%d,"v":{"B":%d,"C":"c%d","M":{%s}}}`, round, round, round, iltRepeat(round%45, func(j int) string { return fmt.Sprintf(`"k%d":%d`, j, j) }))
		if round%4 == 3 {
			in = fmt.Sprintf(`{"n":%d,"v":{"B":"bad","M":{"a":1}}}`, round)
		}
		var h host
		if err := p.Unmarshal([]byte(in), &h); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		txt, _ := h.V.MarshalJSON()
		fp, _ := NewParser[iltChild]()
		want := ilRun[iltChild](fp, string(txt), nil)
		got := iltUV[iltChild](d, h.V, nil)
		if ilClass(want.err) != ilClass(got.err) || (got.err == "" && want.json != got.js) {
			t.Fatalf("round %d: %.100q\n  Unmarshal(text): err=%s %.150s\n  UnmarshalValue:  err=%s %.150s", round, txt, want.err, want.json, got.err, got.js)
		}
		if got.err == "" {
			kept = append(kept, iltRetained{in, ilOutcome{json: got.js, val: got.val}})
		}
		// A failing contiguous parse in between exercises the pooled state of p.
		_ = p.Unmarshal([]byte(in[:len(in)/2]), new(host))
		if round%7 == 0 {
			runtime.GC()
			if !iltRetainedCheck(t, kept, fmt.Sprintf("round %d", round)) {
				return
			}
		}
	}
}
