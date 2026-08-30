// Package main demonstrates partial binding: known JSON keys bind to typed
// Go fields, unmatched keys are captured by an embedded Value.
package main

import vjson "github.com/velox-io/json"

type Foo struct {
	Name    string      `json:"name"`
	Count   int         `json:"count"`
	Exts    vjson.Value `json:",embed"`
	Message string      `json:"message"`
}

func main() {
	data := `{"name": "bob", "count": 10, "abc": {"a":1, "b":2, "c": 3}, "message": "OK", "xx": "some info"}`

	var result Foo
	if err := vjson.Unmarshal([]byte(data), &result); err != nil {
		panic(err)
	}

	// Known fields bind to typed Go slots.
	if result.Name != "bob" {
		panic("name: " + result.Name)
	}
	if result.Count != 10 {
		panic("count")
	}
	if result.Message != "OK" {
		panic("message: " + result.Message)
	}

	// Unmatched keys are captured into Exts as a tape-backed value.Value
	// (kind Object). Navigation aliases the parse tape, so reads are zero-copy.
	if result.Exts.Type() != vjson.ValueObject {
		panic("exts type")
	}
	if result.Exts.Len() != 2 {
		panic("exts len")
	}

	abc := result.Exts.Get("abc")
	if !abc.Valid() {
		panic("abc missing")
	}
	a := abc.Get("a")
	if ai, ok := a.Int(); !ok || ai != 1 {
		panic("abc.a")
	}

	xx := result.Exts.Get("xx")
	if xs, ok := xx.Str(); !ok || xs != "some info" {
		panic("xx")
	}
}
