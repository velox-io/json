//go:build vjstackstress

// Stack-depth stress workloads. Each case targets one of the deepest
// static native chains: float slow-path binding (bind atof chain),
// indent-mode encoding (full VM), tape re-serialization (tape walk),
// reformatting (fmt), and yield re-entry paths (large strings, interface
// handlers). See stackstress.go for the sweep and canary mechanics.
package stackstress

import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/native/encvm"
)

// semanticMismatch compares two JSON documents by decoding both into any
// and matching the values, tolerating formatting and map-order
// differences.
func semanticMismatch(got, want []byte) error {
	var va, vb any
	if err := stdjson.Unmarshal(got, &va); err != nil {
		return fmt.Errorf("got invalid JSON: %v\n%s", err, got)
	}
	if err := stdjson.Unmarshal(want, &vb); err != nil {
		return fmt.Errorf("want invalid JSON: %v\n%s", err, want)
	}
	if !reflect.DeepEqual(va, vb) {
		return fmt.Errorf("semantic mismatch:\n got: %s\nwant: %s", got, want)
	}
	return nil
}

// patternString builds a deterministic byte string interleaving plain
// text with characters that force encoder escape handling.
func patternString(n int) string {
	const alphabet = "ab0123456789 \t\n\\\"zq"
	var sb strings.Builder
	x := uint32(0x9e3779b9)
	for i := 0; i < n; i++ {
		x = x*1664525 + 1013904223
		sb.WriteByte(alphabet[int(x>>16)%len(alphabet)])
	}
	return sb.String()
}

type floatDoc struct {
	F float64 `json:"f"`
	G float64 `json:"g"`
}

// floatDocs carries subnormal, overflow-boundary, denormal-boundary and
// midpoint-tie literals plus an integer beyond the 53-bit mantissa. Each
// drives the bind float path through the atof comparison chain.
var floatDocs = []string{
	`{"f":2.2250738585072011e-308,"g":9007199254740993}`,
	`{"f":5e-324,"g":1.7976931348623157e308}`,
	`{"f":0.3000000000000000444089209850062616169452667236328125,"g":2.2250738585072014e-308}`,
}

// floatRefBits holds the bit patterns encoding/json decodes for floatDocs,
// fixed once at init so a mismatch report can name the diverging side.
var floatRefBits = func() [][2]uint64 {
	refs := make([][2]uint64, len(floatDocs))
	for i, doc := range floatDocs {
		var d floatDoc
		if err := stdjson.Unmarshal([]byte(doc), &d); err != nil {
			panic(err)
		}
		refs[i] = floatDocBits(&d)
	}
	return refs
}()

// floatResult carries the decoded documents with the slice base Run
// observed, so Verify can check the operand pointer it compares through.
type floatResult struct {
	docs []floatDoc
	base uintptr
}

// floatDocBits snapshots d through integer loads, keeping the bits clear
// of the vector registers the float comparison uses.
//
//go:noinline
func floatDocBits(d *floatDoc) [2]uint64 {
	return [2]uint64{
		atomic.LoadUint64((*uint64)(unsafe.Pointer(&d.F))),
		atomic.LoadUint64((*uint64)(unsafe.Pointer(&d.G))),
	}
}

// Field bits of a comparison mask.
const (
	fieldF uint8 = 1 << iota
	fieldG
)

func maskString(m uint8) string {
	switch m {
	case 0:
		return "-"
	case fieldF:
		return "F"
	case fieldG:
		return "G"
	}
	return "FG"
}

// floatDocNEMask is the compiled float comparison Verify judges by,
// reporting the unequal fields. Out of line, it loads both operands from
// memory on its own.
//
//go:noinline
func floatDocNEMask(a, b *floatDoc) uint8 {
	var m uint8
	if a.F != b.F {
		m |= fieldF
	}
	if a.G != b.G {
		m |= fieldG
	}
	return m
}

// RFLAGS bits UCOMISD sets.
const (
	flagPF = 1 << 2
	flagZF = 1 << 6
)

// flagsNE decodes one UCOMISD RFLAGS image: ZF clear or PF (unordered)
// set means the operands differ.
func flagsNE(fl uint64) bool { return fl&flagZF == 0 || fl&flagPF != 0 }

// floatObs is one comparison of a decoded document against its
// reference: integer bit snapshots, the compiled comparison mask and,
// where the probe exists, the UCOMISD flags of each field.
type floatObs struct {
	gs, ws [2]uint64
	mask   uint8
	flags  [2]uint64
}

func observeFloatDoc(gp, wp *floatDoc) floatObs {
	o := floatObs{gs: floatDocBits(gp), ws: floatDocBits(wp), mask: floatDocNEMask(gp, wp)}
	if haveFPProbe {
		o.flags[0], o.flags[1] = ucomisdFlags((*[2]float64)(unsafe.Pointer(gp)), (*[2]float64)(unsafe.Pointer(wp)))
	}
	return o
}

// flagMask is the unequal-field mask the UCOMISD flags imply.
func (o floatObs) flagMask() uint8 {
	if !haveFPProbe {
		return 0
	}
	var m uint8
	if flagsNE(o.flags[0]) {
		m |= fieldF
	}
	if flagsNE(o.flags[1]) {
		m |= fieldG
	}
	return m
}

func (o floatObs) bad() bool { return o.mask != 0 || o.flagMask() != 0 || o.gs != o.ws }

// floatRepeats is how many comparisons floatMismatch reruns to tell a
// one-shot fault from a persistent one.
const floatRepeats = 64

// floatMismatch reports the bad observation o. It reads MXCSR, reruns
// the comparison floatRepeats times, re-reads both operands and
// classifies the failure:
//   - pointer: an operand address differs from the one recorded at
//     allocation, so a stack slot holding it was overwritten;
//   - float compare: the snapshots agree bit for bit while a comparison
//     reports unequal fields, so the comparison itself misbehaved;
//   - transient: the snapshots differ and the re-read agrees, so memory
//     changed underneath Verify;
//   - contents: the operands still differ; ref names the wrong side.
func floatMismatch(i int, base, wantAddr uintptr, gp, wp *floatDoc, o floatObs) error {
	mxcsr := stmxcsr()
	maskHits, flagHits := 0, 0
	for range floatRepeats {
		r := observeFloatDoc(gp, wp)
		if r.mask != 0 {
			maskHits++
		}
		if r.flagMask() != 0 {
			flagHits++
		}
	}
	gotAddr := uintptr(unsafe.Pointer(gp))
	wantBase := base + uintptr(i)*unsafe.Sizeof(floatDoc{})
	gr, wr := floatDocBits(gp), floatDocBits(wp)
	var kind string
	switch {
	case gotAddr != wantBase || uintptr(unsafe.Pointer(wp)) != wantAddr:
		kind = "pointer"
	case o.gs == o.ws:
		kind = "float compare"
	case gr == wr:
		kind = "transient"
	default:
		kind = "contents"
	}
	probe := ""
	if haveFPProbe {
		probe = fmt.Sprintf(" ucomisd=%s flags=[%#x %#x] repeat ucomisd=%d/%d mxcsr=%#x",
			maskString(o.flagMask()), o.flags[0], o.flags[1], flagHits, floatRepeats, mxcsr)
	}
	return fmt.Errorf("doc %d: %s mismatch: compiled=%s repeat compiled=%d/%d%s snap got=%016x want=%016x reread got=%016x want=%016x ref=%016x got@%#x expect@%#x want@%#x expect@%#x (reread got %+v want %+v)",
		i, kind, maskString(o.mask), maskHits, floatRepeats, probe, o.gs, o.ws, gr, wr, floatRefBits[i],
		gotAddr, wantBase, uintptr(unsafe.Pointer(wp)), wantAddr, *gp, *wp)
}

func floatPrecisionCase() Case {
	return Case{
		Name: "float-precision-bind",
		Run: func() any {
			out := make([]floatDoc, len(floatDocs))
			for i, doc := range floatDocs {
				if err := vjson.Unmarshal([]byte(doc), &out[i]); err != nil {
					return err
				}
			}
			return floatResult{docs: out, base: uintptr(unsafe.Pointer(&out[0]))}
		},
		Verify: func(res any) error {
			r := res.(floatResult)
			for i, doc := range floatDocs {
				var want floatDoc
				wantAddr := uintptr(unsafe.Pointer(&want))
				if err := stdjson.Unmarshal([]byte(doc), &want); err != nil {
					return fmt.Errorf("std decode doc %d: %w", i, err)
				}
				gp := &r.docs[i]
				if o := observeFloatDoc(gp, &want); o.bad() {
					return floatMismatch(i, r.base, wantAddr, gp, &want, o)
				}
			}
			return nil
		},
	}
}

type indentInner struct {
	S string `json:"s"`
}

type indentEdge struct {
	A     string      `json:"a"`
	B     string      `json:"b"`
	C     string      `json:"c"`
	N     int         `json:"n"`
	F     float64     `json:"f"`
	Inner indentInner `json:"inner"`
}

var indentPayload = indentEdge{
	A:     "plain",
	B:     "quote\"newline\n",
	C:     "uni\u00e9\U0001F600\tback\\slash",
	N:     -42,
	F:     0.3000000000000000444089209850062616169452667236328125,
	Inner: indentInner{S: "ctrl\u0001x"},
}

func indentCase() Case {
	return Case{
		Name: "marshal-indent-struct-strings",
		Run: func() any {
			bs, err := vjson.MarshalIndent(indentPayload, "", "  ")
			if err != nil {
				return err
			}
			return bs
		},
		Verify: func(res any) error {
			got, ok := res.([]byte)
			if !ok {
				return fmt.Errorf("unexpected result type %T", res)
			}
			want, err := stdjson.MarshalIndent(indentPayload, "", "  ")
			if err != nil {
				return err
			}
			if string(got) == string(want) {
				return nil
			}
			return semanticMismatch(got, want)
		},
	}
}

// tapeWalkDoc packs escaped strings, an array and a high-precision
// number so re-serialization walks string, array and raw-number tape
// entries.
const tapeWalkDoc = `{"esc":"a\tb\"c\\d\u0001e\u00e9f\ud83d\ude00g","arr":["x\n","y\""],"n":1.5e308}`

func tapeWalkCase() Case {
	return Case{
		Name: "tape-walk-remarshal",
		Run: func() any {
			v, err := vjson.Parse([]byte(tapeWalkDoc))
			if err != nil {
				return err
			}
			bs, err := vjson.MarshalIndent(v, "", "  ")
			if err != nil {
				return err
			}
			return bs
		},
		Verify: func(res any) error {
			got, ok := res.([]byte)
			if !ok {
				return fmt.Errorf("unexpected result type %T", res)
			}
			var want any
			if err := stdjson.Unmarshal([]byte(tapeWalkDoc), &want); err != nil {
				return err
			}
			wantBytes, err := stdjson.MarshalIndent(want, "", "  ")
			if err != nil {
				return err
			}
			return semanticMismatch(got, wantBytes)
		},
	}
}

// fmtDoc mixes nesting, escapes and boundary number literals; the fmt
// path copies number literals verbatim, so no float64 conversion occurs.
const fmtDoc = `{
  "f": [1e-999, 1.7976931348623157e308, -0.0],
  "s": "a\tb\"c\\d",
  "nested": {"arr": [{"k": "v\n"}, [], {}], "deep": {"x": [[["end"]]]}},
  "tail": true
}`

func fmtCase() Case {
	return Case{
		Name: "compact-indent",
		Run: func() any {
			var compact bytes.Buffer
			if err := vjson.Compact(&compact, []byte(fmtDoc)); err != nil {
				return err
			}
			var indent bytes.Buffer
			if err := vjson.Indent(&indent, []byte(fmtDoc), "", "  "); err != nil {
				return err
			}
			return [2]string{compact.String(), indent.String()}
		},
		Verify: func(res any) error {
			got := res.([2]string)
			var wc, wi bytes.Buffer
			if err := stdjson.Compact(&wc, []byte(fmtDoc)); err != nil {
				return err
			}
			if err := stdjson.Indent(&wi, []byte(fmtDoc), "", "  "); err != nil {
				return err
			}
			if got[0] != wc.String() {
				return fmt.Errorf("compact mismatch:\n got: %s\nwant: %s", got[0], wc.String())
			}
			if got[1] != wi.String() {
				return fmt.Errorf("indent mismatch:\n got: %s\nwant: %s", got[1], wi.String())
			}
			return nil
		},
	}
}

type largeDoc struct {
	A string `json:"a"`
	B string `json:"b"`
	C int    `json:"c"`
}

var largePayload = largeDoc{
	A: patternString(8 << 10),
	B: patternString(16 << 10),
	C: 7,
}

var largeEncoded = func() []byte {
	b, err := stdjson.Marshal(largePayload)
	if err != nil {
		panic(err)
	}
	return b
}()

func largeStringsMarshalCase() Case {
	return Case{
		Name: "large-strings-marshal",
		Run: func() any {
			bs, err := vjson.Marshal(largePayload)
			if err != nil {
				return err
			}
			return bs
		},
		Verify: func(res any) error {
			got, ok := res.([]byte)
			if !ok {
				return fmt.Errorf("unexpected result type %T", res)
			}
			want, err := stdjson.Marshal(largePayload)
			if err != nil {
				return err
			}
			if string(got) == string(want) {
				return nil
			}
			return semanticMismatch(got, want)
		},
	}
}

func largeStringsUnmarshalCase() Case {
	return Case{
		Name: "large-strings-unmarshal",
		Run: func() any {
			var v largeDoc
			if err := vjson.Unmarshal(largeEncoded, &v); err != nil {
				return err
			}
			return v
		},
		Verify: func(res any) error {
			got := res.(largeDoc)
			if got != largePayload {
				return fmt.Errorf("got %+v want %+v", got, largePayload)
			}
			return nil
		},
	}
}

type ifaceDoc struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
	Extra any    `json:"extra"`
}

var ifacePayload = ifaceDoc{
	Name:  "mixed",
	Value: map[string]any{"k": []any{1.5, true, nil, "s\""}},
	Extra: []any{map[string]any{"n": float64(2)}, "tail\n"},
}

func ifaceReentryCase() Case {
	return Case{
		Name: "interface-reentry",
		Run: func() any {
			bs, err := vjson.Marshal(ifacePayload)
			if err != nil {
				return err
			}
			return bs
		},
		Verify: func(res any) error {
			got, ok := res.([]byte)
			if !ok {
				return fmt.Errorf("unexpected result type %T", res)
			}
			want, err := stdjson.Marshal(ifacePayload)
			if err != nil {
				return err
			}
			if string(got) == string(want) {
				return nil
			}
			return semanticMismatch(got, want)
		},
	}
}

func TestStackStress_FloatPrecisionBind(t *testing.T) {
	Sweep(t, floatPrecisionCase())
}

func TestStackStress_MarshalIndentStructStrings(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	Sweep(t, indentCase())
}

func TestStackStress_TapeWalkValueRemarshal(t *testing.T) {
	if !encvm.Available {
		t.Skip("native parser or encoder not available on this platform")
	}
	Sweep(t, tapeWalkCase())
}

func TestStackStress_CompactIndent(t *testing.T) {
	Sweep(t, fmtCase())
}

func TestStackStress_LargeStringsMarshal(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	Sweep(t, largeStringsMarshalCase())
}

func TestStackStress_LargeStringsUnmarshal(t *testing.T) {
	Sweep(t, largeStringsUnmarshalCase())
}

func TestStackStress_InterfaceReentry(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	Sweep(t, ifaceReentryCase())
}

func TestStackStress_ConcurrentSweepGC(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	cases := []Case{
		floatPrecisionCase(),
		indentCase(),
		ifaceReentryCase(),
		largeStringsUnmarshalCase(),
	}
	dur := 5 * time.Second
	if s := os.Getenv("VJSTACKSTRESS_SECS"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("bad VJSTACKSTRESS_SECS %q", s)
		}
		dur = time.Duration(v) * time.Second
	}
	SweepDuration(t, cases, 20, dur)
	runtime.GC()
}
