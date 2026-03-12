package vjson

import (
	"reflect"
	"sync"
	"unsafe"
)

const (
	arenaBlockSize = 8192 // 8KB arena blocks
	arenaInlineMax = 1024 // decoded results <= 1KB → copy to arena
	scratchBufSize = 2048 // 2KB scratch buffer for single-pass decoding
)

var defaultPool = parserPool{
	pool: sync.Pool{
		New: func() any { return newParser() },
	},
}

func init() {
	// Pre-warm: ensure the first Unmarshal call doesn't pay allocation cost.
	defaultPool.pool.Put(newParser())
}

type parserPool struct {
	pool sync.Pool
}

func (p *parserPool) Get() *Parser {
	return p.pool.Get().(*Parser)
}

func (p *parserPool) Put(sc *Parser) {
	// Arena memory may still be referenced by strings in the caller's result
	// (via unsafe.String from unescape). We must not reset arenaOff to 0,
	// as that would let the next Unmarshal overwrite live string data.
	//
	// If more than half the arena block remains free, keep it and continue
	// appending after the current offset — old data stays intact.
	// Otherwise, release the block so the next parse gets a fresh one.
	if sc.arenaOff > arenaBlockSize/2 {
		sc.arenaData = nil
		sc.arenaOff = 0
	}
	for _, a := range sc.ptrAllocs {
		a.reset()
	}
	p.pool.Put(sc)
}

func newParser() *Parser {
	return &Parser{
		arenaData: make([]byte, arenaBlockSize/2),
		ptrAllocs: make(map[unsafe.Pointer]*rtypeAllocator, 8),
	}
}

// Unmarshal parses JSON data into v using the single-pass scanner.
// v must be a non-nil pointer. Strings in v may reference the data buffer
// directly (zero-copy); the caller must not modify data after calling Unmarshal.
func Unmarshal[T any](data []byte, v *T) error {
	sc := defaultPool.Get()
	defer defaultPool.Put(sc)
	return unmarshalInto(sc, data, v)
}

func unmarshalInto(sc *Parser, data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errNotPointer
	}
	if len(data) == 0 {
		return errEmptyInput
	}

	elemType := rv.Elem().Type()
	ti := GetDecoder(elemType)
	ptr := rv.UnsafePointer()

	idx := skipWS(data, 0)
	newIdx, err := sc.scanValue(data, idx, ti, ptr)
	if err != nil {
		return err
	}

	// Verify no trailing non-whitespace
	newIdx = skipWS(data, newIdx)
	if newIdx < len(data) {
		return errSyntax
	}
	_ = newIdx
	return nil
}

// Parser is a reusable single-pass JSON scanner.
type Parser struct {
	scratch   [128]byte                          // for LookupFieldBytes lowercase
	buf       [scratchBufSize]byte               // reusable scratch for single-pass decoding
	arenaData []byte                             // current arena block
	arenaOff  int                                // next free offset in arenaData
	ptrAllocs map[unsafe.Pointer]*rtypeAllocator // per-type batch allocators for pointer fields
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
