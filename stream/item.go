package stream

// Item represents one JSON value's parse scope within a stream. Each item
// starts pending; the handler consumes it by calling exactly one of Decode or
// Skip. After that call the item is terminated and cannot drive further
// parsing.
//
// For leaf streams (no nested Stream field in the element type tree), the
// element body is already bound when the item is yielded: Decode is a no-op
// beyond consuming the item. For non-leaf streams, Decode drives the unified
// main loop (driveBind) to bind the element body, which recursively activates
// any nested stream handlers registered on the Target pointer.
//
// Target returns the element's destination pointer without advancing. Use it
// to configure nested streams before consuming the item, and to read the
// bound element after Decode returns.
type Item[T any] struct {
	// target is the element destination. For leaf streams it is already bound
	// when this item was yielded; for non-leaf streams Decode drives native to
	// bind it. Target exposes it pre-consume for nested-stream configuration.
	target *T

	// scope carries the owning scope, used by Decode to drive the main loop and
	// by Skip to flag fast-forward.
	scope *scope[T]

	// pending is true until Decode or Skip is called. A pending item at the
	// next Iter step is an unhandled-item error: the parser cannot advance to
	// the next batch without resolving the current one.
	pending bool
}

// Target returns the element destination pointer without advancing the parser.
// Use Target before Decode to register nested stream handlers on fields of the
// element struct. After Decode returns, the target points to the fully bound
// element. After Skip or a parse error, target contents have no availability
// guarantee.
func (it *Item[T]) Target() *T {
	return it.target
}

// Decode drives binding of the current element. For leaf streams the native
// binder already filled the target storage during the batch fill, so Decode
// only consumes the item. For non-leaf streams Decode drives the unified main
// loop (DriveBind) to bind the element body, which recursively activates any
// nested stream handlers registered on the Target pointer. The bound element
// is available via Target after Decode returns nil.
//
// If a BreakSignal is stashed on any active scope (produced by an inner
// handler that called outer.Break), Decode returns it unmodified. The handler
// must propagate it via IsBreak so the target scope recognizes it.
//
// Decode must be called at most once per item and cannot be combined with Skip.
// Calling Decode on a terminated item panics.
func (it *Item[T]) Decode() error {
	if !it.pending {
		panic("stream: Decode called on terminated item")
	}
	it.pending = false
	// Non-leaf: drive the main loop to bind this element's body. The drive
	// recurses on nested stream yields, so a nested Stream[T] field's OnRead
	// runs inside this call. It halts either at the next element's slot or at
	// the end of this scope's elements; the latter is recorded so Iter ends
	// after the current item instead of driving native past the array. Leaf:
	// body already bound during the batch fill, no drive needed.
	if it.scope.elemHasStream {
		reason, err := it.scope.driver.DriveBind()
		if err != nil {
			return err
		}
		if reason != StopElement {
			it.scope.atEnd = true
		}
		// Settle before returning: the body is bound, but values staged during
		// parsing (map entries, deferred fields) have not reached it yet, and
		// Target() is readable the moment this returns.
		if err := it.scope.driver.SettleBatch(); err != nil {
			return err
		}
	}
	// Surface a stashed BreakSignal from an inner stream handler. The signal
	// was produced inside the bind of this element (a nested stream field's
	// handler called outer.Break); it targets some active scope (possibly this
	// one, possibly an outer one). Returning it makes the handler's IsBreak
	// fire. The nil check is load-bearing: PeekAnyBreak returns the concrete
	// pointer type, and returning it unchecked would wrap a typed nil into a
	// non-nil error interface.
	sig := it.scope.driver.PeekAnyBreak()
	if sig == nil {
		return nil
	}
	return sig
}

// Skip signals that the handler does not want the rest of the array: the native
// binder fast-forwards the remaining elements without binding, and iteration
// ends after the current item. Skip does not consume the current element's
// bound value (it is already in the target) but the handler is signaling that
// no further items are needed.
//
// For per-element "I don't need this one but continue" semantics, simply don't
// read the target and continue the loop: the bound element is harmless if
// ignored.
//
// Skip must be called at most once per item and cannot be combined with Decode.
// Calling Skip on a terminated item panics.
func (it *Item[T]) Skip() error {
	if !it.pending {
		panic("stream: Skip called on terminated item")
	}
	it.pending = false
	it.scope.skipRemaining = true
	return nil
}
