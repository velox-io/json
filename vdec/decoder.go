package vdec

import (
	"bytes"
	"io"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
)

const (
	defaultBufSize = 128 * 1024
	minReadSize    = 512
)

// DecoderOption configures a [Decoder].
type DecoderOption func(*Decoder)

// WithBufferSize sets the initial read buffer size (default 128 KB).
func WithBufferSize(size int) DecoderOption {
	return func(d *Decoder) {
		if size > 0 {
			d.bufSize = size
		}
	}
}

// WithSkipErrors enables skip-on-error recovery for NDJSON streams.
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

// WithExpectedSize hints the total input size (e.g. HTTP Content-Length).
func WithExpectedSize(size int) DecoderOption {
	return func(d *Decoder) {
		if size > 0 {
			d.expectedSize = size
		}
	}
}

// Decoder reads and decodes JSON values from an input stream.
type Decoder struct {
	r io.Reader

	buf    []byte
	bufLen int
	scanAt int

	bufSize int

	expectedSize int

	err error
	eof bool

	lastBufSize int
	prevBufSize int

	lastValueSize int
	prevValueSize int
	maxSeenSize   int

	valuesInBuf    int
	minGoodBufSize int

	skipErrors func(err error) bool
	useNumber  bool
	copyString bool

	sc       *Parser
	lastType reflect.Type
	lastTI   *DecTypeInfo
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

// UseNumber causes numbers in interface{} fields to decode as json.Number.
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
		a.Reset()
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
	var ti *DecTypeInfo
	if elemType == d.lastType {
		ti = d.lastTI
	} else {
		ti = DecTypeInfoOf(elemType)
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
		err = wrapUnexpectedEOF(err, d.bufLen)
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

func (d *Decoder) retryDecode(sc *Parser, valueStart int, ti *DecTypeInfo, ptr unsafe.Pointer, rv reflect.Value) error {
	for {
		rv.Elem().SetZero()

		if err := d.growAndFill(valueStart); err != nil {
			return err
		}

		data := d.buf[:d.bufLen]
		idx := skipWS(data, d.scanAt)
		if idx >= d.bufLen {
			if d.eof {
				return newSyntaxErrorWrap("vjson: unexpected end of input", d.bufLen, io.ErrUnexpectedEOF)
			}
			continue
		}

		newIdx, err := sc.scanValue(data, idx, ti, ptr)

		if err == errUnexpectedEOF && !d.eof {
			d.scanAt = idx
			continue
		}
		if err != nil {
			err = wrapUnexpectedEOF(err, d.bufLen)
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

func (d *Decoder) More() bool {
	if d.buf != nil {
		if skipWS(d.buf[:d.bufLen], d.scanAt) < d.bufLen {
			return true
		}
	}
	return !d.eof
}

func (d *Decoder) Buffered() io.Reader {
	if d.buf == nil || d.scanAt >= d.bufLen {
		return bytes.NewReader(nil)
	}
	return bytes.NewReader(d.buf[d.scanAt:d.bufLen])
}

// Err returns the sticky error, if any. Used by wrapper layers to detect
// whether consecutive Decode calls return the same underlying error object.
func (d *Decoder) Err() error { return d.err }

func DecodeValue[T any](d *Decoder, v *T) error {
	return d.Decode(v)
}

func (d *Decoder) ensureData() error {
	if d.buf != nil && d.scanAt < d.bufLen {
		return nil
	}
	if d.eof {
		return io.EOF
	}
	if d.expectedSize > 0 && d.buf == nil {
		return d.prereadExpected()
	}
	if err := d.ensureBuffer(); err != nil {
		return err
	}
	return d.readMore()
}

func (d *Decoder) prereadExpected() error {
	size := d.expectedSize + minReadSize
	d.buf = gort.MakeDirtyBytes(size, size)
	d.bufLen = 0
	d.scanAt = 0

	target := d.expectedSize
	d.expectedSize = 0

	for d.bufLen < target && !d.eof {
		if err := d.readMore(); err != nil {
			return err
		}
	}
	return nil
}

func (d *Decoder) ensureBuffer() error {
	if d.buf == nil {
		d.buf = gort.MakeDirtyBytes(d.bufSize, d.bufSize)
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
		if unscanned > predicted {
			coldNeeded := unscanned*2 + minReadSize
			if coldNeeded > minNeeded {
				minNeeded = coldNeeded
			}
		}
		for newSize < minNeeded {
			newSize *= 2
		}
		avg := d.recentAvgValueSize()
		if predicted > minReadSize && predicted == avg {
			nFit := (newSize - minReadSize) / predicted
			aligned := nFit*predicted + minReadSize
			if aligned >= minNeeded {
				newSize = aligned
			}
		}
		newBuf := gort.MakeDirtyBytes(newSize, newSize)
		copy(newBuf, d.buf[d.scanAt:d.bufLen])
		d.buf = newBuf
		d.bufLen = unscanned
		d.scanAt = 0
	} else {
		d.buf = gort.MakeDirtyBytes(newSize, newSize)
		d.bufLen = 0
		d.scanAt = 0
	}
	d.prevBufSize = d.lastBufSize
	d.lastBufSize = curSize
	return nil
}

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

func (d *Decoder) nextBufSize() int {
	if d.lastBufSize > 0 && d.prevBufSize > 0 {
		return (d.lastBufSize + d.prevBufSize) / 2
	}
	return d.bufSize
}

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

func (d *Decoder) recordValueSize(size int) {
	d.prevValueSize = d.lastValueSize
	d.lastValueSize = size
	if size > d.maxSeenSize {
		d.maxSeenSize = size
	} else if d.maxSeenSize > 0 {
		d.maxSeenSize -= d.maxSeenSize >> 5
	}
}

func (d *Decoder) recentAvgValueSize() int {
	if d.lastValueSize > 0 && d.prevValueSize > 0 {
		return (d.lastValueSize + d.prevValueSize) / 2
	}
	if d.lastValueSize > 0 {
		return d.lastValueSize
	}
	return d.bufSize / 4
}

func (d *Decoder) predictedValueSize() int {
	avg := d.recentAvgValueSize()
	if d.maxSeenSize > avg {
		return d.maxSeenSize
	}
	return avg
}

func (d *Decoder) growBufferAndFill() error {
	d.growBuffer()
	for len(d.buf)-d.bufLen >= minReadSize && !d.eof {
		if err := d.readMore(); err != nil {
			return err
		}
	}
	return nil
}

func (d *Decoder) growBuffer() {
	predicted := d.predictedValueSize()
	unscanned := d.bufLen - d.scanAt

	if d.valuesInBuf >= 2 && (d.minGoodBufSize == 0 || len(d.buf) < d.minGoodBufSize) {
		d.minGoodBufSize = len(d.buf)
	}

	curSize := len(d.buf)
	newSize := max(d.minGoodBufSize, d.nextBufSize())
	needed := unscanned + predicted + minReadSize
	if d.valuesInBuf < 2 {
		needed *= 2
	}
	for newSize < needed {
		newSize *= 2
	}
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
		newBuf := gort.MakeDirtyBytes(newSize, newSize)
		copy(newBuf, d.buf[d.scanAt:d.bufLen])
		d.buf = newBuf
		d.bufLen = unscanned
		d.scanAt = 0
	} else {
		d.buf = gort.MakeDirtyBytes(newSize, newSize)
		d.bufLen = 0
		d.scanAt = 0
	}
	d.prevBufSize = d.lastBufSize
	d.lastBufSize = curSize
	d.valuesInBuf = 0
}

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

func isTokenContinuation(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		b == '.' || b == '+' || b == '-'
}
