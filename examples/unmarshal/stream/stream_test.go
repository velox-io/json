package stream

import "testing"

func TestStreamBasicIter(t *testing.T) {
	if err := StreamBasicIter(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamBreak(t *testing.T) {
	if err := StreamBreak(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamAllowValueReuse(t *testing.T) {
	if err := StreamAllowValueReuse(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamParallelSiblings(t *testing.T) {
	if err := StreamParallelSiblings(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamNested(t *testing.T) {
	if err := StreamNested(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamInnerBreakOuter(t *testing.T) {
	if err := StreamInnerBreakOuter(); err != nil {
		t.Fatal(err)
	}
}
