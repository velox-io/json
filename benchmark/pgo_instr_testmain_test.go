//go:build vjpgoinstr

// TestMain for instrumentation-PGO collection: runs the benchmarks, then
// flushes the LLVM instrumentation counters.
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
