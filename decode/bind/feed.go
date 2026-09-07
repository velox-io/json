package bind

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/native/ndec"
)

// Streaming input driver. The native engine consumes one window at a time:
// the driver runs the native window scanner, mounts the stable prefix's
// structurals and window coordinates, and re-enters the streaming bind entry
// until native reports completion, an error, or BIND_YIELD_INPUT.
//
// Window layout: [relocated tail | newly read bytes | 64 bytes of 0x20 pad].
// Every window starts at the first structural the binder has not consumed
// (or the scanner's withheld tail when the cursor sits at the sentinel), and
// the scan state restarts fresh each window.

const (
	feedInitialWindow = 1 << 17
	feedArenaFloor    = 4096
	feedMaxZeroReads  = 128
)

type feedState struct {
	r         io.Reader
	win       []byte // window buffer; capacity includes the 64-byte pad
	n         int    // logical window length
	base      uint64 // absolute document offset of win[0]; only error positions consume it
	final     bool
	sawEOF    bool
	zeroReads int
	scan      ndec.BindWindowScan
	raw       []byte // raw scratch for deferred values crossing a window edge

	// doc roots the Value document staged in the machine for the current
	// value. The machine's ValueDoc field sits in the noscan block, so this
	// typed field is the Go root until the destination's Value descriptors
	// take over at publication.
	doc *valueabi.Doc

	// liveFor names the machine whose window state (cursor pair, window
	// coordinates, structural indexes, and Ctx source view) still describes
	// win. A completed value leaves it set: the machine cursor already
	// addresses the next value's first token, so the next Decode binds
	// without rescanning. A parser rebuild or an error path clears it.
	liveFor *ndec.BindMachine

	// consumed is the window offset of the first byte the binder has not
	// finished with: the machine cursor's value at the last value boundary.
	// Bytes before it are dead; a refill compacts from it.
	consumed int
}

// fill performs one read into the window and tracks finality. One read per
// input yield lets the reader's own chunking size the windows. A read that
// returns EOF marks the stream exhausted without asserting finality:
// finality is asserted on the next fill, so bytes are first scanned as
// non-final and a truncated tail withholds instead of erroring. The error
// surfaces when the binder, demanding input past the withheld tail, drives
// the refill cycle that mounts the window as final.
func (f *feedState) fill() error {
	if f.sawEOF {
		f.final = true
		return nil
	}
	usable := len(f.win) - ndec.BindScanPad
	if f.n >= usable {
		grown := make([]byte, len(f.win)*2)
		copy(grown, f.win[:f.n])
		f.win = grown
		usable = len(f.win) - ndec.BindScanPad
	}
	n, err := f.r.Read(f.win[f.n:usable])
	f.n += n
	if err == io.EOF {
		f.sawEOF = true
		return nil
	}
	if err != nil {
		return err
	}
	if n == 0 {
		f.zeroReads++
		if f.zeroReads > feedMaxZeroReads {
			return errors.New("bind: reader made no progress")
		}
	} else {
		f.zeroReads = 0
	}
	return nil
}

func (f *feedState) src() []byte {
	return f.win[:f.n]
}

// mountWindow makes the current window the machine's live input: it scans
// the window and seeds the machine with the stable prefix. The first call
// runs before the initial native entry; later calls service BIND_YIELD_INPUT.
func (f *feedState) mountWindow(p *Parser, m *ndec.BindMachine) error {
	// The binder's sentinel reads address src[len]; keep the pad contract.
	*(*[ndec.BindScanPad]byte)(unsafe.Pointer(&f.win[f.n])) = scanPad

	// The cursor pair holds interior pointers into the structural buffer;
	// syncStructural clears them on regrow and reinstalls the pair below.
	syncStructural(m, &p.structural, f.n)
	// String arena growth happens only at this safe point: native re-reads the
	// arena base and str_used after every yield. Growth preserves content so
	// tape string offsets written before the growth stay resolvable in the new
	// backing.
	need := int(m.Core.StrUsed) + f.n + 64
	if need < feedArenaFloor {
		need = feedArenaFloor
	}
	if displaced := p.alloc.GrowStrArenaPreserve(int(m.Core.StrUsed), need); displaced != nil {
		if err := recordStrProv(m, displaced, int(m.Core.StrUsed)); err != nil {
			return err
		}
	}
	m.Alloc.StrArena = (*byte)(unsafe.SliceData(p.alloc.StrArena))
	m.Alloc.StrArenaCap = uint64(cap(p.alloc.StrArena))

	// Tape words consumed from one window are bounded by two per source byte
	// plus slack, so the per-window reservation keeps the vd submachine's
	// unchecked bump writes safe. Growth preserves content, keeping container
	// indices and seams written earlier valid. The ABI view syncs on every
	// mount: a reused allocator view can satisfy the need without growth.
	if p.tt.HasValueField || p.tt.HasPolyField {
		tapeNeed := int(m.Alloc.TapeUsed) + 2*f.n + 64
		if cap(p.alloc.TapeArena) < tapeNeed {
			p.alloc.GrowTapeArenaPreserve(int(m.Alloc.TapeUsed), tapeNeed)
			if c := cap(p.alloc.TapeArena); c > maxSeamDistance {
				return fmt.Errorf("vjson: input too large: a %d-word tape arena exceeds the %d-word seam distance limit",
					c, maxSeamDistance)
			}
		}
		m.Alloc.TapeArena = (*uint64)(unsafe.SliceData(p.alloc.TapeArena))
		m.Alloc.TapeArenaCap = uint64(cap(p.alloc.TapeArena))
	}

	wctx := ndec.BindWindowScanCtx{
		Src:        unsafe.SliceData(f.win[:f.n]),
		Len:        uintptr(f.n),
		OutIndexes: unsafe.SliceData(p.structural),
		Capacity:   uint32(len(p.structural)),
		IsFinal:    boolU32(f.final),
		Strict:     boolU32(m.Ctx.OptFlags&ndec.BindOptStrictScan != 0),
	}
	ndec.WindowScanRun(unsafe.Pointer(&wctx))
	f.scan = wctx.Result

	if f.scan.Status == ndec.WindowInvalid {
		// Scan-level failures mirror the contiguous scan error: a
		// syntax error without a source position.
		return jerr.NewSyntaxError("bind: syntax error", 0)
	}

	m.Ctx.Src = unsafe.SliceData(f.win[:f.n])
	m.Ctx.SrcLen = uint64(f.n)

	base := unsafe.Pointer(m)
	*(*uint64)(unsafe.Add(base, ndec.BindMachineWindowBaseOffset)) = f.base
	*(*uint8)(unsafe.Add(base, ndec.BindMachineWindowFinalOffset)) = uint8(b2i(f.final))
	// Spans recorded from this window end at the stable prefix; the sentinel
	// offset itself is the full window length and can swallow the tail.
	*(*uint32)(unsafe.Add(base, ndec.BindMachineWindowStableEndOffset)) = f.scan.StableEnd
	if f.raw != nil {
		// One run's appends cover disjoint byte ranges of this window: a
		// completing value's tail ends at or before the next deferred value's
		// start, so n covers the run's writes.
		used := *(*uint32)(unsafe.Add(base, ndec.BindMachineRawUsedOffset))
		if need := int(used) + f.n; need > cap(f.raw) {
			grown := make([]byte, max(2*cap(f.raw), need))
			copy(grown, f.raw[:used])
			m.DropRawView()
			f.raw = grown
		}
		*m.RawArena() = unsafe.Pointer(unsafe.SliceData(f.raw))
		*(*uint32)(unsafe.Add(base, ndec.BindMachineRawCapOffset)) = uint32(cap(f.raw))
	}

	cursor := m.CursorPair()
	cursor[0] = unsafe.Pointer(unsafe.SliceData(p.structural))
	cursor[1] = unsafe.Pointer(unsafe.SliceData(p.structural[f.scan.NIdx:]))
	return nil
}

// feedCursorOff reads the machine cursor's window offset: the first
// structural the binder has not consumed, or a scan sentinel value at the
// stable edge.
func feedCursorOff(m *ndec.BindMachine) int {
	return int(*(*uint32)(m.CursorPair()[0]))
}

// unconsumedOff reports the window offset of the first byte the binder
// has not finished with: the machine cursor's value clamped to the stable
// end, because a cursor resting on the scan sentinel addresses the window
// length while a withheld tail may start earlier.
func (f *feedState) unconsumedOff(m *ndec.BindMachine) int {
	off := feedCursorOff(m)
	if stable := int(f.scan.StableEnd); off > stable {
		off = stable
	}
	return off
}

// compact moves the window suffix starting at cut to the window head.
// Bytes before cut are dead: consumed by the binder or skipped past.
func (f *feedState) compact(cut int) {
	if cut > 0 {
		if tail := f.n - cut; tail > 0 {
			copy(f.win, f.win[cut:f.n])
			f.n = tail
		} else {
			f.n = 0
		}
		f.base += uint64(cut)
	}
	f.consumed = 0
}

// relocate consumes the window prefix the binder no longer needs and keeps
// the remainder at the window head. The cut point is the first unconsumed
// structural rather than the scanner's stable end: a yield may leave stable
// tokens unconsumed (the phase2 key wait), and those bytes must survive into
// the next window. Cursor slots at or past cursor_end address the sentinels,
// whose value is the window length, so EOF relocations are unchanged.
func (f *feedState) relocate(m *ndec.BindMachine) {
	f.compact(f.unconsumedOff(m))
}

// serveInput relocates the window to the first byte the binder still needs
// and mounts the next window.
func (f *feedState) serveInput(p *Parser, m *ndec.BindMachine) error {
	f.relocate(m)
	// fill may replace the window backing, whose only root is f.win, so the
	// machine's view of it must go first; mountWindow republishes Ctx.Src from
	// the post-fill window.
	m.DropWindowView()
	if err := f.fill(); err != nil {
		return err
	}
	return f.mountWindow(p, m)
}

// checkTrailing rejects non-whitespace input past the completed root, both in
// the window and still unread from the reader, matching the contiguous
// trailing error. The streaming engine reports root completion without
// judging the remainder, so this check is the single-value driver's own; a
// value-per-call driver skips it and reads the remainder as its next value.
func (f *feedState) checkTrailing(m *ndec.BindMachine) error {
	// The scan starts at the machine cursor, the first byte the binder has
	// not consumed: a stable trailing token reports its own position, like
	// the contiguous engine. The scanner's withheld tail and the unread
	// reader bytes are trailing data too.
	for i := f.unconsumedOff(m); i < f.n; i++ {
		if c := f.win[i]; c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return jerr.NewSyntaxError("bind: trailing data after value", int(f.base)+i)
		}
	}
	var buf [512]byte
	off := 0
	for !f.sawEOF {
		n, err := f.r.Read(buf[:])
		for i := 0; i < n; i++ {
			if c := buf[i]; c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				return jerr.NewSyntaxError("bind: trailing data after value", int(f.base)+f.n+off+i)
			}
		}
		off += n
		if err == io.EOF {
			f.sawEOF = true
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func boolU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UnmarshalFeed decodes one JSON value from io.Reader into dst
func (p *Parser) UnmarshalFeed(r io.Reader, dst any, opts ...UnmarshalOption) error {
	rt := reflect.TypeOf(dst)
	if rt == nil || rt.Kind() != reflect.Pointer {
		return &InvalidUnmarshalError{Type: rt}
	}
	dstPtr := (*gort.GoIface)(unsafe.Pointer(&dst)).Data
	if dstPtr == nil {
		return &InvalidUnmarshalError{Type: rt}
	}
	applyOpts(p, opts)
	return p.unmarshalFeed(r, dstPtr)
}

// feedBegin resets the per-value machine state for one streaming parse into
// rootDst. A live window (f.liveFor == m) continues from the machine cursor:
// Ctx keeps the mounted source view, and the string and tape arenas re-base
// onto the allocator views the previous value's commits advanced. The bytes
// from the cursor to the window end bound this value's arena needs until a
// refill re-checks them, mirroring mountWindow's whole-window sizing.
func (p *Parser) feedBegin(m *ndec.BindMachine, rootDst unsafe.Pointer, f *feedState) error {
	m.Core.Phase = 0 // Select the streaming root bootstrap.

	p.streamScopes = p.streamScopes[:0]

	cont := f.liveFor == m
	var src *byte
	var srcLen uint64
	if cont {
		src, srcLen = m.Ctx.Src, m.Ctx.SrcLen
	}
	m.Ctx = p.ctxTemplate
	m.Ctx.OptFlags |= p.optFlags
	m.Ctx.RootDst = rootDst
	if cont {
		m.Ctx.Src, m.Ctx.SrcLen = src, srcLen
	}

	allocABI := &m.Alloc
	// Only a reachable KindValue can put a Value in the destination, so the
	// doc gate is tighter than the tape gates below: a poly tree's tape is
	// scratch consumed by the case walker and never published.
	if p.tt.HasValueField {
		f.doc = syncDoc(allocABI)
	} else {
		f.doc = nil
		allocABI.ValueDoc = nil
	}
	allocABI.TapeArena = nil
	allocABI.TapeArenaCap = 0
	allocABI.TapeUsed = 0
	allocABI.TapeNeed = 0
	allocABI.StrGenStart = 0
	m.Core.StrUsed = 0
	// The raw scratch cursor pairs with the caller's buffer; native resets
	// raw_depth and raw_scratch_start at the streaming root bootstrap.
	mbase := unsafe.Pointer(m)
	*(*uint32)(unsafe.Add(mbase, ndec.BindMachineRawUsedOffset)) = 0
	// Truncate rather than merely zero the count: the positioning loop that
	// precedes this call (Decoder.Decode) can serve an input yield, and that
	// mount may grow the string arena and record a retired generation. Those
	// entries belong to no value and are discarded here, but their bases must
	// be nil'd while the backings are still retained, or the next append's
	// pointer store hands the write barrier a base whose span is already gone.
	m.TruncateStrProv(0)

	if cont {
		remaining := f.n - f.unconsumedOff(m)
		need := remaining + 64
		if need < feedArenaFloor {
			need = feedArenaFloor
		}
		p.alloc.GrowStrArenaPreserve(0, need)
		allocABI.StrArena = (*byte)(unsafe.SliceData(p.alloc.StrArena))
		allocABI.StrArenaCap = uint64(cap(p.alloc.StrArena))

		if p.tt.HasValueField || p.tt.HasPolyField {
			tapeNeed := 2*remaining + 64
			if cap(p.alloc.TapeArena) < tapeNeed {
				p.alloc.GrowTapeArenaPreserve(0, tapeNeed)
				if c := cap(p.alloc.TapeArena); c > maxSeamDistance {
					return fmt.Errorf("vjson: input too large: a %d-word tape arena exceeds the %d-word seam distance limit",
						c, maxSeamDistance)
				}
			}
			allocABI.TapeArena = (*uint64)(unsafe.SliceData(p.alloc.TapeArena))
			allocABI.TapeArenaCap = uint64(cap(p.alloc.TapeArena))
		}
	}

	syncDeferredDrain(p.alloc, allocABI)
	syncMapBuf(p.alloc, allocABI)
	return nil
}

// feedDrive runs the streaming engine until the root completes or errors.
// Root close with input remaining is completion: the streaming engine leaves
// the trailing judgment to the driver.
func (p *Parser) feedDrive(m *ndec.BindMachine) error {
	return p.driveBind(m, func() bool { return false })
}

// feedFinish completes one bound value: deferred callbacks before map slots
// publish, then doc publication and arena commits. It runs before any window
// relocation: source-backed record spans reference current window
// coordinates.
func (p *Parser) feedFinish(m *ndec.BindMachine, f *feedState) error {
	if m.Alloc.DeferredDrainUsed > 0 {
		// Records from the final window use its source span; values that
		// crossed an edge use the raw scratch.
		if err := drainDeferredRecords(p, m); err != nil {
			return err
		}
	}
	if m.Alloc.MapBufUsed > 0 {
		if err := drainAllMapSlots(m); err != nil {
			return err
		}
	}
	// Arena coordinates are relative to the final backings; growth copied
	// every earlier generation into them. Feed Values never borrow the
	// input, so the Doc carries no source view.
	publishDoc(p, f.doc, m)
	f.doc = nil
	p.alloc.CommitStrArena(int(m.Core.StrUsed))
	p.alloc.CommitTapeArena(int(m.Alloc.TapeUsed))
	return nil
}

func (p *Parser) unmarshalFeed(r io.Reader, rootDst unsafe.Pointer) error {
	m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
	f := &feedState{r: r, win: make([]byte, feedInitialWindow+ndec.BindScanPad)}
	if p.tt.HasRawSpan {
		f.raw = make([]byte, feedInitialWindow)
	}
	p.feed = f
	if err := p.feedBegin(m, rootDst, f); err != nil {
		return err
	}

	defer func() {
		p.feed = nil
		// Nil provenance entries while their retired backings are still
		// retained; the release below drops them.
		m.TruncateStrProv(0)
		p.alloc.Release()
		// The window and raw scratch die with the feed state while the pooled
		// machine lives on, so their views must go while f still roots the
		// backings.
		m.DropWindowView()
		m.Ctx.RootDst = nil
		m.Alloc.ValueDoc = nil
		if f.raw != nil {
			m.DropRawView()
		}
		runtime.KeepAlive(rootDst)
		runtime.KeepAlive(f.win)
		runtime.KeepAlive(f.raw)
	}()

	if err := f.fill(); err != nil {
		return err
	}
	if err := f.mountWindow(p, m); err != nil {
		return err
	}

	if err := p.feedDrive(m); err != nil {
		sealFailedStrArena(p.alloc, m)
		return err
	}
	if err := f.checkTrailing(m); err != nil {
		sealFailedStrArena(p.alloc, m)
		return err
	}
	if err := p.feedFinish(m, f); err != nil {
		sealFailedStrArena(p.alloc, m)
		return err
	}
	return nil
}
