package bind

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/vopt"

	"github.com/velox-io/json/value"
)

// options_test covers the per-call UnmarshalOption surface. Options must take
// effect on the call that receives them and must not stick to a pooled or reused
// Parser across calls.

// --- UseNumber ---

// TestUseNumber_BoxesAsJsonNumber confirms the option routes any/interface{}
// numbers through the native BIND_OPT_USE_NUMBER path, which tags the eface
// with json.Number (vbind/build.go NumberType) instead of float64.
func TestUseNumber_BoxesAsJsonNumber(t *testing.T) {
	src := `{"v":123}`

	// Default: interface{} numbers decode as float64.
	var def anyField
	if err := Unmarshal([]byte(src), &def); err != nil {
		t.Fatalf("default Unmarshal: %v", err)
	}
	if f, ok := def.V.(float64); !ok || f != 123 {
		t.Fatalf("default V = %T(%v), want float64(123)", def.V, def.V)
	}

	// UseNumber: interface{} numbers decode as json.Number.
	var num anyField
	if err := Unmarshal([]byte(src), &num, vopt.UseNumber(true)); err != nil {
		t.Fatalf("UseNumber Unmarshal: %v", err)
	}
	n, ok := num.V.(json.Number)
	if !ok {
		t.Fatalf("UseNumber V = %T, want json.Number", num.V)
	}
	if s := n.String(); s != "123" {
		t.Fatalf("json.Number = %q, want %q", s, "123")
	}
}

// TestUseNumber_DoesNotStickAcrossCalls guards the pooled-Parser reset at
// the package Unmarshal entry (bind.go optFlags=0). A prior call's option must
// not leak into a later call that omits it. This is the regression that
// Parser.UseNumber() silently failed: sticky-per-call is the contract here.
func TestUseNumber_DoesNotStickAcrossCalls(t *testing.T) {
	src := `{"v":1}`

	var first anyField
	if err := Unmarshal([]byte(src), &first, vopt.UseNumber(true)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, ok := first.V.(json.Number); !ok {
		t.Fatalf("first V = %T, want json.Number", first.V)
	}

	// Same destination type → same pooled Parser shape. The option must not
	// survive the pool round-trip.
	var second anyField
	if err := Unmarshal([]byte(src), &second); err != nil {
		t.Fatalf("second: %v", err)
	}
	if _, ok := second.V.(json.Number); ok {
		t.Fatalf("second V = json.Number, want float64 (option leaked across calls)")
	}
}

// TestParser_UseNumber_PerCall confirms the Parser.Unmarshal entry also
// resets per call (bind.go optFlags=0), so a Parser reused with and without
// the option behaves per-call, not sticky.
func TestParser_UseNumber_PerCall(t *testing.T) {
	p, err := NewParser[anyField]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := []byte(`{"v":42}`)

	var with anyField
	if err := p.Unmarshal(src, &with, vopt.UseNumber(true)); err != nil {
		t.Fatalf("with option: %v", err)
	}
	if _, ok := with.V.(json.Number); !ok {
		t.Fatalf("with option V = %T, want json.Number", with.V)
	}

	var without anyField
	if err := p.Unmarshal(src, &without); err != nil {
		t.Fatalf("without option: %v", err)
	}
	if _, ok := without.V.(json.Number); ok {
		t.Fatalf("without option V = json.Number, want float64 (option stuck on Parser)")
	}
}

// --- RejectUnknownMembers ---

type disallowTarget struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// TestRejectUnknownMembers_RejectsUnknownField confirms the option arms
// the native BIND_OPT_DISALLOW_UNKNOWN check (bind.h), which yields
// BIND_ERR_UNKNOWN_FIELD for a JSON member with no matching Go field.
func TestRejectUnknownMembers_RejectsUnknownField(t *testing.T) {
	src := `{"name":"Ada","age":36,"role":"admin"}`

	// Without the option: unknown "role" is silently ignored.
	var lax disallowTarget
	if err := Unmarshal([]byte(src), &lax); err != nil {
		t.Fatalf("lax Unmarshal: %v", err)
	}
	if lax.Name != "Ada" || lax.Age != 36 {
		t.Fatalf("lax = %+v, want {Ada 36}", lax)
	}

	// With the option: unknown "role" is an error.
	var strict disallowTarget
	err := Unmarshal([]byte(src), &strict, vopt.RejectUnknownMembers(true))
	if err == nil {
		t.Fatalf("strict Unmarshal: want error for unknown field, got nil")
	}
	// The bind path surfaces this as *UnmarshalTypeError (errors.go BindErrUnknownField).
	var tee *UnmarshalTypeError
	if !errors.As(err, &tee) {
		t.Fatalf("err = %v, want assignable to *UnmarshalTypeError", err)
	}
}

// TestRejectUnknownMembers_AcceptsKnownFields confirms the option does
// not reject inputs whose fields all map.
func TestRejectUnknownMembers_AcceptsKnownFields(t *testing.T) {
	src := `{"name":"Ada","age":36}`
	var strict disallowTarget
	if err := Unmarshal([]byte(src), &strict, vopt.RejectUnknownMembers(true)); err != nil {
		t.Fatalf("strict Unmarshal: %v", err)
	}
	if strict.Name != "Ada" || strict.Age != 36 {
		t.Fatalf("strict = %+v, want {Ada 36}", strict)
	}
}

// TestRejectUnknownMembers_DoesNotStickAcrossCalls guards the pooled
// reset: a strict call must not make a later lax call on the same pooled
// Parser reject unknown fields.
func TestRejectUnknownMembers_DoesNotStickAcrossCalls(t *testing.T) {
	src := `{"name":"Ada","age":36,"extra":true}`

	var strict disallowTarget
	if err := Unmarshal([]byte(src), &strict, vopt.RejectUnknownMembers(true)); err == nil {
		t.Fatalf("strict: want error, got nil")
	}

	var lax disallowTarget
	if err := Unmarshal([]byte(src), &lax); err != nil {
		t.Fatalf("lax after strict: want nil (option leaked), got %v", err)
	}
}

// --- AllowInvalidUTF8(false) ---

type strictScanTarget struct {
	S string `json:"s"`
}

type strictScanValueTarget struct {
	V value.Value `json:"v"`
}

func TestRejectInvalidUTF8_ValidatesRawInput(t *testing.T) {
	tests := []struct {
		name string
		src  []byte
	}{
		{name: "raw-nul", src: []byte{'{', '"', 's', '"', ':', '"', 'a', 0, 'b', '"', '}'}},
		{name: "invalid-utf8", src: []byte{'{', '"', 's', '"', ':', '"', 0xff, '"', '}'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lax strictScanTarget
			if err := Unmarshal(tt.src, &lax); err != nil {
				t.Fatalf("default scan: %v", err)
			}
			var strict strictScanTarget
			if err := Unmarshal(tt.src, &strict, vopt.AllowInvalidUTF8(false)); err == nil {
				t.Fatal("StrictScan accepted invalid raw input")
			}
		})
	}
}

func TestRejectInvalidUTF8_AcceptsValidStrings(t *testing.T) {
	for _, src := range []string{
		`{"s":"世界"}`,
		`{"s":"a\nb"}`,
		`{"s":"a\u0001b"}`,
	} {
		var dst strictScanTarget
		if err := Unmarshal([]byte(src), &dst, vopt.AllowInvalidUTF8(false)); err != nil {
			t.Fatalf("Unmarshal(%q): %v", src, err)
		}
	}
}

func TestRejectInvalidUTF8_CountedScan(t *testing.T) {
	src := []byte{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'}
	var lax strictScanValueTarget
	if err := Unmarshal(src, &lax); err != nil {
		t.Fatalf("default counted scan: %v", err)
	}
	var strict strictScanValueTarget
	if err := Unmarshal(src, &strict, vopt.AllowInvalidUTF8(false)); err == nil {
		t.Fatal("StrictScan counted path accepted invalid UTF-8")
	}
}

func TestRejectInvalidUTF8_DoesNotStickAcrossParserCalls(t *testing.T) {
	p, err := NewParser[strictScanTarget]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := []byte{'{', '"', 's', '"', ':', '"', 0xff, '"', '}'}
	var strict strictScanTarget
	if err := p.Unmarshal(src, &strict, vopt.AllowInvalidUTF8(false)); err == nil {
		t.Fatal("StrictScan accepted invalid UTF-8")
	}
	var lax strictScanTarget
	if err := p.Unmarshal(src, &lax); err != nil {
		t.Fatalf("default scan after StrictScan: %v", err)
	}
}

func TestRejectInvalidUTF8_UnmarshalPadded(t *testing.T) {
	src := Pad([]byte{'{', '"', 's', '"', ':', '"', 0xff, '"', '}'})
	var dst strictScanTarget
	if err := UnmarshalPadded(src, &dst, vopt.AllowInvalidUTF8(false)); err == nil {
		t.Fatal("StrictScan accepted invalid UTF-8 from padded input")
	}
}

// --- SkipLenient ---
//
// SkipLenient governs every value a decode discards, and the sites must all
// answer to it: an unknown struct member, a fixed array's surplus element, a
// stopped stream's remainder, and the root value a mismatch discards. Each
// case below puts a malformed token or comma inside the discarded region, so
// the strict skip reports it and the lenient one does not.

type skipLaxTarget struct {
	A int `json:"a"`
}

type skipFixedTarget struct {
	A [1]int `json:"a"`
}

func TestSkipLenient_SkipsUnknownMember(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
	}{
		{name: "malformed-number", src: `{"a":1,"b":1.2.3}`},
		{name: "malformed-comma", src: `{"a":1,"b":[1,,2]}`},
		{name: "junk-atom", src: `{"a":1,"b":xyz}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var strict skipLaxTarget
			if err := Unmarshal([]byte(tt.src), &strict); err == nil {
				t.Fatalf("strict Unmarshal(%q): want a syntax error, got nil", tt.src)
			}
			var lenient skipLaxTarget
			if err := Unmarshal([]byte(tt.src), &lenient, vopt.SkipLenient(true)); err != nil {
				t.Fatalf("lenient Unmarshal(%q): %v", tt.src, err)
			}
			if lenient.A != 1 {
				t.Fatalf("lenient A = %d, want 1", lenient.A)
			}
		})
	}
}

func TestSkipLenient_SkipsSurplusFixedArrayElement(t *testing.T) {
	src := `{"a":[1,2.3.4]}`
	var strict skipFixedTarget
	if err := Unmarshal([]byte(src), &strict); err == nil {
		t.Fatalf("strict Unmarshal(%q): want a syntax error, got nil", src)
	}
	var lenient skipFixedTarget
	if err := Unmarshal([]byte(src), &lenient, vopt.SkipLenient(true)); err != nil {
		t.Fatalf("lenient Unmarshal(%q): %v", src, err)
	}
	if lenient.A[0] != 1 {
		t.Fatalf("lenient A = %v, want [1]", lenient.A)
	}
}

// TestSkipLenient_RootMismatchReportsTheMismatch covers the root skip: it
// consumes the whole root value before surfacing the recorded mismatch, and
// under lenient the discarded scalar's syntax no longer preempts it. The root
// is a malformed atom rather than a malformed number, because a token outside
// the number grammar is malformed input and aborts as a syntax error whatever
// the destination does.
func TestSkipLenient_RootMismatchReportsTheMismatch(t *testing.T) {
	src := `tru`
	var strict skipLaxTarget
	if err := Unmarshal([]byte(src), &strict); err == nil {
		t.Fatalf("strict Unmarshal(%q): want an error, got nil", src)
	}
	var lenient skipLaxTarget
	err := Unmarshal([]byte(src), &lenient, vopt.SkipLenient(true))
	if err == nil {
		t.Fatalf("lenient Unmarshal(%q): want a type mismatch, got nil", src)
	}
	var tee *UnmarshalTypeError
	if !errors.As(err, &tee) {
		t.Fatalf("lenient err = %T (%v), want *UnmarshalTypeError", err, err)
	}
}

// TestSkipLenient_RootMalformedNumberIsSyntax pins the other side of that
// rule: a root number outside the grammar is a syntax error at its first
// byte under lenient too, before any mismatch with the destination.
func TestSkipLenient_RootMalformedNumberIsSyntax(t *testing.T) {
	for _, src := range []string{`0x`, `-x`, `1.5"a"`, `0"X":[1]}`} {
		var lenient skipLaxTarget
		err := Unmarshal([]byte(src), &lenient, vopt.SkipLenient(true))
		var se *SyntaxError
		if !errors.As(err, &se) || se.Offset != 0 {
			t.Errorf("lenient Unmarshal(%q) = %T (%v), want a *SyntaxError at offset 0", src, err, err)
		}
	}
}

// TestSkipLenient_ValidInputIsIdentical guards the other half of the contract:
// over well-formed input the lenient skip changes nothing.
func TestSkipLenient_ValidInputIsIdentical(t *testing.T) {
	for _, src := range []string{
		`{"a":1}`,
		`{"a":1,"b":{"x":[1,2]},"c":"s"}`,
		`{"a":1,"b":[{"y":null},true]}`,
	} {
		var strict, lenient skipLaxTarget
		errStrict := Unmarshal([]byte(src), &strict)
		errLenient := Unmarshal([]byte(src), &lenient, vopt.SkipLenient(true))
		if errStrict != nil || errLenient != nil {
			t.Fatalf("Unmarshal(%q): strict=%v lenient=%v", src, errStrict, errLenient)
		}
		if strict != lenient {
			t.Fatalf("Unmarshal(%q): strict=%+v lenient=%+v", src, strict, lenient)
		}
	}
}

type skipFixedHost struct {
	F [1]int `json:"f"`
	A int    `json:"a"`
}

// TestSkipLenient_FeedResumesAcrossWindowEdges cuts every skipped region at
// every byte: the skip parks its bracket depth at each window edge and
// resumes through the one skip phase, which must reselect the lenient walk
// rather than fall back to the strict one or bind the region as an element.
func TestSkipLenient_FeedResumesAcrossWindowEdges(t *testing.T) {
	for _, src := range []string{
		`{"f":[1,[2,{"k":[3]}],"s",4.4.4],"a":3}`,
		`{"b":[1,,{"x":1.2.3}],"f":[1],"a":3}`,
	} {
		for _, chunk := range []int{1, 3, 7} {
			p, err := NewParser[skipFixedHost]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			var strict skipFixedHost
			if err = p.UnmarshalFeed(&chunkReader{data: []byte(src), chunk: chunk}, &strict); err == nil {
				t.Fatalf("strict UnmarshalFeed(%q) chunk=%d: want a syntax error, got nil", src, chunk)
			}
			var lenient skipFixedHost
			err = p.UnmarshalFeed(&chunkReader{data: []byte(src), chunk: chunk}, &lenient, vopt.SkipLenient(true))
			if err != nil {
				t.Fatalf("lenient UnmarshalFeed(%q) chunk=%d: %v", src, chunk, err)
			}
			if want := (skipFixedHost{F: [1]int{1}, A: 3}); lenient != want {
				t.Fatalf("lenient UnmarshalFeed(%q) chunk=%d = %+v, want %+v", src, chunk, lenient, want)
			}
		}
	}
}

// TestSkipLenient_DrainsStoppedStream stops a stream after its first element,
// so the remainder is drained by the skip. The malformed elements sit past a
// run of well-formed ones, beyond the first batch a leaf stream binds before
// its handler runs.
func TestSkipLenient_DrainsStoppedStream(t *testing.T) {
	src := []byte(`{"items":[` + strings.Repeat(`{"id":"a","n":1},`, 4096) + `{"id":"b","n":1.2.3},[,]],"name":"x"}`)
	run := func(feed bool, opts ...UnmarshalOption) (feedStreamHost, int, error) {
		var h feedStreamHost
		seen := 0
		h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				seen++
				break
			}
			return nil
		})
		if feed {
			p, err := NewParser[feedStreamHost]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			return h, seen, p.UnmarshalFeed(&chunkReader{data: src, chunk: 1}, &h, opts...)
		}
		return h, seen, Unmarshal(src, &h, opts...)
	}
	for _, feed := range []bool{false, true} {
		if _, _, err := run(feed); err == nil {
			t.Fatalf("strict feed=%v: want a syntax error, got nil", feed)
		}
		h, seen, err := run(feed, vopt.SkipLenient(true))
		if err != nil {
			t.Fatalf("lenient feed=%v: %v", feed, err)
		}
		if seen != 1 || h.Name != "x" {
			t.Fatalf("lenient feed=%v: seen=%d name=%q, want 1 and %q", feed, seen, h.Name, "x")
		}
	}
}

func TestSkipLenient_DoesNotStickAcrossParserCalls(t *testing.T) {
	p, err := NewParser[skipLaxTarget]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	src := []byte(`{"a":1,"b":1.2.3}`)
	var lenient skipLaxTarget
	if err := p.Unmarshal(src, &lenient, vopt.SkipLenient(true)); err != nil {
		t.Fatalf("lenient: %v", err)
	}
	var strict skipLaxTarget
	if err := p.Unmarshal(src, &strict); err == nil {
		t.Fatalf("strict after lenient: want a syntax error, got nil (option leaked)")
	}
}

func TestRejectInvalidUTF8_UnmarshalValueDoesNotRescan(t *testing.T) {
	src := []byte{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'}
	var doc strictScanValueTarget
	if err := Unmarshal(src, &doc); err != nil {
		t.Fatalf("build Value: %v", err)
	}
	var got string
	if err := UnmarshalValue(doc.V, &got, vopt.AllowInvalidUTF8(false)); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	// The lax tape build preserves the malformed byte, so serving from the
	// tape yields the raw byte; a strict rescan of the source would instead
	// error, which the call above proves it does not.
	if got != "\xff" {
		t.Fatalf("got %q, want the tape's raw byte", got)
	}
}
