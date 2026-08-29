package bind

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/vbind"
)

// Nested deferred value fields (Unmarshaler/TextUnmarshaler/RawMessage) are
// rejected by the tape-bind sub-routine: it has no tape→bytes rebuild step.
// computeTapeBindUnsupported marks them at build time so UnmarshalValue fails
// fast at entry with TapeBindUnsupportedError (matching the runtime
// t_unsupported gate in bind.h). The JSON bind path (Unmarshal) supports them
// via raw-bytes capture, so it is the control that must still pass.

type deferredUnmarshalerTarget struct {
	V string
}

func (t *deferredUnmarshalerTarget) UnmarshalJSON(b []byte) error {
	t.V = string(b)
	return nil
}

type deferredRawFieldHost struct {
	ID  string          `json:"id"`
	Raw json.RawMessage `json:"raw"`
}

type deferredUmFieldHost struct {
	ID string                    `json:"id"`
	Um deferredUnmarshalerTarget `json:"um"`
}

type deferredSliceHost struct {
	Raws []json.RawMessage `json:"raws"`
}

type deferredMapHost struct {
	Raws map[string]json.RawMessage `json:"raws"`
}

// Variant case struct that nests a RawMessage field. The case type itself is
// a struct (concrete), so checkVariantCaseTypes does not flag it; the nested
// RawMessage field is caught by walkVariantCaseIntoType.
type deferredCaseRaw struct {
	Raw json.RawMessage `json:"raw"`
}

type deferredCaseEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[deferredCaseEnvelope, struct {
		_ deferredCaseRaw `case:"raw"`
	}]()
}

func TestUnmarshalValueUnsupportedDeferred(t *testing.T) {
	cases := []struct {
		name string
		in   string
		dest func() any
	}{
		{"raw-field", `{"id":"x","raw":{"a":1}}`, func() any { return new(deferredRawFieldHost) }},
		{"unmarshaler-field", `{"id":"x","um":{"a":1}}`, func() any { return new(deferredUmFieldHost) }},
		{"raw-slice", `{"raws":[{"a":1},{"b":2}]}`, func() any { return new(deferredSliceHost) }},
		{"raw-map", `{"raws":{"k":{"a":1}}}`, func() any { return new(deferredMapHost) }},
		{"raw-in-variant-case", `{"type":"raw","data":{"raw":{"a":1}}}`, func() any { return new(deferredCaseEnvelope) }},
	}
	for _, c := range cases {
		// JSON bind path supports deferred via raw-bytes capture.
		if err := Unmarshal([]byte(c.in), c.dest()); err != nil {
			t.Errorf("%s: Unmarshal (control) unexpected error: %v", c.name, err)
		}

		// tape-bind path rejects at entry.
		val, err := dom.Parse([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: dom.Parse: %v", c.name, err)
		}
		err = UnmarshalValue(val, c.dest())
		var uerr *TapeBindUnsupportedError
		if !errors.As(err, &uerr) {
			t.Errorf("%s: want *TapeBindUnsupportedError, got %T: %v", c.name, err, err)
			continue
		}
		if !strings.Contains(uerr.Pos.Reason, "deferred value field") {
			t.Errorf("%s: Reason=%q, want substring %q", c.name, uerr.Pos.Reason, "deferred value field")
		}
		if uerr.Pos.Path == "" {
			t.Errorf("%s: empty path: %+v", c.name, uerr.Pos)
		}
	}
}

// TestUnmarshalValueNumberFieldRejected pins that json.Number fields are
// flagged at entry: tape number tokens (TAPE_INT64/UINT64/DOUBLE) store only
// the parsed value, not the source offset, so json.Number's original-text
// contract cannot be honored by the tape-bind sub-routine. UnmarshalValue
// fails fast with TapeBindUnsupportedError; Unmarshal (JSON bind) supports
// Number via raw-bytes capture and is the control.
func TestUnmarshalValueNumberFieldRejected(t *testing.T) {
	type numberHost struct {
		ID string      `json:"id"`
		N  json.Number `json:"n"`
	}
	src := `{"id":"x","n":42}`

	// JSON bind path supports Number (raw bytes → string).
	var u numberHost
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("Unmarshal (control): %v", err)
	}

	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var uv numberHost
	err = UnmarshalValue(val, &uv)
	assertTapeBindUnsupported(t, err, "json.Number field unsupported", ".n")
}
