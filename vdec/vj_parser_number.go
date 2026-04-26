package vdec

import (
	"fmt"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/typ"
)

func scanArrayInt(src []byte, idx int, arrayLen int, elemSize uintptr, elemKind typ.ElemTypeKind, elemType reflect.Type, ptr unsafe.Pointer) (int, error) {
	n := len(src)
	idx++

	if idx < n && sliceAt(src, idx) <= ' ' {
		idx = skipWSLong(src, idx)
	}

	if idx >= n {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == ']' {
		zeroArrayElements(ptr, elemSize, 0, arrayLen)
		return idx + 1, nil
	}

	count := 0
	for {
		if count < arrayLen {
			elemPtr := unsafe.Add(ptr, uintptr(count)*elemSize)
			end, v, isFloat, ok := scanInt64(src, idx)
			if isFloat {
				numEnd, _, numErr := scanNumberSpan(src, idx)
				if numErr != nil {
					return numEnd, numErr
				}
				return numEnd, newUnmarshalTypeError("number", elemType, numEnd)
			}
			if !ok {
				if end == idx || (end == idx+1 && sliceAt(src, idx) == '-') {
					return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
				}
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), elemType, end)
			}
			if !intFitsKind(v, elemKind) {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), elemType, end)
			}
			writeIntValue(elemPtr, elemKind, v)
			idx = end
		} else {
			var err error
			idx, err = skipValue(src, idx)
			if err != nil {
				return idx, err
			}
		}
		count++

		if idx < n && sliceAt(src, idx) <= ' ' {
			idx = skipWS(src, idx)
		}
		if idx >= n {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == ',' {
			idx++
			if idx < n && sliceAt(src, idx) <= ' ' {
				idx = skipWSLong(src, idx)
			}
			continue
		}
		if sliceAt(src, idx) == ']' {
			if count < arrayLen {
				zeroArrayElements(ptr, elemSize, count, arrayLen)
			}
			return idx + 1, nil
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or ']' in array, got %q", sliceAt(src, idx)), idx)
	}
}

func scanArrayUint(src []byte, idx int, arrayLen int, elemSize uintptr, elemKind typ.ElemTypeKind, elemType reflect.Type, ptr unsafe.Pointer) (int, error) {
	n := len(src)
	idx++

	if idx < n && sliceAt(src, idx) <= ' ' {
		idx = skipWSLong(src, idx)
	}

	if idx >= n {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == ']' {
		zeroArrayElements(ptr, elemSize, 0, arrayLen)
		return idx + 1, nil
	}

	count := 0
	for {
		if count < arrayLen {
			elemPtr := unsafe.Add(ptr, uintptr(count)*elemSize)
			end, v, isFloat, ok := scanUint64(src, idx)
			if isFloat {
				numEnd, _, numErr := scanNumberSpan(src, idx)
				if numErr != nil {
					return numEnd, numErr
				}
				return numEnd, newUnmarshalTypeError("number", elemType, numEnd)
			}
			if !ok {
				if end == idx {
					return end, newSyntaxError(fmt.Sprintf("vjson: invalid number at offset %d", idx), idx)
				}
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), elemType, end)
			}
			if !uintFitsKind(v, elemKind) {
				return end, newUnmarshalTypeError("number "+string(src[idx:end]), elemType, end)
			}
			writeUintValue(elemPtr, elemKind, v)
			idx = end
		} else {
			var err error
			idx, err = skipValue(src, idx)
			if err != nil {
				return idx, err
			}
		}
		count++

		if idx < n && sliceAt(src, idx) <= ' ' {
			idx = skipWS(src, idx)
		}
		if idx >= n {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == ',' {
			idx++
			if idx < n && sliceAt(src, idx) <= ' ' {
				idx = skipWSLong(src, idx)
			}
			continue
		}
		if sliceAt(src, idx) == ']' {
			if count < arrayLen {
				zeroArrayElements(ptr, elemSize, count, arrayLen)
			}
			return idx + 1, nil
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or ']' in array, got %q", sliceAt(src, idx)), idx)
	}
}

func scanArrayFloat64(src []byte, idx int, arrayLen int, elemSize uintptr, ptr unsafe.Pointer) (int, error) {
	n := len(src)
	idx++

	if idx < n && sliceAt(src, idx) <= ' ' {
		idx = skipWSLong(src, idx)
	}

	if idx >= n {
		return idx, errUnexpectedEOF
	}
	if sliceAt(src, idx) == ']' {
		zeroArrayElements(ptr, elemSize, 0, arrayLen)
		return idx + 1, nil
	}

	count := 0
	for {
		if count < arrayLen {
			elemPtr := unsafe.Add(ptr, uintptr(count)*elemSize)
			end, v, scanErr := scanFloat64(src, idx)
			if scanErr != nil {
				return end, scanErr
			}
			*(*float64)(elemPtr) = v
			idx = end
		} else {
			var err error
			idx, err = skipValue(src, idx)
			if err != nil {
				return idx, err
			}
		}
		count++

		if idx < n && sliceAt(src, idx) <= ' ' {
			idx = skipWS(src, idx)
		}
		if idx >= n {
			return idx, errUnexpectedEOF
		}
		if sliceAt(src, idx) == ',' {
			idx++
			if idx < n && sliceAt(src, idx) <= ' ' {
				idx = skipWSLong(src, idx)
			}
			continue
		}
		if sliceAt(src, idx) == ']' {
			if count < arrayLen {
				zeroArrayElements(ptr, elemSize, count, arrayLen)
			}
			return idx + 1, nil
		}
		return idx, newSyntaxError(fmt.Sprintf("vjson: expected ',' or ']' in array, got %q", sliceAt(src, idx)), idx)
	}
}
