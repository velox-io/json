package bind

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/value"
)

// Native advances the tape and string arenas with unchecked bumps, so the
// capacities Go reserves are the only thing between a dense document and a
// write outside the arena. The reserve is a pure accounting argument: every
// arena byte must be chargeable to a distinct span of the source. These cases
// maximize words and bytes per source byte and then assert the reserve held.
//
// The two independent tape bounds are the source-size ceiling (srcLen+3, or
// 2*srcLen+3 with a split tape) and the token bound ndec_scan_tape_words
// derives from the scan populations; native keeps the smaller. The string
// arena reserve is srcLen+64. Tape binding from a value.Value instead uses
// the source tape length, at 3W/2 words.

type boundValueHost struct {
	Rest value.Value `json:",embed"`
}

// boundStringHost forces committed arena strings through bind_intern_str.
// A reserve-unknown host alone never interned a string, which left the string
// budget untested.
type boundStringHost struct {
	A string            `json:"a"`
	B []string          `json:"b"`
	M map[string]string `json:"m"`
	C []boundInner      `json:"c"`
}

type boundInner struct {
	K string `json:"k"`
	V string `json:"v"`
}

func machineOf(p *Parser) *ndec.BindMachine {
	return (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
}

// adversarialDocs returns documents that saturate the per-byte accounting.
// Bracket ladders and flat scalar arrays both reach one tape word per source
// byte, which is the tightest ratio any document can produce.
func adversarialDocs() []struct {
	name string
	doc  string
} {
	var out []struct {
		name string
		doc  string
	}
	add := func(name, doc string) {
		out = append(out, struct {
			name string
			doc  string
		}{name, doc})
	}

	// Depth ladders, stopping just under BIND_MAX_DEPTH.
	for _, n := range []int{1, 2, 63, 127, 200, 253, 254} {
		add(fmt.Sprintf("ladder%d", n), strings.Repeat("[", n)+"1"+strings.Repeat("]", n))
		add(fmt.Sprintf("objladder%d", n/2), strings.Repeat(`{"a":`, n/2)+"1"+strings.Repeat("}", n/2))
	}
	// Flat scalar arrays: two source bytes per scalar, two tape words.
	for _, n := range []int{1, 2, 3, 1000, 20000} {
		add(fmt.Sprintf("scalars%d", n), "["+strings.Repeat("1,", n-1)+"1]")
		add(fmt.Sprintf("pairs%d", n), "["+strings.Repeat("[1],", n-1)+"[1]]")
	}
	// Empty and alternating containers, which spend ops without spending bytes.
	for _, n := range []int{1, 1000} {
		add(fmt.Sprintf("emptyarr%d", n), "["+strings.Repeat("[],", n-1)+"[]]")
		add(fmt.Sprintf("emptyobj%d", n), "["+strings.Repeat("{},", n-1)+"{}]")
		add(fmt.Sprintf("alt%d", n), "["+strings.Repeat(`[{},1],`, n-1)+"[{},1]]")
	}
	// Key storms. An entry costs a tape word per key, a seam, and a value.
	for _, n := range []int{1, 1000, 5000} {
		add(fmt.Sprintf("dupkeys%d", n), "{"+strings.Repeat(`"k":1,`, n-1)+`"k":1}`)
		add(fmt.Sprintf("uniqkeys%d", n), "{"+uniqJSONKeys(n)+"}")
		add(fmt.Sprintf("arrval%d", n), "{"+strings.Repeat(`"k":[1],`, n-1)+`"k":[1]}`)
		add(fmt.Sprintf("strval%d", n), "{"+strings.Repeat(`"k":"",`, n-1)+`"k":""}`)
	}
	// Strings and numbers, which charge the string arena len+1 per token.
	add("longstr", `"`+strings.Repeat("x", 100000)+`"`)
	add("emptystrs", "["+strings.Repeat(`"",`, 2000)+`""]`)
	add("escapes", `["`+strings.Repeat(`\n`, 5000)+`"]`)
	add("wideescapes", `["`+strings.Repeat(`é`, 2000)+`"]`)
	add("surrogates", `["`+strings.Repeat(`😀`, 2000)+`"]`)
	add("longnum", "["+strings.Repeat("123456789012345678901234567890,", 500)+"1]")
	add("mixed", "["+strings.Repeat(`1,"s",[[]],{"k":{}},true,null,`, 500)+"1]")
	return out
}

func uniqJSONKeys(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"k%d":1`, i)
	}
	return b.String()
}

// checkArenaBounds fails when native wrote past a reserve. strBudget is the
// string reserve for this document; tapeBound is the tape reserve in words.
func checkArenaBounds(t *testing.T, tag, name string, src []byte, p *Parser, tapeBound int) {
	t.Helper()
	m := machineOf(p)
	tapeUsed, tapeNeed := int(m.Alloc.TapeUsed), int(m.Alloc.TapeNeed)
	tapeCap, strUsed, strCap := int(m.Alloc.TapeArenaCap), int(m.Core.StrUsed), int(m.Alloc.StrArenaCap)

	if tapeUsed > tapeCap {
		t.Errorf("%s/%s: tape used %d exceeds arena cap %d (srcLen %d)", tag, name, tapeUsed, tapeCap, len(src))
	}
	if strUsed > strCap {
		t.Errorf("%s/%s: strings used %d exceeds arena cap %d (srcLen %d)", tag, name, strUsed, strCap, len(src))
	}
	if tapeUsed > tapeBound {
		t.Errorf("%s/%s: tape used %d exceeds bound %d (need %d, srcLen %d)", tag, name, tapeUsed, tapeBound, tapeNeed, len(src))
	}
	if strUsed > len(src)+64 {
		t.Errorf("%s/%s: strings used %d exceeds srcLen+64 budget (srcLen %d)", tag, name, strUsed, len(src))
	}
}

func TestArenaBoundsAdversarial(t *testing.T) {
	docs := adversarialDocs()

	// Reserve-unknown host: the whole document lands on the merged tape.
	vp, err := NewParser[boundValueHost]()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		src := []byte(d.doc)
		var host boundValueHost
		if err = vp.Unmarshal(src, &host); err != nil {
			continue
		}
		m := machineOf(vp)
		bound := int(m.Alloc.TapeNeed)
		if bound == 0 {
			bound = len(src) + 3
		}
		checkArenaBounds(t, "value", d.name, src, vp, bound)
	}

	// String host: exercises the string arena reserve.
	sp, err := NewParser[boundStringHost]()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		src := []byte(d.doc)
		var host boundStringHost
		if err = sp.Unmarshal(src, &host); err != nil {
			continue
		}
		checkArenaBounds(t, "string", d.name, src, sp, 1<<30)
	}

	// Root value.Value documents the tape the binder publishes.
	rp, err := NewParser[value.Value]()
	if err != nil {
		t.Fatal(err)
	}
	tbp, err := NewParser[boundValueHost]()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		src := []byte(d.doc)
		var v value.Value
		if err := rp.Unmarshal(src, &v); err != nil {
			continue
		}
		checkArenaBounds(t, "root", d.name, src, rp, len(src)+3)

		// Tape binding sizes from the source tape, not from srcLen.
		words := len(valueabi.Load(unsafe.Pointer(&v)).Doc.Tape)
		var host boundValueHost
		if err := tbp.UnmarshalValue(v, &host); err != nil {
			continue
		}
		checkArenaBounds(t, "tapebind", d.name, src, tbp, (3*words+1)/2)
	}
}
