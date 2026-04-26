package vdec

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/typ"
)

const (
	// Little-endian literal words for branchless validation.
	litU32True = uint32(0x65757274) // "true"
	litU32Null = uint32(0x6c6c756e) // "null"
	litU32Alse = uint32(0x65736c61)
)

func invalidLiteralError(idx int) error {
	return newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
}

func unmarshalBoolTypeError(ti *DecTypeInfo, offset int) error {
	return newUnmarshalTypeError("bool", ti.Type, offset)
}

func nullifyMap(ti *DecTypeInfo, ptr unsafe.Pointer) {
	// typedmemmove keeps the old map pointer visible to the write barrier.
	var zero unsafe.Pointer
	gort.TypedMemmove(ti.TypePtr, ptr, unsafe.Pointer(&zero))
}

const (
	arenaBlockSize = 8192 // 8KB arena blocks
	arenaInlineMax = 1024 // small decoded strings kept in arena to reduce allocs
	scratchBufSize = 4096 // reusable scratch buffer size for single-pass decoding
)

// Parser is the reusable single-pass decoder core.
type Parser struct {
	scratchBuf [scratchBufSize]byte              // reusable scratch for decoding
	arenaData  []byte                            // current arena block
	arenaOff   int                               // next free offset in arenaData
	ptrAllocs  map[unsafe.Pointer]*TypeAllocator // per-type batch allocators for pointer fields
	useNumber  bool                              // decode numbers in interface{} as json.Number
	copyString bool                              // copy all strings instead of zero-copy
}

// arenaAlloc hands out bytes from the parser arena.
func (sc *Parser) arenaAlloc(size int) []byte {
	if sc.arenaData == nil || sc.arenaOff+size > len(sc.arenaData) {
		sc.arenaData = make([]byte, arenaBlockSize)
		sc.arenaOff = 0
	}
	p := sc.arenaData[sc.arenaOff : sc.arenaOff+size]
	sc.arenaOff += size
	return p
}

func (sc *Parser) scanValue(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	if ti.Kind == typ.KindPointer {
		return sc.scanPointer(src, idx, ti, ptr)
	}
	if ti.TypeFlags&DecTypeFlagSpecial != 0 {
		return sc.scanValueSpecial(src, idx, ti, ptr)
	}
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch sliceAt(src, idx) {
	case '"':
		if ti.Kind == typ.KindSlice {
			return sc.scanStringToSlice(src, idx, ti, ptr)
		}
		return sc.scanStringValue(src, idx, ti, ptr)
	case '{':
		switch ti.Kind {
		case typ.KindStruct:
			{
				dec := ti.ResolveStruct()
				base := ptr
				idx++
				idx = skipWSLong(src, idx)
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) == '}' {
					return idx + 1, nil
				}

				var firstErr error

				bm, hasBitmap := dec.Lookup.(*bitmapLookup8)

				for {
					if idx >= len(src) {
						return idx, errUnexpectedEOF
					}
					if sliceAt(src, idx) != '"' {
						return idx, newSyntaxError("vjson: syntax error", idx)
					}

					var fi *DecFieldInfo
					var err error

					if hasBitmap {
						// Bitmap lookup scans and matches the key in one pass.
						start := idx + 1
						pos := start
						n := len(src)
						cur := uint8(0xFF)
						bmMaxKeyLen := int(bm.maxKeyLen)
						bmBitmap := bm.bitmap
						bmLenMask := bm.lenMask
						charIdx := 0

						for pos < n {
							c := sliceAt(src, pos)
							if c == '"' {
								if cur != 0 && charIdx <= bmMaxKeyLen {
									cur &= sliceAt(bmLenMask, charIdx)
									if cur != 0 {
										fi = &dec.Fields[bits.TrailingZeros8(cur)]
									}
								}
								idx = pos + 1
								goto bmDone
							}
							if c == '\\' {
								// Escapes force the generic decoded-key lookup path.
								var keyBytes []byte
								idx, keyBytes, err = sc.unescapeSinglePass(src, start, pos)
								if err != nil {
									return idx, err
								}
								fi = dec.LookupFieldBytes(keyBytes)
								goto bmFieldResolved
							}
							if c < 0x20 {
								return pos, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", pos), pos)
							}
							if charIdx < bmMaxKeyLen && cur != 0 {
								cur &= sliceAt(bmBitmap, charIdx*256+int(c))
							} else {
								cur = 0
							}
							charIdx++
							pos++
						}
						return pos, errUnexpectedEOF
					bmDone:
						// Match encoding/json's ASCII-fold fallback rules.
						if fi == nil {
							keySlice := sliceRangeT(src, start, idx-1)
							if dec.HasMixedCase || hasUpperASCII(keySlice) {
								k := unsafe.String(unsafe.SliceData(keySlice), len(keySlice))
								fields := dec.Fields
								for i := range fields {
									if equalFoldASCII(fields[i].JSONName, k) {
										fi = &fields[i]
										break
									}
								}
							}
						}
					bmFieldResolved:
					} else {
						var keyBytes []byte

						{
							start := idx + 1
							n := len(src)

							pos := start
							base := unsafe.Pointer(unsafe.SliceData(src)) //nolint
							for pos+8 <= n {
								w := *(*uint64)(unsafe.Add(base, pos))
								mq := hasZeroByte(w ^ (lo64 * 0x22)) // '"'
								mb := hasZeroByte(w ^ (lo64 * 0x5C)) // '\\'
								mc := (w - lo64*0x20) & ^w & hi64    // < 0x20
								combined := mq | mb | mc
								if combined != 0 {
									off := firstMarkedByteIndex(combined)
									foundIdx := pos + off
									c := sliceAt(src, foundIdx)
									if c == '"' {
										idx, keyBytes, err = foundIdx+1, unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), start)), foundIdx-start), nil
										goto out
									}
									if c == '\\' {
										idx, keyBytes, err = sc.unescapeSinglePass(src, start, foundIdx)
										goto out
									}
									idx, keyBytes, err = foundIdx, nil, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", foundIdx), foundIdx)
									goto out
								}
								pos += 8
							}

							for pos < n {
								c := sliceAt(src, pos)
								if c == '"' {
									idx, keyBytes, err = pos+1, unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), start)), pos-start), nil
									goto out
								}
								if c == '\\' {
									idx, keyBytes, err = sc.unescapeSinglePass(src, start, pos)
									goto out
								}
								if c < 0x20 {
									idx, keyBytes, err = pos, nil, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", pos), pos)
									goto out
								}
								pos++
							}
							idx, keyBytes, err = n, nil, errUnexpectedEOF
						out:
						}
						if err != nil {
							return idx, err
						}

						fi = dec.LookupFieldBytes(keyBytes)
					}

					idx = skipWS(src, idx)
					if idx >= len(src) {
						return idx, errUnexpectedEOF
					}
					if sliceAt(src, idx) != ':' {
						return idx, newSyntaxError("vjson: syntax error", idx)
					}
					idx++
					idx = skipWS(src, idx)
					if fi == nil {
						idx, err = skipValue(src, idx)
						if err != nil {
							return idx, err
						}
					} else {
						savedIdx := idx
						fieldPtr := unsafe.Add(base, fi.Offset)
						fti := fi.TypeInfo

						if fi.TagFlags&DecTagFlagSpecial != 0 {
							idx, err = sc.scanFieldTagged(src, idx, fi, fti, fieldPtr)
						} else if fti.TypeFlags&DecTypeFlagSpecial == 0 && fi.Kind != typ.KindPointer {
							if idx >= len(src) {
								return idx, errUnexpectedEOF
							}
							b := sliceAt(src, idx)

							switch {
							case b == '"':
								if fi.Kind == typ.KindSlice {
									idx, err = sc.scanStringToSlice(src, idx, fti, fieldPtr)
								} else {
									idx, err = sc.scanStringValue(src, idx, fti, fieldPtr)
								}
							case (b >= '0' && b <= '9') || b == '-':
								switch fi.Kind {
								case typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
									typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
									typ.KindFloat32, typ.KindFloat64:
									idx, err = sc.scanNumber(src, idx, fti, fieldPtr)
								default:
									idx, err = sc.scanValue(src, idx, fti, fieldPtr)
								}
							default:
								idx, err = sc.scanValue(src, idx, fti, fieldPtr)
							}
						} else {
							idx, err = sc.scanValue(src, idx, fti, fieldPtr)
						}

						if err != nil {
							var ute *UnmarshalTypeError
							if !errors.As(err, &ute) {
								return idx, err // syntax error → abort
							}
							// Type mismatches skip the value and keep scanning.
							if idx == savedIdx {
								idx, err = skipValue(src, idx)
								if err != nil {
									return idx, err
								}
							}
							if firstErr == nil {
								firstErr = ute
							}
						}
					}

					if idx >= len(src) {
						return idx, errUnexpectedEOF
					}
					c := sliceAt(src, idx)
					if c == ',' {
						idx++
						if idx >= len(src) {
							return idx, errUnexpectedEOF
						}
						if sliceAt(src, idx) == '"' {
							continue
						}
						idx = skipWSLong(src, idx)
						if idx >= len(src) {
							return idx, errUnexpectedEOF
						}
						if sliceAt(src, idx) != '"' {
							return idx, newSyntaxError("vjson: syntax error", idx)
						}
						continue
					}
					if c == '}' {
						return idx + 1, firstErr
					}
					if wsLUT[c] != 0 {
						idx = skipWSLong(src, idx)
						if idx >= len(src) {
							return idx, errUnexpectedEOF
						}
						c = sliceAt(src, idx)
						if c == ',' {
							idx++
							if idx >= len(src) {
								return idx, errUnexpectedEOF
							}
							if sliceAt(src, idx) == '"' {
								continue
							}
							idx = skipWSLong(src, idx)
							if idx >= len(src) {
								return idx, errUnexpectedEOF
							}
							if sliceAt(src, idx) != '"' {
								return idx, newSyntaxError("vjson: syntax error", idx)
							}
							continue
						}
						if c == '}' {
							return idx + 1, firstErr
						}
					}
					return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or '}' in object, got %q", sliceAt(src, idx)), idx)
				}
			}
		case typ.KindMap:
			return sc.scanMap(src, idx, ti, ptr)
		case typ.KindAny:
			newIdx, m, err := sc.scanMapAny(src, idx)
			if err != nil {
				return newIdx, err
			}
			*(*any)(ptr) = m
			return newIdx, nil
		default:
			return idx, newUnmarshalTypeError("object", ti.Type, idx)
		}
	case '[':
		switch ti.Kind {
		case typ.KindSlice:
			return sc.scanSlice(src, idx, ti.ResolveSlice(), ptr)
		case typ.KindArray:
			return sc.scanArray(src, idx, ti.ResolveArray(), ptr)
		case typ.KindAny:
			newIdx, arr, err := sc.scanSliceAny(src, idx)
			if err != nil {
				return newIdx, err
			}
			*(*any)(ptr) = arr
			return newIdx, nil
		default:
			return idx, newUnmarshalTypeError("array", ti.Type, idx)
		}
	case 't':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), idx)) != litU32True {
			return idx, invalidLiteralError(idx)
		}
		switch ti.Kind {
		case typ.KindBool:
			*(*bool)(ptr) = true
		case typ.KindAny:
			*(*any)(ptr) = true
		default:
			return idx + 4, unmarshalBoolTypeError(ti, idx+4)
		}
		return idx + 4, nil
	case 'f':
		if idx+5 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), idx+1)) != litU32Alse {
			return idx, invalidLiteralError(idx)
		}
		switch ti.Kind {
		case typ.KindBool:
			*(*bool)(ptr) = false
		case typ.KindAny:
			*(*any)(ptr) = false
		default:
			return idx + 5, unmarshalBoolTypeError(ti, idx+5)
		}
		return idx + 5, nil
	case 'n':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), idx)) != litU32Null {
			return idx, invalidLiteralError(idx)
		}
		switch ti.Kind {
		case typ.KindPointer:
			*(*unsafe.Pointer)(ptr) = nil
		case typ.KindSlice:
			*(*SliceHeader)(ptr) = SliceHeader{}
		case typ.KindMap:
			nullifyMap(ti, ptr)
		case typ.KindAny:
			*(*any)(ptr) = nil
		default:
			// Primitive values and structs keep their existing value on null.
		}
		return idx + 4, nil
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-':
		return sc.scanNumber(src, idx, ti, ptr)
	default:
		return idx, newSyntaxError(fmt.Sprintf("vjson: unexpected character %q at offset %d", sliceAt(src, idx), idx), idx)
	}
}

func copyStringIfNeeded(raw []byte, copyStr bool) string {
	if len(raw) == 0 {
		return ""
	}
	if copyStr {
		return string(raw)
	}
	return unsafe.String(slicePtrT(raw), len(raw))
}

// scanStringValue decodes a JSON string into a typed destination.
// The no-escape path aliases src unless copyString is set.
func (sc *Parser) scanStringValue(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	start := idx + 1
	n := len(src)

	if start >= n {
		return start, errUnexpectedEOF
	}

	pos := start
	base := slicePtr(src)
	for pos+8 <= n {
		w := *(*uint64)(unsafe.Add(base, pos))

		mq := hasZeroByte(w ^ (lo64 * 0x22))
		mb := hasZeroByte(w ^ (lo64 * 0x5C))
		mc := (w - lo64*0x20) & ^w & hi64

		if (mq | mb | mc) == 0 {
			pos += 8
			continue
		}

		combined := mq | mb | mc
		off := firstMarkedByteIndex(combined)
		foundPos := pos + off
		foundChar := sliceAt(src, foundPos)

		if foundChar == '"' {
			raw := sliceRangeT(src, start, foundPos)
			needCopy := sc.copyString
			s := copyStringIfNeeded(raw, needCopy)
			switch ti.Kind {
			case typ.KindString:
				*(*string)(ptr) = s
			case typ.KindAny:
				*(*any)(ptr) = s
			default:
				return foundPos + 1, newUnmarshalTypeError("string", ti.Type, foundPos+1)
			}
			return foundPos + 1, nil
		}

		if foundChar == '\\' {
			return sc.processEscapedString(src, start, foundPos, ti, ptr)
		}

		return foundPos, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", foundPos), foundPos)
	}

	for pos < n {
		c := sliceAt(src, pos)
		if c == '"' {
			raw := sliceRangeT(src, start, pos)
			needCopy := sc.copyString
			s := copyStringIfNeeded(raw, needCopy)
			switch ti.Kind {
			case typ.KindString:
				*(*string)(ptr) = s
			case typ.KindAny:
				*(*any)(ptr) = s
			default:
				return pos + 1, newUnmarshalTypeError("string", ti.Type, pos+1)
			}
			return pos + 1, nil
		}
		if c == '\\' {
			return sc.processEscapedString(src, start, pos, ti, ptr)
		}
		if c < 0x20 {
			return pos, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", pos), pos)
		}
		pos++
	}

	return n, errUnexpectedEOF
}

// scanStringKey returns decoded object-key bytes.
// The no-escape path aliases src.
func (sc *Parser) scanStringKey(src []byte, idx int) (int, []byte, error) {
	start := idx + 1
	n := len(src)

	pos := start
	base := slicePtr(src)
	for pos+8 <= n {
		w := *(*uint64)(unsafe.Add(base, pos))
		mq := hasZeroByte(w ^ (lo64 * 0x22)) // '"'
		mb := hasZeroByte(w ^ (lo64 * 0x5C)) // '\\'
		mc := (w - lo64*0x20) & ^w & hi64    // < 0x20
		combined := mq | mb | mc
		if combined != 0 {
			off := firstMarkedByteIndex(combined)
			foundIdx := pos + off
			c := sliceAt(src, foundIdx)
			if c == '"' {
				return foundIdx + 1, sliceRangeT(src, start, foundIdx), nil
			}
			if c == '\\' {
				return sc.unescapeSinglePass(src, start, foundIdx)
			}
			return foundIdx, nil, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", foundIdx), foundIdx)
		}
		pos += 8
	}

	for pos < n {
		c := sliceAt(src, pos)
		if c == '"' {
			return pos + 1, sliceRangeT(src, start, pos), nil
		}
		if c == '\\' {
			return sc.unescapeSinglePass(src, start, pos)
		}
		if c < 0x20 {
			return pos, nil, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", pos), pos)
		}
		pos++
	}
	return n, nil, errUnexpectedEOF
}

// scanString returns a decoded string.
// It copies only when sc.copyString is set.
func (sc *Parser) scanString(src []byte, idx int) (int, string, error) {
	start := idx + 1
	n := len(src)
	needCopy := sc.copyString

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
			c := sliceAt(src, foundIdx)
			if c == '"' {
				return foundIdx + 1, copyStringIfNeeded(sliceRangeT(src, start, foundIdx), needCopy), nil
			}
			if c == '\\' {
				endIdx, result, err := sc.unescapeSinglePass(src, start, foundIdx)
				if err != nil {
					return endIdx, "", err
				}
				if needCopy {
					return endIdx, string(result), nil
				}
				return endIdx, unsafe.String(unsafe.SliceData(result), len(result)), nil
			}
			return foundIdx, "", newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", foundIdx), foundIdx)
		}
		pos += 8
	}

	for pos < n {
		c := sliceAt(src, pos)
		if c == '"' {
			return pos + 1, copyStringIfNeeded(sliceRangeT(src, start, pos), needCopy), nil
		}
		if c == '\\' {
			endIdx, result, err := sc.unescapeSinglePass(src, start, pos)
			if err != nil {
				return endIdx, "", err
			}
			if needCopy {
				return endIdx, string(result), nil
			}
			return endIdx, unsafe.String(unsafe.SliceData(result), len(result)), nil
		}
		if c < 0x20 {
			return pos, "", newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", pos), pos)
		}
		pos++
	}
	return n, "", errUnexpectedEOF
}

// scanNumberSpan finds the end of a JSON number starting at idx.
// Returns (endIdx, isFloat, error).
func scanNumberSpan(src []byte, idx int) (int, bool, error) {
	i := idx
	n := len(src)

	if i < n && sliceAt(src, i) == '-' {
		i++
	}

	if i >= n || sliceAt(src, i) < '0' || sliceAt(src, i) > '9' {
		return i, false, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
	}
	if sliceAt(src, i) == '0' {
		i++
		if i < n && sliceAt(src, i) >= '0' && sliceAt(src, i) <= '9' {
			return i, false, newSyntaxError(fmt.Sprintf("vjson: leading zeros in number at offset %d", idx), idx)
		}
	} else {
		i++
		for i < n && sliceAt(src, i) >= '0' && sliceAt(src, i) <= '9' {
			i++
		}
	}

	isFloat := false

	if i < n && sliceAt(src, i) == '.' {
		isFloat = true
		i++
		if i >= n || sliceAt(src, i) < '0' || sliceAt(src, i) > '9' {
			return i, true, newSyntaxError(fmt.Sprintf("vjson: invalid fraction in number at offset %d", idx), idx)
		}
		i++
		for i < n && sliceAt(src, i) >= '0' && sliceAt(src, i) <= '9' {
			i++
		}
	}

	if i < n && (sliceAt(src, i) == 'e' || sliceAt(src, i) == 'E') {
		isFloat = true
		i++
		if i < n && (sliceAt(src, i) == '+' || sliceAt(src, i) == '-') {
			i++
		}
		if i >= n || sliceAt(src, i) < '0' || sliceAt(src, i) > '9' {
			return i, true, newSyntaxError(fmt.Sprintf("vjson: invalid exponent in number at offset %d", idx), idx)
		}
		i++
		for i < n && sliceAt(src, i) >= '0' && sliceAt(src, i) <= '9' {
			i++
		}
	}

	return i, isFloat, nil
}

func (sc *Parser) scanNumber(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	switch ti.Kind {

	case typ.KindFloat64:
		end, v, scanErr := scanFloat64(src, idx)
		if scanErr != nil {
			return end, scanErr
		}
		*(*float64)(ptr) = v
		return end, nil

	case typ.KindFloat32:
		end, _, numErr := scanNumberSpan(src, idx)
		if numErr != nil {
			return end, numErr
		}
		v, err := strconv.ParseFloat(unsafeString(sliceRangeT(src, idx, end)), 32)
		if err != nil {
			return end, newSyntaxErrorWrap(fmt.Sprintf("vjson: invalid float %q: %v", src[idx:end], err), end, err)
		}
		*(*float32)(ptr) = float32(v)
		return end, nil

	case typ.KindInt, typ.KindInt64:
		end, v, isFloat, ok := scanInt64(src, idx)
		if isFloat {
			numEnd, _, numErr := scanNumberSpan(src, idx)
			if numErr != nil {
				return numEnd, numErr
			}
			return numEnd, newUnmarshalTypeError("number", ti.Type, numEnd)
		}
		if !ok {
			if end == idx || (end == idx+1 && sliceAt(src, idx) == '-') {
				return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
			}
			return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
		}
		// typ.KindInt and typ.KindInt64 always fit — no overflow check needed.
		if ti.Kind == typ.KindInt {
			*(*int)(ptr) = int(v)
		} else {
			*(*int64)(ptr) = v
		}
		return end, nil

	case typ.KindInt8, typ.KindInt16, typ.KindInt32:
		end, v, isFloat, ok := scanInt64(src, idx)
		if isFloat {
			numEnd, _, numErr := scanNumberSpan(src, idx)
			if numErr != nil {
				return numEnd, numErr
			}
			return numEnd, newUnmarshalTypeError("number", ti.Type, numEnd)
		}
		if !ok {
			if end == idx || (end == idx+1 && sliceAt(src, idx) == '-') {
				return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
			}
			return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
		}
		switch ti.Kind {
		case typ.KindInt8:
			if v < math.MinInt8 || v > math.MaxInt8 {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
			}
			*(*int8)(ptr) = int8(v)
		case typ.KindInt16:
			if v < math.MinInt16 || v > math.MaxInt16 {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
			}
			*(*int16)(ptr) = int16(v)
		default: // typ.KindInt32
			if v < math.MinInt32 || v > math.MaxInt32 {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
			}
			*(*int32)(ptr) = int32(v)
		}
		return end, nil

	case typ.KindUint, typ.KindUint64:
		end, v, isFloat, ok := scanUint64(src, idx)
		if isFloat {
			numEnd, _, numErr := scanNumberSpan(src, idx)
			if numErr != nil {
				return numEnd, numErr
			}
			return numEnd, newUnmarshalTypeError("number", ti.Type, numEnd)
		}
		if !ok {
			if end == idx {
				return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
			}
			return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
		}
		// typ.KindUint and typ.KindUint64 always fit — no overflow check needed.
		if ti.Kind == typ.KindUint {
			*(*uint)(ptr) = uint(v)
		} else {
			*(*uint64)(ptr) = v
		}
		return end, nil

	case typ.KindUint8, typ.KindUint16, typ.KindUint32:
		end, v, isFloat, ok := scanUint64(src, idx)
		if isFloat {
			numEnd, _, numErr := scanNumberSpan(src, idx)
			if numErr != nil {
				return numEnd, numErr
			}
			return numEnd, newUnmarshalTypeError("number", ti.Type, numEnd)
		}
		if !ok {
			if end == idx {
				return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
			}
			return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
		}
		switch ti.Kind {
		case typ.KindUint8:
			if v > math.MaxUint8 {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
			}
			*(*uint8)(ptr) = uint8(v)
		case typ.KindUint16:
			if v > math.MaxUint16 {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
			}
			*(*uint16)(ptr) = uint16(v)
		default: // typ.KindUint32
			if v > math.MaxUint32 {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), ti.Type, end)
			}
			*(*uint32)(ptr) = uint32(v)
		}
		return end, nil

	case typ.KindAny:
		// UseNumber: preserve raw text as json.Number.
		if sc.useNumber {
			end, _, numErr := scanNumberSpan(src, idx)
			if numErr != nil {
				return end, numErr
			}
			*(*any)(ptr) = json.Number(string(src[idx:end]))
			return end, nil
		}
		// Default: all numbers → float64 for interface{}
		end, v, err := scanFloat64(src, idx)
		if err != nil {
			return end, err
		}
		*(*any)(ptr) = v
		return end, nil

	default:
		return idx, newUnmarshalTypeError("number", ti.Type, idx)
	}
}

// scanNumberAny parses a number for interface{} context.
// When useNumber is set, returns json.Number; otherwise returns float64.
// Uses interned floats for small integers (0-255) to avoid allocation.
func (sc *Parser) scanNumberAny(src []byte, idx int) (int, any, error) {
	end, _, numErr := scanNumberSpan(src, idx)
	if numErr != nil {
		return end, nil, numErr
	}
	span := sliceRangeT(src, idx, end)

	// json.Number path: preserve the raw text, no float conversion.
	if sc.useNumber {
		return end, json.Number(string(span)), nil
	}

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
			return end, internedFloats[val], nil
		}
	}

	_, v, err := scanFloat64(src, idx)
	if err != nil {
		return end, nil, err
	}
	return end, v, nil
}

func (sc *Parser) scanMap(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	mDec := ti.ResolveMap()
	// Fast path for map[string]V with known V — zero reflection
	if mDec.ScanMapFn != nil {
		return mDec.ScanMapFn(sc, src, idx, ptr)
	}

	mapRType := ti.TypePtr

	idx++
	idx = skipWSLong(src, idx)

	mp := *(*unsafe.Pointer)(ptr)
	if mp == nil {
		mp = makemap(mapRType, 0, nil)
		// Use typedmemmove to store the new map pointer with a GC write barrier.
		typedmemmove(mapRType, ptr, unsafe.Pointer(&mp))
	}

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == '}' {
		return idx + 1, nil
	}

	for {
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != '"' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		var key string
		var err error
		idx, key, err = sc.scanString(src, idx)
		if err != nil {
			return idx, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) != ':' {
			return idx, newSyntaxError("vjson: syntax error", idx)
		}
		idx++
		idx = skipWS(src, idx)

		if mDec.IsStringKey {
			if mDec.ValHasPtr {
				// Value contains GC-traced pointers: write to a temp alloc, then
				// typedmemmove into the map slot to ensure write barriers fire.
				valBuf := unsafe_New(mDec.ValRType)
				idx, err = sc.scanValue(src, idx, mDec.ValTI, valBuf)
				if err != nil {
					return idx, err
				}
				valSlot := mapassign_faststr(mapRType, mp, key)
				typedmemmove(mDec.ValRType, valSlot, valBuf)
			} else {
				// Scalar value (no pointers): safe to write directly into the slot.
				valSlot := mapassign_faststr(mapRType, mp, key)
				idx, err = sc.scanValue(src, idx, mDec.ValTI, valSlot)
			}
		} else {
			keyBuf := unsafe_New(mDec.KeyRType)
			if err = resolveMapKey(key, mDec.KeyType, mDec.KeyTI, keyBuf); err != nil {
				return idx, err
			}
			if mDec.ValHasPtr {
				valBuf := unsafe_New(mDec.ValRType)
				idx, err = sc.scanValue(src, idx, mDec.ValTI, valBuf)
				if err != nil {
					return idx, err
				}
				valSlot := mapassign(mapRType, mp, keyBuf)
				typedmemmove(mDec.ValRType, valSlot, valBuf)
			} else {
				valSlot := mapassign(mapRType, mp, keyBuf)
				idx, err = sc.scanValue(src, idx, mDec.ValTI, valSlot)
			}
		}
		if err != nil {
			return idx, err
		}

		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		c := sliceAt(src, idx)
		if c == ',' {
			idx++
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) == '"' {
				continue
			}
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			if sliceAt(src, idx) != '"' {
				return idx, newSyntaxError("vjson: syntax error", idx)
			}
			continue
		}
		if c == '}' {
			return idx + 1, nil
		}
		if wsLUT[c] != 0 {
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, errUnexpectedEOF
			}
			c = sliceAt(src, idx)
			if c == ',' {
				idx++
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) == '"' {
					continue
				}
				idx = skipWSLong(src, idx)
				if idx >= len(src) {
					return idx, errUnexpectedEOF
				}
				if sliceAt(src, idx) != '"' {
					return idx, newSyntaxError("vjson: syntax error", idx)
				}
				continue
			}
			if c == '}' {
				return idx + 1, nil
			}
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or '}' in map, got %q", src[idx]), idx)
	}
}

func (sc *Parser) scanMapAny(src []byte, idx int) (int, map[string]any, error) {
	idx++
	idx = skipWSLong(src, idx)

	m := make(map[string]any)

	if idx >= len(src) {
		return idx, nil, errUnexpectedEOF
	}
	if sliceAt(src, idx) == '}' {
		return idx + 1, m, nil
	}

	for {
		if idx >= len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if sliceAt(src, idx) != '"' {
			return idx, nil, newSyntaxError("vjson: syntax error", idx)
		}
		var key string
		var err error
		idx, key, err = sc.scanString(src, idx)
		if err != nil {
			return idx, nil, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if sliceAt(src, idx) != ':' {
			return idx, nil, newSyntaxError("vjson: syntax error", idx)
		}
		idx++
		idx = skipWS(src, idx)

		var val any
		idx, val, err = sc.scanValueAny(src, idx)
		if err != nil {
			return idx, nil, err
		}
		m[key] = val

		if idx >= len(src) {
			return idx, nil, errUnexpectedEOF
		}
		c := sliceAt(src, idx)
		if c == ',' {
			idx++
			if idx >= len(src) {
				return idx, nil, errUnexpectedEOF
			}
			if sliceAt(src, idx) == '"' {
				continue
			}
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, nil, errUnexpectedEOF
			}
			if sliceAt(src, idx) != '"' {
				return idx, nil, newSyntaxError("vjson: syntax error", idx)
			}
			continue
		}
		if c == '}' {
			return idx + 1, m, nil
		}
		if wsLUT[c] != 0 {
			idx = skipWSLong(src, idx)
			if idx >= len(src) {
				return idx, nil, errUnexpectedEOF
			}
			c = sliceAt(src, idx)
			if c == ',' {
				idx++
				if idx >= len(src) {
					return idx, nil, errUnexpectedEOF
				}
				if sliceAt(src, idx) == '"' {
					continue
				}
				idx = skipWSLong(src, idx)
				if idx >= len(src) {
					return idx, nil, errUnexpectedEOF
				}
				if sliceAt(src, idx) != '"' {
					return idx, nil, newSyntaxError("vjson: syntax error", idx)
				}
				continue
			}
			if c == '}' {
				return idx + 1, m, nil
			}
		}
		return idx, nil, newSyntaxError(fmt.Sprintf("vjson: expected ',' or '}' in any object, got %q", src[idx]), idx)
	}
}

// zeroArrayElements zeroes array elements from index 'from' to 'to' (exclusive).
func zeroArrayElements(base unsafe.Pointer, elemSize uintptr, from, to int) {
	start := unsafe.Add(base, uintptr(from)*elemSize)
	n := uintptr(to-from) * elemSize
	b := unsafe.Slice((*byte)(start), n)
	clear(b)
}

func (sc *Parser) scanSlice(src []byte, idx int, sDec *DecSliceInfo, ptr unsafe.Pointer) (int, error) {
	idx++
	idx = skipWSLong(src, idx)

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == ']' {
		sh := (*SliceHeader)(ptr)
		sh.Data = sDec.EmptySliceData
		sh.Len = 0
		sh.Cap = 0
		return idx + 1, nil
	}

	// Determine initial slice capacity using adaptive EMA of previously
	// observed array lengths.
	sliceCap := max(int(sDec.CapHint.Load()), 2)
	elemSize := sDec.ElemSize
	sliceLen := 0

	// Pointer-free elements (int, float64, etc.): allocate via make([]byte) which
	// produces a noscan (GC-invisible) block — faster than runtime.mallocgc with
	// type metadata. Pointer-containing elements (string, *T, etc.): allocate via
	// unsafe_NewArray with the correct rtype so GC can trace interior pointers.
	var base unsafe.Pointer
	var backingBytes []byte // kept alive for pointer-free path

	if sDec.ElemHasPtr {
		base = unsafe_NewArray(sDec.ElemRType, sliceCap)
	} else {
		backingBytes = make([]byte, sliceCap*int(elemSize))
		base = slicePtr(backingBytes)
	}

	for {
		// Grow if needed
		if sliceLen == sliceCap {
			newCap := sliceCap * 2
			if sDec.ElemHasPtr {
				newBase := unsafe_NewArray(sDec.ElemRType, newCap)
				typedslicecopy(sDec.ElemRType, newBase, newCap, base, sliceLen)
				base = newBase
			} else {
				newBacking := make([]byte, newCap*int(elemSize))
				copy(newBacking, backingBytes)
				backingBytes = newBacking
				base = slicePtr(backingBytes)
			}
			sliceCap = newCap
		}

		elemPtr := unsafe.Add(base, uintptr(sliceLen)*elemSize)
		sliceLen++

		var err error
		idx, err = sc.scanValue(src, idx, sDec.ElemTI, elemPtr)
		if err != nil {
			return idx, err
		}

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if sliceAt(src, idx) == ']' {
			// Update adaptive capacity hint using EMA.
			// Relaxed store is fine — a stale read just means one sub-optimal alloc.
			old := int(sDec.CapHint.Load())
			if old == 0 {
				sDec.CapHint.Store(int32(sliceLen))
			} else {
				alpha := int(sDec.EmaAlpha)
				sDec.CapHint.Store(int32((old*(alpha-1) + sliceLen) / alpha))
			}
			sh := (*SliceHeader)(ptr)
			sh.Data = base
			sh.Len = sliceLen
			sh.Cap = sliceCap
			return idx + 1, nil
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or ']' in array, got %q", src[idx]), idx)
	}
}

func (sc *Parser) scanSliceAny(src []byte, idx int) (int, []any, error) {
	idx++
	idx = skipWSLong(src, idx)

	if idx >= len(src) {
		return idx, nil, errUnexpectedEOF
	}
	if sliceAt(src, idx) == ']' {
		return idx + 1, []any{}, nil
	}

	arrayCap := 2
	arr := make([]any, 0, arrayCap)
	for {
		var val any
		var err error
		idx, val, err = sc.scanValueAny(src, idx)
		if err != nil {
			return idx, nil, err
		}
		arr = append(arr, val)

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if sliceAt(src, idx) == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if sliceAt(src, idx) == ']' {
			return idx + 1, arr, nil
		}
		return idx, nil, newSyntaxError(fmt.Sprintf("vjson: expected ',' or ']' in any array, got %q", src[idx]), idx)
	}
}

// scanArrayFixed decodes a JSON array into a Go fixed-size array [N]T.
func (sc *Parser) scanArray(src []byte, idx int, aDec *DecArrayInfo, ptr unsafe.Pointer) (int, error) {
	if fn := aDec.ScanArrayFn; fn != nil {
		return fn(src, idx, aDec.ArrayLen, aDec.ElemSize, ptr)
	}

	idx++
	idx = skipWSLong(src, idx)

	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == ']' {
		zeroArrayElements(ptr, aDec.ElemSize, 0, aDec.ArrayLen)
		return idx + 1, nil
	}

	elemSize := aDec.ElemSize
	arrayLen := aDec.ArrayLen
	count := 0

	for {
		if count < arrayLen {
			elemPtr := unsafe.Add(ptr, uintptr(count)*elemSize)
			var err error
			idx, err = sc.scanValue(src, idx, aDec.ElemTI, elemPtr)
			if err != nil {
				return idx, err
			}
		} else {
			var err error
			idx, err = skipValue(src, idx)
			if err != nil {
				return idx, err
			}
		}
		count++

		idx = skipWS(src, idx)
		if idx >= len(src) {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == ',' {
			idx++
			idx = skipWSLong(src, idx)
			continue
		}
		if sliceAt(src, idx) == ']' {
			if count < arrayLen {
				zeroArrayElements(ptr, elemSize, count, arrayLen)
			}
			return idx + 1, nil
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or ']' in array, got %q", src[idx]), idx)
	}
}

func (sc *Parser) scanPointer(src []byte, idx int, ti *DecTypeInfo, ptr unsafe.Pointer) (int, error) {
	pDec := ti.ResolvePointer()

	idx = skipWS(src, idx)
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}

	// null → set pointer to nil.
	if sliceAt(src, idx) == 'n' {
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
			return idx, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		*(*unsafe.Pointer)(ptr) = nil
		return idx + 4, nil
	}

	// Reuse existing allocation if non-nil (matches encoding/json behavior).
	// Otherwise allocate: pointer-containing types use unsafe_New for
	// GC-correct type metadata; pointer-free types use make([]byte).
	elemPtr := *(*unsafe.Pointer)(ptr)
	if elemPtr == nil {
		if pDec.ElemHasPtr {
			elemPtr = sc.ptrAlloc(pDec.ElemRType, pDec.ElemSize)
		} else {
			backing := make([]byte, pDec.ElemSize)
			elemPtr = slicePtr(backing)
		}
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
		a = &TypeAllocator{
			rtype:    rtype,
			elemSize: elemSize,
			cap:      PtrBatchSize,
			offset:   PtrBatchSize, // forces alloc on first use
		}
		sc.ptrAllocs[rtype] = a
	}
	return a.Alloc()
}

func (sc *Parser) scanValueAny(src []byte, idx int) (int, any, error) {
	if idx >= len(src) {
		return idx, nil, errUnexpectedEOF
	}
	switch sliceAt(src, idx) {
	case '"':
		newIdx, s, err := sc.scanString(src, idx)
		return newIdx, s, err
	case '{':
		newIdx, m, err := sc.scanMapAny(src, idx)
		return newIdx, m, err
	case '[':
		newIdx, arr, err := sc.scanSliceAny(src, idx)
		return newIdx, arr, err
	case 't':
		if idx+4 > len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32True {
			return idx, nil, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		return idx + 4, true, nil
	case 'f':
		if idx+5 > len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx+1])) != litU32Alse { // "else" suffix; 'f' already matched by caller
			return idx, nil, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		return idx + 5, false, nil
	case 'n':
		if idx+4 > len(src) {
			return idx, nil, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
			return idx, nil, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		return idx + 4, nil, nil
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-':
		return sc.scanNumberAny(src, idx)
	default:
		return idx, nil, newSyntaxError(fmt.Sprintf("vjson: unexpected character %q in any value", src[idx]), idx)
	}
}

// skipValue skips a complete JSON value starting at idx.
// Uses depth counting for objects/arrays instead of recursion.
func skipValue(src []byte, idx int) (int, error) {
	if idx >= len(src) {
		return idx, errUnexpectedEOF
	}
	switch sliceAt(src, idx) {
	case '"':
		return skipString(src, idx)
	case 't':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32True {
			return idx, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		return idx + 4, nil
	case 'f':
		if idx+5 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx+1])) != litU32Alse { // "else" suffix; 'f' already matched by caller
			return idx, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		return idx + 5, nil
	case 'n':
		if idx+4 > len(src) {
			return idx, errUnexpectedEOF
		}
		if *(*uint32)(unsafe.Pointer(&src[idx])) != litU32Null {
			return idx, newSyntaxError(fmt.Sprintf("vjson: invalid literal at offset %d", idx), idx)
		}
		return idx + 4, nil
	case '{', '[':
		return skipContainer(src, idx)
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-':
		end, _, numErr := scanNumberSpan(src, idx)
		if numErr != nil {
			return end, numErr
		}
		return end, nil
	default:
		return idx, newSyntaxError(fmt.Sprintf("vjson: unexpected character %q", src[idx]), idx)
	}
}

// skipStringEscape validates and skips a JSON escape sequence at escIdx (src[escIdx] == '\\').
// Returns the next index after the escape.
func skipStringEscape(src []byte, escIdx, n int) (int, error) {
	if escIdx+1 >= n {
		return escIdx, errUnexpectedEOF
	}

	next := sliceAt(src, escIdx+1)
	if next == 'u' {
		// \uXXXX — exactly 4 hex digits.
		if escIdx+5 >= n {
			return escIdx, errUnexpectedEOF
		}
		if !isHexChar(sliceAt(src, escIdx+2)) || !isHexChar(sliceAt(src, escIdx+3)) || !isHexChar(sliceAt(src, escIdx+4)) || !isHexChar(sliceAt(src, escIdx+5)) {
			return escIdx, newSyntaxError(fmt.Sprintf("vjson: invalid unicode escape in string at offset %d", escIdx), escIdx)
		}
		return escIdx + 6, nil
	}

	if escapeTable[next] == 0 {
		return escIdx, newSyntaxError(fmt.Sprintf("vjson: invalid escape '\\%c' in string at offset %d", next, escIdx), escIdx)
	}
	return escIdx + 2, nil
}

// skipString skips a JSON string starting at idx (the opening '"').
func skipString(src []byte, idx int) (int, error) {
	i := idx + 1
	n := len(src)
	limit := n - 8

	// SWAR scan 8 bytes at a time for '"', '\\', or control chars (< 0x20).
	base := slicePtr(src)
	for i <= limit {
		w := *(*uint64)(unsafe.Add(base, i))
		mq := hasZeroByte(w ^ (lo64 * 0x22)) // '"'
		mb := hasZeroByte(w ^ (lo64 * 0x5C)) // '\\'
		mc := (w - lo64*0x20) & ^w & hi64    // < 0x20
		combined := mq | mb | mc
		if combined == 0 {
			i += 8
			continue
		}

		off := firstMarkedByteIndex(combined)
		pos := i + off
		c := sliceAt(src, pos)
		if c == '"' {
			return pos + 1, nil
		}
		if c == '\\' {
			next, err := skipStringEscape(src, pos, n)
			if err != nil {
				return pos, err
			}
			i = next
			continue
		}
		// control character
		return pos, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", pos), pos)
	}

	// Tail: byte-at-a-time
	for i < n {
		c := sliceAt(src, i)
		if c == '"' {
			return i + 1, nil
		}
		if c == '\\' {
			next, err := skipStringEscape(src, i, n)
			if err != nil {
				return i, err
			}
			i = next
			continue
		}
		if c < 0x20 {
			return i, newSyntaxError(fmt.Sprintf("vjson: control character in string at offset %d", i), i)
		}
		i++
	}
	return i, errUnexpectedEOF
}

// skipContainer skips a JSON object or array using depth counting.
// Optimized with multi-match SWAR processing and inline string skipping.
func skipContainer(src []byte, idx int) (int, error) {
	depth := 1
	i := idx + 1
	n := len(src)

outer:
	for i < n && depth > 0 {
		// Fast path: SWAR scan 8 bytes at a time for { } [ ] "
		if i+8 <= n {
			base := slicePtr(src)
			w := *(*uint64)(unsafe.Add(base, i))

			// Fold bit 0x20 so '{' and '[' share 0x5B, '}' and ']' share 0x5D.
			wNoCase := w & ^(lo64 * 0x20)
			m := hasZeroByte(wNoCase ^ (lo64 * 0x5B)) // '{' or '['
			m |= hasZeroByte(wNoCase ^ (lo64 * 0x5D)) // '}' or ']'
			m |= hasZeroByte(w ^ (lo64 * 0x22))       // '"'

			if m == 0 {
				i += 8
				continue
			}

			// Process ALL structural chars in this 8-byte word.
			for m != 0 {
				off := firstMarkedByteIndex(m)
				c := sliceAt(src, i+off)

				switch c {
				case '{', '[':
					depth++
				case '}', ']':
					depth--
					if depth == 0 {
						return i + off + 1, nil
					}
				case '"':
					// Inline string skip: find closing quote, handling \" escapes.
					j := i + off + 1
					for {
						if j+8 <= n {
							sw := *(*uint64)(unsafe.Add(base, j))
							sq := hasZeroByte(sw ^ (lo64 * 0x22)) // '"'
							sb := hasZeroByte(sw ^ (lo64 * 0x5C)) // '\\'
							sc := sq | sb
							if sc == 0 {
								j += 8
								continue
							}
							soff := firstMarkedByteIndex(sc)
							if sliceAt(src, j+soff) == '"' {
								j += soff + 1
								break
							}
							// Backslash: skip the escape sequence
							j += soff + 2
							continue
						}
						// Tail: byte-at-a-time
						if j >= n {
							return j, errUnexpectedEOF
						}
						if sliceAt(src, j) == '"' {
							j++
							break
						}
						if sliceAt(src, j) == '\\' {
							j += 2
							continue
						}
						j++
					}
					i = j
					continue outer
				}

				// Clear this byte's marker.
				m &^= 0x80 << (off * 8)
			}
			i += 8
			continue
		}

		// Slow path: byte-by-byte for remaining < 8 bytes
		c := sliceAt(src, i)
		switch c {
		case '{', '[':
			depth++
			i++
		case '}', ']':
			depth--
			i++
		case '"':
			// Inline string skip for tail bytes
			i++
			for i < n {
				if sliceAt(src, i) == '"' {
					i++
					continue outer
				}
				if sliceAt(src, i) == '\\' {
					i += 2
					continue
				}
				i++
			}
			return i, errUnexpectedEOF
		default:
			i++
		}
	}

	if depth > 0 {
		return i, errUnexpectedEOF
	}
	return i, nil
}
