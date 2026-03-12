package main

import (
	"fmt"

	json "github.com/velox-io/json"
)

func main() {
	u := NewTestUser()

	// b, err := json.Marshal(&u)
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Println("--- compact ---")
	// fmt.Println(string(b))

	b2, err := json.MarshalIndent(&u, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println("--- indent ---")
	fmt.Println(string(b2))
}
