package gort

import (
	"reflect"
	"testing"
	"unsafe"
)

// readMapDirLen reads the runtime-internal Map.dirLen field via known offset.
// Map layout (64-bit, internal/runtime/maps.Map):
//
//	used        uint64    off 0
//	seed        uintptr   off 8
//	dirPtr      uintptr   off 16
//	dirLen      int       off 24
//	globalDepth uint8     off 32
func readMapDirLen(mp unsafe.Pointer) int {
	return *(*int)(unsafe.Add(mp, 24))
}

// TestMapPresize_AllocatesBuckets creates an empty map via MakeMap(nil),
// then calls MapPresize(hint=256) and verifies the directory was allocated.
func TestMapPresize_AllocatesBuckets(t *testing.T) {
	mt := TypePtr(reflect.TypeFor[map[string]int]())

	// Create empty map: MakeMap(t, 0, nil).
	mp := MakeMap(mt, 0, nil)
	if mp == nil {
		t.Fatal("MakeMap returned nil")
	}
	if readMapDirLen(mp) != 0 {
		t.Fatalf("empty make(map,0): dirLen=%d, want 0", readMapDirLen(mp))
	}

	// Presize to 256 entries. Should allocate directory (dirLen >= 1).
	MapPresize(mt, 256, mp)

	if readMapDirLen(mp) < 1 {
		t.Fatalf("after presize(256): dirLen=%d, want >= 1", readMapDirLen(mp))
	}

	// Verify mapassign works and entries land correctly.
	for i := 0; i < 256; i++ {
		key := "key_" + itoa(i)
		slot := MapAssignFastStr(mt, mp, key)
		*(*int)(slot) = i
	}
	if MapLen(mp) != 256 {
		t.Fatalf("after 256 assigns: len=%d, want 256", MapLen(mp))
	}

	// Spot-check.
	for _, i := range []int{0, 1, 127, 128, 255} {
		key := "key_" + itoa(i)
		slot := MapAssignFastStr(mt, mp, key)
		if v := *(*int)(slot); v != i {
			t.Fatalf("m[%s] = %d, want %d", key, v, i)
		}
	}
}

// TestMapPresize_SmallHintNoop verifies hint <= 8 is a no-op.
func TestMapPresize_SmallHintNoop(t *testing.T) {
	mt := TypePtr(reflect.TypeFor[map[string]int]())

	mp := MakeMap(mt, 0, nil)
	dirLenBefore := readMapDirLen(mp)

	MapPresize(mt, 8, mp) // hint == MapGroupSlots

	dirLenAfter := readMapDirLen(mp)
	if dirLenAfter != dirLenBefore {
		t.Fatalf("hint=8 changed dirLen: before=%d after=%d (expected no-op)",
			dirLenBefore, dirLenAfter)
	}

	// Still usable.
	slot := MapAssignFastStr(mt, mp, "k")
	*(*int)(slot) = 1
	if MapLen(mp) != 1 {
		t.Fatalf("assign after small-hint presize: len=%d, want 1", MapLen(mp))
	}
}

// TestMapPresize_NilMapNoop verifies nil m is a no-op (no crash).
func TestMapPresize_NilMapNoop(t *testing.T) {
	mt := TypePtr(reflect.TypeFor[map[string]int]())
	MapPresize(mt, 256, nil) // must not crash
}

// TestMapPresize_PointerStable verifies the *hmap pointer is unchanged
// after presize. makemap(m != nil) reuses the Map struct.
func TestMapPresize_PointerStable(t *testing.T) {
	mt := TypePtr(reflect.TypeFor[map[string]int]())

	// Create via MakeMap(nil), then presize in place.
	mp := MakeMap(mt, 0, nil)
	before := mp

	MapPresize(mt, 256, mp)

	if mp != before {
		t.Fatalf("presize changed pointer: before=%p after=%p", before, mp)
	}

	// Verify mapassign still uses the same pointer.
	slot := MapAssignFastStr(mt, mp, "x")
	*(*int)(slot) = 42
	if MapLen(mp) != 1 {
		t.Fatalf("assign after presize: len=%d, want 1", MapLen(mp))
	}
}

// TestMapPresize_NonEmptyNoop verifies presize on a non-empty map is a
// no-op. Without this guard, makemap would rewrite seed/dirPtr and orphan
// existing entries.
func TestMapPresize_NonEmptyNoop(t *testing.T) {
	mt := TypePtr(reflect.TypeFor[map[string]int]())

	mp := MakeMap(mt, 0, nil)
	slot := MapAssignFastStr(mt, mp, "existing")
	*(*int)(slot) = 99
	dirLenBefore := readMapDirLen(mp)

	MapPresize(mt, 256, mp) // non-empty: must no-op

	dirLenAfter := readMapDirLen(mp)
	if dirLenAfter != dirLenBefore {
		t.Fatalf("non-empty presize changed dirLen: before=%d after=%d (should be no-op)",
			dirLenBefore, dirLenAfter)
	}

	// Existing entry still intact.
	slot2 := MapAssignFastStr(mt, mp, "existing")
	if v := *(*int)(slot2); v != 99 {
		t.Fatalf("existing entry corrupted: got %d, want 99", v)
	}
	if MapLen(mp) != 1 {
		t.Fatalf("len after non-empty presize: got %d, want 1", MapLen(mp))
	}
}

// itoa is a minimal int-to-string to avoid pulling in strconv for the test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
