package tests

import (
	"fmt"
	"runtime"
	"sync"
	"testing"

	vjson "github.com/velox-io/json"
)

// Goroutine 0: SafetyItem (struct with *SafetyInner, map[string]string)
// Goroutine 1: SimpleStruct (struct with string, int fields)
// Goroutine 2: AnyMap (map[string]any)
// Goroutine 3: AnySlice ([]any)
// Goroutine 4: NestedStruct (struct with nested pointers)
// Goroutine 5: MixedMap (map[string]SpecificType)
// ... etc, cycling through type patterns
//
// This forces concurrent type building for multiple types simultaneously,
// maximizing the chance of a race in UniTypeOf / DecTypeInfoOf.
func TestBuildDiverseTypesRace(t *testing.T) {
	const (
		numG  = 24
		iters = 300
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

			// Each goroutine keeps results alive.
			results := make([]any, iters)

			for i := range iters {
				idx := gid*iters + i

				var err error
				switch gid % 6 {
				case 0: // SafetyItem (struct + *ptr + map)
					data, _ := safetyInput(idx)
					var s SafetyItem
					err = vjson.Unmarshal(data, &s)
					results[i] = s

				case 1: // map[string]string
					data := fmt.Appendf(nil, `{"k%d":"v%d\nescaped"}`, idx, idx)
					var m map[string]string
					err = vjson.Unmarshal(data, &m)
					results[i] = m

				case 2: // map[string]any
					data := fmt.Appendf(nil, `{"name":"item-%d\n","value":%d}`, idx, idx)
					var m map[string]any
					err = vjson.Unmarshal(data, &m)
					results[i] = m

				case 3: // []any
					data := fmt.Appendf(nil, `[%d,"text%d\n",true,null]`, idx, idx)
					var s []any
					err = vjson.Unmarshal(data, &s)
					results[i] = s

				case 4: // []struct with pointer fields
					type Item struct {
						ID    int64   `json:"id"`
						Name  string  `json:"name"`
						Price float64 `json:"price"`
					}
					type List struct {
						Items []Item `json:"items"`
					}
					data := fmt.Appendf(nil,
						`{"items":[{"id":%d,"name":"item-%d\ntext","price":%d.99}]}`,
						idx, idx, idx)
					var list List
					err = vjson.Unmarshal(data, &list)
					results[i] = list

				case 5: // map[string]SafetyInner with escaped strings
					type MapInner struct {
						M map[string]SafetyInner `json:"m"`
					}
					data := fmt.Appendf(nil,
						`{"m":{"key%d":{"id":%d,"label":"L%d\nOK"}}}`,
						idx, idx, idx)
					var mi MapInner
					err = vjson.Unmarshal(data, &mi)
					results[i] = mi
				}

				if err != nil {
					errs <- fmt.Errorf("g%d/i%d: %w", gid, i, err)
					return
				}
			}
			_ = results
		}(g)
	}

	wg.Wait()
	close(stop)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
