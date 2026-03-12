package vjson

import (
	"testing"
)

// TestArenaReuse_EscapedStringCorruption verifies that strings decoded via the
// arena allocator are NOT corrupted when the Parser is reused from the pool.
//
// The arena stores unescaped string data (e.g. strings containing \n, \", \uXXXX).
// parserPool.Put resets arenaOff to 0 without releasing the underlying arenaData,
// so the next Unmarshal call may overwrite memory still referenced by the previous
// result's string fields.
func TestArenaReuse_EscapedStringCorruption(t *testing.T) {
	type Item struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	}

	// Both inputs use escaped characters to force the unescape path through
	// the arena (plain strings without escapes are zero-copy from src and
	// don't touch the arena).
	input1 := []byte(`{"name":"hello\nworld","desc":"foo\tbar"}`)
	input2 := []byte(`{"name":"AAAA\nBBBB","desc":"CCCC\tDDDD"}`)

	var item1 Item
	if err := Unmarshal(input1, &item1); err != nil {
		t.Fatal(err)
	}

	// Snapshot the values before the second Unmarshal reuses the parser.
	want1Name := "hello\nworld"
	want1Desc := "foo\tbar"

	if item1.Name != want1Name {
		t.Fatalf("item1.Name = %q, want %q", item1.Name, want1Name)
	}
	if item1.Desc != want1Desc {
		t.Fatalf("item1.Desc = %q, want %q", item1.Desc, want1Desc)
	}

	// Second unmarshal — if the parser reuses the same arena block, it will
	// overwrite the memory backing item1's strings.
	var item2 Item
	if err := Unmarshal(input2, &item2); err != nil {
		t.Fatal(err)
	}

	// Verify item2 is correct.
	want2Name := "AAAA\nBBBB"
	want2Desc := "CCCC\tDDDD"
	if item2.Name != want2Name {
		t.Fatalf("item2.Name = %q, want %q", item2.Name, want2Name)
	}
	if item2.Desc != want2Desc {
		t.Fatalf("item2.Desc = %q, want %q", item2.Desc, want2Desc)
	}

	// THE CRITICAL CHECK: item1's strings must still be intact.
	// If the arena was reused, item1.Name and item1.Desc will now contain
	// data from input2 (e.g. "AAAA\nBBBB" instead of "hello\nworld").
	if item1.Name != want1Name {
		t.Errorf("CORRUPTION: item1.Name changed from %q to %q after second Unmarshal",
			want1Name, item1.Name)
	}
	if item1.Desc != want1Desc {
		t.Errorf("CORRUPTION: item1.Desc changed from %q to %q after second Unmarshal",
			want1Desc, item1.Desc)
	}
}
