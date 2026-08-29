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

func init() {
	json.DefineVariantCases[host, struct {
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
}
