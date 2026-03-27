package tests

import (
	"bytes"
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
)

func TestEncoder_NDJSON_Stream(t *testing.T) {
	var buf bytes.Buffer
	enc := vjson.NewEncoder(&buf)

	type Record struct {
		V int `json:"v"`
	}

	for i := range 5 {
		r := Record{V: i}
		if err := enc.Encode(&r); err != nil {
			t.Fatalf("encode %d: %v", i, err)
		}
	}

	// Verify: decode with Decoder
	dec := vjson.NewDecoder(strings.NewReader(buf.String()))
	for i := range 5 {
		var r Record
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if r.V != i {
			t.Errorf("decode %d: got V=%d, want %d", i, r.V, i)
		}
	}
}
