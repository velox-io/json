// Package main demonstrates struct marshalling with vjson: a nested
// user payload in indent mode, and a GeoJSON feature collection in
// compact and indent modes.
package main

import (
	"fmt"
	"os"

	json "github.com/velox-io/json"
)

// main runs every demo when invoked without arguments; with one
// argument it runs only the named demo (for example "canada"). An
// unknown name lists the available demos.
func main() {
	demos := []struct {
		name string
		fn   func() error
	}{
		{"user", marshalUser},
		{"canada", marshalCanada},
		{"canadaIndent", marshalCanadaIndent},
	}

	if len(os.Args) > 1 {
		for _, d := range demos {
			if d.name == os.Args[1] {
				if err := d.fn(); err != nil {
					panic(err)
				}
				return
			}
		}
		fmt.Fprintf(os.Stderr, "unknown demo %q, available:", os.Args[1])
		for _, d := range demos {
			fmt.Fprintf(os.Stderr, " %s", d.name)
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	for _, d := range demos {
		if err := d.fn(); err != nil {
			panic(err)
		}
	}
}

// marshalUser encodes the test user in indent mode.
func marshalUser() error {
	u := NewTestUser()

	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println("--- user (indent) ---")
	fmt.Println(string(b))
	return nil
}

// marshalCanada encodes the canada feature collection in compact mode.
func marshalCanada() error {
	c := NewCanadaRoot()

	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	fmt.Printf("--- canada (compact, %d bytes) ---\n", len(b))
	fmt.Println(string(b[:200]) + "...")
	return nil
}

// marshalCanadaIndent encodes the canada feature collection in indent
// mode.
func marshalCanadaIndent() error {
	c := NewCanadaRoot()

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("--- canada (indent, %d bytes) ---\n", len(b))
	fmt.Println(string(b[:300]) + "...")
	return nil
}
