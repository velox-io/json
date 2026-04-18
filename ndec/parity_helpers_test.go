package ndec

import (
	"encoding/json"
	"reflect"
	"testing"
)

// runParity compares ndec against encoding/json using zero-initialized
// destinations of the same concrete type.
func runParity(t *testing.T, input string, got, want any) {
	t.Helper()
	if err := Unmarshal([]byte(input), got); err != nil {
		t.Fatalf("ndec.Unmarshal(%q): %v", input, err)
	}
	if err := json.Unmarshal([]byte(input), want); err != nil {
		t.Fatalf("encoding/json.Unmarshal(%q): %v", input, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parity drift on %q:\n ndec   = %+v\n stdlib = %+v", input, got, want)
	}
}
