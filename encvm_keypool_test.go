package vjson

import (
	"encoding/json"
	"testing"

	"github.com/velox-io/json/native/encvm"
)

// Key Pool overflow tests
//
// These tests verify that when the global key pool (64KB uint16 address
// space) is full, the compiler gracefully falls back to Go-driven
// encoding for affected fields instead of panicking.

// saveAndInjectKeyPool saves the current key pool snapshot and replaces
// it with a near-full fake snapshot. Returns a restore function.
// The fake pool is filled to `used` bytes with dummy data, leaving
// only (65535 - used) bytes available.
func saveAndInjectKeyPool(used int) (restore func()) {
	return saveAndInjectKeyPoolWithKeys(used, nil)
}

// saveAndInjectKeyPoolWithKeys is like saveAndInjectKeyPool but also
// plants pre-existing keys into the fake pool's dedup index, so that
// subsequent addKey calls for those keys will be dedup hits (ok=true)
// even though the pool is otherwise full. Each key in `preload` must
// fit within the `used` region — their offsets are assigned sequentially
// starting at offset 0.
func saveAndInjectKeyPoolWithKeys(used int, preload [][]byte) (restore func()) {
	saved := globalKeyPool.current.Load()

	// Build a fake near-full snapshot.
	fakeData := make([]byte, used)
	for i := range fakeData {
		fakeData[i] = byte(i % 256)
	}
	fakeIdx := make(map[string]keyPoolEntry, len(preload))

	// Plant preloaded keys at sequential offsets within the fake data.
	// Copy their actual bytes so keyPoolBytes() returns valid data.
	off := 0
	for _, kb := range preload {
		if off+len(kb) > used {
			panic("saveAndInjectKeyPoolWithKeys: preloaded keys exceed 'used' region")
		}
		copy(fakeData[off:], kb)
		fakeIdx[string(kb)] = keyPoolEntry{off: uint16(off), len: uint8(len(kb))}
		off += len(kb)
	}

	globalKeyPool.current.Store(&keyPoolSnapshot{
		data: fakeData,
		idx:  fakeIdx,
	})

	return func() {
		globalKeyPool.current.Store(saved)
	}
}

// Layer 1: Unit test globalKeyPoolInsert directly

func TestGlobalKeyPoolInsert_OverflowReturnsFalse(t *testing.T) {
	// Fill the pool to 65530 bytes — only 5 bytes of headroom.
	restore := saveAndInjectKeyPool(65530)
	defer restore()

	// A 6-byte key should fail (65530 + 6 > 65535).
	key6 := []byte(`"x":  `) // 6 bytes
	_, _, ok := globalKeyPoolInsert(key6)
	if ok {
		t.Error("expected ok=false for key that would exceed pool capacity, got ok=true")
	}

	// A 5-byte key should succeed (65530 + 5 == 65535, which is <= 65535).
	key5 := []byte(`"x":`) // 4 bytes
	_, _, ok = globalKeyPoolInsert(key5)
	if !ok {
		t.Error("expected ok=true for key that fits exactly, got ok=false")
	}
}

func TestGlobalKeyPoolInsert_DedupHitSucceedsWhenFull(t *testing.T) {
	// Start with a pool that has some room, insert a key, then fill
	// the pool completely, then verify the dedup hit still works.
	restore := saveAndInjectKeyPool(65500)
	defer restore()

	// Insert a small key that fits.
	key := []byte(`"a":`)
	off, klen, ok := globalKeyPoolInsert(key)
	if !ok {
		t.Fatal("expected first insert to succeed")
	}
	if klen != uint8(len(key)) {
		t.Fatalf("klen = %d, want %d", klen, len(key))
	}

	// Now fill the pool to maximum.
	restore2 := saveAndInjectKeyPool(65535)
	// Manually add the key to the new full snapshot's index so dedup finds it.
	snap := globalKeyPool.current.Load()
	snap.idx[string(key)] = keyPoolEntry{off: off, len: klen}
	globalKeyPool.current.Store(snap) // re-store (same pointer, but idx updated)
	defer restore2()

	// Dedup hit: same key should succeed even though pool is full.
	off2, klen2, ok2 := globalKeyPoolInsert(key)
	if !ok2 {
		t.Error("expected dedup hit to return ok=true when pool is full")
	}
	if off2 != off || klen2 != klen {
		t.Errorf("dedup hit returned different values: off=%d/%d, klen=%d/%d", off2, off, klen2, klen)
	}
}

func TestGlobalKeyPoolInsert_EmptyKeyAlwaysSucceeds(t *testing.T) {
	restore := saveAndInjectKeyPool(65535)
	defer restore()

	off, klen, ok := globalKeyPoolInsert(nil)
	if !ok {
		t.Error("expected ok=true for empty key")
	}
	if off != 0 || klen != 0 {
		t.Errorf("expected (0, 0), got (%d, %d)", off, klen)
	}
}

// Layer 2: End-to-end Marshal test with pool overflow

// keyPoolOverflowStruct is a dedicated struct type used ONLY by the
// key pool overflow end-to-end test. Its field names are chosen to be
// unique — they won't appear in any other test's key pool entries.
// Since Blueprint compilation is cached per-type via sync.Once, this
// type must never be used elsewhere.
type keyPoolOverflowStruct struct {
	KpOvfAlpha   int     `json:"kp_ovf_alpha"`
	KpOvfBeta    string  `json:"kp_ovf_beta"`
	KpOvfGamma   float64 `json:"kp_ovf_gamma"`
	KpOvfDelta   bool    `json:"kp_ovf_delta"`
	KpOvfEpsilon int64   `json:"kp_ovf_epsilon"`
}

func TestKeyPoolOverflow_MarshalProducesCorrectJSON(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	// Inject a near-full pool so that the Blueprint compilation for
	// keyPoolOverflowStruct triggers addKey overflow on every field.
	// The key `"kp_ovf_alpha":` is 16 bytes, so even one field won't fit
	// when only 5 bytes of headroom remain.
	restore := saveAndInjectKeyPool(65530)
	defer restore()

	v := keyPoolOverflowStruct{
		KpOvfAlpha:   42,
		KpOvfBeta:    "hello",
		KpOvfGamma:   3.14,
		KpOvfDelta:   true,
		KpOvfEpsilon: 999,
	}

	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Compare with encoding/json — must produce identical output.
	want, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("output mismatch under key pool overflow:\n  vjson:  %s\n  stdlib: %s", got, want)
	}
}

// keyPoolOverflowMixedStruct has a mix of field types (nested struct,
// pointer, slice, map) to verify all emit* overflow paths.
type keyPoolOverflowInner struct {
	KpOvfInnerX int    `json:"kp_ovf_inner_x"`
	KpOvfInnerY string `json:"kp_ovf_inner_y"`
}

type keyPoolOverflowMixedStruct struct {
	KpOvfMxID    int                  `json:"kp_ovf_mx_id"`
	KpOvfMxName  *string              `json:"kp_ovf_mx_name"`
	KpOvfMxInner keyPoolOverflowInner `json:"kp_ovf_mx_inner"`
	KpOvfMxItems []int                `json:"kp_ovf_mx_items"`
	KpOvfMxTags  map[string]string    `json:"kp_ovf_mx_tags"`
	KpOvfMxIface any                  `json:"kp_ovf_mx_iface"`
	KpOvfMxTrail string               `json:"kp_ovf_mx_trail"`
}

func TestKeyPoolOverflow_MixedFieldTypes(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	restore := saveAndInjectKeyPool(65530)
	defer restore()

	name := "overflow-test"
	v := keyPoolOverflowMixedStruct{
		KpOvfMxID:    1,
		KpOvfMxName:  &name,
		KpOvfMxInner: keyPoolOverflowInner{KpOvfInnerX: 10, KpOvfInnerY: "y"},
		KpOvfMxItems: []int{1, 2, 3},
		KpOvfMxTags:  map[string]string{"a": "b"},
		KpOvfMxIface: "dynamic",
		KpOvfMxTrail: "end",
	}

	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	want, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Use unmarshal round-trip for comparison because map key order is non-deterministic.
	var gotObj, wantObj map[string]any
	if err := json.Unmarshal(got, &gotObj); err != nil {
		t.Fatalf("unmarshal got: %v\nraw: %s", err, got)
	}
	if err := json.Unmarshal(want, &wantObj); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}

	// Use JSON round-trip comparison (re-marshal both maps and compare).
	gotNorm, _ := json.Marshal(gotObj)
	wantNorm, _ := json.Marshal(wantObj)
	if string(gotNorm) != string(wantNorm) {
		t.Errorf("output mismatch under key pool overflow (mixed types):\n  vjson:  %s\n  stdlib: %s", got, want)
	}
}

// Layer 3: Partial overflow — some fields native, some fallback
//
// This tests the critical scenario where within a single struct's
// Blueprint, some fields have their keys already in the pool (dedup hit
// → native VM instruction) while other fields' keys can't fit (overflow
// → Go fallback). The resulting Blueprint interleaves native ops and
// OP_FALLBACK instructions, exercising the C→Go→C hot-resume path.

// keyPoolPartialStruct has fields whose key names are deliberately
// chosen: "id" and "name" will be pre-loaded into the pool as dedup
// hits; "kp_part_unique_xxx" fields are new keys that won't fit.
// Field ordering creates an interleaved pattern: native→fallback→native→fallback.
type keyPoolPartialStruct struct {
	ID            int     `json:"id"`                   // dedup hit → native
	KpPartUniqueA string  `json:"kp_part_unique_alpha"` // new key → overflow fallback
	Name          string  `json:"name"`                 // dedup hit → native
	KpPartUniqueB float64 `json:"kp_part_unique_beta"`  // new key → overflow fallback
	KpPartUniqueC bool    `json:"kp_part_unique_gamma"` // new key → overflow fallback
	Tag           string  `json:"tag"`                  // dedup hit → native
}

func TestKeyPoolOverflow_PartialOverflowInterleaved(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	// Pre-computed key bytes for shared fields (same format as encodeKeyBytes).
	keyID := []byte(`"id":`)
	keyName := []byte(`"name":`)
	keyTag := []byte(`"tag":`)

	// Total preloaded bytes: 5 + 7 + 6 = 18 bytes.
	// Set pool to 65530 used, with the first 18 bytes containing valid key data.
	// The unique fields' keys ("kp_part_unique_alpha": = 23 bytes) won't fit
	// in the remaining 5 bytes of headroom.
	restore := saveAndInjectKeyPoolWithKeys(65530, [][]byte{keyID, keyName, keyTag})
	defer restore()

	v := keyPoolPartialStruct{
		ID:            42,
		KpPartUniqueA: "fallback-value",
		Name:          "native-value",
		KpPartUniqueB: 2.718,
		KpPartUniqueC: true,
		Tag:           "also-native",
	}

	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	want, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("output mismatch under partial key pool overflow:\n  vjson:  %s\n  stdlib: %s", got, want)
	}
}
