package vjson

import "github.com/velox-io/json/stream"

// Stream is the field-local streaming binding marker. Declare it as a named
// struct field tagged with the JSON key; the decoder activates the registered
// OnRead handler when the field's array is reached in the input, and the
// encoder activates the registered OnWrite handler when the field is written.
//
// Reading (Unmarshal): the native binder walks the JSON tree depth-first and
// yields each filled batch of elements to the handler instead of accumulating
// the whole array. Stream[T] is the JSON DFS traversal made explicit, not a
// byte-stream abstraction: the input is still a single []byte consumed in
// full before Unmarshal returns.
//
//	type Response struct {
//		Events  vjson.Stream[Event] `json:"events"`
//		Message string              `json:"message"`
//	}
//
//	var resp Response
//	resp.Events.OnRead(func(s stream.Scope[Event]) error {
//		for item := range s.Iter() {
//			if err := item.Decode(); err != nil {
//				return err
//			}
//			// item.Target() returns the bound element.
//		}
//		return nil
//	})
//	vjson.Unmarshal(data, &resp)
//
// Writing (Marshal and Encoder): the producer registered with OnWrite pushes
// elements into a stream.Sink one at a time while the encoder owns the array
// framing, so neither side materializes the whole []T. The producer runs
// exactly once per encode. An unregistered producer is a configuration error
// (the field is omitted when tagged omitempty); an empty producer encodes as
// []. With an Encoder, output is flushed to the writer at a low-water mark,
// so memory stays bounded regardless of the array length.
//
//	resp.Events.OnWrite(func(sink stream.Sink[Event]) error {
//		for _, e := range events {
//			if err := sink.Encode(&e); err != nil {
//				return err
//			}
//		}
//		return nil
//	})
//	vjson.Marshal(&resp)
//
// This is object-level streaming on both sides, not byte streaming: the
// output is still one JSON array, and a single oversized element is not split
// across writer boundaries.
type Stream[T any] = stream.Stream[T]
