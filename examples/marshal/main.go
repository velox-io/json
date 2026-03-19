package main

import (
	"fmt"

	json "github.com/velox-io/json"
)

//nolint:unused
func marshalUser() {
	u := NewTestUser()

	b2, err := json.MarshalIndent(&u, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println("--- indent ---")
	fmt.Println(string(b2))
}

//nolint:unused
func marshalCanada() {
	c := NewCanadaRoot()

	b, err := json.Marshal(&c)
	if err != nil {
		panic(err)
	}
	fmt.Printf("--- canada (compact, %d bytes) ---\n", len(b))
	fmt.Println(string(b[:200]) + "...")

	// b2, err := json.MarshalIndent(&c, "", "  ")
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Printf("--- canada (indent, %d bytes) ---\n", len(b2))
	// fmt.Println(string(b2[:300]) + "...")
}

func main() {
	marshalUser()
	// marshalCanada()
}
