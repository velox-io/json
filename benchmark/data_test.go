package benchmark

import (
	"encoding/json"
	"testing"

	vjson "github.com/velox-io/json"

	"dev.local/benchmark/jsonbench"
	"dev.local/benchmark/twitter"
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

func TestMarshalMapAny(t *testing.T) {
	var m map[string]any
	if err := vjson.Unmarshal(LoadPodsCompactJSON(), &m); err != nil {
		t.Fatal("load map[string]any:", err)
	}
	_, err := vjson.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMarshalSliceAny(t *testing.T) {
	var s []any
	if err := vjson.Unmarshal(LoadPodsCompactJSON(), &s); err != nil {
		// Pods JSON is an object, wrap it in an array for []any
		var m any
		if err2 := vjson.Unmarshal(LoadPodsCompactJSON(), &m); err2 != nil {
			t.Fatal("load any:", err2)
		}
		s = []any{m, "hello", float64(42), true, nil}
	}
	_, err := vjson.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMarshalTwitter(t *testing.T) {
	var tv twitter.TwitterStruct
	if err := json.Unmarshal(LoadTwitterCompactJSON(), &tv); err != nil {
		t.Fatal("load twitter:", err)
	}
	tv.Statuses = tv.Statuses[:1]
	_, err := vjson.Marshal(tv)
	if err != nil {
		t.Fatal(err)
	}
}

type omitemptyMapStruct struct {
	Name  string            `json:"name"`
	Tags  map[string]string `json:"tags,omitempty"`
	Extra map[string]any    `json:"extra,omitempty"`
}

func TestMarshalOmitemptyMap(t *testing.T) {
	v := omitemptyMapStruct{
		Name:  "test",
		Tags:  map[string]string{"a": "b", "c": "d"},
		Extra: nil,
	}
	got, err := vjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	want, err = json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	// Compare with stdlib (note: map key order may differ, so unmarshal and compare)
	var gotMap, wantMap map[string]any
	if err := json.Unmarshal(got, &gotMap); err != nil {
		t.Fatalf("unmarshal got: %v\ngot: %s", err, got)
	}
	if err := json.Unmarshal(want, &wantMap); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if len(gotMap) != len(wantMap) {
		t.Fatalf("field count mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestMarshalKubePods(t *testing.T) {
	var pods KubePodList
	if err := json.Unmarshal(LoadPodsCompactJSON(), &pods); err != nil {
		t.Fatal("load pods:", err)
	}
	_, err := vjson.Marshal(pods)
	if err != nil {
		t.Fatal(err)
	}
}
