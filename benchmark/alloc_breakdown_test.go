//go:build vbindstats

package benchmark

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/velox-io/json/decode/bind"
	"github.com/velox-io/json/vbind"
)

// TestAllocBreakdown runs N parses and reports per-SlotClass call counts for
// the four yield-driven allocator entry points. Build with -tags vbindstats.
//
// Usage:
//
//	go test -tags=vbindstats -run='TestAllocBreakdown' -v ./benchmark
func TestAllocBreakdown(t *testing.T) {
	t.Run("KubePodList", func(t *testing.T) { runAllocBreakdown(t, reflect.TypeOf(KubePodList{}), KubePodsJSON) })
	t.Run("MapAny", func(t *testing.T) { runAllocBreakdown(t, reflect.TypeOf(map[string]any{}), KubePodsJSON) })
}

func runAllocBreakdown(t *testing.T, rtype reflect.Type, data []byte) {
	vbind.SetStats(true)
	defer vbind.SetStats(false)

	p, err := bind.NewParserForType(rtype)
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	const warm = 200
	for i := 0; i < warm; i++ {
		v := reflect.New(rtype).Interface()
		if err := p.Unmarshal(data, v); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
	}

	vbind.ResetStats()

	const N = 1000
	for i := 0; i < N; i++ {
		v := reflect.New(rtype).Interface()
		if err := p.Unmarshal(data, v); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
	}

	isSlice := vbind.IsSliceSnapshot()
	isMap := vbind.IsMapSnapshot()
	esz, _, _, _, _ := vbind.StatsSnapshot()
	muBlock, cap, limit, off := p.FinalSlotState()
	sliceGrow, newBlock, recRefill, recBypass := vbind.YieldCounts()

	fmt.Println()
	fmt.Printf("=== %s: after %d parses (warmup %d) ===\n", rtype, N, warm)
	fmt.Printf("%5s %4s %6s %12s %12s %12s %12s %10s %10s %10s %10s\n",
		"idx", "kind", "esz", "sliceGrow", "newBlock", "recRefill", "recBypass",
		"muBlock", "cap", "limit", "offset")
	fmt.Printf("%5s %4s %6s %12s %12s %12s %12s %10s %10s %10s %10s\n",
		"----", "----", "----", "---------", "--------", "---------", "---------",
		"-------", "---", "-----", "------")
	var totSG, totNB, totRF, totBP uint64
	for i := 0; i < len(esz); i++ {
		kind := "ptr"
		if i < len(isSlice) && isSlice[i] {
			kind = "sli"
		} else if i < len(isMap) && isMap[i] {
			kind = "map"
		}
		sg, nb, rf, bp := uint64(0), uint64(0), uint64(0), uint64(0)
		if i < len(sliceGrow) {
			sg = sliceGrow[i]
		}
		if i < len(newBlock) {
			nb = newBlock[i]
		}
		if i < len(recRefill) {
			rf = recRefill[i]
		}
		if i < len(recBypass) {
			bp = recBypass[i]
		}
		if sg == 0 && nb == 0 && rf == 0 && bp == 0 {
			continue
		}
		mb, cp, lm, of := uint32(0), uint32(0), uint32(0), uint32(0)
		if i < len(muBlock) {
			mb, cp, lm, of = muBlock[i], cap[i], limit[i], off[i]
		}
		fmt.Printf("%5d %4s %6d %12d %12d %12d %12d %10d %10d %10d %10d\n",
			i, kind, esz[i], sg, nb, rf, bp, mb, cp, lm, of)
		totSG += sg
		totNB += nb
		totRF += rf
		totBP += bp
	}
	fmt.Printf("%5s %4s %6s %12d %12d %12d %12d\n", "TOT", "", "", totSG, totNB, totRF, totBP)
	fmt.Printf("\nper-op:\n  sliceGrow=%.3f  newBlock=%.3f  recRefill=%.3f  recBypass=%.3f\n",
		float64(totSG)/float64(N), float64(totNB)/float64(N),
		float64(totRF)/float64(N), float64(totBP)/float64(N))
	fmt.Printf("  total SlotClass-yielding calls/op = %.3f\n",
		float64(totSG+totNB+totRF+totBP)/float64(N))
}
