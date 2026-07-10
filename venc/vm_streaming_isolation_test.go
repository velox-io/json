package venc

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// TestStreamingEncoder_IsolatedFromMarshalErosion verifies the dual-buffer
// design: the streaming Encoder path uses a buffer isolated from Marshal's
// zero-copy erosion, so heavy Marshal traffic (which advances es.buf's base
// via es.buf[n:] and shrinks cap on the pooled object) does not leave the
// streaming Encoder with a too-small workBuf.
//
// Observable signal: a small streaming response must complete in very few
// Write calls (one final write plus at most a few flushes), not a storm of
// tiny writes. Without buffer isolation the pooled es.buf could be eroded
// below OP_INTERFACE's ~331-byte reservation, tripping BUF_FULL repeatedly.
func TestStreamingEncoder_IsolatedFromMarshalErosion(t *testing.T) {
	// 1) Heavily erode the marshal buffer via Marshal of a large map, which
	//    returns es.buf[:n:n] and advances the base, shrinking cap.
	big := strings.Repeat("x", 30000)
	for range 200 {
		if _, err := Marshal(map[string]string{"k": big}); err != nil {
			t.Fatalf("erode Marshal: %v", err)
		}
	}

	// 2) Stream a small response through the real Encoder.Encode path on the
	//    same pool, under concurrency to stress cross-goroutine reuse.
	var wg sync.WaitGroup
	const workers = 8
	const perWorker = 200
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range perWorker {
				var writes int
				cw := &countWriter{n: &writes}
				enc := NewEncoder(cw)
				if err := enc.Encode(map[string]any{
					"status":      "ok",
					"service":     "dboss-platform",
					"instance_id": "dboss-dbossserver-0",
				}); err != nil {
					t.Errorf("worker %d Encode: %v", id, err)
					return
				}
				// A ~72-byte response fits the 32KB streamBuf in one write,
				// plus the trailing '\n' write. A storm would be hundreds+.
				if writes > 8 {
					t.Errorf("worker %d: %d writes for a small response (storm?)", id, writes)
					return
				}
				if !bytes.Contains(cw.bytes, []byte(`"status":"ok"`)) {
					t.Errorf("worker %d: bad output %q", id, cw.bytes)
				}
			}
		}(w)
	}
	wg.Wait()
}

type countWriter struct {
	n     *int
	bytes []byte
}

func (w *countWriter) Write(p []byte) (int, error) {
	*w.n++
	w.bytes = append(w.bytes, p...)
	return len(p), nil
}
