package venc

import (
	"io"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
)

const (
	encBufInitSize = 32 * 1024
	// streamBufInitSize is the initial (and post-capBack) capacity of the
	// streaming Encoder's working buffer. Larger than encBufInitSize so that
	// interface-heavy payloads (e.g. Twitter's 17 interface{} fields) hit
	// VJ_IFACE_BUF_FULL less often — each BUF_FULL on an interface field
	// rolls back the speculative key write and forces a VM retry.
	streamBufInitSize = 128 * 1024
	// streamBufCapMax bounds the parked streaming buffer across encodes. The
	// streaming path grows the buffer on demand (e.g. a large string whose
	// 2+len*6 escape reservation exceeds streamBufInitSize); we tolerate that
	// overgrowth for the rest of the in-flight encode, then cap it back down
	// on the next release so a single oversized value cannot ratchet the
	// pooled buffer toward unbounded memory.
	streamBufCapMax = 4 * streamBufInitSize
)

type encMode uint8

const (
	// modeBuffer is the Marshal/AppendMarshal mode that accumulates the whole
	// output in one buffer, so reclaim can only grow. Chosen as zero so a pooled
	// encodeState is buffer-ready without any store into es.mode.
	modeBuffer encMode = iota
	// modeStream is the Encoder mode: a full buffer is flushed to the writer
	// and the window reopens in place.
	modeStream
)

type encodeState struct {
	// vmCtx must stay first so VjExecCtx.Stack keeps the native-required alignment.
	vmCtx VjExecCtx
	flags uint32
	inVM  bool
	// buf is the working output buffer and the VM's write window. Its backing
	// array has one of three origins: the pooled arena (Marshal, the default
	// resident), the caller's dst (AppendMarshal), or out's parked streaming
	// buffer swapped in for the duration of an Encoder encode.
	buf []byte

	indentString string
	indentPrefix string
	indentDepth  int
	indentTpl    *[1 + 255 + maxIndentDepth*8]byte // points into global cache; read-only
	// useNativeVM is true when the native VM can run this encode. It is false
	// if encvm is unavailable, or if indent mode uses a pattern the VM cannot
	// synthesize (isSimpleIndent); in those cases exec falls back to interp.
	useNativeVM bool
	tplKey      string // L1 cache key for indentTpl (survives pool recycle)

	// mode selects how a full es.buf reclaims writable space (see reclaim). Its
	// zero value (arena) is the Marshal/AppendMarshal default, so the hot path
	// stores nothing; encodePtr flips it to stream for an Encoder encode.
	mode encMode

	// stream holds the Encoder-only writer callback and parked buffer. It is
	// untouched (all-zero) outside the streaming path, so buffer mode carries no
	// dead state it must reset — only encodePtr writes it, and undoes it.
	stream streamState

	// bufSize carries WithBufSize across the MarshalOption boundary (whose
	// func(*encodeState) signature is public and fixed). marshalWith drains it
	// into a local and zeroes it on entry, so it is not cross-encode state.
	// Zero means "not set".
	bufSize int

	// inUse is a diagnostic guard (race builds only) that catches the pool
	// handing one encodeState to two goroutines, which would alias es.buf.
	inUse poolGuard
}

var _ [0]byte = [unsafe.Offsetof(encodeState{}.vmCtx)]byte{}

var encodeStatePool = sync.Pool{
	New: func() any {
		return &encodeState{
			buf: gort.MakeDirtyBytes(0, encBufInitSize),
		}
	},
}

// indentTplCache caches pre-built indent templates keyed by "prefix\x00indent".
// Entries are immutable after insertion — safe for concurrent read without copying.
var indentTplCache sync.Map

func init() {
	es := acquireEncodeState()
	releaseEncodeState(es)
}

func acquireEncodeState() *encodeState {
	es := encodeStatePool.Get().(*encodeState)
	es.inUse.acquire()

	es.buf = es.buf[:0]
	es.flags = 0
	es.indentString = ""
	// compact Marshal/Encoder (indentString=="") never goes through withIndent
	// or encodePtr's indent branch, so the VM-vs-interp default must live here.
	es.useNativeVM = encvm.Available

	es.setupVMTrace()

	return es
}

func releaseEncodeState(es *encodeState) {
	// Just returns the object: dirty-state reset lives in acquire, and all
	// stream-only teardown (mode, write, capBack) in encodePtr.
	es.inUse.release()
	encodeStatePool.Put(es)
}

// reclaim makes writable room in es.buf, dispatching on es.mode. bufFull
// reports that the previous VM run ended in BUF_FULL (it needs a larger
// writable window to make progress); produced is how many bytes that run wrote
// before stopping. A BUF_FULL run may still have produced > 0: the VM makes
// partial progress, then hits a reservation larger than the remaining tail. So
// "needs room" is bufFull, not len == cap. produced == 0 on a BUF_FULL means
// the reservation exceeds even an empty window, which is what forces stream
// mode to grow rather than spin flushing nothing.
func (es *encodeState) reclaim(bufFull bool, produced int) error {
	if es.mode == modeBuffer {
		// Buffer: grow whenever the last run hit BUF_FULL (the tail was too
		// small for its next reservation) or the window is exactly full (a
		// yield handler may have appended up to cap).
		if bufFull || len(es.buf) == cap(es.buf) {
			es.grow()
		}
		return nil
	}

	// Stream: flush committed bytes to reopen the window, then grow only when
	// flushing cannot help — the window is still full after flushing, or the VM
	// stalled on a reservation larger than the whole (empty) window.
	if len(es.buf) > 0 {
		if err := es.stream.flush(es); err != nil {
			return err
		}
	}
	if len(es.buf) == cap(es.buf) || (bufFull && produced == 0) {
		es.grow()
	}
	return nil
}

func (es *encodeState) growBuf(hint int) {
	if hint > cap(es.buf) {
		es.buf = gort.MakeDirtyBytes(0, max((encBufInitSize/hint)*hint, hint*2))
	}
}

// grow doubles es.buf (with a +4096 floor over the committed length) and copies
// the committed bytes into the new backing array. Shared by both modes: for
// buffer mode the copied prefix is real output; for stream mode it is the
// post-flush residual. cap is preserved by keeping len unchanged.
func (es *encodeState) grow() {
	newCap := max(cap(es.buf)*2, len(es.buf)+4096)
	newBuf := gort.MakeDirtyBytes(len(es.buf), newCap)
	copy(newBuf, es.buf)
	es.buf = newBuf
}

func (es *encodeState) exec(bp *Blueprint, base unsafe.Pointer) error {
	if !es.inVM && es.useNativeVM {
		return es.execVM(bp, base)
	}
	return es.interp(bp, base)
}

func (es *encodeState) encodeTop(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	return ti.Encode(es, ptr)
}

// buildIndentTpl looks up (or creates) a cached indent template for the
// given prefix/indent pair.
// L1: compare the key kept on the encodeState (survives pool recycle).
// L2: global sync.Map for all known patterns.
func (es *encodeState) buildIndentTpl(prefix, indent string) {
	key := prefix + "\x00" + indent
	if key == es.tplKey {
		return
	}
	if v, ok := indentTplCache.Load(key); ok {
		es.indentTpl = v.(*[1 + 255 + maxIndentDepth*8]byte)
		es.tplKey = key
		return
	}
	tpl := new([1 + 255 + maxIndentDepth*8]byte)
	tpl[0] = '\n'
	off := 1
	off += copy(tpl[off:], prefix)
	for range maxIndentDepth {
		off += copy(tpl[off:], indent)
	}
	actual, _ := indentTplCache.LoadOrStore(key, tpl)
	es.indentTpl = actual.(*[1 + 255 + maxIndentDepth*8]byte)
	es.tplKey = key
}

// isSimpleIndent reports whether the native VM can synthesize this indent
// pattern: indent must be 1..8 bytes of a single ' ' or '\t', prefix <= 255.
func isSimpleIndent(prefix, indent string) bool {
	if len(prefix) > 255 || len(indent) == 0 || len(indent) > 8 {
		return false
	}
	ch := indent[0]
	if ch != ' ' && ch != '\t' {
		return false
	}
	for i := 1; i < len(indent); i++ {
		if indent[i] != ch {
			return false
		}
	}
	return true
}

// encodingSizeHint returns the best buffer size estimate for the given type and data.
func encodingSizeHint(ti *EncTypeInfo, ptr unsafe.Pointer) int {
	if fn := ti.SizeFn; fn != nil {
		if predicted := fn(ptr); predicted > 0 {
			return predicted + predicted/32
		}
	}
	return ti.HintBytes
}

// streamState holds the Encoder-only reclaim state, kept separate from mode so
// buffer-mode encodes carry no dead fields. write is the writer callback; buf is the
// parked streaming buffer held between encodes, isolated from the arena's
// zero-copy erosion (Marshal advances es.buf's base via es.buf[n:], shrinking
// cap on the pooled object). buf's capacity is bounded by capBack in encodePtr.
type streamState struct {
	write func([]byte) (int, error)
	buf   []byte
}

// acquireBuf returns the parked streaming buffer, cleared and guaranteed to
// hold at least streamBufInitSize, allocating on first use. The streamBufCapMax
// bound is enforced by capBack at the end of the prior encode, so by acquire
// time buf is already in range; this only tops up the floor.
func (s *streamState) acquireBuf() []byte {
	if cap(s.buf) >= streamBufInitSize {
		return s.buf[:0]
	}
	return gort.MakeDirtyBytes(0, streamBufInitSize)
}

// park stores the (possibly grown) working buffer back after an encode, to be
// reused or capped on the next round.
func (s *streamState) park(buf []byte) {
	s.buf = buf
}

// capBack reduces the parked buffer to streamBufInitSize when a prior encode
// grew it past streamBufCapMax, so a single oversized value cannot ratchet the
// pooled buffer toward unbounded memory.
func (s *streamState) capBack() {
	if cap(s.buf) > streamBufCapMax {
		s.buf = gort.MakeDirtyBytes(0, streamBufInitSize)
	}
}

// flush writes the buffered bytes to the underlying writer. An io.Writer may
// legally short-write (return n < len(buf), err == nil); flush preserves the
// unwritten tail by memmoving it back to the buffer's base so the next
// iteration re-attempts it, rather than discarding data. Only a fully-consumed
// flush (n == len(es.buf)) clears es.buf. A non-nil err stops encoding
// immediately; in that case the tail is dropped because the caller is about to
// bail out.
//
// A zero write (n == 0, err == nil) on a non-empty buffer is treated as a
// short write: the writer made no progress, and re-entering the VM would
// grow es.buf unboundedly on each BUF_FULL cycle (flush no-ops, the workBuf
// cap doubles until the whole payload fits, then the final writeAll catches
// it anyway). Fail fast instead of buffering the entire output in memory.
func (s *streamState) flush(es *encodeState) error {
	n, err := s.write(es.buf)
	if err != nil {
		return err
	}
	if n >= len(es.buf) {
		es.buf = es.buf[:0]
	} else if n > 0 {
		// Memmove the unwritten tail back to the buffer's base, keeping the
		// underlying array and its cap intact. This preserves the streaming
		// buffer as a fixed-size window: residual returns to the head, the
		// write window reopens at full size, and only a single value whose
		// reservation exceeds the whole cap forces a realloc. (An earlier
		// es.buf = es.buf[n:] advanced the base and eroded cap on every short
		// write, silently turning the fixed buffer into a repeatedly-growing
		// one.)
		m := copy(es.buf, es.buf[n:len(es.buf)])
		es.buf = es.buf[:m]
	} else if len(es.buf) > 0 {
		return io.ErrShortWrite
	}
	return nil
}
