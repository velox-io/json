package vdec

import (
	"unsafe"
)

// wsLUT is a lookup table for JSON whitespace characters.
// Indexed by byte value; non-zero means whitespace.
var wsLUT [256]byte

func init() {
	wsLUT[' '] = 1
	wsLUT['\t'] = 1
	wsLUT['\n'] = 1
	wsLUT['\r'] = 1
}

func skipWS(src []byte, idx int) int {
	for idx < len(src) && wsLUT[sliceAt(src, idx)] != 0 {
		idx++
	}
	return idx
}

//
//go:nosplit
func skipWSLong(src []byte, idx int) int {
	n := len(src)
	if idx >= n || wsLUT[sliceAt(src, idx)] == 0 {
		return idx
	}
	if sliceAt(src, idx) <= '\r' {
		idx++
		if idx >= n || wsLUT[sliceAt(src, idx)] == 0 {
			return idx
		}
		if sliceAt(src, idx) == '\n' {
			idx++
			if idx >= n || wsLUT[sliceAt(src, idx)] == 0 {
				return idx
			}
		}
	}
	const allSpaces = lo64 * 0x20 // 0x2020202020202020
	for idx+8 <= n {
		w := *(*uint64)(unsafe.Add(slicePtr(src), idx))
		if w != allSpaces {
			break
		}
		idx += 8
	}

	for idx < n && wsLUT[sliceAt(src, idx)] != 0 {
		idx++
	}
	return idx
}
