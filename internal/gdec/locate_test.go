package gdec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestLocateMatchesStdlib checks that Locate accepts what encoding/json
// accepts and names the byte encoding/json stops after: the offending byte
// sits at std's offset minus one, and an early end sits at len(src).
func TestLocateMatchesStdlib(t *testing.T) {
	docs := []string{
		`{}`, `[]`, `{"a":1}`, ` [1, "x", true, null, -1.5e3, {"b":[]}] `,
		`{"a":1,tS":2}`, `{"Y"[4,5]}`, `{"a":"x`, `{"a":1,"b"`, `{"a"`, `{"a":1,}`, `[1,]`, `[1 2]`,
		`{"a":{"b" 1}}`, `]`, `{"a":1}}`, `{"a":1} x`, `{"a":tru}`, `{"a":nul`, `{"a":01}`, `{"a":1.}`,
		`{"a":-}`, `{"a":"\q"}`, `[{"a":1]`, `{"a":[1}`, `{,}`, `[,1]`, `"x"`, `7`, ``, `   `,
	}
	for _, d := range docs {
		src := []byte(d)
		off, eof, ok := Locate(src, ScanCtl)
		var se *json.SyntaxError
		err := json.Unmarshal(src, new(any))
		if !errors.As(err, &se) {
			if ok {
				t.Errorf("%q: std accepts, Locate reports %d eof=%v", d, off, eof)
			}
			continue
		}
		want, wantEOF := int(se.Offset)-1, se.Error() == "unexpected end of JSON input"
		// Before go1.27, encoding/json ends the input by scanning a synthetic
		// space, so a truncated literal reports that space at offset len(src).
		if len(src) > 0 && int(se.Offset) == len(src) && src[len(src)-1] != ' ' &&
			strings.HasPrefix(se.Error(), "invalid character ' '") {
			wantEOF = true
		}
		if wantEOF {
			want = len(src)
		}
		if !ok || off != want || eof != wantEOF {
			t.Errorf("%q: Locate = %d eof=%v ok=%v, std %d (%s)", d, off, eof, ok, se.Offset, se)
		}
	}
}

// TestLocateStringPolicy checks that the string checks follow the scan mode
// and name the byte that breaks an escape.
func TestLocateStringPolicy(t *testing.T) {
	for _, c := range []struct {
		doc        string
		mode       ScanMode
		off        int
		eof, wrong bool
	}{
		{"{\"a\":\"x\x01\"}", ScanLax, 0, false, false},
		{"{\"a\":\"x\x01\"}", ScanCtl, 7, false, true},
		{"{\"a\":\"x\xff\"}", ScanCtl, 0, false, false},
		{"{\"a\":\"x\xff\"}", ScanStrict, 7, false, true},
		{"{\"a\":\"x\xe2\x82\xac\"}", ScanStrict, 0, false, false},
		// A short \u escape names its first non-hex byte.
		{`{"a":"\u12"}`, ScanLax, 10, false, true},
		{`{"a":"\u12`, ScanLax, 10, true, true},
	} {
		off, eof, wrong := Locate([]byte(c.doc), c.mode)
		if off != c.off || eof != c.eof || wrong != c.wrong {
			t.Errorf("%q mode %d: Locate = %d eof=%v ok=%v, want %d %v %v", c.doc, c.mode, off, eof, wrong, c.off, c.eof, c.wrong)
		}
	}
}

// TestCheckAtAgreesWithCheck checks that CheckAt fails exactly where Check
// fails and names the offending byte.
func TestCheckAtAgreesWithCheck(t *testing.T) {
	for _, c := range []struct {
		doc  string
		mode ScanMode
		off  int
		eof  bool
	}{
		{`{"a":"x`, ScanLax, 7, true},
		{`{"a":"x\"`, ScanLax, 9, true},
		{"{\"a\":\"x\x01\"}", ScanStrict, 7, false},
		{"{\"a\":\"x\\\x01\"}", ScanStrict, 8, false},
		{"{\"a\":1,\xff}", ScanStrict, 7, false},
		{`{"a":"x"}`, ScanStrict, 0, false},
		{"{\"a\":\"x\x01\"}", ScanLax, 0, false},
	} {
		off, eof, bad := CheckAt([]byte(c.doc), c.mode)
		if bad == Check([]byte(c.doc), c.mode) {
			t.Errorf("%q mode %d: CheckAt bad=%v disagrees with Check", c.doc, c.mode, bad)
		}
		if bad && (off != c.off || eof != c.eof) {
			t.Errorf("%q mode %d: CheckAt = %d eof=%v, want %d eof=%v", c.doc, c.mode, off, eof, c.off, c.eof)
		}
	}
}
