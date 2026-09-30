package bind

import (
	"reflect"
	"testing"
)

// The key transition memo indexes predictions, including the one-past
// sentinel after the last declaration field, so a member that follows it
// must read and record inside its own struct's row.

func TestGoKeyMemoAfterLastField(t *testing.T) {
	type memoS struct {
		W int `json:"w"`
		X int `json:"x"`
		Y int `json:"y"`
		Z int `json:"z"`
	}
	// Each object binds z, the last declaration field, and then w, a
	// misprediction at the sentinel the second object reads through.
	const doc = `[{"z":1,"w":2},{"z":3,"w":4},{"y":5,"z":6,"w":7}]`
	var got []memoS
	if err := Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []memoS{{W: 2, Z: 1}, {W: 4, Z: 3}, {W: 7, Y: 5, Z: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
