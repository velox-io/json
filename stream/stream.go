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
// handler never touches tokens, delimiters, or parser depth.
//
// On the encode side, OnWrite registers a producer that pushes elements into
// a Sink[T] one at a time while the encoder writes the array framing around
// them. Read and write handlers are independent: registering one does not
// configure the other.
package stream

import (
	"reflect"
	"unsafe"
)

// Stream is the field-local streaming binding marker and handler registration
// point. Declare it as a named struct field tagged with the JSON key; the
// decoder activates the registered OnRead handler when the field's array is
// reached in the input, and the encoder activates the registered OnWrite
// handler when the field is encoded.
//
// A Stream with no registered OnRead handler is skipped by the decoder; a
// Stream with no registered OnWrite handler is a configuration error at encode
// time (omitted when the field is omitempty).
//
// Layout: the first 24 bytes carry a slice header (data/len/cap) that the
// native binder drives like an ordinary slice backing, so stream element
// storage reuses the same SlotClass machinery as []T. The handlers are stored
// after the header so the native path never sees them.
type Stream[T any] struct {
	// sliceData / sliceLen / sliceCap form a slice header at offset 0..23.
	// The native binder reads and writes them exactly like a []T backing
	// when advancing elements; the Go-side driver also reads element pointers
	// from sliceData. Keep this layout in sync with bind.h's slice handling.
	sliceData unsafe.Pointer // off 0
	sliceLen  uintptr        // off 8
	sliceCap  uintptr        // off 16

	// onReadHandle is the user-registered read handler. It is invoked once per
	// activation of the stream field, with a fresh Scope that yields the
	// array's elements as Item[T] values.
	onReadHandle func(Scope[T]) error // off 24

	// onWriteHandle is the user-registered producer. It is invoked once per
	// activation of the stream field at encode time, with a Sink that
	// consumes the produced elements.
	onWriteHandle func(Sink[T]) error // off 32
}

// OnRead registers the handler invoked when the decoder activates this stream
// field. OnRead is a parse-time configuration call, not a data operation:
//
//   - It must be called before Decode starts (typically at field initialization
//     time, before the owning struct's Value is bound).
//   - A later registration overwrites the earlier one; OnRead(nil) clears the
//     handler. Handlers must not be re-registered while a decode that can
//     reach this stream is in progress.
func (s *Stream[T]) OnRead(handle func(Scope[T]) error) {
	s.onReadHandle = handle
}

// OnWrite registers the producer invoked when the encoder activates this
// stream field. OnWrite is an encode-time configuration call, not a data
// operation:
//
//   - The producer runs once per activation: each time an encoder reaches this
//     stream, it invokes the handler, which pushes zero or more elements into
//     the Sink and returns.
//   - A later registration overwrites the earlier one; OnWrite(nil) clears the
//     producer. Producers must not be re-registered while an encode that can
//     reach this stream is in progress.
//   - The same Stream may be read by different encoders concurrently, but the
//     handler and any state it captures must be concurrency-safe by itself;
//     one encoder still cannot be used concurrently.
//
// A Stream encodes as a JSON array: an unregistered producer is a
// configuration error (the field is omitted when tagged omitempty), and an
// empty producer encodes as []. Use a nil *Stream[T] to encode null.
func (s *Stream[T]) OnWrite(handle func(Sink[T]) error) {
	s.onWriteHandle = handle
}

// ElemType returns the reflect.Type of the stream's element type T. The
// decoder's type-tree builder calls this through reflect to discover T without
// needing public reflect support for generic type parameters. Not intended for
// application code.
func (s *Stream[T]) ElemType() reflect.Type {
	return reflect.TypeFor[T]()
}
