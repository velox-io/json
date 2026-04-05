package venc

import (
	"reflect"
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
	flags uint32 // escape flags | vjEncFloatExpAuto
	inVM  bool   // blocks re-entrant VM entry
	buf   []byte

	indent       string
	prefix       string
	indentDepth  int
	nativeCompat bool                              // true if compact or simple indent pattern (C VM can handle)
	indentTpl    *[1 + 255 + maxIndentDepth*8]byte // "\n" + prefix + indent*maxDepth

	flushFn func([]byte) error // streaming sink for Encoder
}

// vmCtx offset must remain 0 for the native stack alignment rule.
var _ [0]byte = [unsafe.Offsetof(encodeState{}.vmCtx)]byte{}

var encodeStatePool = sync.Pool{
	New: func() any {
		return &encodeState{
			buf: gort.MakeDirtyBytes(0, encBufInitSize),
		}
	},
}

var indentTplPool = sync.Pool{
	New: func() any {
		return new([1 + 255 + maxIndentDepth*8]byte)
	},
}

func init() {
	encodeStatePool.Put(&encodeState{buf: gort.MakeDirtyBytes(0, encBufInitSize)})
}

func acquireEncodeState() *encodeState {
	es := encodeStatePool.Get().(*encodeState)
	es.buf = es.buf[:0] // reset buffer (may contain partial output from a prior error path)
	es.indent = ""
	es.prefix = ""
	es.indentDepth = 0
	es.nativeCompat = encvm.Available // compact mode: native OK if C VM linked
	es.flags = 0
	es.flushFn = nil
	// Compact-mode VM entry assumes the indent fields are zeroed.
	es.vmCtx.IndentTpl = nil
	es.vmCtx.IndentStep = 0
	es.vmCtx.IndentPrefixLen = 0
	es.vmCtx.IndentDepth = 0

	es.setupVMTrace()

	return es
}

func releaseEncodeState(es *encodeState) {
	const encBufPoolLimit = 1024 * 1024
	if cap(es.buf) > encBufPoolLimit {
		es.buf = nil // discard oversized buffer, let GC reclaim
	}
	if es.indentTpl != nil {
		indentTplPool.Put(es.indentTpl)
		es.indentTpl = nil
	}
	es.flushFn = nil        // clear closure reference before pooling
	encodeStatePool.Put(es) // always recycle the struct (vmCtx is 2152 bytes)
}

// flush writes buffered data through flushFn.
func (es *encodeState) flush() error {
	if es.flushFn == nil || len(es.buf) == 0 {
		return nil
	}
	err := es.flushFn(es.buf)
	es.buf = es.buf[:0]
	return err
}

// finalize detaches the output before pooling the encodeState.
func (es *encodeState) finalize() []byte {
	n := len(es.buf)

	// result := makeDirtyBytes(n, n)
	// copy(result, m.buf)

	result := es.buf[:n:n]
	c := cap(es.buf)
	remain := c - n
	if remain >= encBufInitSize/4 {
		es.buf = es.buf[n:n:c]
	} else {
		es.buf = nil
	}

	releaseEncodeState(es)
	return result
}

// exec runs a compiled Blueprint through the best available VM.
func (es *encodeState) exec(bp *Blueprint, base unsafe.Pointer) error {
	if !es.inVM && es.nativeCompat {
		return es.execVM(bp, base)
	}
	return es.interp(bp, base)
}

// encodeTop dispatches to the compile-time bound encode function.
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

// buildIndentTpl materializes the VM indent template on first use.
func (es *encodeState) buildIndentTpl(prefix, indent string) {
	if es.indentTpl == nil {
		es.indentTpl = indentTplPool.Get().(*[1 + 255 + maxIndentDepth*8]byte)
	}
	es.indentTpl[0] = '\n'
	off := 1
	off += copy(es.indentTpl[off:], prefix)
	for range maxIndentDepth {
		off += copy(es.indentTpl[off:], indent)
	}
}

// encodingTarget unwraps one pointer level; nil pointers stay on the pointer codec.
func encodingTarget[T any](v T) (*EncTypeInfo, unsafe.Pointer) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() == reflect.Pointer {
		elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if elemPtr == nil {
			return EncTypeInfoOf(rt), unsafe.Pointer(&v)
		}
		return EncTypeInfoOf(rt.Elem()), elemPtr
	}
	return EncTypeInfoOf(rt), unsafe.Pointer(&v)
}

// encodingSizeHint returns the best buffer size estimate for the given type and data.
func encodingSizeHint(ti *EncTypeInfo, ptr unsafe.Pointer) int {
	if ah := int(ti.AdaptiveHint.Load()); ah > 0 {
		return ah
	}
	// First call: use SizeFn if available for data-driven prediction.
	if fn := ti.SizeFn; fn != nil {
		if predicted := fn(ptr); predicted > 0 {
			return predicted + predicted/20 // +5% headroom
		}
	}
	return ti.HintBytes
}
