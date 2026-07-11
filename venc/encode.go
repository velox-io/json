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
	// streamBufCapMax bounds es.streamBuf across encodes. The streaming path
	// grows the buffer on demand (e.g. a large string whose 2+len*6 escape
	// reservation exceeds encBufInitSize); we tolerate that overgrowth for
	// the rest of the in-flight encode, then cap it back down on the next
	// acquire so a single oversized value cannot ratchet the pooled buffer
	// toward unbounded memory.
	streamBufCapMax = 4 * encBufInitSize
)

type encodeState struct {
	// vmCtx must stay first so VjExecCtx.Stack keeps the native-required alignment.
	vmCtx VjExecCtx
	flags uint32
	inVM  bool
	buf   []byte

	// streamBuf is a buffer dedicated to the streaming Encoder path, kept
	// isolated from Marshal's zero-copy erosion. marshalWith returns
	// es.buf[:n:n] and advances es.buf's base via es.buf[n:], shrinking cap
	// on the pooled object; the streaming path never lends its buffer to a
	// caller, so it must not inherit that eroded cap. Lazy-allocated on first
	// streaming use; reused thereafter.
	streamBuf []byte

	indentString string
	indentPrefix string
	indentDepth  int
	indentTpl    *[1 + 255 + maxIndentDepth*8]byte // points into global cache; read-only
	nativeIndent bool                              // simple indent pattern (C VM can handle)
	tplKey       string                            // L1 cache key for indentTpl (survives pool recycle)

	flushFn func([]byte) (int, error)
	bufSize int
}

var _ [0]byte = [unsafe.Offsetof(encodeState{}.vmCtx)]byte{}

var encodeStatePool = sync.Pool{
	New: func() any {
		es := &encodeState{
			buf:          gort.MakeDirtyBytes(0, encBufInitSize),
			nativeIndent: encvm.Available,
		}
		return es
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
	es.buf = es.buf[:0]
	es.setupVMTrace()

	return es
}

func releaseEncodeState(es *encodeState) {
	es.flushFn = nil
	es.bufSize = 0

	es.flags = 0

	es.indentString = ""
	es.indentPrefix = ""
	es.indentDepth = 0
	// indentTpl and tplKey are intentionally kept: they point into the
	// global immutable cache and serve as an L1 fast-path on reuse.

	// Cap the streaming buffer back to the floor if a previous streaming
	// encode grew it past streamBufCapMax, so a single oversized value cannot
	// ratchet the pooled buffer toward unbounded memory. Done at release so
	// the bound holds regardless of which pool consumer acquires next.
	es.streamBuf = es.cappedStreamBuf()

	encodeStatePool.Put(es)
}

// cappedStreamBuf returns es.streamBuf reduced to encBufInitSize when its cap
// exceeds streamBufCapMax, otherwise the existing (reused) buffer. Shared by
// releaseEncodeState and acquireStreamBuf so the bound is enforced both at
// pool-out and at acquire time.
func (es *encodeState) cappedStreamBuf() []byte {
	if cap(es.streamBuf) > streamBufCapMax {
		return gort.MakeDirtyBytes(0, encBufInitSize)
	}
	return es.streamBuf
}

// flush writes the buffered bytes to the underlying writer via flushFn. An
// io.Writer may legally short-write (return n < len(buf), err == nil); flush
// preserves the unwritten tail in es.buf so the next iteration re-attempts it,
// rather than discarding data. Only a fully-consumed flush (n == len(es.buf))
// clears es.buf. A non-nil err stops encoding immediately; in that case the
// tail is dropped because the caller is about to bail out.
//
// A zero write (n == 0, err == nil) on a non-empty buffer is treated as a
// short write: the writer made no progress, and re-entering the VM would
// grow es.buf unboundedly on each BUF_FULL cycle (flush() no-ops, the workBuf
// cap doubles until the whole payload fits, then the final writeAll catches
// it anyway). Fail fast instead of buffering the entire output in memory.
func (es *encodeState) flush() error {
	n, err := es.flushFn(es.buf)
	if err != nil {
		return err
	}
	if n >= len(es.buf) {
		es.buf = es.buf[:0]
	} else if n > 0 {
		// Keep the unwritten tail at the buffer's base so the VM loop's
		// workBuf slice and any subsequent grow/copy see the residual bytes.
		es.buf = es.buf[n:]
	} else if len(es.buf) > 0 {
		return io.ErrShortWrite
	}
	return nil
}

func (es *encodeState) exec(bp *Blueprint, base unsafe.Pointer) error {
	if !es.inVM && es.nativeIndent {
		return es.execVM(bp, base)
	}
	return es.interp(bp, base)
}

func (es *encodeState) encodeTop(ti *EncTypeInfo, ptr unsafe.Pointer) error {
	return ti.Encode(es, ptr)
}

// isSimpleIndent reports whether the native VM can synthesize this indent pattern.
func isSimpleIndent(prefix, indent string) int {
	if len(prefix) > 255 || len(indent) == 0 || len(indent) > 8 {
		return 0
	}
	ch := indent[0]
	if ch != ' ' && ch != '\t' {
		return 0
	}
	for i := 1; i < len(indent); i++ {
		if indent[i] != ch {
			return 0
		}
	}
	return len(indent)
}

// buildIndentTpl looks up (or creates) a cached indent template for the
// given prefix/indent pair. L1: compare the key kept on the encodeState
// (survives pool recycle). L2: global sync.Map for all known patterns.
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

// encodingSizeHint returns the best buffer size estimate for the given type and data.
func encodingSizeHint(ti *EncTypeInfo, ptr unsafe.Pointer) int {
	if fn := ti.SizeFn; fn != nil {
		if predicted := fn(ptr); predicted > 0 {
			return predicted + predicted/32
		}
	}
	return ti.HintBytes
}

func (es *encodeState) growBuf(hint int) {
	if hint > cap(es.buf) {
		es.buf = gort.MakeDirtyBytes(0, max((encBufInitSize/hint)*hint, hint*2))
	}
}

// acquireStreamBuf returns a streaming buffer of at least encBufInitSize,
// reusing es.streamBuf across pool recycles. Lazily allocated on first use.
// The streamBufCapMax bound is enforced at releaseEncodeState (pool-out), so
// by acquire time it is already within range; this only tops up the floor.
func (es *encodeState) acquireStreamBuf() []byte {
	sb := es.cappedStreamBuf()
	if cap(sb) >= encBufInitSize {
		return sb[:0]
	}
	return gort.MakeDirtyBytes(0, encBufInitSize)
}
