package bind

import (
	"testing"

	"github.com/velox-io/json/stream"
)

// TestRootStream exercises the root dispatch path that recognizes a Stream[T]
// root directly (no host struct wrapping the field). The input is a bare JSON
// array that the decoder streams into the root Stream.
func TestRootStream(t *testing.T) {
	type event struct {
		ID    string `json:"id"`
		Match bool   `json:"match"`
	}

	t.Run("decode-matching", func(t *testing.T) {
		data := []byte(`[
			{"id":"e1","match":true},
			{"id":"e2","match":false},
			{"id":"e3","match":true}
		]`)
		var s stream.Stream[event]
		var ids []string
		s.OnRead(func(sc stream.Scope[event]) error {
			for it := range sc.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				if it.Target().Match {
					ids = append(ids, it.Target().ID)
				}
			}
			return nil
		})
		if err := Unmarshal(data, &s); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		want := []string{"e1", "e3"}
		if len(ids) != len(want) {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
		for i := range want {
			if ids[i] != want[i] {
				t.Fatalf("ids = %v, want %v", ids, want)
			}
		}
	})

	t.Run("empty-array", func(t *testing.T) {
		data := []byte(`[]`)
		var s stream.Stream[event]
		activated := false
		s.OnRead(func(sc stream.Scope[event]) error {
			activated = true
			for range sc.Iter() {
				t.Error("unexpected item in empty root stream")
			}
			return nil
		})
		if err := Unmarshal(data, &s); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !activated {
			t.Error("handler not activated for empty root array")
		}
	})

	t.Run("early-break", func(t *testing.T) {
		data := []byte(`[
			{"id":"e1","match":false},
			{"id":"e2","match":true},
			{"id":"e3","match":false}
		]`)
		var s stream.Stream[event]
		var ids []string
		s.OnRead(func(sc stream.Scope[event]) error {
			for it := range sc.Iter() {
				if err := it.Decode(); err != nil {
					return err
				}
				if it.Target().Match {
					break
				}
				ids = append(ids, it.Target().ID)
			}
			return nil
		})
		if err := Unmarshal(data, &s); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		want := []string{"e1"}
		if len(ids) != len(want) || ids[0] != want[0] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	})

	t.Run("skip-remaining", func(t *testing.T) {
		data := []byte(`[
			{"id":"e1","match":true},
			{"id":"e2","match":false},
			{"id":"e3","match":true}
		]`)
		var s stream.Stream[event]
		count := 0
		s.OnRead(func(sc stream.Scope[event]) error {
			for it := range sc.Iter() {
				// Skip fast-forwards the rest of the array after the current
				// item, so the first Skip ends iteration.
				if err := it.Skip(); err != nil {
					return err
				}
				count++
			}
			return nil
		})
		if err := Unmarshal(data, &s); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if count != 1 {
			t.Fatalf("count = %d, want 1 (Skip ends iteration after current item)", count)
		}
	})
}
