package benchmark

import (
	"testing"

	vjson "github.com/velox-io/json"

	"dev.local/benchmark/jsonbench"
)

func loadTwitterSingleStatus() *jsonbench.TwitterRoot {
	root, err := jsonbench.LoadTwitterStatus()
	if err != nil {
		panic("load twitter_status: " + err.Error())
	}
	root.Statuses = root.Statuses[:1]
	return root
}

func TestMarshalTwitterSingleStatus(t *testing.T) {
	root := loadTwitterSingleStatus()
	_, err := vjson.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
}
