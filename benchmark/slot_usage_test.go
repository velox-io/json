//go:build vbindstats

package benchmark

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/velox-io/json/decode/bind"
	"github.com/velox-io/json/vbind"
)

// TestSlotFinalState runs N parses then dumps per-SlotClass final Batch /
// Offset plus the stats-module derived metrics (growBytes, wasteSlots, etc.)
// to verify the instrumentation captures raw facts and computes derived
// metrics without the allocator making policy judgements.
func TestSlotFinalState(t *testing.T) {
	vbind.SetStats(true)
	defer vbind.SetStats(false)

	p, err := bind.NewParserForType(reflect.TypeOf(KubePodList{}))
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	const N = 100
	for i := 0; i < N; i++ {
		var pl KubePodList
		if err := p.Unmarshal(KubePodsJSON, &pl); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
	}

	esz, _, grow, waste, gb := vbind.StatsSnapshot()
	fb := vbind.FinalBatchSnapshot()
	off := p.FinalOffsets()
	isSlice := vbind.IsSliceSnapshot()
	isMap := vbind.IsMapSnapshot()

	fmt.Println()
	fmt.Printf("=== After %d parses: per-SlotClass state (raw facts + derived metrics) ===\n", N)
	fmt.Printf("%5s %4s %6s %8s %8s %10s %12s %10s %10s\n",
		"idx", "kind", "esz", "batch", "offset", "growCalls", "growBytes", "wasteSlots", "goBump")
	fmt.Printf("%5s %4s %6s %8s %8s %10s %12s %10s %10s\n",
		"----", "----", "----", "-----", "------", "--------", "--------", "---------", "------")
	for i := 0; i < len(esz); i++ {
		kind := "ptr"
		if i < len(isSlice) && isSlice[i] {
			kind = "sli"
		} else if i < len(isMap) && isMap[i] {
			kind = "map"
		}
		var bump uint64
		if i < len(globalStatsBumpSnapshot()) {
			bump = globalStatsBumpSnapshot()[i]
		}
		fmt.Printf("%5d %4s %6d %8d %8d %10d %12d %10d %10d\n",
			i, kind, esz[i], fb[i], off[i], grow[i], gb[i], waste[i], bump)
	}
	fmt.Println()
	fmt.Print(vbind.FormatStats())
}

// globalStatsBumpSnapshot is a placeholder for the bump counter accessor; we
// expose it via the package's StatsSnapshot return tuple, but for the per-
// class bump column we fetch from a second StatsSnapshot call.
func globalStatsBumpSnapshot() []uint64 {
	_, bump, _, _, _ := vbind.StatsSnapshot()
	return bump
}
