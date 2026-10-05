package venc

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestStaleStackFrameState pins that every native VM stack push initializes
// the frame's state. The exec ctx lives in a pooled encodeState, so a stack
// slot carries whatever state the previous run left in it; the slots are
// poisoned directly here so the check does not depend on sync.Pool handing
// back the same object. A push that skips the init lets the nested SEQ read
// the stale ACTIVE bit as its own BUF_FULL resume marker and wrap the depth
// field on its unconditional close decrement.
//
// Compact only: under indent the skipped INDENT_INC drives indent_depth
// negative, VM_WRITE_INDENT then memcpys a negative length over the heap
// after the buffer, and once that overwrites the running g the signal
// handler faults on it again and again, so the process hangs instead of
// failing.
func TestStaleStackFrameState(t *testing.T) {
	type inner struct {
		S []int `json:"s"`
	}

	cases := []struct {
		name string
		v    any
	}{
		{"slice-begin", [][]int{{1}, {2}}},
		{"array-begin", [2][]int{{1}, {2}}},
		{"ptr-deref", struct{ P *inner }{P: &inner{S: []int{1, 2}}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := json.Marshal(c.v)
			if err != nil {
				t.Fatalf("std marshal: %v", err)
			}

			rv := reflect.ValueOf(c.v)
			box := reflect.New(rv.Type())
			box.Elem().Set(rv)

			es := acquireEncodeState()
			defer releaseEncodeState(es)
			for i := range es.vmCtx.Stack {
				es.vmCtx.Stack[i].State |= 0x07 // ACTIVE | WALK | PRESERVE_FIRST
			}

			if err := es.encodeTop(EncTypeInfoOf(rv.Type()), box.UnsafePointer()); err != nil {
				t.Fatalf("encode: %v", err)
			}
			if got := string(es.buf); got != string(want) {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}
