package tests

import (
	"fmt"
	"runtime"
	"sync"
	"testing"

	vjson "github.com/velox-io/json"
)

func TestArenaDanglingPointer_Concurrent(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	const (
		numG  = 16
		iters = 150
	)
	var wg sync.WaitGroup
	errs := make(chan error, numG*2)
	for g := range numG {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			results := make([]S, iters)
			for i := range iters {
				x := makeStr(1792, 'x')
				want := fmt.Sprintf("item-%d-%d-%s\nok", gid, i, x)
				jsonData := fmt.Sprintf(`{"name":"item-%d-%d-%s\nok"}`, gid, i, x)
				if err := vjson.Unmarshal([]byte(jsonData), &results[i]); err != nil {
					errs <- fmt.Errorf("g%d/i%d: Unmarshal: %w", gid, i, err)
					return
				}
				if results[i].Name != want {
					errs <- fmt.Errorf("g%d/i%d: Name=%q want=%q", gid, i, results[i].Name, want)
					return
				}
				runtime.GC() // after every parse
			}
			for i := range iters {
				x := makeStr(1792, 'x')
				want := fmt.Sprintf("item-%d-%d-%s\nok", gid, i, x)
				if results[i].Name != want {
					errs <- fmt.Errorf("g%d/i%d: final Name=%q want=%q", gid, i, results[i].Name, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestArenaDanglingPointer_GCStress(t *testing.T) {
	type S struct {
		Name string `json:"name"`
	}
	const (
		numG  = 16
		iters = 150
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
				x := makeStr(1792, 'x')
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
				x := makeStr(1792, 'x')
				want := fmt.Sprintf("item-%d-%d-%s\nok", gid, i, x)
				if results[i].Name != want {
					errs <- fmt.Errorf("g%d/i%d: final mismatch", gid, i)
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

func makeStr(n int, c byte) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = c
	}
	return string(buf)
}
