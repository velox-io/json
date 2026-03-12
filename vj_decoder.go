package vjson

import (
	"bytes"
	"io"
	"reflect"
	"unsafe"
)

const (
	defaultBufSize = 128 * 1024 // default read buffer size (128KB)
	minReadSize    = 512        // minimum free space to attempt a read
)

// DecoderOption configures a [Decoder].
type DecoderOption func(*Decoder)

// WithBufferSize sets the initial read buffer size (default 128 KB).
// The buffer may grow beyond this size to accommodate large JSON values.
func WithBufferSize(size int) DecoderOption {
	return func(d *Decoder) {
		if size > 0 {
			d.bufSize = size
		}
	}
}

// WithSkipErrors enables skip-on-error recovery for NDJSON streams.
// When a decode error occurs (syntax error, type mismatch, etc.), fn is
// called with the error. If fn returns true, the decoder skips to the next
// newline and continues; the current Decode call returns the original error
// so callers can log or count it. If fn returns false (or is nil), the
// error becomes sticky and all subsequent Decode calls fail.
func WithSkipErrors(fn func(err error) bool) DecoderOption {
	return func(d *Decoder) {
		d.skipErrors = fn
	}
}

// DecoderUseNumber causes numbers in interface{} fields to be decoded as
// [json.Number] instead of float64, preserving the original text
// representation and avoiding precision loss for large integers.
func DecoderUseNumber() DecoderOption {
	return func(d *Decoder) {
		d.useNumber = true
	}
}

// Decoder reads and decodes JSON values from an input stream.
//
// Each call to [Decoder.Decode] parses the next complete value directly via
// scanValue (single-pass). When the value spans a buffer boundary, the
// decoder grows the buffer and retries.
//
// Buffers are never compacted in-place — decoded zero-copy strings may
// reference them via unsafe.String. Old buffers become eligible for GC
// once no live strings point into them.
type Decoder struct {
	r io.Reader

	buf    []byte // read buffer
	bufLen int    // valid bytes in buf
	scanAt int    // next byte to consume

	bufSize int // configured initial buffer size

	err error // sticky error
	eof bool  // reader returned io.EOF

	// Buffer-size history for nextBufSize() averaging.
	lastBufSize int
	prevBufSize int

	// Value-size prediction (see predictedValueSize).
	lastValueSize int
	prevValueSize int
	maxSeenSize   int // high-water mark with slow decay

	// Buffer fitness (see maybeNewBuffer).
	valuesInBuf    int // values decoded in the current buffer
	minGoodBufSize int // sticky floor: smallest buf that held ≥ 2 values

	skipErrors func(err error) bool // nil = sticky error (default)
	useNumber  bool                 // decode numbers in interface{} as json.Number

	// Owned parser — acquired lazily from the pool on first Decode,
	// reused across calls to avoid per-value sync.Pool round-trips.
	sc *Parser

	// Type cache — when consecutive Decode calls use the same type
	// (the common streaming case), skip the sync.Map lookup in GetCodec.
	lastType reflect.Type
	lastTI   *TypeInfo
}

// NewDecoder creates a Decoder that reads from r.
func NewDecoder(r io.Reader, opts ...DecoderOption) *Decoder {
	d := &Decoder{
		r:       r,
		bufSize: defaultBufSize,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// parser returns the decoder's owned Parser, acquiring one from the pool
// on first use. Between Decode calls the parser's arena and batch allocators
// are cleaned up (matching the semantics of parserPool.Put) so that
// previously decoded strings are not overwritten.
func (d *Decoder) parser() *Parser {
	if d.sc == nil {
		d.sc = defaultPool.Get()
		d.sc.useNumber = d.useNumber
		return d.sc
	}
	// Inter-decode cleanup: same logic as parserPool.Put, but without
	// the pool round-trip. Arena blocks that are more than half-used are
	// released so the next value gets a fresh arena; partially-used blocks
	// are kept to avoid immediate reallocation.
	sc := d.sc
	if sc.arenaOff > arenaBlockSize/2 {
		sc.arenaData = nil
		sc.arenaOff = 0
	}
	for _, a := range sc.ptrAllocs {
		a.reset()
	}
	return sc
}

// releaseParser returns the owned parser to the pool when the Decoder
// reaches a terminal state (EOF or sticky error). This ensures the Parser
// is recycled even if the Decoder is abandoned without explicit cleanup.
func (d *Decoder) releaseParser() {
	if d.sc != nil {
		defaultPool.Put(d.sc)
		d.sc = nil
	}
}

// Decode reads the next JSON value from the stream into v (must be a non-nil pointer).
//
// Strings in v may alias the decoder's internal buffer (zero-copy).
// Returns [io.EOF] when no more values remain.
func (d *Decoder) Decode(v any) error {
	if d.err != nil {
		return d.err
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &InvalidUnmarshalError{Type: reflect.TypeOf(v)}
	}
	ptr := rv.UnsafePointer()

	// P1: cache TypeInfo across calls for the common same-type streaming case.
	elemType := rv.Elem().Type()
	var ti *TypeInfo
	if elemType == d.lastType {
		ti = d.lastTI
	} else {
		ti = GetCodec(elemType)
		d.lastType = elemType
		d.lastTI = ti
	}

	if err := d.ensureData(); err != nil {
		d.releaseParser()
		return err
	}

	// Find value start (skip whitespace), reading more if needed.
	idx := skipWS(d.buf[:d.bufLen], d.scanAt)
	for idx >= d.bufLen && !d.eof {
		if err := d.ensureBuffer(); err != nil {
			return err
		}
		if err := d.readMore(); err != nil {
			return err
		}
		idx = skipWS(d.buf[:d.bufLen], d.scanAt)
	}
	if idx >= d.bufLen {
		d.releaseParser()
		return io.EOF
	}

	// P0: reuse the parser across Decode calls, avoiding sync.Pool round-trips.
	sc := d.parser()

	data := d.buf[:d.bufLen]
	newIdx, err := sc.scanValue(data, idx, ti, ptr)

	if err == errUnexpectedEOF && !d.eof {
		return d.retryDecode(sc, idx, ti, ptr, rv)
	}
	if err != nil {
		if d.skipErrors != nil && d.skipErrors(err) {
			d.scanAt = idx
			if skipErr := d.skipToNewline(); skipErr != nil {
				return skipErr
			}
			return err
		}
		d.err = err
		d.releaseParser()
		return err
	}
	// Number/literal boundary ambiguity: scanNumberSpan stops at bufLen
	// even if the token continues in unread data. Strings, objects, and
	// arrays have explicit closing delimiters so they are unambiguous.
	if newIdx >= d.bufLen && !d.eof {
		c := data[idx]
		if c != '"' && c != '{' && c != '[' {
			if err := d.ensureBuffer(); err != nil {
				return err
			}
			if err := d.readMore(); err != nil {
				return err
			}
			if d.bufLen > newIdx && isTokenContinuation(d.buf[newIdx]) {
				rv.Elem().SetZero()
				return d.retryDecode(sc, idx, ti, ptr, rv)
			}
		}
	}

	d.recordValueSize(newIdx - idx)
	d.scanAt = newIdx
	d.valuesInBuf++
	// Inline fast-exit of maybeNewBuffer: only call the slow path when
	// the remaining capacity is unlikely to hold the next value.
	if !d.eof {
		if len(d.buf)-d.scanAt < d.predictedValueSize() {
			if err := d.growBufferAndFill(); err != nil {
				return err
			}
		}
	}
	return nil
}

// retryDecode is the slow path for values that span a buffer boundary.
// It grows the buffer, zeros the target (scanValue may have partially
// written), and retries until the value fits or a real error occurs.
func (d *Decoder) retryDecode(sc *Parser, valueStart int, ti *TypeInfo, ptr unsafe.Pointer, rv reflect.Value) error {
	for {
		rv.Elem().SetZero()

		if err := d.growAndFill(valueStart); err != nil {
			return err
		}

		data := d.buf[:d.bufLen]
		idx := skipWS(data, d.scanAt)
		if idx >= d.bufLen {
			if d.eof {
				return newSyntaxError("vjson: unexpected end of input", d.bufLen)
			}
			continue
		}

		newIdx, err := sc.scanValue(data, idx, ti, ptr)

		if err == errUnexpectedEOF && !d.eof {
			d.scanAt = idx
			continue
		}
		if err != nil {
			if d.skipErrors != nil && d.skipErrors(err) {
				d.scanAt = idx
				if skipErr := d.skipToNewline(); skipErr != nil {
					return skipErr
				}
				return err
			}
			d.err = err
			d.releaseParser()
			return err
		}

		// Number/literal boundary check (same logic as Decode fast path).
		if newIdx >= d.bufLen && !d.eof {
			c := data[idx]
			if c != '"' && c != '{' && c != '[' {
				if err := d.ensureBuffer(); err != nil {
					return err
				}
				if err := d.readMore(); err != nil {
					return err
				}
				if d.bufLen > newIdx && isTokenContinuation(d.buf[newIdx]) {
					d.scanAt = idx
					continue
				}
			}
		}

		d.recordValueSize(newIdx - idx)
		d.scanAt = newIdx
		d.valuesInBuf++
		if !d.eof {
			if len(d.buf)-d.scanAt < d.predictedValueSize() {
				if err := d.growBufferAndFill(); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// More reports whether there appears to be another value in the stream.
func (d *Decoder) More() bool {
	if d.buf != nil {
		if skipWS(d.buf[:d.bufLen], d.scanAt) < d.bufLen {
			return true
		}
	}
	return !d.eof
}

// Buffered returns a reader over the unscanned portion of the buffer.
func (d *Decoder) Buffered() io.Reader {
	if d.buf == nil || d.scanAt >= d.bufLen {
		return bytes.NewReader(nil)
	}
	return bytes.NewReader(d.buf[d.scanAt:d.bufLen])
}

// ensureData guarantees buf contains at least one readable byte,
// allocating a buffer and issuing the first read if necessary.
func (d *Decoder) ensureData() error {
	if d.buf != nil && d.scanAt < d.bufLen {
		return nil
	}
	if d.eof {
		return io.EOF
	}
	if err := d.ensureBuffer(); err != nil {
		return err
	}
	return d.readMore()
}

// ensureBuffer makes sure d.buf has at least minReadSize free bytes.
// When the current buffer is full, unscanned data is copied to a new
// (potentially larger) buffer. The old buffer is NOT compacted — it may
// still be referenced by zero-copy strings.
func (d *Decoder) ensureBuffer() error {
	if d.buf == nil {
		d.buf = make([]byte, d.bufSize)
		return nil
	}
	if len(d.buf)-d.bufLen >= minReadSize {
		return nil
	}

	curSize := len(d.buf)
	unscanned := d.bufLen - d.scanAt
	newSize := d.nextBufSize()
	if unscanned > 0 {
		minNeeded := unscanned + minReadSize
		predicted := d.predictedValueSize()
		// Modification 2: prediction-aware sizing — include predicted value
		// size so retry-path buffers grow fast enough to hold the next value.
		if predicted > minReadSize {
			needed := unscanned + predicted + minReadSize
			if needed > minNeeded {
				minNeeded = needed
			}
		}
		// Modification 3: cold-start heuristic — when unscanned exceeds the
		// prediction, the value is provably >= unscanned bytes. Use 2x as
		// target to converge in fewer retries.
		if unscanned > predicted {
			coldNeeded := unscanned*2 + minReadSize
			if coldNeeded > minNeeded {
				minNeeded = coldNeeded
			}
		}
		for newSize < minNeeded {
			newSize *= 2
		}
		// Value-align when predicted is driven by recent average (not spike decay).
		avg := d.recentAvgValueSize()
		if predicted > minReadSize && predicted == avg {
			nFit := (newSize - minReadSize) / predicted
			aligned := nFit*predicted + minReadSize
			if aligned >= minNeeded {
				newSize = aligned
			}
		}
		newBuf := make([]byte, newSize)
		copy(newBuf, d.buf[d.scanAt:d.bufLen])
		d.buf = newBuf
		d.bufLen = unscanned
		d.scanAt = 0
	} else {
		d.buf = make([]byte, newSize)
		d.bufLen = 0
		d.scanAt = 0
	}
	d.prevBufSize = d.lastBufSize
	d.lastBufSize = curSize
	return nil
}

// growAndFill moves scanAt back to valueStart (preserving the partial
// value), ensures the buffer has space, and reads repeatedly until the
// buffer is full or EOF is reached. This minimizes retries by giving
// scanValue the most data possible.
func (d *Decoder) growAndFill(valueStart int) error {
	if valueStart < d.scanAt {
		d.scanAt = valueStart
	}
	if err := d.ensureBuffer(); err != nil {
		return err
	}
	for len(d.buf)-d.bufLen >= minReadSize && !d.eof {
		if err := d.readMore(); err != nil {
			return err
		}
	}
	return nil
}

// nextBufSize returns the adaptive size for the next buffer allocation
// (average of the last two, falling back to the configured default).
func (d *Decoder) nextBufSize() int {
	if d.lastBufSize > 0 && d.prevBufSize > 0 {
		return (d.lastBufSize + d.prevBufSize) / 2
	}
	return d.bufSize
}

// readMore issues a single Read into the buffer's free tail.
// On io.EOF it sets d.eof without returning an error.
func (d *Decoder) readMore() error {
	n, err := d.r.Read(d.buf[d.bufLen:])
	d.bufLen += n
	if err != nil {
		if err == io.EOF {
			d.eof = true
			return nil
		}
		d.err = err
		return err
	}
	return nil
}

// recordValueSize updates the value-size prediction state after a
// successful decode. It maintains a high-water mark (maxSeenSize) that
// decays by ~3% per value, keeping the decoder prepared for recurring
// large values even across long runs of small ones.
func (d *Decoder) recordValueSize(size int) {
	d.prevValueSize = d.lastValueSize
	d.lastValueSize = size
	if size > d.maxSeenSize {
		d.maxSeenSize = size
	} else if d.maxSeenSize > 0 {
		d.maxSeenSize -= d.maxSeenSize >> 5 // decay ~3%/value
	}
}

// recentAvgValueSize returns the average of the last two decoded value sizes,
// or a conservative initial estimate if fewer than two values have been seen.
func (d *Decoder) recentAvgValueSize() int {
	if d.lastValueSize > 0 && d.prevValueSize > 0 {
		return (d.lastValueSize + d.prevValueSize) / 2
	}
	if d.lastValueSize > 0 {
		return d.lastValueSize
	}
	return d.bufSize / 4
}

// predictedValueSize returns the expected size of the next JSON value.
// It takes the larger of the recent average (last two values) and
// the decaying high-water mark, ensuring spike-heavy streams keep
// buffers large enough to avoid retries.
func (d *Decoder) predictedValueSize() int {
	avg := d.recentAvgValueSize()
	if d.maxSeenSize > avg {
		return d.maxSeenSize
	}
	return avg
}

// growBufferAndFill allocates a new prediction-sized buffer (via growBuffer)
// and immediately fills it with data from the reader. This ensures the next
// Decode call finds a buffer that is both correctly sized and full, preventing
// ensureData from skipping the read (because scanAt < bufLen) and thus
// eliminating the retry that would otherwise occur on insufficient data.
func (d *Decoder) growBufferAndFill() error {
	d.growBuffer()
	for len(d.buf)-d.bufLen >= minReadSize && !d.eof {
		if err := d.readMore(); err != nil {
			return err
		}
	}
	return nil
}

// growBuffer allocates a new buffer when the remaining capacity is
// insufficient for the predicted next value. Callers perform the
// fast-exit check inline (eof + capacity) so this method is only
// reached on the slow path.
func (d *Decoder) growBuffer() {
	predicted := d.predictedValueSize()
	unscanned := d.bufLen - d.scanAt

	// Buffer fitness: record the current buffer size as a "good" floor
	// when it held at least 2 values. This prevents nextBufSize()'s
	// averaging from shrinking back to a poorly-fitting size.
	if d.valuesInBuf >= 2 && (d.minGoodBufSize == 0 || len(d.buf) < d.minGoodBufSize) {
		d.minGoodBufSize = len(d.buf)
	}

	curSize := len(d.buf)
	newSize := max(d.minGoodBufSize, d.nextBufSize())
	needed := unscanned + predicted + minReadSize
	// If this buffer only held one value, the size is a poor fit.
	// Double the target so the next buffer can hold more values.
	if d.valuesInBuf < 2 {
		needed *= 2
	}
	for newSize < needed {
		newSize *= 2
	}
	// Value-align: trim the power-of-2 overshoot down to the nearest
	// multiple of predicted + minReadSize, so the buffer boundary falls
	// right after the last value that fits, eliminating wasted tail space.
	// Only apply when predicted is driven by the recent average, not by
	// a decaying spike (maxSeenSize). When maxSeenSize dominates, the
	// predicted size doesn't reflect actual values being decoded, so
	// aligning to it would trim to the wrong grid.
	avg := d.recentAvgValueSize()
	if predicted > minReadSize && predicted == avg {
		nFit := (newSize - minReadSize) / predicted
		aligned := nFit*predicted + minReadSize
		if aligned < needed {
			aligned += predicted
		}
		newSize = aligned
	}
	if unscanned > 0 {
		newBuf := make([]byte, newSize)
		copy(newBuf, d.buf[d.scanAt:d.bufLen])
		d.buf = newBuf
		d.bufLen = unscanned
		d.scanAt = 0
	} else {
		d.buf = make([]byte, newSize)
		d.bufLen = 0
		d.scanAt = 0
	}
	d.prevBufSize = d.lastBufSize
	d.lastBufSize = curSize
	d.valuesInBuf = 0
}

// skipToNewline advances scanAt past the next '\n' in the buffer,
// reading more data as needed. Returns io.EOF if no newline is found
// before the end of the stream.
func (d *Decoder) skipToNewline() error {
	for {
		for i := d.scanAt; i < d.bufLen; i++ {
			if d.buf[i] == '\n' {
				d.scanAt = i + 1
				return nil
			}
		}
		// No newline found in current buffer data.
		d.scanAt = d.bufLen
		if d.eof {
			return io.EOF
		}
		if err := d.ensureBuffer(); err != nil {
			return err
		}
		if err := d.readMore(); err != nil {
			return err
		}
	}
}

// isTokenContinuation reports whether b can extend a JSON number or
// literal (digits, letters, '.', '+', '-'). Used to detect buffer-
// boundary ambiguity for undelimited tokens.
func isTokenContinuation(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		b == '.' || b == '+' || b == '-'
}

// DecodeValue is a generic convenience wrapper around [Decoder.Decode].
func DecodeValue[T any](d *Decoder, v *T) error {
	return d.Decode(v)
}
