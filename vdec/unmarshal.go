package vdec

import (
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/gort"
)

const fastCacheSize = 32 // must be power of two

type fastCacheEntry struct {
	key uintptr // rtype pointer
	val *DecTypeInfo
}

type fastCache [fastCacheSize]atomic.Pointer[fastCacheEntry]

func fastCacheIndex(rtp uintptr) uintptr {
	const magic = 0x9e3779b97f4a7c15
	return (rtp * magic) >> (64 - 5) // 5 = log2(32)
}

func (c *fastCache) get(t reflect.Type) *DecTypeInfo {
	rtp := uintptr(gort.TypePtr(t))
	idx := fastCacheIndex(rtp)
	if p := c[idx].Load(); p != nil && p.key == rtp {
		return p.val
	}
	dti := DecTypeInfoOf(t)
	c[idx].Store(&fastCacheEntry{key: rtp, val: dti})
	return dti
}

var decFastCache fastCache

var parsers = parserPool{
	pool: sync.Pool{
		New: func() any { return newParser() },
	},
}

func init() {
	parsers.pool.Put(newParser())
}

type parserPool struct {
	pool sync.Pool
}

func (p *parserPool) Get() *Parser {
	return p.pool.Get().(*Parser)
}

func (p *parserPool) Put(sc *Parser) {
	if sc.arenaOff > arenaBlockSize/2 {
		sc.arenaData = nil
		sc.arenaOff = 0
	}
	if len(sc.ptrAllocs) > 0 {
		for _, a := range sc.ptrAllocs {
			a.Reset()
		}
	}

	sc.useNumber = false
	sc.copyString = false
	p.pool.Put(sc)
}

func newParser() *Parser {
	return &Parser{
		arenaData: make([]byte, arenaBlockSize/2),
		ptrAllocs: make(map[unsafe.Pointer]*TypeAllocator, 8),
	}
}

// UnmarshalOption configures Unmarshal behavior.
type UnmarshalOption func(*Parser)

// WithUseNumber causes numbers in interface{} fields to be decoded as
// json.Number instead of float64.
func WithUseNumber() UnmarshalOption {
	return func(sc *Parser) { sc.useNumber = true }
}

// WithCopyString causes all decoded strings to be heap-copied instead of
// zero-copy referencing the input buffer.
func WithCopyString() UnmarshalOption {
	return func(sc *Parser) { sc.copyString = true }
}

// Unmarshal parses JSON data into v.
// v must be a non-nil pointer. Strings may reference data (zero-copy);
// the caller must not modify data after this call.
func Unmarshal[T any](data []byte, v T, opts ...UnmarshalOption) error {
	rt := reflect.TypeFor[T]()

	var ptr unsafe.Pointer
	var dti *DecTypeInfo

	if rt.Kind() == reflect.Pointer {
		ptr = *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if ptr == nil {
			return &InvalidUnmarshalError{Type: rt}
		}
		dti = decFastCache.get(rt.Elem())
	} else if rt.Kind() == reflect.Interface {
		rv := reflect.ValueOf(v)
		if !rv.IsValid() {
			return &InvalidUnmarshalError{Type: nil}
		}
		if rv.Kind() != reflect.Pointer {
			return &InvalidUnmarshalError{Type: rv.Type()}
		}
		if rv.IsNil() {
			return &InvalidUnmarshalError{Type: rv.Type()}
		}
		ptr = rv.UnsafePointer()
		dti = decFastCache.get(rv.Elem().Type())
	} else {
		return &InvalidUnmarshalError{Type: rt}
	}

	sc := parsers.Get()
	defer parsers.Put(sc)
	for _, o := range opts {
		o(sc)
	}

	idx := skipWS(data, 0)
	newIdx, scanErr := sc.scanValue(data, idx, dti, ptr)
	if scanErr != nil {
		return wrapUnexpectedEOF(scanErr, len(data))
	}

	newIdx = skipWS(data, newIdx)
	if newIdx < len(data) {
		return newSyntaxError(fmt.Sprintf("vjson: invalid character %q after top-level value", data[newIdx]), newIdx)
	}
	return nil
}

// wrapUnexpectedEOF converts the sentinel errUnexpectedEOF into a proper
// SyntaxError wrapping io.ErrUnexpectedEOF, for encoding/json compatibility.
func wrapUnexpectedEOF(err error, offset int) error {
	if err == errUnexpectedEOF {
		return newSyntaxErrorWrap("vjson: unexpected end of input", offset, io.ErrUnexpectedEOF)
	}
	return err
}
