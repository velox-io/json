package bind

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/native/ndec"
)

// Decoder reads consecutive JSON values from an io.Reader through the
// streaming input engine. The window persists across values: each completed
// value relocates the window to the next value's first structural, and the
// document offset base accumulates so error positions stay absolute.
// A Decoder is single goroutine, like Parser.
type Decoder struct {
	r       io.Reader
	fs      *feedState // persistent window state; created lazily
	winSize int        // initial window capacity excluding the scan pad

	err        error // sticky; io.EOF is never stored
	useNumber  bool
	skipErrors func(err error) bool

	parser   *Parser // owned by the Decoder; rebuilt when the target type changes
	lastType reflect.Type
}

// DecoderOption configures a Decoder at construction.
type DecoderOption func(*Decoder)

// WithBufferSize sets the initial window size. The window still grows by
// doubling when a single value exceeds it.
func WithBufferSize(size int) DecoderOption {
	return func(d *Decoder) {
		if size > 0 {
			d.winSize = size
		}
	}
}

// WithExpectedSize hints the expected value size; it raises the initial
// window to fit one value without regrowth.
func WithExpectedSize(size int) DecoderOption {
	return func(d *Decoder) {
		if size > d.winSize {
			d.winSize = size
		}
	}
}

// WithSkipErrors installs a predicate that recovers from decode errors: when
// fn returns true for an error, the decoder skips to the next line and
// returns the error non-sticky, leaving the stream usable.
func WithSkipErrors(fn func(err error) bool) DecoderOption {
	return func(d *Decoder) {
		d.skipErrors = fn
	}
}

// NewDecoder creates a Decoder reading JSON values from r. Construction does
// not read from r; the first read happens on the first Decode or More.
func NewDecoder(r io.Reader, opts ...DecoderOption) *Decoder {
	d := &Decoder{r: r, winSize: feedInitialWindow}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// UseNumber makes subsequent Decodes decode numbers into json.Number when
// they land in an interface{} target, like encoding/json.
func (d *Decoder) UseNumber() {
	d.useNumber = true
}

// Err returns the first sticky error. io.EOF is never sticky.
func (d *Decoder) Err() error {
	return d.err
}

// parserFor returns the Parser for elemType, rebuilding it when the target
// type changes. Shape construction is shared through the cache; the machine
// and allocator are owned by this Decoder. A rejected type leaves the
// current Parser untouched.
func (d *Decoder) parserFor(elemType reflect.Type) (*Parser, error) {
	if d.parser != nil && d.lastType == elemType {
		return d.parser, nil
	}
	sh, err := shapeFor(elemType)
	if err != nil {
		return nil, err
	}
	d.parser = newParserFromShape(sh)
	d.lastType = elemType
	return d.parser, nil
}

func (d *Decoder) ensureFeed() *feedState {
	if d.fs == nil {
		d.fs = &feedState{r: d.r, win: make([]byte, d.winSize+ndec.BindScanPad)}
	}
	return d.fs
}

// skipToValue advances past whitespace to the next value byte, reading on
// demand. io.EOF means the input ended in whitespace. A whitespace-only
// window is discarded whole so whitespace-heavy streams never grow the
// window.
func (d *Decoder) skipToValue(f *feedState) error {
	for {
		if feedSkipWS(f.win[:f.n]) < f.n {
			return nil
		}
		if f.sawEOF {
			return io.EOF
		}
		f.base += uint64(f.n)
		f.n = 0
		f.consumed = 0
		if err := f.fill(); err != nil {
			return err
		}
	}
}

func feedSkipWS(b []byte) int {
	for i, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
		default:
			return i
		}
	}
	return len(b)
}

// Decode reads the next JSON value from the reader into v, which must be a
// non-nil pointer. Trailing data is left for the next Decode. io.EOF is never
// sticky, a type mismatch that consumed its value returns non-sticky, and
// every other decode or reader error sticks.
func (d *Decoder) Decode(v any) error {
	if d.err != nil {
		return d.err
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &jerr.InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}
	ptr := rv.UnsafePointer()
	p, err := d.parserFor(rv.Elem().Type())
	if err != nil {
		return err
	}
	f := d.ensureFeed()
	if f.raw == nil && p.tt.HasRawSpan {
		f.raw = make([]byte, d.winSize)
	}
	m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
	p.feed = f
	p.optFlags = 0
	if d.useNumber {
		p.optFlags |= ndec.BindOptUseNumber
	}

	defer func() {
		p.feed = nil
		// Nil provenance entries while their retired backings are still
		// retained; the release below drops them.
		m.TruncateStrProv(0)
		p.alloc.Release()
		m.Ctx.RootDst = nil
		m.Alloc.ValueDoc = nil
		runtime.KeepAlive(ptr)
		runtime.KeepAlive(f.win)
		runtime.KeepAlive(f.raw)
	}()

	if f.liveFor != m {
		// The machine holds no window state: compact to the unconsumed
		// suffix, skip leading whitespace, and mount a fresh window.
		f.compact(f.consumed)
		if err = d.skipToValue(f); err != nil {
			if err == io.EOF {
				return io.EOF
			}
			d.err = err
			return err
		}
		if err = p.feedBegin(m, ptr, f); err != nil {
			return d.failDecode(f, m, err)
		}
		if err = f.mountWindow(p, m); err != nil {
			return d.failDecode(f, m, err)
		}
		f.liveFor = m
	} else {
		// The machine keeps the mounted window and its cursor addresses
		// the next value's first token, so a stable value binds without
		// rescanning. The arena cursors zero first so a refill inside the
		// positioning loop sizes from this value's start rather than the
		// previous value's totals.
		m.Core.StrUsed = 0
		m.Alloc.TapeUsed = 0
		for {
			cursor := m.CursorPair()
			if cursor[0] != cursor[1] {
				break // The next value's first token is stable.
			}
			// A final window resolved every withheld tail at its mount,
			// so a cursor resting at its stable edge faces only whitespace.
			if f.final {
				return io.EOF
			}
			if err = f.serveInput(p, m); err != nil {
				return d.failDecode(f, m, err)
			}
		}
		if err = p.feedBegin(m, ptr, f); err != nil {
			return d.failDecode(f, m, err)
		}
	}

	if err := p.feedDrive(m); err != nil {
		sealFailedStrArena(p.alloc, m)
		return d.failDecode(f, m, err)
	}
	if err := p.feedFinish(m, f); err != nil {
		sealFailedStrArena(p.alloc, m)
		return d.failDecode(f, m, err)
	}
	// The root closed with the machine cursor at the next value's first
	// token (or the stable edge), so the window and its scan stay mounted
	// and the next Decode binds without rescanning.
	f.consumed = f.unconsumedOff(m)
	return nil
}

// failDecode applies the error policy. A type mismatch that reached
// document_end consumed the failed value and left the machine cursor at the
// next value, so it returns non-sticky, like encoding/json; a mismatch that
// aborted mid-value, and any syntax error, leaves the stream position
// undefined and sticks. The skipErrors hook takes precedence and discards
// through the next newline.
func (d *Decoder) failDecode(f *feedState, m *ndec.BindMachine, err error) error {
	if d.skipErrors != nil && d.skipErrors(err) {
		if skipErr := d.skipToNewline(f); skipErr != nil {
			return skipErr
		}
		return err
	}
	var ute *UnmarshalTypeError
	if errors.As(err, &ute) && m.Core.Phase == ndec.BindPhaseDocumentEnd {
		return err
	}
	d.err = err
	return err
}

// skipToNewline discards window and reader bytes through the next '\n'.
// io.EOF means the input ended before a newline. Reader and zero-progress
// errors are sticky. The failed value's machine state dies with the skip;
// the next Decode remounts the window from the surviving suffix.
func (d *Decoder) skipToNewline(f *feedState) error {
	f.liveFor = nil
	for {
		for i := f.consumed; i < f.n; i++ {
			if f.win[i] == '\n' {
				copy(f.win, f.win[i+1:f.n])
				f.n -= i + 1
				f.base += uint64(i + 1)
				f.consumed = 0
				return nil
			}
		}
		f.base += uint64(f.n)
		f.n = 0
		f.consumed = 0
		if f.sawEOF {
			return io.EOF
		}
		if err := f.fill(); err != nil {
			d.err = err
			return err
		}
	}
}

// More reports whether another value is available, reading past whitespace
// on demand. It returns false after EOF, a sticky error, or a reader error.
func (d *Decoder) More() bool {
	if d.err != nil {
		return false
	}
	f := d.ensureFeed()
	if p := d.parser; p != nil {
		m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
		if f.liveFor == m {
			cursor := m.CursorPair()
			if cursor[0] != cursor[1] {
				return true // A stable value token is already in the window.
			}
			// The stable edge: the bytes from the first unconsumed offset
			// to the window end are the only candidates for another value.
			// The raw refill below moves window content, so the machine
			// mount ends here; the next Decode remounts.
			f.liveFor = nil
			f.compact(f.unconsumedOff(m))
		} else {
			f.compact(f.consumed)
		}
	} else {
		f.compact(f.consumed)
	}
	for {
		if feedSkipWS(f.win[:f.n]) < f.n {
			return true
		}
		if f.sawEOF {
			return false
		}
		f.base += uint64(f.n)
		f.n = 0
		f.consumed = 0
		if f.fill() != nil {
			return false
		}
	}
}

// Buffered returns a reader over the bytes not yet consumed. The bytes are
// copied: later window relocations must not corrupt an outstanding reader.
func (d *Decoder) Buffered() io.Reader {
	f := d.fs
	if f == nil || f.n <= f.consumed {
		return bytes.NewReader(nil)
	}
	out := make([]byte, f.n-f.consumed)
	copy(out, f.win[f.consumed:f.n])
	return bytes.NewReader(out)
}

// DecodeValue reads the next JSON value into v.
func DecodeValue[T any](d *Decoder, v *T) error {
	return d.Decode(v)
}
