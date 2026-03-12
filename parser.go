package pjson

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"unsafe"

	"github.com/penglei/pjson/jsonmarker"
)

// =============================================================================
// Errors
// =============================================================================

var (
	errEmptyInput    = errors.New("pjson: empty input")
	errUnexpectedEOF = errors.New("pjson: unexpected end of input")
	errSyntax        = errors.New("pjson: syntax error")
	errNotPointer    = errors.New("pjson: v must be a non-nil pointer")
)

// UnsafeString converts a byte slice to a string without copying.
// The caller must ensure the byte slice is not modified during the
// lifetime of the returned string.
func UnsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// =============================================================================
// Parser
// =============================================================================

// parserOptions holds configurable parameters for NewParser.
type parserOptions struct {
	chunkCap int
}

// ParserOption configures optional behavior for NewParser.
type ParserOption func(*parserOptions)

// WithParserChunkCap sets the chunk capacity for the underlying ChunkManager.
// Defaults to CapMedium (128).
func WithParserChunkCap(n int) ParserOption {
	return func(o *parserOptions) { o.chunkCap = n }
}

// SliceHeader matches the internal layout of a Go slice.
type SliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

// Parser is a simple JSON parser built on top of Tokenizer.
// It focuses on correctness rather than performance.
type Parser struct {
	cm      *ChunkManager
	tok     *Tokenizer
	data    []byte      // original input buffer
	tier    int         // pool tier index, used by ParserPool.Put
	scratch [128]byte   // scratch buffer for LookupFieldBytes lowercase
}

// NewParser creates a parser with the given SIMD scanner.
func NewParser(scanner *jsonmarker.StdScanner, opts ...ParserOption) *Parser {
	o := parserOptions{chunkCap: CapMedium}
	for _, fn := range opts {
		fn(&o)
	}
	cm := NewChunkManager(scanner, WithChunkCap(o.chunkCap))
	return &Parser{cm: cm}
}

// Parse parses complete JSON data into v.
// v must be a non-nil pointer.
func (p *Parser) Parse(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errNotPointer
	}

	if len(data) == 0 {
		return errEmptyInput
	}

	p.data = data
	p.cm.Reset()
	p.cm.FeedBuffer(data)
	p.cm.Complete()

	if p.tok == nil {
		p.tok = NewTokenizer(p.cm)
	} else {
		p.tok.Reload()
	}

	elemType := rv.Elem().Type()
	ti := GetDecoder(elemType)
	rootPtr := rv.UnsafePointer()

	return p.parseValue(ti, rootPtr)
}

// =============================================================================
// Token Helpers
// =============================================================================

// next returns the byte at the next token position and advances.
// Returns 0 on exhaustion.
func (p *Parser) next() byte {
	off := p.tok.Next()
	if off < 0 {
		return 0
	}
	return p.data[off]
}

// peek returns the byte at the next token position without advancing.
// Returns 0 on exhaustion.
func (p *Parser) peek() byte {
	off := p.tok.Peek()
	if off < 0 {
		return 0
	}
	return p.data[off]
}

// nextOffset returns the offset of the next token and advances.
// Returns -1 on exhaustion.
func (p *Parser) nextOffset() int {
	return p.tok.Next()
}

// peekOffset returns the offset of the next token without advancing.
// Returns -1 on exhaustion.
func (p *Parser) peekOffset() int {
	return p.tok.Peek()
}

// =============================================================================
// Value Dispatch
// =============================================================================

func (p *Parser) parseValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	if ti.Kind == KindPointer {
		return p.parsePointerValue(ti, ptr)
	}

	b := p.peek()
	switch {
	case b == '"':
		return p.parseStringValue(ti, ptr)
	case b == '{':
		return p.parseObjectValue(ti, ptr)
	case b == '[':
		return p.parseArrayValue(ti, ptr)
	case b == 't', b == 'f':
		return p.parseBoolValue(ti, ptr)
	case b == 'n':
		return p.parseNullValue(ti, ptr)
	case (b >= '0' && b <= '9') || b == '-':
		return p.parseNumberValue(ti, ptr)
	case b == 0:
		return errUnexpectedEOF
	default:
		return fmt.Errorf("pjson: unexpected character %q", b)
	}
}

// parseAnyValue parses a JSON value into an interface{} value.
// Returns the parsed Go value: string, float64, bool, nil,
// map[string]any, or []any.
func (p *Parser) parseAnyValue() (any, error) {
	b := p.peek()
	switch {
	case b == '"':
		return p.parseStringAny()
	case b == '{':
		return p.parseObjectAny()
	case b == '[':
		return p.parseArrayAny()
	case b == 't':
		p.next() // consume 't'
		return true, nil
	case b == 'f':
		p.next() // consume 'f'
		return false, nil
	case b == 'n':
		p.next() // consume 'n'
		return nil, nil
	case (b >= '0' && b <= '9') || b == '-':
		return p.parseNumberAny()
	case b == 0:
		return nil, errUnexpectedEOF
	default:
		return nil, fmt.Errorf("pjson: unexpected character %q in any value", b)
	}
}

// =============================================================================
// String Parsing
// =============================================================================

// parseStringAny reads a JSON string and returns it as a Go string.
// Fast path (no escapes): zero-copy via unsafe.String pointing into p.data.
// Slow path (has escapes): allocates a fresh buffer and returns a string via unsafe.String.
// Safe because p.data is the caller's input buffer and outlives the Parser.
func (p *Parser) parseStringAny() (string, error) {
	openOff, closeOff, hasEscape := p.tok.NextString()
	if openOff < 0 {
		return "", errUnexpectedEOF
	}
	if closeOff < 0 {
		return "", errUnexpectedEOF
	}

	start := openOff + 1

	if !hasEscape {
		// Fast path: no escapes, zero-copy string from input buffer
		return UnsafeString(p.data[start:closeOff]), nil
	}

	// Slow path: allocate + unescape + unsafe.String (single allocation)
	return p.unescapeToString(start, closeOff)
}

// parseStringBytes reads a JSON string and returns a []byte of its content.
// Uses precomputed escape flags via NextString() for O(1) escape detection.
func (p *Parser) parseStringBytes() ([]byte, error) {
	openOff, closeOff, hasEscape := p.tok.NextString()
	if openOff < 0 {
		return nil, errUnexpectedEOF
	}
	if closeOff < 0 {
		return nil, errUnexpectedEOF
	}

	start := openOff + 1

	if !hasEscape {
		// Fast path: no escapes, zero-copy slice
		return p.data[start:closeOff], nil
	}

	// Slow path: has escapes, but we know the exact closing quote position
	return p.parseStringBytesSlow(start, closeOff)
}

// parseStringBytesSlow handles the escape-containing slow path.
// start is the byte after the opening quote; endOff is the position of the
// closing quote. Processes escapes within the bounded range [start, endOff).
func (p *Parser) parseStringBytesSlow(start, endOff int) ([]byte, error) {
	buf := make([]byte, 0, endOff-start)
	data := p.data[start:endOff]

	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\\')
		if idx < 0 {
			buf = append(buf, data...)
			break
		}
		buf = append(buf, data[:idx]...)
		if idx+1 >= len(data) {
			break
		}
		switch data[idx+1] {
		case '"', '\\', '/':
			buf = append(buf, data[idx+1])
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'u':
			if idx+5 < len(data) {
				r := hexToRune(data[idx+2 : idx+6])
				buf = append(buf, string(r)...)
				data = data[idx+6:]
				continue
			}
			buf = append(buf, '\\', 'u')
		default:
			buf = append(buf, '\\', data[idx+1])
		}
		data = data[idx+2:]
	}
	return buf, nil
}

// unescapeToString allocates a fresh buffer, processes escape sequences in
// data[start:endOff], and returns the result as a string via unsafe.String.
// This is a single-allocation path: the string directly owns the buffer backing array.
func (p *Parser) unescapeToString(start, endOff int) (string, error) {
	data := p.data[start:endOff]
	n := len(data)
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, 0, n)

	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\\')
		if idx < 0 {
			buf = append(buf, data...)
			break
		}
		buf = append(buf, data[:idx]...)
		if idx+1 >= len(data) {
			break
		}
		switch data[idx+1] {
		case '"', '\\', '/':
			buf = append(buf, data[idx+1])
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'u':
			if idx+5 < len(data) {
				r := hexToRune(data[idx+2 : idx+6])
				buf = append(buf, string(r)...)
				data = data[idx+6:]
				continue
			}
			buf = append(buf, '\\', 'u')
		default:
			buf = append(buf, '\\', data[idx+1])
		}
		data = data[idx+2:]
	}
	return unsafe.String(unsafe.SliceData(buf), len(buf)), nil
}

// skipString consumes a JSON string token without returning its content.
// The closing quote is now a structural token, so just call Next() to skip it.
func (p *Parser) skipString() error {
	off := p.nextOffset() // consume opening '"'
	if off < 0 {
		return errUnexpectedEOF
	}

	// Closing quote is the next structural token
	closeOff := p.nextOffset() // consume closing '"'
	if closeOff < 0 {
		return errUnexpectedEOF
	}

	return nil
}

func (p *Parser) parseStringValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	openOff, closeOff, hasEscape := p.tok.NextString()
	if openOff < 0 {
		return errUnexpectedEOF
	}
	if closeOff < 0 {
		return errUnexpectedEOF
	}

	start := openOff + 1

	if !hasEscape {
		// Fast path: no escapes, zero-copy via unsafe.String
		raw := p.data[start:closeOff]
		var s string
		if len(raw) > 0 {
			s = unsafe.String(&raw[0], len(raw))
		}
		switch ti.Kind {
		case KindString:
			*(*string)(ptr) = s
		case KindAny:
			*(*any)(ptr) = s
		default:
			return fmt.Errorf("pjson: cannot assign string to %v field", ti.Kind)
		}
		return nil
	}

	// Slow path: has escapes — allocate + unescape + unsafe.String (single allocation)
	s, err := p.unescapeToString(start, closeOff)
	if err != nil {
		return err
	}
	switch ti.Kind {
	case KindString:
		*(*string)(ptr) = s
	case KindAny:
		*(*any)(ptr) = s
	default:
		return fmt.Errorf("pjson: cannot assign string to %v field", ti.Kind)
	}
	return nil
}

// =============================================================================
// Number Parsing
// =============================================================================

// parseNumberSpan consumes the number token and returns the raw byte span.
// Numbers are a single structural token; the span extends from the token
// offset until the next non-number character.
func (p *Parser) parseNumberSpan() ([]byte, error) {
	off := p.nextOffset() // consume number start
	if off < 0 {
		return nil, errUnexpectedEOF
	}
	end := off + 1
	for end < len(p.data) {
		c := p.data[end]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E' {
			end++
		} else {
			break
		}
	}
	return p.data[off:end], nil
}

func (p *Parser) parseNumberValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	span, err := p.parseNumberSpan()
	if err != nil {
		return err
	}

	switch ti.Kind {
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		v := parseInt64Fast(span)
		WriteIntValue(ptr, ti.Kind, v)
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		v := parseUint64Fast(span)
		WriteUintValue(ptr, ti.Kind, v)
	case KindFloat32:
		v, err := strconv.ParseFloat(UnsafeString(span), 32)
		if err != nil {
			return fmt.Errorf("pjson: invalid float %q: %w", span, err)
		}
		*(*float32)(ptr) = float32(v)
	case KindFloat64:
		v, err := strconv.ParseFloat(UnsafeString(span), 64)
		if err != nil {
			return fmt.Errorf("pjson: invalid float %q: %w", span, err)
		}
		*(*float64)(ptr) = v
	case KindAny:
		// Fast path: small non-negative integers 0-255 → interned float64
		if len(span) >= 1 && len(span) <= 3 && span[0] >= '0' && span[0] <= '9' {
			val := int(span[0] - '0')
			allDigits := true
			for j := 1; j < len(span); j++ {
				if span[j] < '0' || span[j] > '9' {
					allDigits = false
					break
				}
				val = val*10 + int(span[j]-'0')
			}
			if allDigits && val < 256 {
				*(*any)(ptr) = InternedFloats[val]
				return nil
			}
		}
		v, err := strconv.ParseFloat(UnsafeString(span), 64)
		if err != nil {
			return fmt.Errorf("pjson: invalid number %q: %w", span, err)
		}
		*(*any)(ptr) = v
	default:
		return fmt.Errorf("pjson: cannot assign number to %v field", ti.Kind)
	}
	return nil
}

// InternedFloats holds pre-boxed float64 values for 0-255 to avoid
// heap allocation when returning small integers as any (interface{}).
var InternedFloats = func() [256]any {
	var arr [256]any
	for i := range arr {
		arr[i] = float64(i)
	}
	return arr
}()

func (p *Parser) parseNumberAny() (any, error) {
	span, err := p.parseNumberSpan()
	if err != nil {
		return nil, err
	}
	// Fast path: small non-negative integers 0-255 → return interned value
	if len(span) >= 1 && len(span) <= 3 && span[0] >= '0' && span[0] <= '9' {
		val := int(span[0] - '0')
		allDigits := true
		for j := 1; j < len(span); j++ {
			if span[j] < '0' || span[j] > '9' {
				allDigits = false
				break
			}
			val = val*10 + int(span[j]-'0')
		}
		if allDigits && val < 256 {
			return InternedFloats[val], nil
		}
	}
	v, err := strconv.ParseFloat(UnsafeString(span), 64)
	if err != nil {
		return nil, fmt.Errorf("pjson: invalid number %q: %w", span, err)
	}
	return v, nil
}

// parseInt64Fast parses an integer from a byte span without allocation.
// Handles optional leading '-'. No overflow or format validation —
// the SIMD tokenizer has already validated the JSON structure.
func parseInt64Fast(span []byte) int64 {
	if len(span) == 0 {
		return 0
	}
	neg := false
	i := 0
	if span[0] == '-' {
		neg = true
		i = 1
	}
	var n int64
	for ; i < len(span); i++ {
		n = n*10 + int64(span[i]-'0')
	}
	if neg {
		return -n
	}
	return n
}

// parseUint64Fast parses an unsigned integer from a byte span without allocation.
func parseUint64Fast(span []byte) uint64 {
	var n uint64
	for i := 0; i < len(span); i++ {
		n = n*10 + uint64(span[i]-'0')
	}
	return n
}

// hexToRune parses exactly 4 hex digits into a rune without allocation.
func hexToRune(hex []byte) rune {
	var r rune
	for _, c := range hex[:4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			r |= rune(c - 'A' + 10)
		}
	}
	return r
}

func WriteIntValue(ptr unsafe.Pointer, kind ElemTypeKind, v int64) {
	switch kind {
	case KindInt:
		*(*int)(ptr) = int(v)
	case KindInt8:
		*(*int8)(ptr) = int8(v)
	case KindInt16:
		*(*int16)(ptr) = int16(v)
	case KindInt32:
		*(*int32)(ptr) = int32(v)
	case KindInt64:
		*(*int64)(ptr) = v
	}
}

func WriteUintValue(ptr unsafe.Pointer, kind ElemTypeKind, v uint64) {
	switch kind {
	case KindUint:
		*(*uint)(ptr) = uint(v)
	case KindUint8:
		*(*uint8)(ptr) = uint8(v)
	case KindUint16:
		*(*uint16)(ptr) = uint16(v)
	case KindUint32:
		*(*uint32)(ptr) = uint32(v)
	case KindUint64:
		*(*uint64)(ptr) = v
	}
}

// =============================================================================
// Bool Parsing
// =============================================================================

func (p *Parser) parseBoolValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	b := p.next() // consume 't' or 'f'

	var val bool
	switch b {
	case 't':
		val = true
	case 'f':
		val = false
	default:
		return errSyntax
	}

	switch ti.Kind {
	case KindBool:
		*(*bool)(ptr) = val
	case KindAny:
		*(*any)(ptr) = val
	default:
		return fmt.Errorf("pjson: cannot assign bool to %v field", ti.Kind)
	}
	return nil
}

// =============================================================================
// Null Parsing
// =============================================================================

func (p *Parser) parseNullValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	p.next() // consume 'n'

	switch ti.Kind {
	case KindString:
		*(*string)(ptr) = ""
	case KindBool:
		*(*bool)(ptr) = false
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		WriteIntValue(ptr, ti.Kind, 0)
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		WriteUintValue(ptr, ti.Kind, 0)
	case KindFloat32:
		*(*float32)(ptr) = 0
	case KindFloat64:
		*(*float64)(ptr) = 0
	case KindPointer:
		// Set pointer to nil — already handled by parsePointerValue
		// but if called directly, zero the pointer.
		*(*unsafe.Pointer)(ptr) = nil
	case KindSlice:
		// Set slice to nil
		*(*[]byte)(ptr) = nil
	case KindMap:
		// Set map to nil — write nil pointer at the map location
		reflect.NewAt(reflect.MapOf(reflect.TypeOf(""), reflect.TypeOf((*any)(nil)).Elem()), ptr).Elem().Set(reflect.Zero(reflect.MapOf(reflect.TypeOf(""), reflect.TypeOf((*any)(nil)).Elem())))
	case KindStruct:
		// null for struct: zero the struct (like encoding/json)
		// no-op: struct is already at its zero value if freshly allocated
	case KindAny:
		*(*any)(ptr) = nil
	}
	return nil
}

// =============================================================================
// Object Parsing
// =============================================================================

func (p *Parser) parseObjectValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	switch ti.Kind {
	case KindStruct:
		return p.parseObjectToStruct(ti, ptr)
	case KindMap:
		return p.parseObjectToMap(ti, ptr)
	case KindAny:
		m, err := p.parseObjectAny()
		if err != nil {
			return err
		}
		*(*any)(ptr) = m
		return nil
	default:
		return fmt.Errorf("pjson: cannot assign object to %v field", ti.Kind)
	}
}

func (p *Parser) parseObjectToStruct(ti *TypeInfo, base unsafe.Pointer) error {
	p.next() // consume '{'

	sDec := ti.Decoder.(*ReflectStructDecoder)

	// Empty object
	if p.peek() == '}' {
		p.next() // consume '}'
		return nil
	}

	for {
		// Key — zero-copy []byte, looked up via LookupFieldBytes
		keyBytes, err := p.parseStringBytes()
		if err != nil {
			return err
		}

		// Colon
		b := p.next()
		if b != ':' {
			return fmt.Errorf("pjson: expected ':' after key, got %q", string(b))
		}

		// Value
		fi := sDec.LookupFieldBytes(keyBytes, p.scratch[:])
		if fi == nil {
			// Unknown field — skip the value
			if err := p.skipValue(); err != nil {
				return err
			}
		} else {
			fieldPtr := unsafe.Add(base, fi.Offset)
			if err := p.parseValue(fi, fieldPtr); err != nil {
				return err
			}
		}

		// Comma or closing brace
		b = p.peek()
		if b == ',' {
			p.next() // consume ','
			continue
		}
		if b == '}' {
			p.next() // consume '}'
			return nil
		}
		return fmt.Errorf("pjson: expected ',' or '}' in object, got %q", string(b))
	}
}

func (p *Parser) parseObjectToMap(ti *TypeInfo, ptr unsafe.Pointer) error {
	p.next() // consume '{'

	mDec := ti.Decoder.(*ReflectMapDecoder)

	// Obtain reflect.Value of the map
	mapPtr := reflect.NewAt(mDec.MapType, ptr)
	mapVal := mapPtr.Elem()
	if mapVal.IsNil() {
		mapVal.Set(reflect.MakeMap(mDec.MapType))
	}

	// Empty object
	if p.peek() == '}' {
		p.next() // consume '}'
		return nil
	}

	for {
		// Key
		keyBytes, err := p.parseStringBytes()
		if err != nil {
			return err
		}

		// Colon
		b := p.next()
		if b != ':' {
			return fmt.Errorf("pjson: expected ':' in map object")
		}

		// Value
		valRV := reflect.New(mDec.ValType)
		valPtr := valRV.UnsafePointer()
		if err := p.parseValue(mDec.ValTI, valPtr); err != nil {
			return err
		}
		mapVal.SetMapIndex(reflect.ValueOf(string(keyBytes)), valRV.Elem())

		// Comma or closing brace
		b = p.peek()
		if b == ',' {
			p.next()
			continue
		}
		if b == '}' {
			p.next()
			return nil
		}
		return fmt.Errorf("pjson: expected ',' or '}' in map, got %q", string(b))
	}
}

func (p *Parser) parseObjectAny() (map[string]any, error) {
	p.next() // consume '{'

	m := make(map[string]any)

	if p.peek() == '}' {
		p.next()
		return m, nil
	}

	for {
		key, err := p.parseStringAny()
		if err != nil {
			return nil, err
		}

		b := p.next()
		if b != ':' {
			return nil, fmt.Errorf("pjson: expected ':' in any object")
		}

		val, err := p.parseAnyValue()
		if err != nil {
			return nil, err
		}
		m[key] = val

		b = p.peek()
		if b == ',' {
			p.next()
			continue
		}
		if b == '}' {
			p.next()
			return m, nil
		}
		return nil, fmt.Errorf("pjson: expected ',' or '}' in any object, got %q", string(b))
	}
}

// =============================================================================
// Array Parsing
// =============================================================================

func (p *Parser) parseArrayValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	switch ti.Kind {
	case KindSlice:
		return p.parseArrayToSlice(ti, ptr)
	case KindAny:
		arr, err := p.parseArrayAny()
		if err != nil {
			return err
		}
		*(*any)(ptr) = arr
		return nil
	default:
		return fmt.Errorf("pjson: cannot assign array to %v field", ti.Kind)
	}
}

func (p *Parser) parseArrayToSlice(ti *TypeInfo, ptr unsafe.Pointer) error {
	p.next() // consume '['

	sDec := ti.Decoder.(*ReflectSliceDecoder)

	// Empty array — use pre-created empty slice pointer (no allocation)
	if p.peek() == ']' {
		p.next()
		sh := (*SliceHeader)(ptr)
		sh.Data = sDec.EmptySliceData
		sh.Len = 0
		sh.Cap = 0
		return nil
	}

	// Pre-allocate backing array, grow with doubling.
	// reflect.MakeSlice + reflect.Copy ensures GC write barriers
	// are honored for pointer-containing element types.
	const initCap = 8
	cap_ := initCap
	len_ := 0
	backing := reflect.MakeSlice(sDec.SliceType, initCap, initCap)
	base := backing.Pointer() // uintptr

	for {
		// Grow if needed
		if len_ == cap_ {
			newCap := cap_ * 2
			newBacking := reflect.MakeSlice(sDec.SliceType, newCap, newCap)
			reflect.Copy(newBacking, backing.Slice(0, len_))
			backing = newBacking
			base = newBacking.Pointer() // uintptr
			cap_ = newCap
		}

		elemPtr := unsafe.Pointer(base + uintptr(len_)*sDec.ElemSize) //nolint:govet
		len_++

		if err := p.parseValue(sDec.ElemTI, elemPtr); err != nil {
			return err
		}

		b := p.peek()
		if b == ',' {
			p.next()
			continue
		}
		if b == ']' {
			p.next()
			// Write slice header directly
			sh := (*SliceHeader)(ptr)
			sh.Data = unsafe.Pointer(base) //nolint:govet
			sh.Len = len_
			sh.Cap = cap_
			return nil
		}
		return fmt.Errorf("pjson: expected ',' or ']' in array, got %q", string(b))
	}
}

func (p *Parser) parseArrayAny() ([]any, error) {
	p.next() // consume '['

	if p.peek() == ']' {
		p.next()
		return []any{}, nil
	}

	var arr []any
	for {
		val, err := p.parseAnyValue()
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)

		b := p.peek()
		if b == ',' {
			p.next()
			continue
		}
		if b == ']' {
			p.next()
			return arr, nil
		}
		return nil, fmt.Errorf("pjson: expected ',' or ']' in any array, got %q", string(b))
	}
}

// =============================================================================
// Pointer Parsing
// =============================================================================

func (p *Parser) parsePointerValue(ti *TypeInfo, ptr unsafe.Pointer) error {
	pDec := ti.Decoder.(*ReflectPointerDecoder)

	// null → set pointer to nil
	if p.peek() == 'n' {
		p.next() // consume 'n'
		*(*unsafe.Pointer)(ptr) = nil
		return nil
	}

	// Allocate a new element and parse into it
	elemRV := reflect.New(pDec.ElemType)
	elemPtr := elemRV.UnsafePointer()
	if err := p.parseValue(pDec.ElemTI, elemPtr); err != nil {
		return err
	}

	// Write the pointer: ptr points to a *T slot, set it to the new allocation.
	*(*unsafe.Pointer)(ptr) = elemPtr
	return nil
}

// =============================================================================
// Skip Value (for unknown fields)
// =============================================================================

func (p *Parser) skipValue() error {
	b := p.next()
	switch {
	case b == '"':
		p.next() // skip closing quote
		return nil
	case b == 't', b == 'f', b == 'n':
		return nil
	case (b >= '0' && b <= '9') || b == '-':
		return nil
	case b == '{', b == '[':
		// depth-counting skip: no recursion, no :/comma validation
		depth := 1
		for depth > 0 {
			b = p.next()
			switch b {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			case '"':
				p.next() // skip closing quote
			case 0:
				return errUnexpectedEOF
			}
		}
		return nil
	case b == 0:
		return errUnexpectedEOF
	default:
		return errSyntax
	}
}

func (p *Parser) skipObject() error {
	p.next() // consume '{'
	if p.peek() == '}' {
		p.next()
		return nil
	}
	for {
		// Skip key
		if err := p.skipString(); err != nil {
			return err
		}
		// Skip colon
		if b := p.next(); b != ':' {
			return errSyntax
		}
		// Skip value
		if err := p.skipValue(); err != nil {
			return err
		}
		b := p.peek()
		if b == ',' {
			p.next()
			continue
		}
		if b == '}' {
			p.next()
			return nil
		}
		return errSyntax
	}
}

func (p *Parser) skipArray() error {
	p.next() // consume '['
	if p.peek() == ']' {
		p.next()
		return nil
	}
	for {
		if err := p.skipValue(); err != nil {
			return err
		}
		b := p.peek()
		if b == ',' {
			p.next()
			continue
		}
		if b == ']' {
			p.next()
			return nil
		}
		return errSyntax
	}
}
