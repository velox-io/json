package vjson

import (
	"fmt"
	"reflect"
	"strconv"
	"unsafe"
)

const (
	// 32-bit little-endian representations for literal validation
	lit_true = uint32(0x65757274) // "true"
	lit_alse = uint32(0x65736c61) // "alse"
	lit_null = uint32(0x6c6c756e) // "null"
)

func (sc *Parser) scanValue(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	if ti.Kind == KindPointer {
		return sc.scanPointer(src, idx, ti, ptr)
	}
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch src[idx] {
	case '"':
		return sc.scanStringValue(src, idx, ti, ptr)
	case '{':
		return sc.scanObjectValue(src, idx, ti, ptr)
	case '[':
		return sc.scanArrayValue(src, idx, ti, ptr)
	case 't':
		return sc.scanTrue(src, idx, ti, ptr)
	case 'f':
		return sc.scanFalse(src, idx, ti, ptr)
	case 'n':
		return sc.scanNull(src, idx, ti, ptr)
	default:
		if (src[idx] >= '0' && src[idx] <= '9') || src[idx] == '-' {
			return sc.scanNumber(src, idx, ti, ptr)
		}
		return idx, fmt.Errorf("vjson: unexpected character %q at offset %d", src[idx], idx)
	}
}

// --- String Scanning ---

// scanStringValue is an optimized string scanner that finds the closing quote
// in a single pass and performs in-place unescaping without intermediate allocations.
//
// findQuoteOrBackslash is manually inlined because the Go compiler
// does not inline it (cost 143 > budget 80).
func (sc *Parser) scanStringValue(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	start := idx + 1
	n := len(src)

	// Quick check for empty string
	if start >= n {
		return start, errUnexpectedEOF
	}

	// Process 8 bytes at a time looking for '"' or '\\'
	pos := start
	for pos+8 <= n {
		w := *(*uint64)(unsafe.Pointer(&src[pos]))

		// Check for quote (0x22)
		mq := hasZeroByte(w ^ (lo64 * 0x22))
		// Check for backslash (0x5C)
		mb := hasZeroByte(w ^ (lo64 * 0x5C))
		// Check for control chars (< 0x20)
		mc := (w - lo64*0x20) & ^w & hi64

		if (mq | mb | mc) == 0 {
			pos += 8
			continue
		}

		// Found something - determine which came first
		combined := mq | mb | mc
		off := firstMarkedByteIndex(combined)
		foundPos := pos + off
		foundChar := src[foundPos]

		if foundChar == '"' {
			// Fast path: no escapes, zero-copy
			raw := src[start:foundPos]
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
				return foundPos + 1, fmt.Errorf("vjson: cannot assign string to %v field", ti.Kind)
			}
			return foundPos + 1, nil
		}

		if foundChar == '\\' {
			// Has escapes - find closing quote and unescape
			return sc.processEscapedString(src, start, foundPos, ti, ptr)
		}

		// Control character
		return foundPos, fmt.Errorf("vjson: control character in string at offset %d", foundPos)
	}

	// Handle remaining bytes (tail)
	for pos < n {
		c := src[pos]
		if c == '"' {
			// Fast path: no escapes
			raw := src[start:pos]
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
				return pos + 1, fmt.Errorf("vjson: cannot assign string to %v field", ti.Kind)
			}
			return pos + 1, nil
		}
		if c == '\\' {
			return sc.processEscapedString(src, start, pos, ti, ptr)
		}
		if c < 0x20 {
			return pos, fmt.Errorf("vjson: control character in string at offset %d", pos)
		}
		pos++
	}

	return n, errUnexpectedEOF
}

// scanStringBytes scans a JSON string starting at idx (pointing to the opening '"').
// Returns (newIdx, rawBytes, error). rawBytes is the decoded string content.
// Fast path (no escapes): zero-copy slice into src.
//
// findQuoteOrBackslash is manually inlined because the Go compiler
// does not inline it (cost 143 > budget 80).
func (sc *Parser) scanStringBytes(src []byte, idx int) (int, []byte, error) {
	start := idx + 1
	n := len(src)

	// SWAR scan 8 bytes at a time for '"', '\\', or control chars (< 0x20)
	pos := start
	for pos+8 <= n {
		w := *(*uint64)(unsafe.Pointer(&src[pos]))
		mq := hasZeroByte(w ^ (lo64 * 0x22)) // '"'
		mb := hasZeroByte(w ^ (lo64 * 0x5C)) // '\\'
		mc := (w - lo64*0x20) & ^w & hi64    // < 0x20
		combined := mq | mb | mc
		if combined != 0 {
			off := firstMarkedByteIndex(combined)
			foundIdx := pos + off
			c := src[foundIdx]
			if c == '"' {
				return foundIdx + 1, src[start:foundIdx], nil
			}
			if c == '\\' {
				return sc.unescapeSinglePass(src, start, foundIdx)
			}
			return foundIdx, nil, fmt.Errorf("vjson: control character in string at offset %d", foundIdx)
		}
		pos += 8
	}

	for pos < n {
		c := src[pos]
		if c == '"' {
			return pos + 1, src[start:pos], nil
		}
		if c == '\\' {
			return sc.unescapeSinglePass(src, start, pos)
		}
		if c < 0x20 {
			return pos, nil, fmt.Errorf("vjson: control character in string at offset %d", pos)
		}
		pos++
	}
	return n, nil, errUnexpectedEOF
}

// scanStringAny scans a JSON string and returns it as a Go string.
// Fast path: zero-copy via unsafe.String. Slow path: allocate + unescape.
//
// findQuoteOrBackslash is manually inlined because the Go compiler
// does not inline it (cost 143 > budget 80).
func (sc *Parser) scanStringAny(src []byte, idx int) (int, string, error) {
	start := idx + 1
	n := len(src)

	// SWAR scan 8 bytes at a time for '"', '\\', or control chars (< 0x20)
	pos := start
	for pos+8 <= n {
		w := *(*uint64)(unsafe.Pointer(&src[pos]))
		mq := hasZeroByte(w ^ (lo64 * 0x22)) // '"'
		mb := hasZeroByte(w ^ (lo64 * 0x5C)) // '\\'
		mc := (w - lo64*0x20) & ^w & hi64    // < 0x20
		combined := mq | mb | mc
		if combined != 0 {
			off := firstMarkedByteIndex(combined)
			foundIdx := pos + off
			c := src[foundIdx]
			if c == '"' {
				return foundIdx + 1, UnsafeString(src[start:foundIdx]), nil
			}
			if c == '\\' {
				endIdx, result, err := sc.unescapeSinglePass(src, start, foundIdx)
				if err != nil {
					return endIdx, "", err
				}
				return endIdx, unsafe.String(unsafe.SliceData(result), len(result)), nil
			}
			return foundIdx, "", fmt.Errorf("vjson: control character in string at offset %d", foundIdx)
		}
		pos += 8
	}

	for pos < n {
		c := src[pos]
		if c == '"' {
			return pos + 1, UnsafeString(src[start:pos]), nil
		}
		if c == '\\' {
			endIdx, result, err := sc.unescapeSinglePass(src, start, pos)
			if err != nil {
				return endIdx, "", err
			}
			return endIdx, unsafe.String(unsafe.SliceData(result), len(result)), nil
		}
		if c < 0x20 {
			return pos, "", fmt.Errorf("vjson: control character in string at offset %d", pos)
		}
		pos++
	}
	return n, "", errUnexpectedEOF
}

// --- Number Scanning ---

// scanNumberSpan finds the end of a JSON number starting at idx,
// validating the format per RFC 8259 §6:
//
//	number = [ "-" ] int [ frac ] [ exp ]
//	int    = "0" / ( digit1-9 *DIGIT )
//	frac   = "." 1*DIGIT
//	exp    = ( "e" / "E" ) [ "+" / "-" ] 1*DIGIT
//
// Returns (endIdx, isFloat, error).
func scanNumberSpan(src []byte, idx int) (int, bool, error) {
	i := idx
	n := len(src)

	// Optional leading '-'
	if i < n && src[i] == '-' {
		i++
	}

	// Integer part (required)
	if i >= n || src[i] < '0' || src[i] > '9' {
		return i, false, fmt.Errorf("vjson: invalid number at offset %d", idx)
	}
	if src[i] == '0' {
		i++
		// Leading zeros forbidden: "0" must not be followed by another digit
		if i < n && src[i] >= '0' && src[i] <= '9' {
			return i, false, fmt.Errorf("vjson: leading zeros in number at offset %d", idx)
		}
	} else {
		// 1-9 followed by any digits
		i++
		for i < n && src[i] >= '0' && src[i] <= '9' {
			i++
		}
	}

	isFloat := false

	// Optional fraction
	if i < n && src[i] == '.' {
		isFloat = true
		i++
		// Must have at least one digit after '.'
		if i >= n || src[i] < '0' || src[i] > '9' {
			return i, true, fmt.Errorf("vjson: invalid fraction in number at offset %d", idx)
		}
		i++
		for i < n && src[i] >= '0' && src[i] <= '9' {
			i++
		}
	}

	// Optional exponent
	if i < n && (src[i] == 'e' || src[i] == 'E') {
		isFloat = true
		i++
		// Optional sign
		if i < n && (src[i] == '+' || src[i] == '-') {
			i++
		}
		// Must have at least one digit after exponent marker
		if i >= n || src[i] < '0' || src[i] > '9' {
			return i, true, fmt.Errorf("vjson: invalid exponent in number at offset %d", idx)
		}
		i++
		for i < n && src[i] >= '0' && src[i] <= '9' {
			i++
		}
	}

	return i, isFloat, nil
}

func (sc *Parser) scanNumber(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	end, isFloat, numErr := scanNumberSpan(src, idx)
	if numErr != nil {
		return end, numErr
	}

	switch ti.Kind {
	case KindInt, KindInt8, KindInt16, KindInt32, KindInt64:
		if isFloat {
			return end, fmt.Errorf("vjson: cannot unmarshal number %q into integer field", src[idx:end])
		}
		v := parseInt64(src, idx, end)
		WriteIntValue(ptr, ti.Kind, v)
	case KindUint, KindUint8, KindUint16, KindUint32, KindUint64:
		if isFloat {
			return end, fmt.Errorf("vjson: cannot unmarshal number %q into unsigned integer field", src[idx:end])
		}
		v := parseUint64(src, idx, end)
		WriteUintValue(ptr, ti.Kind, v)
	case KindFloat32:
		v, err := strconv.ParseFloat(UnsafeString(src[idx:end]), 32)
		if err != nil {
			return end, fmt.Errorf("vjson: invalid float %q: %w", src[idx:end], err)
		}
		*(*float32)(ptr) = float32(v)
	case KindFloat64:
		v, err := strconv.ParseFloat(UnsafeString(src[idx:end]), 64)
		if err != nil {
			return end, fmt.Errorf("vjson: invalid float %q: %w", src[idx:end], err)
		}
		*(*float64)(ptr) = v
	case KindAny:
		if isFloat {
			v, err := strconv.ParseFloat(UnsafeString(src[idx:end]), 64)
			if err != nil {
				return end, fmt.Errorf("vjson: invalid number %q: %w", src[idx:end], err)
			}
			*(*any)(ptr) = v
		} else {
			// encoding/json compatible: all numbers → float64 for interface{}
			v, err := strconv.ParseFloat(UnsafeString(src[idx:end]), 64)
			if err != nil {
				return end, fmt.Errorf("vjson: invalid number %q: %w", src[idx:end], err)
			}
			*(*any)(ptr) = v
		}
	default:
		return end, fmt.Errorf("vjson: cannot assign number to %v field", ti.Kind)
	}
	return end, nil
}

// scanNumberAny parses a number for interface{} context.
// Uses interned floats for small integers (0-255) to avoid allocation.
func (sc *Parser) scanNumberAny(src []byte, idx int) (int, any, error) {
	end, _, numErr := scanNumberSpan(src, idx)
	if numErr != nil {
		return end, nil, numErr
	}
	span := src[idx:end]

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
			return end, InternedFloats[val], nil
		}
	}

	v, err := strconv.ParseFloat(UnsafeString(span), 64)
	if err != nil {
		return end, nil, fmt.Errorf("vjson: invalid number %q: %w", span, err)
	}
	return end, v, nil
}

func (sc *Parser) scanTrue(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	if idx+4 > len(src) {
		return idx, errUnexpectedEOF
	}
	if *(*uint32)(unsafe.Pointer(&src[idx])) != lit_true {
		return idx, fmt.Errorf("vjson: invalid literal at offset %d", idx)
	}
	switch ti.Kind {
	case KindBool:
		*(*bool)(ptr) = true
	case KindAny:
		*(*any)(ptr) = true
	default:
		return idx + 4, fmt.Errorf("vjson: cannot assign bool to %v field", ti.Kind)
	}
	return idx + 4, nil
}

func (sc *Parser) scanFalse(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	if idx+5 > len(src) {
		return idx, errUnexpectedEOF
	}
	if *(*uint32)(unsafe.Pointer(&src[idx+1])) != lit_alse {
		return idx, fmt.Errorf("vjson: invalid literal at offset %d", idx)
	}
	switch ti.Kind {
	case KindBool:
		*(*bool)(ptr) = false
	case KindAny:
		*(*any)(ptr) = false
	default:
		return idx + 5, fmt.Errorf("vjson: cannot assign bool to %v field", ti.Kind)
	}
	return idx + 5, nil
}

func (sc *Parser) scanNull(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	if idx+4 > len(src) {
		return idx, errUnexpectedEOF
	}
	if *(*uint32)(unsafe.Pointer(&src[idx])) != lit_null {
		return idx, fmt.Errorf("vjson: invalid literal at offset %d", idx)
	}
	newIdx := idx + 4

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
		*(*unsafe.Pointer)(ptr) = nil
	case KindSlice:
		*(*SliceHeader)(ptr) = SliceHeader{}
	case KindMap:
		// nil map
		mapDec := ti.Decoder.(*ReflectMapDecoder)
		reflect.NewAt(mapDec.MapType, ptr).Elem().Set(reflect.Zero(mapDec.MapType))
	case KindStruct:
		// no-op: struct is already at zero value
	case KindAny:
		*(*any)(ptr) = nil
	}
	return newIdx, nil
}

func (sc *Parser) scanObjectValue(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	switch ti.Kind {
	case KindStruct:
		return sc.scanObject(src, idx, ti.Decoder.(*ReflectStructDecoder), ptr)
	case KindMap:
		return sc.scanObjectToMap(src, idx, ti.Decoder.(*ReflectMapDecoder), ptr)
	case KindAny:
		newIdx, m, err := sc.scanObjectAny(src, idx)
		if err != nil {
			return newIdx, err
		}
		*(*any)(ptr) = m
		return newIdx, nil
	default:
		return idx, fmt.Errorf("vjson: cannot assign object to %v field", ti.Kind)
	}
}

func (sc *Parser) scanObject(src []byte, idx int, dec *ReflectStructDecoder, base unsafe.Pointer) (int, error) {
	idx++ // consume '{'
	idx = skipWSLong(src, idx)
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}

	// Empty object
	if src[idx] == '}' {
		return idx + 1, nil
	}

	for {
		// Key
		if idx >= len(src) || src[idx] != '"' {
			return idx, errSyntax
		}
		var keyBytes []byte
		var err error
		idx, keyBytes, err = sc.scanStringBytes(src, idx)
		if err != nil {
			return idx, err
		}

		// Colon
		idx = skipWS(src, idx)
		if idx >= len(src) || src[idx] != ':' {
			return idx, errSyntax
		}
		idx++ // consume ':'
		idx = skipWS(src, idx)

		// Value — lookup field
		fi := dec.LookupFieldBytes(keyBytes, sc.scratch[:])
		if fi == nil {
			// Unknown field — skip value
			idx, err = skipValue(src, idx)
			if err != nil {
				return idx, err
			}
		} else {
			fieldPtr := unsafe.Add(base, fi.Offset)
			idx, err = sc.scanValue(src, idx, fi, fieldPtr)
			if err != nil {
				return idx, err
			}
		}

		// Comma or closing brace
		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if src[idx] == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if src[idx] == '}' {
			return idx + 1, nil
		}
		return idx, fmt.Errorf("vjson: expected ',' or '}' in object, got %q", src[idx])
	}
}

func (sc *Parser) scanObjectToMap(src []byte, idx int, mDec *ReflectMapDecoder, ptr unsafe.Pointer) (int, error) {
	// Fast path for map[string]string - zero reflection
	if mDec.ValIsString {
		return sc.scanMapStringString(src, idx, ptr)
	}

	idx++ // consume '{'
	idx = skipWSLong(src, idx)

	mapPtr := reflect.NewAt(mDec.MapType, ptr)
	mapVal := mapPtr.Elem()
	if mapVal.IsNil() {
		mapVal.Set(reflect.MakeMap(mDec.MapType))
	}

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if src[idx] == '}' {
		return idx + 1, nil
	}

	for {
		// Key
		if idx >= len(src) || src[idx] != '"' {
			return idx, errSyntax
		}
		var key string
		var err error
		idx, key, err = sc.scanStringAny(src, idx)
		if err != nil {
			return idx, err
		}

		// Colon
		idx = skipWS(src, idx)
		if idx >= len(src) || src[idx] != ':' {
			return idx, errSyntax
		}
		idx++
		idx = skipWS(src, idx)

		// Value
		valRV := reflect.New(mDec.ValType)
		valPtr := valRV.UnsafePointer()
		idx, err = sc.scanValue(src, idx, mDec.ValTI, valPtr)
		if err != nil {
			return idx, err
		}
		mapVal.SetMapIndex(reflect.ValueOf(key), valRV.Elem())

		// Comma or closing brace
		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if src[idx] == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if src[idx] == '}' {
			return idx + 1, nil
		}
		return idx, fmt.Errorf("vjson: expected ',' or '}' in map, got %q", src[idx])
	}
}

// scanMapStringString is a zero-reflection fast path for map[string]string.
func (sc *Parser) scanMapStringString(src []byte, idx int, ptr unsafe.Pointer) (int, error) {
	idx++ // consume '{'
	idx = skipWSLong(src, idx)

	// Get or create the map
	m := *(*map[string]string)(ptr)
	if m == nil {
		m = make(map[string]string)
		*(*map[string]string)(ptr) = m
	}

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if src[idx] == '}' {
		return idx + 1, nil
	}

	for {
		// Key
		if idx >= len(src) || src[idx] != '"' {
			return idx, errSyntax
		}
		var key string
		var err error
		idx, key, err = sc.scanStringAny(src, idx)
		if err != nil {
			return idx, err
		}

		// Colon
		idx = skipWS(src, idx)
		if idx >= len(src) || src[idx] != ':' {
			return idx, errSyntax
		}
		idx++
		idx = skipWS(src, idx)

		// Value - zero-copy string scan
		var val string
		idx, val, err = sc.scanStringAny(src, idx)
		if err != nil {
			return idx, err
		}
		m[key] = val

		// Comma or closing brace
		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if src[idx] == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if src[idx] == '}' {
			return idx + 1, nil
		}
		return idx, fmt.Errorf("vjson: expected ',' or '}' in map, got %q", src[idx])
	}
}

func (sc *Parser) scanObjectAny(src []byte, idx int) (int, map[string]any, error) {
	idx++ // consume '{'
	idx = skipWSLong(src, idx)

	m := make(map[string]any)

	if idx >= len(src) {
		return idx, nil, errUnexpectedEOF
	}
	if src[idx] == '}' {
		return idx + 1, m, nil
	}

	for {
		if idx >= len(src) || src[idx] != '"' {
			return idx, nil, errSyntax
		}
		var key string
		var err error
		idx, key, err = sc.scanStringAny(src, idx)
		if err != nil {
			return idx, nil, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) || src[idx] != ':' {
			return idx, nil, errSyntax
		}
		idx++
		idx = skipWS(src, idx)

		var val any
		idx, val, err = sc.scanAnyValue(src, idx)
		if err != nil {
			return idx, nil, err
		}
		m[key] = val

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if src[idx] == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if src[idx] == '}' {
			return idx + 1, m, nil
		}
		return idx, nil, fmt.Errorf("vjson: expected ',' or '}' in any object, got %q", src[idx])
	}
}

func (sc *Parser) scanArrayValue(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	switch ti.Kind {
	case KindSlice:
		return sc.scanArray(src, idx, ti.Decoder.(*ReflectSliceDecoder), ptr)
	case KindAny:
		newIdx, arr, err := sc.scanArrayAny(src, idx)
		if err != nil {
			return newIdx, err
		}
		*(*any)(ptr) = arr
		return newIdx, nil
	default:
		return idx, fmt.Errorf("vjson: cannot assign array to %v field", ti.Kind)
	}
}

func (sc *Parser) scanArray(src []byte, idx int, sDec *ReflectSliceDecoder, ptr unsafe.Pointer) (int, error) {
	idx++ // consume '['
	idx = skipWSLong(src, idx)

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}

	// Empty array
	if src[idx] == ']' {
		sh := (*SliceHeader)(ptr)
		sh.Data = sDec.EmptySliceData
		sh.Len = 0
		sh.Cap = 0
		return idx + 1, nil
	}

	// Build slice with initial capacity of 2 (same as Sonic) to minimize over-allocation.
	const initCap = 2
	elemSize := sDec.ElemSize
	cap_ := initCap
	len_ := 0

	// Two allocation strategies based on whether elements contain GC-managed pointers:
	//
	// Pointer-free elements (e.g. int, float64, [4]byte):
	//   Use make([]byte) for zero-reflection allocation. Safe because GC doesn't
	//   need to scan inside these elements — no pointers to track.
	//
	// Pointer-containing elements (e.g. string, *T, struct with string fields):
	//   Use unsafe_NewArray (runtime.mallocgc via go:linkname) to allocate with
	//   correct type metadata, so GC can scan pointer fields correctly.
	//   Growth copies use typedslicecopy to trigger write barriers.
	var base unsafe.Pointer
	var backingBytes []byte // kept alive for pointer-free path

	if sDec.ElemHasPtr {
		base = unsafe_NewArray(sDec.ElemRType, initCap)
	} else {
		backingBytes = make([]byte, initCap*int(elemSize))
		base = unsafe.Pointer(&backingBytes[0])
	}

	for {
		// Grow if needed
		if len_ == cap_ {
			newCap := cap_ * 2
			if sDec.ElemHasPtr {
				newBase := unsafe_NewArray(sDec.ElemRType, newCap)
				// typedslicecopy triggers write barriers, ensuring GC
				// correctly tracks pointer fields during concurrent marking.
				typedslicecopy(sDec.ElemRType, newBase, newCap, base, len_)
				base = newBase
			} else {
				newBacking := make([]byte, newCap*int(elemSize))
				copy(newBacking, backingBytes)
				backingBytes = newBacking
				base = unsafe.Pointer(&backingBytes[0])
			}
			cap_ = newCap
		}

		elemPtr := unsafe.Add(base, uintptr(len_)*elemSize)
		len_++

		var err error
		idx, err = sc.scanValue(src, idx, sDec.ElemTI, elemPtr)
		if err != nil {
			return idx, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if src[idx] == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if src[idx] == ']' {
			sh := (*SliceHeader)(ptr)
			sh.Data = base
			sh.Len = len_
			sh.Cap = cap_
			return idx + 1, nil
		}
		return idx, fmt.Errorf("vjson: expected ',' or ']' in array, got %q", src[idx])
	}
}

func (sc *Parser) scanArrayAny(src []byte, idx int) (int, []any, error) {
	idx++ // consume '['
	idx = skipWSLong(src, idx)

	if idx >= len(src) {
		return idx, nil, errUnexpectedEOF
	}
	if src[idx] == ']' {
		return idx + 1, []any{}, nil
	}

	var arr []any
	for {
		var val any
		var err error
		idx, val, err = sc.scanAnyValue(src, idx)
		if err != nil {
			return idx, nil, err
		}
		arr = append(arr, val)

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if src[idx] == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if src[idx] == ']' {
			return idx + 1, arr, nil
		}
		return idx, nil, fmt.Errorf("vjson: expected ',' or ']' in any array, got %q", src[idx])
	}
}

func (sc *Parser) scanPointer(src []byte, idx int, ti *TypeInfo, ptr unsafe.Pointer) (int, error) {
	pDec := ti.Decoder.(*ReflectPointerDecoder)

	idx = skipWS(src, idx)
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}

	// null → set pointer to nil
	if src[idx] == 'n' {
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		*(*unsafe.Pointer)(ptr) = nil
		return idx + 4, nil
	}

	// Allocate a new element.
	// For pointer-free types, use make([]byte) (fast, no reflect overhead).
	// For pointer-containing types, use unsafe_New (runtime.mallocgc via
	// go:linkname) for GC-correct type metadata.
	var elemPtr unsafe.Pointer
	if pDec.ElemHasPtr {
		elemPtr = sc.ptrAlloc(pDec.ElemRType, pDec.ElemSize)
	} else {
		backing := make([]byte, pDec.ElemSize)
		elemPtr = unsafe.Pointer(&backing[0])
	}

	newIdx, err := sc.scanValue(src, idx, pDec.ElemTI, elemPtr)
	if err != nil {
		return newIdx, err
	}

	*(*unsafe.Pointer)(ptr) = elemPtr
	return newIdx, nil
}

// ptrAlloc returns a zeroed element from the per-type batch allocator.
// On first call for a given rtype, an allocator is created. Batches are
// allocated via unsafe_NewArray for GC-correct type metadata.
func (sc *Parser) ptrAlloc(rtype unsafe.Pointer, elemSize uintptr) unsafe.Pointer {
	a, ok := sc.ptrAllocs[rtype]
	if !ok {
		a = &rtypeAllocator{
			rtype:    rtype,
			elemSize: elemSize,
			cap:      ptrBatchSize,
			offset:   ptrBatchSize, // forces alloc on first use
		}
		sc.ptrAllocs[rtype] = a
	}
	return a.alloc()
}

func (sc *Parser) scanAnyValue(src []byte, idx int) (int, any, error) {
	if idx >= len(src) {
		return idx, nil, errUnexpectedEOF
	}
	switch src[idx] {
	case '"':
		newIdx, s, err := sc.scanStringAny(src, idx)
		return newIdx, s, err
	case '{':
		newIdx, m, err := sc.scanObjectAny(src, idx)
		return newIdx, m, err
	case '[':
		newIdx, arr, err := sc.scanArrayAny(src, idx)
		return newIdx, arr, err
	case 't':
		if idx+4 > len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != lit_true {
			return idx, nil, fmt.Errorf("vjson: invalid literal at offset %d", idx)
		}
		return idx + 4, true, nil
	case 'f':
		if idx+5 > len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx+1])) != lit_alse {
			return idx, nil, fmt.Errorf("vjson: invalid literal at offset %d", idx)
		}
		return idx + 5, false, nil
	case 'n':
		if idx+4 > len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != lit_null {
			return idx, nil, fmt.Errorf("vjson: invalid literal at offset %d", idx)
		}
		return idx + 4, nil, nil
	default:
		if (src[idx] >= '0' && src[idx] <= '9') || src[idx] == '-' {
			return sc.scanNumberAny(src, idx)
		}
		return idx, nil, fmt.Errorf("vjson: unexpected character %q in any value", src[idx])
	}
}

// skipValue skips a complete JSON value starting at idx.
// Uses depth counting for objects/arrays instead of recursion.
func skipValue(src []byte, idx int) (int, error) {
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch src[idx] {
	case '"':
		return skipString(src, idx)
	case 't':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != lit_true {
			return idx, fmt.Errorf("vjson: invalid literal at offset %d", idx)
		}
		return idx + 4, nil
	case 'f':
		if idx+5 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx+1])) != lit_alse {
			return idx, fmt.Errorf("vjson: invalid literal at offset %d", idx)
		}
		return idx + 5, nil
	case 'n':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != lit_null {
			return idx, fmt.Errorf("vjson: invalid literal at offset %d", idx)
		}
		return idx + 4, nil
	case '{', '[':
		return skipContainer(src, idx)
	default:
		if (src[idx] >= '0' && src[idx] <= '9') || src[idx] == '-' {
			end, _, numErr := scanNumberSpan(src, idx)
			if numErr != nil {
				return end, numErr
			}
			return end, nil
		}
		return idx, fmt.Errorf("vjson: unexpected character %q", src[idx])
	}
}

// skipString skips a JSON string starting at idx (the opening '"').
//
// findQuoteOrBackslash is manually inlined because the Go compiler
// does not inline it (cost 143 > budget 80).
func skipString(src []byte, idx int) (int, error) {
	i := idx + 1
	n := len(src)

	for i < n {
		// SWAR scan 8 bytes at a time for '"', '\\', or control chars (< 0x20)
		if i+8 <= n {
			w := *(*uint64)(unsafe.Pointer(&src[i]))
			mq := hasZeroByte(w ^ (lo64 * 0x22)) // '"'
			mb := hasZeroByte(w ^ (lo64 * 0x5C)) // '\\'
			mc := (w - lo64*0x20) & ^w & hi64    // < 0x20
			combined := mq | mb | mc
			if combined == 0 {
				i += 8
				continue
			}
			off := firstMarkedByteIndex(combined)
			c := src[i+off]
			if c == '"' {
				return i + off + 1, nil
			}
			if c == '\\' {
				// Validate escape sequence
				escIdx := i + off
				if escIdx+1 >= n {
					return escIdx, errUnexpectedEOF
				}
				next := src[escIdx+1]
				if next == 'u' {
					// \uXXXX — need 4 hex digits
					if escIdx+5 >= n {
						return escIdx, fmt.Errorf("vjson: invalid unicode escape in string at offset %d", escIdx)
					}
					for k := escIdx + 2; k < escIdx+6; k++ {
						if !isHexChar(src[k]) {
							return escIdx, fmt.Errorf("vjson: invalid unicode escape in string at offset %d", escIdx)
						}
					}
					i = escIdx + 6
				} else if escapeTable[next] != 0 {
					i = escIdx + 2
				} else {
					return escIdx, fmt.Errorf("vjson: invalid escape '\\%c' in string at offset %d", next, escIdx)
				}
				continue
			}
			// control character
			return i + off, fmt.Errorf("vjson: control character in string at offset %d", i+off)
		}

		// Tail: byte-at-a-time
		c := src[i]
		if c == '"' {
			return i + 1, nil
		}
		if c == '\\' {
			if i+1 >= n {
				return i, errUnexpectedEOF
			}
			next := src[i+1]
			if next == 'u' {
				if i+5 >= n {
					return i, fmt.Errorf("vjson: invalid unicode escape in string at offset %d", i)
				}
				for k := i + 2; k < i+6; k++ {
					if !isHexChar(src[k]) {
						return i, fmt.Errorf("vjson: invalid unicode escape in string at offset %d", i)
					}
				}
				i += 6
			} else if escapeTable[next] != 0 {
				i += 2
			} else {
				return i, fmt.Errorf("vjson: invalid escape '\\%c' in string at offset %d", next, i)
			}
			continue
		}
		if c < 0x20 {
			return i, fmt.Errorf("vjson: control character in string at offset %d", i)
		}
		i++
	}
	return i, errUnexpectedEOF
}

// skipContainer skips a JSON object or array using depth counting.
// findStructuralChar is manually inlined here because the Go compiler
// does not inline it (cost 185 > budget 80).
func skipContainer(src []byte, idx int) (int, error) {
	depth := 1
	i := idx + 1
	n := len(src)

	for i < n && depth > 0 {
		// Fast path: SWAR scan 8 bytes at a time for { } [ ] "
		if i+8 <= n {
			w := *(*uint64)(unsafe.Pointer(&src[i]))

			m := hasZeroByte(w ^ (lo64 * 0x7B)) // {
			m |= hasZeroByte(w ^ (lo64 * 0x7D)) // }
			m |= hasZeroByte(w ^ (lo64 * 0x5B)) // [
			m |= hasZeroByte(w ^ (lo64 * 0x5D)) // ]
			m |= hasZeroByte(w ^ (lo64 * 0x22)) // "

			if m == 0 {
				i += 8
				continue
			}

			off := firstMarkedByteIndex(m)
			c := src[i+off]
			i += off

			switch c {
			case '{', '[':
				depth++
				i++
			case '}', ']':
				depth--
				i++
			case '"':
				var err error
				i, err = skipString(src, i)
				if err != nil {
					return i, err
				}
			}
			continue
		}

		// Slow path: byte-by-byte for remaining < 8 bytes
		c := src[i]
		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case '"':
			var err error
			i, err = skipString(src, i)
			if err != nil {
				return i, err
			}
			continue
		}
		i++
	}

	if depth > 0 {
		return i, errUnexpectedEOF
	}
	return i, nil
}
