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

// DecoderCopyString causes all decoded strings to be heap-copied.
func DecoderCopyString() DecoderOption {
	return func(d *Decoder) {
		d.copyString = true
	}
}

// Decoder reads and decodes JSON values from an input stream.
// Each [Decoder.Decode] call parses the next value via scanValue (single-pass).
// When a value spans a buffer boundary the decoder grows the buffer and retries.
//
// Old buffers are never reused — zero-copy strings may reference them via
// unsafe.String. Unscanned data is copied to a fresh buffer; the old one
// stays live until the GC reclaims it.
type Decoder struct {
	r io.Reader

	buf    []byte
	bufLen int
	scanAt int

	bufSize int

	err error
	eof bool

	lastBufSize int
	prevBufSize int

	lastValueSize int
	prevValueSize int
	maxSeenSize   int // high-water mark with slow decay

	valuesInBuf    int
	minGoodBufSize int // smallest buf that held ≥ 2 values

	skipErrors func(err error) bool
	useNumber  bool
	copyString bool

	sc       *Parser      // owned parser, lazily acquired from pool
	lastType reflect.Type // type cache for consecutive same-type Decode calls
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

// UseNumber causes numbers in interface{} fields to decode as [json.Number]
// instead of float64. It is safe to call before decoding starts or between
// Decode calls; if a parser is already acquired, the setting is applied
// immediately to the owned parser.
func (d *Decoder) UseNumber() {
	d.useNumber = true
	if d.sc != nil {
		d.sc.useNumber = true
	}
}

// CopyString causes all decoded strings to be heap-copied.
func (d *Decoder) CopyString() {
	d.copyString = true
	if d.sc != nil {
		d.sc.copyString = true
	}
}

// parser returns the owned Parser, acquiring one from the pool on first use.
// Between Decode calls it performs inter-decode cleanup (same as parserPool.Put
// but without the pool round-trip).
func (d *Decoder) parser() *Parser {
	if d.sc == nil {
		d.sc = parsers.Get()
		d.sc.useNumber = d.useNumber
		d.sc.copyString = d.copyString
		return d.sc
	}
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

func (d *Decoder) releaseParser() {
	if d.sc != nil {
		parsers.Put(d.sc)
		d.sc = nil
	}
}

// Decode reads the next JSON value from the stream into v (non-nil pointer).
// Strings in v may alias the internal buffer (zero-copy).
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

	elemType := rv.Elem().Type()
	var ti *TypeInfo
	if elemType == d.lastType {
		ti = d.lastTI
	} else {
		ti = getCodec(elemType)
		d.lastType = elemType
		d.lastTI = ti
	}

	if err := d.ensureData(); err != nil {
		d.releaseParser()
		return err
	}

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
	// Number/literal boundary: token may continue beyond bufLen.
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
	if !d.eof {
		if len(d.buf)-d.scanAt < d.predictedValueSize() {
			if err := d.growBufferAndFill(); err != nil {
				return err
			}
		}
	}
	return nil
}

// retryDecode grows the buffer and retries until the value fits or a real
// error occurs. Re-zeros the target on each iteration (scanValue may have
// partially written before errUnexpectedEOF).
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

// ensureData guarantees buf contains at least one readable byte.
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
func (d *Decoder) ensureBuffer() error {
	if d.buf == nil {
		d.buf = makeDirtyBytes(d.bufSize, d.bufSize)
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
		if predicted > minReadSize {
			needed := unscanned + predicted + minReadSize
			if needed > minNeeded {
				minNeeded = needed
			}
		}
		// Cold-start: double to converge faster.
		if unscanned > predicted {
			coldNeeded := unscanned*2 + minReadSize
			if coldNeeded > minNeeded {
				minNeeded = coldNeeded
			}
		}
		for newSize < minNeeded {
			newSize *= 2
		}
		// Value-align when predicted reflects recent average (not spike decay).
		avg := d.recentAvgValueSize()
		if predicted > minReadSize && predicted == avg {
			nFit := (newSize - minReadSize) / predicted
			aligned := nFit*predicted + minReadSize
			if aligned >= minNeeded {
				newSize = aligned
			}
		}
		newBuf := makeDirtyBytes(newSize, newSize)
		copy(newBuf, d.buf[d.scanAt:d.bufLen])
		d.buf = newBuf
		d.bufLen = unscanned
		d.scanAt = 0
	} else {
		d.buf = makeDirtyBytes(newSize, newSize)
		d.bufLen = 0
		d.scanAt = 0
	}
	d.prevBufSize = d.lastBufSize
	d.lastBufSize = curSize
	return nil
}

// growAndFill preserves the partial value at valueStart, grows the buffer,
// and fills it to minimize retries.
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

// nextBufSize returns the average of the last two buffer sizes, or the default.
func (d *Decoder) nextBufSize() int {
	if d.lastBufSize > 0 && d.prevBufSize > 0 {
		return (d.lastBufSize + d.prevBufSize) / 2
	}
	return d.bufSize
}

// readMore issues a single Read into the buffer's free tail.
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

// recordValueSize updates prediction state and decays the high-water mark.
func (d *Decoder) recordValueSize(size int) {
	d.prevValueSize = d.lastValueSize
	d.lastValueSize = size
	if size > d.maxSeenSize {
		d.maxSeenSize = size
	} else if d.maxSeenSize > 0 {
		d.maxSeenSize -= d.maxSeenSize >> 5 // decay ~3%/value
	}
}

// recentAvgValueSize returns the average of the last two value sizes.
func (d *Decoder) recentAvgValueSize() int {
	if d.lastValueSize > 0 && d.prevValueSize > 0 {
		return (d.lastValueSize + d.prevValueSize) / 2
	}
	if d.lastValueSize > 0 {
		return d.lastValueSize
	}
	return d.bufSize / 4
}

// predictedValueSize returns max(recentAvg, decaying high-water mark).
func (d *Decoder) predictedValueSize() int {
	avg := d.recentAvgValueSize()
	if d.maxSeenSize > avg {
		return d.maxSeenSize
	}
	return avg
}

// growBufferAndFill allocates a prediction-sized buffer and fills it.
func (d *Decoder) growBufferAndFill() error {
	d.growBuffer()
	for len(d.buf)-d.bufLen >= minReadSize && !d.eof {
		if err := d.readMore(); err != nil {
			return err
		}
	}
	return nil
}

// growBuffer allocates a new buffer sized for the predicted next value.
func (d *Decoder) growBuffer() {
	predicted := d.predictedValueSize()
	unscanned := d.bufLen - d.scanAt

	if d.valuesInBuf >= 2 && (d.minGoodBufSize == 0 || len(d.buf) < d.minGoodBufSize) {
		d.minGoodBufSize = len(d.buf)
	}

	curSize := len(d.buf)
	newSize := max(d.minGoodBufSize, d.nextBufSize())
	needed := unscanned + predicted + minReadSize
	if d.valuesInBuf < 2 { // poor fit — double target
		needed *= 2
	}
	for newSize < needed {
		newSize *= 2
	}
	// Value-align: trim overshoot to a multiple of predicted size,
	// but skip when maxSeenSize dominates (spike decay).
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
		newBuf := makeDirtyBytes(newSize, newSize)
		copy(newBuf, d.buf[d.scanAt:d.bufLen])
		d.buf = newBuf
		d.bufLen = unscanned
		d.scanAt = 0
	} else {
		d.buf = makeDirtyBytes(newSize, newSize)
		d.bufLen = 0
		d.scanAt = 0
	}
	d.prevBufSize = d.lastBufSize
	d.lastBufSize = curSize
	d.valuesInBuf = 0
}

// skipToNewline advances scanAt past the next '\n', reading more if needed.
func (d *Decoder) skipToNewline() error {
	for {
		for i := d.scanAt; i < d.bufLen; i++ {
			if d.buf[i] == '\n' {
				d.scanAt = i + 1
				return nil
			}
		}
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

// isTokenContinuation reports whether b can extend a JSON number or literal.
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
