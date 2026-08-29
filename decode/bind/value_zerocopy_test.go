package bind

import (
	"errors"
	"testing"

	"github.com/velox-io/json/decode/dom"
)

// TestUnmarshalValueRejectsZeroCopy pins the navigation-only contract of
// zero-copy Values: binding would alias the caller's input buffer inside the
// destination, so both the root and any child sharing the doc are rejected
// before the machine runs.
func TestUnmarshalValueRejectsZeroCopy(t *testing.T) {
	v, err := dom.ParsePadded(dom.Pad([]byte(`{"s":"hello","n":[1,2]}`)), dom.WithZeroCopy())
	if err != nil {
		t.Fatalf("ParsePadded: %v", err)
	}

	var obj struct {
		S string `json:"s"`
	}
	if err := UnmarshalValue(v, &obj); !errors.Is(err, ErrZeroCopyValue) {
		t.Errorf("root: got %v, want ErrZeroCopyValue", err)
	}

	var nums []int
	if err := UnmarshalValue(v.Get("n"), &nums); !errors.Is(err, ErrZeroCopyValue) {
		t.Errorf("child: got %v, want ErrZeroCopyValue", err)
	}
}

// TestUnmarshalValueAcceptsCopyModeDom pins that the rejection is scoped to
// the zero-copy mode: a copy-mode DOM doc (no Src) binds normally.
func TestUnmarshalValueAcceptsCopyModeDom(t *testing.T) {
	v, err := dom.Parse([]byte(`{"s":"hello","e":"a\nb"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var obj struct {
		S string `json:"s"`
		E string `json:"e"`
	}
	if err := UnmarshalValue(v, &obj); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if obj.S != "hello" || obj.E != "a\nb" {
		t.Errorf("bound %+v", obj)
	}
}
