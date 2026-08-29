package bind

import (
	"testing"
	"time"

	"github.com/velox-io/json/vbind"
)

// An inline variant case with an empty struct target allocates from a
// zero-size SlotClass; the bump must never report it full (see
// Allocator.installBlock). Pins the BLOCK_FULL livelock fix.

type ecUser struct {
	ID int `json:"id"`
}

type ecEmpty struct{}

type ecHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

func init() {
	vbind.DefineVariantCases[ecHost, struct {
		_ ecUser  `case:"user"`
		_ ecEmpty `case:"empty"`
	}]()
}

func TestVariantInlineEmptyCaseDecode(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		var h ecHost
		done <- Unmarshal([]byte(`{"type":"empty"}`), &h)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HANG decoding empty inline case")
	}
}
