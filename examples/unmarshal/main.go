package main

import (
	"fmt"
	"log"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// inlHost: inline variant only. Data is virtual (,inline); case fields
// (name/role, title/price, level) unfold into the host JSON object.
type inlHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

type inlUser struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type inlProduct struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

type inlAdmin struct {
	Level int `json:"level"`
}

type inlineCaseWithNestedInlineField struct {
	Label string  `json:"label"`
	Inner inlHost `json:"inner"`
}

type hostWithNestedInlineCase struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[hostWithNestedInlineCase, struct {
		_ inlineCaseWithNestedInlineField `case:"nested"`
	}]()
	vbind.DefineVariantCases[inlHost, struct {
		_ inlUser    `case:"user"`
		_ inlProduct `case:"product"`
		_ inlAdmin   `case:"admin"`
	}]()
}

func demo1() {
	src := `{"type":"nested","label":"x","inner":{"type":"user","name":"Alice","role":"admin"}}`
	var u hostWithNestedInlineCase
	if err := vjson.Unmarshal([]byte(src), &u); err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}
}

type nestedValueOuter struct {
	Inner coldCaseInlineValue `json:"inner"`
}

type coldCaseInlineValue struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[coldCaseInlineValue, struct {
		_ vjson.Value `case:"raw"`
	}]()
}

func demo2() {
	src := `{"inner":{"type":"raw","extra":{"a":1}}}`
	val, _ := vjson.Parse([]byte(src))
	var uv nestedValueOuter
	if err := vjson.UnmarshalValue(val, &uv); err != nil {
		log.Fatalf("UnmarshalValue: %v", err)
	}
	for _, h := range []nestedValueOuter{uv} {
		vv, ok := h.Inner.Data.(value.Value)
		if !ok {
			log.Fatalf("Inner.Data = %T, want value.Value", h.Inner.Data)
		}
		typ := vv.Get("type")
		if s, ok := typ.Str(); !ok || s != "raw" {
			log.Fatalf("Inner.Data.type = %q, want %q", s, "raw")
		}
		extra := vv.Get("extra")
		a := extra.Get("a")
		if n, ok := a.Int(); !ok || n != 1 {
			log.Fatalf("Inner.Data.extra.a = %d, want 1", n)
		}
	}
}

type Item struct {
	Type    string `json:"type"`
	Data    any    `json:",embed" vjson:"variant=type"`
	Num     value.Value
	Remains vjson.Value `json:",embed"`
}

type User struct {
	Name string      `json:"name"`
	Role string      `json:"role"`
	Ext  value.Value `json:"ext"`
}

type Product struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

func init() {
	vbind.DefineVariantCases[Item, struct {
		user    User
		product Product
	}]()
}

func demo3() {
	jsonText := `[
		{"a": 1111, "type": "user", "name":"Alice", "Num":1234567, "role":"admin", "x": "AAAA" },
		{"title": "Widget","price":99,"type":"product", "b": 2222, "c": 333 } ]
	`

	fmt.Printf("src length:%d\n", len(jsonText))
	// Dump the whole-document tape so the merged A/B jump structure is visible
	// before the typed bind carves sub-Values out of it.
	_, err := vjson.Parse([]byte(jsonText))
	if err != nil {
		log.Fatalf("parse failed: %+v", err)
	}

	var results []Item
	err = vjson.Unmarshal([]byte(jsonText), &results)
	if err != nil {
		log.Fatalf("demo3 fail:%+v", err)
	}

	for _, item := range results {
		fmt.Printf("%s\n", item.Remains.String())
		fmt.Print(item.Remains.TapeDiagram())
	}

}

func main() {
	// demo1()
	// demo2()
	demo3()
}
