package ndec

import "testing"

type sliceInt32Box struct {
	Xs []int32 `json:"xs"`
}

type sliceStringBox struct {
	Ss []string `json:"ss"`
}

type sliceFloatBox struct {
	Fs []float64 `json:"fs"`
}

type sliceBoolBox struct {
	Bs []bool `json:"bs"`
}

type sliceMixedBox struct {
	Tag string   `json:"tag"`
	Xs  []int32  `json:"xs"`
	Ss  []string `json:"ss"`
	N   int32    `json:"n"`
}

func TestSliceInt32(t *testing.T) {
	cases := []string{
		`{"xs":[1,2,3]}`,
		`{"xs":[]}`,
		`{"xs":[42]}`,
		`{"xs":[1,2,3,4,5,6,7,8,9,10]}`,
		`{"xs":[-2147483648,2147483647,0]}`,
		`{"xs":null}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want sliceInt32Box
			runParity(t, in, &got, &want)
		})
	}
}

func TestSliceString(t *testing.T) {
	cases := []string{
		`{"ss":["a","b","c"]}`,
		`{"ss":[]}`,
		`{"ss":[""]}`,
		`{"ss":["one","two","three","four","five","six"]}`, // grow
		`{"ss":["esc\nhere","plain","中文"]}`,
		`{"ss":[null,"x"]}`,
		// This case forces a grow on the same element that carries null so the
		// grow sentinel cannot be confused with the literal string "null".
		`{"ss":["a","b","c","d",null,"f"]}`,
		// The literal string "null" must not take the null sentinel path.
		`{"ss":["a","b","c","d","null","f"]}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want sliceStringBox
			runParity(t, in, &got, &want)
		})
	}
}

func TestSliceFloat(t *testing.T) {
	cases := []string{
		`{"fs":[3.14,2.718,1.0]}`,
		`{"fs":[0,-0.0,1e10,1e-10]}`,
		`{"fs":[]}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want sliceFloatBox
			runParity(t, in, &got, &want)
		})
	}
}

func TestSliceBool(t *testing.T) {
	cases := []string{
		`{"bs":[true,false,true]}`,
		`{"bs":[]}`,
		`{"bs":[true,true,false,false,true]}`, // grow
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want sliceBoolBox
			runParity(t, in, &got, &want)
		})
	}
}

func TestSliceMixed(t *testing.T) {
	cases := []string{
		`{"tag":"hello","xs":[1,2,3],"ss":["a","b"],"n":42}`,
		`{"xs":[],"ss":[],"tag":"","n":0}`,
		`{"tag":"both-grow","xs":[1,2,3,4,5,6,7,8,9],"ss":["a","b","c","d","e","f","g","h"],"n":1}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want sliceMixedBox
			runParity(t, in, &got, &want)
		})
	}
}

type sliceStructPoint struct {
	X int    `json:"x"`
	Y string `json:"y"`
}

type sliceStructBox struct {
	Tag string             `json:"tag"`
	Ps  []sliceStructPoint `json:"ps"`
}

func TestSliceOfStruct(t *testing.T) {
	cases := []string{
		`{"ps":[]}`,
		`{"ps":[{"x":1,"y":"a"}]}`,
		`{"ps":[{"x":1,"y":"a"},{"x":2,"y":"b"},{"x":3,"y":"c"}]}`,
		`{"ps":[{"x":1,"y":"a"},{"x":2,"y":"b"},{"x":3,"y":"c"},{"x":4,"y":"d"}]}`,
		`{"ps":[{"x":1,"y":"a"},{"x":2,"y":"b"},{"x":3,"y":"c"},{"x":4,"y":"d"},{"x":5,"y":"e"}]}`,
		// This case forces two grow steps so repeated rebasing is covered.
		`{"ps":[{"x":1,"y":"a"},{"x":2,"y":"b"},{"x":3,"y":"c"},{"x":4,"y":"d"},{"x":5,"y":"e"},{"x":6,"y":"f"},{"x":7,"y":"g"},{"x":8,"y":"h"},{"x":9,"y":"i"}]}`,
		// Missing fields must stay zeroed even when the struct slots are reused.
		`{"ps":[{"y":"first","x":7},{"x":-1},{"y":""}]}`,
		// Keep one element with escaped strings so slice growth and unescape coexist.
		`{"ps":[{"x":1,"y":"a\nb"},{"x":2,"y":"中文"}]}`,
		// Null struct elements remain intentionally disabled until that parity case is implemented.
		// `{"ps":[{"x":1,"y":"a"},null,{"x":3}]}`,
		// Keep one case where sibling fields appear after the slice payload.
		`{"ps":[{"x":1,"y":"a"},{"x":2,"y":"b"}],"tag":"after"}`,
		`{"tag":"before","ps":[{"x":1,"y":"a"}]}`,
		`{"tag":"","ps":[]}`,
		`{}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var got, want sliceStructBox
			runParity(t, in, &got, &want)
		})
	}
}
