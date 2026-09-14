package bind

import (
	"io"
	"testing"
)

// clobberReader delivers the document in fixed-size chunks like chunkReader,
// but overwrites the dead stack region before each read.
//
// The native binder runs on the goroutine stack and returns to Go to refill a
// window, so the C frame that held the deferred value's type is gone by the
// time the resumed raw scan writes the record. Whatever the Go driver leaves on
// that stack region decides the record's Kind byte, so the clobber turns a
// layout-dependent latent bug into a deterministic failure: without a stable
// type home the drain reports "unknown unmarshal record kind".
type clobberReader struct {
	data  []byte
	chunk int
	pos   int
}

// clobber writes non-zero bytes deep enough to cover the frames the native
// entry chain leaves behind.
//
//go:noinline
func clobber(depth int) byte {
	var pad [512]byte
	for i := range pad {
		pad[i] = 0xAB
	}
	if depth == 0 {
		return pad[0]
	}
	return pad[0] ^ clobber(depth-1)
}

var clobberSink byte

func (r *clobberReader) Read(p []byte) (int, error) {
	clobberSink ^= clobber(24)
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data)-r.pos {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	if r.pos >= len(r.data) {
		return n, io.EOF
	}
	return n, nil
}

// TestFeedDeferredStackClobber pins the invariant that a deferred record's type
// must live in storage that outlives an input yield. A root Unmarshaler takes
// the one dispatch path whose type is a register-live local, and its raw scan
// stops at every window edge, so each chunk size drives the record write
// through a resume.
func TestFeedDeferredStackClobber(t *testing.T) {
	doc := `{"u":{"a":[1,2,{"b":"c"}]},"i":1}`
	for _, chunk := range []int{1, 2, 3, 7, 16, 31, 32, 33, 63, 64, 65} {
		for rep := 0; rep < 200; rep++ {
			p, err := NewParser[feedUnmValue]()
			if err != nil {
				t.Fatalf("NewParser: %v", err)
			}
			feedUnmCalls = 0
			var out feedUnmValue
			err = p.UnmarshalFeed(&clobberReader{data: []byte(doc), chunk: chunk}, &out)
			if err != nil {
				t.Fatalf("chunk=%d rep=%d: %v", chunk, rep, err)
			}
			if feedUnmCalls != 1 {
				t.Fatalf("chunk=%d rep=%d: UnmarshalJSON called %d times, want 1", chunk, rep, feedUnmCalls)
			}
			if out.Payload != doc {
				t.Fatalf("chunk=%d rep=%d: payload=%q want %q", chunk, rep, out.Payload, doc)
			}
		}
	}
}
