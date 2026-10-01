package vlib

import (
	"bytes"
	"math/rand"
	"testing"
	"unsafe"

	"github.com/velox-io/json/native/vlib/internal/nativeref"
)

// buildNative builds through the C builder in the embedded blob, the
// reference for Build parity.
func buildNative(tb testing.TB, keys []string, tiers uint32) ([]byte, int32) {
	vkeys := make([]nativeref.Key, len(keys)+1)
	for i, k := range keys {
		vkeys[i] = nativeref.Key{Str: unsafe.StringData(k), Len: uintptr(len(k))}
	}
	scratch := make([]byte, nativeref.ScratchSize())
	cfg := nativeref.Config{
		Keys:        &vkeys[0],
		N:           uintptr(len(keys)),
		Tiers:       tiers,
		Scratch:     unsafe.Pointer(&scratch[0]),
		ScratchSize: uintptr(len(scratch)),
	}
	sz := nativeref.SizeFor(&cfg)
	buf := make([]byte, sz)
	if len(buf) < 8 {
		buf = make([]byte, 8)
	}
	rc := nativeref.Init(unsafe.Pointer(&buf[0]), uintptr(len(buf)), &cfg)
	if rc <= 0 {
		return nil, rc
	}
	return buf, rc
}

// compareParity builds one keys/tiers case through both builders and holds
// them to identical codes and bytes. It reports whether the build succeeded.
func compareParity(t *testing.T, keys []string, tiers uint32) bool {
	t.Helper()
	goBlob, goRC := Build(keys, tiers)
	natBlob, natRC := buildNative(t, keys, tiers)
	if goRC != natRC {
		t.Fatalf("keys=%q tiers=%#x: Go rc=%d, native rc=%d", keys, tiers, goRC, natRC)
	}
	if goRC <= 0 {
		return false
	}
	if !bytes.Equal(goBlob, natBlob) {
		i := 0
		for i < len(goBlob) && goBlob[i] == natBlob[i] {
			i++
		}
		t.Fatalf("keys=%q tiers=%#x: blob mismatch at offset %d (len go=%d native=%d)", keys, tiers, i, len(goBlob), len(natBlob))
	}
	return true
}

func TestBuild_ErrorCodes(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		tier uint32
		want int32
	}{
		{"empty set", nil, TiersAll, ErrKeysEmpty},
		{"empty key", []string{"a", ""}, TiersAll, ErrKeyEmpty},
		{"too many", make([]string, maxKeys+1), TiersAll, ErrKeysTooMany},
		{"invalid quote", []string{"a\"b"}, TiersAll, ErrKeyInvalidByte},
		{"invalid backslash", []string{"a\\b"}, TiersAll, ErrKeyInvalidByte},
		{"invalid nul", []string{"a\x00b"}, TiersAll, ErrKeyInvalidByte},
		{"duplicate", []string{"aa", "bb", "aa"}, TiersAll, ErrKeyDuplicate},
		{"too long", []string{string(bytes.Repeat([]byte{'a'}, keyStrideMax))}, TiersPerfect, ErrKeyTooLong},
	}
	for _, tc := range cases {
		if tc.name == "too many" {
			// The count check runs first, so the key contents stay
			// irrelevant for this case.
			for i := range tc.keys {
				tc.keys[i] = "k"
			}
		}
		_, rc := Build(tc.keys, tc.tier)
		if rc != tc.want {
			t.Errorf("%s: Build rc = %d, want %d", tc.name, rc, tc.want)
		}
		if !nativeref.Available {
			continue
		}
		_, nrc := buildNative(t, tc.keys, tc.tier)
		if nrc != rc {
			t.Errorf("%s: native rc = %d, Go rc = %d", tc.name, nrc, rc)
		}
	}
}

// TestBuild_TooLongWithTable keeps a >63-byte key buildable because TABLE
// remains a candidate.
func TestBuild_TooLongWithTable(t *testing.T) {
	keys := []string{string(bytes.Repeat([]byte{'a'}, keyStrideMax))}
	blob, rc := Build(keys, TiersAll)
	if rc != int32(TierTable) {
		t.Fatalf("Build rc = %d, want %d (TABLE)", rc, TierTable)
	}
	if !bytes.HasPrefix(blob, []byte{byte(TierTable), 0, 0, 0}) {
		t.Errorf("blob kind = %x, want TABLE", blob[:4])
	}
}

// TestBuild_ParityNative holds Build to byte-for-byte agreement with the C
// builder. Natural selection (TiersAll) covers the chosen tier, and each
// single-tier mask runs its builder directly, so every tier's construction is
// exercised even where the dispatcher would pick another tier first. Hand is
// covered through its mask: a random pair that survives gperf's position
// selection also agrees in the early bytes hand hashes, so natural selection
// lands on hand essentially never.
func TestBuild_ParityNative(t *testing.T) {
	if !nativeref.Available {
		t.Skip("native lookup not loaded on this platform")
	}
	alphabets := []string{
		"ab",
		"abcd",
		"abcdefgh",
		"abcdefghijklmnopqrstuvwxyz0123456789_.",
	}
	corpora := [][]string{
		{"name"},
		{"ab", "ax"},
		{"user", "product"},
		{"apiVersion", "kind", "namespace", "generation", "resourceVersion", "deleted", "selfLink", "replicas"},
		{"aaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb", "ccccccccccccccccccccccccccccccc"},
		{string(bytes.Repeat([]byte{'a'}, 70)), string(bytes.Repeat([]byte{'b'}, 71))},
	}
	rng := rand.New(rand.NewSource(1))
	for range 800 {
		n := 1 + rng.Intn(maxKeys)
		ab := alphabets[rng.Intn(len(alphabets))]
		sharedLen := 1 + rng.Intn(70)
		seen := make(map[string]bool, n)
		keys := make([]string, 0, n)
		for len(keys) < n {
			L := sharedLen
			if rng.Intn(2) == 0 {
				L = 1 + rng.Intn(70)
			}
			b := make([]byte, L)
			for i := range b {
				b[i] = ab[rng.Intn(len(ab))]
			}
			if rng.Intn(4) == 0 && len(keys) > 0 {
				// Shared prefixes stress the position selection.
				base := keys[rng.Intn(len(keys))]
				cut := min(len(base), L)
				copy(b, base[:cut])
			}
			s := string(b)
			if !seen[s] {
				seen[s] = true
				keys = append(keys, s)
			}
		}
		corpora = append(corpora, keys)
	}

	soloTiers := []uint32{TierWindow, TierGperf, TierHand, TierTable}
	natural := map[uint32]int{}
	solo := map[uint32]int{}
	for _, keys := range corpora {
		if compareParity(t, keys, TiersAll) {
			_, rc := Build(keys, TiersAll)
			natural[uint32(rc)]++
		}
		for _, tier := range soloTiers {
			// A single-tier mask may legitimately fail (a window that
			// cannot separate); parity still holds through the codes.
			if compareParity(t, keys, tier) {
				solo[tier]++
			}
		}
	}
	for _, tier := range soloTiers {
		if solo[tier] == 0 {
			t.Errorf("tier %d (%s) never built solo; parity coverage is incomplete", tier, TierName(tier))
		}
	}
	for _, tier := range soloTiers[:len(soloTiers)-1] {
		if tier == TierHand {
			continue
		}
		if natural[tier] == 0 {
			t.Errorf("tier %d (%s) never selected naturally; natural-selection parity is incomplete", tier, TierName(tier))
		}
	}
	t.Logf("coverage: natural window=%d gperf=%d hand=%d table=%d; solo window=%d gperf=%d hand=%d table=%d",
		natural[TierWindow], natural[TierGperf], natural[TierHand], natural[TierTable],
		solo[TierWindow], solo[TierGperf], solo[TierHand], solo[TierTable])
}
