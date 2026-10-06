package bind

import (
	"errors"
	"io"
	"unsafe"

	"github.com/velox-io/json/internal/gbind"
	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/vbind"
)

// Entry adapters for the pure-Go engine (internal/gbind), taken wherever
// useGoCore selects it. The engine binds one contiguous value; these adapters
// feed it the input each API path holds and translate its errors with the
// native vocabulary.

// goHost serves the engine's Host calls from the Parser's hook table and
// stream scope stack.
type goHost Parser

func (h *goHost) Deferred(kind vbind.Kind, typeIdx uint16, target unsafe.Pointer, data []byte, borrowed bool, docOff int64) error {
	return applyDeferred((*Parser)(h), kind, typeIdx, target, data, borrowed, docOff)
}

func (h *goHost) PushStreamScope(addr unsafe.Pointer, elemHasStream bool) int {
	return (*Parser)(h).pushStreamScope(addr, elemHasStream)
}

func (h *goHost) PopStreamScope(idx int) { (*Parser)(h).popStreamScope(idx) }

func (h *goHost) PeekAnyScopeBreak() *stream.BreakSignal { return (*Parser)(h).peekAnyScopeBreak() }

func (h *goHost) StashScopeBreak(sig *stream.BreakSignal) bool {
	return (*Parser)(h).stashScopeBreak(sig)
}

// goBind runs the engine over src at document offset base. alias is the
// caller-owned buffer src was taken from, the zero-copy alias target.
func (p *Parser) goBind(src, alias []byte, base uint64, tape bool, dst unsafe.Pointer) (settled bool, err error) {
	in := gbind.Input{Src: src, Alias: alias, Base: base, Opt: p.optFlags, Tape: tape, State: &p.gstate, Alloc: p.alloc}
	// The release cadence matches the native driver's: one release point per
	// bind, charged with the document bytes it decoded.
	p.alloc.NoteParsedBytes(len(src))
	settled, err = gbind.Bind(p.goPlan(), (*goHost)(p), &in, dst)
	p.alloc.Release()
	if e, ok := err.(*gbind.Error); ok {
		// The Go engine binds one complete value per call, so its source is
		// always value-rooted and the mismatch identity can be rebuilt by
		// walking it. It records no token-end delta; the value-rooted
		// window supplies the extent directly.
		err = mkBindErr(p, bindErrInfo{Kind: e.Kind, Detail: e.Detail, Pos: e.Pos,
			TypeIdx: e.TypeIdx, Target: e.Target}, src, base, true)
	}
	return settled, err
}

// goUnmarshal binds one complete document.
func (p *Parser) goUnmarshal(data []byte, dst unsafe.Pointer, alias []byte) error {
	_, err := p.goBind(data, alias, 0, false, dst)
	return err
}

// goUnmarshalFeed drains the reader first; the bytes are private, so
// nothing aliases the caller.
func (p *Parser) goUnmarshalFeed(r io.Reader, dst unsafe.Pointer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return p.goUnmarshal(data, dst, nil)
}

// goUnmarshalValue binds the Value's JSON text, as the engine has no tape
// walker. Numbers keep their source text or their binary kind, so the
// result is the same, and errors carry no position, as tape-walk errors
// carry none. The tape was scanned when it was built, so strict scanning
// is dropped.
func (p *Parser) goUnmarshalValue(desc *valueabi.Descriptor, dst unsafe.Pointer) error {
	p.optFlags &^= ndec.BindOptStrictScan
	text, err := gbind.ValueText(desc)
	if err != nil {
		return err
	}
	_, err = p.goBind(text, nil, 0, true, dst)
	return err
}

// goDecode is Decoder.Decode on the Go engine: the window first
// accumulates one complete value, found by a resumable extent scan, and
// the value then binds in one pass. Stream handlers therefore run once the
// value has arrived rather than while it streams.
func (d *Decoder) goDecode(p *Parser, ptr unsafe.Pointer) error {
	f := d.ensureFeed()
	f.liveFor = nil
	f.compact(f.consumed)
	if err := d.skipToValue(f); err != nil {
		if err == io.EOF {
			return io.EOF
		}
		d.err = err
		return err
	}
	start := feedSkipWS(f.win[:f.n])
	sc := gdec.ExtentScanner{Pos: start}
	end := 0
	for {
		var done bool
		if end, done = sc.Scan(f.win[:f.n], f.sawEOF); done {
			break
		}
		if f.sawEOF {
			// A truncated value binds as is and reports the end of input.
			end = f.n
			break
		}
		if err := f.fill(); err != nil {
			d.err = err
			return err
		}
	}
	settled, err := p.goBind(f.win[start:end], nil, f.base+uint64(start), false, ptr)
	// The boundary cut drops the whitespace after the value, as the
	// native relocation does.
	next := end + feedSkipWS(f.win[end:f.n])
	if err == nil {
		f.consumed = next
		return nil
	}
	if d.skipErrors != nil && d.skipErrors(err) {
		f.consumed = start
		if skipErr := d.skipToNewline(f); skipErr != nil {
			return skipErr
		}
		return err
	}
	// A mismatch surfacing after the whole value bound leaves the stream at
	// the next value, like encoding/json; anything else sticks.
	var ute *UnmarshalTypeError
	if settled && errors.As(err, &ute) {
		f.consumed = next
		return err
	}
	d.err = err
	return err
}
