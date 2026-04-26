package venc

import (
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/native/encvm"
)

const (
	encBufInitSize = 32 * 1024
)

type encodeState struct {
	// vmCtx must stay first so VjExecCtx.Stack keeps the native-required alignment.
	vmCtx VjExecCtx
	flags uint32
	inVM  bool
	buf   []byte

	indentString string
	indentPrefix string
	indentDepth  int
	indentTpl    *[1 + 255 + maxIndentDepth*8]byte // points into global cache; read-only
	nativeIndent bool                              // simple indent pattern (C VM can handle)
	tplKey       string                            // L1 cache key for indentTpl (survives pool recycle)

	flushFn func([]byte) error
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

	encodeStatePool.Put(es)
}

func (es *encodeState) flush() error {
	err := es.flushFn(es.buf)
	es.buf = es.buf[:0]
	return err
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
