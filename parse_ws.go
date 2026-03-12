package vjson

import "unsafe"

// wsLUT is a lookup table for JSON whitespace characters.
// Indexed by byte value; non-zero means whitespace.
var wsLUT [256]byte

func init() {
	wsLUT[' '] = 1
	wsLUT['\t'] = 1
	wsLUT['\n'] = 1
	wsLUT['\r'] = 1
}

// skipWS skips JSON whitespace (SP, TAB, LF, CR) starting at idx.
func skipWS(src []byte, idx int) int {
	for idx < len(src) && wsLUT[sliceAt(src, idx)] != 0 {
		idx++
	}
	return idx
}

// skipWSLong skips JSON whitespace with a fast path for long space runs.
//
//go:nosplit
func skipWSLong(src []byte, idx int) int {
	n := len(src)
	if idx >= n || wsLUT[sliceAt(src, idx)] == 0 {
		return idx
	}
	// Handle leading control whitespace.
	if sliceAt(src, idx) <= '\r' {
		idx++
		if idx >= n || wsLUT[sliceAt(src, idx)] == 0 {
			return idx
		}
		// CRLF support.
		if sliceAt(src, idx) == '\n' {
			idx++
			if idx >= n || wsLUT[sliceAt(src, idx)] == 0 {
				return idx
			}
		}
	}
	// SWAR scan for 8-byte all-space chunks.
	const allSpaces = lo64 * 0x20 // 0x2020202020202020
	for idx+8 <= n {
		w := *(*uint64)(unsafe.Add(slicePtr(src), idx))
		if w != allSpaces {
			break
		}
		idx += 8
	}

	// Tail scan for remaining whitespace.
	for idx < n && wsLUT[sliceAt(src, idx)] != 0 {
		idx++
	}
	return idx
}
