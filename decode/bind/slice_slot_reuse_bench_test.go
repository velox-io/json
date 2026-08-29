package bind

import "testing"

// These pin the reason sealOpenSlices exists. array_begin charges a slice's whole
// borrowed tail up front, so a parse that dies mid-slice leaves Offset == Limit
// and the next borrow would have to install a fresh block. Reclaiming the unused
// part on the error path is what keeps a reject-heavy workload from allocating a
// block per request.
//
// The inputs stay small enough to fit the installed block, so no SLICE_GROW
// yield fires. A larger slice would allocate through ServeSliceGrow regardless
// and hide the effect being measured.
const (
	sealBenchBad  = `{"items":[{"a":"x","b":"y","c":1},{"a":"p"` + "\x00"
	sealBenchGood = `{"items":[{"a":"ok"}]}`
)

// BenchmarkRejectedParse is the validating-front-door shape: every input is
// rejected, on a pooled Parser.
func BenchmarkRejectedParse(b *testing.B) {
	p, err := NewParser[slotReuseRoot]()
	if err != nil {
		b.Fatal(err)
	}
	in := []byte(sealBenchBad)
	b.ReportAllocs()
	for b.Loop() {
		var sink slotReuseRoot
		if err := p.Unmarshal(in, &sink); err == nil {
			b.Fatal("expected error")
		}
	}
}

// BenchmarkRejectedThenAcceptedParse alternates the two, which is what actually
// shows whether a reclaimed tail is reused by the following parse.
func BenchmarkRejectedThenAcceptedParse(b *testing.B) {
	p, err := NewParser[slotReuseRoot]()
	if err != nil {
		b.Fatal(err)
	}
	bad, good := []byte(sealBenchBad), []byte(sealBenchGood)
	b.ReportAllocs()
	for b.Loop() {
		var s1 slotReuseRoot
		if err := p.Unmarshal(bad, &s1); err == nil {
			b.Fatal("expected error")
		}
		var s2 slotReuseRoot
		if err := p.Unmarshal(good, &s2); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAcceptedParse is the baseline the rejected case is compared against.
func BenchmarkAcceptedParse(b *testing.B) {
	p, err := NewParser[slotReuseRoot]()
	if err != nil {
		b.Fatal(err)
	}
	in := []byte(sealBenchGood)
	b.ReportAllocs()
	for b.Loop() {
		var sink slotReuseRoot
		if err := p.Unmarshal(in, &sink); err != nil {
			b.Fatal(err)
		}
	}
}
