package bind

import (
	"reflect"
	"testing"

	"github.com/velox-io/json/vbind"
)

// Nested slice/array fields inside a variant case struct. The tape-bind
// sub-routine's t_array_value switch must carry a SLICE/ARRAY case (mirrors
// main bind.h:2849-2871) so a [][]T element descends into t_array_begin
// instead of hitting t_unsupported. RecBatch grow (recursive slice backing)
// is handled inline at t_array_value's cap check (mirrors main bind.h:2752).

type nestedSliceCase struct {
	Name string     `json:"name"`
	Mat  [][]int    `json:"mat"`
	Strs [][]string `json:"strs"`
	Cube [][][]int  `json:"cube"`
}
type nestedSliceEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[nestedSliceEnvelope, struct {
		_ nestedSliceCase `case:"ns"`
	}]()
}

func TestVariantNestedSlice_VariantFirst(t *testing.T) {
	src := `{"data":{"name":"A","mat":[[1,2],[3]],"strs":[["x"],["y","z"]],"cube":[[[1,2]]]},"type":"ns"}`
	var env nestedSliceEnvelope
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, ok := env.Data.(nestedSliceCase)
	if !ok {
		t.Fatalf("Data = %T, want nestedSliceCase", env.Data)
	}
	want := nestedSliceCase{
		Name: "A",
		Mat:  [][]int{{1, 2}, {3}},
		Strs: [][]string{{"x"}, {"y", "z"}},
		Cube: [][][]int{{{1, 2}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestVariantNestedSlice_DiscriminatorFirst(t *testing.T) {
	src := `{"type":"ns","data":{"name":"A","mat":[[1,2],[3]],"strs":[["x"]],"cube":[[[1]]]}}`
	var env nestedSliceEnvelope
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, ok := env.Data.(nestedSliceCase)
	if !ok {
		t.Fatalf("Data = %T, want nestedSliceCase", env.Data)
	}
	want := nestedSliceCase{
		Name: "A",
		Mat:  [][]int{{1, 2}, {3}},
		Strs: [][]string{{"x"}},
		Cube: [][][]int{{{1}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
