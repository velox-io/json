package bind

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"
	"unsafe"

	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vopt"
)

// Stream[T] under interleaved failing and succeeding parses. One long-lived
// Parser (or the pooled entry point) runs failing documents, handler errors,
// handler panics and early exits between good documents. Every outcome is
// compared with a fresh Parser on the same input: error identity, the
// sequence of elements the handler saw, the activation counts, and the
// destination outside the stream. Delivered elements are re-read after the
// parse, periodically after a GC, to expose storage that was recycled while
// a handler still held a pointer to it.

// ilsHeldParses counts the parses whose handler held elements; ilsExec
// collects before the re-read on every ilsHeldGCEvery-th one.
var ilsHeldParses atomic.Uint64

const ilsHeldGCEvery = 8

var (
	errIlsHandler = errors.New("ils: handler error")
	errIlsHook    = errors.New("ils: hook error")
	errIlsLoop    = errors.New("ils: iteration continued 200 times after Decode failed")
	ilsPanicVal   = &struct{ s string }{"ils: handler panic"}
)

type ilsKind uint8

const (
	ilsFull        ilsKind = iota // decode every element
	ilsBreak                      // Go break before decoding the element at k
	ilsEarlyReturn                // return nil before decoding the element at k
	ilsSkip                       // Item.Skip on the element at k
	ilsErr                        // return a handler error at k
	ilsPanic                      // panic at k
	ilsCross                      // inner scope only: outer.Break()
	ilsNoHandler                  // register nothing
	ilsSwallow                    // ignore Decode errors and keep iterating
)

// ilsMode describes the handler behavior for one parse. kind/k drive the
// outermost stream; ikind/uk/ik drive the nested events stream of user uk at
// event ik.
type ilsMode struct {
	name    string
	kind    ilsKind
	k       int
	ikind   ilsKind
	uk, ik  int
	reuse   bool
	noInner bool
}

func ilsM(name string, kind ilsKind, k int) ilsMode {
	return ilsMode{name: name, kind: kind, k: k, ikind: ilsFull, uk: -1, ik: -1}
}

// ilsExpectedFull reports whether the mode decodes the whole array normally.
func (m ilsMode) full() bool {
	return (m.kind == ilsFull || m.kind == ilsSwallow || m.kind == ilsNoHandler) && m.ikind == ilsFull
}

// ilsAbortsByHandler reports whether the handler itself ends the parse.
func (m ilsMode) abortsByHandler() bool {
	return m.kind == ilsErr || m.kind == ilsPanic || m.ikind == ilsErr || m.ikind == ilsPanic
}

type ilsTrace struct {
	err     string
	elems   []string
	host    string
	acts    int
	inActs  int
	cross   int
	corrupt string
	held    []func() string
	hung    bool
}

func ilsEq(a, b *ilsTrace) bool {
	return a.err == b.err && a.host == b.host && a.acts == b.acts && a.inActs == b.inActs &&
		a.cross == b.cross && slices.Equal(a.elems, b.elems)
}

func ilsTraceDiff(want, got *ilsTrace) string {
	var sb strings.Builder
	if want.err != got.err {
		fmt.Fprintf(&sb, "\n   err    fresh=%s\n          dirty=%s", want.err, got.err)
	}
	if !slices.Equal(want.elems, got.elems) {
		fmt.Fprintf(&sb, "\n   elems  fresh(%d)=%s\n          dirty(%d)=%s", len(want.elems), ilsClip(strings.Join(want.elems, " ")), len(got.elems), ilsClip(strings.Join(got.elems, " ")))
	}
	if want.host != got.host {
		fmt.Fprintf(&sb, "\n   host   fresh=%s\n          dirty=%s", ilsClip(want.host), ilsClip(got.host))
	}
	if want.acts != got.acts || want.inActs != got.inActs || want.cross != got.cross {
		fmt.Fprintf(&sb, "\n   acts   fresh=%d/%d/%d dirty=%d/%d/%d", want.acts, want.inActs, want.cross, got.acts, got.inActs, got.cross)
	}
	return sb.String()
}

func ilsClip(s string) string {
	if len(s) > 260 {
		return s[:260] + "..."
	}
	return s
}

func ilsDig[T any](p *T) string {
	b, err := json.Marshal(p)
	if err != nil {
		return "marshal-err:" + err.Error()
	}
	return string(b)
}

// ilsDriver runs one parse. refRun is the driver a fresh Parser uses as the
// comparison baseline (the same driver, except for the pooled entry point).
type ilsDriver struct {
	name   string
	run    func(p *Parser, in string, h any, opts []UnmarshalOption) error
	refRun func(p *Parser, in string, h any, opts []UnmarshalOption) error
	pooled bool
	feed   bool
}

func ilsContigRun(p *Parser, in string, h any, opts []UnmarshalOption) error {
	return p.Unmarshal([]byte(in), h, opts...)
}

var ilsContig = ilsDriver{name: "contig", run: ilsContigRun, refRun: ilsContigRun}

var ilsPooledDrv = ilsDriver{
	name:   "pooled",
	pooled: true,
	run: func(_ *Parser, in string, h any, opts []UnmarshalOption) error {
		return Unmarshal([]byte(in), h, opts...)
	},
	refRun: ilsContigRun,
}

func ilsFeedDrv(chunk int) ilsDriver {
	run := func(p *Parser, in string, h any, opts []UnmarshalOption) error {
		return p.UnmarshalFeed(&chunkReader{data: []byte(in), chunk: chunk}, h)
	}
	return ilsDriver{name: fmt.Sprintf("feed%d", chunk), run: run, refRun: run, feed: true}
}

// ilsRig binds one host type to its handler installer and its oracles.
type ilsRig[H any] struct {
	name    string
	install func(*H, *ilsTrace, ilsMode)
	host    func(*H) string
	leak    func(*H) string
	// expect decodes in without streams and returns the element digests and
	// host digest; ok is false when the oracle does not apply.
	expect func(in string) (elems []string, host string, ok bool)
	// plainClass decodes in into the same shape with a plain slice in place
	// of each stream and reports the error class.
	plainClass func(in string, drv ilsDriver) string
	good       []string
	modes      []ilsMode
}

func ilsExec[H any](rig *ilsRig[H], p *Parser, drv ilsDriver, in string, m ilsMode, opts []UnmarshalOption, ref bool) (tr ilsTrace) {
	h := new(H)
	rig.install(h, &tr, m)
	run := drv.run
	if ref {
		run = drv.refRun
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				if r == any(ilsPanicVal) {
					tr.err = "PANIC(handler)"
				} else {
					tr.err = fmt.Sprintf("PANIC(other): %v", r)
				}
			}
		}()
		tr.err = ilDescribeErr(run(p, in, h, opts))
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		// The parse goroutine is leaked and keeps owning tr.
		return ilsTrace{err: "HANG", hung: true}
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				tr.corrupt += fmt.Sprintf("host digest panicked: %v; ", r)
			}
		}()
		tr.host = rig.host(h)
	}()
	// A forced GC per parse would dominate the sweeps' run time, so only
	// every ilsHeldGCEvery-th parse that holds elements collects before the
	// re-read.
	if len(tr.held) > 0 && ilsHeldParses.Add(1)%ilsHeldGCEvery == 0 {
		runtime.GC()
	}
	for i, f := range tr.held {
		if got := f(); got != tr.elems[i] {
			tr.corrupt += fmt.Sprintf("held element %d changed after the parse: saw %s, now %s; ", i, ilsClip(tr.elems[i]), ilsClip(got))
			break
		}
	}
	if rig.leak != nil {
		tr.corrupt += rig.leak(h)
	}
	return tr
}

// ilsParserLeaks reports scope-stack state a finished parse left behind.
func ilsParserLeaks(p *Parser) string {
	if p == nil {
		return ""
	}
	if len(p.streamScopes) != 0 {
		return fmt.Sprintf("streamScopes holds %d entries after the parse; ", len(p.streamScopes))
	}
	for i, e := range p.streamScopes[:cap(p.streamScopes)] {
		if e.streamAddr != nil || e.breakSig != nil {
			return fmt.Sprintf("stale streamScopes backing entry %d (addr=%v sig=%v); ", i, e.streamAddr != nil, e.breakSig != nil)
		}
	}
	return ""
}

type ilsStep struct {
	in string
	mi int
	oi int
}

type ilsFailure struct {
	what  string
	chain []ilsStep
	diff  string
}

var ilsOptSets = [][]UnmarshalOption{nil, {vopt.ZeroCopy(false)}, nil, {vopt.AllowInvalidUTF8(false)}}

// ilsCompare runs one step on the dirty Parser and on a fresh one and
// returns a failure description or "".
func ilsCompare[H any](rig *ilsRig[H], newParser func() *Parser, dirty *Parser, drv ilsDriver, s ilsStep) (string, *ilsTrace, *ilsTrace) {
	opts := ilsOptSets[s.oi%len(ilsOptSets)]
	m := rig.modes[s.mi%len(rig.modes)]
	got := ilsExec(rig, dirty, drv, s.in, m, opts, false)
	want := ilsExec(rig, newParser(), drv, s.in, m, opts, true)
	if got.hung || want.hung {
		return fmt.Sprintf("parse never terminates (mode=%s, hung: dirty=%v fresh=%v)", m.name, got.hung, want.hung), &want, &got
	}
	if got.corrupt != "" {
		return "memory oracle: " + got.corrupt, &want, &got
	}
	if want.corrupt != "" {
		return "memory oracle (fresh parser): " + want.corrupt, &want, &got
	}
	if strings.Contains(got.err, errIlsLoop.Error()) {
		return "handler kept receiving items after Decode failed (parse never ends)", &want, &got
	}
	if leaks := ilsParserLeaks(dirty); leaks != "" {
		return "scope leak: " + leaks, &want, &got
	}
	if !ilsEq(&want, &got) {
		return "history dependence", &want, &got
	}
	return "", &want, &got
}

// ilsMinimize shrinks a failing history to a short replayable chain.
func ilsMinimize[H any](rig *ilsRig[H], newParser func() *Parser, drv ilsDriver, hist []ilsStep) []ilsStep {
	last := hist[len(hist)-1]
	fails := func(pre []ilsStep) bool {
		d := newParser()
		for _, s := range pre {
			ilsExec(rig, d, drv, s.in, rig.modes[s.mi%len(rig.modes)], ilsOptSets[s.oi%len(ilsOptSets)], false)
		}
		what, _, _ := ilsCompare(rig, newParser, d, drv, last)
		return what != ""
	}
	pre := hist[:len(hist)-1]
	if len(pre) > 48 {
		pre = pre[len(pre)-48:]
	}
	if !fails(pre) {
		return nil
	}
	for k := 1; k < len(pre); k *= 2 {
		if fails(pre[len(pre)-k:]) {
			pre = pre[len(pre)-k:]
			break
		}
	}
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(pre); i++ {
			cand := append(append([]ilsStep(nil), pre[:i]...), pre[i+1:]...)
			if fails(cand) {
				pre, changed = cand, true
				i--
			}
		}
	}
	return append(append([]ilsStep(nil), pre...), last)
}

func ilsFormatChain[H any](rig *ilsRig[H], chain []ilsStep) string {
	var sb strings.Builder
	for i, s := range chain {
		fmt.Fprintf(&sb, "\n   step%d mode=%s opts#%d %q", i, rig.modes[s.mi%len(rig.modes)].name, s.oi%len(ilsOptSets), s.in)
	}
	if chain == nil {
		sb.WriteString("\n   (not reproducible by replay: GC- or pool-dependent)")
	}
	return sb.String()
}

// ilsNormClass folds truncation into the syntax class.
func ilsNormClass(err string) string {
	c := ilsClassOf(err)
	if c == "trunc" {
		return "syntax"
	}
	return c
}

func ilsPlainClassOf[P any](in string, drv ilsDriver) string {
	p, err := NewParserForType(reflect.TypeFor[P]())
	if err != nil {
		panic(err)
	}
	var v P
	return ilsNormClass(ilDescribeErr(drv.refRun(p, in, &v, nil)))
}

// ilsPlainOracle judges the stream host's error class against two references:
// encoding/json for syntax validity and a decode of the same document into a
// non-stream shape. Only divergences that the stream feature introduces are
// reported: a well-formed document must never become a syntax error, and a
// malformed document that the plain decode rejects as a syntax error must be
// rejected the same way when it is read, skipped or drained through a stream.
func ilsPlainOracle[H any](rig *ilsRig[H], drv ilsDriver, in string, m ilsMode, oi int, tr *ilsTrace) string {
	if rig.plainClass == nil || m.abortsByHandler() || oi%len(ilsOptSets) != 0 {
		return ""
	}
	pc := rig.plainClass(in, drv)
	sc := ilsNormClass(tr.err)
	valid := json.Valid([]byte(in))
	switch {
	case valid && sc == "syntax":
		return fmt.Sprintf("well-formed document reported as a syntax error (err=%q, mode=%s)", tr.err, m.name)
	case !valid && pc == "syntax" && sc != "syntax" && m.full() && m.kind != ilsNoHandler:
		return fmt.Sprintf("malformed document accepted or misclassified: stream=%s plain=syntax (err=%q, mode=%s)", sc, tr.err, m.name)
	case valid && m.full() && sc != pc:
		return fmt.Sprintf("error class differs from the non-stream decode: stream=%s plain=%s (err=%q, mode=%s)", sc, pc, tr.err, m.name)
	}
	return ""
}

func ilsSweep[H any](t *testing.T, rig *ilsRig[H], drv ilsDriver, stride int) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	newParser := func() *Parser {
		p, err := NewParserForType(reflect.TypeFor[H]())
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	var mutants []string
	for _, g := range rig.good {
		ms := ilSweepMutants(g)
		for i := 0; i < len(ms); i += stride {
			mutants = append(mutants, ms[i])
		}
	}
	dirty := newParser()
	hangs := 0
	var hist []ilsStep
	var fails []ilsFailure
	seen := map[string]bool{}
	step := func(s ilsStep) {
		hist = append(hist, s)
		if len(hist) > 300 {
			hist = hist[len(hist)-300:]
		}
		what, want, got := ilsCompare(rig, newParser, dirty, drv, s)
		if got.hung || want.hung {
			hangs++
			dirty = newParser()
		}
		if what == "" {
			m := rig.modes[s.mi%len(rig.modes)]
			if v := ilsPlainOracle(rig, drv, s.in, m, s.oi, got); v != "" {
				what = v
			}
		}
		if what == "" {
			return
		}
		key := what + "|" + want.err + "|" + got.err
		if len(what) > 40 {
			key = what[:40] + "|" + want.err + "|" + got.err
		}
		if seen[key] {
			return
		}
		seen[key] = true
		var chain []ilsStep
		if strings.HasPrefix(what, "history") || strings.HasPrefix(what, "scope") {
			chain = ilsMinimize(rig, newParser, drv, hist)
		} else {
			chain = []ilsStep{s}
		}
		fails = append(fails, ilsFailure{what: what, chain: chain, diff: ilsTraceDiff(want, got)})
	}
	for i, mu := range mutants {
		if len(fails) >= 6 || hangs > 0 {
			break
		}
		step(ilsStep{mu, i, i})
		g := rig.good[(i*5+1)%len(rig.good)]
		step(ilsStep{g, i / 3, i + 1})
		if i%61 == 0 {
			runtime.GC()
		}
	}
	for _, f := range fails {
		t.Errorf("%s/%s: %s; chain:%s%s", rig.name, drv.name, f.what, ilsFormatChain(rig, f.chain), f.diff)
	}
}

// ilsGoodMatchesOracle runs every good document through every full-decode mode
// on a dirty Parser and compares with the non-stream oracle.
func ilsGoodMatchesOracle[H any](t *testing.T, rig *ilsRig[H], drv ilsDriver) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	p, _ := NewParserForType(reflect.TypeFor[H]())
	bad := 0
	for range 3 {
		for _, g := range rig.good {
			for _, m := range rig.modes {
				if !m.full() || m.kind == ilsNoHandler || m.noInner {
					continue
				}
				tr := ilsExec(rig, p, drv, g, m, nil, false)
				we, wh, ok := rig.expect(g)
				if !ok {
					continue
				}
				if tr.err != "" || tr.corrupt != "" || !slices.Equal(tr.elems, we) || tr.host != wh {
					bad++
					if bad <= 4 {
						t.Errorf("%s/%s mode=%s doc=%q\n   err=%q corrupt=%q\n   want elems(%d)=%s\n   got  elems(%d)=%s\n   want host=%s\n   got  host=%s",
							rig.name, drv.name, m.name, ilsClip(g), tr.err, tr.corrupt,
							len(we), ilsClip(strings.Join(we, " ")), len(tr.elems), ilsClip(strings.Join(tr.elems, " ")), wh, tr.host)
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------- leaf rig

type ilsInner struct {
	N int `json:"n"`
}

type ilsElem struct {
	ID   int               `json:"id"`
	Name string            `json:"name"`
	Tags []string          `json:"tags"`
	Meta map[string]string `json:"meta"`
	P    *ilsInner         `json:"p"`
}

type ilsHost struct {
	Pre   string                 `json:"pre"`
	Items stream.Stream[ilsElem] `json:"items"`
	Post  string                 `json:"post"`
	Tail  []int                  `json:"tail"`
	M     map[string]int         `json:"m"`
}

type ilsPlainHost struct {
	Pre   string         `json:"pre"`
	Items []ilsElem      `json:"items"`
	Post  string         `json:"post"`
	Tail  []int          `json:"tail"`
	M     map[string]int `json:"m"`
}

type ilsHostProj struct {
	Pre  string
	Post string
	Tail []int
	M    map[string]int
}

func ilsHostDigest(h *ilsHost) string {
	return ilsDig(&ilsHostProj{h.Pre, h.Post, h.Tail, h.M})
}

// ilsStreamData reads the data word of the slice header at the front of a
// Stream field. The driver clears it when a scope ends.
func ilsStreamData[T any](s *stream.Stream[T]) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(s))
}

func ilsElemJSON(i int) string {
	switch i % 4 {
	case 0:
		return fmt.Sprintf(`{"id":%d,"name":"n%d\n\u00e9","tags":["a%d","b"],"meta":{"k%d":"v"},"p":{"n":%d}}`, i, i, i, i, i)
	case 1:
		return fmt.Sprintf(`{"id":%d,"name":"n%d"}`, i, i)
	case 2:
		return fmt.Sprintf(`{"id":%d,"tags":["t%d"],"p":{"n":%d}}`, i, i, i)
	}
	return fmt.Sprintf(`{"id":%d,"meta":{"k":"v%d"}}`, i, i)
}

func ilsItemsJSON(n int) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(ilsElemJSON(i))
	}
	sb.WriteByte(']')
	return sb.String()
}

func ilsHostDoc(n int) string {
	return `{"pre":"p","items":` + ilsItemsJSON(n) + `,"post":"z","tail":[1,2,3],"m":{"x":1}}`
}

// ilsDrive is the handler body shared by the leaf and nested rigs.
func ilsDrive[T any](sc stream.Scope[T], tr *ilsTrace, m ilsMode, digest func(*T) string, hold bool) error {
	n := 0
	swallowed := 0
loop:
	for it := range sc.Iter() {
		if n == m.k {
			switch m.kind {
			case ilsBreak:
				break loop
			case ilsEarlyReturn:
				return nil
			case ilsSkip:
				_ = it.Skip()
				continue
			case ilsErr:
				return errIlsHandler
			case ilsPanic:
				panic(ilsPanicVal)
			}
		}
		if err := it.Decode(); err != nil {
			if m.kind == ilsSwallow {
				if swallowed++; swallowed > 200 {
					return errIlsLoop
				}
				continue
			}
			return err
		}
		tgt := it.Target()
		tr.elems = append(tr.elems, digest(tgt))
		if hold {
			tr.held = append(tr.held, func() string { return digest(tgt) })
		}
		n++
	}
	return nil
}

func ilsInstallLeaf(h *ilsHost, tr *ilsTrace, m ilsMode) {
	if m.kind == ilsNoHandler {
		return
	}
	h.Items.OnRead(func(sc stream.Scope[ilsElem]) error {
		tr.acts++
		if m.reuse {
			sc.AllowValueReuse()
		}
		return ilsDrive(sc, tr, m, ilsDig[ilsElem], !m.reuse)
	})
}

func ilsExpectLeaf(in string) ([]string, string, bool) {
	if strings.Count(in, `"items"`) != 1 {
		return nil, "", false
	}
	p, _ := NewParser[ilsPlainHost]()
	var h ilsPlainHost
	if err := p.Unmarshal([]byte(in), &h); err != nil {
		return nil, "", false
	}
	var elems []string
	for i := range h.Items {
		elems = append(elems, ilsDig(&h.Items[i]))
	}
	return elems, ilsDig(&ilsHostProj{h.Pre, h.Post, h.Tail, h.M}), true
}

func ilsLeafModes() []ilsMode {
	reuse := ilsM("full-reuse", ilsFull, -1)
	reuse.reuse = true
	brk1 := ilsM("break@1", ilsBreak, 1)
	brk5r := ilsM("break@5-reuse", ilsBreak, 5)
	brk5r.reuse = true
	return []ilsMode{
		ilsM("full", ilsFull, -1), reuse, brk1, brk5r,
		ilsM("return@2", ilsEarlyReturn, 2), ilsM("skip@2", ilsSkip, 2), ilsM("skip@0", ilsSkip, 0),
		ilsM("err@0", ilsErr, 0), ilsM("err@3", ilsErr, 3), ilsM("err@6", ilsErr, 6),
		ilsM("panic@1", ilsPanic, 1), ilsM("panic@5", ilsPanic, 5),
		ilsM("nohandler", ilsNoHandler, -1), ilsM("swallow", ilsSwallow, -1),
	}
}

var ilsLeafGood = []string{
	ilsHostDoc(6),
	ilsHostDoc(4),
	ilsHostDoc(8),
	ilsHostDoc(0),
	ilsHostDoc(1),
	`{"post":"z","items":` + ilsItemsJSON(5) + `,"pre":"p"}`,
	`{"items":null,"post":"n"}`,
	`{"items":[null,{"id":1},null,{"id":2,"name":"x"},{"id":3}],"post":"z"}`,
	`{"items":` + ilsItemsJSON(2) + `,"pre":"x","items":` + ilsItemsJSON(5) + `}`,
}

var ilsLeaf = &ilsRig[ilsHost]{
	name:    "leaf",
	install: ilsInstallLeaf,
	host:    ilsHostDigest,
	leak: func(h *ilsHost) string {
		if ilsStreamData(&h.Items) != nil {
			return "Stream field keeps its slice backing after the parse; "
		}
		return ""
	},
	expect:     ilsExpectLeaf,
	plainClass: ilsPlainClassOf[ilsPlainHost],
	good:       ilsLeafGood,
	modes:      ilsLeafModes(),
}

func TestIlsLeafSweepContig(t *testing.T)  { ilsSweep(t, ilsLeaf, ilsContig, 2) }
func TestIlsLeafSweepPooled(t *testing.T)  { ilsSweep(t, ilsLeaf, ilsPooledDrv, 5) }
func TestIlsLeafSweepFeed7(t *testing.T)   { ilsSweep(t, ilsLeaf, ilsFeedDrv(7), 3) }
func TestIlsLeafSweepFeed1(t *testing.T)   { ilsSweep(t, ilsLeaf, ilsFeedDrv(1), 6) }
func TestIlsLeafSweepFeed256(t *testing.T) { ilsSweep(t, ilsLeaf, ilsFeedDrv(256), 4) }

func TestIlsLeafGoodMatchesOracle(t *testing.T) {
	for _, drv := range []ilsDriver{ilsContig, ilsPooledDrv, ilsFeedDrv(5), ilsFeedDrv(64)} {
		ilsGoodMatchesOracle(t, ilsLeaf, drv)
	}
}

// ---------------------------------------------------------------- rich leaf rig

type ilsHook struct{ Raw string }

func (x *ilsHook) UnmarshalJSON(b []byte) error {
	if string(b) == `"fail"` {
		return errIlsHook
	}
	x.Raw = "H:" + string(b)
	return nil
}

type ilsText struct{ V string }

func (x *ilsText) UnmarshalText(b []byte) error {
	if string(b) == "bad" {
		return errIlsHook
	}
	x.V = "T:" + string(b)
	return nil
}

type ilsRich struct {
	ID  int             `json:"id"`
	V   value.Value     `json:"v"`
	Raw json.RawMessage `json:"raw"`
	H   ilsHook         `json:"h"`
	T   ilsText         `json:"t"`
	M   map[string]int  `json:"m"`
	S   string          `json:"s"`
}

type ilsRichHost struct {
	Pre   string                 `json:"pre"`
	Items stream.Stream[ilsRich] `json:"items"`
	Post  string                 `json:"post"`
}

type ilsRichPlain struct {
	Pre   string    `json:"pre"`
	Items []ilsRich `json:"items"`
	Post  string    `json:"post"`
}

func ilsRichJSON(i int) string {
	return fmt.Sprintf(`{"id":%d,"v":{"k":"v-%d","a":[1,2,{"z":null}]},"raw":{"n":%d,"a":[1, 2]},"h":{"x":%d},"t":"txt-%d","m":{"k%d":%d},"s":"q\"u\\ote %d"}`,
		i, i, i, i, i, i, i, i)
}

func ilsRichDoc(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"pre":"p","items":[`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(ilsRichJSON(i))
	}
	sb.WriteString(`],"post":"z"}`)
	return sb.String()
}

func ilsInstallRich(h *ilsRichHost, tr *ilsTrace, m ilsMode) {
	if m.kind == ilsNoHandler {
		return
	}
	h.Items.OnRead(func(sc stream.Scope[ilsRich]) error {
		tr.acts++
		if m.reuse {
			sc.AllowValueReuse()
		}
		return ilsDrive(sc, tr, m, ilsDig[ilsRich], !m.reuse)
	})
}

func ilsExpectRich(in string) ([]string, string, bool) {
	p, _ := NewParser[ilsRichPlain]()
	var h ilsRichPlain
	if err := p.Unmarshal([]byte(in), &h); err != nil {
		return nil, "", false
	}
	var elems []string
	for i := range h.Items {
		elems = append(elems, ilsDig(&h.Items[i]))
	}
	return elems, h.Pre + "|" + h.Post, true
}

var ilsRichRig = &ilsRig[ilsRichHost]{
	name:    "rich",
	install: ilsInstallRich,
	host:    func(h *ilsRichHost) string { return h.Pre + "|" + h.Post },
	leak: func(h *ilsRichHost) string {
		if ilsStreamData(&h.Items) != nil {
			return "Stream field keeps its slice backing after the parse; "
		}
		return ""
	},
	expect:     ilsExpectRich,
	plainClass: ilsPlainClassOf[ilsRichPlain],
	good:       []string{ilsRichDoc(6), ilsRichDoc(4), ilsRichDoc(9), ilsRichDoc(1), ilsRichDoc(0)},
	modes:      ilsLeafModes(),
}

func TestIlsRichSweepContig(t *testing.T)  { ilsSweep(t, ilsRichRig, ilsContig, 4) }
func TestIlsRichSweepPooled(t *testing.T)  { ilsSweep(t, ilsRichRig, ilsPooledDrv, 9) }
func TestIlsRichSweepFeed9(t *testing.T)   { ilsSweep(t, ilsRichRig, ilsFeedDrv(9), 7) }
func TestIlsRichSweepFeed200(t *testing.T) { ilsSweep(t, ilsRichRig, ilsFeedDrv(200), 7) }

func TestIlsRichGoodMatchesOracle(t *testing.T) {
	for _, drv := range []ilsDriver{ilsContig, ilsPooledDrv, ilsFeedDrv(5), ilsFeedDrv(64)} {
		ilsGoodMatchesOracle(t, ilsRichRig, drv)
	}
}

// ---------------------------------------------------------------- nested rig

type ilsEvent struct {
	K    int      `json:"k"`
	S    string   `json:"s"`
	Tags []string `json:"tags"`
}

type ilsUser struct {
	ID     int                     `json:"id"`
	Events stream.Stream[ilsEvent] `json:"events"`
	Name   string                  `json:"name"`
	Meta   map[string]string       `json:"meta"`
}

type ilsNestHost struct {
	Users stream.Stream[ilsUser] `json:"users"`
	Post  string                 `json:"post"`
}

type ilsPlainUser struct {
	ID     int               `json:"id"`
	Events []ilsEvent        `json:"events"`
	Name   string            `json:"name"`
	Meta   map[string]string `json:"meta"`
}

type ilsNestPlain struct {
	Users []ilsPlainUser `json:"users"`
	Post  string         `json:"post"`
}

type ilsUserFields struct {
	ID   int
	Name string
	Meta map[string]string
}

func ilsUserDigest(id int, name string, meta map[string]string, evs []string) string {
	return ilsDig(&ilsUserFields{id, name, meta}) + "|" + strings.Join(evs, ",")
}

func ilsEventJSON(u, e int) string {
	if e%2 == 0 {
		return fmt.Sprintf(`{"k":%d,"s":"e%d.%d","tags":["x","y%d"]}`, e, u, e, e)
	}
	return fmt.Sprintf(`{"k":%d}`, e)
}

// ilsUserJSON renders one user. Dense users always carry every member, so a
// non-leaf slot reused across elements shows no stale fields, and an empty
// events array is rendered as null (see TestIlsEmptyInnerStreamNoPhantomElement,
// TestIlsEmptyThenShortInnerStream and TestIlsNonLeafSlotMatchesStd for those
// defects in isolation).
func ilsUserJSON(u, ne int, eventsFirst bool) string {
	var ev strings.Builder
	ev.WriteByte('[')
	for e := range ne {
		if e > 0 {
			ev.WriteByte(',')
		}
		ev.WriteString(ilsEventJSON(u, e))
	}
	ev.WriteByte(']')
	if ne == 0 {
		return fmt.Sprintf(`{"id":%d,"name":"u%d","meta":{"m":"%d"},"events":null}`, u, u, u)
	}
	if eventsFirst {
		return fmt.Sprintf(`{"events":%s,"id":%d,"name":"u%d","meta":{"m":"%d"}}`, ev.String(), u, u, u)
	}
	if u%2 == 1 {
		return fmt.Sprintf(`{"id":%d,"name":"u%d","meta":{"m":"%d"},"events":%s}`, u, u, u, ev.String())
	}
	return fmt.Sprintf(`{"id":%d,"name":"u%d","events":%s,"meta":{"m":"%d"}}`, u, u, ev.String(), u)
}

func ilsNestDoc(counts ...int) string {
	var sb strings.Builder
	sb.WriteString(`{"users":[`)
	for u, ne := range counts {
		if u > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(ilsUserJSON(u, ne, u%3 == 2))
	}
	sb.WriteString(`],"post":"z"}`)
	return sb.String()
}

func ilsInstallNest(h *ilsNestHost, tr *ilsTrace, m ilsMode) {
	if m.kind == ilsNoHandler {
		return
	}
	h.Users.OnRead(func(users stream.Scope[ilsUser]) error {
		tr.acts++
		ui := 0
		swallowed := 0
	loop:
		for it := range users.Iter() {
			if ui == m.k {
				switch m.kind {
				case ilsBreak:
					break loop
				case ilsEarlyReturn:
					return nil
				case ilsSkip:
					_ = it.Skip()
					continue
				case ilsErr:
					return errIlsHandler
				case ilsPanic:
					panic(ilsPanicVal)
				}
			}
			var evs []string
			tgt := it.Target()
			if !m.noInner {
				cur := ui
				tgt.Events.OnRead(func(es stream.Scope[ilsEvent]) error {
					tr.inActs++
					ei := 0
				inner:
					for ev := range es.Iter() {
						if cur == m.uk && ei == m.ik {
							switch m.ikind {
							case ilsBreak:
								break inner
							case ilsEarlyReturn:
								return nil
							case ilsCross:
								return users.Break()
							case ilsSkip:
								_ = ev.Skip()
								continue
							case ilsErr:
								return errIlsHandler
							case ilsPanic:
								panic(ilsPanicVal)
							}
						}
						if err := ev.Decode(); err != nil {
							return err
						}
						evs = append(evs, ilsDig(ev.Target()))
						ei++
					}
					return nil
				})
			}
			err := it.Decode()
			if users.IsBreak(err) {
				tr.cross++
				break loop
			}
			if err != nil {
				if m.kind == ilsSwallow {
					if swallowed++; swallowed > 200 {
						return errIlsLoop
					}
					continue
				}
				return err
			}
			tr.elems = append(tr.elems, ilsUserDigest(tgt.ID, tgt.Name, tgt.Meta, evs))
			ui++
		}
		return nil
	})
}

func ilsExpectNest(in string) ([]string, string, bool) {
	p, _ := NewParser[ilsNestPlain]()
	var h ilsNestPlain
	if err := p.Unmarshal([]byte(in), &h); err != nil {
		return nil, "", false
	}
	var elems []string
	for i := range h.Users {
		u := &h.Users[i]
		var evs []string
		for j := range u.Events {
			evs = append(evs, ilsDig(&u.Events[j]))
		}
		elems = append(elems, ilsUserDigest(u.ID, u.Name, u.Meta, evs))
	}
	return elems, h.Post, true
}

func ilsNestModes() []ilsMode {
	mk := func(name string, kind ilsKind, k int, ikind ilsKind, uk, ik int) ilsMode {
		return ilsMode{name: name, kind: kind, k: k, ikind: ikind, uk: uk, ik: ik}
	}
	return []ilsMode{
		mk("full", ilsFull, -1, ilsFull, -1, -1),
		mk("outer-break@1", ilsBreak, 1, ilsFull, -1, -1),
		mk("outer-skip@0", ilsSkip, 0, ilsFull, -1, -1),
		mk("outer-err@2", ilsErr, 2, ilsFull, -1, -1),
		mk("outer-panic@1", ilsPanic, 1, ilsFull, -1, -1),
		mk("inner-break@1.1", ilsFull, -1, ilsBreak, 1, 1),
		mk("inner-break@0.0", ilsFull, -1, ilsBreak, 0, 0),
		mk("inner-skip@1.2", ilsFull, -1, ilsSkip, 1, 2),
		mk("inner-cross@1.1", ilsFull, -1, ilsCross, 1, 1),
		mk("inner-cross@0.0", ilsFull, -1, ilsCross, 0, 0),
		mk("inner-cross@2.0", ilsFull, -1, ilsCross, 2, 0),
		mk("inner-err@1.0", ilsFull, -1, ilsErr, 1, 0),
		mk("inner-err@2.1", ilsFull, -1, ilsErr, 2, 1),
		mk("inner-panic@1.1", ilsFull, -1, ilsPanic, 1, 1),
		mk("inner-return@1.1", ilsFull, -1, ilsEarlyReturn, 1, 1),
		{name: "no-inner", kind: ilsFull, k: -1, ikind: ilsFull, uk: -1, ik: -1, noInner: true},
		mk("nohandler", ilsNoHandler, -1, ilsFull, -1, -1),
		mk("swallow", ilsSwallow, -1, ilsFull, -1, -1),
	}
}

var ilsNest = &ilsRig[ilsNestHost]{
	name:    "nested",
	install: ilsInstallNest,
	host:    func(h *ilsNestHost) string { return h.Post },
	leak: func(h *ilsNestHost) string {
		if ilsStreamData(&h.Users) != nil {
			return "outer Stream field keeps its slice backing after the parse; "
		}
		return ""
	},
	expect:     ilsExpectNest,
	plainClass: ilsPlainClassOf[ilsNestPlain],
	good: []string{
		ilsNestDoc(2, 3, 0),
		ilsNestDoc(5, 1, 6, 0),
		ilsNestDoc(0),
		ilsNestDoc(),
		ilsNestDoc(1, 1, 1, 1, 1),
		`{"users":[{"id":1,"events":null,"name":"n","meta":null},{"id":2,"name":"","meta":null}],"post":"z"}`,
	},
	modes: ilsFilterModes(ilsNestModes(), func(m ilsMode) bool { return m.ikind != ilsCross && m.kind != ilsSwallow }),
}

func ilsFilterModes(ms []ilsMode, keep func(ilsMode) bool) []ilsMode {
	var out []ilsMode
	for _, m := range ms {
		if keep(m) {
			out = append(out, m)
		}
	}
	return out
}

// ilsNestX covers the cross-scope Break modes. Its documents keep every inner
// array within one leaf batch: a Break from a leaf stream that holds more than
// one batch never terminates (TestIlsBreakLeafBeyondBatchTerminates).
var ilsNestX = &ilsRig[ilsNestHost]{
	name:       "nested-cross",
	install:    ilsInstallNest,
	host:       func(h *ilsNestHost) string { return h.Post },
	leak:       ilsNest.leak,
	expect:     ilsExpectNest,
	plainClass: ilsPlainClassOf[ilsNestPlain],
	good: []string{
		ilsNestDoc(2, 3, 0),
		ilsNestDoc(4, 1, 4, 0),
		ilsNestDoc(0),
		ilsNestDoc(1, 1, 1, 1, 1),
		`{"users":[{"id":1,"events":null,"name":"n","meta":null},{"id":2,"name":"","meta":null}],"post":"z"}`,
	},
	modes: ilsFilterModes(ilsNestModes(), func(m ilsMode) bool {
		return m.ikind == ilsCross || m.kind == ilsFull && m.ikind == ilsFull && !m.noInner
	}),
}

func TestIlsNestSweepContig(t *testing.T)      { ilsSweep(t, ilsNest, ilsContig, 2) }
func TestIlsNestSweepPooled(t *testing.T)      { ilsSweep(t, ilsNest, ilsPooledDrv, 5) }
func TestIlsNestCrossSweepContig(t *testing.T) { ilsSweep(t, ilsNestX, ilsContig, 2) }
func TestIlsNestCrossSweepFeed11(t *testing.T) { ilsSweep(t, ilsNestX, ilsFeedDrv(11), 3) }
func TestIlsNestSweepFeed11(t *testing.T)      { ilsSweep(t, ilsNest, ilsFeedDrv(11), 3) }
func TestIlsNestSweepFeed200(t *testing.T)     { ilsSweep(t, ilsNest, ilsFeedDrv(200), 4) }

func TestIlsNestGoodMatchesOracle(t *testing.T) {
	for _, drv := range []ilsDriver{ilsContig, ilsPooledDrv, ilsFeedDrv(5), ilsFeedDrv(64)} {
		ilsGoodMatchesOracle(t, ilsNest, drv)
	}
}

// ---------------------------------------------------------------- boundary matrix

type ilsDefect struct {
	name string
	// apply returns the document with element j of n broken.
	apply func(n, j int) string
	// kind is "syntax", "type" or "trunc" for the expected error class, or
	// "" when only history independence is checked.
	kind string
}

func ilsItemsWith(n, j int, repl string) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		if i == j {
			sb.WriteString(repl)
		} else {
			sb.WriteString(ilsElemJSON(i))
		}
	}
	sb.WriteByte(']')
	return sb.String()
}

func ilsDocWithItems(items string) string {
	return `{"pre":"p","items":` + items + `,"post":"z","tail":[1,2,3],"m":{"x":1}}`
}

var ilsDefects = []ilsDefect{
	{"syntax-missing-value", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"name":}`)) }, "syntax"},
	{"syntax-unclosed-object", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"name":"x"`)) }, "syntax"},
	{"syntax-bad-literal", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":tru}`)) }, "syntax"},
	{"syntax-trailing-comma", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,}`)) }, "syntax"},
	{"type-scalar-element", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `7`)) }, "type"},
	{"type-array-element", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `[1]`)) }, "type"},
	{"type-string-id", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":"x","name":"y"}`)) }, "type"},
	{"type-tags-scalar", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"tags":5}`)) }, "type"},
	{"type-meta-number", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"meta":{"a":1}}`)) }, "type"},
	{"type-ptr-string", func(n, j int) string { return ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"p":"x"}`)) }, "type"},
	{"trunc-at-element", func(n, j int) string {
		d := ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"name":"abcdef"}`))
		return d[:strings.Index(d, `{"id":1,"name":"abcdef"}`)+9]
	}, "trunc"},
	{"trunc-after-comma", func(n, j int) string {
		d := ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"name":"abcdef"}`))
		return d[:strings.Index(d, `{"id":1,"name":"abcdef"}`)]
	}, "trunc"},
	{"trunc-in-string", func(n, j int) string {
		d := ilsDocWithItems(ilsItemsWith(n, j, `{"id":1,"name":"abcdef"}`))
		return d[:strings.Index(d, `"abcdef"`)+4]
	}, "trunc"},
}

func ilsClassOf(err string) string {
	switch {
	case err == "":
		return "ok"
	case strings.HasPrefix(err, "SE"):
		if strings.Contains(err, "eof=true") {
			return "trunc"
		}
		return "syntax"
	case strings.HasPrefix(err, "UTE"):
		return "type"
	}
	return err
}

// TestIlsBoundaryMatrix places every defect at every element index for
// element counts around the batch size, on one dirty Parser, with a good
// parse after each failure. Delivered elements must be an exact prefix of the
// good document's elements, and a broken element must never be delivered.
func TestIlsBoundaryMatrix(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	modes := []ilsMode{ilsM("full", ilsFull, -1), func() ilsMode { m := ilsM("full-reuse", ilsFull, -1); m.reuse = true; return m }(),
		ilsM("break@1", ilsBreak, 1), ilsM("skip@2", ilsSkip, 2)}
	for _, drv := range []ilsDriver{ilsContig, ilsFeedDrv(13)} {
		dirty, _ := NewParser[ilsHost]()
		fresh := func() *Parser { p, _ := NewParser[ilsHost](); return p }
		bad := 0
		report := func(format string, args ...any) {
			bad++
			if bad <= 8 {
				t.Errorf("%s: "+format, append([]any{drv.name}, args...)...)
			}
		}
		for n := 1; n <= 11; n++ {
			goodDoc := ilsHostDoc(n)
			trueElems, _, _ := ilsExpectLeaf(goodDoc)
			for j := 0; j < n; j++ {
				for _, df := range ilsDefects {
					doc := df.apply(n, j)
					for mi, m := range modes {
						got := ilsExec(ilsLeaf, dirty, drv, doc, m, nil, false)
						want := ilsExec(ilsLeaf, fresh(), drv, doc, m, nil, true)
						if !ilsEq(&want, &got) || got.corrupt != "" {
							report("n=%d j=%d defect=%s mode=%s history/memory failure corrupt=%q doc=%q%s",
								n, j, df.name, m.name, got.corrupt, ilsClip(doc), ilsTraceDiff(&want, &got))
						}
						cls := ilsClassOf(got.err)
						if m.kind == ilsFull && !m.reuse || m.kind == ilsFull {
							if df.kind == "syntax" || df.kind == "trunc" {
								if cls != "syntax" && cls != "trunc" {
									report("n=%d j=%d defect=%s mode=%s: want syntax class, got %q doc=%q", n, j, df.name, m.name, got.err, ilsClip(doc))
								}
							}
						}
						// Delivered elements stay an exact prefix of the true list below j.
						lim := min(len(got.elems), j)
						if lim > len(trueElems) || !slices.Equal(got.elems[:lim], trueElems[:lim]) {
							report("n=%d j=%d defect=%s mode=%s: delivered prefix differs from the good document: got %s want %s doc=%q",
								n, j, df.name, m.name, ilsClip(strings.Join(got.elems, " ")), ilsClip(strings.Join(trueElems, " ")), ilsClip(doc))
						}
						if (df.kind == "syntax" || df.kind == "trunc") && len(got.elems) > 4*(j/4) {
							report("n=%d j=%d defect=%s mode=%s: delivered %d elements, but batch of element %d never completed; doc=%q",
								n, j, df.name, m.name, len(got.elems), j, ilsClip(doc))
						}
						// A good parse right after must match the oracle.
						gm := modes[(mi+1)%len(modes)]
						gg := ilsExec(ilsLeaf, dirty, drv, goodDoc, gm, nil, false)
						ge, gh, _ := ilsExpectLeaf(goodDoc)
						if gm.kind == ilsFull {
							if gg.err != "" || !slices.Equal(gg.elems, ge) || gg.host != gh || gg.corrupt != "" {
								report("n=%d j=%d defect=%s: good parse after failure (mode %s) wrong: err=%q corrupt=%q elems=%d want %d host=%s",
									n, j, df.name, gm.name, gg.err, gg.corrupt, len(gg.elems), len(ge), gg.host)
							}
						}
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------- swallowed errors

// TestIlsSwallowedDecodeError keeps iterating after Decode failed on a
// non-leaf stream. The parse must still end with the original error, deliver
// nothing past the failure, and not loop.
func TestIlsSwallowedDecodeError(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	docs := []string{
		`{"users":[{"id":1},{"id":"x"},{"id":3},{"id":4}],"post":"z"}`,
		`{"users":[{"id":1},{"id":2,"events":[{"k":1},{"k":"x"},{"k":3}]},{"id":3}],"post":"z"}`,
		`{"users":[{"id":1},{"id":2,"events":[{"k":1},{"k":2}]},{"id":3,]},{"id":4}],"post":"z"}`,
		`{"users":[{"id":1},{"id":2},7,{"id":4}],"post":"z"}`,
		`{"users":[{"id":1},{"id":2},{"id":3`,
	}
	dirty, _ := NewParser[ilsNestHost]()
	for _, d := range docs {
		sw := ilsM("swallow", ilsSwallow, -1)
		full := ilsM("full", ilsFull, -1)
		wantFull := ilsExec(ilsNest, ilsMustParser[ilsNestHost](), ilsContig, d, full, nil, true)
		got := ilsExec(ilsNest, dirty, ilsContig, d, sw, nil, false)
		if got.err == "" {
			t.Errorf("swallowed Decode error vanished: doc=%q (full-run err=%q)", d, wantFull.err)
			continue
		}
		if got.err != wantFull.err {
			t.Errorf("swallowed Decode error changed identity: doc=%q swallow=%q full=%q", d, got.err, wantFull.err)
		}
		if len(got.elems) > len(wantFull.elems) {
			t.Errorf("elements delivered after the failure: doc=%q swallow delivered %d, full run %d: %s", d, len(got.elems), len(wantFull.elems), ilsClip(strings.Join(got.elems, " ")))
		}
		// The next good parse must be unaffected.
		g := ilsNestDoc(2, 1, 3)
		gt := ilsExec(ilsNest, dirty, ilsContig, g, full, nil, false)
		ge, gh, _ := ilsExpectNest(g)
		if gt.err != "" || !slices.Equal(gt.elems, ge) || gt.host != gh {
			t.Errorf("good parse after swallowed error wrong: doc=%q err=%q elems=%v want %v", d, gt.err, gt.elems, ge)
		}
	}
}

func ilsMustParser[H any]() *Parser {
	p, err := NewParserForType(reflect.TypeFor[H]())
	if err != nil {
		panic(err)
	}
	return p
}

// ---------------------------------------------------------------- reuse semantics

// TestIlsReuseSlotMatchesFreshSlot compares AllowValueReuse output with the
// default for the same document. Reuse only changes storage lifetime, so a
// sparse element after a full one must not inherit the full one's fields.
func TestIlsReuseSlotMatchesFreshSlot(t *testing.T) {
	docs := []string{
		ilsHostDoc(9),
		`{"items":[{"id":1,"name":"a","tags":["t"],"meta":{"k":"v"},"p":{"n":9}},{"id":2},{"id":3},{"id":4},{"id":5},{"id":6},{"id":7},null,{"id":9}]}`,
		`{"items":[{"id":1,"name":"a","tags":["t"],"meta":{"k":"v"},"p":{"n":9}},null,null,null,null,null,null,null,null]}`,
	}
	for _, d := range docs {
		p, _ := NewParser[ilsHost]()
		fresh := ilsExec(ilsLeaf, p, ilsContig, d, ilsM("full", ilsFull, -1), nil, false)
		rm := ilsM("full-reuse", ilsFull, -1)
		rm.reuse = true
		reuse := ilsExec(ilsLeaf, p, ilsContig, d, rm, nil, false)
		if !slices.Equal(fresh.elems, reuse.elems) {
			t.Errorf("reuse changes element contents: doc=%q\n   default=%s\n   reuse  =%s", ilsClip(d), ilsClip(strings.Join(fresh.elems, " ")), ilsClip(strings.Join(reuse.elems, " ")))
		}
	}
}

// TestIlsNonLeafSlotMatchesStd checks that the reused single slot is zeroed
// between elements.
func TestIlsNonLeafSlotMatchesStd(t *testing.T) {
	d := `{"users":[{"id":1,"name":"a","meta":{"k":"v"},"events":[{"k":1,"s":"x","tags":["q"]}]},{"id":2},{"id":3,"events":[{"k":9}]},null,{"id":5}],"post":"z"}`
	p, _ := NewParser[ilsNestHost]()
	tr := ilsExec(ilsNest, p, ilsContig, d, ilsM("full", ilsFull, -1), nil, false)
	we, _, ok := ilsExpectNest(d)
	if !ok {
		t.Fatal("oracle failed")
	}
	if tr.err != "" || !slices.Equal(tr.elems, we) {
		t.Errorf("non-leaf slot reuse differs from the non-stream decode: err=%q\n   got =%s\n   want=%s", tr.err, strings.Join(tr.elems, " "), strings.Join(we, " "))
	}
}

// ---------------------------------------------------------------- pooled panic storm

// TestIlsPooledHandlerPanicStorm alternates handler panics, handler errors and
// failing parses with good parses through the pooled entry point, where the
// borrowed Parser returns to the pool whatever state the failure left.
func TestIlsPooledHandlerPanicStorm(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	r := rand.New(rand.NewPCG(3, 5))
	modes := ilsLeaf.modes
	good := ilsLeaf.good
	bad := 0
	for i := 0; i < 600 && bad < 6; i++ {
		var in string
		if i%2 == 0 {
			in = good[r.IntN(len(good))]
		} else {
			ms := ilMutants(good[r.IntN(len(good))])
			in = ms[r.IntN(len(ms))]
		}
		m := modes[r.IntN(len(modes))]
		got := ilsExec(ilsLeaf, nil, ilsPooledDrv, in, m, nil, false)
		want := ilsExec(ilsLeaf, ilsMustParser[ilsHost](), ilsPooledDrv, in, m, nil, true)
		if !ilsEq(&want, &got) || got.corrupt != "" {
			bad++
			t.Errorf("iteration %d mode=%s in=%q corrupt=%q%s", i, m.name, ilsClip(in), got.corrupt, ilsTraceDiff(&want, &got))
		}
		if i%50 == 0 {
			runtime.GC()
		}
	}
}

// ---------------------------------------------------------------- concurrent pooled storms

func TestIlsPooledConcurrentStorm(t *testing.T) {
	const workers = 6
	errs := make(chan string, workers)
	for w := range workers {
		go func(w int) {
			r := rand.New(rand.NewPCG(uint64(w), 77))
			for i := range 250 {
				var in string
				if i%2 == 0 {
					in = ilsLeaf.good[r.IntN(len(ilsLeaf.good))]
				} else {
					ms := ilMutants(ilsLeaf.good[r.IntN(len(ilsLeaf.good))])
					in = ms[r.IntN(len(ms))]
				}
				m := ilsLeaf.modes[r.IntN(len(ilsLeaf.modes))]
				got := ilsExec(ilsLeaf, nil, ilsPooledDrv, in, m, nil, false)
				want := ilsExec(ilsLeaf, ilsMustParser[ilsHost](), ilsPooledDrv, in, m, nil, true)
				if !ilsEq(&want, &got) || got.corrupt != "" {
					errs <- fmt.Sprintf("worker %d iteration %d mode=%s in=%q corrupt=%q%s", w, i, m.name, ilsClip(in), got.corrupt, ilsTraceDiff(&want, &got))
					return
				}
			}
			errs <- ""
		}(w)
	}
	for range workers {
		if e := <-errs; e != "" {
			t.Error(e)
		}
	}
}

// ---------------------------------------------------------------- contiguous and feed on one Parser

// TestIlsFeedContigAlternation switches one Parser between the feed driver and
// contiguous input around failures. The views a scope installs differ between
// the two input models.
func TestIlsFeedContigAlternation(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	for _, rig := range []*ilsRig[ilsHost]{ilsLeaf} {
		p := ilsMustParser[ilsHost]()
		fresh := func() *Parser { return ilsMustParser[ilsHost]() }
		drvs := []ilsDriver{ilsContig, ilsFeedDrv(3), ilsFeedDrv(50), ilsContig, ilsFeedDrv(1)}
		bad := 0
		i := 0
		for _, g := range rig.good {
			for _, mu := range ilSweepMutants(g) {
				i++
				if i%7 != 0 {
					continue
				}
				d := drvs[i%len(drvs)]
				m := rig.modes[i%len(rig.modes)]
				got := ilsExec(rig, p, d, mu, m, nil, false)
				want := ilsExec(rig, fresh(), d, mu, m, nil, true)
				if !ilsEq(&want, &got) || got.corrupt != "" {
					bad++
					if bad <= 5 {
						t.Errorf("driver=%s mode=%s mutant=%q corrupt=%q%s", d.name, m.name, ilsClip(mu), got.corrupt, ilsTraceDiff(&want, &got))
					}
				}
				d2 := drvs[(i+2)%len(drvs)]
				gd := rig.good[i%len(rig.good)]
				full := ilsM("full", ilsFull, -1)
				g2 := ilsExec(rig, p, d2, gd, full, nil, false)
				w2 := ilsExec(rig, fresh(), d2, gd, full, nil, true)
				if !ilsEq(&w2, &g2) || g2.corrupt != "" {
					bad++
					if bad <= 5 {
						t.Errorf("good after failure: driver=%s doc=%q corrupt=%q%s", d2.name, ilsClip(gd), g2.corrupt, ilsTraceDiff(&w2, &g2))
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------- Decoder

type ilsDecLine struct {
	err   string
	elems string
	host  string
	acts  int
}

func ilsDecRun(data []byte, rd io.Reader, bufSize int, n int, mk func() (any, func() ilsDecLine)) (out []ilsDecLine) {
	defer func() {
		if r := recover(); r != nil {
			out = append(out, ilsDecLine{err: fmt.Sprintf("PANIC: %v", r)})
		}
	}()
	opts := []DecoderOption{WithSkipErrors(func(error) bool { return true })}
	if bufSize > 0 {
		opts = append(opts, WithBufferSize(bufSize))
	}
	d := NewDecoder(rd, opts...)
	for i := 0; i < n+4; i++ {
		v, collect := mk()
		err := d.Decode(v)
		if err == io.EOF {
			break
		}
		l := collect()
		l.err = ilClass(ilDescribeErr(err))
		out = append(out, l)
	}
	return out
}

// ilsLeafLineMaker builds a decode target whose handler records its elements.
func ilsLeafLineMaker(m ilsMode) func() (any, func() ilsDecLine) {
	return func() (any, func() ilsDecLine) {
		h := new(ilsHost)
		var tr ilsTrace
		ilsInstallLeaf(h, &tr, m)
		return h, func() ilsDecLine {
			return ilsDecLine{elems: strings.Join(tr.elems, " "), host: ilsHostDigest(h), acts: tr.acts}
		}
	}
}

func ilsNestLineMaker(m ilsMode) func() (any, func() ilsDecLine) {
	return func() (any, func() ilsDecLine) {
		h := new(ilsNestHost)
		var tr ilsTrace
		ilsInstallNest(h, &tr, m)
		return h, func() ilsDecLine {
			return ilsDecLine{elems: strings.Join(tr.elems, " "), host: h.Post, acts: tr.acts*100 + tr.inActs}
		}
	}
}

func ilsLineModel(lines []ilLine, mk func() (any, func() ilsDecLine), newParser func() *Parser) []ilsDecLine {
	var out []ilsDecLine
	for _, l := range lines {
		v, collect := mk()
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("PANIC: %v", r)
				}
			}()
			err = newParser().Unmarshal([]byte(l.text), v)
		}()
		r := collect()
		r.err = ilClass(ilDescribeErr(err))
		out = append(out, r)
	}
	return out
}

// ilsDecEqual compares decoder lines with the contiguous model. Failed lines
// compare as one class: the contiguous engine validates structure before any
// element is delivered, while the window engine delivers as it goes, so which
// of a handler error, a type mismatch and a syntax error wins may differ. What
// must agree is the line count and which lines succeed with which contents.
func ilsDecEqual(want, got []ilsDecLine) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if (want[i].err == "ok") != (got[i].err == "ok") {
			return false
		}
		if want[i].err == "ok" && want[i] != got[i] {
			return false
		}
	}
	return true
}

func ilsDecDiff(lines []ilLine, want, got []ilsDecLine) string {
	var sb strings.Builder
	for i := 0; i < max(len(want), len(got)); i++ {
		var w, g ilsDecLine
		ws, gs := "<none>", "<none>"
		if i < len(want) {
			w, ws = want[i], want[i].err
		}
		if i < len(got) {
			g, gs = got[i], got[i].err
		}
		mark := " "
		if i >= len(want) || i >= len(got) || (w.err == "ok") != (g.err == "ok") || (w.err == "ok" && w != g) {
			mark = "!"
		}
		txt := ""
		if i < len(lines) {
			txt = lines[i].text
		}
		fmt.Fprintf(&sb, "  %s line %2d want=%-6s got=%-6s %q\n", mark, i, ws, gs, txt)
		if mark == "!" && w.err == "ok" && g.err == "ok" {
			fmt.Fprintf(&sb, "      want elems=%s host=%s acts=%d\n      got  elems=%s host=%s acts=%d\n", ilsClip(w.elems), w.host, w.acts, ilsClip(g.elems), g.host, g.acts)
		}
	}
	return sb.String()
}

// ilsBuildLines mixes good lines with failing ones. With validOnly the failing
// lines are well-formed documents of the wrong types, which keeps a drain from
// resynchronising on garbage (a drain that misreads a malformed region ends on
// a later offset and swallows the next line, a consequence of the lenient
// skip reported by TestIlsDrainValidatesSyntax).
func ilsBuildLines(r *rand.Rand, n int, good []string, validOnly bool) []ilLine {
	if !validOnly {
		// Root-level mismatches (a scalar or array in place of the object)
		// trip the plain Decoder defect pinned by
		// TestIlsDecoderRootMismatchReportedOnce, so they stay out of here.
		var out []ilLine
		for _, l := range ilBuildLines(r, 3*n, good) {
			if strings.HasPrefix(strings.TrimSpace(l.text), "{") {
				out = append(out, l)
			}
		}
		return out[:min(n, len(out))]
	}
	var typeMut []string
	for _, g := range good {
		for _, m := range ilTypeMutants(g) {
			if strings.HasPrefix(m, "{") {
				typeMut = append(typeMut, m)
			}
		}
	}
	var lines []ilLine
	for len(lines) < n {
		if r.IntN(3) == 0 {
			lines = append(lines, ilLine{typeMut[r.IntN(len(typeMut))], ilLineTypeBad})
		} else {
			lines = append(lines, ilLine{good[r.IntN(len(good))], ilLineGood})
		}
	}
	return lines
}

func ilsDecoderSweep(t *testing.T, name string, good []string, validOnly bool, mkFor func(ilsMode) func() (any, func() ilsDecLine), modes []ilsMode, newParser func() *Parser) {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	r := rand.New(rand.NewPCG(21, 4))
	type readerMode struct {
		name string
		wrap func([]byte) io.Reader
	}
	rmodes := []readerMode{
		{"whole", func(b []byte) io.Reader { return bytes.NewReader(b) }},
		{"one-byte", func(b []byte) io.Reader { return iotest.OneByteReader(bytes.NewReader(b)) }},
		{"chunk7", func(b []byte) io.Reader { return &chunkReader{data: b, chunk: 7} }},
		{"chunk64", func(b []byte) io.Reader { return &chunkReader{data: b, chunk: 64} }},
		{"half", func(b []byte) io.Reader { return iotest.HalfReader(bytes.NewReader(b)) }},
	}
	bufSizes := []int{0, 16, 64, 1 << 12}
	fails := 0
	for round := 0; round < 24 && fails < 5; round++ {
		lines := ilsBuildLines(r, 10, good, validOnly)
		var sb strings.Builder
		for _, l := range lines {
			sb.WriteString(l.text)
			sb.WriteByte('\n')
		}
		data := []byte(sb.String())
		for _, m := range modes {
			mk := mkFor(m)
			want := ilsLineModel(lines, mk, newParser)
			for _, rm := range rmodes {
				for _, bs := range bufSizes {
					got := ilsDecRun(data, rm.wrap(data), bs, len(lines), mk)
					if ilsDecEqual(want, got) {
						continue
					}
					fails++
					diverges := func(ls []ilLine) bool {
						var sb strings.Builder
						for _, l := range ls {
							sb.WriteString(l.text)
							sb.WriteByte('\n')
						}
						b := []byte(sb.String())
						return !ilsDecEqual(ilsLineModel(ls, mk, newParser), ilsDecRun(b, rm.wrap(b), bs, len(ls), mk))
					}
					min := lines
					for changed := true; changed; {
						changed = false
						for i := 0; i < len(min); i++ {
							cand := append(append([]ilLine(nil), min[:i]...), min[i+1:]...)
							if len(cand) > 0 && diverges(cand) {
								min, changed = cand, true
								i--
							}
						}
					}
					var sb2 strings.Builder
					for _, l := range min {
						sb2.WriteString(l.text)
						sb2.WriteByte('\n')
					}
					mb := []byte(sb2.String())
					t.Errorf("%s mode=%s reader=%s buf=%d minimal (%d of %d lines):\n%s", name, m.name, rm.name, bs, len(min), len(lines),
						ilsDecDiff(min, ilsLineModel(min, mk, newParser), ilsDecRun(mb, rm.wrap(mb), bs, len(min), mk)))
					if fails >= 5 {
						return
					}
				}
			}
		}
	}
}

func TestIlsDecoderLeafLines(t *testing.T) {
	good := []string{ilsHostDoc(6), ilsHostDoc(4), ilsHostDoc(0), ilsHostDoc(9), `{"items":null,"post":"n"}`}
	modes := []ilsMode{ilsM("full", ilsFull, -1), ilsM("err@2", ilsErr, 2)}
	ilsDecoderSweep(t, "leaf", good, false, func(m ilsMode) func() (any, func() ilsDecLine) { return ilsLeafLineMaker(m) }, modes,
		func() *Parser { return ilsMustParser[ilsHost]() })
}

func TestIlsDecoderNestedLines(t *testing.T) {
	good := []string{ilsNestDoc(2, 3, 0), ilsNestDoc(1, 1, 1, 1, 1), ilsNestDoc(0), ilsNestDoc(4, 2)}
	modes := []ilsMode{ilsNestModes()[0], ilsNestModes()[11]}
	ilsDecoderSweep(t, "nested", good, false, func(m ilsMode) func() (any, func() ilsDecLine) { return ilsNestLineMaker(m) }, modes,
		func() *Parser { return ilsMustParser[ilsNestHost]() })
}

// TestIlsDecoderStreamStickyErrors uses a Decoder without a skip predicate:
// a failing document must stop the decoder with a stable sticky error, and
// the next decoder over good input must be unaffected.
func TestIlsDecoderStreamStickyErrors(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	good := ilsHostDoc(6)
	bad := ilsDefectDoc(7, 5)
	for _, bs := range []int{0, 16, 128} {
		for round := range 20 {
			input := good + "\n" + bad + "\n" + good + "\n"
			d := NewDecoder(&chunkReader{data: []byte(input), chunk: 5 + round}, WithBufferSize(max(bs, 1)))
			var seen []int
			var errs []string
			for range 5 {
				h := new(ilsHost)
				var tr ilsTrace
				ilsInstallLeaf(h, &tr, ilsM("full", ilsFull, -1))
				err := d.Decode(h)
				if err == io.EOF {
					break
				}
				seen = append(seen, len(tr.elems))
				errs = append(errs, ilClass(ilDescribeErr(err)))
			}
			if len(seen) < 2 || seen[0] != 6 || errs[0] != "ok" {
				t.Errorf("buf=%d round=%d: first good line wrong: seen=%v errs=%v", bs, round, seen, errs)
			}
			if len(errs) >= 2 && errs[1] == "ok" {
				t.Errorf("buf=%d round=%d: malformed line accepted: seen=%v errs=%v", bs, round, seen, errs)
			}
			if len(errs) >= 3 && errs[1] != "ok" && errs[2] != errs[1] && errs[2] != "ok" {
				t.Errorf("buf=%d round=%d: error after the failure is not sticky or recovered: errs=%v", bs, round, errs)
			}
		}
	}
}

func ilsDefectDoc(n, j int) string { return ilsDefects[0].apply(n, j) }

var _ = value.Value{}

// ---------------------------------------------------------------- focused reproducers

// ilsGuard runs f with a deadline. A parse that never terminates leaks its
// goroutine, so the reproducers that can hang run last in the file order and
// keep documents tiny.
func ilsGuard(f func() string) string {
	ch := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- fmt.Sprintf("PANIC: %v", r)
			}
		}()
		ch <- f()
	}()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		return "HANG (no result after 3s)"
	}
}

func ilsEventsJSON(n int) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"k":%d}`, i)
	}
	sb.WriteByte(']')
	return sb.String()
}

// TestIlsDrainValidatesSyntax: elements that a drain fast-forwards (no
// handler, Item.Skip, or an early exit) must still be validated. Elements
// inside the first batch are bound and rejected; the same defect in a later
// batch passes through the drain unnoticed.
func TestIlsDrainValidatesSyntax(t *testing.T) {
	type host struct {
		Items stream.Stream[ilsInner] `json:"items"`
		Post  string                  `json:"post"`
	}
	defects := []string{`{"n":}`, `{"n":tru}`, `{"n":1e"x":2}`, `{"n":1]`, `{"n":01}`, `{"n":"a\q"}`}
	modes := []string{"nohandler", "skip-first", "break-first"}
	for _, df := range defects {
		for _, mode := range modes {
			var accepted []int
			for j := range 10 {
				var el []string
				for i := range 10 {
					if i == j {
						el = append(el, df)
					} else {
						el = append(el, `{"n":1}`)
					}
				}
				doc := `{"items":[` + strings.Join(el, ",") + `],"post":"z"}`
				if json.Valid([]byte(doc)) {
					t.Fatalf("test document is valid: %s", doc)
				}
				var h host
				if mode != "nohandler" {
					h.Items.OnRead(func(sc stream.Scope[ilsInner]) error {
						for it := range sc.Iter() {
							if mode == "skip-first" {
								return it.Skip()
							}
							return it.Decode()
						}
						return nil
					})
				}
				p, _ := NewParser[host]()
				if err := p.Unmarshal([]byte(doc), &h); err == nil {
					accepted = append(accepted, j)
				}
			}
			if len(accepted) > 0 {
				t.Errorf("mode=%s: malformed element %s accepted when it sits at index %v of 10 (first batch holds 4); want a syntax error at every index", mode, df, accepted)
			}
		}
	}
}

// TestIlsBreakLeafBeyondBatchTerminates: Scope.Break returned from a leaf
// stream whose array holds more than one batch must end the parse.
func TestIlsBreakLeafBeyondBatchTerminates(t *testing.T) {
	for n := 4; n <= 6; n++ {
		doc := `{"items":` + ilsEventsJSON(n) + `,"post":"z"}`
		type host struct {
			Items stream.Stream[ilsEvent] `json:"items"`
			Post  string                  `json:"post"`
		}
		res := ilsGuard(func() string {
			var h host
			h.Items.OnRead(func(sc stream.Scope[ilsEvent]) error {
				for it := range sc.Iter() {
					_ = it
					return sc.Break()
				}
				return nil
			})
			return fmt.Sprintf("err=%v post=%q", Unmarshal([]byte(doc), &h), h.Post)
		})
		if strings.HasPrefix(res, "HANG") || strings.HasPrefix(res, "PANIC") {
			t.Errorf("self Break at element 0 of a %d-element leaf stream: %s; doc=%s", n, res, doc)
		}
	}
	for n := 4; n <= 6; n++ {
		doc := `{"users":[{"id":1,"events":` + ilsEventsJSON(n) + `}],"post":"z"}`
		res := ilsGuard(func() string {
			var h ilsNestHost
			h.Users.OnRead(func(users stream.Scope[ilsUser]) error {
				for it := range users.Iter() {
					it.Target().Events.OnRead(func(es stream.Scope[ilsEvent]) error {
						for range es.Iter() {
							return users.Break()
						}
						return nil
					})
					err := it.Decode()
					if users.IsBreak(err) {
						break
					}
					if err != nil {
						return err
					}
				}
				return nil
			})
			return fmt.Sprintf("err=%v post=%q", Unmarshal([]byte(doc), &h), h.Post)
		})
		if strings.HasPrefix(res, "HANG") || strings.HasPrefix(res, "PANIC") {
			t.Errorf("outer Break from event 0 of a %d-event inner leaf stream: %s; doc=%s", n, res, doc)
		}
	}
}

// TestIlsEmptyInnerStreamNoPhantomElement: the last element of a non-leaf
// outer stream, when it holds an empty array for a nested stream, is delivered
// twice.
func TestIlsEmptyInnerStreamNoPhantomElement(t *testing.T) {
	docs := []string{
		`{"users":[{"id":2,"name":"a","events":[]}],"post":"z"}`,
		`{"users":[{"events":[],"id":2}],"post":"z"}`,
		`{"users":[{"events":[],"name":"a","id":2}],"post":"z"}`,
		`{"users":[{"id":2,"events":[],"name":"a"}],"post":"z"}`,
	}
	for _, d := range docs {
		var h ilsNestHost
		var ids []int
		h.Users.OnRead(func(users stream.Scope[ilsUser]) error {
			for it := range users.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				ids = append(ids, it.Target().ID)
			}
			return nil
		})
		err := Unmarshal([]byte(d), &h)
		if err != nil || len(ids) != 1 {
			t.Errorf("doc=%s: err=%v delivered ids=%v, want exactly one element [2]", d, err, ids)
		}
	}
}

// TestIlsEmptyThenShortInnerStream: an element whose nested stream array is
// empty, followed by an element whose nested array holds two to four items,
// hands the second handler an item with a nil target.
func TestIlsEmptyThenShortInnerStream(t *testing.T) {
	for n := 1; n <= 6; n++ {
		doc := `{"users":[{"id":1,"events":[]},{"id":2,"events":` + ilsEventsJSON(n) + `}],"post":"z"}`
		res := ilsGuard(func() string {
			var h ilsNestHost
			var got []int
			h.Users.OnRead(func(users stream.Scope[ilsUser]) error {
				for it := range users.Iter() {
					id := -1
					it.Target().Events.OnRead(func(es stream.Scope[ilsEvent]) error {
						for ev := range es.Iter() {
							if err := ev.Decode(); err != nil {
								return err
							}
							id = id*0 + 1
							_ = ev.Target().K
						}
						return nil
					})
					if err := it.Decode(); err != nil {
						return err
					}
					got = append(got, it.Target().ID)
				}
				return nil
			})
			err := Unmarshal([]byte(doc), &h)
			return fmt.Sprintf("err=%v users=%v", err, got)
		})
		if res != "err=<nil> users=[1 2]" {
			t.Errorf("empty inner array then %d inner items: %s; want users=[1 2]; doc=%s", n, res, doc)
		}
	}
}

// TestIlsDecoderDrainValidLines keeps break and skip handlers on a Decoder
// across lines whose failures are type mismatches only.
func TestIlsDecoderDrainValidLines(t *testing.T) {
	good := []string{ilsHostDoc(6), ilsHostDoc(4), ilsHostDoc(9), ilsHostDoc(1)}
	modes := []ilsMode{ilsM("break@1", ilsBreak, 1), ilsM("skip@2", ilsSkip, 2), ilsM("return@5", ilsEarlyReturn, 5)}
	ilsDecoderSweep(t, "leaf-drain", good, true, func(m ilsMode) func() (any, func() ilsDecLine) { return ilsLeafLineMaker(m) }, modes,
		func() *Parser { return ilsMustParser[ilsHost]() })
	ngood := []string{ilsNestDoc(2, 3, 0), ilsNestDoc(1, 1, 1, 1, 1), ilsNestDoc(4, 2)}
	nmodes := []ilsMode{ilsNestModes()[1], ilsNestModes()[2], ilsNestModes()[5], ilsNestModes()[7]}
	ilsDecoderSweep(t, "nested-drain", ngood, true, func(m ilsMode) func() (any, func() ilsDecLine) { return ilsNestLineMaker(m) }, nmodes,
		func() *Parser { return ilsMustParser[ilsNestHost]() })
}

// TestIlsDecoderRootMismatchReportedOnce: a root type mismatch consumes its
// value, so one bad line yields exactly one error whatever the window size and
// read granularity. No stream is involved.
func TestIlsDecoderRootMismatchReportedOnce(t *testing.T) {
	type rec struct {
		A int `json:"a"`
	}
	data := "{\"a\":1}\n[]\n"
	bad := 0
	for _, bs := range []int{4, 8, 12, 16, 24, 32} {
		for _, name := range []string{"half", "one-byte", "whole"} {
			var rd io.Reader = strings.NewReader(data)
			switch name {
			case "half":
				rd = iotest.HalfReader(rd)
			case "one-byte":
				rd = iotest.OneByteReader(rd)
			}
			d := NewDecoder(rd, WithSkipErrors(func(error) bool { return true }), WithBufferSize(bs))
			var got []string
			for range 6 {
				var v rec
				err := d.Decode(&v)
				if err == io.EOF {
					break
				}
				got = append(got, ilClass(ilDescribeErr(err)))
			}
			if !slices.Equal(got, []string{"ok", "type"}) {
				bad++
				if bad <= 4 {
					t.Errorf("buf=%d reader=%s input=%q: got %v, want [ok type]", bs, name, data, got)
				}
			}
		}
	}
}
