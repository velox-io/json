package venc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/velox-io/json/vopt"

	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/value"
)

// The native VM writes only inside the window it is handed: bytes past
// cap(es.buf) belong to whatever the allocator placed next. AppendMarshal
// into backing[:k:n] makes the first window end exactly at n with a canary
// behind it, so sweeping n puts every op, at every write offset, against the
// window edge. A wide store that overhangs the reservation corrupts the
// canary here and a neighboring heap object in production.

const windowCanaryLen = 64

const windowCanaryByte = 0xA5

type windowOpt struct {
	name string
	opts []MarshalOption
}

var windowOpts = []windowOpt{
	{"default", nil},
	{"std", []MarshalOption{vopt.EscapeHTML(true), vopt.EscapeLineTerms(true), vopt.AllowInvalidUTF8(false), vopt.FloatExpAuto(true)}},
	{"fast", nil},
	{"nohtml", []MarshalOption{vopt.EscapeHTML(true), vopt.EscapeLineTerms(true), vopt.AllowInvalidUTF8(false), vopt.FloatExpAuto(true), vopt.EscapeHTML(false)}},
	{"indent", []MarshalOption{vopt.EscapeHTML(true), vopt.EscapeLineTerms(true), vopt.AllowInvalidUTF8(false), vopt.FloatExpAuto(true), vopt.Indent("", "  ")}},
	{"indent-prefix", []MarshalOption{vopt.Indent(">>", "\t")}},
}

// windowSweep encodes v into every capacity from 0 to past the full output,
// at two start offsets, and fails on a canary hit or an output mismatch.
func windowSweep[T any](t *testing.T, v *T, o windowOpt) {
	t.Helper()
	want, err := Marshal(v, o.opts...)
	if err != nil {
		t.Fatalf("%s: Marshal: %v", o.name, err)
	}
	for _, prefix := range []int{0, 5} {
		for n := prefix; n <= prefix+len(want)+1; n++ {
			backing := make([]byte, n+windowCanaryLen)
			for i := range backing {
				backing[i] = windowCanaryByte
			}
			got, err := AppendMarshal(backing[:prefix:n], v, o.opts...)
			if err != nil {
				t.Fatalf("%s: AppendMarshal(prefix=%d cap=%d): %v", o.name, prefix, n, err)
			}
			for i := n; i < len(backing); i++ {
				if backing[i] != windowCanaryByte {
					t.Fatalf("%s: prefix=%d cap=%d (output %d bytes): VM wrote %d+ bytes past the window end\ncanary: %q\nwant:   %s",
						o.name, prefix, n, len(want), i-n+1, backing[n:], want)
				}
			}
			if !bytes.Equal(got[prefix:], want) {
				t.Fatalf("%s: prefix=%d cap=%d: output mismatch\n got: %s\nwant: %s", o.name, prefix, n, got[prefix:], want)
			}
		}
	}
}

// windowStrings covers each escape class at every length residue mod 16 and
// 32, so short-string, SIMD-block, and tail paths all meet the edge. The
// reduced set keeps the residues that overhang furthest, for builds where
// every VM entry is expensive (gcstress) and for -short.
func windowStrings() []string {
	units := []string{"a", "\"", "<", "é", "\xff", "\u2028", "\x01", "中"}
	lens := make([]int, 0, 41)
	for l := 0; l <= 40; l++ {
		lens = append(lens, l)
	}
	if gcStressBuild || testing.Short() {
		units = units[:4]
		lens = []int{0, 1, 12, 15, 16, 17, 31}
	}
	var out []string
	for _, u := range units {
		for _, l := range lens {
			out = append(out, strings.Repeat("a", l)+u)
		}
	}
	for _, l := range lens {
		out = append(out, strings.Repeat("a", l))
	}
	return out
}

// windowSweepOpts is the option matrix of a sweep; reduced builds keep one
// compact and one indent mode.
func windowSweepOpts() []windowOpt {
	if gcStressBuild || testing.Short() {
		return []windowOpt{windowOpts[1], windowOpts[5]}
	}
	return windowOpts
}

func TestVMWindowOverrunString(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	type keyed struct {
		S string `json:"s"`
	}
	type unkeyed struct {
		S string
		T string `json:"-"`
	}
	for _, o := range windowSweepOpts() {
		for _, s := range windowStrings() {
			windowSweep(t, &keyed{S: s}, o)
			windowSweep(t, &unkeyed{S: s}, o)
			ss := []string{s, s}
			windowSweep(t, &ss, o)
			var a any = s
			windowSweep(t, &a, o)
		}
	}
}

type windowInner struct {
	A int    `json:"a"`
	B string `json:"b"`
}

type windowAll struct {
	S     string                 `json:"s"`
	PS    *string                `json:"ps"`
	I     int                    `json:"i"`
	I8    int8                   `json:"i8"`
	I64   int64                  `json:"i64"`
	U     uint                   `json:"u"`
	U64   uint64                 `json:"u64"`
	F32   float32                `json:"f32"`
	F64   float64                `json:"f64"`
	FExp  float64                `json:"fexp"`
	Bo    bool                   `json:"bo"`
	QI    int                    `json:"qi,string"`
	QI64  int64                  `json:"qi64,string"`
	T     time.Time              `json:"t"`
	By    []byte                 `json:"by"`
	Raw   json.RawMessage        `json:"raw"`
	Num   json.Number            `json:"num"`
	MSS   map[string]string      `json:"mss"`
	MSI   map[string]int         `json:"msi"`
	MSI64 map[string]int64       `json:"msi64"`
	MSV   map[string]windowInner `json:"msv"`
	MIS   map[int]string         `json:"mis"`
	SF    []float64              `json:"sf"`
	SI    []int                  `json:"si"`
	SI64  []int64                `json:"si64"`
	SS    []string               `json:"ss"`
	Arr   [2]string              `json:"arr"`
	P     *windowInner           `json:"p"`
	NilP  *windowInner           `json:"nilp"`
	Any   any                    `json:"any"`
	AnyM  any                    `json:"anym"`
	AnyS  any                    `json:"anys"`
	AnyT  any                    `json:"anyt"`
	In    windowInner            `json:"in"`
	Sl    []windowInner          `json:"sl"`
	Val   value.Value            `json:"val"`
	Empty string                 `json:"empty,omitempty"`
}

func newWindowAll(t *testing.T, s string) *windowAll {
	return &windowAll{
		S: s, PS: &s,
		I: -1234567, I8: -12, I64: -9223372036854775808, U: 18446744073709551615, U64: 42,
		F32: 3.4028235e38, F64: 123.456, FExp: 1.5e-7, Bo: true,
		QI: -77, QI64: 1 << 62,
		T:     time.Date(2024, 2, 29, 23, 59, 59, 123456789, time.FixedZone("x", 5*3600+30*60)),
		By:    []byte(s + "\x00\xff"),
		Raw:   json.RawMessage(`{"r":[1,2]}`),
		Num:   json.Number("-1.25e+10"),
		MSS:   map[string]string{s: s},
		MSI:   map[string]int{s: -5},
		MSI64: map[string]int64{"k": 1 << 40},
		MSV:   map[string]windowInner{s: {A: 1, B: s}},
		MIS:   map[int]string{-3: s},
		SF:    []float64{1e21, -0.5, 1e-7},
		SI:    []int{0, -1, 1 << 50},
		SI64:  []int64{-1 << 63},
		SS:    []string{s, "", s},
		Arr:   [2]string{s, s},
		P:     &windowInner{A: 2, B: s},
		Any:   s,
		AnyM:  map[string]any{"m": s},
		AnyS:  []any{s, 1.5, true, nil},
		AnyT:  windowInner{A: 3, B: s},
		In:    windowInner{A: 4, B: s},
		Sl:    []windowInner{{A: 5, B: s}, {A: 6}},
		Val:   parseValue(t, `{"v":["`+strings.Repeat("x", len(s))+`",-1.5,{"w":null}]}`),
	}
}

func TestVMWindowOverrunAllOps(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}
	strs := windowStrings()
	if gcStressBuild || testing.Short() {
		strs = []string{"", "a", strings.Repeat("a", 13), strings.Repeat("a", 17) + "\"", "é\xff"}
	}
	for _, o := range windowSweepOpts() {
		for _, s := range strs {
			windowSweep(t, newWindowAll(t, s), o)
		}
	}
}
