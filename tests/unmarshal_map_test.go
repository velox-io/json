package tests

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"weak"

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

// mkText is a string-kind key whose UnmarshalText outranks its kind.
type mkText string

func (k *mkText) UnmarshalText(b []byte) error {
	*k = mkText("t:" + string(b))
	return nil
}

// mkIntText is an integer-kind key whose UnmarshalText outranks the base-10
// parse.
type mkIntText int

func (k *mkIntText) UnmarshalText(b []byte) error {
	*k = mkIntText(len(b))
	return nil
}

// mkAcc appends to what its receiver holds, so it decodes a key to its text
// only from a zero receiver.
type mkAcc struct{ S string }

func (k *mkAcc) UnmarshalText(b []byte) error {
	k.S += "|" + string(b)
	return nil
}

// mkValRecv decodes through a value receiver, so every key stays zero.
type mkValRecv struct{ S string }

func (mkValRecv) UnmarshalText([]byte) error { return nil }

// mkJSONStr is a string-kind key with only UnmarshalJSON, which a key does
// not consult.
type mkJSONStr string

func (k *mkJSONStr) UnmarshalJSON([]byte) error {
	*k = "json"
	return nil
}

type mkPlain struct{ A int }

type mkHost struct {
	A int                  `json:"a"`
	M map[netip.Addr][]int `json:"m"`
	P map[mkPlain]int      `json:"p"`
	B int                  `json:"b"`
}

// addrObject builds an object of n distinct address keys, enough to fill
// more than one staging region.
func addrObject(n int, extra ...string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"10.0.%d.%d":%d`, i/256, i%256, i)
	}
	for _, e := range extra {
		b.WriteString(",")
		b.WriteString(e)
	}
	b.WriteByte('}')
	return b.String()
}

// TestUnmarshal_MapTextKeys pins map key conversion against encoding/json:
// a TextUnmarshaler key decodes through UnmarshalText from a zero key
// whatever its kind, a string kind without one takes the key as is, and a
// key of any other kind is a type error.
func TestUnmarshal_MapTextKeys(t *testing.T) {
	cases := []corpusCase{
		{"addr", `{"192.168.0.1":"a","::1":"b","10.0.0.1":"c"}`, newOf[map[netip.Addr]string](), true},
		{"addr escaped key", `{"1\u002e2.3.4":1}`, newOf[map[netip.Addr]int](), true},
		{"addr empty key", `{"":1}`, newOf[map[netip.Addr]int](), true},
		{"addr duplicate key", `{"1.2.3.4":1,"1.2.3.4":2}`, newOf[map[netip.Addr]int](), true},
		{"addr many keys", addrObject(70), newOf[map[netip.Addr]int](), true},
		{"addr nested", `{"1.2.3.4":{"x":1,"yy":2},"::":{}}`, newOf[map[netip.Addr]map[mkIntText]int](), true},
		{"addr in struct", `{"a":1,"m":{"1.2.3.4":[1,2],"::1":[]},"b":2}`, newOf[mkHost](), true},
		{"addr large value", `{"1.2.3.4":[1,2,3]}`, newOf[map[netip.Addr][20]int64](), true},
		{"addr deferred value", `{"1.2.3.4":{"k":[1]},"::1":"s"}`, newOf[map[netip.Addr]json.RawMessage](), false},
		{"string kind", `{"a":1,"b\u00e9":2,"":3}`, newOf[map[mkText]int](), true},
		{"int kind", `{"abc":1,"7":2}`, newOf[map[mkIntText]int](), true},
		{"zero receiver", `{"a":1,"b":2,"a":3}`, newOf[map[mkAcc]int](), true},
		{"value receiver", `{"a":1,"b":2}`, newOf[map[mkValRecv]int](), true},
		{"json-only string kind", `{"a":1,"b":2}`, newOf[map[mkJSONStr]int](), true},
		{"number", `{"1.5":1,"-2":2}`, newOf[map[json.Number]int](), true},
		{"struct key", `{"a":1}`, newOf[map[mkPlain]int](), true},
		{"bool key", `{"true":1}`, newOf[map[bool]int](), true},
		{"array key", `{"a":1}`, newOf[map[[2]int]int](), true},
		{"struct key in host", `{"a":1,"p":{"x":1},"b":2}`, newOf[mkHost](), true},
	}
	for _, c := range cases {
		assertParity(t, c)
	}
}

// decodeEntries decodes in into a fresh mk() through Unmarshal, chunked
// Decoders, and, when uv, Parse followed by UnmarshalValue, handing each
// result to check.
func decodeEntries(in string, mk func() any, uv bool, check func(entry string, v any, err error)) {
	data := []byte(in)
	v := mk()
	check("Unmarshal", v, vjson.Unmarshal(data, v))
	for _, chunk := range []int{1, len(data) + 1} {
		v = mk()
		check(fmt.Sprintf("Decoder(chunk=%d)", chunk), v, vjson.NewDecoder(&chunkReader{data: data, size: chunk}).Decode(v))
	}
	if uv {
		v = mk()
		pv, err := vjson.Parse(data)
		if err == nil {
			err = vjson.UnmarshalValue(pv, v)
		}
		check("UnmarshalValue", v, err)
	}
}

var (
	errKeyHook = errors.New("key hook failure")
	errValHook = errors.New("value hook failure")
)

// mkFailKey fails the key "bad".
type mkFailKey struct{ S string }

func (k *mkFailKey) UnmarshalText(b []byte) error {
	if string(b) == "bad" {
		return errKeyHook
	}
	k.S = string(b)
	return nil
}

type mkFailVal struct{}

func (*mkFailVal) UnmarshalText([]byte) error { return errValHook }

type mkKeyFailHost struct {
	M map[mkFailKey]int `json:"m"`
	N int               `json:"n"`
}

type mkFailHost struct {
	M map[mkFailKey]int `json:"m"`
	V mkFailVal         `json:"v"`
	N int               `json:"n"`
}

// TestUnmarshal_MapTextKeyError pins a failing key hook: the caller gets
// the error the hook returned, and the walk goes on, so every other key and
// field lands. Failures rank walk errors first, then value hooks, then keys,
// whatever their document order; encoding/json reports the first in
// document order instead.
func TestUnmarshal_MapTextKeyError(t *testing.T) {
	many := strings.ReplaceAll(addrObject(40, `"bad":40`, `"10.1.0.0":41`), `"10.0.`, `"k.`)
	for _, in := range []string{`{"m":{"ok":1,"bad":2,"ok2":3},"n":1}`, `{"m":` + many + `,"n":1}`} {
		decodeEntries(in, newOf[mkKeyFailHost](), true, func(entry string, v any, err error) {
			if err != errKeyHook {
				t.Errorf("%.30s/%s: got %v, want the key hook's error", in, entry, err)
				return
			}
			h := v.(*mkKeyFailHost)
			var std map[string]int
			if err := json.Unmarshal([]byte(in), &struct {
				M *map[string]int `json:"m"`
			}{&std}); err != nil {
				t.Fatal(err)
			}
			delete(std, "bad")
			if len(h.M) != len(std) || h.N != 1 {
				t.Errorf("%.30s/%s: kept %d keys and n=%d, want %d keys and n=1", in, entry, len(h.M), h.N, len(std))
			}
			for k, n := range std {
				if got, ok := h.M[mkFailKey{k}]; !ok || got != n {
					t.Errorf("%.30s/%s: key %q holds %d (present %v), want %d", in, entry, k, got, ok, n)
				}
			}
		})
	}
	for _, c := range []struct {
		in   string
		want func(error) bool
	}{
		{`{"m":{"bad":1},"v":"x"}`, func(err error) bool { return err == errValHook }},
		{`{"v":"x","m":{"bad":1}}`, func(err error) bool { return err == errValHook }},
		{`{"m":{"bad":1},"n":"x"}`, func(err error) bool {
			var ute *json.UnmarshalTypeError
			return errors.As(err, &ute) && ute.Type == reflect.TypeFor[int]()
		}},
	} {
		decodeEntries(c.in, newOf[mkFailHost](), false, func(entry string, _ any, err error) {
			if !c.want(err) {
				t.Errorf("%s/%s: got %v", c.in, entry, err)
			}
		})
	}
}

// mkHeld's hook leaves a fresh payload reachable only through the key it
// decodes into, then collects: the payload survives only if the GC scans
// that key's storage.
type mkHeld struct{ P *mkPayload }

type mkPayload struct{ S string }

var mkHeldLost int

func (k *mkHeld) UnmarshalText(b []byte) error {
	k.P = &mkPayload{S: string(b)}
	w := weak.Make(k.P)
	runtime.GC()
	if w.Value() == nil {
		mkHeldLost++
	}
	return nil
}

// TestUnmarshal_MapTextKeyGCVisible pins that a key hook decodes into
// storage the GC scans, so what it writes there outlives a collection before
// the map takes the key.
func TestUnmarshal_MapTextKeyGCVisible(t *testing.T) {
	decodeEntries(`{"a":1,"b":2}`, newOf[map[mkHeld]int](), true, func(entry string, v any, err error) {
		mkHeldLost = 0
		if err != nil {
			t.Fatalf("%s: %v", entry, err)
		}
		got := map[string]int{}
		for k, n := range *v.(*map[mkHeld]int) {
			got[k.P.S] = n
		}
		if !reflect.DeepEqual(got, map[string]int{"a": 1, "b": 2}) {
			t.Errorf("%s: decoded %v", entry, got)
		}
		if mkHeldLost != 0 {
			t.Errorf("%s: %d hook payloads collected while their key awaited the map", entry, mkHeldLost)
		}
	})
}

// mkBoth implements both hooks; a key decodes through UnmarshalText, as with
// Go 1.27's encoding/json.
type mkBoth struct{ S string }

func (k *mkBoth) UnmarshalJSON(b []byte) error {
	k.S = "json:" + string(b)
	return nil
}

func (k *mkBoth) UnmarshalText(b []byte) error {
	k.S = "text:" + string(b)
	return nil
}

func TestUnmarshal_MapKeyBothHooks(t *testing.T) {
	decodeEntries(`{"a":1}`, newOf[map[mkBoth]int](), true, func(entry string, v any, err error) {
		if want := (map[mkBoth]int{{"text:a"}: 1}); err != nil || !reflect.DeepEqual(*v.(*map[mkBoth]int), want) {
			t.Errorf("%s: decoded %v (%v), want %v", entry, *v.(*map[mkBoth]int), err, want)
		}
	})
}

// TestUnmarshal_MapKeyUnconvertible pins that a key of a kind no key
// converts to is an UnmarshalTypeError naming the key type, and that an
// empty object still decodes into an empty map.
func TestUnmarshal_MapKeyUnconvertible(t *testing.T) {
	for _, c := range []struct {
		mk  func() any
		typ reflect.Type
	}{
		{newOf[map[float64]int](), reflect.TypeFor[float64]()},
		{newOf[map[*mkText]int](), reflect.TypeFor[*mkText]()},
		{newOf[map[any]int](), reflect.TypeFor[any]()},
		{newOf[map[mkPlain]int](), reflect.TypeFor[mkPlain]()},
	} {
		decodeEntries(`{"1":1}`, c.mk, true, func(entry string, _ any, err error) {
			var ute *json.UnmarshalTypeError
			if !errors.As(err, &ute) || ute.Type != c.typ || ute.Value != "string" {
				t.Errorf("%v/%s: got %v, want a type error naming the key", c.typ, entry, err)
			}
		})
		decodeEntries(`{}`, c.mk, true, func(entry string, v any, err error) {
			if m := reflect.ValueOf(v).Elem(); err != nil || m.IsNil() || m.Len() != 0 {
				t.Errorf("%v/%s: decoded %v (%v), want an empty map", c.typ, entry, m, err)
			}
		})
	}
}
