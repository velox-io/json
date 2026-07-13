package vdec

// Valid reports whether data is a valid JSON encoding.
// It accepts a single JSON value optionally surrounded by whitespace.
// An empty or whitespace-only input is not valid.
//
// Validity is determined by the same scanner used by Unmarshal, so the
// accepted language is exactly what Unmarshal would accept.
func Valid(data []byte) bool {
	n := len(data)
	idx := skipWS(data, 0)
	if idx >= n {
		return false
	}
	sc := parsers.Get()
	defer parsers.Put(sc)
	end, _, err := sc.scanValueAny(data, idx)
	if err != nil {
		return false
	}
	// Only whitespace may trail the top-level value.
	for end < n {
		if wsLUT[data[end]] == 0 {
			return false
		}
		end++
	}
	return true
}
