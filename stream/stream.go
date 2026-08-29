// Package stream provides field-local streaming JSON binding.
//
// A stream.Stream[T] field on a struct activates an in-place handler when the
// decoder reaches the field's JSON array. The handler iterates Item[T] values
// and decides per element whether to bind (Item.Decode), skip (Item.Skip), or
// configure nested streams via the pre-bind Target pointer.
//
// The decoder drives element production through a single unified main loop
// (driveBind in decode/bind); Item.Decode calls back into that loop to bind a
// non-leaf element's body, recursing through nested stream handlers. The
// handler never touches tokens, delimiters, or parser depth. See
// examples/unmarshal/stream/README.md for the full design.
package stream

import (
	"reflect"
	"unsafe"
)

// Stream is the field-local streaming binding marker and handler registration
// point. Embed it as a struct field tagged with the JSON key; the decoder
// activates the registered OnRead handler when the field's array is reached in
// the input.
//
// A Stream with no registered handler is skipped by the decoder.
//
// Layout: the first 24 bytes carry a slice header (data/len/cap) that the
// native binder drives like an ordinary slice backing, so stream element
// storage reuses the same SlotClass machinery as []T. The handler is stored
// after the header so the native path never sees it.
type Stream[T any] struct {
	// sliceData / sliceLen / sliceCap form a slice header at offset 0..23.
	// The native binder reads and writes them exactly like a []T backing
	// when advancing elements; the Go-side driver also reads element pointers
	// from sliceData. Keep this layout in sync with bind.h's slice handling.
	sliceData unsafe.Pointer // off 0
	sliceLen  uintptr        // off 8
	sliceCap  uintptr        // off 16

	// onReadHandle is the user-registered handler. It is invoked once per
	// activation of the stream field, with a fresh Scope that yields the
	// array's elements as Item[T] values.
	onReadHandle func(Scope[T]) error // off 24
}

// OnRead registers the handler invoked when the decoder activates this stream
// field. OnRead is a parse-time configuration call, not a data operation:
//
//   - It must be called before Decode starts (typically at field initialization
//     time, before the owning struct's Value is bound).
//   - A specific Stream instance accepts exactly one handler. Calling OnRead
//     twice on the same instance is a configuration error recorded by the
//     parse session and returned by Decode; the second registration does not
//     overwrite the first.
func (s *Stream[T]) OnRead(handle func(Scope[T]) error) {
	s.onReadHandle = handle
}

// ElemType returns the reflect.Type of the stream's element type T. The
// decoder's type-tree builder calls this through reflect to discover T without
// needing public reflect support for generic type parameters. Not intended for
// application code.
func (s *Stream[T]) ElemType() reflect.Type {
	return reflect.TypeFor[T]()
}

// Activate invokes the registered handler with a Scope backed by driver and
// seeded with the initial batch view (batchData[0:batchLen]). Called by the
// decode/bind driver through reflect when the native binder yields on a stream
// slice: batchData/batchLen describe the elements already claimed/bound into
// the current slice block (per-element unbound for non-leaf streams, cap-full
// bound for leaf streams, empty for an array close on an empty stream).
//
// reason is the stop that produced this seeding batch. Anything other than a
// per-element or cap-full stop means the batch handed in is the last one, so
// iteration must end after it rather than drive native past the array.
//
// Activate constructs the Scope (reading ElemHasStream from the driver to
// select the per-element vs leaf advance strategy) and runs the handler to
// completion. It returns the handler's error, which may be a BreakSignal the
// driver propagates across scopes. A parse error hit while advancing batches
// takes precedence: Iter cannot report it, so the Scope stashes it and it
// surfaces here.
//
// Activate is not part of the user-facing API.
func (s *Stream[T]) Activate(driver ScopeDriver, batchData unsafe.Pointer, batchLen int, reason StopReason) error {
	if s.onReadHandle == nil {
		return nil
	}
	var zero T
	sc := &scope[T]{
		driver:        driver,
		streamAddr:    unsafe.Pointer(s),
		elemSize:      unsafe.Sizeof(zero),
		elemHasStream: driver.ElemHasStream(),
		batchData:     batchData,
		batchLen:      batchLen,
		atEnd:         reason != StopElement && reason != StopBatch,
	}
	err := s.onReadHandle(sc)
	if sc.err != nil {
		return sc.err
	}
	return err
}
