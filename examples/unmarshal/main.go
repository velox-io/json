package main

import (
	"encoding/json"
	"fmt"

	vjson "github.com/velox-io/json"
)

type Foo struct {
	Name  string         `json:"name"`
	Value map[int]string `json:"value"`
}

func main() {
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
