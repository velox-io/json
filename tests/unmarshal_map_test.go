package tests

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"

	vjson "github.com/velox-io/json"
)

// TestUnmarshal_MapIntKeyTypeError pins that a key an integer map cannot
// hold is an UnmarshalTypeError naming the key type, as with encoding/json,
// and that the keys around it still land. Each case runs through Unmarshal,
// UnmarshalValue, and a chunked Decoder.
func TestUnmarshal_MapIntKeyTypeError(t *testing.T) {
	type host struct {
		M map[int8]string
		U map[uint16]int
	}
	cases := []struct {
		name, in, value string
		typ             reflect.Type
		cause           error
	}{
		{"not a number", `{"M":{"1":"a","x":"b","-2":"c"}}`, "number x", reflect.TypeFor[int8](), strconv.ErrSyntax},
		{"int overflow", `{"M":{"1":"a","128":"b"}}`, "number 128", reflect.TypeFor[int8](), strconv.ErrRange},
		{"uint negative", `{"U":{"7":1,"-1":2}}`, "number -1", reflect.TypeFor[uint16](), strconv.ErrSyntax},
		{"uint overflow", `{"U":{"65536":1,"9":2}}`, "number 65536", reflect.TypeFor[uint16](), strconv.ErrRange},
	}
	for _, tc := range cases {
		var std host
		stdErr := json.Unmarshal([]byte(tc.in), &std)
		var se *json.UnmarshalTypeError
		if !errors.As(stdErr, &se) || se.Type != tc.typ {
			t.Fatalf("%s: encoding/json gave %v, the case expects a %v type error", tc.name, stdErr, tc.typ)
		}
		decoders := []struct {
			name   string
			decode func(*host) error
		}{
			{"Unmarshal", func(h *host) error { return vjson.Unmarshal([]byte(tc.in), h) }},
			{"UnmarshalValue", func(h *host) error {
				v, err := vjson.Parse([]byte(tc.in))
				if err != nil {
					return err
				}
				return vjson.UnmarshalValue(v, h)
			}},
			{"Decoder", func(h *host) error { return vjson.NewDecoder(&chunkReader{data: []byte(tc.in), size: 3}).Decode(h) }},
		}
		for _, d := range decoders {
			var got host
			err := d.decode(&got)
			var ute *json.UnmarshalTypeError
			if !errors.As(err, &ute) {
				t.Errorf("%s/%s: got %T (%v), want *json.UnmarshalTypeError", tc.name, d.name, err, err)
				continue
			}
			if ute.Type != tc.typ || ute.Value != tc.value {
				t.Errorf("%s/%s: Type=%v Value=%q, want Type=%v Value=%q", tc.name, d.name, ute.Type, ute.Value, tc.typ, tc.value)
			}
			if !errors.Is(err, tc.cause) {
				t.Errorf("%s/%s: %v does not wrap %v", tc.name, d.name, err, tc.cause)
			}
			if !reflect.DeepEqual(got, std) {
				t.Errorf("%s/%s: decoded %+v, encoding/json %+v", tc.name, d.name, got, std)
			}
		}
	}
}
