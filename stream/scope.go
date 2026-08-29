package stream

import (
	"iter"
	"unsafe"
)

// StopReason names the native stop points a stream scope cares about. The
// driver owns the classification: it holds the machine state (yield action,
// resume phase, yield target) that distinguishes these cases, so the stream
// package consumes the verdict instead of re-deriving it from raw fields.
type StopReason uint8

const (
	// StopNone means native is not at a stop point for this scope. Yields for
	// other scopes and non-SliceGrow yields (BlockFull, FlushMap, ...) fall
	// through to the driver's own yield service.
	StopNone StopReason = iota

	// StopElement is a non-leaf per-element halt: native claimed the next
	// element's slot but has not bound its body, so the handler can register
	// nested OnRead on it before Item.Decode binds it.
	StopElement

	// StopBatch is a leaf cap-full halt: native filled the batch backing.
	StopBatch

	// StopClosed means this scope's JSON array reached its closing ']'. A
	// close after a cap-full still carries the final partial batch.
	StopClosed

	// StopBreak means some active scope (this one or an outer one) has a
	// stashed BreakSignal, so the drive halted before crossing a stream
	// boundary.
	StopBreak

	// StopDone means native finished the whole document.
	StopDone
)

// ScopeDriver is the seam between the stream package's per-item iteration model
// and the decode/bind unified main loop (driveBind). The driver backs a Scope:
// Item.Decode drives the main loop to bind an element body (recursively, since
// binding the body may yield on nested streams); nextBatch advances to the next
// batch or element slot.
//
// ScopeDriver is not part of the user-facing API: handlers obtain a Scope via
// OnRead and interact with it through Scope/Item methods. The decode/bind
// package assigns a concrete implementation when constructing a Scope.
type ScopeDriver interface {
	// DriveBind runs the unified main loop until it reaches a stop point for
	// this scope, and reports which one. The stop classification lives in the
	// driver, so the stream package never inspects machine fields.
	DriveBind() (StopReason, error)

	// CurrentBatch returns the stream slice header's (data, len). At
	// StopBatch and StopClosed the length is the number of bound elements; at
	// StopElement the length is meaningless (the single-slot buffer's body is
	// not yet bound) and the caller supplies a length of 1.
	CurrentBatch() (data unsafe.Pointer, length int)

	// ElemHasStream reports whether the stream is non-leaf: the element type
	// tree contains a Stream field, so native yields per-element before
	// binding each body. Read once at scope construction to select the Iter
	// advance strategy.
	ElemHasStream() bool

	// PeekAnyBreak returns a stashed BreakSignal targeting any active scope on
	// the streamScopes stack (self or outer), without consuming it. nil if
	// none. Used by stop(), Decode(), and nextBatch to short-circuit on a
	// cross-scope break without driving native across a stream boundary.
	PeekAnyBreak() *BreakSignal

	// GrowBatch prepares the next leaf batch. Under reuse the current backing
	// is reset in place (native overwrites it); otherwise a fresh equal-length
	// backing is allocated and the old one is released to GC (the handler's
	// *T pointers keep the previous batch reachable). The scope passes its
	// reuse flag (set by AllowValueReuse) so the driver need not reach back
	// into the stream package's scope state.
	GrowBatch(reuse bool) error

	// DrainRemaining marks the stream array for fast-forward: native skips the
	// remaining elements without binding their bodies. Used by Item.Skip, Iter
	// break, and the driver's own break handling.
	DrainRemaining() error

	// SettleBatch completes the elements native has just bound and releases the
	// decoder memory retained to bind them. It must be called after a batch (or
	// a single element's body) is bound and before the handler reads it.
	//
	// Completing them is not bookkeeping: some values are staged during parsing
	// and only reach their destination at a drain, so without this an element's
	// map or deferred field would still be empty when the handler read it. A
	// non-streaming decode gets that drain at the end of the parse, which for a
	// stream is far too late to be useful.
	//
	// The release is what keeps a long array's cost proportional to what the
	// handler holds rather than to the array's length. It drops retention, not
	// memory: an element the handler keeps holds its own nested allocations
	// reachable, so values stay valid per the Scope contract.
	SettleBatch() error
}

// Scope represents one activation of a stream field: the consumption scope for
// a single JSON array bound to a Stream[T]. A Scope is single-use: it yields
// the array's elements via Iter and carries the cross-scope control
// (Break/IsBreak).
//
// A Scope and every Item it produces are invalid outside the handler invocation
// that created them. They must not escape the handler.
type Scope[T any] interface {
	// Iter returns a one-shot iterator over the array's elements. Each
	// iteration yields one *Item[T] in pending state; the handler calls
	// Item.Decode to consume the bound element or Item.Skip to fast-forward the
	// rest of the array. Iter may be called at most once per Scope.
	Iter() iter.Seq[*Item[T]]

	// AllowValueReuse grants the decoder permission to reuse element storage
	// across batches: the next batch overwrites the same slice backing instead
	// of allocating a fresh one. Element pointers from the current batch are
	// invalid once the next batch is requested, so the handler must consume or
	// copy each value before advancing.
	//
	// Without this call (default), each batch gets a fresh equal-length backing
	// and the previous backing is released to the GC; element pointers stay
	// reachable as long as the handler holds them, so values remain stable
	// across batches without the decoder retaining anything for the whole Decode.
	//
	// Non-leaf streams (element type contains a Stream field) always reuse a
	// single-slot buffer per element regardless of this flag: each element
	// overwrites the previous slot, so element pointers are valid only until
	// the next Iter step.
	//
	// AllowValueReuse must be called before Iter.
	AllowValueReuse()

	// Break returns a control signal addressed to this scope. The handler
	// returns it from an inner scope to break out of the outer scope's
	// iteration. The target scope recognizes it via IsBreak and then executes
	// a native Go break.
	Break() error

	// IsBreak reports whether err is a Break signal addressed to this scope.
	// Handlers must call IsBreak on the target scope before treating err as a
	// normal error. Only signals addressed to this scope are recognized.
	IsBreak(err error) bool
}

// scope is the concrete Scope implementation. It is constructed by Stream.Activate
// when the decode/bind driver activates a stream field.
type scope[T any] struct {
	driver        ScopeDriver
	streamAddr    unsafe.Pointer
	elemSize      uintptr
	elemHasStream bool

	// current batch view. batchData points at the first element of the batch;
	// batchLen is the number of elements in it. cursor is the index of the
	// next item to yield.
	batchData unsafe.Pointer
	batchLen  int
	cursor    int

	// skipRemaining is set by Item.Skip. Iter observes it at the next loop
	// check and calls DrainRemaining to fast-forward the array, then exits.
	skipRemaining bool

	// atEnd records that the batch currently in view is the last one: native
	// reached this array's ']', finished the document, or a break is pending.
	// The final batch and the signal that ends iteration arrive together (a
	// close still carries the trailing partial batch), so the batch is served
	// first and the next advance ends iteration instead of driving native past
	// the array.
	atEnd bool

	// reuse, set by AllowValueReuse before Iter, selects GrowBatch's leaf
	// backing strategy: true overwrites the current backing (element pointers
	// invalidated on the next batch); false allocates a fresh equal-length
	// backing and releases the old one to the GC (pointers stay reachable via
	// the handler's *T). Non-leaf streams ignore this (always single-slot reuse).
	reuse bool

	// err captures the first parse/bind error the driver reported while
	// advancing batches. Iter cannot return it (an iter.Seq yields values, not
	// errors), so it is stashed here and Activate returns it after the handler
	// finishes. Without this the handler would see iteration end early and a
	// real parse error would be silently dropped.
	err error
}

func (s *scope[T]) Iter() iter.Seq[*Item[T]] {
	return func(yield func(*Item[T]) bool) {
		for {
			for s.cursor < s.batchLen {
				if s.skipRemaining {
					_ = s.driver.DrainRemaining()
					return
				}
				target := (*T)(unsafe.Add(s.batchData, uintptr(s.cursor)*s.elemSize))
				s.cursor++
				it := &Item[T]{
					target:  target,
					scope:   s,
					pending: true,
				}
				if !yield(it) {
					// Native break (user's `break`): drain so the outer object
					// can resume cleanly.
					_ = s.driver.DrainRemaining()
					return
				}
			}
			if s.skipRemaining {
				_ = s.driver.DrainRemaining()
				return
			}
			// Current batch exhausted; request the next one. For per-element
			// streams this reads the slot Decode() already advanced to; for
			// leaf streams this drives native to fill the next batch.
			data, length, done, err := s.nextBatch()
			if err != nil {
				// iter.Seq has no error channel, so stash it: Activate
				// returns it once the handler unwinds.
				s.err = err
				return
			}
			if done {
				return
			}
			s.batchData = data
			s.batchLen = length
			s.cursor = 0
		}
	}
}

// nextBatch advances to the next batch view. For per-element streams the next
// slot is the one Item.Decode's DriveBind already advanced to (no further native
// drive); for leaf streams a fresh block is grown and native drives to fill it,
// and the filled batch is settled before it is handed back.
func (s *scope[T]) nextBatch() (data unsafe.Pointer, length int, done bool, err error) {
	if sig := s.driver.PeekAnyBreak(); sig != nil {
		// A cross-scope break is pending: drain the rest of this array so
		// native reaches close ']' and pops to the parent, then signal done.
		_ = s.driver.DrainRemaining()
		return nil, 0, true, nil
	}
	if s.atEnd {
		return nil, 0, true, nil
	}
	if s.elemHasStream {
		// Per-element: Item.Decode bound the body, settled it, and advanced
		// native to the next element's slot. Hand that slot back.
		bd, _ := s.driver.CurrentBatch()
		return bd, 1, false, nil
	}
	// Leaf: grow a fresh block, then drive native to fill the next batch.
	// GrowBatch resets the element count so native binds from the new block
	// start instead of re-yielding on the old cap-full.
	if growErr := s.driver.GrowBatch(s.reuse); growErr != nil {
		return nil, 0, false, growErr
	}
	reason, err := s.driver.DriveBind()
	if err != nil {
		return nil, 0, false, err
	}
	if err := s.driver.SettleBatch(); err != nil {
		return nil, 0, false, err
	}
	switch reason {
	case StopBatch:
		bd, bl := s.driver.CurrentBatch()
		return bd, bl, false, nil
	case StopClosed:
		// The close still carries the final partial batch in the slice
		// header, so serve it and let the next advance end iteration. Only
		// finish immediately when the close is empty.
		s.atEnd = true
		bd, bl := s.driver.CurrentBatch()
		if bl > 0 {
			return bd, bl, false, nil
		}
		return nil, 0, true, nil
	default:
		// StopBreak / StopDone: no more elements for this scope.
		return nil, 0, true, nil
	}
}

func (s *scope[T]) AllowValueReuse() {
	s.reuse = true
}

func (s *scope[T]) Break() error {
	return &BreakSignal{target: s, streamAddr: s.streamAddr}
}

func (s *scope[T]) IsBreak(err error) bool {
	bs, ok := err.(*BreakSignal)
	return ok && bs.target == s
}
