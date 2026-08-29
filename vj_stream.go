package vjson

import "github.com/velox-io/json/stream"

// Stream is the field-local streaming binding marker. Embed it as a struct
// field tagged with the JSON key; the decoder activates the registered OnRead
// handler when the field's array is reached in the input.
//
// A Stream[T] field works with Unmarshal: as the native binder walks the JSON
// tree depth-first, it yields each filled batch of elements to the handler
// instead of accumulating the whole array. Stream[T] is the JSON DFS traversal
// made explicit, not a byte-stream abstraction: the input is still a single
// []byte consumed in full before Unmarshal returns.
//
//	type Response struct {
//		Events  vjson.Stream[Event] `json:"events"`
//		Message string              `json:"message"`
//	}
//
//	var resp Response
//	resp.Events.OnRead(func(s stream.Scope[Event]) error {
//		for it := range s.Iter() {
//			e, err := it.Decode()
//			// ...
//		}
//		return nil
//	})
//	vjson.Unmarshal(data, &resp)
type Stream[T any] = stream.Stream[T]
