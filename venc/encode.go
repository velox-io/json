package venc

import (
	"fmt"
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
	// VJ_IFACE_BUF_FULL less often; each BUF_FULL on an interface field
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
	// It is the root invocation's execution context; nested executions get
	// their own invocation (see invocations).
	vmCtx VjExecCtx
	flags uint32
	// inVM reports that a native VM run is active or suspended on this
	// encodeState (vmDepth > 0). It routes map Encode and nested dispatch away
	// from re-entering the root context; nesting per se is governed by
	// execDepth.
	inVM    bool
	vmDepth int
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
	// tplPrefix/tplIndent retain the pattern the cached indentTpl was built
	// for (survive pool recycle); comparing them avoids rebuilding a concat
	// key on every nested invocation.
	tplPrefix string
	tplIndent string

	// mode selects how a full es.buf reclaims writable space (see reclaim). Its
	// zero value (arena) is the Marshal/AppendMarshal default, so the hot path
	// stores nothing; encodePtr flips it to stream for an Encoder encode.
	mode encMode

	// stream holds the Encoder-only writer callback and parked buffer. It is
	// untouched (all-zero) outside the streaming path, so buffer mode carries no
	// dead state it must reset; only encodePtr writes it, and undoes it.
	stream streamState

	// bufSize carries WithBufSize across the MarshalOption boundary (whose
	// func(*encodeState) signature is public and fixed). marshalWith drains it
	// into a local and zeroes it on entry, so it is not cross-encode state.
	// Zero means "not set".
	bufSize int

	// execDepth counts es.exec runs on the Go call stack. The outermost run
	// owns vmCtx; every nested run executes in its own child invocation, so a
	// suspended parent's stack frames are never shared or overwritten. It is
	// the cross-invocation nesting budget: a child cannot push past
	// VJ_MAX_STACK_DEPTH levels.
	execDepth int

	// childNative, when set by the stream driver around an element encode,
	// makes nested es.exec runs prefer the native VM for their child
	// invocation. Saved and restored per element so nested activations compose.
	childNative bool

	// childElemNL seeds the initial elemNL latch of the next interpreter run
	// when a stream driver hands it an element in interpreter mode: the driver
	// supplies separators (native-style), so the element's own leading newline
	// must still come from the element side. Consumed at interp entry and
	// cleared by execStreamElement when the element never runs the
	// interpreter.
	childElemNL bool

	// invocations is the child-invocation stack. invocations[:len] are active
	// (innermost last); the entries beyond len are the free list. The backing
	// array holds pointers, so each invocation's address stays stable even if
	// the slice grows.
	invocations []*invocation

	// inUse is a diagnostic guard (race builds only) that catches the pool
	// handing one encodeState to two goroutines, which would alias es.buf.
	inUse poolGuard
}

// invocation is one nested execution: a suspended or running child of an
// outer es.exec. It carries its own VjExecCtx (stack included) and the
// interpreter's logical indent depth to restore when it completes. Elements
// of one stream activation reuse the same invocation, and an element's nested
// streams push the next one.
type invocation struct {
	ctx         VjExecCtx
	savedIndent int
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
// Entries are immutable after insertion, safe for concurrent read without copying.
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
	// flushing cannot help: the window is still full after flushing, or the VM
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

// exec runs one Blueprint against base. The outermost run owns vmCtx; a run
// nested inside another (a Go fallback handler, an interpreter fallback op, or
// a stream driver) executes in a child invocation so the parent's stack,
// program counter, and base stay intact. Nested runs use the interpreter,
// matching the pre-invocation routing, except when the stream driver requests
// the native plan via execStreamElement. Every run is depth-balanced: it exits
// with es.indentDepth at its entry value, nested runs through popInvocation
// and the outermost run through its exit defer.
func (es *encodeState) exec(bp *Blueprint, base unsafe.Pointer) error {
	if es.execDepth == 0 {
		es.execDepth++
		// The outermost run exits at its entry indent depth, mirroring
		// popInvocation for nested runs: yields sync es.indentDepth up from
		// ctx.IndentDepth, and a root stream driver runs one outermost
		// execution per stream element.
		savedDepth := es.indentDepth
		defer func() {
			es.execDepth--
			es.indentDepth = savedDepth
		}()
		if es.useNativeVM {
			return es.execVM(&es.vmCtx, bp, base)
		}
		return es.interp(&es.vmCtx, bp, base)
	}

	if es.execDepth >= VJ_MAX_STACK_DEPTH {
		return fmt.Errorf("venc: nesting depth exceeds limit (%d)", VJ_MAX_STACK_DEPTH)
	}
	inv := es.pushInvocation()
	defer es.popInvocation()
	if es.useNativeVM && es.childNative {
		return es.execVM(&inv.ctx, bp, base)
	}
	return es.interp(&inv.ctx, bp, base)
}

// execStreamElement encodes one stream element. It is the explicit child
// entry: executions nested inside the element (and the element's own
// blueprint run, when a parent execution is suspended) prefer the native plan
// for their child invocation. In interpreter mode the element runs through
// its Blueprint rather than its Encode shortcut, so the element-position
// newline protocol (every blueprint op writes its own leading newline) holds
// uniformly; elemNL seeds that latch for the element's run.
func (es *encodeState) execStreamElement(ti *EncTypeInfo, ptr unsafe.Pointer, interpElem bool) (err error) {
	prev := es.childNative
	es.childNative = true
	defer func() {
		es.childNative = prev
		es.childElemNL = false
	}()
	es.childElemNL = interpElem
	if interpElem {
		return es.exec(ti.getBlueprint(), ptr)
	}
	return ti.Encode(es, ptr)
}

// pushInvocation activates the next child invocation, reusing a released one
// (LIFO, so consecutive stream elements share the same context). The indent
// depth at entry is saved for restoration by popInvocation.
func (es *encodeState) pushInvocation() *invocation {
	var inv *invocation
	if n := len(es.invocations); n < cap(es.invocations) {
		// The slot beyond len holds the most recently popped invocation; it is
		// nil where a previous append growth left unwritten capacity.
		if extended := es.invocations[:n+1]; extended[n] != nil {
			es.invocations = extended
			inv = extended[n]
		}
	}
	if inv == nil {
		inv = &invocation{}
		es.invocations = append(es.invocations, inv)
	}
	inv.savedIndent = es.indentDepth
	// Trace builds share the root's trace buffer across invocations; the
	// executions are serialized, so the interleaved log keeps one buffer.
	inv.ctx.TraceBuf = es.vmCtx.TraceBuf
	return inv
}

// popInvocation deactivates the innermost child invocation and restores the
// interpreter indent depth it inherited. The stack is truncated before the
// invocation is read so a fault here cannot leave a corrupted length behind.
func (es *encodeState) popInvocation() {
	n := len(es.invocations) - 1
	inv := es.invocations[n]
	es.invocations = es.invocations[:n]
	if inv != nil {
		es.indentDepth = inv.savedIndent
	}
}

func (es *encodeState) encodeTop(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	return ti.Encode(es, ptr)
}

// buildIndentTpl looks up (or creates) a cached indent template for the
// given prefix/indent pair. The comparison fast path avoids the concat key
// allocation: execVM runs per nested invocation, so a stream of N elements
// calls this N times per encode.
// L1: the retained pattern strings (survive pool recycle).
// L2: global sync.Map for all known patterns.
func (es *encodeState) buildIndentTpl(prefix, indent string) {
	if es.indentTpl != nil && es.tplPrefix == prefix && es.tplIndent == indent {
		return
	}
	key := prefix + "\x00" + indent
	if v, ok := indentTplCache.Load(key); ok {
		es.indentTpl = v.(*[1 + 255 + maxIndentDepth*8]byte)
		es.tplPrefix = prefix
		es.tplIndent = indent
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
	es.tplPrefix = prefix
	es.tplIndent = indent
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
