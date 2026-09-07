package bind

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// Multi-GiB feed stress tests, opt-in via environment variables. Each reader
// synthesizes randomly shaped items from a seeded PRNG while tracking the
// aggregates the consumer must reproduce, so correctness is checked against
// generation, not against a fixture.
//
// Item shape mixes every scoped-arena writer: variable-length strings (str
// arena), a slice field (slot churn), a Value field (tape snapshots) with
// deterministic nulls, and a variant field whose discriminator binds before a
// pad that grows the string arena mid element, so case selection at the
// variant field must prove provenance through retired generations
// (strprov.go). Omitted fields never appear: a reused backing retains the
// previous element's fields, so absence is expressed as null.
//
//	VJSON_STREAM_STRESS_GB=4 go test ./decode/bind/ -run TestStreamStressFeedGB -v -timeout 30m
//	VJSON_NDJSON_STRESS_GB=4 go test ./decode/bind/ -run TestNdjsonStressFeedGB -v -timeout 30m
//
// VJSON_*_HOLD_EVERY=<n> additionally retains every n-th element (a struct
// copy) so generation pinning and tape snapshots are observable against the
// pure-streaming baseline.
//
// TestStreamStressStrProv and TestNdjsonStressStrProv are the always-on
// second-scale versions: same corpus, retired-generation coverage asserted,
// retired bases above the live count verified nil, so the truncation
// discipline regresses in every test run instead of only in manual GiB runs.

const (
	// stressGCEvery forces a GC cycle each n-th element, so long runs see the
	// collector between retirements exactly like a live service parsing a
	// long stream.
	stressGCEvery = 16_000

	// stressHeapGate bounds live memory growth over the run's baseline at the
	// in-run GC checkpoints and after a finished run with nothing held. The
	// gate is baseline-relative because a shared test process carries pools
	// and unswept residue from earlier tests; the baseline is sampled after a
	// forced GC at run start. The in-run gate is the primary one: a
	// scoped-release leak accumulates during the parse and the teardown
	// Release clears it, so a post-run sample alone watches an empty heap. A
	// real leak grows linearly with input and clears the gate by orders of
	// magnitude; the steady state stays an order below.
	stressHeapGate = 64 << 20
)

// stressHeapBase samples the live heap the leak gates compare against.
func stressHeapBase() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

type stressElem struct {
	ID   string      `json:"id"`
	Kind string      `json:"kind"`
	Pad  string      `json:"pad"`
	P    any         `json:"p" vjson:"variant=kind"`
	N    int64       `json:"n"`
	Tags []string    `json:"tags"`
	V    value.Value `json:"v"`
}

type stressHost struct {
	Items stream.Stream[stressElem] `json:"items"`
}

// stressKindTags lists the variant tags in the order the generator accounts
// for them; consumer aggregates line up by case type.
var stressKindTags = [3]string{"msg", "list", "deep"}

// Each case writes the string arena so case content and the provenance
// interval overlap inside every generation.
type stressCaseMsg struct {
	Msg string `json:"msg"`
	W   int64  `json:"w"`
}

type stressCaseList struct {
	Items []string `json:"items"`
	F     float64  `json:"f"`
}

type stressCaseDeep struct {
	Deep value.Value `json:"deep"`
	K    int64       `json:"k"`
}

func init() {
	vbind.DefineVariantCases[stressElem, struct {
		_ stressCaseMsg  `case:"msg"`
		_ stressCaseList `case:"list"`
		_ stressCaseDeep `case:"deep"`
	}]()
}

// stressAgg carries the per-run aggregates: the generator accumulates what it
// emitted, the consumer what it decoded, and the two must be equal.
type stressAgg struct {
	items     int64
	sumN      int64
	sumTags   int64
	kindCount [3]int64
	sumW      int64
	sumItems  int64
	sumK      int64
}

// accountElem folds one decoded element in. A case selection that misroutes
// lands in the wrong bucket or hits nil, so stale discriminators and
// provenance misses surface here.
func (a *stressAgg) accountElem(e *stressElem) error {
	idx := a.items
	a.items++
	a.sumN += e.N
	a.sumTags += int64(len(e.Tags))
	switch c := e.P.(type) {
	case stressCaseMsg:
		a.kindCount[0]++
		a.sumW += c.W
	case stressCaseList:
		a.kindCount[1]++
		a.sumItems += int64(len(c.Items))
	case stressCaseDeep:
		a.kindCount[2]++
		a.sumK += c.K
	default:
		return fmt.Errorf("item %d: variant case %T", idx, e.P)
	}
	return nil
}

// stressGen produces randomly shaped stressElem JSON from a seeded PRNG and
// tracks the aggregates the consumer must reproduce. Two framing readers
// share it: an array document for Stream[T], and newline-delimited values
// for the Decoder.
type stressGen struct {
	rand *rand.Rand
	agg  stressAgg
}

func newStressGen() *stressGen {
	return &stressGen{rand: rand.New(rand.NewSource(20260903))}
}

func (g *stressGen) stats() stressAgg {
	return g.agg
}

// appendStressPad emits one pad by index cadence: every 512th element carries
// 160..192KiB so a single pad spans several default windows, every 8th a
// 16..32KiB one, the rest stay tiny. The cadence is deterministic, so the
// very first element already grows the arena between the discriminator and
// the variant field.
func appendStressPad(b []byte, idx int64, r *rand.Rand) []byte {
	n := r.Intn(33)
	switch {
	case idx%512 == 0:
		n = 160*1024 + r.Intn(32*1024)
	case idx%8 == 4:
		n = 16*1024 + r.Intn(16*1024)
	}
	for i := 0; i < n; i++ {
		b = append(b, byte('a'+r.Intn(26)))
	}
	return b
}

func (g *stressGen) appendItem(b []byte) []byte {
	idx := g.agg.items
	g.agg.items++
	n := g.rand.Int63n(1 << 24)
	g.agg.sumN += n

	b = append(b, `{"id":"id-`...)
	b = strconv.AppendInt(b, idx, 10)
	b = append(b, '-')
	for i, pad := 0, g.rand.Intn(33); i < pad; i++ {
		b = append(b, byte('a'+g.rand.Intn(26)))
	}

	// The discriminator binds first, the pad grows the string arena mid
	// element, and the case selection at "p" must recognize a discriminator
	// left in a retired generation.
	ki := g.rand.Intn(3)
	g.agg.kindCount[ki]++
	b = append(b, `","kind":"`...)
	b = append(b, stressKindTags[ki]...)
	b = append(b, `","pad":"`...)
	b = appendStressPad(b, idx, g.rand)
	b = append(b, `","p":`...)
	switch ki {
	case 0:
		g.agg.sumW += n
		b = append(b, `{"msg":"m-`...)
		b = strconv.AppendInt(b, idx, 10)
		b = append(b, '-')
		for i, pad := 0, g.rand.Intn(17); i < pad; i++ {
			b = append(b, byte('a'+g.rand.Intn(26)))
		}
		b = append(b, `","w":`...)
		b = strconv.AppendInt(b, n, 10)
		b = append(b, '}')
	case 1:
		nitems := g.rand.Intn(4)
		g.agg.sumItems += int64(nitems)
		b = append(b, `{"items":[`...)
		for i := 0; i < nitems; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, `"t-`...)
			b = strconv.AppendInt(b, g.rand.Int63n(1000), 10)
			b = append(b, '"')
		}
		b = append(b, `],"f":`...)
		b = strconv.AppendFloat(b, float64(n%1000), 'g', -1, 64)
		b = append(b, '}')
	default:
		k := idx % 97
		g.agg.sumK += k
		b = append(b, `{"deep":{"d":`...)
		b = strconv.AppendInt(b, idx, 10)
		b = append(b, `,"w":`...)
		b = strconv.AppendInt(b, n, 10)
		b = append(b, `},"k":`...)
		b = strconv.AppendInt(b, k, 10)
		b = append(b, '}')
	}

	b = append(b, `,"n":`...)
	b = strconv.AppendInt(b, n, 10)

	b = append(b, `,"tags":`...)
	if g.rand.Intn(8) == 0 {
		b = append(b, `null`...)
	} else {
		ntags := g.rand.Intn(4)
		g.agg.sumTags += int64(ntags)
		b = append(b, '[')
		for i := 0; i < ntags; i++ {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, `"t-`...)
			b = strconv.AppendInt(b, g.rand.Int63n(1000), 10)
			b = append(b, '"')
		}
		b = append(b, ']')
	}

	if idx%8 == 0 {
		b = append(b, `,"v":null`...)
	} else {
		b = append(b, `,"v":{"k":"v-`...)
		b = strconv.AppendInt(b, idx, 10)
		b = append(b, `","w":`...)
		b = strconv.AppendInt(b, n, 10)
		b = append(b, '}')
	}
	if g.rand.Intn(8) == 0 {
		b = append(b, `,"x":{"junk":[1,2,3],"more":"`...)
		for i, pad := 0, g.rand.Intn(17); i < pad; i++ {
			b = append(b, byte('a'+g.rand.Intn(26)))
		}
		b = append(b, `"}`...)
	}
	return append(b, '}')
}

// stressStreamReader frames stressGen output as a {"items":[...]} document of
// roughly the target byte size. Read serves an internal buffer refilled with
// whole items, so any window size the feed driver chooses works.
type stressStreamReader struct {
	gen *stressGen

	buf     []byte
	pos     int
	started bool
	done    bool

	target  int64
	emitted int64

	// sample runs inside Read while the feed driver refills mid parse, after
	// earlier mounts in the same batch retired generations and before the
	// batch settle truncates them, so it observes the live table height.
	sample func()
}

func newStressStreamReader(bytes int64) *stressStreamReader {
	return &stressStreamReader{
		gen:    newStressGen(),
		buf:    make([]byte, 0, 1<<16),
		target: bytes,
	}
}

func (r *stressStreamReader) stats() stressAgg {
	return r.gen.stats()
}

func (r *stressStreamReader) Read(p []byte) (int, error) {
	if r.sample != nil {
		r.sample()
	}
	for {
		if r.pos < len(r.buf) {
			n := copy(p, r.buf[r.pos:])
			r.pos += n
			return n, nil
		}
		if r.done {
			return 0, io.EOF
		}
		r.refill()
	}
}

func (r *stressStreamReader) refill() {
	r.buf = r.buf[:0]
	r.pos = 0
	if !r.started {
		r.buf = append(r.buf, `{"items":[`...)
		r.started = true
		return
	}
	if r.emitted >= r.target {
		r.buf = append(r.buf, `]}`...)
		r.done = true
		return
	}
	for r.emitted < r.target && len(r.buf) < 1<<15 {
		if r.gen.agg.items > 0 {
			r.buf = append(r.buf, ',')
			r.emitted++
		}
		before := len(r.buf)
		r.buf = r.gen.appendItem(r.buf)
		r.emitted += int64(len(r.buf) - before)
	}
}

// ndjsonReader frames stressGen output as newline-delimited values of roughly
// the target byte size, one value per line.
type ndjsonReader struct {
	gen *stressGen

	buf  []byte
	pos  int
	done bool

	target  int64
	emitted int64

	// sample mirrors stressStreamReader.sample: Read runs mid value, after a
	// mount retired a generation and before the value-end defer truncates.
	sample func()
}

func newNdjsonReader(bytes int64) *ndjsonReader {
	return &ndjsonReader{
		gen:    newStressGen(),
		buf:    make([]byte, 0, 1<<16),
		target: bytes,
	}
}

func (r *ndjsonReader) stats() stressAgg {
	return r.gen.stats()
}

func (r *ndjsonReader) Read(p []byte) (int, error) {
	if r.sample != nil {
		r.sample()
	}
	for {
		if r.pos < len(r.buf) {
			n := copy(p, r.buf[r.pos:])
			r.pos += n
			return n, nil
		}
		if r.done {
			return 0, io.EOF
		}
		r.refill()
	}
}

func (r *ndjsonReader) refill() {
	r.buf = r.buf[:0]
	r.pos = 0
	for r.emitted < r.target && len(r.buf) < 1<<15 {
		before := len(r.buf)
		r.buf = r.gen.appendItem(r.buf)
		r.buf = append(r.buf, '\n')
		r.emitted += int64(len(r.buf) - before)
	}
	if r.emitted >= r.target {
		r.done = true
	}
}

// stressMachine returns the parser's bind machine. Iteration callbacks run
// between yields while native is suspended, so the provenance table is
// readable there.
func stressMachine(p *Parser) *ndec.BindMachine {
	return (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
}

// assertProvEmpty verifies the end state every path must reach: no live
// entry, and no base left above the count in the noscan block.
func assertProvEmpty(t *testing.T, m *ndec.BindMachine) {
	t.Helper()
	if c := m.StrProvCount(); c != 0 {
		t.Fatalf("provenance count after run: %d", c)
	}
	for i := 0; i < ndec.BindStrProvMax; i++ {
		if base := strProvEntryBase(m, i); base != nil {
			t.Fatalf("provenance entry %d above count 0 kept base %p", i, base)
		}
	}
}

// checkValueRead verifies a sampled element's Value against its directly
// bound fields. The generator makes V null exactly when idx%8 == 0.
func checkValueRead(idx int64, e *stressElem) error {
	kv := e.V.Get("k")
	if ks, ok := kv.Str(); !ok || ks != "v-"+strconv.FormatInt(idx, 10) {
		return fmt.Errorf("item %d: V.k = %q,%v", idx, ks, ok)
	}
	wv := e.V.Get("w")
	if w, ok := wv.Int(); !ok || w != e.N {
		return fmt.Errorf("item %d: V.w = %d,%v want %d", idx, w, ok, e.N)
	}
	return nil
}

// checkPolyRead verifies a sampled element's variant content against its
// directly bound fields, so case content stays paired with the case the
// discriminator selected.
func checkPolyRead(idx int64, e *stressElem) error {
	switch c := e.P.(type) {
	case stressCaseMsg:
		if c.W != e.N || !strings.HasPrefix(c.Msg, "m-"+strconv.FormatInt(idx, 10)+"-") {
			return fmt.Errorf("item %d: msg case w=%d n=%d msg=%q", idx, c.W, e.N, c.Msg)
		}
	case stressCaseList:
		if c.F != float64(e.N%1000) {
			return fmt.Errorf("item %d: list case f=%v n=%d", idx, c.F, e.N)
		}
	case stressCaseDeep:
		if c.K != idx%97 {
			return fmt.Errorf("item %d: deep case k=%d", idx, c.K)
		}
		dv := c.Deep.Get("d")
		wv := c.Deep.Get("w")
		d, _ := dv.Int()
		w, _ := wv.Int()
		if d != idx || w != e.N {
			return fmt.Errorf("item %d: deep case d=%d w=%d", idx, d, w)
		}
	default:
		return fmt.Errorf("item %d: variant case %T", idx, e.P)
	}
	return nil
}

func TestStreamStressFeedGB(t *testing.T) {
	gbEnv := os.Getenv("VJSON_STREAM_STRESS_GB")
	if gbEnv == "" {
		t.Skip("set VJSON_STREAM_STRESS_GB=<gibibytes> to run")
	}
	gb, err := strconv.ParseInt(gbEnv, 10, 64)
	if err != nil || gb <= 0 {
		t.Fatalf("VJSON_STREAM_STRESS_GB=%q: %v", gbEnv, err)
	}
	holdEvery, _ := strconv.ParseInt(os.Getenv("VJSON_STREAM_HOLD_EVERY"), 10, 64)

	src := newStressStreamReader(gb << 30)
	p, err := NewParser[stressHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	m := stressMachine(p)
	heapBase := stressHeapBase()
	var seen stressAgg
	var maxProv uint32
	var held []*stressElem
	var ms runtime.MemStats
	var h stressHost
	src.sample = func() {
		if c := m.StrProvCount(); c > maxProv {
			maxProv = c
		}
	}
	h.Items.OnRead(func(s stream.Scope[stressElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			e := it.Target()
			idx := seen.items
			if err := seen.accountElem(e); err != nil {
				return err
			}
			if holdEvery > 0 && seen.items%holdEvery == 0 {
				cp := *e
				held = append(held, &cp)
			}
			if seen.items%1024 == 0 && idx%8 != 0 {
				if err := checkValueRead(idx, e); err != nil {
					return err
				}
				if err := checkPolyRead(idx, e); err != nil {
					return err
				}
			}
			if seen.items%stressGCEvery == 0 {
				runtime.GC()
				runtime.ReadMemStats(&ms)
				t.Logf("items=%dM heapAlloc=%dMiB heapSys=%dMiB totalAlloc=%dGiB provMax=%d",
					seen.items/1_000_000, ms.HeapAlloc>>20, ms.HeapSys>>20, ms.TotalAlloc>>30, maxProv)
				if holdEvery == 0 && ms.HeapAlloc > heapBase+stressHeapGate {
					t.Fatalf("heapAlloc=%dMiB at %d items exceeds baseline+%dMiB; retained backings leak",
						ms.HeapAlloc>>20, seen.items, stressHeapGate>>20)
				}
			}
		}
		return nil
	})
	if err := p.UnmarshalFeed(src, &h); err != nil {
		t.Fatalf("UnmarshalFeed: %v", err)
	}
	if seen != src.stats() {
		t.Fatalf("aggregates: got %+v want %+v", seen, src.stats())
	}
	if maxProv == 0 {
		t.Fatal("string arena retired no generation; provenance path not exercised")
	}
	assertProvEmpty(t, m)
	runtime.GC()
	runtime.ReadMemStats(&ms)
	t.Logf("done: items=%d held=%d provMax=%d heapAlloc=%dMiB heapSys=%dMiB",
		seen.items, len(held), maxProv, ms.HeapAlloc>>20, ms.HeapSys>>20)
	if holdEvery == 0 && ms.HeapAlloc > heapBase+stressHeapGate {
		t.Fatalf("heapAlloc=%dMiB after GC exceeds baseline+%dMiB; retired backings leak",
			ms.HeapAlloc>>20, stressHeapGate>>20)
	}
}

func TestNdjsonStressFeedGB(t *testing.T) {
	gbEnv := os.Getenv("VJSON_NDJSON_STRESS_GB")
	if gbEnv == "" {
		t.Skip("set VJSON_NDJSON_STRESS_GB=<gibibytes> to run")
	}
	gb, err := strconv.ParseInt(gbEnv, 10, 64)
	if err != nil || gb <= 0 {
		t.Fatalf("VJSON_NDJSON_STRESS_GB=%q: %v", gbEnv, err)
	}
	holdEvery, _ := strconv.ParseInt(os.Getenv("VJSON_NDJSON_HOLD_EVERY"), 10, 64)

	src := newNdjsonReader(gb << 30)
	// A small window makes the ramp phase span many mounts, so the first
	// huge-pad value retires several generations within one value and the
	// reader observes them; arena capacity then converges geometrically and
	// the rest of the run verifies the converged steady state under forced
	// GC, not recurring growth.
	d := NewDecoder(src, WithBufferSize(1<<12))
	heapBase := stressHeapBase()
	var seen stressAgg
	var maxProv uint32
	var held []*stressElem
	var ms runtime.MemStats
	src.sample = func() {
		if d.parser == nil {
			return
		}
		if c := stressMachine(d.parser).StrProvCount(); c > maxProv {
			maxProv = c
		}
	}
	for {
		var e stressElem
		err := d.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Decode at value %d: %v", seen.items, err)
		}
		idx := seen.items
		if err := seen.accountElem(&e); err != nil {
			t.Fatalf("%v", err)
		}
		if holdEvery > 0 && seen.items%holdEvery == 0 {
			cp := e
			held = append(held, &cp)
		}
		if seen.items%1024 == 0 && idx%8 != 0 {
			if err := checkValueRead(idx, &e); err != nil {
				t.Fatalf("%v", err)
			}
			if err := checkPolyRead(idx, &e); err != nil {
				t.Fatalf("%v", err)
			}
		}
		m := stressMachine(d.parser)
		for i := int(m.StrProvCount()); i < ndec.BindStrProvMax; i++ {
			if base := strProvEntryBase(m, i); base != nil {
				t.Fatalf("value %d: provenance entry %d above count %d kept base %p",
					seen.items, i, m.StrProvCount(), base)
			}
		}
		if seen.items%stressGCEvery == 0 {
			runtime.GC()
			runtime.ReadMemStats(&ms)
			t.Logf("values=%dM heapAlloc=%dMiB heapSys=%dMiB totalAlloc=%dGiB provMax=%d",
				seen.items/1_000_000, ms.HeapAlloc>>20, ms.HeapSys>>20, ms.TotalAlloc>>30, maxProv)
			if holdEvery == 0 && ms.HeapAlloc > heapBase+stressHeapGate {
				t.Fatalf("heapAlloc=%dMiB at %d values exceeds baseline+%dMiB; retained backings leak",
					ms.HeapAlloc>>20, seen.items, stressHeapGate>>20)
			}
		}
	}
	if seen != src.stats() {
		t.Fatalf("aggregates: got %+v want %+v", seen, src.stats())
	}
	if maxProv == 0 {
		t.Fatal("string arena retired no generation; provenance path not exercised")
	}
	runtime.GC()
	runtime.ReadMemStats(&ms)
	t.Logf("done: values=%d held=%d provMax=%d heapAlloc=%dMiB heapSys=%dMiB",
		seen.items, len(held), maxProv, ms.HeapAlloc>>20, ms.HeapSys>>20)
	if holdEvery == 0 && ms.HeapAlloc > heapBase+stressHeapGate {
		t.Fatalf("heapAlloc=%dMiB after GC exceeds baseline+%dMiB; retired backings leak",
			ms.HeapAlloc>>20, stressHeapGate>>20)
	}
}

// TestStreamStressStrProv is the always-on stream-scale provenance stress:
// default window, full aggregate check, the retired-generation history
// asserted non-empty so the run proves it exercises the mechanism, and the
// end state clean.
func TestStreamStressStrProv(t *testing.T) {
	src := newStressStreamReader(32 << 20)
	p, err := NewParser[stressHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	m := stressMachine(p)
	heapBase := stressHeapBase()
	var seen stressAgg
	var maxProv uint32
	var ms runtime.MemStats
	var h stressHost
	src.sample = func() {
		if c := m.StrProvCount(); c > maxProv {
			maxProv = c
		}
	}
	h.Items.OnRead(func(s stream.Scope[stressElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			e := it.Target()
			idx := seen.items
			if err := seen.accountElem(e); err != nil {
				return err
			}
			if idx%256 == 0 && idx%8 != 0 {
				if err := checkValueRead(idx, e); err != nil {
					return err
				}
				if err := checkPolyRead(idx, e); err != nil {
					return err
				}
			}
			if seen.items%1024 == 0 {
				runtime.GC()
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > heapBase+stressHeapGate {
					t.Fatalf("heapAlloc=%dMiB at %d items exceeds baseline+%dMiB; retained backings leak",
						ms.HeapAlloc>>20, seen.items, stressHeapGate>>20)
				}
			}
		}
		return nil
	})
	if err := p.UnmarshalFeed(src, &h); err != nil {
		t.Fatalf("UnmarshalFeed: %v", err)
	}
	if seen != src.stats() {
		t.Fatalf("aggregates: got %+v want %+v", seen, src.stats())
	}
	if maxProv == 0 {
		t.Fatal("string arena retired no generation; provenance path not exercised")
	}
	assertProvEmpty(t, m)
	t.Logf("items=%d provMax=%d", seen.items, maxProv)
}

// TestNdjsonStressStrProv is the always-on Decoder-scale provenance stress. A
// small window makes each value's arena sizing tight, so mounts during the
// positioning loop grow the arena and retire generations; the per-value defer
// must nil every dropped base. Arena capacity converges geometrically, so
// retirements concentrate in the ramp while the rest of the run verifies the
// converged steady state.
func TestNdjsonStressStrProv(t *testing.T) {
	src := newNdjsonReader(8 << 20)
	d := NewDecoder(src, WithBufferSize(1<<12))
	var maxProv uint32
	src.sample = func() {
		if d.parser == nil {
			return
		}
		if c := stressMachine(d.parser).StrProvCount(); c > maxProv {
			maxProv = c
		}
	}

	var seen stressAgg
	for {
		var e stressElem
		err := d.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Decode at value %d: %v", seen.items, err)
		}
		idx := seen.items
		if err := seen.accountElem(&e); err != nil {
			t.Fatalf("%v", err)
		}
		if idx%64 == 0 && idx%8 != 0 {
			if err := checkValueRead(idx, &e); err != nil {
				t.Fatalf("%v", err)
			}
			if err := checkPolyRead(idx, &e); err != nil {
				t.Fatalf("%v", err)
			}
		}
		m := stressMachine(d.parser)
		for i := int(m.StrProvCount()); i < ndec.BindStrProvMax; i++ {
			if base := strProvEntryBase(m, i); base != nil {
				t.Fatalf("value %d: provenance entry %d above count %d kept base %p",
					seen.items, i, m.StrProvCount(), base)
			}
		}
	}
	if seen != src.stats() {
		t.Fatalf("aggregates: got %+v want %+v", seen, src.stats())
	}
	if maxProv == 0 {
		t.Fatal("string arena retired no generation; provenance path not exercised")
	}
	t.Logf("values=%d provMax=%d", seen.items, maxProv)
}
