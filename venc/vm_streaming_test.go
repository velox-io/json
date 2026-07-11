package venc

import (
	"bytes"
	"errors"
	"io"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

// tail returns the last 60 bytes of b (or all of it), for readable mismatch
// diagnostics on large streaming payloads.
func tail(b []byte) []byte {
	if len(b) > 60 {
		return b[len(b)-60:]
	}
	return b
}

// runStreamEncode drives the streaming Encoder discipline directly on a pooled
// encodeState, bypassing Encoder.encodePtr so a test can control the workBuf
// cap and observe every Write. It acquires an encodeState, installs a
// write-counting sink with a storm cap (errStorm once writes exceed maxWrites,
// to fail fast instead of hanging on a deadlock), sets stream mode, runs
// encodeTop, and appends the residual buffer. It restores mode/stream before
// the pooled release exactly as encodePtr's defer does in production, so no
// dirty stream state leaks back into the pool. It returns the collected output,
// the number of Write calls, and encodeTop's error.
func runStreamEncode(t *testing.T, ti *EncTypeInfo, ptr unsafe.Pointer, workBufCap, maxWrites int, errStorm error) (out []byte, writes int, err error) {
	t.Helper()

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.flags = uint32(escapeStringFlags)
	es.useNativeVM = true
	es.buf = make([]byte, 0, workBufCap)
	es.stream.write = func(p []byte) (int, error) {
		writes++
		if writes > maxWrites {
			return 0, errStorm
		}
		out = append(out, p...)
		return len(p), nil
	}
	es.mode = modeStream

	err = es.encodeTop(ti, ptr)
	out = append(out, es.buf...)
	es.buf = es.buf[:0]
	es.mode, es.stream.write = modeBuffer, nil // restore before pooled release (encodePtr does this in prod)
	return out, writes, err
}

// TestStreamingBufFull_AnyValueDeadlock reproduces the production CPU-100%
// infinite loop in the streaming Encoder path.
//
// A pooled encodeState whose buf cap was nibbled below ~331 bytes (Marshal
// reuses the tail via es.buf = es.buf[n:], shrinking cap) is reused for a
// streaming Encode of a value containing an `any` field. OP_INTERFACE
// speculatively VM_CHECKs 330+ bytes up front regardless of the real value
// size; with a smaller workBuf it returns BUF_FULL having written 0 bytes.
// The streaming BUF_FULL handler only flushed (0 bytes) and never grew, so
// execVMLoop spun forever emitting empty Write calls.
//
// The grow-on-zero-progress guard in execVMLoop breaks the deadlock: with it,
// the buffer grows until the VM's next atomic write fits and encoding
// completes in a handful of writes. Without it this test hangs and is failed
// by the per-encode write cap below.
func TestStreamingBufFull_AnyValueDeadlock(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	v := map[string]any{
		"status":      "ok",
		"service":     "dboss-platform",
		"instance_id": "dboss-dbossserver-0",
	}
	// velox iterates map keys in Swiss-map order (JSON object key order is
	// unspecified), so compare against velox's own Marshal, not stdlib.
	want, _ := Marshal(v)

	ti := EncTypeInfoOf(reflect.TypeFor[map[string]any]())
	// Simulate a nibbled pooled buffer: cap below OP_INTERFACE's ~331-byte
	// reservation. The victim's size hint (~304) is smaller than this, so
	// growBuf would not restore it, leaving the streaming Encoder stuck.
	const maxWrites = 1024
	out, writes, err := runStreamEncode(t, ti, unsafe.Pointer(&v), 64, maxWrites,
		errors.New("BUF_FULL storm: deadlock not broken"))
	if err != nil {
		t.Fatalf("encodeTop failed after %d writes: %v", writes, err)
	}

	if writes > maxWrites {
		t.Fatalf("BUF_FULL storm: %d writes, deadlock not broken", writes)
	}
	if string(out) != string(want) {
		t.Errorf("mismatch\n  got:  %s\n  want: %s", out, want)
	}
}

// TestStreamingBufFull_LargeStringIface reproduces the second deadlock class:
// a large interface value (e.g. a 60KB string) whose atomic reservation never
// fits the streaming workBuf.
//
// OP_INTERFACE speculatively writes the key, then delegates to
// encode_primitive_value. For OP_STRING the C encoder reserves
// 2 + len*6 bytes (worst-case escaping) and returns VJ_EXIT_BUF_FULL having
// written zero bytes when that won't fit (eface.c OP_STRING branch). Two
// problems follow:
//
//  1. vj_op_interface's VJ_IFACE_BUF_FULL case does NOT roll back the
//     speculatively-written key (unlike the YIELD / CACHE_MISS / NAN_INF
//     cases), so on re-entry the key is written again, duplicating it.
//
//  2. Because the key write makes written > 0, the streaming BUF_FULL path
//     only flushes and never grows; the string's atomic reservation
//     (360002 bytes here) is larger than any reasonable workBuf, so the loop
//     spins forever flushing just the key. The written==0 guard added for the
//     empty-write storm does not catch this, since written equals the key
//     length.
//
// The cap on writes bounds the run so the test fails fast instead of hanging.
func TestStreamingBufFull_LargeStringIface(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	type S struct {
		Data any `json:"data"`
	}
	big := strings.Repeat("x", 60000)
	v := S{Data: big}

	// Warm the ifaceCache so string values resolve to OP_STRING in
	// encode_primitive_value instead of the first-encode CACHE_MISS yield.
	if _, err := Marshal(v); err != nil {
		t.Fatalf("warmup Marshal: %v", err)
	}

	ti := EncTypeInfoOf(reflect.TypeFor[S]())
	// Small workBuf: large enough for OP_INTERFACE's key+330 reservation but
	// far smaller than the 60KB string's atomic reservation (360002 bytes).
	const maxWrites = 5000
	out, writes, err := runStreamEncode(t, ti, unsafe.Pointer(&v), 4096, maxWrites,
		errors.New("large-string deadlock: key flushed without progress"))

	switch {
	case writes > maxWrites:
		t.Fatalf("deadlock: flushed the key %d times without completing the value", writes)
	case err != nil:
		t.Fatalf("encodeTop: %v", err)
	}

	if c := strings.Count(string(out), `"data"`); c != 1 {
		head := string(out)
		if len(head) > 80 {
			head = head[:80] + "..."
		}
		t.Errorf("key \"data\" emitted %d times, want 1 (output head=%q)", c, head)
	}

	want := `{"data":"` + big + `"}`
	if string(out) != want {
		got := string(out)
		if len(got) > 80 {
			got = got[:80] + "..."
		}
		t.Errorf("mismatch\n  got  len=%d head=%q\n  want len=%d", len(out), got, len(want))
	}
}

// TestStreamingEncoder_RollbackDoesNotConflictWithFlushedBytes pins the
// invariant that encvm's speculative-key rollback never conflicts with bytes
// already flushed to the writer.
//
// OP_INTERFACE speculatively writes the JSON key, then probes the interface
// cache. A value whose type the C VM cannot compile (here *big.Int via
// MarshalJSON) returns VJ_IFACE_YIELD, and the VM rolls buf back to before the
// speculative key write (buf = iface_saved_buf). Go's fallback then re-emits
// the key itself.
//
// The concern: if rollback ever undid bytes that flush() had already pushed
// to the writer, those bytes could not be "un-written" (覆水难收), corrupting
// the stream. The design prevents this because rollback only touches the
// in-flight workBuf region (es.buf[len:cap], written this VM call) while flush
// only drains the already-committed prefix (es.buf[:len]); they occupy disjoint
// memory and never interleave (flush is Go-side, the VM runs in C between
// flushes). This test exercises the path under a tiny streaming workBuf so
// rollback happens near the flush boundary, and asserts:
//
//   - no rolled-back key leaks to the writer (the speculative key appears
//     exactly once, written by Go's fallback),
//   - the output is byte-identical to a fully-buffered reference.
func TestStreamingEncoder_RollbackDoesNotConflictWithFlushedBytes(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	type Wallet struct {
		Owner   string   `json:"owner"`
		Balance *big.Int `json:"balance"` // MarshalJSON → VJ_IFACE_YIELD → key rollback
	}
	v := Wallet{Owner: "alice", Balance: new(big.Int).SetInt64(42)}

	// Many repeats so the payload spans several flush rounds.
	type Batch struct {
		Wallets []Wallet `json:"wallets"`
	}
	big := Batch{Wallets: make([]Wallet, 200)}
	for i := range big.Wallets {
		big.Wallets[i] = v
	}

	wantBig, err := Marshal(big)
	if err != nil {
		t.Fatalf("reference Marshal big: %v", err)
	}

	var outBig bytes.Buffer
	encBig := NewEncoder(&outBig)
	if err := encBig.Encode(&big); err != nil {
		t.Fatalf("streaming Encode: %v", err)
	}

	got := outBig.Bytes()
	// Encoder.Encode appends a trailing '\n' (encoder.go); Marshal does not,
	// so strip it before comparing against the buffered reference.
	gotBody := got
	if n := len(gotBody); n > 0 && gotBody[n-1] == '\n' {
		gotBody = gotBody[:n-1]
	}
	if !bytes.Equal(gotBody, wantBig) {
		t.Fatalf("streaming output mismatch: got %d bytes, want %d bytes\n  got tail: %q\n  want tail: %q",
			len(gotBody), len(wantBig), tail(gotBody), tail(wantBig))
	}

	// The speculative "balance" key must appear exactly 200 times (once per
	// wallet), each written by Go's fallback after the VM rolled its
	// speculative copy back. A leaked speculative key would inflate the count.
	if c := strings.Count(string(got), `"balance"`); c != 200 {
		t.Fatalf("rolled-back key leaked: \"balance\" appears %d times, want 200", c)
	}
}

// TestStreamingEncoder_RollbackAtFlushBoundary drives the rollback path with a
// tiny workBuf so the VM's speculative key write and the subsequent rollback
// land right at the flush boundary, the regime where a rollback-vs-flushed
// conflict would surface. Counts every flush call to confirm no key is leaked
// and the writer sees each "balance" exactly once.
func TestStreamingEncoder_RollbackAtFlushBoundary(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	type Wallet struct {
		Owner   string   `json:"owner"`
		Balance *big.Int `json:"balance"`
	}
	type batch struct {
		Wallets []Wallet `json:"wallets"`
	}
	bigVal := batch{Wallets: make([]Wallet, 50)}
	for i := range bigVal.Wallets {
		bigVal.Wallets[i] = Wallet{Owner: "alice", Balance: new(big.Int).SetInt64(42)}
	}

	want, err := Marshal(bigVal)
	if err != nil {
		t.Fatalf("reference Marshal: %v", err)
	}

	// workBuf just above the OP_INTERFACE key reservation so rollback trips
	// near every flush.
	ti := EncTypeInfoOf(reflect.TypeFor[batch]())
	const maxFlushes = 4000
	out, flushes, err := runStreamEncode(t, ti, unsafe.Pointer(&bigVal), 80, maxFlushes,
		errors.New("flush storm"))
	if err != nil {
		t.Fatalf("encodeTop: %v (flushes=%d)", err, flushes)
	}

	if !bytes.Equal(out, want) {
		t.Fatalf("mismatch at flush boundary: got %d bytes, want %d bytes\n  got tail: %q\n  want tail: %q",
			len(out), len(want), tail(out), tail(want))
	}
	if c := strings.Count(string(out), `"balance"`); c != 50 {
		t.Fatalf("rolled-back key leaked at boundary: \"balance\" appears %d times, want 50", c)
	}
}

// TestStreamingBuffer_CapBackAfterOversized confirms that stream.buf is
// bounded across encodes: an encode whose atomic write grows the buffer past
// streamBufCapMax (here a ~100KB string reserving 600KB+ under worst-case
// escaping) is tolerated for the in-flight encode, but release caps it back to
// streamBufInitSize so a single oversized value cannot ratchet the pooled buffer
// toward unbounded memory.
func TestStreamingBuffer_CapBackAfterOversized(t *testing.T) {
	if !encvm.Available {
		t.Skip("native VM not available")
	}

	const bufFloor = streamBufInitSize
	const bufMax = streamBufCapMax

	type S struct {
		Data any `json:"data"`
	}
	big := strings.Repeat("x", 100000)
	v := S{Data: big}
	// Warm the ifaceCache so the string resolves to OP_STRING inline.
	if _, err := Marshal(v); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	ti := EncTypeInfoOf(reflect.TypeFor[S]())

	es := acquireEncodeState()
	es.flags = uint32(escapeStringFlags)
	es.useNativeVM = true

	// Drive the streaming path (swap in stream.buf) with a large string, which
	// grows es.buf (the swapped-in stream buffer, mid-encode) past bufMax.
	var writes int
	const maxWrites = 5000
	es.stream.write = func(p []byte) (int, error) {
		writes++
		if writes > maxWrites {
			return 0, errStr("overflow")
		}
		return len(p), nil
	}
	es.mode = modeStream
	marshalBuf := es.buf
	es.buf = es.stream.acquireBuf()
	if err := es.encodeTop(ti, unsafe.Pointer(&v)); err != nil {
		t.Fatalf("oversized encodeTop: %v", err)
	}
	es.stream.park(es.buf)
	es.buf = marshalBuf

	if cap(es.stream.buf) <= bufMax {
		t.Fatalf("post-oversize cap=%d, want grown past %d", cap(es.stream.buf), bufMax)
	}

	// capBack (part of encodePtr's teardown, replicated here) caps stream.buf
	// back to the floor when it exceeds streamBufCapMax. Assert on the same
	// object: sync.Pool does not guarantee a release/re-acquire round-trip
	// returns the object we just released.
	es.stream.capBack()
	es.mode, es.stream.write = modeBuffer, nil
	releaseEncodeState(es)
	switch c := cap(es.stream.buf); {
	case c > bufMax:
		t.Fatalf("after capBack stream.buf cap=%d, want <= %d (not capped back)", c, bufMax)
	case c < bufFloor:
		t.Fatalf("after capBack stream.buf cap=%d, want >= %d (floor)", c, bufFloor)
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

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

// shortWriter accepts writes but only flushes the first half bytes of each
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

// zeroWriter returns (0, nil) for every Write. The io.Writer contract
// permits short writes (n < len(p), err == nil), and returning n == 0
// with err == nil is technically legal ("accepted nothing, nothing
// wrong"). A misbehaving writer like this must not cause the encoder to
// infinite-loop, silently buffer unbounded data, or return nil as if
// the data was written. The encoder should surface a real error.
type zeroWriter struct {
	mu    sync.Mutex
	calls int
}

func (w *zeroWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.calls++
	w.mu.Unlock()
	return 0, nil
}

// TestStreamingEncoder_ZeroWriteReturnsError drives Encoder.Encode with a
// writer that always returns (0, nil). Encode must not return nil (which
// would mean it believed the data was written) and must not loop forever;
// it should surface io.ErrShortWrite so the caller knows the writer made
// no progress.
func TestStreamingEncoder_ZeroWriteReturnsError(t *testing.T) {
	w := &zeroWriter{}
	enc := NewEncoder(w)
	err := enc.Encode(map[string]any{"k": "v"})
	if err == nil {
		t.Fatalf("zero-write writer: Encode returned nil error; data silently lost")
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero-write writer: expected io.ErrShortWrite, got %v", err)
	}
	if w.calls == 0 {
		t.Fatalf("zero-write writer: Write was never called")
	}
}

// TestStreamingEncoder_ZeroWriteLargePayloadTerminates exercises the
// intermediate flush path with a payload large enough to trigger BUF_FULL
// cycles. With a zero-write writer, every flush() is a no-op (n == 0,
// err == nil), so flush() makes no progress. The encoder must still
// terminate (no infinite loop) and return an error rather than buffering
// unbounded data forever.
func TestStreamingEncoder_ZeroWriteLargePayloadTerminates(t *testing.T) {
	big := bytes.Repeat([]byte("ab"), 100*1024) // 200 KiB of string content
	w := &zeroWriter{}
	enc := NewEncoder(w)
	err := enc.Encode(map[string]any{"data": string(big)})
	if err == nil {
		t.Fatalf("zero-write writer: Encode returned nil error; data silently lost")
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero-write writer: expected io.ErrShortWrite, got %v", err)
	}
}
