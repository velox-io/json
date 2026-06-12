package tests

import (
	"fmt"
	"runtime"
	"sync"
	"testing"

	vjson "github.com/velox-io/json"
)

// TestArenaPersistence_SequentialReuse verifies that arena-backed strings
// (from escaped JSON strings) survive parser pool reuse.
//
// This test proves that the current arena implementation is NOT causing
// data corruption in a sequential single-goroutine scenario.
// In a concurrent scenario, the same arena block may be abandoned
// while another goroutine's strings still reference it — that's the
// suspected crash mechanism.
func TestArenaPersistence_SequentialReuse(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	const N = 100
	results := make([]S, N)
	for i := range N {
		x := makeStr(3000, 'x')
		// JSON with \n escape sequence forces arena unescape path.
		jsonData := fmt.Sprintf(`{"name":"item-%d-%s\nok"}`, i, x)
		if err := vjson.Unmarshal([]byte(jsonData), &results[i]); err != nil {
			t.Fatalf("i=%d: %v", i, err)
		}
	}
	for range 10 {
		runtime.GC()
	}
	for i := range N {
		x := makeStr(3000, 'x')
		want := fmt.Sprintf("item-%d-%s\nok", i, x)
		if results[i].Name != want {
			t.Errorf("i=%d: Name=%q want=%q", i, results[i].Name, want)
		}
	}
}

// TestArenaPersistence_SequentialReuseKeepAlive keeps ALL intermediate
// results alive throughout and triggers GC between batches.
func TestArenaPersistence_SequentialReuseKeepAlive(t *testing.T) {
	const N = 200
	type S struct {
		Name string `json:"name"`
	}
	allResults := make([][]S, N)
	for i := range N {
		batch := make([]S, 50)
		for j := range 50 {
			idx := i*50 + j
			x := makeStr(3000, 'x')
			jsonData := fmt.Sprintf(`{"name":"item-%d-%s\nok"}`, idx, x)
			if err := vjson.Unmarshal([]byte(jsonData), &batch[j]); err != nil {
				t.Fatalf("i=%d,j=%d: %v", i, j, err)
			}
		}
		allResults[i] = batch
		runtime.GC()
	}
	for range 10 {
		runtime.GC()
	}
	for i := range N {
		for j := range 50 {
			idx := i*50 + j
			x := makeStr(3000, 'x')
			want := fmt.Sprintf("item-%d-%s\nok", idx, x)
			if allResults[i][j].Name != want {
				t.Errorf("i=%d,j=%d: Name=%q want=%q", i, j, allResults[i][j].Name, want)
				return
			}
		}
	}
}

// TestArenaPersistence_ConcurrentKeepAlive is the closest to the
// CI crash scenario: concurrent goroutines, result-keeping, GC pressure.
func TestArenaPersistence_ConcurrentKeepAlive(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	const (
		numG  = 16
		iters = 200
	)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, numG*2)
	for g := range numG {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			results := make([]S, iters)
			for i := range iters {
				x := makeStr(2000, 'x')
				want := fmt.Sprintf("item-%d-%d-%s\nok", gid, i, x)
				jsonData := fmt.Sprintf(`{"name":"item-%d-%d-%s\nok"}`, gid, i, x)
				if err := vjson.Unmarshal([]byte(jsonData), &results[i]); err != nil {
					errs <- fmt.Errorf("g%d/i%d: %w", gid, i, err)
					return
				}
				if results[i].Name != want {
					errs <- fmt.Errorf("g%d/i%d: mismatch", gid, i)
					return
				}
			}
			for i := range iters {
				x := makeStr(2000, 'x')
				want := fmt.Sprintf("item-%d-%d-%s\nok", gid, i, x)
				if results[i].Name != want {
					errs <- fmt.Errorf("g%d/i%d: post-check Name=%q want=%q", gid, i, results[i].Name, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
