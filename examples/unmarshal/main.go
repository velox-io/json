package main

import (
	"encoding/json"
	"fmt"

	vjson "github.com/velox-io/json"
)

type Foo struct {
	Name    string         `json:"name"`
	Value   map[int]string `json:"value"`
	Dynamic any            `json:"dynamic"`
}

type Bar struct {
	Key1 string `json:"key1"`
	Key2 string `json:"key2"`
}

func demo1() {
	var v = make(map[int]string)
	v[0] = "djlajfdlajf"
	foo := &Foo{
		Name:  "abc",
		Value: v,
	}
	data := `{"Name":"edf", "value": {"123": "v"}}`
	err := vjson.Unmarshal([]byte(data), foo)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", foo)

	var v2 = make(map[int]string)
	v2[0] = "djlajfdlajf"
	foo2 := &Foo{
		Name:  "abc",
		Value: v2,
	}
	err = json.Unmarshal([]byte(data), foo2)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", foo2)
}

func unmarshalDynamicField() {
	data := `{"Name":"edf", "value": {"123": "v"}, "dynamic": {"key2": "key2 value from json"}}`

	newFoo := func() *Foo {
		return &Foo{
			Name:  "abc",
			Value: map[int]string{0: "djlajfdlajf"},
			Dynamic: Bar{
				Key1: "pre defined key1 value",
			},
		}
	}

	// vjson
	vj := newFoo()
	if err := vjson.Unmarshal([]byte(data), vj); err != nil {
		panic(err)
	}

	// encoding/json
	std := newFoo()
	if err := json.Unmarshal([]byte(data), std); err != nil {
		panic(err)
	}

	fmt.Println("--- demo2: unmarshal into struct with pre-populated Dynamic (Bar) ---")
	fmt.Printf("  vjson: Name=%q Value=%v Dynamic=%+v\n", vj.Name, vj.Value, vj.Dynamic)
	fmt.Printf("  std:   Name=%q Value=%v Dynamic=%+v\n", std.Name, std.Value, std.Dynamic)
}

func main() {
	unmarshalDynamicField()
}
