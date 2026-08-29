package dom

import (
	"testing"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/value"
)

// Copy-mode strings whose bodies decode without a single escape reach the tape
// as TagStrFree: an arena-backed copy that re-serializes verbatim. Bodies with
// any escape stay TagString. These tests pin the tag boundary, the navigation
// surface over the new tag, and re-forwarding fidelity.

var strFreeBodies = []string{
	``, `a`, `hello`, `plain text with spaces`,
	`unicode é☃ snow ☃`,
	`semi;colon [brackets] {braces}`,
	str100('X'),
}

// raw is the JSON source form, decoded the bytes the body must read back as.
// The last two entries decode to escape-free content but still carry a
// backslash in source, so the cheap producer gate keeps them TagString.
var strEscapedBodies = []struct{ raw, decoded string }{
	{`a\nb`, "a\nb"},
	{`quote\"inside`, `quote"inside`},
	{`back\\slash`, `back\slash`},
	{`tab\there`, "tab\there"},
	{`ctrl\u0001x`, "ctrl\x01x"},
	{`\/slash`, `/slash`},
	{`\u0041A`, "AA"},
}

func TestStrFree_TagBoundary(t *testing.T) {
	for _, body := range strFreeBodies {
		src := `"` + body + `"`
		v, err := Parse([]byte(src))
		if err != nil {
			t.Errorf("%s: Parse: %v", src, err)
			continue
		}
		if tag := descriptor(&v).TagAt(0); tag != valueabi.TagStrFree {
			t.Errorf("%s: value tag=%q, want TagStrFree", src, tag)
		}
		if got, ok := v.Str(); !ok || got != body {
			t.Errorf("%s: Str()=%q,%v", src, got, ok)
		}
	}
	for _, c := range strEscapedBodies {
		src := `"` + c.raw + `"`
		v, err := Parse([]byte(src))
		if err != nil {
			t.Errorf("%s: Parse: %v", src, err)
			continue
		}
		if tag := descriptor(&v).TagAt(0); tag != valueabi.TagString {
			t.Errorf("%s: value tag=%q, want TagString", src, tag)
		}
		if got, ok := v.Str(); !ok || got != c.decoded {
			t.Errorf("%s: Str()=%q,%v, want %q", src, got, ok, c.decoded)
		}
	}
}

// Object keys are tape strings too: escape-free keys carry the new tag at the
// key position, escaped keys keep the old one.
func TestStrFree_KeyBoundary(t *testing.T) {
	v, err := Parse([]byte(`{"plain":1,"esc\"aped":2}`))
	if err != nil {
		t.Fatal(err)
	}
	root := contentRoot()
	if tag := descriptor(&v).TagAt(root + 1); tag != valueabi.TagStrFree {
		t.Errorf("key 'plain' tag=%q, want TagStrFree", tag)
	}
	escIdx := descriptor(&v).Skip(root + 2)
	if tag := descriptor(&v).TagAt(escIdx); tag != valueabi.TagString {
		t.Errorf("key 'esc\"aped' tag=%q, want TagString", tag)
	}
}

func TestStrFree_Navigation(t *testing.T) {
	v, err := Parse([]byte(`{"s":"hello","arr":["a","b"],"esc":"x\ny"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Type(); got != value.KindObject {
		t.Fatalf("Type()=%v", got)
	}
	if s, ok := v.GetString("s"); !ok || s != "hello" {
		t.Errorf("GetString(s)=%q,%v", s, ok)
	}
	arr := v.Get("arr")
	arr.ForEachElem(func(i int, val value.Value) bool {
		if got, ok := val.Str(); !ok || got != string(rune('a'+i)) {
			t.Errorf("arr[%d].Str()=%q,%v", i, got, ok)
		}
		return true
	})
	// Mixed tags iterate uniformly: ForEachKey reads both flavors.
	n := 0
	v.ForEachKey(func(key string, val value.Value) bool {
		n++
		switch key {
		case "s", "arr":
		case "esc":
			if s, ok := val.Str(); !ok || s != "x\ny" {
				t.Errorf("esc=%q,%v", s, ok)
			}
		default:
			t.Errorf("unexpected key %q", key)
		}
		return true
	})
	if n != 3 {
		t.Errorf("ForEachKey visited %d keys, want 3", n)
	}
}

// Re-forwarding reproduces the original bytes for the shapes where the tag
// matters, including siblings after the string so a wrong word count would land
// the walk mid-entry.
func TestStrFree_ReforwardsSourceBytes(t *testing.T) {
	srcs := []string{
		`"hello"`,
		`{"k":"v"}`,
		`["a","b","c"]`,
		`{"s":"hello","n":42,"esc":"a\nb"}`,
		`["a",7,"b"]`,
		`{"a":"x","b":{"c":"y"},"d":[null,"z"]}`,
	}
	for _, src := range srcs {
		v, err := Parse([]byte(src))
		if err != nil {
			t.Errorf("%s: Parse: %v", src, err)
			continue
		}
		if got := v.String(); got != src {
			t.Errorf("reforward mismatch\n  src=%s\n  got=%s", src, got)
		}
		if got, err := v.MarshalJSON(); err != nil || string(got) != src {
			t.Errorf("%s: MarshalJSON=%s,%v", src, got, err)
		}
	}
}

// Zero-copy mode is untouched: escape-free strings alias src as TagStrRaw,
// escaped ones go to the arena as TagString.
func TestStrFree_ZeroCopyUnchanged(t *testing.T) {
	v := mustParseTapeZC(t, `{"plain":"hello","esc":"a\nb"}`)
	root := contentRoot()
	if tag := descriptor(&v).TagAt(root + 1); tag != valueabi.TagStrRaw {
		t.Errorf("zc key 'plain' tag=%q, want TagStrRaw", tag)
	}
	esc := v.Get("esc")
	if !esc.Exists() {
		t.Fatal("missing esc field")
	}
	escDesc := descriptor(&esc)
	if tag := escDesc.TagAt(int(escDesc.Tidx)); tag != valueabi.TagString {
		t.Errorf("zc value 'a\\nb' tag=%q, want TagString", tag)
	}
}
