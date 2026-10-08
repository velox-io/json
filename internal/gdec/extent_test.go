package gdec

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// refExtent is the byte-at-a-time extent automaton ExtentScanner.Scan must
// agree with at every refill.
type refExtent struct {
	pos, depth                  int
	started, scalar, inStr, esc bool
}

func (s *refExtent) scan(b []byte, eof bool) (int, bool) {
	for ; s.pos < len(b); s.pos++ {
		c := b[s.pos]
		if s.inStr {
			switch {
			case s.esc:
				s.esc = false
			case c == '\\':
				s.esc = true
			case c == '"':
				s.inStr = false
				if s.depth == 0 {
					return s.pos + 1, true
				}
			}
			continue
		}
		if !s.started {
			s.started = true
			switch c {
			case '"':
				s.inStr = true
			case '{', '[':
				s.depth = 1
			case '}', ']', ',':
				return s.pos + 1, true
			default:
				s.scalar = true
			}
			continue
		}
		if s.scalar {
			if !IsNonDelim(c) {
				return s.pos, true
			}
			continue
		}
		switch c {
		case '"':
			s.inStr = true
		case '{', '[':
			s.depth++
		case '}', ']':
			if s.depth--; s.depth == 0 {
				return s.pos + 1, true
			}
		}
	}
	if eof && s.scalar {
		return len(b), true
	}
	return 0, false
}

// checkExtentCuts feeds src[:cut] for each ascending cut to both scanners,
// the last cut carrying final as its eof, and requires equal verdicts at
// every step.
func checkExtentCuts(t *testing.T, src []byte, start int, cuts []int, final bool) {
	t.Helper()
	got := ExtentScanner{Pos: start}
	want := refExtent{pos: start}
	for k, cut := range cuts {
		eof := final && k == len(cuts)-1
		ge, gd := got.Scan(src[:cut], eof)
		we, wd := want.scan(src[:cut], eof)
		if ge != we || gd != wd {
			t.Fatalf("src %q start %d cuts %v: at cut %d eof=%v got (%d,%v), want (%d,%v)",
				src, start, cuts, cut, eof, ge, gd, we, wd)
		}
		if gd {
			return
		}
	}
}

// checkExtent runs src through the refill shapes a reader produces: one
// buffer, every two-way split with a refill that adds no bytes, and one
// byte per refill.
func checkExtent(t *testing.T, src []byte, start int) {
	t.Helper()
	n := len(src)
	for _, final := range []bool{true, false} {
		checkExtentCuts(t, src, start, []int{n}, final)
		for k := start; k < n; k++ {
			checkExtentCuts(t, src, start, []int{k, k, n}, final)
		}
		var bytewise []int
		for k := start; k <= n; k++ {
			bytewise = append(bytewise, k)
		}
		checkExtentCuts(t, src, start, bytewise, final)
	}
}

func TestExtentScannerMatchesReference(t *testing.T) {
	long := strings.Repeat("abcdefgh", 3)
	docs := []string{
		``, `{}`, `[]`, `{"a":1}`, `[1,2,[3,{"b":[]}]]`, `{"a":"}"}`, `["]",{"[":"{"}]`,
		`"x"`, `""`, `"\""`, `"\\"`, `"a\\\"b"`, `{"a":"\\"}`, `{"a":"\"}"}`, `["\\\\",1]`,
		`123`, `-1.5e3 `, `true`, `null,`, `7]`, `}`, `]`, `,`, `"open`, `{"a":[1,2`, `{"a":"x\`,
		`{"` + long + `":"` + long + `\"` + long + `"}`, `[` + long + `]`, `"` + long + `\\"`,
		`{"é":"ü\u00e9","x":["日本",{"k":"\\"}]}`, `[1, 2 , {"a" : [ ] } ]  `, `{}{}`, `[][1]`,
	}
	for _, d := range docs {
		checkExtent(t, []byte(d), 0)
		// A value that starts past a consumed prefix.
		checkExtent(t, []byte(`{"z":1} `+d), 8)
	}
}

func TestExtentScannerRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	// A borrowing byte match misflags a byte that equals a flagged byte with
	// bit 0 flipped when it follows a real match: '#', ']', 'z', 'Z', '|',
	// and '\\', the last two after the bracket fold. The high-bit twins of
	// the brackets catch a match that ignores bit 7.
	alphabet := []byte("{}[]\"\\,: ab01\xc3\xa9#zZ|\xdb\xfb\xfd")
	for range 4000 {
		src := make([]byte, r.IntN(70))
		for i := range src {
			src[i] = alphabet[r.IntN(len(alphabet))]
		}
		if len(src) > 0 && r.IntN(2) == 0 {
			src[0] = "{[\""[r.IntN(3)]
		}
		checkExtent(t, src, 0)
	}
}
