package bind

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/stream"
)

// streamScopeEntry is one slot on the Parser streamScopes stack, keyed by
// streamAddr so an inner handler's BreakSignal can be routed to the matching
// (possibly outer) scope.
type streamScopeEntry struct {
	// streamAddr is the Stream[T] field address (== m.Yield.Target at scope
	// entry). Used to route BreakSignals to the matching scope.
	streamAddr unsafe.Pointer

	// elemHasStream is cached at push time so serveYield and ElemHasStream()
	// avoid re-deriving the type tree.
	elemHasStream bool

	// breakSig is stashed by an inner handler targeting this scope; nil
	// when no signal is pending.
	breakSig *stream.BreakSignal
}

// pushStreamScope appends a scope entry and returns its index for a
// deferred popStreamScope. LIFO defer ordering keeps the entry on top
// until the pop runs.
func (p *Parser) pushStreamScope(addr unsafe.Pointer, elemHasStream bool) int {
	p.streamScopes = append(p.streamScopes, streamScopeEntry{
		streamAddr:    addr,
		elemHasStream: elemHasStream,
	})
	return len(p.streamScopes) - 1
}

// popStreamScope removes the entry at idx. LIFO defer ordering from
// serveStreamBatch guarantees idx is top-of-stack, so the copy is
// defensive.
func (p *Parser) popStreamScope(idx int) {
	if idx < 0 || idx >= len(p.streamScopes) {
		return
	}
	copy(p.streamScopes[idx:], p.streamScopes[idx+1:])
	p.streamScopes = p.streamScopes[:len(p.streamScopes)-1]
}

// peekAnyScopeBreak returns the topmost stashed BreakSignal on the stack
// (any scope, not just self) without clearing it. The signal propagates
// up: each layer's driveBind returns without crossing a stream boundary,
// each layer's serveStreamBatch drains its own array, and the target
// scope's Item.Decode surfaces it via IsBreak.
func (p *Parser) peekAnyScopeBreak() *stream.BreakSignal {
	for i := len(p.streamScopes) - 1; i >= 0; i-- {
		if p.streamScopes[i].breakSig != nil {
			return p.streamScopes[i].breakSig
		}
	}
	return nil
}

// stashScopeBreak stashes sig on the entry whose streamAddr matches the
// signal's target, so the target (possibly outer) scope's Item.Decode
// surfaces it. Returns false if no matching entry exists.
func (p *Parser) stashScopeBreak(sig *stream.BreakSignal) bool {
	addr := sig.StreamAddr()
	for i := len(p.streamScopes) - 1; i >= 0; i-- {
		if p.streamScopes[i].streamAddr == addr {
			p.streamScopes[i].breakSig = sig
			return true
		}
	}
	return false
}

// streamSlotCheck is a test-only hook. CurrentBatch calls it so a test can pin
// the invariant that a non-leaf stream's element slot is the buffer base, which
// is what lets the driver derive the slot from the slice header instead of
// reading the noscan Core.CurAux. nil in normal builds.
var streamSlotCheck func(m *ndec.BindMachine, hdr *gort.SliceHeader, elemHasStream bool)

func assertStreamSlotAtBase(m *ndec.BindMachine, hdr *gort.SliceHeader, elemHasStream bool) {
	if streamSlotCheck != nil {
		streamSlotCheck(m, hdr, elemHasStream)
	}
}

// streamScopeDriver implements stream.ScopeDriver for one active stream scope.
// It owns everything that requires reading the native machine: the stop
// classification, the batch view, and the memory-release boundary. The stream
// package consumes the verdicts, so the machine fields have a single reader.
//
// Stateless beyond its identity fields: all iteration progress lives in the
// scope and the native machine.
type streamScopeDriver struct {
	p   *Parser
	m   *ndec.BindMachine
	src []byte

	// streamAddr identifies this scope's Stream[T] field, whose first 24 bytes
	// are the slice header native drives. A yield belongs to this scope only
	// when Yield.Target matches it; nested and outer streams carry their own.
	streamAddr unsafe.Pointer

	// elemHasStream selects the per-element (non-leaf) or batch (leaf) stop
	// phase. Cached from the type tree at scope entry.
	elemHasStream bool

	// retainMark is this scope's allocator retention floor, taken at scope
	// entry. SettleBatch drops everything staged above it.
	retainMark int
}

var _ stream.ScopeDriver = (*streamScopeDriver)(nil)

func (d *streamScopeDriver) DriveBind() (stream.StopReason, error) {
	if _, err := d.p.driveBind(d.m, d.src, d.stop); err != nil {
		return stream.StopNone, err
	}
	return d.reason(), nil
}

// stop is the driveBind halt predicate: halt as soon as the machine reaches a
// state this scope must act on.
func (d *streamScopeDriver) stop() bool {
	return d.reason() != stream.StopNone
}

// reason classifies the machine's current state for this scope. It is the only
// place the stream feature interprets Yield.PendingAction / Yield.Target /
// Core.Phase, and it serves as both the halt predicate and the post-halt
// verdict so the two cannot disagree.
func (d *streamScopeDriver) reason() stream.StopReason {
	if d.p.peekAnyScopeBreak() != nil {
		return stream.StopBreak
	}
	if d.m.Yield.PendingAction == ndec.BindYieldNone {
		return stream.StopDone
	}
	// Only the stream's own SliceGrow yields are stop points. Every other
	// yield (BlockFull, FlushMap, FlushUnmarshal, ...) must fall through to
	// serveYield: halting on an unserviced yield leaves native re-yielding the
	// same state forever.
	if d.m.Yield.PendingAction != ndec.BindYieldSliceGrow {
		return stream.StopNone
	}
	// A yield targeting a nested or outer stream is that scope's business;
	// serveYield routes it (recursing into serveStreamBatch for a nested one).
	if unsafe.Pointer(d.m.Yield.Target) != d.streamAddr {
		return stream.StopNone
	}
	switch d.m.Core.Phase {
	case ndec.BindPhaseArrayClose:
		return stream.StopClosed
	case ndec.BindPhaseArrayValueBegin:
		// Per-element yield; only non-leaf streams produce these.
		if d.elemHasStream {
			return stream.StopElement
		}
	case ndec.BindPhaseArrayValue:
		// Cap-full. For a non-leaf stream serveStreamSliceGrow services this
		// inline (reset and reuse slot 0), so it is not a stop point.
		if !d.elemHasStream {
			return stream.StopBatch
		}
	}
	return stream.StopNone
}

// CurrentBatch reads the batch view from the stream's slice header. For a
// non-leaf stream this is also the current element slot: its buffer holds one
// element and every element resets the write cursor to the buffer start, so the
// slot is the buffer base. Deriving it from the slice header rather than
// Core.CurAux keeps the pointer GC-visible, since Core.CurAux is a noscan
// uintptr by design.
func (d *streamScopeDriver) CurrentBatch() (unsafe.Pointer, int) {
	hdr := (*gort.SliceHeader)(d.streamAddr)
	assertStreamSlotAtBase(d.m, hdr, d.elemHasStream)
	return hdr.Data, hdr.Len
}

func (d *streamScopeDriver) ElemHasStream() bool {
	return d.elemHasStream
}

func (d *streamScopeDriver) PeekAnyBreak() *stream.BreakSignal {
	return d.p.peekAnyScopeBreak()
}

// SettleBatch publishes deferred fields and map entries before the handler reads
// them, then releases scoped retention. Noscan staging requires GC-visible
// retained roots through the drains.
func (d *streamScopeDriver) SettleBatch() error {
	if d.m.Alloc.DeferredDrainUsed > 0 {
		if err := drainDeferredRecords(d.p, d.m, d.src); err != nil {
			return err
		}
	}
	if d.m.Alloc.MapBufUsed > 0 {
		if err := drainAllMapSlots(d.m); err != nil {
			return err
		}
	}
	d.p.alloc.ReleaseScoped(d.retainMark)
	return nil
}

// GrowBatch prepares the next leaf batch. The stream SlotClass has no
// bump/EWMA state: the backing is a fixed buffer (sc.Cap = batch size).
// Under reuse the current backing is reset in place; otherwise a fresh
// equal-length backing is allocated and the old one stays GC-reachable via
// the handler's *T pointers.
func (d *streamScopeDriver) GrowBatch(reuse bool) error {
	sc, hdr := d.p.slotForGrow(d.m)
	if !reuse {
		hdr.Data = gort.UnsafeNewArray(sc.RType, int(sc.Cap))
		hdr.Cap = int(sc.Cap)
	}
	hdr.Len = 0
	d.m.Core.CurCount = 0
	d.p.finishGrow(d.m, sc, hdr)
	return nil
}

// DrainRemaining sets BIND_FLAG_STREAM_SKIP on CurType so native
// fast-forwards the remaining elements via safe_skip_value. Does not
// re-enter native: the drain happens on the caller's next BindParseRun.
// bind_pop clears the flag when the stream array closes.
func (d *streamScopeDriver) DrainRemaining() error {
	d.m.Core.CurType.SetStreamSkip()
	return nil
}

// drainStop is the halt predicate for the post-handler drain: reach this
// scope's own next SliceGrow yield, crossing any nested stream closes left
// pending. Unlike reason() it ignores a stashed break, because halting on a
// signal would leave CurType on an inner stream and SetStreamSkip would then
// target the wrong scope. Nested serveStreamBatch re-entries drain themselves
// via their own entry break check.
func (d *streamScopeDriver) drainStop() bool {
	if d.m.Yield.PendingAction != ndec.BindYieldSliceGrow {
		return false
	}
	if unsafe.Pointer(d.m.Yield.Target) != d.streamAddr {
		return false
	}
	phase := d.m.Core.Phase
	if d.elemHasStream {
		return phase == ndec.BindPhaseArrayValueBegin || phase == ndec.BindPhaseArrayClose
	}
	return phase == ndec.BindPhaseArrayValue || phase == ndec.BindPhaseArrayClose
}

// serveStreamSliceGrow handles BindYieldSliceGrow for a stream slice.
// Self-scope cap-full (top of streamScopes matches Yield.Target): native
// filled the single-slot non-leaf buffer, so reset cur_count and the write
// pointer to reuse slot 0 for the next element. This path is non-leaf only;
// leaf cap-full is caught by the stream's stop predicate before reaching
// serveYield. Otherwise activate the stream via serveStreamBatch.
func (p *Parser) serveStreamSliceGrow(m *ndec.BindMachine, src []byte) error {
	if len(p.streamScopes) > 0 &&
		p.streamScopes[len(p.streamScopes)-1].streamAddr == unsafe.Pointer(m.Yield.Target) {
		sc, hdr := p.slotForGrow(m)
		hdr.Len = 0
		m.Core.CurCount = 0
		p.finishGrow(m, sc, hdr)
		return nil
	}
	return p.serveStreamBatch(m, src)
}

// serveStreamBatch is the stream activation entry point, called from
// serveStreamSliceGrow when BindYieldSliceGrow lands on a stream slice with
// no active driver on that exact stream (self-scope cap-full is handled by
// serveStreamSliceGrow). It activates OnRead with the initial batch view;
// the handler iterates via Scope.Iter / Item.Decode, which drive driveBind
// to bind bodies.
func (p *Parser) serveStreamBatch(m *ndec.BindMachine, src []byte) error {
	typeIdx := m.Yield.Arg0
	tree := p.alloc.Tree

	hdr := (*gort.SliceHeader)(unsafe.Pointer(m.Yield.Target))
	streamAddr := unsafe.Pointer(m.Yield.Target)
	elemHasStream := tree.Types[typeIdx].HasElemHasStream()

	// Native shrinks hdr.Cap to cur_count at array_close, so a backing reused
	// across activations (non-leaf slot 0, or a pooled Stream field) would
	// carry a stale shrunken cap and cap-full-loop next time. Clearing Data
	// forces the next activation back through the force-yield path; the
	// handler's *T pointers keep the old backing GC-reachable.
	defer func() { hdr.Data = nil }()

	driver := &streamScopeDriver{
		p:             p,
		m:             m,
		src:           src,
		streamAddr:    streamAddr,
		elemHasStream: elemHasStream,
		retainMark:    p.alloc.RetainMark(),
	}

	scopeIdx := p.pushStreamScope(streamAddr, elemHasStream)
	defer p.popStreamScope(scopeIdx)

	// Settle on the way out as well as per batch. Per-batch settling bounds one
	// scope's cost; this bounds a sequence of them. A document holding many
	// sibling streams (a []Host each with its own Stream field) activates a
	// scope per host, and each would otherwise leave its last batch's retention
	// behind for the parse to accumulate.
	defer func() { _ = driver.SettleBatch() }()

	// Cross-scope break pending: skip Activate and drain this array to close
	// ']' so native pops to the parent. The signal stays stashed and
	// propagates up as each layer returns.
	if p.peekAnyScopeBreak() != nil {
		m.Core.CurType.SetStreamSkip()
		return nil
	}

	// First activation starts with an empty stream backing, so array begin emits
	// BindYieldSliceGrow with hdr.Data nil. Allocate the
	// fixed-cap backing (leaf = batch size, non-leaf = 1) and drive to fill
	// the first batch (leaf) or reach the first element (non-leaf) before
	// Activate.
	reason := driver.reason()
	if hdr.Data == nil {
		sc, _ := p.slotForGrow(m)
		hdr.Data = gort.UnsafeNewArray(sc.RType, int(sc.Cap))
		hdr.Cap = int(sc.Cap)
		hdr.Len = 0
		m.Core.CurCount = 0
		p.finishGrow(m, sc, hdr)
		var err error
		if reason, err = driver.DriveBind(); err != nil {
			return err
		}
	}

	// Settle the seeding batch for the same reason every later batch is settled
	// before the handler sees it: a leaf batch is already bound here, and its
	// staged map and deferred values have to land before OnRead reads them. A
	// non-leaf stop has no bound body yet, so there is nothing to settle and
	// Item.Decode settles it instead.
	if reason != stream.StopElement {
		if err := driver.SettleBatch(); err != nil {
			return err
		}
	}

	// Initial batch view handed to Activate. A non-leaf stop means native
	// claimed the element's slot with its body unbound, so the handler gets a
	// one-element batch to Target() and register nested OnRead on. Otherwise
	// (cap-full or close) the slice header carries the bound elements.
	batchData, batchLen := driver.CurrentBatch()
	if reason == stream.StopElement {
		batchLen = 1
	}

	// Activate via reflect. Stream[T] lives at m.Yield.Target: its first 24
	// bytes are the slice header native drives, so the field address keys
	// both the native slice cursor and the scope stack entry BreakSignals
	// route to.
	streamType := tree.ReflectTypes[typeIdx]
	streamPtr := reflect.NewAt(streamType, streamAddr)
	results := streamPtr.MethodByName("Activate").Call([]reflect.Value{
		reflect.ValueOf(stream.ScopeDriver(driver)),
		reflect.ValueOf(batchData),
		reflect.ValueOf(batchLen),
		reflect.ValueOf(reason),
	})
	err, _ := results[0].Interface().(error)

	// Break: drain this array and stash the signal on the matching entry;
	// the target scope's Item.Decode surfaces it via PeekAnyBreak.
	if sig, ok := err.(*stream.BreakSignal); ok {
		m.Core.CurType.SetStreamSkip()
		if !p.stashScopeBreak(sig) {
			return sig
		}
		return nil
	}
	if err != nil {
		return err
	}

	// Handler returned nil. If native is not at this scope's array close,
	// the handler exited early (Go break): drain to close ']' so native
	// pops. Phase may be ARRAY_CLOSE from a nested stream's close
	// (Yield.Target == inner stream), so only treat this scope as closed
	// when the close yield targets this stream.
	usersClosed := m.Core.Phase == ndec.BindPhaseArrayClose &&
		unsafe.Pointer(m.Yield.Target) == streamAddr
	if !usersClosed {
		// Drain to the next self-scope yield, crossing any nested stream
		// closes left pending. The break check is intentionally absent:
		// stopping at a signal would leave CurType on an inner stream and
		// SetStreamSkip would target the wrong scope. Nested
		// serveStreamBatch re-entries drain themselves via their own
		// peekAnyScopeBreak entry check.
		if _, err := p.driveBind(m, src, driver.drainStop); err != nil {
			return err
		}
		m.Core.CurType.SetStreamSkip()
		// At a cap-full yield (ARRAY_VALUE), native runs
		// BIND_SLICE_GROW_CHECK before STREAM_SKIP; reset CurCount so
		// cur_count < cap and the skip check runs.
		if m.Core.Phase == ndec.BindPhaseArrayValue {
			m.Core.CurCount = 0
		}
	}
	return nil
}
