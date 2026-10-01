package vlib_test

import (
	"testing"
	"unsafe"

	vlib "github.com/velox-io/json/native/vlib"
	"github.com/velox-io/json/native/vlib/internal/nativeref"
)

// makeKeys pins Go strings and returns a []Key slice + retention slice.
// The C build path treats key.str as a (ptr, len) tuple and copies the bytes
// out during Init.
func makeKeys(strs []string) ([]nativeref.Key, []string) {
	keys := make([]nativeref.Key, len(strs))
	for i, s := range strs {
		keys[i] = nativeref.Key{
			Str: unsafe.StringData(s),
			Len: uintptr(len(s)),
		}
	}
	return keys, strs
}

func TestBuildWindow(t *testing.T) {
	keys, _ := makeKeys([]string{"apple", "banana", "cherry"})
	scratch := make([]byte, nativeref.ScratchSize())
	cfg := nativeref.Config{
		Keys:        &keys[0],
		N:           uintptr(len(keys)),
		Tiers:       vlib.TiersAll,
		Scratch:     unsafe.Pointer(&scratch[0]),
		ScratchSize: uintptr(len(scratch)),
	}
	sz := nativeref.SizeFor(&cfg)
	if sz == 0 {
		t.Fatal("SizeFor returned 0")
	}
	buf := make([]byte, sz)
	rc := nativeref.Init(unsafe.Pointer(&buf[0]), sz, &cfg)
	if rc <= 0 {
		t.Fatalf("Init failed: %d", rc)
	}
	tier := nativeref.GetTier(unsafe.Pointer(&buf[0]))
	if tier != uint32(rc) {
		t.Fatalf("tier mismatch: init=%d get=%d", rc, tier)
	}
	fp := nativeref.Footprint(unsafe.Pointer(&buf[0]))
	if fp == 0 || fp > sz {
		t.Fatalf("bad footprint: %d (size=%d)", fp, sz)
	}
	name := vlib.TierName(tier)
	if name == "none" || name == "" {
		t.Fatalf("bad tier name: %q", name)
	}
	t.Logf("tier=%s footprint=%d/%d", name, fp, sz)
}

func TestErrorPaths(t *testing.T) {
	// Empty keys.
	cfg := nativeref.Config{Keys: nil, N: 0, Tiers: vlib.TiersAll}
	if got := nativeref.SizeFor(&cfg); got != 0 {
		t.Fatalf("empty keys should return 0 size, got %d", got)
	}

	// Duplicate keys: SizeFor returns 0 (config invalid), Init reports the
	// specific error even given a small non-empty buffer.
	keys, _ := makeKeys([]string{"a", "a"})
	cfg = nativeref.Config{Keys: &keys[0], N: 2, Tiers: vlib.TiersAll}
	if got := nativeref.SizeFor(&cfg); got != 0 {
		t.Fatalf("SizeFor should return 0 for duplicate keys, got %d", got)
	}
	// Init still called against a scratch buffer; it will reject validate_keys.
	scratch := make([]byte, 4096)
	rc := nativeref.Init(unsafe.Pointer(&scratch[0]), uintptr(len(scratch)), &cfg)
	if rc != vlib.ErrKeyDuplicate {
		t.Fatalf("expected ErrKeyDuplicate (%d), got %d", vlib.ErrKeyDuplicate, rc)
	}
}

func TestTierName(t *testing.T) {
	cases := map[uint32]string{
		vlib.TierWindow: "window",
		vlib.TierGperf:  "gperf",
		vlib.TierHand:   "hand",
		vlib.TierTable:  "table",
		vlib.TierNone:   "none",
	}
	for tier, want := range cases {
		if got := vlib.TierName(tier); got != want {
			t.Errorf("TierName(%d) = %q, want %q", tier, got, want)
		}
	}
}
