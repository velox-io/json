//go:build !vdec

package stream_test

import (
	"strings"
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/vbind"
)

// Variant case target that directly contains a stream.Stream[T] field. This
// must be rejected at build time: a variant case cannot host a stream field.
type streamInVariantCase struct {
	Events stream.Stream[simpleEvt] `json:"events"`
	Name   string                   `json:"name"`
}

type simpleEvt struct {
	ID string `json:"id"`
}

type variantHostWithStreamCase struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

// TestVariantCaseRejectsStreamField verifies the one-directional constraint:
// a variant case whose target type directly contains a stream.Stream[T]
// field is rejected at build time (when Decode triggers the TypeTree build).
func TestVariantCaseRejectsStreamField(t *testing.T) {
	vbind.DefineVariantCases[variantHostWithStreamCase, struct {
		_ streamInVariantCase `case:"bad"`
	}]()

	var v variantHostWithStreamCase
	err := vjson.Unmarshal([]byte(`{"type":"bad","data":{"name":"x"}}`), &v)
	if err == nil {
		t.Fatal("Decode accepted a variant case containing a stream field; expected build error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "stream") {
		t.Errorf("Decode err = %v, want substring 'stream'", err)
	}
}

// TestStreamElementAllowsVariantField verifies the reverse direction: a
// stream.Stream[T] whose element type T contains a variant field is legal.
// The stream package cannot import vbind to construct a variant descriptor,
// so this test is a build-only check: it declares the types and would
// register a variant on the element type if vbind were imported. The test
// exists to lock the one-directional constraint in documentation and catch
// regressions if the build check is ever tightened to reject the reverse
// direction.
type elementWithVariant struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
	Name string `json:"name"`
}

type elementWithVariantUser struct {
	Name string `json:"name"`
}

type streamOfVariantElements struct {
	Items stream.Stream[elementWithVariant] `json:"items"`
}

func TestStreamElementAllowsVariantFieldBuild(t *testing.T) {
	// Register a variant descriptor on the element type so build succeeds.
	// This confirms the stream → variant direction is allowed: the stream
	// slice collects elementWithVariant, which itself hosts a variant field.
	vbind.DefineVariantCases[elementWithVariant, struct {
		_ elementWithVariantUser `case:"user"`
	}]()

	var v streamOfVariantElements
	v.Items.OnRead(func(s stream.Scope[elementWithVariant]) error {
		for range s.Iter() {
		}
		return nil
	})
	if err := vjson.Unmarshal([]byte(`{"items":[{"type":"user","data":{"name":"a"},"name":"x"}]}`), &v); err != nil {
		t.Fatalf("Decode: %v", err)
	}
}

// TestStreamElementAllowsNestedStreamField verifies that a stream element
// type that itself contains a stream.Stream[T] field is now accepted at build
// time: the per-element yield path (BIND_FLAG_ELEM_HAS_STREAM) lets Go
// register nested OnRead via Item.Target() before the element body binds.
type nestedStreamElement struct {
	Inner stream.Stream[simpleEvt] `json:"inner"`
}

type streamOfNestedStream struct {
	Items stream.Stream[nestedStreamElement] `json:"items"`
}

func TestStreamElementAllowsNestedStreamField(t *testing.T) {
	var v streamOfNestedStream
	v.Items.OnRead(func(s stream.Scope[nestedStreamElement]) error {
		for range s.Iter() {
		}
		return nil
	})
	// Empty array: build should succeed, no handler activation.
	if err := vjson.Unmarshal([]byte(`{"items":[]}`), &v); err != nil {
		t.Fatalf("Decode rejected nested stream element: %v", err)
	}
}
