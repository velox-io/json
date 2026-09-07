package stream

import (
	"errors"
	"runtime"
	"unsafe"
)

// Sink consumes the elements of one OnWrite activation. The encoder creates a
// Sink for the dynamic extent of the producer handler: Encode is called
// synchronously, and the encoder does not read the element again once Encode
// returns, so the producer may reuse one object slot across calls.
//
// A Sink is invalid outside the handler invocation that received it and must
// not be called reentrantly or from multiple goroutines; violations are state
// errors.
type Sink[T any] interface {
	// Encode submits one element. The first error wins: once an element
	// encode, the underlying writer, or a previous state error has failed,
	// later calls return the same error and produce no output.
	Encode(v *T) error
}

// Sink lifecycle state errors.
var (
	// ErrSinkClosed is returned by Encode when the Sink is used after its
	// OnWrite handler has returned.
	ErrSinkClosed = errors.New("stream: Sink used outside its OnWrite handler")
	// ErrSinkBusy is returned by Encode when it is called reentrantly, from
	// within an unfinished Encode of the same Sink.
	ErrSinkBusy = errors.New("stream: reentrant Sink.Encode call")
	// ErrSinkNilElement is returned by Encode when it is called with a nil
	// element pointer. To encode a JSON null for a pointer element type,
	// submit a non-nil pointer to a nil *U.
	ErrSinkNilElement = errors.New("stream: Sink.Encode called with nil element")
)

// errSinkBusy occupies the sink state word for the duration of one driver
// call. Its identity is distinct from the public ErrSinkBusy, so a driver
// failing with that sentinel latches as a genuine first error.
var errSinkBusy = errors.New("stream: Sink.Encode in flight")

// WriteDriver is the seam between the stream package's push model and the
// encoder: each Sink.Encode hands the element address to the driver, which
// owns the element's encode plan and the array framing around it.
//
// WriteDriver is not part of the user-facing API: the venc package installs a
// concrete implementation when activating an OnWrite producer.
type WriteDriver interface {
	// EncodeValue encodes one element at p synchronously. The driver does not
	// retain p after the call returns. The sink submits an element only while
	// the activation is error-free, so a failed call is always the last.
	EncodeValue(p unsafe.Pointer) error
}

// sink is the concrete Sink handed to an OnWrite handler. A single error
// word carries the whole lifecycle state; the driver owns the JSON framing.
type sink[T any] struct {
	driver WriteDriver

	// err is the sink's sole state word: nil while the handler may encode,
	// errSinkBusy for the duration of one driver call, the latched first
	// error once anything has failed, and ErrSinkClosed after the handler
	// has returned.
	err error
}

// ActivateWrite invokes the registered producer with a fresh Sink. It reports
// whether a producer was configured; an unconfigured stream is the caller's
// decision (configuration error, or omission under omitempty).
//
// The first error wins across element encodes, the writer, and the handler's
// own return value: a handler that ignores Sink.Encode errors and returns nil
// still fails the activation with the latched error. A handler panic
// propagates unchanged; the sink is closed either way.
//
// ActivateWrite is not part of the user-facing API.
func (s *Stream[T]) ActivateWrite(driver WriteDriver) (configured bool, err error) {
	if s.onWriteHandle == nil {
		return false, nil
	}
	sk := &sink[T]{driver: driver}
	defer func() { sk.err = ErrSinkClosed }()
	err = s.onWriteHandle(sk)
	// A recovered driver panic leaves the busy marker latched; the handler's
	// own return value then decides the activation.
	if sk.err != nil && sk.err != errSinkBusy {
		return true, sk.err
	}
	return true, err
}

func (sk *sink[T]) Encode(v *T) error {
	if sk.err != nil || v == nil {
		return sk.except()
	}
	sk.err = errSinkBusy
	err := sk.driver.EncodeValue(unsafe.Pointer(v))
	// The result assignment clears the busy marker and latches a failure.
	sk.err = err
	// The driver may keep the element address only in untyped form; the typed
	// parameter keeps it reachable for the whole encode.
	runtime.KeepAlive(v)
	return err
}

// except is the Encode cold path. The busy marker maps to ErrSinkBusy and a
// set state word returns the latched error; a nil state word means the
// element pointer was nil.
func (sk *sink[T]) except() error {
	if sk.err == errSinkBusy {
		return ErrSinkBusy
	}
	if sk.err != nil {
		return sk.err
	}
	sk.err = ErrSinkNilElement
	return sk.err
}
