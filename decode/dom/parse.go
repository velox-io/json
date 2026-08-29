// Package dom parses JSON into read-only navigation values. Values created in
// zero-copy mode reflect mutations to their caller-owned source buffer.
//
// Parse returns a Value that callers can walk through Get, Index, scalar
// accessors, and iteration without exposing its internal representation.
package dom

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/velox-io/json/decode"
	"github.com/velox-io/json/decode/option"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/jerr"
	nativendec "github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/value"
)

// Value is an alias for value.Value; dom.Parse returns one.
type Value = value.Value

// StrMode selects how the parser handles string bytes.
type StrMode = uint32

const (
	StrModeCopy     StrMode = 0 // strings copied into strArena
	StrModeZeroCopy StrMode = 1 // escape-free strings alias src; escaped copied
)

// ParseOption is an alias for option.Option, the unified functional-option
// type shared with bind and package vjson.
type ParseOption = option.Option

func WithZeroCopy() ParseOption { return option.WithZeroCopy() }

func WithStrictScan() ParseOption { return option.WithStrictScan() }

// resolveOpts resolves opts into the string mode and the scan strictness.
func resolveOpts(opts []ParseOption) (mode StrMode, strictScan bool) {
	cfg := option.Apply(opts)
	if cfg.ZeroCopy {
		mode = StrModeZeroCopy
	}
	return mode, cfg.StrictScan
}

// Parser owns reusable scratch and monotonic tape and string arenas. It is
// single-goroutine state. Each parse carves fresh arena views, and their Doc
// slice headers retain the backings across later parses and arena growth.
// Typed tape binding may publish strings that alias a Doc's StrArena.
type Parser struct {
	// structural is never aliased by the returned Value: cap-grow only.
	structural []uint32

	// Opaque scratch for the C control blocks; fixed size, allocated once.
	domState  []byte
	atofState []byte

	// domCtx is a typed, GC-scanned ABI block. Native borrows its per-call
	// pointers during RunCounted and RunBuild; Go-owned slices and retained roots
	// keep their backings reachable through those calls.
	domCtx nativendec.DOMContext

	// The arena slice headers are cursors. Native writes from their current
	// bases, then the parser advances them by the committed extents.
	tapeArena []uint64
	strArena  []byte

	// paddedSrcBuf is writable SIMD scratch (src + DOMScanPad bytes of 0x20
	// sentinel). Reused across parses (cap-grow only). Not aliased by Value.
	paddedSrcBuf []byte

	// retained pins backings replaced while preparing a call. It complements the
	// scanned domCtx until native returns; published Doc slices provide subsequent
	// Value lifetime.
	retained []unsafe.Pointer
}

// NewParser allocates a Parser with its fixed scratch regions.
func NewParser() *Parser {
	return &Parser{
		domState:  make([]byte, nativendec.DOMStateSize),
		atofState: make([]byte, nativendec.AtofStateSize),
	}
}

var parserPool = sync.Pool{New: func() any { return NewParser() }}

// scanPad is the 0x20 fill written past src so the SIMD scanner can read
// past srcLen without faulting: 0x20 (space) can never open or close a
// token, so reads into the pad are inert.
var scanPad = func() [nativendec.ScanPadding]byte {
	var p [nativendec.ScanPadding]byte
	for i := range p {
		p[i] = 0x20
	}
	return p
}()

// PaddingSize is the minimum number of 0x20 padding bytes a buffer must
// carry past its length to be usable with ParsePadded / Pad. Callers that
// manage their own buffer without calling Pad must reserve at least this
// many trailing bytes.
const PaddingSize = nativendec.ScanPadding

// Pad returns a buffer holding data followed by PaddingSize bytes of 0x20
// scan sentinel, suitable for ParsePadded.
//
// If data has at least PaddingSize bytes of spare capacity past its length,
// Pad reuses data's backing array and writes the padding in place. Otherwise
// it allocates a new buffer and copies. The returned slice has len equal to
// len(data) and cap equal to len(data) + PaddingSize.
func Pad(data []byte) []byte {
	n := len(data)
	need := n + nativendec.ScanPadding
	if cap(data) >= need {
		out := data[:need:need]
		*(*[nativendec.ScanPadding]byte)(unsafe.Pointer(unsafe.Add(unsafe.Pointer(unsafe.SliceData(out)), uintptr(n)))) = scanPad
		return out[:n:need]
	}
	out := make([]byte, need)
	copy(out, data)
	*(*[nativendec.ScanPadding]byte)(unsafe.Pointer(unsafe.Add(unsafe.Pointer(unsafe.SliceData(out)), uintptr(n)))) = scanPad
	return out[:n:need]
}

// ErrZeroCopyNeedsPadded reports that zero-copy strings require a caller-owned
// ParsePadded buffer. Parse uses reusable source scratch.
var ErrZeroCopyNeedsPadded = errors.New("dom: WithZeroCopy requires ParsePadded with a caller-owned buffer")

// Parse returns a navigation Value using copy mode and lax scanning by default.
// ParsePadded owns the zero-copy contract because Parse uses reusable source
// scratch. Monotonic arena carves remain valid through the Value lifetime.
func Parse(src []byte, opts ...ParseOption) (Value, error) {
	if !nativendec.Available {
		return Value{}, decode.ErrNoNative
	}
	p := parserPool.Get().(*Parser)
	defer parserPool.Put(p)
	return p.Parse(src, opts...)
}

// ParsePadded runs the native DOM parser over a caller-padded buffer and
// returns a navigation Value viewing the produced tape. opts select string
// handling.
//
// paddedSrc must carry at least PaddingSize bytes of 0x20 padding past its
// length; use Pad to construct it. The parser reads up to 64 bytes past the
// actual JSON end.
//
// In zero-copy mode, escape-free strings and Doc.Src alias paddedSrc. The Doc
// keeps its backing reachable; the caller preserves its bytes and backing
// allocation through the lifetime of every derived Value. Typed binding accepts
// arena-backed Values.
func ParsePadded(paddedSrc []byte, opts ...ParseOption) (Value, error) {
	if !nativendec.Available {
		return Value{}, decode.ErrNoNative
	}
	p := parserPool.Get().(*Parser)
	defer parserPool.Put(p)
	return p.ParsePadded(paddedSrc, opts...)
}

// Parse uses p's reusable source scratch and returns a fresh carve of its
// monotonic arenas. ParsePadded provides the zero-copy entry point.
func (p *Parser) Parse(src []byte, opts ...ParseOption) (Value, error) {
	n := len(src)
	if n == 0 {
		return Value{}, decode.ErrEmptyInput
	}
	mode, strictScan := resolveOpts(opts)
	if mode == StrModeZeroCopy {
		return Value{}, ErrZeroCopyNeedsPadded
	}
	// Copy src into paddedSrcBuf with DOMScanPad bytes of 0x20 sentinel
	// past srcLen so the SIMD scanner can read past the actual end. The
	// slice handed to parseTapePadded keeps len == n (SrcLen) and exposes
	// cap == n + DOMScanPad (sentinel tail).
	srcNeed := n + nativendec.ScanPadding
	srcView := p.ensureSrcBuf(srcNeed)
	copy(srcView, src)
	*(*[nativendec.ScanPadding]byte)(unsafe.Pointer(unsafe.Add(unsafe.Pointer(unsafe.SliceData(srcView)), uintptr(n)))) = scanPad
	return p.parseTapePadded(srcView[:n:srcNeed], mode, strictScan)
}

// ParsePadded uses p's reusable scratch and a caller-padded buffer. In zero-copy
// mode the Doc roots paddedSrc, and callers preserve its bytes and backing
// allocation through every derived Value's lifetime.
func (p *Parser) ParsePadded(paddedSrc []byte, opts ...ParseOption) (Value, error) {
	if err := checkPadded(paddedSrc); err != nil {
		return Value{}, err
	}
	mode, strictScan := resolveOpts(opts)
	return p.parseTapePadded(paddedSrc, mode, strictScan)
}

// checkPadded verifies the caller-supplied padded buffer carries at least
// DOMScanPad bytes of capacity past len, all 0x20.
func checkPadded(padded []byte) error {
	if cap(padded)-len(padded) < nativendec.ScanPadding {
		return fmt.Errorf("dom: padded buffer must have at least %d bytes of capacity past len", nativendec.ScanPadding)
	}
	tail := padded[len(padded) : len(padded)+nativendec.ScanPadding]
	for _, b := range tail {
		if b != 0x20 {
			return errors.New("dom: padded buffer tail must be all 0x20")
		}
	}
	return nil
}

// parseTapePadded runs the native DOM parser over a caller-padded buffer.
// paddedSrc must already carry DOMScanPad bytes of 0x20 sentinel past len
// (caller is responsible: public ParsePadded runs checkPadded first; the
// internal Parser.Parse path pads into paddedSrcBuf before calling here).
// In zero-copy mode the published doc marks ZeroCopy and its Src aliases
// paddedSrc; copy mode publishes no Src.
func (p *Parser) parseTapePadded(paddedSrc []byte, mode StrMode, strictScan bool) (value.Value, error) {
	n := len(paddedSrc)
	if n == 0 {
		return value.Value{}, decode.ErrEmptyInput
	}

	// str_arena bound is mode-independent and must match dom_ensure_capacity
	// in native/ndec/impl/ndec/dom.h, which carries the full charging
	// argument.
	//
	// str_arena at srcLen: every arena byte is a decoded string byte or a byte
	// of a number kept as text, each charged to a distinct source span. A
	// string costs decoded + 1 terminator against a body plus two quotes; a
	// kept number copies 1:1, which is the tight case. ZC writes strictly
	// less than COPY, so one figure serves both modes; and since the parse
	// allocates nothing, an undersized arena means rejecting a legal
	// document, not growing mid-parse.
	//
	// The 64-byte tail covers one SIMD chunk store past the decoded end and
	// the string terminator.
	strNeed := n + 64

	// The previous native call has completed. Published Docs now own any arena
	// backings that remain observable.
	for i := range p.retained {
		p.retained[i] = nil
	}
	p.retained = p.retained[:0]
	structCap := n + 24

	strView := p.ensureStrArena(strNeed)

	if cap(p.structural) < structCap {
		p.structural = make([]uint32, structCap)
	}
	structural := p.structural[:structCap]

	// Install per-call borrowed views and the Parser-owned native scratch.
	c := &p.domCtx
	c.Src = unsafe.SliceData(paddedSrc)
	c.SrcLen = uintptr(n)
	c.StrArena = unsafe.SliceData(strView)
	c.StrArenaCap = uintptr(strNeed)
	c.Structural = unsafe.SliceData(structural)
	c.StructuralCap = uint32(structCap)
	c.StrMode = mode
	c.ScanStrict = 0
	if strictScan {
		c.ScanStrict = 1
	}
	c.DOMState = unsafe.Pointer(unsafe.SliceData(p.domState))
	c.AtofState = unsafe.Pointer(unsafe.SliceData(p.atofState))

	// Counted sizing: one native call scans with the scalar population
	// counted and, when the arena has room for the reported bound, builds
	// the tape before returning. A shortfall returns DomTapeFull with the
	// bound in TapeNeed; the retry grows the arena and finishes through
	// the build entry, reusing the scan the first call already wrote.
	// The whole remaining arena is handed over, so the bound needs only to
	// fit the capacity already pooled.
	var tapeView []uint64
	if len(p.tapeArena) == 0 {
		// Start with a proportional guess; token-dense input retries at TapeNeed.
		p.ensureTapeArena(n/8 + 64)
	}
	tapeView = p.tapeArena
	c.Tape = unsafe.SliceData(tapeView)
	c.TapeCap = uintptr(len(tapeView))
	c.RunCounted()
	if c.Err == nativendec.DomTapeFull {
		tapeView = p.ensureTapeArena(int(c.TapeNeed))
		c.Tape = unsafe.SliceData(tapeView)
		c.TapeCap = uintptr(len(tapeView))
		c.RunBuild()
	}

	if c.Err != 0 {
		return value.Value{}, jerr.NewSyntaxError("dom: invalid JSON", 0)
	}

	// Advance the arena cursors by the bytes the native parser actually wrote.
	// The slice header is the cursor: prior Values reach the same backing
	// through their own Doc, so they stay valid across parses (and across
	// arena grows) without explicit retain.
	p.tapeArena = p.tapeArena[c.TapeLen:]
	p.strArena = p.strArena[c.StrUsed:]

	// A fresh doc per parse: the previous call's Value still points at its
	// own doc, and this parse's views are carved at a later cursor.
	doc := &valueabi.Doc{
		Tape:     tapeView[:c.TapeLen],
		StrArena: strView[:c.StrUsed],
	}
	if mode == StrModeZeroCopy {
		doc.ZeroCopy = true
		doc.Src = paddedSrc
	}
	var result value.Value
	valueabi.Store(unsafe.Pointer(&result), valueabi.Descriptor{
		Doc: doc,
		End: int32(c.TapeLen),
	})
	return result, nil
}

// domAmortize sizes a fresh arena to cover this many of the caller's computed
// needs. Each need is a bound above what a parse actually consumes, and the
// leftover serves later parses, so one backing amortizes the bound's slack
// across several parses.
const domAmortize = 3

// ensureSrcBuf returns a writable view of exactly need bytes carved from
// paddedSrcBuf. paddedSrcBuf is cap-grow only (not aliased by Value); the
// caller copies src in and writes DOMScanPad bytes of sentinel past it.
func (p *Parser) ensureSrcBuf(need int) []byte {
	if cap(p.paddedSrcBuf) >= need {
		return p.paddedSrcBuf[:need]
	}
	if old := unsafe.SliceData(p.paddedSrcBuf); old != nil {
		p.retained = append(p.retained, unsafe.Pointer(old))
	}
	p.paddedSrcBuf = make([]byte, need)
	return p.paddedSrcBuf[:need]
}

// ensureTapeArena returns a writable view of need uint64 words at the current
// tapeArena cursor, growing the arena when the remaining cap is too small.
func (p *Parser) ensureTapeArena(need int) []uint64 {
	if cap(p.tapeArena) >= need {
		return p.tapeArena[:need]
	}
	if old := unsafe.SliceData(p.tapeArena); old != nil {
		p.retained = append(p.retained, unsafe.Pointer(old))
	}
	newCap := max(domAmortize*need, need)
	p.tapeArena = make([]uint64, newCap)
	return p.tapeArena[:need]
}

// ensureStrArena returns a writable view of need bytes at the current
// strArena cursor, growing the arena when the remaining cap is too small.
func (p *Parser) ensureStrArena(need int) []byte {
	if cap(p.strArena) >= need {
		return p.strArena[:need]
	}
	if old := unsafe.SliceData(p.strArena); old != nil {
		p.retained = append(p.retained, unsafe.Pointer(old))
	}
	newCap := max(domAmortize*need, need)
	p.strArena = make([]byte, newCap)
	return p.strArena[:need]
}
