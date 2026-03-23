package vjson

import (
	"fmt"
	"reflect"
	"sync"
	"unsafe"
)

const (
	arenaBlockSize = 8192 // 8KB arena blocks
	arenaInlineMax = 1024 // small decoded strings kept in arena to reduce allocs
	scratchBufSize = 4096 // reusable scratch buffer size for single-pass decoding
)

var parsers = parserPool{
	pool: sync.Pool{
		New: func() any { return newParser() },
	},
}

func init() {
	// Pre-warm: ensure the first Unmarshal call doesn't pay allocation cost.
	parsers.pool.Put(newParser())
}

type parserPool struct {
	pool sync.Pool
}

func (p *parserPool) Get() *Parser {
	return p.pool.Get().(*Parser)
}

func (p *parserPool) Put(sc *Parser) {
	// Keep lightly-used arena blocks; drop heavily-used ones.
	if sc.arenaOff > arenaBlockSize/2 {
		sc.arenaData = nil
		sc.arenaOff = 0
	}
	if len(sc.ptrAllocs) > 0 {
		for _, a := range sc.ptrAllocs {
			a.reset()
		}
	}

	sc.useNumber = false
	sc.copyString = false
	p.pool.Put(sc)
}

func newParser() *Parser {
	return &Parser{
		// Start smaller so the first pooled return is retained.
		arenaData: make([]byte, arenaBlockSize/2),
		ptrAllocs: make(map[unsafe.Pointer]*rtypeAllocator, 8),
	}
}

// UnmarshalOption configures Unmarshal behavior.
type UnmarshalOption func(*Parser)

// WithUseNumber causes numbers in interface{} fields to be decoded as
// [json.Number] instead of float64, preserving the original text
// representation and avoiding precision loss for large integers.
func WithUseNumber() UnmarshalOption {
	return func(sc *Parser) { sc.useNumber = true }
}

// WithCopyString causes all decoded strings to be heap-copied instead of
// zero-copy referencing the input buffer. Per-field opt-in: `json:"name,copy"`.
func WithCopyString() UnmarshalOption {
	return func(sc *Parser) { sc.copyString = true }
}

// Unmarshal parses JSON data into v.
// v must be a non-nil pointer. Strings may reference data (zero-copy);
// the caller must not modify data after this call.
func Unmarshal[T any](data []byte, v T, opts ...UnmarshalOption) error {
	if len(data) == 0 {
		return newSyntaxError("vjson: unexpected end of JSON input", 0)
	}

	ti, ptr, err := unmarshalTarget[T](&v)
	if err != nil {
		return err
	}

	sc := parsers.Get()
	defer parsers.Put(sc)
	for _, o := range opts {
		o(sc)
	}

	idx := skipWS(data, 0)
	newIdx, scanErr := sc.scanValue(data, idx, ti, ptr)
	if scanErr != nil {
		if scanErr == errUnexpectedEOF {
			return newSyntaxError("vjson: unexpected end of input", len(data))
		}
		return scanErr
	}

	newIdx = skipWS(data, newIdx)
	if newIdx < len(data) {
		return newSyntaxError(fmt.Sprintf("vjson: invalid character %q after top-level value", data[newIdx]), newIdx)
	}
	return nil
}

// unmarshalTarget resolves the TypeInfo and data pointer for decoding.
// When T is a pointer, it unwraps one level. When T is interface{},
// the concrete value must be a non-nil pointer.
// Returns InvalidUnmarshalError on invalid targets.
func unmarshalTarget[T any](vp *T) (*TypeInfo, unsafe.Pointer, error) {
	rt := reflect.TypeFor[T]()

	if rt.Kind() == reflect.Pointer {
		elemPtr := *(*unsafe.Pointer)(unsafe.Pointer(vp))
		if elemPtr == nil {
			return nil, nil, &InvalidUnmarshalError{Type: rt}
		}
		return codecCacheUnmarshal.getCodec(rt.Elem()), elemPtr, nil
	}

	if rt.Kind() == reflect.Interface {
		rv := reflect.ValueOf(*vp)
		if !rv.IsValid() {
			return nil, nil, &InvalidUnmarshalError{Type: nil}
		}
		if rv.Kind() != reflect.Pointer {
			return nil, nil, &InvalidUnmarshalError{Type: rv.Type()}
		}
		if rv.IsNil() {
			return nil, nil, &InvalidUnmarshalError{Type: rv.Type()}
		}
		return codecCacheUnmarshal.getCodec(rv.Elem().Type()), rv.UnsafePointer(), nil
	}

	return nil, nil, &InvalidUnmarshalError{Type: rt}
}

// Parser is a reusable single-pass JSON scanner.
type Parser struct {
	scratchBuf [scratchBufSize]byte               // reusable scratch for decoding
	arenaData  []byte                             // current arena block
	arenaOff   int                                // next free offset in arenaData
	ptrAllocs  map[unsafe.Pointer]*rtypeAllocator // per-type batch allocators for pointer fields
	useNumber  bool                               // decode numbers in interface{} as json.Number
	copyString bool                               // copy all strings instead of zero-copy
}

// arenaAlloc allocates size bytes from the arena.
// If the current arena block is full, a new one is allocated.
// GC manages arena block lifetimes — no manual freeing needed.
func (sc *Parser) arenaAlloc(size int) []byte {
	if sc.arenaData == nil || sc.arenaOff+size > len(sc.arenaData) {
		sc.arenaData = make([]byte, arenaBlockSize)
		sc.arenaOff = 0
	}
	p := sc.arenaData[sc.arenaOff : sc.arenaOff+size]
	sc.arenaOff += size
	return p
}
