package vjson

import "github.com/velox-io/json/vdec"

// Valid reports whether data is a valid JSON encoding.
// It accepts a single JSON value optionally surrounded by whitespace.
// An empty or whitespace-only input is not valid.
//
// Equivalent to encoding/json.Valid.
func Valid(data []byte) bool {
	return vdec.Valid(data)
}
