package tests

// Coverage matrix for the strict body policy across every consumer the bind
// walk passes a string span through. Each case names a placement of a raw
// violation (malformed UTF-8 or an unescaped control byte) that the default
// scan passes raw and WithStrictScan must reject. The same corpus runs
// against the native and the pure-Go engine through the test matrix, so
// both reach the same verdict.

import (
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
)

type strictMatrix struct {
	Bound   string            `json:"bound"`
	Known   map[string]string `json:"known"`
	Unknown map[string]string `json:"unknown"`
	Nested  []strictMatrixSub `json:"nested"`
}

type strictMatrixSub struct {
	S string `json:"s"`
}

// strictMatrixCases lists a source with the placement its violation sits
// in. Sources stay valid JSON under the default scan, which passes raw
// bytes through.
var strictMatrixCases = []struct {
	placement string
	src       string
}{
	{"bound-field", "{\"bound\":\"a\xffb\"}"},
	{"bound-field-ctl", "{\"bound\":\"a\x01b\"}"},
	{"bound-field-long", "{\"bound\":\"" + strings.Repeat("x", 32) + "\xff\"}"},
	{"bound-field-escape-then", "{\"bound\":\"a\\nb\xff\"}"},
	{"bound-field-escape-before", "{\"bound\":\"a\xffb\\nc\"}"},
	{"map-key", "{\"known\":{\"k\xff\":\"v\"}}"},
	{"map-value", "{\"known\":{\"k\":\"v\xff\"}}"},
	{"unknown-field-skipped", "{\"zz\xff\":\"v\"}"},
	{"unknown-field-value", "{\"zz\":\"v\xff\"}"},
	{"unknown-container", "{\"zz\":{\"a\":\"v\xff\",\"b\":1}}"},
	{"nested-element", "{\"nested\":[{\"s\":\"a\xffb\"}]}"},
	{"struct-key-bad", "{\"bo\xffund\":\"v\"}"},
	{"bound-8b-aligned", "{\"bound\":\"xxxxxxxx\xed\xa0\x80\"}"},
	{"bound-8b-misaligned", "{\"bound\":\"xxxxxxxxx\xed\xa0\x80\"}"},
	{"truncated-lead", "{\"bound\":\"\xe4\xb8\"}"},
	{"overlong", "{\"bound\":\"\xc0\x80\"}"},
	{"lone-continuation", "{\"bound\":\"\x80\"}"},
}

func TestStrictScanMatrix_RejectsEveryPlacement(t *testing.T) {
	for _, tc := range strictMatrixCases {
		src := []byte(tc.src)
		var lax strictMatrix
		if err := vjson.Unmarshal(src, &lax); err != nil {
			t.Errorf("%s: default scan rejected: %v", tc.placement, err)
			continue
		}
		var strict strictMatrix
		if err := vjson.Unmarshal(src, &strict, vjson.WithStrictScan()); err == nil {
			t.Errorf("%s: WithStrictScan accepted %q", tc.placement, tc.src)
		}
	}
}

// TestStrictScanMatrix_AcceptsValidMultibyte keeps the strict path open for
// well-formed bodies at the same placements.
func TestStrictScanMatrix_AcceptsValidMultibyte(t *testing.T) {
	src := "{\"bound\":\"世界\",\"known\":{\"ключ\":\"значение\"},\"zz\":\"" + strings.Repeat("ab世界", 20) + "\",\"nested\":[{\"s\":\"技\"}]}"
	var dst strictMatrix
	if err := vjson.Unmarshal([]byte(src), &dst, vjson.WithStrictScan()); err != nil {
		t.Fatalf("WithStrictScan rejected valid multibyte: %v", err)
	}
}

// TestStrictScanMatrix_ValueDoc covers the Value document walk.
func TestStrictScanMatrix_ValueDoc(t *testing.T) {
	src := "{\"v\":\"a\xffb\"}"
	var lax struct {
		V value.Value `json:"v"`
	}
	if err := vjson.Unmarshal([]byte(src), &lax); err != nil {
		t.Fatalf("default scan rejected: %v", err)
	}
	var strict struct {
		V value.Value `json:"v"`
	}
	if err := vjson.Unmarshal([]byte(src), &strict, vjson.WithStrictScan()); err == nil {
		t.Fatal("WithStrictScan accepted invalid UTF-8 in a Value doc")
	}
}
