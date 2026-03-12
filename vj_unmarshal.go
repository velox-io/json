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
	// Arena may still be referenced by zero-copy strings; keep the
	// block if < half-full, else release it for a fresh allocation.
	if sc.arenaOff > arenaBlockSize/2 {
		sc.arenaData = nil
		sc.arenaOff = 0
	}
	for _, a := range sc.ptrAllocs {
		a.reset()
	}

	sc.useNumber = false
	sc.copyString = false
	p.pool.Put(sc)
}

func newParser() *Parser {
	return &Parser{
		// Start with half-size arena so first Put() keeps it.
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

// Unmarshal parses JSON data into v using the single-pass scanner.
// v must be a non-nil pointer. Strings may reference data (zero-copy);
// the caller must not modify data after this call.
func Unmarshal[T any](data []byte, v *T, opts ...UnmarshalOption) error {
	sc := parsers.Get()
	defer parsers.Put(sc)
	for _, o := range opts {
		o(sc)
	}
	return unmarshalInto(sc, data, v)
}

func unmarshalInto(sc *Parser, data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}
	if len(data) == 0 {
		return newSyntaxError("vjson: unexpected end of JSON input", 0)
	}

	elemType := rv.Elem().Type()
	ti := GetCodec(elemType)
	ptr := rv.UnsafePointer()

	idx := skipWS(data, 0)
	newIdx, err := sc.scanValue(data, idx, ti, ptr)
	if err != nil {
		if err == errUnexpectedEOF {
			return newSyntaxError("vjson: unexpected end of input", len(data))
		}
		return err
	}

	// Verify no trailing non-whitespace
	newIdx = skipWS(data, newIdx)
	if newIdx < len(data) {
		return newSyntaxError(fmt.Sprintf("vjson: invalid character %q after top-level value", data[newIdx]), newIdx)
	}
	return nil
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
