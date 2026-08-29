package bind

import (
	"testing"

	"github.com/velox-io/json/internal/valueabi"
)

// The bind tape producer applies the same escape-free gate as dom's copy mode:
// bodies that decode without a single escape are tagged TagStrFree and
// re-forward verbatim; escaped bodies stay TagString. UnmarshalValue leg of the
// tag contract (dom.Parse is covered by decode/dom/strfree_test.go).
func TestStrFree_BindProducer(t *testing.T) {
	v := mustUnmarshalValue(t, `{"s":"hello","esc":"a\nb"}`)

	desc := valueDescriptor(&v)
	if got, want := desc.TagAt(0), byte(valueabi.TagObjBeg); got != want {
		t.Fatalf("root tag=%q, want %q", got, want)
	}
	if tag := desc.TagAt(1); tag != valueabi.TagStrFree {
		t.Errorf("key 's' tag=%q, want TagStrFree", tag)
	}
	if tag := desc.TagAt(2); tag != valueabi.TagStrFree {
		t.Errorf("value 'hello' tag=%q, want TagStrFree", tag)
	}
	escKey := 3
	if tag := desc.TagAt(escKey); tag != valueabi.TagStrFree {
		t.Errorf("key 'esc' tag=%q, want TagStrFree", tag)
	}
	if tag := desc.TagAt(escKey + 1); tag != valueabi.TagString {
		t.Errorf("value 'a\\nb' tag=%q, want TagString", tag)
	}

	if got, want := v.String(), `{"s":"hello","esc":"a\nb"}`; got != want {
		t.Errorf("String()=%s, want %s", got, want)
	}
}
