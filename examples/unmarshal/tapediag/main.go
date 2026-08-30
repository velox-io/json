package main

import (
	"fmt"

	json "github.com/velox-io/json"
	"github.com/velox-io/json/value"
)

type user struct {
	Name string `json:"name"`
	Role string `json:"role"`
}
type product struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}
type host struct {
	Type   string      `json:"type"`
	M1     json.Value  `json:"m1"`
	EVar   any         `json:",embed" vjson:"variant=type"`
	Others value.Value `json:",embed"`
}
type foo struct {
	Arr []host `json:"arr"`
}

// capture carries all three capture forms at once: inline variant (Data on
// "type"), plain Value member (Num), and embed capture (Remains) collecting
// every member no field claims. Remains aliases the merged document tape, so
// its diagram is the slice of the tape the parser walked for the whole array.
type capture struct {
	Type    string      `json:"type"`
	Data    any         `json:",embed" vjson:"variant=type"`
	Num     value.Value `json:"num"`
	Remains json.Value  `json:",embed"`
}

func init() {
	json.DefineVariantCases[host, struct {
		user    user
		product product
	}]()
	json.DefineVariantCases[capture, struct {
		user    user
		product product
	}]()
}

func main() {
	data := []byte(`{"arr":[
{"name":"Alice","oth1":111,"m1":{"x":1},"role":"admin","type":"user","oth2":222},
{"title":"Widget","price":99,"type":"product","oth1":"aaa","m1":{"A":1,"C":false}}
]}`)
	var obj foo
	if err := json.Unmarshal(data, &obj); err != nil {
		panic(err)
	}
	for i, item := range obj.Arr {
		v := item.Others
		fmt.Printf("=== Arr[%d] Others Type=%v Len=%d ===\n", i, v.Type(), v.Len())
		fmt.Printf("  Others.String() = %s\n", v.String())
		fmt.Print(v.TapeDiagram())
	}

	// Embed capture on a variant host: unmatched members land in Remains, two
	// elements of one array sharing the same merged tape.
	data = []byte(`[
		{"a": 1111, "type": "user", "name":"Alice", "num":1234567, "role":"admin", "x": "AAAA"},
		{"title": "Widget","price":99,"type":"product", "b": 2222, "num": 7, "c": 333}
	]`)
	var items []capture
	if err := json.Unmarshal(data, &items); err != nil {
		panic(err)
	}
	for i, it := range items {
		fmt.Printf("=== items[%d] Remains Type=%v Len=%d Data=%T ===\n",
			i, it.Remains.Type(), it.Remains.Len(), it.Data)
		fmt.Printf("  Remains.String() = %s\n", it.Remains.String())
		fmt.Print(it.Remains.TapeDiagram())
	}
}
