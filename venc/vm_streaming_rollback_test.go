package venc

import (
	"bytes"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/encvm"
)

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

	// Reuse the encodeState streaming path with a workBuf just above the
	// OP_INTERFACE key reservation so rollback trips near every flush.
	ti := EncTypeInfoOf(reflect.TypeFor[batch]())
	var out []byte
	var flushes int
	const maxFlushes = 4000
	errStorm := errors.New("flush storm")

	es := acquireEncodeState()
	defer releaseEncodeState(es)
	es.flags = uint32(escapeStringFlags)
	es.nativeIndent = true
	es.buf = make([]byte, 0, 80)
	es.flushFn = func(p []byte) (int, error) {
		flushes++
		if flushes > maxFlushes {
			return 0, errStorm
		}
		out = append(out, p...)
		return len(p), nil
	}
	if err := es.encodeTop(ti, unsafe.Pointer(&bigVal)); err != nil {
		t.Fatalf("encodeTop: %v (flushes=%d)", err, flushes)
	}
	out = append(out, es.buf...)
	es.buf = es.buf[:0]

	if !bytes.Equal(out, want) {
		t.Fatalf("mismatch at flush boundary: got %d bytes, want %d bytes\n  got tail: %q\n  want tail: %q",
			len(out), len(want), tail(out), tail(want))
	}
	if c := strings.Count(string(out), `"balance"`); c != 50 {
		t.Fatalf("rolled-back key leaked at boundary: \"balance\" appears %d times, want 50", c)
	}
}
