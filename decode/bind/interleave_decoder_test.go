package bind

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

// NDJSON interleaving: a Decoder with a skip-all predicate must treat every
// line independently. Each line's outcome (value or error) must equal what a
// fresh Parser reports for that line alone, whatever the reader chunking or
// window size, and whatever precedes or follows it.

type ilLineKind int

const (
	ilLineGood ilLineKind = iota
	ilLineTrunc
	ilLineTypeBad
	ilLineSyntax
)

type ilLine struct {
	text string
	kind ilLineKind
}

// ilSingleValueLine reports whether line is exactly one JSON value in the
// sense of a Decoder: nothing but whitespace follows the first value, valid or
// not as far as types go. Lines that hold a valid value followed by garbage
// are excluded, since a Decoder rightly yields the value and then the garbage.
func ilSingleValueLine(line string) bool {
	dec := json.NewDecoder(strings.NewReader(line))
	var x any
	err := dec.Decode(&x)
	if err != nil {
		// The value itself is malformed: one error, no values. A line that
		// ends mid-value is excluded here: the Decoder reads on into the
		// following line, which the resync tests cover on their own.
		return !errors.Is(err, io.ErrUnexpectedEOF) && err != io.EOF
	}
	return strings.TrimSpace(line[dec.InputOffset():]) == ""
}

func ilBuildLines(r *rand.Rand, n int, good []string) []ilLine {
	var goodMut []string
	for _, g := range good {
		goodMut = append(goodMut, ilMutants(g)...)
	}
	var typeMut []string
	for _, g := range good {
		typeMut = append(typeMut, ilTypeMutants(g)...)
	}
	var lines []ilLine
	for len(lines) < n {
		switch r.IntN(5) {
		case 0, 1:
			lines = append(lines, ilLine{good[r.IntN(len(good))], ilLineGood})
		case 2:
			g := good[r.IntN(len(good))]
			cut := g[:1+r.IntN(len(g)-1)]
			if !ilSingleValueLine(cut) {
				continue
			}
			lines = append(lines, ilLine{cut, ilLineTrunc})
		case 3:
			lines = append(lines, ilLine{typeMut[r.IntN(len(typeMut))], ilLineTypeBad})
		case 4:
			m := goodMut[r.IntN(len(goodMut))]
			if m == "" || strings.ContainsAny(m, "\n") || !ilSingleValueLine(m) {
				continue
			}
			lines = append(lines, ilLine{m, ilLineSyntax})
		}
	}
	return lines
}

type ilDecRes struct {
	err string
	val string
}

func ilDecodeAll[T any](t *testing.T, data []byte, rd io.Reader, bufSize int, n int) []ilDecRes {
	if t != nil {
		t.Helper()
	}
	opts := []DecoderOption{WithSkipErrors(func(error) bool { return true })}
	if bufSize > 0 {
		opts = append(opts, WithBufferSize(bufSize))
	}
	d := NewDecoder(rd, opts...)
	var out []ilDecRes
	for i := 0; i < n+4; i++ {
		v := new(T)
		err := d.Decode(v)
		if err == io.EOF {
			break
		}
		r := ilDecRes{err: ilClass(ilDescribeErr(err))}
		if err == nil {
			js, _ := json.Marshal(v)
			r.val = string(js)
		}
		out = append(out, r)
	}
	return out
}

// ilModel decodes each line alone through a fresh Decoder over a whole
// buffer. The oracle is the streaming driver itself rather than a contiguous
// Parser: an element-site mismatch aborts before the rest of a line is read,
// and only the contiguous driver's structural prepass sees a syntax error
// past that point.
func ilModel[T any](lines []ilLine) []ilDecRes {
	var out []ilDecRes
	for _, l := range lines {
		one := []byte(l.text + "\n")
		got := ilDecodeAll[T](nil, one, bytes.NewReader(one), 0, 1)
		if len(got) == 0 {
			got = []ilDecRes{{err: "ok"}}
		}
		out = append(out, got[0])
	}
	return out
}

func ilClass(e string) string {
	switch {
	case e == "":
		return "ok"
	case strings.HasPrefix(e, "SE"):
		return "syntax"
	case strings.HasPrefix(e, "UTE"):
		return "type"
	}
	return e
}

func TestInterleaveDecoderNDJSON(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	type readerMode struct {
		name string
		wrap func([]byte) io.Reader
	}
	modes := []readerMode{
		{"whole", func(b []byte) io.Reader { return bytes.NewReader(b) }},
		{"one-byte", func(b []byte) io.Reader { return iotest.OneByteReader(bytes.NewReader(b)) }},
		{"chunk7", func(b []byte) io.Reader { return &chunkReader{data: b, chunk: 7} }},
		{"chunk64", func(b []byte) io.Reader { return &chunkReader{data: b, chunk: 64} }},
		{"half", func(b []byte) io.Reader { return iotest.HalfReader(bytes.NewReader(b)) }},
		{"dataerr", func(b []byte) io.Reader { return iotest.DataErrReader(bytes.NewReader(b)) }},
	}
	bufSizes := []int{0, 16, 64, 1 << 12}
	fails := 0
	for round := 0; round < 40 && fails < 8; round++ {
		lines := ilBuildLines(r, 12, ilPlainGood)
		trailingNL := true
		var sb strings.Builder
		for i, l := range lines {
			sb.WriteString(l.text)
			if i < len(lines)-1 || trailingNL {
				sb.WriteByte('\n')
			}
		}
		data := []byte(sb.String())
		want := ilModel[ilPlain](lines)
		for _, m := range modes {
			for _, bs := range bufSizes {
				got := ilDecodeAll[ilPlain](t, data, m.wrap(data), bs, len(lines))
				// The final line without a newline may report io.EOF from the
				// skip instead of its error; model that separately below.
				if !reflect.DeepEqual(got, want) {
					fails++
					// Shrink to the fewest lines that still diverge.
					diverges := func(ls []ilLine) bool {
						var sb strings.Builder
						for i, l := range ls {
							sb.WriteString(l.text)
							if i < len(ls)-1 || trailingNL {
								sb.WriteByte('\n')
							}
						}
						b := []byte(sb.String())
						return !reflect.DeepEqual(ilDecodeAll[ilPlain](t, b, m.wrap(b), bs, len(ls)), ilModel[ilPlain](ls))
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
					mw := ilModel[ilPlain](min)
					var sb strings.Builder
					for i, l := range min {
						sb.WriteString(l.text)
						if i < len(min)-1 || trailingNL {
							sb.WriteByte('\n')
						}
					}
					mb := []byte(sb.String())
					mg := ilDecodeAll[ilPlain](t, mb, m.wrap(mb), bs, len(min))
					t.Errorf("reader=%s buf=%d trailingNL=%v minimal (%d of %d lines):\n%s", m.name, bs, trailingNL, len(min), len(lines), ilDiffRes(min, mw, mg))
				}
			}
		}
	}
}

func ilDiffRes(lines []ilLine, want, got []ilDecRes) string {
	var sb strings.Builder
	n := max(len(want), len(got))
	for i := range n {
		var w, g ilDecRes
		wm, gm := "<none>", "<none>"
		if i < len(want) {
			w, wm = want[i], want[i].err
		}
		if i < len(got) {
			g, gm = got[i], got[i].err
		}
		mark := " "
		if i >= len(want) || i >= len(got) || w != g {
			mark = "!"
		}
		line := ""
		if i < len(lines) {
			line = lines[i].text
			if len(line) > 90 {
				line = line[:90] + "..."
			}
		}
		fmt.Fprintf(&sb, "  %s line %2d want=%-7s got=%-7s %q\n", mark, i, wm, gm, line)
	}
	return sb.String()
}

// A Decoder may switch target types between calls. Each switch rebuilds the
// Parser while the window and its mounted scan stay with the Decoder, so lines
// of different shapes, some of them wrong for their target, must still decode
// independently.
type ilHeteroStep struct {
	line string
	kind int // 0 ilInner, 1 []int, 2 map[string]string, 3 string, 4 ilPlain
}

func ilHeteroRun(t *testing.T, steps []ilHeteroStep, rd io.Reader, bufSize int) []string {
	t.Helper()
	opts := []DecoderOption{WithSkipErrors(func(error) bool { return true })}
	if bufSize > 0 {
		opts = append(opts, WithBufferSize(bufSize))
	}
	d := NewDecoder(rd, opts...)
	var out []string
	for _, st := range steps {
		var dst any
		switch st.kind {
		case 0:
			dst = new(ilInner)
		case 1:
			dst = new([]int)
		case 2:
			dst = new(map[string]string)
		case 3:
			dst = new(string)
		case 4:
			dst = new(ilPlain)
		}
		err := d.Decode(dst)
		if err != nil {
			out = append(out, "ERR:"+ilClass(ilDescribeErr(err)))
			continue
		}
		js, _ := json.Marshal(dst)
		out = append(out, string(js))
	}
	return out
}

func ilHeteroDiverges(t *testing.T, steps []ilHeteroStep, chunk, bs int) ([]string, []string, bool) {
	var sb strings.Builder
	for _, s := range steps {
		sb.WriteString(s.line)
		sb.WriteByte('\n')
	}
	data := []byte(sb.String())
	var want []string
	for _, s := range steps {
		one := []byte(s.line + "\n")
		want = append(want, ilHeteroRun(t, []ilHeteroStep{s}, bytes.NewReader(one), 0)[0])
	}
	var rd io.Reader = bytes.NewReader(data)
	if chunk > 0 {
		rd = &chunkReader{data: data, chunk: chunk}
	}
	got := ilHeteroRun(t, steps, rd, bs)
	return want, got, !reflect.DeepEqual(got, want)
}

func TestInterleaveDecoderHeterogeneousTargets(t *testing.T) {
	pool := [][]ilHeteroStep{
		{{`{"B":1,"C":"x","D":[1.5]}`, 0}, {`{"B":"bad"}`, 0}, {`[1]`, 0}},
		{{`[1,2,3]`, 1}, {`["a"]`, 1}, {`{"a":1}`, 1}, {`[1,x]`, 1}},
		{{`{"k":"v","j":"w"}`, 2}, {`{"k":1}`, 2}, {`[]`, 2}},
		{{`"hello"`, 3}, {`12`, 3}, {`{}`, 3}},
		{{ilPlainGood[0], 4}, {`{"A":"x","M":{"a":1}}`, 4}, {ilPlainGood[1], 4}, {`{"X":["x"],"Y":[1]}`, 4}},
	}
	r := rand.New(rand.NewPCG(3, 5))
	reported := 0
	for round := 0; round < 80 && reported < 3; round++ {
		var steps []ilHeteroStep
		for len(steps) < 14 {
			g := pool[r.IntN(len(pool))]
			steps = append(steps, g[r.IntN(len(g))])
		}
		for _, chunk := range []int{0, 1, 5, 33} {
			for _, bs := range []int{0, 16, 128} {
				if _, _, bad := ilHeteroDiverges(t, steps, chunk, bs); !bad {
					continue
				}
				min := steps
				for changed := true; changed; {
					changed = false
					for i := 0; i < len(min); i++ {
						cand := append(append([]ilHeteroStep(nil), min[:i]...), min[i+1:]...)
						if _, _, b := ilHeteroDiverges(t, cand, chunk, bs); len(cand) > 0 && b {
							min, changed = cand, true
							i--
						}
					}
				}
				want, got, _ := ilHeteroDiverges(t, min, chunk, bs)
				var sb strings.Builder
				for i := range min {
					mark := " "
					if want[i] != got[i] {
						mark = "!"
					}
					fmt.Fprintf(&sb, "\n  %s kind=%d %-45.45q want=%.50s got=%.50s", mark, min[i].kind, min[i].line, want[i], got[i])
				}
				t.Errorf("chunk=%d buf=%d minimal sequence:%s", chunk, bs, sb.String())
				reported++
				goto nextRound
			}
		}
	nextRound:
	}
}
