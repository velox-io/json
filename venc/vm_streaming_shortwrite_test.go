package venc

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

// shortWriter accepts writes but only flushes the first halfN bytes of each
// call, returning (half, nil). It exercises io.Writer's short-write contract:
// n < len(p) with err == nil is legal, and the encoder must NOT drop the
// unwritten tail.
type shortWriter struct {
	mu   sync.Mutex
	out  []byte
	half int // bytes to actually keep per call (0 = keep none)
}

func (w *shortWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.half >= len(p) {
		w.out = append(w.out, p...)
		return len(p), nil
	}
	keep := max(w.half, 0)
	w.out = append(w.out, p[:keep]...)
	// Report a legal short write: kept `keep` bytes, no error.
	return keep, nil
}

// TestStreamingEncoder_ShortWriteNoDataLoss drives Encoder.Encode through a
// streaming path with a writer that short-writes every call. The output must
// be byte-identical to what a fully-buffered encoder produces; any dropped
// tail indicates flush() discarded unwritten bytes (and that vm_exec.go:137
// grew the buffer without preserving residual data).
func TestStreamingEncoder_ShortWriteNoDataLoss(t *testing.T) {
	// A payload large enough to force multiple BUF_FULL flushes (streamBuf is
	// 32 KiB; ~200 KiB of escaped content guarantees several flush rounds).
	big := bytes.Repeat([]byte("ab"), 100*1024) // 200 KiB
	v := map[string]any{
		"data": string(big),
	}

	// Reference: full buffered output (no streaming).
	wantBuf := &bytes.Buffer{}
	wantEnc := NewEncoder(wantBuf)
	if err := wantEnc.Encode(v); err != nil {
		t.Fatalf("reference Encode: %v", err)
	}
	want := wantBuf.Bytes()

	// Streaming into a writer that short-writes: keeps 1 byte per call.
	sw := &shortWriter{half: 1}
	gotEnc := NewEncoder(sw)
	if err := gotEnc.Encode(v); err != nil {
		t.Fatalf("short-write Encode: %v", err)
	}
	got := sw.out

	if !bytes.Equal(got, want) {
		t.Fatalf("short-write data loss: got %d bytes, want %d bytes\n  got tail: %q\n  want tail: %q",
			len(got), len(want), tail(got), tail(want))
	}
}

func tail(b []byte) []byte {
	if len(b) > 60 {
		return b[len(b)-60:]
	}
	return b
}

// TestStreamingEncoder_WriteErrorPropagates ensures that when the underlying
// writer returns a real error, Encode surfaces it (sticky) and stops.
func TestStreamingEncoder_WriteErrorPropagates(t *testing.T) {
	errSentinel := errors.New("boom")
	ew := &errWriter{err: errSentinel}
	enc := NewEncoder(ew)
	if err := enc.Encode(map[string]any{"k": "v"}); err == nil {
		t.Fatalf("expected error %v, got nil", errSentinel)
	} else if err != errSentinel {
		t.Fatalf("expected %v, got %v", errSentinel, err)
	}
}

type errWriter struct{ err error }

func (w *errWriter) Write(p []byte) (int, error) { return 0, w.err }
