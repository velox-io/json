package bind

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/value"
)

// Stream scopes under the feed driver: the window-aware engine re-enters the
// scope machinery across window edges, scoped views rotate against the
// window-local remaining source, and the retired string-provenance table is
// truncated at batch boundaries. Every test compares chunked feed runs
// against the contiguous oracle.

// feedStreamText prefixes its input so TextUnmarshaler records that crossed a
// window edge are distinguishable from source-backed materializations.
type feedStreamText struct {
	V string
}

func (t *feedStreamText) UnmarshalText(data []byte) error {
	t.V = "text:" + string(data)
	return nil
}

// feedStreamRich mixes every element shape that stresses the seams: escaped
// strings, map staging, deferred RawMessage and TextUnmarshaler spans, and a
// Value field with a per-generation doc.
type feedStreamRich struct {
	ID  string          `json:"id"`
	S   string          `json:"s"`
	M   map[string]int  `json:"m"`
	Raw json.RawMessage `json:"raw"`
	T   feedStreamText  `json:"t"`
	V   value.Value     `json:"v"`
}

type feedStreamRichHost struct {
	Items stream.Stream[feedStreamRich] `json:"items"`
}

func feedStreamRichJSON(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"id-%d","s":"q\"uote\\slash %d 日本","m":{"k%d":%d},"raw":{"n":%d,"a":[1,2]},"t":"val-%d","v":{"k":"v-%d","n":%d}}`,
			i, i, i, i, i, i, i, i)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// feedStreamRichDigest captures the observable output of one decode: midVs
// read each element's Value during iteration, before later batches or the
// scope exit could publish more, while the held fields read after the parse,
// when every generation's doc is published and deferred fields have settled.
type feedStreamRichDigest struct {
	ids   []string
	ss    []string
	ms    []string
	raws  []string
	ts    []string
	vs    []string
	midVs []string
	held  []*feedStreamRich
}

func (d *feedStreamRichDigest) finish() {
	for _, e := range d.held {
		d.ids = append(d.ids, e.ID)
		d.ss = append(d.ss, e.S)
		d.ms = append(d.ms, fmt.Sprint(e.M))
		d.raws = append(d.raws, string(e.Raw))
		d.ts = append(d.ts, e.T.V)
		kvVal := e.V.Get("k")
		kv, _ := kvVal.Str()
		d.vs = append(d.vs, kv)
	}
}

func feedStreamRichRun(t *testing.T, data []byte, chunk int) *feedStreamRichDigest {
	t.Helper()
	p, err := NewParser[feedStreamRichHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var h feedStreamRichHost
	d := &feedStreamRichDigest{}
	count := 0
	h.Items.OnRead(func(s stream.Scope[feedStreamRich]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			midVal := it.Target().V.Get("k")
			midKv, _ := midVal.Str()
			d.midVs = append(d.midVs, midKv)
			if count%7 == 0 {
				d.held = append(d.held, it.Target())
			}
			count++
		}
		return nil
	})
	if chunk > 0 {
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h); err != nil {
			t.Fatalf("feed chunk=%d: %v", chunk, err)
		}
	} else {
		if err := p.Unmarshal(data, &h); err != nil {
			t.Fatalf("contiguous: %v", err)
		}
	}
	d.finish()
	return d
}

func TestFeedStreamRichParity(t *testing.T) {
	data := feedStreamRichJSON(40)
	want := feedStreamRichRun(t, data, 0)
	if len(want.held) == 0 {
		t.Fatal("oracle held nothing")
	}
	// The contiguous oracle validates the mid-iteration reads themselves:
	// per-batch publication must resolve every element's Value inside the
	// handler, before its generation could rotate or the scope exit.
	for i, kv := range want.midVs {
		if kv != fmt.Sprintf("v-%d", i) {
			t.Fatalf("oracle mid-read[%d].V.k = %q, want v-%d", i, kv, i)
		}
	}
	for _, chunk := range feedChunkSizes {
		got := feedStreamRichRun(t, data, chunk)
		if len(got.ids) != len(want.ids) {
			t.Fatalf("chunk=%d: held %d elements, want %d", chunk, len(got.ids), len(want.ids))
		}
		for i := range want.ids {
			if got.ids[i] != want.ids[i] || got.ss[i] != want.ss[i] ||
				got.ms[i] != want.ms[i] || got.raws[i] != want.raws[i] || got.ts[i] != want.ts[i] {
				t.Fatalf("chunk=%d: held[%d] mismatch:\n got %s|%s|%s|%s|%s\nwant %s|%s|%s|%s|%s",
					chunk, i, got.ids[i], got.ss[i], got.ms[i], got.raws[i], got.ts[i],
					want.ids[i], want.ss[i], want.ms[i], want.raws[i], want.ts[i])
			}
		}
		for i := range want.midVs {
			if got.midVs[i] != want.midVs[i] {
				t.Fatalf("chunk=%d: mid-read[%d].V.k = %q, want %q", chunk, i, got.midVs[i], want.midVs[i])
			}
		}
		// Held Values resolve against their own generation's doc; feed docs
		// carry no source view, so string fields must still resolve.
		for i := range want.vs {
			if got.vs[i] != want.vs[i] {
				t.Fatalf("chunk=%d: held[%d].V.k = %q, want %q", chunk, i, got.vs[i], want.vs[i])
			}
		}
	}
}

// Non-leaf streams stop at every element boundary, so a chunk boundary can
// land exactly between the element's slot commit and its body bind.
type feedStreamNestedInner struct {
	V string `json:"v"`
}

type feedStreamNestedElem struct {
	ID    string                               `json:"id"`
	Inner stream.Stream[feedStreamNestedInner] `json:"inner"`
}

type feedStreamNestedHost struct {
	Items stream.Stream[feedStreamNestedElem] `json:"items"`
}

func feedStreamNestedJSON(outer, inner int) []byte {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < outer; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"o-%d","inner":[`, i)
		for j := 0; j < inner; j++ {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"v":"%d-%d-%s"}`, i, j, strings.Repeat("y", 12))
		}
		b.WriteString(`]}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func feedStreamNestedRun(t *testing.T, data []byte, chunk int) (ids []string, vs []string) {
	t.Helper()
	p, err := NewParser[feedStreamNestedHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var h feedStreamNestedHost
	h.Items.OnRead(func(s stream.Scope[feedStreamNestedElem]) error {
		for it := range s.Iter() {
			it.Target().Inner.OnRead(func(is stream.Scope[feedStreamNestedInner]) error {
				for iit := range is.Iter() {
					if err := iit.Decode(); err != nil {
						return err
					}
					vs = append(vs, iit.Target().V)
				}
				return nil
			})
			if err := it.Decode(); err != nil {
				return err
			}
			ids = append(ids, it.Target().ID)
		}
		return nil
	})
	if chunk > 0 {
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h); err != nil {
			t.Fatalf("feed chunk=%d: %v", chunk, err)
		}
	} else {
		if err := p.Unmarshal(data, &h); err != nil {
			t.Fatalf("contiguous: %v", err)
		}
	}
	return ids, vs
}

func TestFeedStreamNonLeafParity(t *testing.T) {
	data := feedStreamNestedJSON(12, 9)
	wantIDs, wantVs := feedStreamNestedRun(t, data, 0)
	for _, chunk := range feedChunkSizes {
		gotIDs, gotVs := feedStreamNestedRun(t, data, chunk)
		if len(gotIDs) != len(wantIDs) || len(gotVs) != len(wantVs) {
			t.Fatalf("chunk=%d: outer=%d inner=%d, want %d/%d", chunk, len(gotIDs), len(gotVs), len(wantIDs), len(wantVs))
		}
		for i := range wantIDs {
			if gotIDs[i] != wantIDs[i] {
				t.Fatalf("chunk=%d: ids[%d] = %q, want %q", chunk, i, gotIDs[i], wantIDs[i])
			}
		}
		for i := range wantVs {
			if gotVs[i] != wantVs[i] {
				t.Fatalf("chunk=%d: vs[%d] = %q, want %q", chunk, i, gotVs[i], wantVs[i])
			}
		}
	}
}

// A variant field whose discriminator and case sit on opposite sides of a
// long inner stream that spans many windows under the feed driver.
func TestFeedStreamDiscAcrossInner(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
	}{
		{"discBeforeInner", `{"d":"a","inner":[` + scopePolyInnerList(600) + `],"cased":{"tag":"t","n":1}}`},
		{"discAfterInner", `{"inner":[` + scopePolyInnerList(600) + `],"d":"a","cased":{"tag":"t","n":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := `{"items":[` + tc.json + `]}`
			run := func(chunk int) scopePolyCase {
				p, err := NewParser[scopePolyHost]()
				if err != nil {
					t.Fatalf("NewParser: %v", err)
				}
				var h scopePolyHost
				var got scopePolyCase
				h.Items.OnRead(func(s stream.Scope[scopePolyElem]) error {
					for it := range s.Iter() {
						it.Target().Inner.OnRead(func(is stream.Scope[scopePolyInner]) error {
							for iit := range is.Iter() {
								if err := iit.Decode(); err != nil {
									return err
								}
							}
							return nil
						})
						if err := it.Decode(); err != nil {
							return err
						}
						got, _ = it.Target().Cased.(scopePolyCase)
					}
					return nil
				})
				if chunk > 0 {
					if err := p.UnmarshalFeed(&chunkReader{data: []byte(doc), chunk: chunk}, &h); err != nil {
						t.Fatalf("feed chunk=%d: %v", chunk, err)
					}
				} else {
					if err := p.Unmarshal([]byte(doc), &h); err != nil {
						t.Fatalf("contiguous: %v", err)
					}
				}
				return got
			}
			want := run(0)
			if want.Tag != "t" || want.N != 1 {
				t.Fatalf("contiguous oracle case = %+v", want)
			}
			for _, chunk := range []int{1, 7, 31, 4096} {
				if got := run(chunk); got != want {
					t.Fatalf("chunk=%d: case = %+v, want %+v", chunk, got, want)
				}
			}
		})
	}
}

// Non-leaf elements settle per element, and a deferred RawMessage field after
// the inner stream stays source-backed until the final close settle: the drain
// must run against the window current at the settle, not the one captured at
// scope activation.
type feedStreamNestedDefElem struct {
	ID    string                               `json:"id"`
	Inner stream.Stream[feedStreamNestedInner] `json:"inner"`
	Raw   json.RawMessage                      `json:"raw"`
}

type feedStreamNestedDefHost struct {
	Items stream.Stream[feedStreamNestedDefElem] `json:"items"`
}

func TestFeedStreamNonLeafDeferred(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 16; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"n-%d","inner":[{"v":"%d"}],"raw":{"payload":[%d,"x",true]}}`, i, i, i)
	}
	b.WriteString(`]}`)
	data := []byte(b.String())

	run := func(chunk int) (ids []string, raws []string, vs []string) {
		p, err := NewParser[feedStreamNestedDefHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedStreamNestedDefHost
		h.Items.OnRead(func(s stream.Scope[feedStreamNestedDefElem]) error {
			for it := range s.Iter() {
				it.Target().Inner.OnRead(func(is stream.Scope[feedStreamNestedInner]) error {
					for iit := range is.Iter() {
						if err := iit.Decode(); err != nil {
							return err
						}
						vs = append(vs, iit.Target().V)
					}
					return nil
				})
				if err := it.Decode(); err != nil {
					return err
				}
				ids = append(ids, it.Target().ID)
				raws = append(raws, string(it.Target().Raw))
			}
			return nil
		})
		if chunk > 0 {
			if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h); err != nil {
				t.Fatalf("feed chunk=%d: %v", chunk, err)
			}
		} else {
			if err := p.Unmarshal(data, &h); err != nil {
				t.Fatalf("contiguous: %v", err)
			}
		}
		return ids, raws, vs
	}

	wantIDs, wantRaws, wantVs := run(0)
	for _, chunk := range feedChunkSizes {
		gotIDs, gotRaws, gotVs := run(chunk)
		if len(gotIDs) != len(wantIDs) || len(gotRaws) != len(wantRaws) || len(gotVs) != len(wantVs) {
			t.Fatalf("chunk=%d: got %d/%d/%d, want %d/%d/%d", chunk, len(gotIDs), len(gotRaws), len(gotVs),
				len(wantIDs), len(wantRaws), len(wantVs))
		}
		for i := range wantIDs {
			if gotIDs[i] != wantIDs[i] {
				t.Fatalf("chunk=%d: ids[%d] = %q, want %q", chunk, i, gotIDs[i], wantIDs[i])
			}
			if gotRaws[i] != wantRaws[i] {
				t.Fatalf("chunk=%d: raws[%d] = %q, want %q", chunk, i, gotRaws[i], wantRaws[i])
			}
			if gotVs[i] != wantVs[i] {
				t.Fatalf("chunk=%d: vs[%d] = %q, want %q", chunk, i, gotVs[i], wantVs[i])
			}
		}
	}
}

// The final batch's close settle is the one drain site with no preceding
// grow-check flush: its deferred records are source-backed against the last
// window. A stream longer than one window must drain them against that window,
// not the one captured at scope activation.
func TestFeedStreamDeferredFinalSettle(t *testing.T) {
	data := feedStreamRichJSON(600)
	want := feedStreamRichRun(t, data, 0)
	if len(want.raws) == 0 {
		t.Fatal("oracle held nothing")
	}
	for _, chunk := range []int{1000, 4096} {
		got := feedStreamRichRun(t, data, chunk)
		if len(got.raws) != len(want.raws) {
			t.Fatalf("chunk=%d: held %d, want %d", chunk, len(got.raws), len(want.raws))
		}
		for i := range want.raws {
			if got.raws[i] != want.raws[i] {
				t.Fatalf("chunk=%d: raws[%d] = %q, want %q", chunk, i, got.raws[i], want.raws[i])
			}
			if got.ts[i] != want.ts[i] {
				t.Fatalf("chunk=%d: ts[%d] = %q, want %q", chunk, i, got.ts[i], want.ts[i])
			}
		}
	}
}

var feedStreamInvalidDocs = []string{
	`{"items":[{"id":"a","n":1},{"id":"b","n":"str"}],"name":"s"}`,
	`{"items":[{"id":"a","n":1}`,
	`{"items":[{"id":"a",`,
	`{"items":[{"id":"a","n":1}`,
	`{"items":[1,2],"name":"s"}`,
	`{"items":[{"id":"a","n":1}],"name":5}`,
}

func TestFeedStreamErrorParity(t *testing.T) {
	for _, doc := range feedStreamInvalidDocs {
		data := []byte(doc)
		_, wantErr := feedStreamErrRun(t, data, 0)
		if wantErr == nil {
			t.Fatalf("contiguous parse of %q unexpectedly succeeded", doc)
		}
		wantKind, wantOff := feedErrKind(t, wantErr)
		for _, chunk := range feedChunkSizes {
			_, err := feedStreamErrRun(t, data, chunk)
			if err == nil {
				t.Fatalf("feed chunk=%d doc=%q: unexpectedly succeeded", chunk, doc)
			}
			kind, off := feedErrKind(t, err)
			if kind != wantKind {
				t.Fatalf("feed chunk=%d doc=%q: error kind %q, contiguous %q (%v vs %v)", chunk, doc, kind, wantKind, err, wantErr)
			}
			if off != wantOff {
				t.Fatalf("feed chunk=%d doc=%q: error offset %d, contiguous %d", chunk, doc, off, wantOff)
			}
		}
	}
}

func feedStreamErrRun(t *testing.T, data []byte, chunk int) (feedStreamHost, error) {
	t.Helper()
	p, err := NewParser[feedStreamHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var h feedStreamHost
	h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
		}
		return nil
	})
	if chunk > 0 {
		return h, p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h)
	}
	return h, p.Unmarshal(data, &h)
}

// A long stream through small chunks rotates the scoped views at nearly every
// batch and grows the fresh views at every window: without the batch-boundary
// truncation the retired string-provenance table (16 entries) exhausts, and
// without scoped views the arena accumulates the whole document. The caps
// stay within the per-window growth floor while the document is far larger.
func TestFeedStreamScopedBounded(t *testing.T) {
	const n = 2000
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"id-%d-%s","n":%d}`, i, strings.Repeat("x", 30), i)
	}
	b.WriteString(`]}`)
	data := []byte(b.String())

	p, err := NewParser[feedStreamHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var h feedStreamHost
	consumed := 0
	maxCap := 0
	h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			if c, _ := p.alloc.ArenaViewCaps(); c > maxCap {
				maxCap = c
			}
			consumed++
		}
		return nil
	})
	if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: 7}, &h); err != nil {
		t.Fatalf("UnmarshalFeed: %v", err)
	}
	if consumed != n {
		t.Fatalf("consumed %d, want %d", consumed, n)
	}
	if maxCap > 3*(4096+64)+2048 {
		t.Fatalf("scoped str view cap %d exceeds the window-scale bound for a %d-byte document", maxCap, len(data))
	}
	if maxCap > len(data)/2 {
		t.Fatalf("scoped str view cap %d tracks the %d-byte document instead of the window", maxCap, len(data))
	}
}

// Break and Skip fast-forward remaining elements across window edges under
// the feed driver's skip-resume machinery.
func TestFeedStreamBreakAndSkip(t *testing.T) {
	const n = 40
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"id-%d","n":%d}`, i, i)
	}
	b.WriteString(`]}`)
	data := []byte(b.String())

	for _, chunk := range []int{1, 7, 64} {
		p, err := NewParser[feedStreamHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedStreamHost
		seen := 0
		h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
			for it := range s.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				seen++
				if seen == 5 {
					break
				}
			}
			return nil
		})
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h); err != nil {
			t.Fatalf("break chunk=%d: %v", chunk, err)
		}
		if seen != 5 {
			t.Fatalf("break chunk=%d: seen %d, want 5", chunk, seen)
		}
	}
	for _, chunk := range []int{1, 7, 64} {
		p, err := NewParser[feedStreamHost]()
		if err != nil {
			t.Fatalf("NewParser: %v", err)
		}
		var h feedStreamHost
		decoded := 0
		h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
			for it := range s.Iter() {
				if err := it.Skip(); err != nil {
					return err
				}
				decoded++
			}
			return nil
		})
		if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: chunk}, &h); err != nil {
			t.Fatalf("skip chunk=%d: %v", chunk, err)
		}
		if decoded != 1 {
			t.Fatalf("skip chunk=%d: decoded %d, want 1 (Skip fast-forwards the array)", chunk, decoded)
		}
	}
}

// GC stress across window rotations and arena growth: held elements and
// Values must stay valid while earlier generations retire and release.
func TestFeedStreamGCStress(t *testing.T) {
	const n = 1200
	data := feedStreamRichJSON(n)

	p, err := NewParser[feedStreamRichHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	var h feedStreamRichHost
	var held []*feedStreamRich
	consumed := 0
	h.Items.OnRead(func(s stream.Scope[feedStreamRich]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			if consumed%100 == 0 {
				held = append(held, it.Target())
			}
			consumed++
			if consumed%200 == 0 {
				runtime.GC()
			}
		}
		return nil
	})
	if err := p.UnmarshalFeed(&chunkReader{data: data, chunk: 11}, &h); err != nil {
		t.Fatalf("UnmarshalFeed: %v", err)
	}
	if consumed != n {
		t.Fatalf("consumed %d, want %d", consumed, n)
	}
	runtime.GC()
	for k, e := range held {
		i := k * 100
		if want := fmt.Sprintf("id-%d", i); e.ID != want {
			t.Fatalf("held[%d].ID = %q, want %q", k, e.ID, want)
		}
		if want := fmt.Sprintf("q\"uote\\slash %d 日本", i); e.S != want {
			t.Fatalf("held[%d].S = %q, want %q", k, e.S, want)
		}
		kvVal := e.V.Get("k")
		kv, ok := kvVal.Str()
		if !ok || kv != fmt.Sprintf("v-%d", i) {
			t.Fatalf("held[%d].V.k = %q,%v, want %q", k, kv, ok, fmt.Sprintf("v-%d", i))
		}
	}
}

// The Decoder serves multiple values whose streams bind through nested
// driveBind re-entry under the window-aware engine.
func TestDecoderStreamValues(t *testing.T) {
	data := []byte(
		`{"items":[{"id":"a1","n":1},{"id":"a2","n":2}],"name":"a"} ` +
			`{"items":[{"id":"b1","n":3}],"name":"b"} ` +
			`{"name":"c"}`)
	for _, chunk := range []int{1, 7, 64} {
		d := NewDecoder(&chunkReader{data: data, chunk: chunk})
		type result struct {
			name string
			ids  []string
		}
		var got []result
		for {
			var h feedStreamHost
			var ids []string
			h.Items.OnRead(func(s stream.Scope[feedStreamElem]) error {
				for it := range s.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
					ids = append(ids, it.Target().ID)
				}
				return nil
			})
			err := d.Decode(&h)
			if err == nil {
				got = append(got, result{name: h.Name, ids: ids})
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		want := []result{
			{name: "a", ids: []string{"a1", "a2"}},
			{name: "b", ids: []string{"b1"}},
			{name: "c"},
		}
		if len(got) != len(want) {
			t.Fatalf("chunk=%d: values %d, want %d", chunk, len(got), len(want))
		}
		for i := range want {
			if got[i].name != want[i].name {
				t.Fatalf("chunk=%d: value %d name %q, want %q", chunk, i, got[i].name, want[i].name)
			}
			if len(got[i].ids) != len(want[i].ids) {
				t.Fatalf("chunk=%d: value %d ids %v, want %v", chunk, i, got[i].ids, want[i].ids)
			}
			for j := range want[i].ids {
				if got[i].ids[j] != want[i].ids[j] {
					t.Fatalf("chunk=%d: value %d ids %v, want %v", chunk, i, got[i].ids, want[i].ids)
				}
			}
		}
	}
}

// A root Stream binds through the Decoder: the array closes with the next
// value's first structural surviving in the window.
func TestDecoderRootStream(t *testing.T) {
	data := []byte(`[{"id":"e1","n":1},{"id":"e2","n":2}] [{"id":"f1","n":3}]`)
	for _, chunk := range []int{1, 7, 64} {
		d := NewDecoder(&chunkReader{data: data, chunk: chunk})
		var total [][]string
		for {
			var s stream.Stream[feedStreamElem]
			var ids []string
			s.OnRead(func(sc stream.Scope[feedStreamElem]) error {
				for it := range sc.Iter() {
					if err := it.Decode(); err != nil {
						return err
					}
					ids = append(ids, it.Target().ID)
				}
				return nil
			})
			err := d.Decode(&s)
			if err == nil {
				total = append(total, ids)
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if len(total) != 2 || len(total[0]) != 2 || len(total[1]) != 1 ||
			total[0][0] != "e1" || total[0][1] != "e2" || total[1][0] != "f1" {
			t.Fatalf("chunk=%d: got %v", chunk, total)
		}
	}
}
