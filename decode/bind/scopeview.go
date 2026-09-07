package bind

import (
	"fmt"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// Scoped arena views for one stream scope. A scope whose element subtree can
// append str_arena bytes or tape words gets its own view pair sized by the
// view bound, the only driver-side bound on one native run's consumption
// (viewBounds). The pair is write-side scratch: native's unchecked bumps need
// the bound reserved ahead, and capacity rotation swaps an oversized pair for
// a fresh one. Publication is read-side and per batch: publish hands the doc
// an exact-fit snapshot of the tape words the batch used, so a retained item
// pins its batch's bytes while recycleTape lets the next batch overwrite the
// scratch backing from index zero. Strings carry no indirection a snapshot
// could redirect, so their backing stays block-pinned by the string headers
// items hold, the same block-granularity contract as slot backings.
type scopeViews struct {
	// Parent state restored at scope exit.
	parentStrView   []byte
	parentStrBase   *byte
	parentStrCap    uint64
	parentStrUsed   uint64
	parentGenStart  uint64
	parentTapeView  []uint64
	parentTapeBase  *uint64
	parentTapeCap   uint64
	parentTapeUsed  uint64
	parentTapeNeed  uint32
	parentValueTape *uint64
	parentValueDoc  unsafe.Pointer

	// typeIdx names the stream type; its TypePublishesValue bit gates doc
	// snapshots for this scope's element subtree.
	typeIdx uint32

	// doc publishes the current tape generation's Value output. Each
	// generation publishes one snapshot, so Values bound by earlier
	// generations keep resolving against their own copies.
	doc *valueabi.Doc

	// provFloor is the provenance table height at scope entry. Entries
	// recorded above it name retired backings of this scope's own
	// generations; a batch boundary and the scope exit truncate to the floor
	// (strprov.go documents the discipline).
	provFloor uint32

	hasStr  bool
	hasTape bool
}

// remainingSource reports the source bytes the binder has not consumed. The
// cursor slot at the last yield holds the next structural's source offset;
// the scan sentinel entries hold srcLen.
func remainingSource(m *ndec.BindMachine, srcLen int) int {
	off := int(*(*uint32)(m.CursorPair()[0]))
	if off > srcLen {
		off = srcLen
	}
	return srcLen - off
}

// scopeTapeBound mirrors the parse-level ceiling from unmarshalPadded over the
// remaining source: the split-tape surcharge is per dual-view site.
func scopeTapeBound(p *Parser, remaining int) int {
	ceiling := remaining
	if p.tt.HasSplitTape {
		if k := p.tt.SplitTapeSites; k != vbind.SplitTapeSitesUnbounded {
			ceiling = remaining + 2*k
		} else {
			ceiling = 2 * remaining
		}
	}
	return ceiling + 3
}

// viewBounds reports the source bounds sizing one generation of a scope's
// views. Under contiguous input the bound is the remaining document, so a
// view installed at scope entry and rotated at half-consumption keeps peak
// memory within a factor of two of the remaining input. Under the feed driver
// the document length is unknown; the window bounds one native run's
// consumption and the per-window growth in mountWindow guarantees the need, so
// the bound is the window and rotation resets a view whenever amortized
// growth has inflated it past twice that bound.
func (p *Parser) viewBounds(m *ndec.BindMachine) (str, words int) {
	if f := p.feed; f != nil {
		return f.n + vbind.StrArenaTail, 2*f.n + 64
	}
	remaining := remainingSource(m, len(p.src))
	return remaining + vbind.StrArenaTail, scopeTapeBound(p, remaining)
}

// installScopeViews installs a fresh view pair for one stream scope after
// draining records that carry parent-view str offsets. It returns nil when
// the stream's element subtree provably writes neither arena.
func (p *Parser) installScopeViews(m *ndec.BindMachine, typeIdx uint32) (*scopeViews, error) {
	tree := p.alloc.Tree
	hasStr := tree.TypeWritesStr[typeIdx]
	hasTape := tree.TypeWritesTape[typeIdx]
	if !hasStr && !hasTape {
		return nil, nil
	}
	// TextUnmarshaler records carry str_arena offsets; they must resolve
	// against the parent backing before the swap. Raw-backed records span a
	// window edge under the feed driver.
	if m.Alloc.DeferredDrainUsed > 0 {
		if err := drainDeferredRecords(p, m); err != nil {
			return nil, err
		}
	}
	_, words := p.viewBounds(m)
	if hasTape && words > maxSeamDistance {
		return nil, fmt.Errorf("vjson: input too large: a %d-word tape arena exceeds the %d-word seam distance limit",
			words, maxSeamDistance)
	}
	v := &scopeViews{
		parentStrView:   p.alloc.StrArena,
		parentStrBase:   m.Alloc.StrArena,
		parentStrCap:    m.Alloc.StrArenaCap,
		parentStrUsed:   m.Core.StrUsed,
		parentGenStart:  m.Alloc.StrGenStart,
		parentTapeView:  p.alloc.TapeArena,
		parentTapeBase:  m.Alloc.TapeArena,
		parentTapeCap:   m.Alloc.TapeArenaCap,
		parentTapeUsed:  m.Alloc.TapeUsed,
		parentTapeNeed:  m.Alloc.TapeNeed,
		parentValueTape: m.Alloc.ValueTape,
		parentValueDoc:  m.Alloc.ValueDoc,
		typeIdx:         typeIdx,
		provFloor:       m.StrProvCount(),
		hasStr:          hasStr,
		hasTape:         hasTape,
	}
	v.installFresh(p, m)
	v.freshDoc(p, m)
	return v, nil
}

// installFresh swaps in a new view pair sized by viewBounds, routing the
// backings through retained so the scoped release publishes native writes.
// StrGenStart and both cursors restart at zero on the fresh backings.
func (v *scopeViews) installFresh(p *Parser, m *ndec.BindMachine) {
	strNeed, words := p.viewBounds(m)
	if v.hasStr {
		p.alloc.InstallScopedStrView(strNeed)
		m.Alloc.StrArena = (*byte)(unsafe.SliceData(p.alloc.StrArena))
		m.Alloc.StrArenaCap = uint64(cap(p.alloc.StrArena))
		m.Alloc.StrGenStart = 0
		m.Core.StrUsed = 0
	}
	if v.hasTape {
		p.alloc.InstallScopedTapeView(words)
		m.Alloc.TapeArena = (*uint64)(unsafe.SliceData(p.alloc.TapeArena))
		m.Alloc.TapeArenaCap = uint64(cap(p.alloc.TapeArena))
		m.Alloc.TapeUsed = 0
		m.Alloc.TapeNeed = uint32(words)
		m.Alloc.ValueTape = (*uint64)(unsafe.SliceData(p.alloc.TapeArena))
	}
}

// freshDoc begins a tape coordinate generation: Values dispatched from now on
// carry this doc, and the generation's publish snapshots its words into it.
func (v *scopeViews) freshDoc(p *Parser, m *ndec.BindMachine) {
	if v.hasTape && p.alloc.Tree.TypePublishesValue[v.typeIdx] {
		v.doc = &valueabi.Doc{}
		m.Alloc.ValueDoc = unsafe.Pointer(v.doc)
	}
}

// publish fills the current generation's doc with what native encoded against
// the current views. The tape extent is a snapshot copy: published docs never
// alias the scratch backing, so the next generation may overwrite it in place
// and a retained item pins only the words its own batch used. The string and
// source extents stay views into the shared str generation, whose backing the
// string headers in items keep alive. Feed docs never borrow the source:
// docSrc is nil while windows relocate under the driver.
func (v *scopeViews) publish(p *Parser, m *ndec.BindMachine) {
	if v.doc == nil {
		return
	}
	v.doc.StrArena = p.alloc.StrArena[:m.Core.StrUsed]
	v.doc.Src = p.docSrc()
	if used := int(m.Alloc.TapeUsed); used > 0 {
		v.doc.Tape = snapshotTape(p.alloc.TapeArena, used)
	}
}

// snapshotTape copies the used tape span into an exact-fit, zero-free backing.
// Tape words carry no pointers, so a noscan allocation is safe and the copy
// overwrites every word.
func snapshotTape(arena []uint64, used int) []uint64 {
	raw := gort.MakeDirtyBytes(8*used, 8*used)
	tape := unsafe.Slice((*uint64)(unsafe.Pointer(unsafe.SliceData(raw))), used)
	copy(tape, arena[:used])
	return tape
}

// recycleTape ends the tape generation on the reused backing. publish holds a
// snapshot of the used words, so nothing references them and the next batch
// starts overwriting at index zero. A fresh doc begins the next generation's
// coordinates; a batch that wrote no tape words publishes none.
func (v *scopeViews) recycleTape(p *Parser, m *ndec.BindMachine) {
	if !v.hasTape {
		return
	}
	m.Alloc.TapeUsed = 0
	v.freshDoc(p, m)
}

// dropScopedProv truncates provenance entries this scope's own growths
// recorded (strprov.go documents the ownership discipline). It must run while
// those entries' retired backings are still retained, ahead of the release
// that drops them.
func (v *scopeViews) dropScopedProv(m *ndec.BindMachine) {
	m.TruncateStrProv(v.provFloor)
}

// closeGeneration publishes this batch's extents so its Values resolve the
// moment its handler reads them, recycles the tape generation onto the reused
// backing, and drops the closed generation's storage claims. The live floor
// advances past strings earlier elements bound, unless a rotation installs a
// fresh pair that resets it, and the provenance entries this scope recorded
// for retired backings stop being consultable. Elements of later generations
// rebind their own discriminators.
func (v *scopeViews) closeGeneration(p *Parser, m *ndec.BindMachine) {
	v.publish(p, m)
	v.recycleTape(p, m)
	if !v.maybeRotate(p, m) {
		m.Alloc.StrGenStart = m.Core.StrUsed
	}
	v.dropScopedProv(m)
}

// rotate installs a fresh pair for the next generation. It runs at batch
// boundaries, where no Value emission, merged tape, or deferred record of
// this scope level is in flight; closeGeneration has already published and
// recycled the outgoing generation's coordinates.
func (v *scopeViews) rotate(p *Parser, m *ndec.BindMachine) {
	v.installFresh(p, m)
}

// maybeRotate shrinks the pair when it holds at least twice the view bound.
// The threshold is one policy over two bound meanings (viewBounds): the
// remaining document under contiguous input, where halving marks real
// progress toward the end, and the window under the feed driver, where the
// amortized growth factor guarantees the threshold fires once per growth and
// the view resets to the window size. It reports whether a rotation happened,
// so the caller can skip the provenance floor advance that a fresh view
// already resets.
func (v *scopeViews) maybeRotate(p *Parser, m *ndec.BindMachine) bool {
	strNeed, wordsNeed := p.viewBounds(m)
	oversized := (v.hasStr && cap(p.alloc.StrArena) >= 2*strNeed) ||
		(v.hasTape && cap(p.alloc.TapeArena) >= 2*wordsNeed)
	if !oversized {
		return false
	}
	if v.hasTape && wordsNeed > maxSeamDistance {
		return false
	}
	v.rotate(p, m)
	return true
}

// restore publishes the final generation and reinstates the parent views.
// Provenance truncation already happened in the exit settle, before the
// scoped release dropped the retired backings.
func (v *scopeViews) restore(p *Parser, m *ndec.BindMachine) {
	if v == nil {
		return
	}
	v.publish(p, m)
	p.alloc.StrArena = v.parentStrView
	m.Alloc.StrArena = v.parentStrBase
	m.Alloc.StrArenaCap = v.parentStrCap
	m.Core.StrUsed = v.parentStrUsed
	m.Alloc.StrGenStart = v.parentGenStart
	p.alloc.TapeArena = v.parentTapeView
	m.Alloc.TapeArena = v.parentTapeBase
	m.Alloc.TapeArenaCap = v.parentTapeCap
	m.Alloc.TapeUsed = v.parentTapeUsed
	m.Alloc.TapeNeed = v.parentTapeNeed
	m.Alloc.ValueTape = v.parentValueTape
	m.Alloc.ValueDoc = v.parentValueDoc
}
