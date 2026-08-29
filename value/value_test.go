package value

import (
	"encoding/json"
	"testing"

	"github.com/velox-io/json/internal/valueabi"
)

func testValue(doc *valueabi.Doc) Value {
	return Value{desc: valueabi.Descriptor{Doc: doc, End: int32(len(doc.Tape))}}
}

func testValueAt(doc *valueabi.Doc, base, tidx, end, mode int32) Value {
	return Value{desc: valueabi.Descriptor{Doc: doc, Base: base, Tidx: tidx, End: end, Mode: mode}}
}

func testStringArena(parts ...string) ([]byte, []uint32) {
	used := 0
	for _, part := range parts {
		used += len(part) + 1
	}
	arena := make([]byte, used+64)
	offsets := make([]uint32, len(parts))
	at := 0
	for i, part := range parts {
		offsets[i] = uint32(at)
		copy(arena[at:], part)
		at += len(part)
		arena[at] = '"'
		at++
	}
	return arena[:used], offsets
}

// makeTapeValue builds a Value backed by a hand-constructed tape so the tape
// dispatch path can be unit-tested without going through the bind engine
// (which lives in a package that imports value, creating a cycle).
//
// Tape layout for {"id":"abc","n":7,"ok":true,"tags":["a","b"],"miss":null}:
//
//	 0  TAPE_OBJ_BEG       (close=15, count=5)
//	 1  TAPE_STRING "id"     (off=0,  len=2)
//	 2  TAPE_STRING "abc"    (off=2,  len=3)
//	 3  TAPE_STRING "n"      (off=5,  len=1)
//	 4  TAPE_INT64 7
//	 5  7                    (int value word)
//	 6  TAPE_STRING "ok"     (off=6,  len=2)
//	 7  TAPE_TRUE
//	 8  TAPE_STRING "tags"   (off=8,  len=4)
//	 9  TAPE_ARR_BEG         (close=12, count=2)
//	10  TAPE_STRING "a"      (off=12, len=1)
//	11  TAPE_STRING "b"      (off=13, len=1)
//	12  TAPE_ARR_END
//	13  TAPE_STRING "miss"   (off=14, len=4)
//	14  TAPE_NULL
//	15  TAPE_OBJ_END
//
// str_arena holds each body followed by its quote sentinel.
func makeTapeValue() Value {
	const (
		tagObjBeg = uint64('{') << 56
		tagObjEnd = uint64('}') << 56
		tagArrBeg = uint64('[') << 56
		tagArrEnd = uint64(']') << 56
		tagStr    = uint64('"') << 56
		tagInt64  = uint64('l') << 56
		tagTrue   = uint64('t') << 56
		tagNull   = uint64('n') << 56
	)
	strPack := func(off, n uint32) uint64 { return tagStr | uint64(off) | (uint64(n) << 32) }

	strArena, off := testStringArena("id", "abc", "n", "ok", "tags", "a", "b", "miss")
	tape := []uint64{
		tagObjBeg | 15 | (5 << 32), // obj at 0, close at 15, count=5
		strPack(off[0], 2),         // 1: "id"
		strPack(off[1], 3),         // 2: "abc"
		strPack(off[2], 1),         // 3: "n"
		tagInt64,                   // 4: TAPE_INT64
		7,                          // 5: value 7
		strPack(off[3], 2),         // 6: "ok"
		tagTrue,                    // 7: TAPE_TRUE
		strPack(off[4], 4),         // 8: "tags"
		tagArrBeg | 12 | (2 << 32), // 9: arr, close at 12, count=2
		strPack(off[5], 1),         // 10: "a"
		strPack(off[6], 1),         // 11: "b"
		tagArrEnd,                  // 12: TAPE_ARR_END
		strPack(off[7], 4),         // 13: "miss"
		tagNull,                    // 14: TAPE_NULL
		tagObjEnd,                  // 15: TAPE_OBJ_END
	}
	return testValue(&valueabi.Doc{Tape: tape, StrArena: strArena})
}

func TestTape_TypeAndLen(t *testing.T) {
	v := makeTapeValue()
	if got := v.Type(); got != KindObject {
		t.Errorf("Type() = %v, want object", got)
	}
	if n := v.Len(); n != 5 {
		t.Errorf("Len() = %d, want 5", n)
	}
	if !v.Valid() {
		t.Error("Valid() = false for tape-backed Value")
	}
	if !v.Exists() {
		t.Error("Exists() = false for tape-backed Value")
	}
}

func TestTape_EmptyValue(t *testing.T) {
	var v Value
	if v.Exists() {
		t.Error("zero Value Exists() = true")
	}
	if v.Type() != KindInvalid {
		t.Errorf("zero Value Type() = %v", v.Type())
	}
	if v.Valid() {
		t.Error("zero Value Valid() = true")
	}
	if _, ok := v.Str(); ok {
		t.Error("zero Value Str() succeeded")
	}
	if s := v.String(); s != "" {
		t.Errorf("zero Value String() = %q", s)
	}
}

func TestTape_GetString(t *testing.T) {
	v := makeTapeValue()
	if got, ok := v.GetString("id"); !ok || got != "abc" {
		t.Errorf(`GetString("id") = %q, %v; want "abc"`, got, ok)
	}
}

func TestTape_GetInt(t *testing.T) {
	v := makeTapeValue()
	if got, ok := v.GetInt("n"); !ok || got != 7 {
		t.Errorf(`GetInt("n") = %d, %v; want 7`, got, ok)
	}
}

func TestTape_GetBool(t *testing.T) {
	v := makeTapeValue()
	if got, ok := v.GetBool("ok"); !ok || !got {
		t.Errorf(`GetBool("ok") = %v, %v; want true`, got, ok)
	}
}

func TestTape_IsNull(t *testing.T) {
	v := makeTapeValue()
	miss := v.Get("miss")
	if !miss.Exists() {
		t.Error(`Get("miss") should exist (it's an explicit null)`)
	}
	if !miss.IsNull() {
		t.Error(`Get("miss").IsNull() = false`)
	}
}

func TestTape_ArrayNavigation(t *testing.T) {
	v := makeTapeValue()
	tags := v.Get("tags")
	if got := tags.Type(); got != KindArray {
		t.Errorf("tags Type() = %v, want array", got)
	}
	if n := tags.Len(); n != 2 {
		t.Errorf("tags Len() = %d, want 2", n)
	}
	var got []string
	tags.ForEachElem(func(_ int, e Value) bool {
		s, ok := e.Str()
		if !ok {
			t.Errorf("elem Str() failed")
		}
		got = append(got, s)
		return true
	})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("tags = %v, want [a b]", got)
	}
	e := tags.Index(1)
	if s, ok := e.Str(); !ok || s != "b" {
		t.Errorf(`tags.Index(1).Str() = %q, %v; want "b"`, s, ok)
	}
	if e = tags.Index(5); e.Exists() {
		t.Error("tags.Index(5) should not exist")
	}
}

func TestTape_ForEachKey(t *testing.T) {
	v := makeTapeValue()
	var keys []string
	v.ForEachKey(func(k string, _ Value) bool {
		keys = append(keys, k)
		return true
	})
	want := []string{"id", "n", "ok", "tags", "miss"}
	if len(keys) != len(want) {
		t.Fatalf("ForEachKey yielded %d keys, want %d: %v", len(keys), len(want), keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("key %d = %q, want %q", i, keys[i], want[i])
		}
	}
}

func TestTape_ForEachEarlyStop(t *testing.T) {
	v := makeTapeValue()
	n := 0
	v.ForEachKey(func(string, Value) bool { n++; return n < 2 })
	if n != 2 {
		t.Errorf("ForEachKey ran %d times, want 2", n)
	}
}

func TestTape_NestedPath(t *testing.T) {
	// A child Value from Get carries the tape index forward: descending into
	// tags should still hit the tape path.
	v := makeTapeValue()
	tags := v.Get("tags")
	e := tags.Index(0)
	if got, ok := e.Str(); !ok || got != "a" {
		t.Errorf(`Get("tags").Index(0).Str() = %q, %v; want "a"`, got, ok)
	}
}

func TestTape_MissingKey(t *testing.T) {
	v := makeTapeValue()
	if e := v.Get("nope"); e.Exists() {
		t.Error(`Get("nope") should not exist`)
	}
	if _, ok := v.GetString("nope"); ok {
		t.Error(`GetString("nope") should fail`)
	}
}

func TestTape_StringReserializes(t *testing.T) {
	// String() re-serializes from tape.
	v := makeTapeValue()
	tags := v.Get("tags")
	if tags.String() != `["a","b"]` {
		t.Errorf("tags String() = %s, want [\"a\",\"b\"]", tags.String())
	}
}

func TestTape_MarshalJSON(t *testing.T) {
	v := makeTapeValue()
	out, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"abc","n":7,"ok":true,"tags":["a","b"],"miss":null}`
	if string(out) != want {
		t.Errorf("MarshalJSON = %s, want %s", out, want)
	}
	// Zero Value marshals as null.
	var z Value
	out, err = z.MarshalJSON()
	if err != nil || string(out) != "null" {
		t.Errorf("zero Value MarshalJSON = %s, %v; want null", out, err)
	}
}

func TestTape_RoundTripWithStdlib(t *testing.T) {
	v := makeTapeValue()
	out, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	// The re-serialized bytes must round-trip through encoding/json to the
	// same logical structure.
	var dst map[string]any
	if err := json.Unmarshal(out, &dst); err != nil {
		t.Fatalf("stdlib rejected re-serialized tape: %v\n%s", err, out)
	}
	if dst["id"] != "abc" {
		t.Errorf("dst[id] = %v, want abc", dst["id"])
	}
	if dst["n"] != float64(7) {
		t.Errorf("dst[n] = %v, want 7", dst["n"])
	}
	if dst["ok"] != true {
		t.Errorf("dst[ok] = %v, want true", dst["ok"])
	}
	if dst["miss"] != nil {
		t.Errorf("dst[miss] = %v, want nil", dst["miss"])
	}
	tags, _ := dst["tags"].([]any)
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("dst[tags] = %v, want [a b]", dst["tags"])
	}
}
