//go:build vjpgoinstr

// TestMain for instrumentation-PGO collection: runs the benchmarks, then
// flushes the LLVM instrumentation counters (see pgo_instr_flush.go for why
// the explicit flush is required). Guarded by the `vjpgoinstr` build tag.
package benchmark

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	vjProfileFlush() // must run before os.Exit (Go does not run C atexit)
	os.Exit(code)
}
