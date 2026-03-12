package pjson

import (
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

// =============================================================================
// Parser
// =============================================================================

// Parser is a simple JSON parser built on top of Tokenizer.
// It focuses on correctness rather than performance.
type Parser struct {
	cm   *ChunkManager
	tok  *Tokenizer
	data []byte // original input buffer
}

// NewParser creates a parser with the given SIMD scanner.
func NewParser(scanner *jsonmarker.Scanner) *Parser {
	cm := NewChunkManager(scanner)
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
	ti := getDecoder(elemType)
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

func (p *Parser) parseValue(ti *typeInfo, ptr unsafe.Pointer) error {
	if ti.kind == kindPointer {
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
		return p.parseStringRaw()
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

// parseStringRaw reads a JSON string and returns the Go string value.
// Consumes the opening quote token (the string-start token from StructuralStart).
func (p *Parser) parseStringRaw() (string, error) {
	off := p.nextOffset() // consume opening '"'
	if off < 0 {
		return "", errUnexpectedEOF
	}

	// Scan forward in the raw buffer for the closing quote.
	// We need to handle escapes: skip \" sequences.
	start := off + 1 // byte after opening quote
	i := start
	hasEscape := false
	for i < len(p.data) {
		c := p.data[i]
		if c == '\\' {
			hasEscape = true
			i += 2 // skip escape sequence
			continue
		}
		if c == '"' {
			// Found closing quote at position i
			if !hasEscape {
				return string(p.data[start:i]), nil
			}
			return p.unescapeString(p.data[start:i]), nil
		}
		i++
	}
	return "", errUnexpectedEOF
}

// unescapeString processes JSON escape sequences in a string.
func (p *Parser) unescapeString(raw []byte) string {
	buf := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) {
			i++
			switch raw[i] {
			case '"', '\\', '/':
				buf = append(buf, raw[i])
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
				// Simple \uXXXX handling — decode 4 hex digits
				if i+4 < len(raw) {
					r, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 32)
					if err == nil {
						buf = append(buf, string(rune(r))...)
						i += 4
						continue
					}
				}
				buf = append(buf, '\\', 'u')
			default:
				buf = append(buf, '\\', raw[i])
			}
		} else {
			buf = append(buf, raw[i])
		}
	}
	return string(buf)
}

func (p *Parser) parseStringValue(ti *typeInfo, ptr unsafe.Pointer) error {
	s, err := p.parseStringRaw()
	if err != nil {
		return err
	}

	switch ti.kind {
	case kindString:
		*(*string)(ptr) = s
	case kindAny:
		*(*any)(ptr) = s
	default:
		return fmt.Errorf("pjson: cannot assign string to %v field", ti.kind)
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

func (p *Parser) parseNumberValue(ti *typeInfo, ptr unsafe.Pointer) error {
	span, err := p.parseNumberSpan()
	if err != nil {
		return err
	}

	switch ti.kind {
	case kindInt, kindInt8, kindInt16, kindInt32, kindInt64:
		v, err := strconv.ParseInt(string(span), 10, 64)
		if err != nil {
			return fmt.Errorf("pjson: invalid integer %q: %w", span, err)
		}
		writeIntValue(ptr, ti.kind, v)
	case kindUint, kindUint8, kindUint16, kindUint32, kindUint64:
		v, err := strconv.ParseUint(string(span), 10, 64)
		if err != nil {
			return fmt.Errorf("pjson: invalid unsigned integer %q: %w", span, err)
		}
		writeUintValue(ptr, ti.kind, v)
	case kindFloat32:
		v, err := strconv.ParseFloat(string(span), 32)
		if err != nil {
			return fmt.Errorf("pjson: invalid float %q: %w", span, err)
		}
		*(*float32)(ptr) = float32(v)
	case kindFloat64:
		v, err := strconv.ParseFloat(string(span), 64)
		if err != nil {
			return fmt.Errorf("pjson: invalid float %q: %w", span, err)
		}
		*(*float64)(ptr) = v
	case kindAny:
		v, err := strconv.ParseFloat(string(span), 64)
		if err != nil {
			return fmt.Errorf("pjson: invalid number %q: %w", span, err)
		}
		*(*any)(ptr) = v
	default:
		return fmt.Errorf("pjson: cannot assign number to %v field", ti.kind)
	}
	return nil
}

func (p *Parser) parseNumberAny() (any, error) {
	span, err := p.parseNumberSpan()
	if err != nil {
		return nil, err
	}
	v, err := strconv.ParseFloat(string(span), 64)
	if err != nil {
		return nil, fmt.Errorf("pjson: invalid number %q: %w", span, err)
	}
	return v, nil
}

func writeIntValue(ptr unsafe.Pointer, kind elemTypeKind, v int64) {
	switch kind {
	case kindInt:
		*(*int)(ptr) = int(v)
	case kindInt8:
		*(*int8)(ptr) = int8(v)
	case kindInt16:
		*(*int16)(ptr) = int16(v)
	case kindInt32:
		*(*int32)(ptr) = int32(v)
	case kindInt64:
		*(*int64)(ptr) = v
	}
}

func writeUintValue(ptr unsafe.Pointer, kind elemTypeKind, v uint64) {
	switch kind {
	case kindUint:
		*(*uint)(ptr) = uint(v)
	case kindUint8:
		*(*uint8)(ptr) = uint8(v)
	case kindUint16:
		*(*uint16)(ptr) = uint16(v)
	case kindUint32:
		*(*uint32)(ptr) = uint32(v)
	case kindUint64:
		*(*uint64)(ptr) = v
	}
}

// =============================================================================
// Bool Parsing
// =============================================================================

func (p *Parser) parseBoolValue(ti *typeInfo, ptr unsafe.Pointer) error {
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

	switch ti.kind {
	case kindBool:
		*(*bool)(ptr) = val
	case kindAny:
		*(*any)(ptr) = val
	default:
		return fmt.Errorf("pjson: cannot assign bool to %v field", ti.kind)
	}
	return nil
}

// =============================================================================
// Null Parsing
// =============================================================================

func (p *Parser) parseNullValue(ti *typeInfo, ptr unsafe.Pointer) error {
	p.next() // consume 'n'

	switch ti.kind {
	case kindString:
		*(*string)(ptr) = ""
	case kindBool:
		*(*bool)(ptr) = false
	case kindInt, kindInt8, kindInt16, kindInt32, kindInt64:
		writeIntValue(ptr, ti.kind, 0)
	case kindUint, kindUint8, kindUint16, kindUint32, kindUint64:
		writeUintValue(ptr, ti.kind, 0)
	case kindFloat32:
		*(*float32)(ptr) = 0
	case kindFloat64:
		*(*float64)(ptr) = 0
	case kindPointer:
		// Set pointer to nil — already handled by parsePointerValue
		// but if called directly, zero the pointer.
		*(*unsafe.Pointer)(ptr) = nil
	case kindSlice:
		// Set slice to nil
		*(*[]byte)(ptr) = nil
	case kindMap:
		// Set map to nil — write nil pointer at the map location
		reflect.NewAt(reflect.MapOf(reflect.TypeOf(""), reflect.TypeOf((*any)(nil)).Elem()), ptr).Elem().Set(reflect.Zero(reflect.MapOf(reflect.TypeOf(""), reflect.TypeOf((*any)(nil)).Elem())))
	case kindStruct:
		// null for struct: zero the struct (like encoding/json)
		// no-op: struct is already at its zero value if freshly allocated
	case kindAny:
		*(*any)(ptr) = nil
	}
	return nil
}

// =============================================================================
// Object Parsing
// =============================================================================

func (p *Parser) parseObjectValue(ti *typeInfo, ptr unsafe.Pointer) error {
	switch ti.kind {
	case kindStruct:
		return p.parseObjectToStruct(ti, ptr)
	case kindMap:
		return p.parseObjectToMap(ti, ptr)
	case kindAny:
		m, err := p.parseObjectAny()
		if err != nil {
			return err
		}
		*(*any)(ptr) = m
		return nil
	default:
		return fmt.Errorf("pjson: cannot assign object to %v field", ti.kind)
	}
}

func (p *Parser) parseObjectToStruct(ti *typeInfo, base unsafe.Pointer) error {
	p.next() // consume '{'

	sDec := ti.decoder.(*reflectStructDecoder)

	// Empty object
	if p.peek() == '}' {
		p.next() // consume '}'
		return nil
	}

	for {
		// Key
		key, err := p.parseStringRaw()
		if err != nil {
			return err
		}

		// Colon
		b := p.next()
		if b != ':' {
			return fmt.Errorf("pjson: expected ':' after key %q, got %q", key, string(b))
		}

		// Value
		fi := sDec.lookupField(key)
		if fi == nil {
			// Unknown field — skip the value
			if err := p.skipValue(); err != nil {
				return err
			}
		} else {
			fieldPtr := unsafe.Add(base, fi.offset)
			if err := p.parseValue(&typeInfo{kind: fi.kind, decoder: fi.decoder}, fieldPtr); err != nil {
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

func (p *Parser) parseObjectToMap(ti *typeInfo, ptr unsafe.Pointer) error {
	p.next() // consume '{'

	mDec := ti.decoder.(*reflectMapDecoder)

	// Obtain reflect.Value of the map
	mapPtr := reflect.NewAt(mDec.mapType, ptr)
	mapVal := mapPtr.Elem()
	if mapVal.IsNil() {
		mapVal.Set(reflect.MakeMap(mDec.mapType))
	}

	// Empty object
	if p.peek() == '}' {
		p.next() // consume '}'
		return nil
	}

	for {
		// Key
		key, err := p.parseStringRaw()
		if err != nil {
			return err
		}

		// Colon
		b := p.next()
		if b != ':' {
			return fmt.Errorf("pjson: expected ':' in map object")
		}

		// Value
		valRV := reflect.New(mDec.valType)
		valPtr := valRV.UnsafePointer()
		if err := p.parseValue(mDec.valTI, valPtr); err != nil {
			return err
		}
		mapVal.SetMapIndex(reflect.ValueOf(key), valRV.Elem())

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
		key, err := p.parseStringRaw()
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

func (p *Parser) parseArrayValue(ti *typeInfo, ptr unsafe.Pointer) error {
	switch ti.kind {
	case kindSlice:
		return p.parseArrayToSlice(ti, ptr)
	case kindAny:
		arr, err := p.parseArrayAny()
		if err != nil {
			return err
		}
		*(*any)(ptr) = arr
		return nil
	default:
		return fmt.Errorf("pjson: cannot assign array to %v field", ti.kind)
	}
}

func (p *Parser) parseArrayToSlice(ti *typeInfo, ptr unsafe.Pointer) error {
	p.next() // consume '['

	sDec := ti.decoder.(*reflectSliceDecoder)

	// Empty array
	if p.peek() == ']' {
		p.next()
		// Set to empty non-nil slice
		sliceVal := reflect.MakeSlice(sDec.sliceType, 0, 0)
		reflect.NewAt(sDec.sliceType, ptr).Elem().Set(sliceVal)
		return nil
	}

	var elems []reflect.Value
	for {
		elemRV := reflect.New(sDec.elemType)
		elemPtr := elemRV.UnsafePointer()
		if err := p.parseValue(sDec.elemTI, elemPtr); err != nil {
			return err
		}
		elems = append(elems, elemRV.Elem())

		b := p.peek()
		if b == ',' {
			p.next()
			continue
		}
		if b == ']' {
			p.next()
			break
		}
		return fmt.Errorf("pjson: expected ',' or ']' in array, got %q", string(b))
	}

	sliceVal := reflect.MakeSlice(sDec.sliceType, len(elems), len(elems))
	for i, e := range elems {
		sliceVal.Index(i).Set(e)
	}
	reflect.NewAt(sDec.sliceType, ptr).Elem().Set(sliceVal)
	return nil
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

func (p *Parser) parsePointerValue(ti *typeInfo, ptr unsafe.Pointer) error {
	pDec := ti.decoder.(*reflectPointerDecoder)

	// null → set pointer to nil
	if p.peek() == 'n' {
		p.next() // consume 'n'
		*(*unsafe.Pointer)(ptr) = nil
		return nil
	}

	// Allocate a new element and parse into it
	elemRV := reflect.New(pDec.elemType)
	elemPtr := elemRV.UnsafePointer()
	if err := p.parseValue(pDec.elemTI, elemPtr); err != nil {
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
	b := p.peek()
	switch {
	case b == '{':
		return p.skipObject()
	case b == '[':
		return p.skipArray()
	case b == '"':
		_, err := p.parseStringRaw() // consume the string
		return err
	case b == 't', b == 'f', b == 'n':
		p.next() // consume keyword token
		return nil
	case (b >= '0' && b <= '9') || b == '-':
		_, err := p.parseNumberSpan() // consume number
		return err
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
		_, err := p.parseStringRaw()
		if err != nil {
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
