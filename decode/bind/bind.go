// Package bind decodes JSON into Go values described by a vbind.TypeTree
// using the native full-buffered binder.
//
// NewParser is for repeated decodes of one type; package Unmarshal pools
// parsers by destination type for one-shot calls.
package bind

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/velox-io/json/decode/option"
	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/rtcache"
	"github.com/velox-io/json/vbind"
)

// UnmarshalOption is an alias for option.Option, the unified functional-option
// type shared with dom and package vjson.
type UnmarshalOption = option.Option

func WithUseNumber() UnmarshalOption { return option.WithUseNumber() }

func WithDisallowUnknownFields() UnmarshalOption { return option.WithDisallowUnknownFields() }

func WithStrictScan() UnmarshalOption { return option.WithStrictScan() }

// applyOpts translates opts into the C-side opt flag bits.
func applyOpts(p *Parser, opts []UnmarshalOption) {
	cfg := option.Apply(opts)
	p.optFlags = 0
	if cfg.UseNumber {
		p.optFlags |= ndec.BindOptUseNumber
	}
	if cfg.DisallowUnknown {
		p.optFlags |= ndec.BindOptDisallowUnknown
	}
	if cfg.StrictScan {
		p.optFlags |= ndec.BindOptStrictScan
	}
	if cfg.SkipLenient {
		p.optFlags |= ndec.BindOptSkipLenient
	}
}

// Unmarshal parses JSON data into the value pointed to by v.
// v must be a non-nil pointer, directly or via an interface{}.
func Unmarshal[T any](data []byte, v T, opts ...UnmarshalOption) error {
	rt := reflect.TypeFor[T]()
	var ptr unsafe.Pointer
	var elemType reflect.Type

	if rt.Kind() == reflect.Pointer {
		ptr = *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if ptr == nil {
			return &InvalidUnmarshalError{Type: rt}
		}
		elemType = rt.Elem()
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
		elemType = rv.Elem().Type()
	} else {
		return &InvalidUnmarshalError{Type: rt}
	}
	if len(data) == 0 {
		return jerr.NewSyntaxErrorWrap("vjson: unexpected end of input", 0, io.ErrUnexpectedEOF)
	}
	sh, err := shapeFor(elemType)
	if err != nil {
		return err
	}
	p := getParser(sh)
	defer putParser(sh, p)
	applyOpts(p, opts)
	return p.unmarshal(data, ptr)
}

// Pad returns a buffer holding data followed by PaddingSize bytes of 0x20
// scan sentinel, suitable for UnmarshalPadded.
//
// If data has at least PaddingSize bytes of spare capacity past its length,
// Pad reuses data's backing array and writes the padding in place. Otherwise
// it allocates a new buffer and copies. The returned slice has len equal to
// len(data) and cap equal to len(data) + PaddingSize.
func Pad(data []byte) []byte {
	n := len(data)
	need := n + ndec.BindScanPad
	if cap(data) >= need {
		out := data[:need:need]
		*(*[ndec.BindScanPad]byte)(unsafe.Pointer(&out[n])) = scanPad
		return out[:n:need]
	}
	out := make([]byte, need)
	copy(out, data)
	*(*[ndec.BindScanPad]byte)(unsafe.Pointer(&out[n])) = scanPad
	return out[:n:need]
}

// UnmarshalPadded parses JSON data into the value pointed to by v using a
// caller-padded buffer. v must be a non-nil pointer, directly or via an
// interface{}.
//
// paddedData must carry at least PaddingSize bytes of 0x20 padding past its
// length; use Pad to construct it. The native parser reads up to 64 bytes
// past the actual JSON end.
func UnmarshalPadded[T any](paddedData []byte, v T, opts ...UnmarshalOption) error {
	rt := reflect.TypeFor[T]()
	var ptr unsafe.Pointer
	var elemType reflect.Type

	if rt.Kind() == reflect.Pointer {
		ptr = *(*unsafe.Pointer)(unsafe.Pointer(&v))
		if ptr == nil {
			return &InvalidUnmarshalError{Type: rt}
		}
		elemType = rt.Elem()
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
		elemType = rv.Elem().Type()
	} else {
		return &InvalidUnmarshalError{Type: rt}
	}
	if len(paddedData) == 0 {
		return jerr.NewSyntaxErrorWrap("vjson: unexpected end of input", 0, io.ErrUnexpectedEOF)
	}
	if err := checkPadded(paddedData); err != nil {
		return err
	}
	sh, err := shapeFor(elemType)
	if err != nil {
		return err
	}
	p := getParser(sh)
	defer putParser(sh, p)
	applyOpts(p, opts)
	return p.unmarshalPadded(paddedData, ptr)
}

// shape is the immutable binding plan and parser pool for one Go root type.
// TypeTree flags describe the graph features used by per-call arena sizing.
type shape struct {
	tt          *vbind.TypeTree
	ctxTemplate ndec.BindContext

	// rtp keys this shape's parsers in parserReserve. Held here because Put
	// sites have the shape but not the reflect.Type it was built from.
	rtp uintptr

	// inFlight distinguishes reserve-eligible surplus from the pool's sole parser.
	inFlight atomic.Int32

	parserPool sync.Pool // *Parser
}

// Parser is reusable state for one caller decoding one root type. Not safe
// for concurrent use; give each goroutine its own Parser or use package
// Unmarshal to borrow from the pool.
type Parser struct {
	*shape

	alloc      *vbind.Allocator
	machine    []byte
	atofBuf    []byte // atof_ctx storage
	structural []uint32
	padBuf     []byte
	optFlags   uint32 // per-call BIND_OPT_* bits; OR'd into Ctx.OptFlags per call

	// streamScopes is the stack of active stream scopes.
	streamScopes []streamScopeEntry
}

// NewParserForType creates a Parser bound to t. Shape construction is shared
// through the cache; mutable parser resources are owned by the returned Parser.
func NewParserForType(t reflect.Type) (*Parser, error) {
	sh, err := shapeFor(t)
	if err != nil {
		return nil, err
	}
	return newParserFromShape(sh), nil
}

// NewParser is a convenience wrapper over NewParserForType using reflect.TypeFor[T].
func NewParser[T any]() (*Parser, error) {
	return NewParserForType(reflect.TypeFor[T]())
}

func newParserFromShape(sh *shape) *Parser {
	p := &Parser{
		shape:   sh,
		alloc:   vbind.NewAllocator(sh.tt),
		machine: make([]byte, ndec.BindMachineSize),
		atofBuf: make([]byte, ndec.AtofStateSize),
	}
	// Slot classes and atof storage live for the Parser's lifetime and never
	// move, so their addresses can be burned into the bridge here. The hot
	// path refreshes only the per-call fields.
	m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
	m.Alloc.SlotClasses = unsafe.SliceData(p.alloc.Slots)
	m.Core.Atof = uintptr(unsafe.Pointer(unsafe.SliceData(p.atofBuf)))
	return p
}

// RefreshAllocatorStats is a test-only hook for the vbind SlotClass stats
// facility. It refreshes the live Batch snapshot from this Parser's Allocator
// so FormatStats can report the steady-state Batch. No-op when stats are
// disabled.
func (p *Parser) RefreshAllocatorStats() {
	vbind.RefreshFinalBatch(p.alloc)
}

// SnapshotOffsets records per-SlotClass Offset for consumption delta accounting.
// Test-only hook.
func (p *Parser) SnapshotOffsets() vbind.OffsetSnapshot {
	return p.alloc.SnapshotOffsets()
}

// ConsumedSince returns per-class slot consumption since the snapshot.
// Test-only hook.
func (p *Parser) ConsumedSince(s vbind.OffsetSnapshot) []uint32 {
	return p.alloc.ConsumedSince(s)
}

// RetainedCount reports the Allocator's current staged-backing count.
// Test-only hook.
func (p *Parser) RetainedCount() int {
	return p.alloc.RetainedCount()
}

// FinalOffsets returns the current per-SlotClass Offset. Test-only hook.
func (p *Parser) FinalOffsets() []uint32 {
	out := make([]uint32, len(p.alloc.Slots))
	for i := range p.alloc.Slots {
		out[i] = p.alloc.Slots[i].Offset
	}
	return out
}

// FinalSlotState returns the live MuBlock/Cap/Limit/Offset for each SlotClass.
// Test-only hook for debugging EWMA convergence; requires -tags vbindstats,
// otherwise all four slices are nil.
func (p *Parser) FinalSlotState() (muBlock, cap, limit, offset []uint32) {
	return vbind.LiveSlotState(p.alloc)
}

// Unmarshal parses data into dst. dst must be a non-nil *T matching the type
// the Parser was created for; the hot path does not recheck that contract.
func (p *Parser) Unmarshal(data []byte, dst any, opts ...UnmarshalOption) error {
	rt := reflect.TypeOf(dst)
	if rt == nil || rt.Kind() != reflect.Pointer {
		return &InvalidUnmarshalError{Type: rt}
	}
	dstPtr := (*gort.GoIface)(unsafe.Pointer(&dst)).Data
	if dstPtr == nil {
		return &InvalidUnmarshalError{Type: rt}
	}
	if len(data) == 0 {
		return jerr.NewSyntaxErrorWrap("vjson: unexpected end of input", 0, io.ErrUnexpectedEOF)
	}
	applyOpts(p, opts)
	return p.unmarshal(data, dstPtr)
}

// UnmarshalPadded parses paddedData into dst. dst must be a non-nil *T
// matching the Parser's type.
//
// paddedData must carry at least PaddingSize bytes of 0x20 padding past its
// length; use Pad to construct it. The parser reads up to 64 bytes past the
// actual JSON end.
func (p *Parser) UnmarshalPadded(paddedData []byte, dst any, opts ...UnmarshalOption) error {
	rt := reflect.TypeOf(dst)
	if rt == nil || rt.Kind() != reflect.Pointer {
		return &InvalidUnmarshalError{Type: rt}
	}
	dstPtr := (*gort.GoIface)(unsafe.Pointer(&dst)).Data
	if dstPtr == nil {
		return &InvalidUnmarshalError{Type: rt}
	}
	if len(paddedData) == 0 {
		return jerr.NewSyntaxErrorWrap("vjson: unexpected end of input", 0, io.ErrUnexpectedEOF)
	}
	if err := checkPadded(paddedData); err != nil {
		return err
	}
	applyOpts(p, opts)
	return p.unmarshalPadded(paddedData, dstPtr)
}

var shapeCache rtcache.Cache[*shape]

// parserReserve retains surplus parsers by root type across sync.Pool eviction.
var parserReserve = rtcache.NewObjPool(parserFootprint, 0, 0)

// parserFootprint reports the retained bytes of a pooled Parser, for the
// reserve's admission and budget accounting.
func parserFootprint(p *Parser) int {
	return len(p.machine) + len(p.atofBuf) + cap(p.padBuf) + cap(p.structural)*4 +
		p.alloc.Footprint()
}

// getParser borrows a Parser for this shape and counts it as in flight, so
// putParser can tell whether the pool has surplus to spare. Callers must pair it
// with putParser.
func getParser(sh *shape) *Parser {
	if bypassParserCache {
		return sh.parserPool.Get().(*Parser)
	}
	sh.inFlight.Add(1)
	return sh.parserPool.Get().(*Parser)
}

// putParser reserves p only when another parser for this shape remains in
// flight. This preserves one parser in the per-shape pool for the fast path.
func putParser(sh *shape, p *Parser) {
	if bypassParserCache {
		sh.parserPool.Put(p)
		return
	}
	if sh.inFlight.Add(-1) > 0 && parserReserve.Offer(sh.rtp, p) {
		return
	}
	sh.parserPool.Put(p)
}

// SetParserReserveEnabled toggles the resident parser floor and clears it when
// disabled. It is a test hook for observing parser reclamation.
func SetParserReserveEnabled(on bool) { parserReserve.SetEnabled(on) }

// shapeFor returns the canonical shape for t within this process.
func shapeFor(t reflect.Type) (*shape, error) {
	rtp := uintptr(gort.TypePtr(t))
	return shapeCache.GetOrBuild(rtp, func() (*shape, error) {
		return buildShape(rtp, t)
	})
}

func buildShape(rtp uintptr, t reflect.Type) (*shape, error) {
	tt, err := vbind.TypeTreeOf(t)
	if err != nil {
		return nil, err
	}
	sh := &shape{
		tt:  tt,
		rtp: rtp,
		ctxTemplate: ndec.BindContext{
			Types:      unsafe.SliceData(tt.Types),
			RootType:   tt.Root,
			AnyTypeIdx: tt.AnyTypeIdx,
			Polys:      unsafe.SliceData(tt.Polys),
			// The bind machine reads the field-name lookup blob from TypeMeta.
			TypeMeta: unsafe.SliceData(tt.TypeMeta),
		},
	}

	// A warm parser from the reserve outlives the pool's GC-driven eviction, so
	// New prefers one over building cold.
	sh.parserPool.New = func() any {
		if p := parserReserve.Take(sh.rtp); p != nil {
			return p
		}
		return newParserFromShape(sh)
	}
	return sh, nil
}

// scanPad is the 0x20 fill written past src so the SIMD scanner can read
// past srcLen without faulting: 0x20 (space) can never open or close a
// token, so reads into the pad are inert.
var scanPad = func() [ndec.BindScanPad]byte {
	var p [ndec.BindScanPad]byte
	for i := range p {
		p[i] = 0x20
	}
	return p
}()

// padInputInto returns data backed by p.padBuf with BindScanPad bytes of
// scan padding in capacity. Reusing padBuf avoids allocating on same-type
// steady-state parses.
func (p *Parser) padInputInto(data []byte) []byte {
	n := len(data)
	need := n + ndec.BindScanPad
	if cap(p.padBuf) < need {
		newCap := need
		if g := cap(p.padBuf) * 2; g > newCap {
			newCap = g
		}
		p.padBuf = make([]byte, newCap)
	}
	buf := p.padBuf[:need]
	copy(buf, data)
	*(*[ndec.BindScanPad]byte)(unsafe.Pointer(&buf[n])) = scanPad
	return buf[:n:need]
}

// syncStructural sizes the structural-index scan buffer for this parse
// (srcLen+64 u32 slots) and syncs the base/cap into the Alloc ABI view.
// The buffer is pooled on the Parser; cap grows monotonically across parses.
func syncStructural(structural *[]uint32, allocABI *ndec.BindAllocator, srcLen int) {
	needCap := srcLen + 64
	if cap(*structural) < needCap {
		*structural = make([]uint32, needCap)
	} else {
		*structural = (*structural)[:needCap]
	}
	allocABI.Structural = unsafe.SliceData(*structural)
	allocABI.StructuralCap = uint32(len(*structural))
}

// syncStrArena ensures the string arena has room for this parse and syncs
// the writable view's base/cap into the Alloc ABI view. The arena is
// pooled on the Allocator; cursor is amortized across parses.
func syncStrArena(alloc *vbind.Allocator, allocABI *ndec.BindAllocator, srcLen int) {
	alloc.EnsureStrArena(srcLen)
	allocABI.StrArena = (*byte)(unsafe.SliceData(alloc.StrArena))
	allocABI.StrArenaCap = uint64(cap(alloc.StrArena))
}

// sealFailedStrArena advances past bytes written before failure. Caller-visible
// values may already reference them, so later calls begin after that extent.
func sealFailedStrArena(alloc *vbind.Allocator, m *ndec.BindMachine) {
	if used := m.Core.StrUsed; used <= uint64(cap(alloc.StrArena)) {
		alloc.CommitStrArena(int(used))
	}
}

// syncTapeArena installs a fresh ABI view with TapeUsed reset. Its capacity is
// limited to the 31-bit seam distance so unchecked seam writes remain
// representable.
func syncTapeArena(alloc *vbind.Allocator, allocABI *ndec.BindAllocator, words int) error {
	alloc.EnsureTapeArena(words)
	if n := cap(alloc.TapeArena); n > maxSeamDistance {
		return fmt.Errorf("vjson: input too large: a %d-word tape arena exceeds the %d-word seam distance limit",
			n, maxSeamDistance)
	}
	allocABI.TapeArena = (*uint64)(unsafe.SliceData(alloc.TapeArena))
	allocABI.TapeArenaCap = uint64(cap(alloc.TapeArena))
	allocABI.TapeUsed = 0
	return nil
}

// maxSeamDistance is the 31-bit word distance encoded by each seam field. It
// must match TAPE_SEAM_MASK in the native tape ABI and seamMask in value.
const maxSeamDistance = 0x7FFFFFFF

// syncDoc installs the Doc pointer native writes into each Value descriptor.
// publishDoc fills its arena views after native determines their extents. Those
// views retain the same bases used to encode tape and string offsets.
func syncDoc(allocABI *ndec.BindAllocator) *valueabi.Doc {
	doc := &valueabi.Doc{}
	allocABI.ValueDoc = unsafe.Pointer(doc)
	return doc
}

// publishDoc exposes the source and arena extents whose bases native encoded.
// Tape is stored last to publish the completed document.
func publishDoc(doc *valueabi.Doc, alloc *vbind.Allocator, m *ndec.BindMachine, src []byte) {
	if doc == nil {
		return
	}
	doc.StrArena = alloc.StrArena[:m.Core.StrUsed]
	doc.Src = src
	doc.Tape = alloc.TapeArena[:m.Alloc.TapeUsed]
}

func (p *Parser) unmarshal(data []byte, rootDst unsafe.Pointer) error {
	return p.unmarshalPadded(p.padInputInto(data), rootDst)
}

// checkPadded verifies the caller-supplied padded buffer meets the scan
// sentinel contract: at least BindScanPad bytes of capacity past len, all 0x20.
func checkPadded(paddedData []byte) error {
	if cap(paddedData)-len(paddedData) < ndec.BindScanPad {
		return fmt.Errorf("vjson: padded buffer must have at least %d bytes of capacity past len", ndec.BindScanPad)
	}
	tail := paddedData[len(paddedData) : len(paddedData)+ndec.BindScanPad]
	for _, b := range tail {
		if b != 0x20 {
			return errors.New("vjson: padded buffer tail must be all 0x20")
		}
	}
	return nil
}

func (p *Parser) unmarshalPadded(src []byte, rootDst unsafe.Pointer) error {
	srcLen := len(src)

	m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
	m.Core.Phase = 0 // Select the native root bootstrap phase.

	// Each call starts with an empty stream scope stack.
	p.streamScopes = p.streamScopes[:0]

	m.Ctx = p.ctxTemplate // Copy the context template and fill per-call fields.
	m.Ctx.OptFlags |= p.optFlags
	m.Ctx.Src = unsafe.SliceData(src) // Borrowed for the native call.
	m.Ctx.SrcLen = uint64(srcLen)
	m.Ctx.RootDst = rootDst // Borrowed for the native call.

	alloc := p.alloc
	allocABI := &m.Alloc

	// Align the real allocator (alloc) with the machine's ABI view (allocABI):
	// each field below mirrors allocator-owned state into the C-visible struct.
	syncStructural(&p.structural, allocABI, srcLen)
	syncStrArena(alloc, allocABI, srcLen)
	allocABI.StrGenStart = 0
	m.Core.StrUsed = 0

	// The native scan computes the tape bound before writing. Go supplies an
	// initial guess and grows to TapeNeed on a BindYieldTapeArena yield.
	var valueDoc *valueabi.Doc
	if p.tt.HasValueField || p.tt.HasPolyField {
		// TAPE_DUAL includes the split-tape surcharge in the scan bound.
		m.Ctx.OptFlags |= ndec.BindOptSizeTape
		if p.tt.HasSplitTape {
			m.Ctx.OptFlags |= ndec.BindOptTapeDual
		}
		ceiling := srcLen
		if p.tt.HasSplitTape {
			if k := p.tt.SplitTapeSites; k != vbind.SplitTapeSitesUnbounded {
				ceiling = srcLen + 2*k
			} else {
				ceiling = 2 * srcLen
			}
		}
		ceiling += 3
		// Native clamps its measured bound to this ceiling before requesting growth.
		allocABI.TapeNeed = uint32(ceiling)
		// Allocate a bounded initial guess; token-dense input grows to TapeNeed.
		guess := min(srcLen/8+64, ceiling)
		// Reuse the completed-scan high-water mark with growth headroom.
		if hw := alloc.TapeHighWater(); hw > 0 {
			guess = min(max(guess, hw+hw/4+16), ceiling)
		}
		if err := syncTapeArena(alloc, allocABI, guess); err != nil {
			return err
		}
	} else {
		allocABI.TapeArena = nil
		allocABI.TapeArenaCap = 0
		allocABI.TapeUsed = 0
	}
	// The doc is gated more tightly than the tape arena. A poly field needs the
	// arena for its intermediate tape, but that tape is scratch: it is consumed
	// by the case walker and never published. Only a reachable KindValue can put
	// a Value in the destination, and only then is there something to publish a
	// doc for.
	if p.tt.HasValueField {
		valueDoc = syncDoc(allocABI)
	} else {
		allocABI.ValueDoc = nil
	}

	syncDeferredDrain(alloc, allocABI)

	syncMapBuf(alloc, allocABI)

	defer func() {
		alloc.Release() // Publish native writes, then stage reusable backings.
		// Clear borrowed ABI pointers before the machine is reused. KeepAlive
		// preserves their Go owners through the final stores.
		m.Ctx.Src = nil
		m.Ctx.RootDst = nil
		allocABI.ValueDoc = nil
		runtime.KeepAlive(rootDst) // Preserve ownership through ABI pointer clearing.
		runtime.KeepAlive(src)
	}()

	done, err := p.driveBind(m, src, func() bool { return false })
	if err != nil {
		sealFailedStrArena(alloc, m)
		return err
	}
	if done {
		// Deferred callbacks complete before map slots publish their values.
		if m.Alloc.DeferredDrainUsed > 0 {
			if err := drainDeferredRecords(p, m, src); err != nil {
				sealFailedStrArena(alloc, m)
				return err
			}
		}
		if m.Alloc.MapBufUsed > 0 {
			// Object close may leave complete entries after the final
			// BindYieldFlushMap, so completion drains the remainder.
			if err := drainAllMapSlots(m); err != nil {
				sealFailedStrArena(alloc, m)
				return err
			}
		}

		// Native coordinates are relative to the current arena views.
		publishDoc(valueDoc, alloc, m, src)

		alloc.CommitStrArena(int(m.Core.StrUsed))
		alloc.CommitTapeArena(int(m.Alloc.TapeUsed))
		// A completed scan contributes the reusable sizing bound.
		alloc.NoteTapeBound(int(m.Alloc.TapeNeed))
		return nil
	}
	// driveBind with a never-stop predicate only returns on done or error.
	return nil
}

// driveBind is the unified main loop. It repeatedly runs BindParseRun and
// dispatches the resulting yield via serveYield until stop() returns true or
// native signals done (BindYieldNone). stop() is consulted after each
// BindParseRun (before serveYield) and after each serveYield, so a BreakSignal
// stashed by an inner handler mid-drive is observed before the next BindParseRun
// would cross a stream boundary.
//
// All BindParseRun driving flows through this function: the top-level unmarshal
// loop passes a never-stop predicate, Item.Decode (non-leaf) passes scope.stop to
// bind one element body, and Scope.nextBatch (leaf) passes scope.stop to fill
// one batch. The recursion (Value -> driveBind -> serveYield -> serveStreamBatch
// -> OnRead -> Value -> driveBind) is what lets a non-leaf element's body bind
// activate nested stream handlers.
func (p *Parser) driveBind(m *ndec.BindMachine, src []byte, stop func() bool) (done bool, err error) {
	for {
		ndec.BindParseRun(unsafe.Pointer(m))
		if stop() {
			return false, nil
		}
		done, err := p.serveYield(m, src)
		if err != nil {
			return false, err
		}
		if done {
			return true, nil
		}
		if stop() {
			return false, nil
		}
	}
}

// serveYield services Yield.PendingAction. BindYieldNone returns done so each
// caller can perform its own completion sequence.
func (p *Parser) serveYield(m *ndec.BindMachine, src []byte) (done bool, err error) {
	switch m.Yield.PendingAction {
	case ndec.BindYieldNone:
		return true, nil
	case ndec.BindYieldError:
		// Reclaim the tails this parse borrowed and will never close. Purely a
		// memory optimization.
		sealOpenSlices(p, m)
		return false, mkBindErr(p, m, src)
	case ndec.BindYieldBlockFull:
		return false, p.alloc.ServeNewBlock(m.Yield.Arg0, m.Yield.Arg1)
	case ndec.BindYieldTapeArena:
		// Grow to the scanned bound and resume at BIND_PHASE_ROOT_SCANNED. Tape
		// writes begin after this yield, and syncTapeArena enforces the seam limit.
		return false, syncTapeArena(p.alloc, &m.Alloc, int(m.Alloc.TapeNeed))
	case ndec.BindYieldSliceGrow:
		if p.alloc.Tree.IsStreamType(m.Yield.Arg0) {
			return false, p.serveStreamSliceGrow(m, src)
		}
		sc, hdr := p.slotForGrow(m)
		if err := p.alloc.ServeSliceGrow(sc, hdr); err != nil {
			return false, err
		}
		p.finishGrow(m, sc, hdr)
		return false, nil
	case ndec.BindYieldRecBatchRefill:
		sc, hdr := p.slotForGrow(m)
		if err := sc.RecBatch().ServeRefill(p.alloc, m.Yield.Arg1, hdr); err != nil {
			return false, err
		}
		p.finishGrow(m, sc, hdr)
		return false, nil
	case ndec.BindYieldRecBatchBypass:
		sc, hdr := p.slotForGrow(m)
		if err := sc.RecBatch().ServeBypass(p.alloc, m.Yield.Arg1, hdr); err != nil {
			return false, err
		}
		p.finishGrow(m, sc, hdr)
		return false, nil
	case ndec.BindYieldFlushMap:
		// Drain Unmarshaler records first: closure writes must land before
		// the map drain copies slots into *hmaps.
		if m.Alloc.DeferredDrainUsed > 0 {
			if err := drainDeferredRecords(p, m, src); err != nil {
				return false, err
			}
		}
		return false, p.serveFlushMap(m)
	case ndec.BindYieldFlushUnmarshal:
		return false, drainDeferredRecords(p, m, src)
	case ndec.BindYieldTapeBindValue:
		return false, p.serveTapeBindValue(m)
	default:
		return false, errors.New("bind: unknown yield action")
	}
}

// slotForGrow resolves the slot class and slice header for BindYieldSliceGrow,
// BindYieldRecBatchRefill, and BindYieldRecBatchBypass. Arg0 identifies the
// slice type whose allocation class indexes Slots.
func (p *Parser) slotForGrow(m *ndec.BindMachine) (*vbind.SlotClass, *gort.SliceHeader) {
	hdr := (*gort.SliceHeader)(m.Yield.Target)
	ci := uint32(p.alloc.Tree.TypeMeta[m.Yield.Arg0].SliceMeta().AllocClass)
	return &p.alloc.Slots[ci], hdr
}

// finishGrow writes the Go-side cur_aux recovery: the next write position is
// data + len*child_size. For the array_begin init path hdr.Len is 0 (backing
// start); for the array_value grow path hdr.Len is the count already memmoved
// (next slot). C resumes at BIND_PHASE_ARRAY_VALUE with this in the spill.
func (p *Parser) finishGrow(m *ndec.BindMachine, sc *vbind.SlotClass, hdr *gort.SliceHeader) {
	m.Core.CurAux = uintptr(hdr.Data) + uintptr(hdr.Len)*uintptr(sc.ElemSize)
}
