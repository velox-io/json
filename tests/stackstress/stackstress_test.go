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
	"testing"
	"time"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/native/encvm"
	"github.com/velox-io/json/native/ndec"
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
			return out
		},
		Verify: func(res any) error {
			got := res.([]floatDoc)
			for i, doc := range floatDocs {
				var want floatDoc
				if err := stdjson.Unmarshal([]byte(doc), &want); err != nil {
					return fmt.Errorf("std decode doc %d: %w", i, err)
				}
				if got[i] != want {
					return fmt.Errorf("doc %d: got %+v want %+v", i, got[i], want)
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
	if !ndec.Available {
		t.Skip("native decoder not available on this platform")
	}
	Sweep(t, floatPrecisionCase())
}

func TestStackStress_MarshalIndentStructStrings(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	Sweep(t, indentCase())
}

func TestStackStress_TapeWalkValueRemarshal(t *testing.T) {
	if !ndec.Available || !encvm.Available {
		t.Skip("native parser or encoder not available on this platform")
	}
	Sweep(t, tapeWalkCase())
}

func TestStackStress_CompactIndent(t *testing.T) {
	if !ndec.Available {
		t.Skip("native decoder not available on this platform")
	}
	Sweep(t, fmtCase())
}

func TestStackStress_LargeStringsMarshal(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	Sweep(t, largeStringsMarshalCase())
}

func TestStackStress_LargeStringsUnmarshal(t *testing.T) {
	if !ndec.Available {
		t.Skip("native decoder not available on this platform")
	}
	Sweep(t, largeStringsUnmarshalCase())
}

func TestStackStress_InterfaceReentry(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available on this platform")
	}
	Sweep(t, ifaceReentryCase())
}

func TestStackStress_ConcurrentSweepGC(t *testing.T) {
	if !ndec.Available || !encvm.Available {
		t.Skip("native decoder or encoder not available on this platform")
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
