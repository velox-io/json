package benchmark

import (
	"bytes"
	_ "embed"
	"encoding/json"
)

var SmallJSON = []byte(`{
	"bool": true,
	"int": 42,
	"int64": 9223372036854775807,
	"float64": 3.14159265358979,
	"string": "hello world benchmark"
}`)

//go:embed testdata/escape_heavy.json
var EscapeHeavyJSON []byte

//go:embed testdata/pods.json
var PodsJSON []byte

//go:embed testdata/twitter.json
var TwitterJSON []byte

// Compact (whitespace-stripped) versions of all JSON test data.
var (
	SmallCompactJSON       []byte
	EscapeHeavyCompactJSON []byte
	PodsCompactJSON        []byte
	TwitterCompactJSON     []byte
)

func compact(src []byte) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, src); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func init() {
	SmallCompactJSON = compact(SmallJSON)
	EscapeHeavyCompactJSON = compact(EscapeHeavyJSON)
	PodsCompactJSON = compact(PodsJSON)
	TwitterCompactJSON = compact(TwitterJSON)
}
