package ndec

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// The Go bodies must reproduce the native entries byte for byte: same
// verdicts, same tape words, same arena bytes. These tests run both on the
// same inputs, so they need the blob.

func needNative(t testing.TB) {
	t.Helper()
	if !Available {
		t.Skip("native ndec blob not mapped")
	}
}

type domOut struct {
	err     int32
	tape    []uint64
	str     []byte
	need    uint32
	nStruct uint32
}

func runDOM(src []byte, zc, strict, useGo bool) domOut {
	n := len(src)
	padded := make([]byte, n+ScanPadding)
	copy(padded, src)
	for i := n; i < len(padded); i++ {
		padded[i] = 0x20
	}
	structural := make([]uint32, n+24)
	str := make([]byte, n+64)
	tape := make([]uint64, 4) // force the TapeFull retry path on most inputs
	c := DOMContext{
		Src: unsafe.SliceData(padded), SrcLen: uintptr(n),
		StrArena: unsafe.SliceData(str), StrArenaCap: uintptr(len(str)),
		Structural: unsafe.SliceData(structural), StructuralCap: uint32(len(structural)),
		DOMState:  unsafe.Pointer(unsafe.SliceData(make([]byte, DOMStateSize))),
		AtofState: unsafe.Pointer(unsafe.SliceData(make([]byte, AtofStateSize))),
	}
	if zc {
		c.StrMode = 1
	}
	if strict {
		c.ScanStrict = 1
	}
	run := func(f func(*DOMContext), g func(unsafe.Pointer)) {
		if useGo {
			f(&c)
		} else {
			g(unsafe.Pointer(&c))
		}
	}
	c.Tape, c.TapeCap = unsafe.SliceData(tape), uintptr(len(tape))
	run(goDOMParseCounted, vjNdecDOMParseCounted)
	if c.Err == DomTapeFull {
		tape = make([]uint64, c.TapeNeed)
		c.Tape, c.TapeCap = unsafe.SliceData(tape), uintptr(len(tape))
		run(goDOMBuild, vjNdecDOMBuild)
	}
	o := domOut{err: c.Err, need: c.TapeNeed, nStruct: c.NStructural}
	if c.Err == 0 {
		o.tape = tape[:c.TapeLen]
		o.str = str[:c.StrUsed]
	}
	return o
}

func runValid(src []byte, useGo bool) bool {
	n := len(src)
	padded := make([]byte, n+ScanPadding)
	copy(padded, src)
	for i := n; i < len(padded); i++ {
		padded[i] = 0x20
	}
	structural := make([]uint32, n+24)
	c := ValidContext{Src: unsafe.SliceData(padded), SrcLen: uintptr(n),
		Structural: unsafe.SliceData(structural), StructuralCap: uint32(len(structural))}
	if useGo {
		goValid(&c)
	} else {
		vjNdecValid(unsafe.Pointer(&c))
	}
	return c.Err == 0
}

func compareDOM(t *testing.T, src []byte) {
	t.Helper()
	for _, zc := range []bool{false, true} {
		for _, strict := range []bool{false, true} {
			nat := runDOM(src, zc, strict, false)
			gov := runDOM(src, zc, strict, true)
			tag := fmt.Sprintf("zc=%v strict=%v src=%.80q", zc, strict, src)
			if (nat.err == 0) != (gov.err == 0) {
				t.Errorf("%s: verdict native=%d go=%d", tag, nat.err, gov.err)
				continue
			}
			if nat.err != 0 {
				continue
			}
			if nat.need != gov.need || nat.nStruct != gov.nStruct {
				t.Errorf("%s: need/nstruct native=%d/%d go=%d/%d", tag, nat.need, nat.nStruct, gov.need, gov.nStruct)
			}
			if len(nat.tape) != len(gov.tape) {
				t.Errorf("%s: tape len native=%d go=%d", tag, len(nat.tape), len(gov.tape))
				continue
			}
			for i := range nat.tape {
				if nat.tape[i] != gov.tape[i] {
					t.Errorf("%s: tape[%d] native=%#x go=%#x", tag, i, nat.tape[i], gov.tape[i])
					break
				}
			}
			if !bytes.Equal(nat.str, gov.str) {
				t.Errorf("%s: str arena differs\nnative=%q\n    go=%q", tag, nat.str, gov.str)
			}
		}
	}
	if nv, gv := runValid(src, false), runValid(src, true); nv != gv {
		t.Errorf("valid %.80q: native=%v go=%v", src, nv, gv)
	}
}

var diffCases = []string{
	`1`, `-0`, `0`, `01`, `1.`, `1.5`, `-1.5e300`, `1e`, `1e+`, `1E400`, `-1E400`, `0e1`, `0.1e-400`,
	`123456789012345678901234`, `18446744073709551615`, `18446744073709551616`, `9223372036854775807`,
	`9223372036854775808`, `-9223372036854775808`, `-9223372036854775809`, `29999999999999999999`,
	`0.00000000000000000000001`, `1.2345678901234567890123`, `1e0000000000000000000001`, `1e-0000000000000000000000001`,
	`12345678901234567890.5`, `1x`, `1-`, `-`, `-a`, `2.e3`, `.5`, `+1`,
	`true`, `false`, `null`, `tru`, `nul`, `truex`, `true1`, `[true,]`, `[tru]`, `nulll`,
	`""`, `"a"`, `"a\nb\tc\\d\"e\f\/g\b\r"`, `"\u00e9"`, `"\ud83d\ude00"`, `"\ud800"`, `"\udc00"`,
	`"\ud800\u0041"`, `"\ud800x"`, `"\u12"`, `"\x"`, "\"a\x01\"", "\"\xff\xfe\"", "\"\xc3\xa9\"",
	"\"ab\x7f\"", `"a\\"`, `"\\\""`, `"unclosed`, `"\"`,
	`{}`, `[]`, `[1]`, `[1,2,3]`, `{"a":1}`, `{"a":1,"b":[true,false,null],"c":{"d":"x"}}`, `[{},[],{},[]]`,
	`[1],2`, `{"a":1},"b":2`, `[1]]`, `{"a":1}}`, `1 2`, `[1:2]`, `{"a":1:2}`, `{"a"::1}`, `[,1]`, `[1,]`,
	`{,}`, `{"a":1,}`, `{"a" 1}`, `{"a"}`, `{1:2}`, `{`, `[`, `]`, `}`, `,`, `:`, ` `, "\t[1]\n", `[1 2]`,
	`[1,,2]`, `{"a":}`, `[}`, `{]`, `[[[]]]`, `[[[[[1]]]]]`, `{"k":"a string long enough to span a chunk boundary in the scanner ok"}`,
	`["aa","bb","cc","dd"]`, `[1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1]`, `{"a":{"b":{"c":[1,2,3,{"d":null}]}}}`,
	`["\\\\\\\"", "x"]`, `["a\\", "b"]`, `[ "a" , 1 , true ]`, "[\"a\"\x00]", "[1\x00]", `[1]x`, `x`, `"a"b`,
	`[-]`, `[-0.0e-0]`, `[1e1.5]`, `{"a":1 "b":2}`, `{"a":1,"a":2}`,
}

func TestGoDOMMatchesNative(t *testing.T) {
	needNative(t)
	for _, s := range diffCases {
		compareDOM(t, []byte(s))
	}
	deep := func(d int) string { return strings.Repeat("[", d) + "1" + strings.Repeat("]", d) }
	for _, d := range []int{255, 256, 257} {
		compareDOM(t, []byte(deep(d)))
		compareDOM(t, []byte(strings.Repeat(`{"a":`, d)+"1"+strings.Repeat("}", d)))
		compareDOM(t, []byte(strings.Repeat("[", d-1)+"[]"+strings.Repeat("]", d-1)))
	}
	long := strings.Repeat("x", 1<<12)
	compareDOM(t, []byte(`["`+long+`","`+strings.Repeat(`\n`, 2000)+`"]`))
}

// repoPath joins elems under the repository root, anchored to this source
// file so it resolves from any working directory the test binary runs in.
func repoPath(elems ...string) string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("ndec_go_test.go: cannot locate source file")
	}
	return filepath.Join(append([]string{filepath.Dir(thisFile), "..", ".."}, elems...)...)
}

func TestGoDOMMatchesNativeCorpus(t *testing.T) {
	needNative(t)
	dir := repoPath("benchmark", "corpus", "testdata")
	files, _ := filepath.Glob(filepath.Join(dir, "*.json.gz"))
	if len(files) == 0 {
		t.Skip("corpus unavailable")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		src, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		compareDOM(t, src)
	}
	suite, _ := filepath.Glob(repoPath("tests", "JSONTestSuite", "*.json"))
	for _, f := range suite {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		compareDOM(t, src)
	}
}

// TestGoDOMMatchesNativeFuzz mutates small documents byte by byte so the
// grammar edges get exercised far beyond the hand-written cases.
func TestGoDOMMatchesNativeFuzz(t *testing.T) {
	needNative(t)
	alphabet := []byte(`{}[],:"\ 0123456789-+.eEtrufalsn` + "\t\n\x01\x80\xff" + `u\ud800`)
	r := rand.New(rand.NewPCG(1, 2))
	seeds := diffCases
	for range 20000 {
		b := []byte(seeds[r.IntN(len(seeds))])
		for range 1 + r.IntN(3) {
			switch op := r.IntN(3); {
			case op == 0 || len(b) == 0:
				i := r.IntN(len(b) + 1)
				b = append(b[:i], append([]byte{alphabet[r.IntN(len(alphabet))]}, b[i:]...)...)
			case op == 1:
				i := r.IntN(len(b))
				b = append(b[:i], b[i+1:]...)
			default:
				b[r.IntN(len(b))] = alphabet[r.IntN(len(alphabet))]
			}
		}
		compareDOM(t, b)
		if t.Failed() {
			return
		}
	}
}
