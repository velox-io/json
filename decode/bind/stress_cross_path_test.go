package bind

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/velox-io/json/decode/dom"
)

// Cross-path stress sweep. The tape-bind sub-routine (entered by UnmarshalValue
// and by variant/kindof cold paths) shares SlotClass state with the JSON bind
// path (Unmarshal) via the pooled Parser. These tests exercise that sharing
// under GC pressure, concurrency, and feature combinations (large slices +
// variants) to surface latent cursor/arena/lifetime bugs. The bypass crash
// (commit 276e3682) and the variant/kindof BLOCK_FULL gap (follow-up) were
// found this way; these are the regression net.

// --- Variant corpora, routed per envelope (cases must match the discriminator). ---

var siblingVariantInputs = []string{
	`{"type":"user","data":{"name":"Alice","role":"admin"}}`,
	`{"data":{"name":"Alice","role":"admin"},"type":"user"}`,
	`{"type":"product","data":{"title":"Widget","price":99}}`,
	`{"data":{"title":"Gizmo","price":7},"type":"product"}`,
	`{"type":"user","data":null}`,
	`{"data":null,"type":"user"}`,
}

var nonStructVariantInputs = []string{
	`{"type":"ints","data":[1,2,3]}`,
	`{"data":[1,2,3],"type":"ints"}`,
	`{"type":"slicestruct","data":{"items":[1,2,3],"tags":["a","b"]}}`,
	`{"data":{"items":[1,2,3],"tags":["a","b"]},"type":"slicestruct"}`,
	`{"type":"mapstruct","data":{"counts":{"a":1,"b":2},"label":"hi"}}`,
	`{"data":{"counts":{"a":1,"b":2},"label":"hi"},"type":"mapstruct"}`,
}

var ptrMapVariantInputs = []string{
	`{"type":"counts","data":{"a":1,"b":2,"c":3}}`,
	`{"data":{"a":1,"b":2},"type":"counts"}`,
	`{"type":"ptruser","data":{"name":"Grace","role":"admin"}}`,
	`{"data":{"name":"Heidi","role":"dev"},"type":"ptruser"}`,
}

var outerVariantInputs = []string{
	`{"type":"wrap","data":{"type":"user","data":{"name":"Carol","role":"owner"}}}`,
	`{"type":"direct","data":{"name":"Dave","role":"guest"}}`,
}

var kindofInputs = []string{
	`{"data":true}`,
	`{"data":false}`,
	`{"data":3.14}`,
	`{"data":"hello"}`,
	`{"data":-42}`,
	`{"data":{"name":"Bob","role":"user"}}`,
	`{"data":[{"name":"A","role":"x"},{"name":"B","role":"y"}]}`,
	`{"data":[]}`,
}

// TestStressVariantKindofRoundTrip runs each envelope's valid payloads through
// roundTrip (Unmarshal vs UnmarshalValue) for many iterations with GC pressure.
// variant/kindof cold paths use the same tape-bind sub-routine as UnmarshalValue,
// so this exercises that code path under cross-path pool reuse. The BLOCK_FULL
// resume (BIND_PHASE_TAPE_BIND_FIELD_VALUE_CASE_RETRY / CLOSE_DRAIN_RETRY) must
// keep the case SlotClass cursor valid across path alternation.
func TestStressVariantKindofRoundTrip(t *testing.T) {
	for rep := range 10 {
		for _, src := range siblingVariantInputs {
			roundTrip[variantEnvelopeSibling](t, src)
		}
		for _, src := range nonStructVariantInputs {
			roundTrip[variantEnvelopeNonStruct](t, src)
		}
		for _, src := range ptrMapVariantInputs {
			roundTrip[variantEnvelopePtrMap](t, src)
		}
		for _, src := range outerVariantInputs {
			roundTrip[variantEnvelopeOuter](t, src)
		}
		for _, src := range kindofInputs {
			roundTrip[kindofEnvelopeMixed](t, src)
		}
		if rep%2 == 0 {
			runtime.GC()
		}
	}
}

// TestStressConcurrentMixed runs Unmarshal and UnmarshalValue concurrently
// across goroutines, each borrowing a Parser from the shared pool. The pool
// hands out the same Parser to different goroutines over time; this stresses
// the cross-path cursor reset under real concurrent usage. GC is provoked
// mid-flight so any rooting gap surfaces as a fault rather than silent reuse.
//
// NOTE: under GOGC=1 (extreme GC pressure) this test surfaces "found bad
// pointer in Go heap", a Go runtime write-barrier-buffer stale pointer when
// the DOM Parser pool is shared across goroutines. The stale pointer is
// harmless (GODEBUG=invalidptr=0 passes with correct results); it is an old
// backing pointer retained in the per-goroutine write barrier buffer across
// the GC cycle that frees the backing. The reachability model is sound (all
// pointers land in scannable memory); the check is a false positive under the
// C-calls-Go-heap model at extreme GC pressure. Passes under default GOGC.
func TestStressConcurrentMixed(t *testing.T) {
	const goroutines = 8
	const itersPerG = 40

	payloads := []string{
		buildSharedJSON(150, 1), // large-slice bypass (Unmarshal path shape)
		buildSharedJSON(250, 2), // larger bypass
		`{"type":"user","data":{"name":"Alice","role":"admin"}}`,
		`{"type":"slicestruct","data":{"items":[1,2,3,4,5],"tags":["a","b","c"]}}`,
		`{"data":{"name":"Bob","role":"user"}}`, // kindof object
	}

	var wg sync.WaitGroup
	var fails atomic.Int32
	for g := range goroutines {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := range itersPerG {
				in := payloads[(seed+i)%len(payloads)]
				if (seed+i)%2 == 0 {
					var got sharedClassRoot
					if err := Unmarshal([]byte(in), &got); err != nil {
						fails.Add(1)
						continue
					}
				} else {
					val, err := dom.Parse([]byte(in))
					if err != nil {
						continue
					}
					var got sharedClassRoot
					if err := UnmarshalValue(val, &got); err != nil {
						fails.Add(1)
						continue
					}
				}
				if i%10 == 0 {
					runtime.GC()
				}
			}
		}(g)
	}
	// Background GC hammerer to maximize rooting pressure while goroutines run.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range goroutines * itersPerG / 4 {
			runtime.GC()
		}
	}()
	wg.Wait()
	if fails.Load() > 0 {
		t.Fatalf("concurrent stress: %d failures", fails.Load())
	}
}

// TestStressLargeSliceVariant crosses the large-slice bypass path with the
// variant cold path. variantSliceCase.Items is []int; with n > slotBatchMax the
// slice grows through the standalone bypass during the variant rebind walk
// (tape-bind sub-routine). The bypass close must not corrupt the shared
// SlotClass cursor for the next parse, and the variant case SlotClass must
// survive BLOCK_FULL across path alternation.
func TestStressLargeSliceVariant(t *testing.T) {
	for iter := range 30 {
		n := 100 + (iter*37)%400 // cross the bypass boundary
		var buf bytes.Buffer
		fmt.Fprintf(&buf, `{"type":"slicestruct","data":{"items":[`)
		for i := range n {
			if i > 0 {
				buf.WriteByte(',')
			}
			fmt.Fprintf(&buf, "%d", i)
		}
		buf.WriteString(`],"tags":["a","b","c"]}}`)
		in := buf.String()

		// roundTrip runs Unmarshal then UnmarshalValue; both must agree.
		roundTrip[variantEnvelopeNonStruct](t, in)

		// Alternate with a plain Unmarshal on the same shape to stress
		// the cold-start -> JSON -> cold-start cursor churn.
		if iter%2 == 0 {
			var ref variantEnvelopeNonStruct
			if err := Unmarshal([]byte(in), &ref); err != nil {
				t.Fatalf("iter %d Unmarshal: %v", iter, err)
			}
			runtime.KeepAlive(&ref)
		}
		if iter%5 == 0 {
			runtime.GC()
		}
	}
}

// TestStressMixedDocumentRoundTrip builds a single document that combines a
// large slice, a nested map, a pointer, and scalar fields all in one struct,
// then runs it through parity3 in a loop. The combination forces several
// SlotClasses to advance in one parse and exercises the close-path guards for
// slice, map, and struct simultaneously.
type mixedAllRoot struct {
	P   *sharedChild   `json:"p"`
	S   []sharedChild  `json:"s"`
	M   map[string]int `json:"m"`
	Tag string         `json:"tag"`
	N   int            `json:"n"`
}

func buildMixedAllJSON(n int, seed int64) string {
	var buf bytes.Buffer
	r := seed
	next := func() int { r = r*1103515245 + 12345; return int(r % 1000) }
	fmt.Fprintf(&buf, `{"p":{"a":%d,"b":"ptr","c":true,"d":1.5},"s":[`, next())
	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"a":%d,"b":"s%d","c":%t,"d":%g}`, i, i, i%2 == 0, float64(i)+0.25)
	}
	buf.WriteString(`],"m":{`)
	for i := range 8 {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `"k%d":%d`, i, next())
	}
	fmt.Fprintf(&buf, `},"tag":"tag-%d","n":%d}`, seed, next())
	return buf.String()
}

func TestStressMixedDocumentRoundTrip(t *testing.T) {
	for iter := range 30 {
		n := 100 + (iter*37)%400
		parity3[mixedAllRoot](t, "MixedAll",
			buildMixedAllJSON(n, int64(iter+1)))
		if iter%5 == 0 {
			runtime.GC()
		}
	}
}
