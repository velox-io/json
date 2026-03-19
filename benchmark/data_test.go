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

func loadSyntheaFHIRSingleEntry() *jsonbench.SyntheaRoot {
	root, err := jsonbench.LoadSyntheaFHIR()
	if err != nil {
		panic("load synthea_fhir: " + err.Error())
	}
	root.Entry = root.Entry[:1]
	return root
}

func TestMarshalSyntheaFHIRSingleEntry(t *testing.T) {
	root := loadSyntheaFHIRSingleEntry()
	_, err := vjson.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
}
