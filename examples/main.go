package main

import (
	"fmt"

	vjson "github.com/velox-io/json"
)

func main() {

	type User struct {
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}

	var u User
	err := vjson.Unmarshal([]byte(`{"name":"alice","roles":["admin"]}`), &u)
	if err != nil {
		panic(err)
	}

	out, err := vjson.Marshal(u) // {"name":"alice","roles":["admin"]}
	if err != nil {
		panic(err)
	}
	fmt.Println(string(out))
}
