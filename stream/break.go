package stream

import "unsafe"

// BreakSignal is the control-flow token returned by Scope.Break. It is
// exported so the decode/bind driver can type-assert on it to detect a
// handler-driven break that must drain the current array and propagate the
// signal to the target scope's IsBreak check.
//
// The signal is opaque to user code: handlers must return it unmodified,
// intermediate Item.Decode calls must propagate it, and only the target
// scope's IsBreak recognizes it.
//
// streamAddr is the address of the Stream[T] field that owns the target
// scope. The driver uses it to route the signal across nested scopes:
// driver-internal stack entries are keyed by Stream field address (which
// equals the native binder's m.Yield.Target), so an inner handler that
// breaks an outer scope causes the signal to be stashed on the matching
// outer entry rather than the current top-of-stack.
type BreakSignal struct {
	// target is the *scope[T] that Break was called on, boxed as any so
	// BreakSignal itself stays non-generic. A nil target is never returned
	// by Break: Break always stashes the caller's own scope pointer.
	target any

	// streamAddr is the address of the Stream[T] field that owns the target
	// scope. The driver compares it against active stack entries' streamAddr
	// to route the signal to the correct scope's slot.
	streamAddr unsafe.Pointer
}

// Target returns the boxed *scope[T] pointer the signal is addressed to.
// Used by the decode/bind driver to propagate the signal through the
// outer Item.Decode return path without importing the stream package's
// generic types.
func (b *BreakSignal) Target() any { return b.target }

// StreamAddr returns the address of the Stream[T] field that owns the
// target scope. The driver uses this to find the matching active stream
// scope stack entry.
func (b *BreakSignal) StreamAddr() unsafe.Pointer { return b.streamAddr }

// Error implements the error interface so the signal can be returned through
// the handler's error channel. The message is intentionally generic: user
// code identifies the signal via Scope.IsBreak, not by inspecting the text.
func (b *BreakSignal) Error() string { return "stream: scope break" }
