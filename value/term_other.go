//go:build !(darwin || linux)

package value

// terminalCols is the non-unix fallback: terminal width is not detectable here
// without platform-specific console APIs, so callers fall back to the default
// cell count.
func terminalCols() int { return 0 }
