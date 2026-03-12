package vjson

import (
	stdjson "encoding/json"
	"testing"
)

type (
	GolangRoot struct {
		Tree     *GolangNode `json:"tree"`
		Username string      `json:"username"`
	}
	GolangNode struct {
		Name     string       `json:"name"`
		Kids     []GolangNode `json:"kids"`
		CLWeight float64      `json:"cl_weight"`
		Touches  int          `json:"touches"`
		MinT     uint64       `json:"min_t"`
		MaxT     uint64       `json:"max_t"`
		MeanT    uint64       `json:"mean_t"`
	}
)

func makeGolangRootFixture() GolangRoot {
	return GolangRoot{
		Tree: &GolangNode{
			Name: "root",
			Kids: []GolangNode{
				{
					Name: "leaf-a",
					Kids: []GolangNode{
						{
							Name: "branch-a1",
							Kids: []GolangNode{
								{Name: "twig-a1-1", Kids: []GolangNode{}, CLWeight: 0.11, Touches: 1, MinT: 2, MaxT: 5, MeanT: 3},
							},
							CLWeight: 0.35,
							Touches:  6,
							MinT:     12,
							MaxT:     24,
							MeanT:    18,
						},
						{Name: "branch-a2", Kids: []GolangNode{}, CLWeight: 0.45, Touches: 7, MinT: 14, MaxT: 28, MeanT: 20},
					},
					CLWeight: 0.25,
					Touches:  3,
					MinT:     10,
					MaxT:     20,
					MeanT:    15,
				},
			},
			CLWeight: 1.5,
			Touches:  10,
			MinT:     100,
			MaxT:     500,
			MeanT:    300,
		},
		Username: "alice",
	}
}

func TestMarshal_GolangRoot(t *testing.T) {
	v := makeGolangRootFixture()

	got, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}

	want, err := stdjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
	t.Logf("got:%s", got)
}

func BenchmarkMarshal_GolangRoot(b *testing.B) {
	v := makeGolangRootFixture()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(&v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_GolangRoot_StdJSON(b *testing.B) {
	v := makeGolangRootFixture()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := stdjson.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_GolangRoot_Compare(b *testing.B) {
	v := makeGolangRootFixture()

	b.Run("vjson", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := Marshal(&v); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("stdjson", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := stdjson.Marshal(v); err != nil {
				b.Fatal(err)
			}
		}
	})
}
