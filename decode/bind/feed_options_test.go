package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/velox-io/json/stream"
)

// UnmarshalOption × Stream fields under the feed driver. Options plumb through
// applyOpts into Ctx.OptFlags, which reaches two consumers: mountWindow's
// per-window scan (StrictScan) and the bind VM reading the live context
// during the drive (UseNumber, DisallowUnknown). Stream scopes route element
// binding through the same machine and the scoped arena views, so each option
// must hold across window edges and batch rotation exactly as it does on the
// contiguous path.
//
// Invalid-UTF-8 inputs are built by byte concatenation: a backtick string
// holds the four literal characters `\xff`, which is a malformed JSON escape,
// not a raw 0xff byte.

type feedOptAnyElem struct {
	ID  int `json:"id"`
	Any any `json:"any"`
}

type feedOptAnyHost struct {
	Items stream.Stream[feedOptAnyElem] `json:"items"`
}

func feedOptAnyJSON(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"any":%d}`, i, 100+i)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// TestFeedOptions_UseNumberHoldsAcrossScopes pins UseNumber inside stream
// elements: the number text lands in str_arena under the active scope view,
// so elements held past scope settle and window rotation must still resolve
// their json.Number text after the parse.
func TestFeedOptions_UseNumberHoldsAcrossScopes(t *testing.T) {
	data := feedOptAnyJSON(40)
	for _, chunk := range feedChunkSizes {
		p, err := NewParser[feedOptAnyHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedOptAnyHost
		var held []*feedOptAnyElem
		h.Items.OnRead(func(s stream.Scope[feedOptAnyElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				held = append(held, it.Target())
			}
			return nil
		})
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h, WithUseNumber()); err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if len(held) != 40 {
			t.Fatalf("chunk=%d: held %d elements, want 40", chunk, len(held))
		}
		for _, e := range held {
			n, ok := e.Any.(json.Number)
			if !ok {
				t.Fatalf("chunk=%d: id=%d Any = %T, want json.Number", chunk, e.ID, e.Any)
			}
			if s := n.String(); s != itoa(e.ID+100) {
				t.Fatalf("chunk=%d: id=%d Number = %q, want %q", chunk, e.ID, s, itoa(e.ID+100))
			}
		}
	}
}

// TestFeedOptions_UseNumberDefaultBoxesFloat64 is the lax parity for the
// same host: without the option the feed boxes float64.
func TestFeedOptions_UseNumberDefaultBoxesFloat64(t *testing.T) {
	data := feedOptAnyJSON(4)
	for _, chunk := range []int{1, 7, 4096} {
		p, err := NewParser[feedOptAnyHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedOptAnyHost
		var vals []any
		h.Items.OnRead(func(s stream.Scope[feedOptAnyElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				vals = append(vals, it.Target().Any)
			}
			return nil
		})
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h); err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if len(vals) != 4 {
			t.Fatalf("chunk=%d: got %d elements, want 4", chunk, len(vals))
		}
		for i, v := range vals {
			if f, ok := v.(float64); !ok || f != float64(100+i) {
				t.Fatalf("chunk=%d: vals[%d] = %T(%v), want float64(%d)", chunk, i, v, v, 100+i)
			}
		}
	}
}

// TestFeedOptions_UseNumberDoesNotStickAcrossFeeds guards the per-call reset
// at the UnmarshalFeed entry: an option armed on one feed call must not
// survive to a later call on the same Parser that omits it.
func TestFeedOptions_UseNumberDoesNotStickAcrossFeeds(t *testing.T) {
	data := feedOptAnyJSON(4)
	p, err := NewParser[feedOptAnyHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	run := func(opts ...UnmarshalOption) []any {
		var h feedOptAnyHost
		var vals []any
		h.Items.OnRead(func(s stream.Scope[feedOptAnyElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				vals = append(vals, it.Target().Any)
			}
			return nil
		})
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: 7}, &h, opts...); err != nil {
			t.Fatalf("feed: %v", err)
		}
		return vals
	}
	for i, v := range run(WithUseNumber()) {
		if _, ok := v.(json.Number); !ok {
			t.Fatalf("with option: vals[%d] = %T, want json.Number", i, v)
		}
	}
	for i, v := range run() {
		if _, ok := v.(json.Number); ok {
			t.Fatalf("without option: vals[%d] = json.Number, want float64 (option stuck)", i)
		}
	}
}

type feedOptStrictElem struct {
	ID string `json:"id"`
	N  int    `json:"n"`
}

type feedOptStrictHost struct {
	Items stream.Stream[feedOptStrictElem] `json:"items"`
}

// TestFeedOptions_DisallowUnknownInsideStream pins the option inside stream
// elements: an unknown field in an element body errors with the same
// *UnmarshalTypeError the contiguous path reports, for every window split;
// without the option the same feed binds all elements.
func TestFeedOptions_DisallowUnknownInsideStream(t *testing.T) {
	data := []byte(`{"items":[{"id":"a","n":1},{"id":"b","n":2,"oops":true}]}`)

	p, err := NewParser[feedOptStrictHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var oracle feedOptStrictHost
	var oracleSeen int
	oracle.Items.OnRead(func(s stream.Scope[feedOptStrictElem]) error {
		for it := range s.Iter() {
			if derr := it.Decode(); derr != nil {
				return derr
			}
			oracleSeen++
		}
		return nil
	})
	err = p.Unmarshal(data, &oracle, WithDisallowUnknownFields())
	var tee *UnmarshalTypeError
	if !errors.As(err, &tee) {
		t.Fatalf("contiguous: err = %v, want assignable to *UnmarshalTypeError", err)
	}

	for _, chunk := range feedChunkSizes {
		ps, err := NewParser[feedOptStrictHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedOptStrictHost
		seen := 0
		h.Items.OnRead(func(s stream.Scope[feedOptStrictElem]) error {
			for it := range s.Iter() {
				if derr := it.Decode(); derr != nil {
					return derr
				}
				seen++
			}
			return nil
		})
		err = ps.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h, WithDisallowUnknownFields())
		if !errors.As(err, &tee) {
			t.Fatalf("chunk=%d: err = %v, want assignable to *UnmarshalTypeError", chunk, err)
		}
		// The contiguous oracle saw the same preemption.
		if seen != oracleSeen {
			t.Fatalf("chunk=%d: handler saw %d elements, contiguous saw %d", chunk, seen, oracleSeen)
		}
	}

	for _, chunk := range []int{1, 7, 4096} {
		ps, err := NewParser[feedOptStrictHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedOptStrictHost
		seen := 0
		h.Items.OnRead(func(s stream.Scope[feedOptStrictElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				seen++
			}
			return nil
		})
		if err := ps.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h); err != nil {
			t.Fatalf("lax chunk=%d: %v", chunk, err)
		}
		if seen != 2 {
			t.Fatalf("lax chunk=%d: seen %d elements, want 2", chunk, seen)
		}
	}
}

// TestFeedOptions_DisallowUnknownBatchPreemption pins the batch semantics the
// option exposes: a leaf stream binds whole batches natively before the
// handler receives them, so an unknown-field error in any element of the
// batch aborts the batch and the handler observes zero elements. The feed
// driver must match the contiguous driver exactly; Skip cannot rescue
// elements the batch already bound.
func TestFeedOptions_DisallowUnknownBatchPreemption(t *testing.T) {
	data := []byte(`{"items":[{"id":"a","n":1},{"id":"b","n":2,"oops":true},{"id":"c","n":3,"oops2":1}]}`)

	pc, err := NewParser[feedOptStrictHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var contiguous feedOptStrictHost
	contiguousSeen := 0
	contiguous.Items.OnRead(func(s stream.Scope[feedOptStrictElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			contiguousSeen++
		}
		return nil
	})
	var tee *UnmarshalTypeError
	if err := pc.Unmarshal(data, &contiguous, WithDisallowUnknownFields()); !errors.As(err, &tee) {
		t.Fatalf("contiguous: err = %v, want assignable to *UnmarshalTypeError", err)
	}

	for _, chunk := range feedChunkSizes {
		p, err := NewParser[feedOptStrictHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedOptStrictHost
		seen := 0
		h.Items.OnRead(func(s stream.Scope[feedOptStrictElem]) error {
			for it := range s.Iter() {
				if derr := it.Decode(); derr != nil {
					return derr
				}
				seen++
			}
			return nil
		})
		err = p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h, WithDisallowUnknownFields())
		var tee *UnmarshalTypeError
		if !errors.As(err, &tee) {
			t.Fatalf("chunk=%d: err = %v, want assignable to *UnmarshalTypeError", chunk, err)
		}
		if seen != contiguousSeen {
			t.Fatalf("chunk=%d: handler saw %d elements, contiguous saw %d", chunk, seen, contiguousSeen)
		}
	}
}

type feedOptScanElem struct {
	ID int    `json:"id"`
	S  string `json:"s"`
}

type feedOptScanHost struct {
	Items stream.Stream[feedOptScanElem] `json:"items"`
}

// TestFeedOptions_StrictScanRejectsStreamContent pins the per-window strict
// scan over stream content: a raw control byte or malformed UTF-8 inside an
// element string errors for every window split, matching the contiguous
// oracle. The lax scan binds the same input with the raw bytes passing
// through, in values and map keys alike.
func TestFeedOptions_StrictScanRejectsStreamContent(t *testing.T) {
	bad := map[string][]byte{
		"control": []byte(`{"items":[{"id":0,"s":"ok"},{"id":1,"s":"x` + "\x00" + `y"}]}`),
		"utf8":    []byte(`{"items":[{"id":0,"s":"ok"},{"id":1,"s":"x` + "\xff" + `y"}]}`),
	}
	wantLax := map[string]string{"control": "x\x00y", "utf8": "x\xffy"}
	for name, data := range bad {
		p, err := NewParser[feedOptScanHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var oracle feedOptScanHost
		oracle.Items.OnRead(func(s stream.Scope[feedOptScanElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
			}
			return nil
		})
		if err := p.Unmarshal(data, &oracle, WithStrictScan()); err == nil {
			t.Fatalf("%s: contiguous strict scan accepted invalid input", name)
		}

		for _, chunk := range feedChunkSizes {
			ps, err := NewParser[feedOptScanHost]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			var h feedOptScanHost
			h.Items.OnRead(func(s stream.Scope[feedOptScanElem]) error {
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
				}
				return nil
			})
			if err := ps.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h, WithStrictScan()); err == nil {
				t.Fatalf("%s chunk=%d: strict feed scan accepted invalid input", name, chunk)
			}

			var lax feedOptScanHost
			var laxS []string
			lax.Items.OnRead(func(s stream.Scope[feedOptScanElem]) error {
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
					laxS = append(laxS, it.Target().S)
				}
				return nil
			})
			if err := ps.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &lax); err != nil {
				t.Fatalf("%s chunk=%d: lax feed: %v", name, chunk, err)
			}
			if len(laxS) != 2 || laxS[1] != wantLax[name] {
				t.Fatalf("%s chunk=%d: lax got %q, want 2 elements with second %q", name, chunk, laxS, wantLax[name])
			}
		}
	}
}

// TestFeedOptions_StrictScanSplitMultibyteValid guards the cross-edge UTF-8
// contract: a valid multibyte sequence split across a window edge must pass
// the strict scan (the scanner completes the pending sequence against the pad
// and re-scans withheld strings whole), and the decoded element strings must
// stay intact for every split.
func TestFeedOptions_StrictScanSplitMultibyteValid(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 33; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"s":"世界-%d"}`, i, i)
	}
	b.WriteString(`]}`)
	data := []byte(b.String())

	for _, chunk := range feedChunkSizes {
		p, err := NewParser[feedOptScanHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedOptScanHost
		var got []string
		h.Items.OnRead(func(s stream.Scope[feedOptScanElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				got = append(got, it.Target().S)
			}
			return nil
		})
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h, WithStrictScan()); err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if len(got) != 33 {
			t.Fatalf("chunk=%d: got %d elements, want 33", chunk, len(got))
		}
		for i, s := range got {
			if want := "世界-" + itoa(i); s != want {
				t.Fatalf("chunk=%d: got[%d] = %q, want %q", chunk, i, s, want)
			}
		}
	}
}

// TestFeedOptions_EscapeSplitAcrossEdges pins the string-atomicity contract
// under window splits: a window ending inside a string withholds the whole
// string from its opening quote (extract.h tail_start), so an escape sequence
// cut at any phase re-scans whole in the next window and the binder never
// sees a truncated body. Every escape form is exercised, including surrogate
// pairs, under both scan modes.
func TestFeedOptions_EscapeSplitAcrossEdges(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"s":"a\"b\\cA\u00e9😀\n\t\/"}`, i)
	}
	b.WriteString(`]}`)
	data := []byte(b.String())
	want := "a\"b\\cAé😀\n\t/"

	for _, chunk := range feedChunkSizes {
		for _, strict := range []bool{false, true} {
			p, err := NewParser[feedOptScanHost]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			var h feedOptScanHost
			var got []string
			h.Items.OnRead(func(s stream.Scope[feedOptScanElem]) error {
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
					got = append(got, it.Target().S)
				}
				return nil
			})
			label := "lax"
			var opts []UnmarshalOption
			if strict {
				label = "strict"
				opts = append(opts, WithStrictScan())
			}
			if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h, opts...); err != nil {
				t.Fatalf("chunk=%d %s: %v", chunk, label, err)
			}
			if len(got) != 30 {
				t.Fatalf("chunk=%d %s: got %d elements, want 30", chunk, label, len(got))
			}
			for i, s := range got {
				if s != want {
					t.Fatalf("chunk=%d %s: got[%d] = %q, want %q", chunk, label, i, s, want)
				}
			}
		}
	}
}

// TestFeedOptions_StrictScanDoesNotStickAcrossFeeds guards the per-call
// reset for the scan flag on the feed entry: a strict feed rejecting invalid
// UTF-8 must not make the next lax feed on the same Parser reject it.
func TestFeedOptions_StrictScanDoesNotStickAcrossFeeds(t *testing.T) {
	data := []byte(`{"items":[{"id":0,"s":"x` + "\xff" + `y"}]}`)
	p, err := NewParser[feedOptScanHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	run := func(opts ...UnmarshalOption) error {
		var h feedOptScanHost
		h.Items.OnRead(func(s stream.Scope[feedOptScanElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
			}
			return nil
		})
		return p.UnmarshalFeed(&chunkReader{data: data, chunk: 3}, &h, opts...)
	}
	if err := run(WithStrictScan()); err == nil {
		t.Fatal("strict feed accepted invalid UTF-8")
	}
	if err := run(); err != nil {
		t.Fatalf("lax feed after strict: %v (option stuck)", err)
	}
}
