package vjson

import (
	stdjson "encoding/json"
	"math"
	"sync"
	"unsafe"

	nativendec "github.com/velox-io/json/native/ndec"
)

// Valid reports whether data is a valid JSON encoding.
// It accepts a single JSON value optionally surrounded by whitespace.
// An empty or whitespace-only input is not valid.
//
// Equivalent to encoding/json.Valid.
func Valid(data []byte) bool {
	if nativendec.Available && len(data) <= maxValidNativeLen {
		return validNative(data)
	}
	return stdjson.Valid(data)
}

// maxValidNativeLen keeps the structural index arithmetic (u32 byte offsets
// plus the scanner's 24-slot slack) in range. Larger inputs fall back to the
// Go scanner.
const maxValidNativeLen = math.MaxUint32 - 24

// validScratch holds the padded source copy and the structural index buffer
// the native entry borrows. Both are cap-grow only and pooled across calls.
type validScratch struct {
	padded     []byte
	structural []uint32
}

var validScratchPool = sync.Pool{New: func() any { return new(validScratch) }}

// validPad is the 0x20 fill written past srcLen. The validation walkers read
// past the document end: the sentinel structural slots address it, and the
// string scanner overshoots its chunk. 0x20 can never open or close a token,
// so those reads are inert.
var validPad = func() [nativendec.ScanPadding]byte {
	var p [nativendec.ScanPadding]byte
	for i := range p {
		p[i] = 0x20
	}
	return p
}()

// validNative runs the ndec validation entry: a SIMD structural scan that
// rejects raw control bytes in strings (malformed UTF-8 stays accepted, as
// in encoding/json), then a read-only walk of the structural index that
// checks the complete grammar.
func validNative(data []byte) bool {
	n := len(data)
	if n == 0 {
		return false
	}
	s := validScratchPool.Get().(*validScratch)
	defer validScratchPool.Put(s)

	srcNeed := n + nativendec.ScanPadding
	if cap(s.padded) < srcNeed {
		s.padded = make([]byte, srcNeed)
	}
	view := s.padded[:srcNeed]
	copy(view, data)
	*(*[nativendec.ScanPadding]byte)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(view)), uintptr(n))) = validPad

	structCap := n + 24
	if cap(s.structural) < structCap {
		s.structural = make([]uint32, structCap)
	}

	var ctx nativendec.ValidContext
	ctx.Src = unsafe.SliceData(view)
	ctx.SrcLen = uintptr(n)
	ctx.Structural = unsafe.SliceData(s.structural)
	ctx.StructuralCap = uint32(structCap)
	ctx.Run()
	return ctx.Err == 0
}
